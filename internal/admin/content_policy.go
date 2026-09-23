package admin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Moderation is low volume. Serialize the linked work/post resource graph so
// different reports cannot acquire its rows in opposite orders.
func lockModeration(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(484341490073)`)
	return err
}

func validContentDecision(reason string, confirm bool, version int) bool {
	n := utf8.RuneCountInString(strings.TrimSpace(reason))
	return confirm && version > 0 && n >= 10 && n <= 2000
}

func contentAudit(ctx context.Context, tx pgx.Tx, actor, id uuid.UUID, action, kind, reason, request string, before, after any) error {
	evidence, err := json.Marshal(map[string]any{"before": before, "after": after})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb)`, actor, action, kind, id, strings.TrimSpace(reason), request, evidence)
	return err
}

type moderatedResource struct {
	Kind string
	ID   uuid.UUID
}

func moderationTargets(ctx context.Context, tx pgx.Tx, kind string, id uuid.UUID) ([]moderatedResource, error) {
	targets := []moderatedResource{{kind, id}}
	if kind == "post" {
		var work *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT work_id FROM posts WHERE id=$1`, id).Scan(&work); err != nil {
			return nil, err
		}
		if work != nil {
			targets = append(targets, moderatedResource{"work", *work})
		}
	} else if kind == "work" {
		rows, err := tx.Query(ctx, `SELECT id FROM posts WHERE work_id=$1 ORDER BY id`, id)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var post uuid.UUID
			if err = rows.Scan(&post); err != nil {
				return nil, err
			}
			targets = append(targets, moderatedResource{"post", post})
		}
		if err = rows.Err(); err != nil {
			return nil, err
		}
	} else if kind != "comment" {
		return nil, ErrInvalid
	}
	return targets, nil
}

func contentTable(kind string) (string, error) {
	switch kind {
	case "post":
		return "posts", nil
	case "work":
		return "works", nil
	case "comment":
		return "comments", nil
	}
	return "", ErrInvalid
}

func contentState(ctx context.Context, tx pgx.Tx, r moderatedResource) (string, int, error) {
	table, err := contentTable(r.Kind)
	if err != nil {
		return "", 0, err
	}
	var status string
	var version int
	err = tx.QueryRow(ctx, `SELECT status,version FROM `+table+` WHERE id=$1 FOR UPDATE`, r.ID).Scan(&status, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return status, version, err
}

func canRestoreContent(ctx context.Context, tx pgx.Tx, r moderatedResource) error {
	var eligible bool
	var query string
	switch r.Kind {
	case "post":
		query = `SELECT EXISTS(SELECT 1 FROM posts p JOIN users u ON u.id=p.author_id LEFT JOIN works w ON w.id=p.work_id LEFT JOIN assets a ON a.id=w.asset_id WHERE p.id=$1 AND NOT p.owner_removed AND u.status='active' AND (p.work_id IS NULL OR a.scan_status='clean'))`
	case "work":
		query = `SELECT EXISTS(SELECT 1 FROM works w JOIN users u ON u.id=w.author_id JOIN assets a ON a.id=w.asset_id WHERE w.id=$1 AND u.status='active' AND a.scan_status='clean' AND NOT EXISTS(SELECT 1 FROM posts p WHERE p.work_id=w.id AND p.owner_removed))`
	case "comment":
		query = `SELECT EXISTS(SELECT 1 FROM comments c JOIN users u ON u.id=c.author_id WHERE c.id=$1 AND u.status='active' AND EXISTS(SELECT 1 FROM community_visible_posts p WHERE p.id=c.post_id))`
	default:
		return ErrInvalid
	}
	if err := tx.QueryRow(ctx, query, r.ID).Scan(&eligible); err != nil {
		return err
	}
	if !eligible {
		return ErrConflict
	}
	return nil
}

func setContentState(ctx context.Context, tx pgx.Tx, r moderatedResource, status string) (int, error) {
	table, err := contentTable(r.Kind)
	if err != nil {
		return 0, err
	}
	if status == "published" {
		if err = canRestoreContent(ctx, tx, r); err != nil {
			return 0, err
		}
	}
	var version int
	assignment := `status=$2,updated_at=now()`
	if r.Kind == "comment" {
		assignment = `status=$2`
	}
	err = tx.QueryRow(ctx, `UPDATE `+table+` SET `+assignment+` WHERE id=$1 RETURNING version`, r.ID, status).Scan(&version)
	return version, err
}

// Each case contributes an independent hold. Removing one hold must retain all
// other active decisions and may never overwrite an unrelated newer mutation.
func applyReportHold(ctx context.Context, tx pgx.Tx, report uuid.UUID, kind string, id uuid.UUID, status string) (string, error) {
	targets, err := moderationTargets(ctx, tx, kind, id)
	if err != nil {
		return "", err
	}
	var previous string
	for index, r := range targets {
		current, version, err := contentState(ctx, tx, r)
		if err != nil {
			return "", err
		}
		if index == 0 {
			previous = current
		}
		if current == "draft" {
			return "", ErrConflict
		}
		var stored int
		err = tx.QueryRow(ctx, `SELECT applied_version FROM content_moderation_resources WHERE resource_type=$1 AND resource_id=$2 FOR UPDATE`, r.Kind, r.ID).Scan(&stored)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = tx.Exec(ctx, `INSERT INTO content_moderation_resources(resource_type,resource_id,base_status,applied_version) VALUES($1,$2,$3,$4)`, r.Kind, r.ID, current, version)
		} else if err == nil && stored != version {
			return "", ErrConflict
		}
		if err != nil {
			return "", err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO content_moderation_holds(report_id,resource_type,resource_id,status) VALUES($1,$2,$3,$4)`, report, r.Kind, r.ID, status); err != nil {
			return "", err
		}
		if err = reconcileHolds(ctx, tx, r); err != nil {
			return "", err
		}
	}
	return previous, nil
}

