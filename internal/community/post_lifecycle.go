package community

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
	"github.com/jackc/pgx/v5"
)

type PostUpdateInput struct {
	PostCreateInput
	ExpectedVersion int `json:"expectedVersion"`
}

func validPostInput(input PostCreateInput) bool {
	return textLength(input.Title) <= 120 && textLength(input.Body) <= 2000 && (input.Draft || (textLength(input.Title) >= 3 && textLength(input.Body) >= 2))
}
func postInputStatus(input PostCreateInput) string {
	if input.Draft {
		return "draft"
	}
	return "published"
}

func (r *Repository) GetOwnedPost(ctx context.Context, actor, id uuid.UUID) (Post, error) {
	var item Post
	var position int64
	err := r.pool.QueryRow(ctx, postProjection+` FROM posts p JOIN users u ON u.id=p.author_id
 LEFT JOIN works w ON w.id=p.work_id LEFT JOIN assets a ON a.id=w.asset_id CROSS JOIN (SELECT 0::bigint AS position) i
 WHERE p.id=$1 AND p.author_id=$2 AND NOT p.owner_removed`, id, actor).Scan(&item.ID, &item.Category, &item.Title, &item.Body, &item.PublishedAt, &item.WorkID, &item.WorkTitle, &item.MediaURL, &item.MediaKind, &item.AIDisclosure, &item.AuthorID, &item.AuthorHandle, &item.AuthorName, &item.CommentCount, &item.LikeCount, &item.BookmarkCount, &item.ViewerLiked, &item.ViewerBookmarked, &item.ViewerFollowing, &item.Status, &item.Version, &position)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return item, err
}

func (r *Repository) UpdatePost(ctx context.Context, actor, id uuid.UUID, input PostUpdateInput, requestIDs ...string) (Post, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Body = strings.TrimSpace(input.Body)
	if input.ExpectedVersion < 1 || !validPostInput(input.PostCreateInput) {
		return Post{}, ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Post{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var version int
	var status string
	var work *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT version,status,work_id FROM posts WHERE id=$1 AND author_id=$2 AND NOT owner_removed FOR UPDATE`, id, actor).Scan(&version, &status, &work)
	if errors.Is(err, pgx.ErrNoRows) {
		return Post{}, ErrNotFound
	}
	if err != nil {
		return Post{}, err
	}
	if version != input.ExpectedVersion || work != nil || !oneOf(status, "draft", "published") || (status == "published" && input.Draft) {
		return Post{}, ErrConflict
	}
	if !input.Draft {
		if err = systemsettings.RequireTx(ctx, tx, systemsettings.Publishing); err != nil {
			return Post{}, err
		}
	}
	if err = categoryTx(ctx, tx, &input.Category); err != nil {
		return Post{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE posts SET title=$2,body=$3,category=$4,status=$5,published_at=CASE WHEN $5='published' THEN COALESCE(published_at,now()) ELSE NULL END,updated_at=now() WHERE id=$1`, id, input.Title, input.Body, input.Category, postInputStatus(input.PostCreateInput)); err != nil {
		return Post{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata) VALUES($1,'community.post_updated','post',$2,'Owner updated a discussion',$6,jsonb_build_object('fromStatus',$3::text,'toStatus',$4::text,'expectedVersion',$5::integer))`, actor, id, status, postInputStatus(input.PostCreateInput), version, communityRequestID(requestIDs)); err != nil {
		return Post{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Post{}, err
	}
	return r.GetOwnedPost(ctx, actor, id)
}

func (r *Repository) DeletePost(ctx context.Context, actor, id uuid.UUID, expected int, requestIDs ...string) error {
	if expected < 1 {
		return ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var version int
	var work *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT version,work_id FROM posts WHERE id=$1 AND author_id=$2 AND NOT owner_removed FOR UPDATE`, id, actor).Scan(&version, &work)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if version != expected || work != nil {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE posts SET status='removed',owner_removed=true,body='',updated_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata) VALUES($1,'community.post_deleted','post',$2,'Owner removed a discussion',$4,jsonb_build_object('expectedVersion',$3::integer))`, actor, id, expected, communityRequestID(requestIDs)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) GetReportForViewer(ctx context.Context, actor, id uuid.UUID) (Report, error) {
	item, err := scanReport(r.pool.QueryRow(ctx, reportSelect+` WHERE r.id=$2 AND (r.reporter_id=$1 OR r.subject_author_id=$1)`, actor, id), actor)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return item, err
}

func communityRequestID(values []string) string {
	if len(values) > 0 {
		return safeRequestID(values[0])
	}
	return uuid.NewString()
}
