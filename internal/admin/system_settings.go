package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type SystemSettingRevision struct {
	ID                         uuid.UUID  `json:"id"`
	Version                    int        `json:"version"`
	ParentRevisionID           *uuid.UUID `json:"parentRevisionId,omitempty"`
	Name                       string     `json:"name"`
	RegistrationsEnabled       bool       `json:"registrationsEnabled"`
	GenerationsEnabled         bool       `json:"generationsEnabled"`
	PublishingEnabled          bool       `json:"publishingEnabled"`
	MarketplaceCheckoutEnabled bool       `json:"marketplaceCheckoutEnabled"`
	TaskCreationEnabled        bool       `json:"taskCreationEnabled"`
	PublicNotice               string     `json:"publicNotice"`
	Reason                     string     `json:"reason"`
	CreatedBy                  *uuid.UUID `json:"createdBy,omitempty"`
	CreatedByHandle            *string    `json:"createdByHandle,omitempty"`
	CreatedAt                  time.Time  `json:"createdAt"`
}
type SystemSettingPolicy struct {
	Current    SystemSettingRevision   `json:"current"`
	History    []SystemSettingRevision `json:"history"`
	NextCursor *string                 `json:"nextCursor,omitempty"`
}
type SystemSettingUpdate struct {
	Name                       string `json:"name"`
	RegistrationsEnabled       bool   `json:"registrationsEnabled"`
	GenerationsEnabled         bool   `json:"generationsEnabled"`
	PublishingEnabled          bool   `json:"publishingEnabled"`
	MarketplaceCheckoutEnabled bool   `json:"marketplaceCheckoutEnabled"`
	TaskCreationEnabled        bool   `json:"taskCreationEnabled"`
	PublicNotice               string `json:"publicNotice"`
	Reason                     string `json:"reason"`
	ExpectedVersion            int    `json:"expectedVersion"`
	Confirmed                  bool   `json:"confirmed"`
}