func reconcileHolds(ctx context.Context, tx pgx.Tx, r moderatedResource) error {
	current, version, err := contentState(ctx, tx, r)
	if err != nil {
		return err
	}
	var base string
	var stored int
	if err = tx.QueryRow(ctx, `SELECT base_status,applied_version FROM content_moderation_resources WHERE resource_type=$1 AND resource_id=$2 FOR UPDATE`, r.Kind, r.ID).Scan(&base, &stored); err != nil {
		return err
	}
	if version != stored {
		return ErrConflict
	}
	var count int
	var strongest *string
	if err = tx.QueryRow(ctx, `SELECT count(*),max(status) FROM content_moderation_holds WHERE resource_type=$1 AND resource_id=$2`, r.Kind, r.ID).Scan(&count, &strongest); err != nil {
		return err
	}
	next := base
	if strongest != nil {
		next = *strongest
		if base == "removed" {
			next = base
		}
	}
	if current != next {
		version, err = setContentState(ctx, tx, r, next)
		if err != nil {
			return err
		}
	}
	if count == 0 {
		_, err = tx.Exec(ctx, `DELETE FROM content_moderation_resources WHERE resource_type=$1 AND resource_id=$2`, r.Kind, r.ID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE content_moderation_resources SET applied_version=$3 WHERE resource_type=$1 AND resource_id=$2`, r.Kind, r.ID, version)
	}
	return err
}

func releaseReportHolds(ctx context.Context, tx pgx.Tx, report uuid.UUID) error {
	rows, err := tx.Query(ctx, `SELECT resource_type,resource_id FROM content_moderation_holds WHERE report_id=$1 ORDER BY resource_type,resource_id`, report)
	if err != nil {
		return err
	}
	var targets []moderatedResource
	for rows.Next() {
		var r moderatedResource
		if err = rows.Scan(&r.Kind, &r.ID); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Historic cases without a versioned hold require an explicit content review.
	if len(targets) == 0 {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `DELETE FROM content_moderation_holds WHERE report_id=$1`, report); err != nil {
		return err
	}
	for _, r := range targets {
		if err = reconcileHolds(ctx, tx, r); err != nil {
			return err
		}
	}
	return nil
}