func (s *Service) GetSystemSettingPolicy(ctx context.Context, inputs ...RevisionHistoryInput) (SystemSettingPolicy, error) {
	input := RevisionHistoryInput{}
	if len(inputs) > 0 {
		input = inputs[0]
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return SystemSettingPolicy{}, ErrInvalidSystemSettingHistory
	}
	var cursorVersion *int
	if input.Cursor != "" {
		cursor, err := decodeRevisionHistoryCursor(input.Cursor, ErrInvalidSystemSettingHistory)
		if err != nil {
			return SystemSettingPolicy{}, err
		}
		cursorVersion = &cursor.Version
	}
	var activeID uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT active_revision_id FROM system_setting_state WHERE singleton=true`).Scan(&activeID); errors.Is(err, pgx.ErrNoRows) {
		return SystemSettingPolicy{}, ErrNotFound
	} else if err != nil {
		return SystemSettingPolicy{}, err
	}
	policy := SystemSettingPolicy{History: []SystemSettingRevision{}}
	err := s.pool.QueryRow(ctx, `SELECT r.id,r.version,r.parent_revision_id,r.name,r.registrations_enabled,r.generations_enabled,r.publishing_enabled,r.marketplace_checkout_enabled,r.task_creation_enabled,r.public_notice,r.reason,r.created_by,u.handle,r.created_at FROM system_setting_revisions r LEFT JOIN users u ON u.id=r.created_by WHERE r.id=$1`, activeID).Scan(
		&policy.Current.ID, &policy.Current.Version, &policy.Current.ParentRevisionID, &policy.Current.Name, &policy.Current.RegistrationsEnabled, &policy.Current.GenerationsEnabled, &policy.Current.PublishingEnabled, &policy.Current.MarketplaceCheckoutEnabled, &policy.Current.TaskCreationEnabled, &policy.Current.PublicNotice, &policy.Current.Reason, &policy.Current.CreatedBy, &policy.Current.CreatedByHandle, &policy.Current.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return SystemSettingPolicy{}, ErrNotFound
	} else if err != nil {
		return SystemSettingPolicy{}, err
	}
	rows, err := s.pool.Query(ctx, `SELECT r.id,r.version,r.parent_revision_id,r.name,r.registrations_enabled,r.generations_enabled,r.publishing_enabled,r.marketplace_checkout_enabled,r.task_creation_enabled,r.public_notice,r.reason,r.created_by,u.handle,r.created_at FROM system_setting_revisions r LEFT JOIN users u ON u.id=r.created_by WHERE ($1::int IS NULL OR r.version < $1) ORDER BY r.version DESC LIMIT $2`, cursorVersion, input.Limit+1)
	if err != nil {
		return SystemSettingPolicy{}, fmt.Errorf("list system settings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item SystemSettingRevision
		if err := rows.Scan(&item.ID, &item.Version, &item.ParentRevisionID, &item.Name, &item.RegistrationsEnabled, &item.GenerationsEnabled, &item.PublishingEnabled, &item.MarketplaceCheckoutEnabled, &item.TaskCreationEnabled, &item.PublicNotice, &item.Reason, &item.CreatedBy, &item.CreatedByHandle, &item.CreatedAt); err != nil {
			return policy, err
		}
		policy.History = append(policy.History, item)
	}
	if err := rows.Err(); err != nil {
		return policy, err
	}
	if len(policy.History) > input.Limit {
		policy.History = policy.History[:input.Limit]
		cursor := encodeRevisionHistoryCursor(policy.History[len(policy.History)-1].Version)
		policy.NextCursor = &cursor
	}
	return policy, nil
}

func (s *Service) UpdateSystemSettingPolicy(ctx context.Context, actorID uuid.UUID, input SystemSettingUpdate, requestID string) (SystemSettingPolicy, error) {
	input.Name, input.PublicNotice, input.Reason = strings.TrimSpace(input.Name), strings.TrimSpace(input.PublicNotice), strings.TrimSpace(input.Reason)
	if !input.Confirmed || input.ExpectedVersion < 1 || len(input.Name) < 3 || len(input.Name) > 80 || len(input.PublicNotice) > 240 || len(input.Reason) < 10 || len(input.Reason) > 500 {
		return SystemSettingPolicy{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SystemSettingPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var activeID uuid.UUID
	var version int
	if err := tx.QueryRow(ctx, `SELECT active_revision_id,version FROM system_setting_state WHERE singleton=true FOR UPDATE`).Scan(&activeID, &version); err != nil {
		return SystemSettingPolicy{}, err
	}
	if version != input.ExpectedVersion {
		return SystemSettingPolicy{}, ErrConflict
	}
	newID, newVersion := uuid.New(), version+1
	_, err = tx.Exec(ctx, `INSERT INTO system_setting_revisions(id,version,parent_revision_id,name,registrations_enabled,generations_enabled,publishing_enabled,marketplace_checkout_enabled,task_creation_enabled,public_notice,reason,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, newID, newVersion, activeID, input.Name, input.RegistrationsEnabled, input.GenerationsEnabled, input.PublishingEnabled, input.MarketplaceCheckoutEnabled, input.TaskCreationEnabled, input.PublicNotice, input.Reason, actorID)
	if err != nil {
		return SystemSettingPolicy{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE system_setting_state SET active_revision_id=$1,version=$2,updated_at=now() WHERE singleton=true`, newID, newVersion); err != nil {
		return SystemSettingPolicy{}, err
	}
	if err = audit(ctx, tx, actorID, "admin.system_settings_updated", "system_setting_revision", newID, input.Reason, requestID, map[string]any{"previousRevisionId": activeID, "newVersion": newVersion}); err != nil {
		return SystemSettingPolicy{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return SystemSettingPolicy{}, err
	}
	return s.GetSystemSettingPolicy(ctx)
}
