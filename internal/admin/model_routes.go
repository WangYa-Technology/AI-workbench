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

type ModelRouteRevision struct {
	ID                   uuid.UUID  `json:"id"`
	Mode                 string     `json:"mode"`
	Version              int        `json:"version"`
	ParentRevisionID     *uuid.UUID `json:"parentRevisionId,omitempty"`
	ProviderProfileID    string     `json:"providerProfileId"`
	ProviderDisplayName  string     `json:"providerDisplayName"`
	Provider             string     `json:"provider"`
	ModelName            string     `json:"modelName"`
	LocalTest            bool       `json:"localTest"`
	ProviderAdminEnabled bool       `json:"providerAdminEnabled"`
	ProviderRuntimeReady bool       `json:"providerRuntimeReady"`
	Name                 string     `json:"name"`
	TimeoutSeconds       int        `json:"timeoutSeconds"`
	MaxAttempts          int        `json:"maxAttempts"`
	Reason               string     `json:"reason"`
	CreatedBy            *uuid.UUID `json:"createdBy,omitempty"`
	CreatedByHandle      *string    `json:"createdByHandle,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
}

type ModelRoutePolicy struct {
	Routes      map[string]ModelRouteRevision   `json:"routes"`
	History     map[string][]ModelRouteRevision `json:"history"`
	NextCursors map[string]string               `json:"nextCursors,omitempty"`
}

type ModelRouteHistoryInput struct {
	Mode   string
	Cursor string
	Limit  int
}

type ModelRouteUpdate struct {
	ProviderProfileID string `json:"providerProfileId"`
	Name              string `json:"name"`
	TimeoutSeconds    int    `json:"timeoutSeconds"`
	MaxAttempts       int    `json:"maxAttempts"`
	Reason            string `json:"reason"`
	ExpectedVersion   int    `json:"expectedVersion"`
	Confirmed         bool   `json:"confirmed"`
}

func (s *Service) GetModelRoutePolicy(ctx context.Context, inputs ...ModelRouteHistoryInput) (ModelRoutePolicy, error) {
	input := ModelRouteHistoryInput{}
	if len(inputs) > 0 {
		input = inputs[0]
	}
	input.Mode = strings.TrimSpace(strings.ToLower(input.Mode))
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 || input.Mode != "" && !oneOfAdmin(input.Mode, "chat", "image", "video", "music") || input.Cursor != "" && input.Mode == "" {
		return ModelRoutePolicy{}, ErrInvalidModelRouteHistory
	}
	var cursorVersion *int
	if input.Cursor != "" {
		cursor, err := decodeRevisionHistoryCursor(input.Cursor, ErrInvalidModelRouteHistory)
		if err != nil {
			return ModelRoutePolicy{}, err
		}
		cursorVersion = &cursor.Version
	}
	result := ModelRoutePolicy{Routes: map[string]ModelRouteRevision{}, History: map[string][]ModelRouteRevision{}, NextCursors: map[string]string{}}
	rows, err := s.pool.Query(ctx, `
		SELECT r.id,r.mode,r.version,r.parent_revision_id,r.provider_profile_id,p.display_name,p.provider,p.model_name,
		       p.local_test,p.admin_enabled,r.name,r.timeout_seconds,r.max_attempts,r.reason,r.created_by,u.handle,r.created_at
		FROM model_route_revisions r JOIN provider_profiles p ON p.id=r.provider_profile_id
		LEFT JOIN users u ON u.id=r.created_by JOIN model_route_state s ON s.mode=r.mode
		WHERE s.active_revision_id=r.id ORDER BY r.mode`)
	if err != nil {
		return result, fmt.Errorf("list active model routes: %w", err)
	}
	for rows.Next() {
		var item ModelRouteRevision
		if err := rows.Scan(&item.ID, &item.Mode, &item.Version, &item.ParentRevisionID, &item.ProviderProfileID, &item.ProviderDisplayName,
			&item.Provider, &item.ModelName, &item.LocalTest, &item.ProviderAdminEnabled, &item.Name, &item.TimeoutSeconds, &item.MaxAttempts,
			&item.Reason, &item.CreatedBy, &item.CreatedByHandle, &item.CreatedAt); err != nil {
			rows.Close()
			return result, err
		}
		item.ProviderRuntimeReady = s.runtimes.Available(item.Provider, item.Mode, item.ModelName)
		result.Routes[item.Mode] = item
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()
	modes := []string{"chat", "image", "video", "music"}
	if input.Mode != "" {
		modes = []string{input.Mode}
	}
	for _, mode := range modes {
		items, nextCursor, err := s.listModelRouteHistory(ctx, mode, cursorVersion, input.Limit)
		if err != nil {
			return result, err
		}
		result.History[mode] = items
		if nextCursor != "" {
			result.NextCursors[mode] = nextCursor
		}
	}
	return result, nil
}

func (s *Service) listModelRouteHistory(ctx context.Context, mode string, cursorVersion *int, limit int) ([]ModelRouteRevision, string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id,r.mode,r.version,r.parent_revision_id,r.provider_profile_id,p.display_name,p.provider,p.model_name,
		       p.local_test,p.admin_enabled,r.name,r.timeout_seconds,r.max_attempts,r.reason,r.created_by,u.handle,r.created_at
		FROM model_route_revisions r JOIN provider_profiles p ON p.id=r.provider_profile_id
		LEFT JOIN users u ON u.id=r.created_by
		WHERE r.mode=$1 AND ($2::int IS NULL OR r.version<$2)
		ORDER BY r.version DESC LIMIT $3`, mode, cursorVersion, limit+1)
	if err != nil {
		return nil, "", fmt.Errorf("list %s model route history: %w", mode, err)
	}
	defer rows.Close()
	items := make([]ModelRouteRevision, 0, limit+1)
	for rows.Next() {
		var item ModelRouteRevision
		if err := rows.Scan(&item.ID, &item.Mode, &item.Version, &item.ParentRevisionID, &item.ProviderProfileID, &item.ProviderDisplayName,
			&item.Provider, &item.ModelName, &item.LocalTest, &item.ProviderAdminEnabled, &item.Name, &item.TimeoutSeconds, &item.MaxAttempts,
			&item.Reason, &item.CreatedBy, &item.CreatedByHandle, &item.CreatedAt); err != nil {
			return nil, "", err
		}
		item.ProviderRuntimeReady = s.runtimes.Available(item.Provider, item.Mode, item.ModelName)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if len(items) > limit {
		items = items[:limit]
		return items, encodeRevisionHistoryCursor(items[len(items)-1].Version), nil
	}
	return items, "", nil
}

func (s *Service) UpdateModelRoute(ctx context.Context, actorID uuid.UUID, mode string, input ModelRouteUpdate, requestID string) (ModelRoutePolicy, error) {
	mode, input.ProviderProfileID, input.Name, input.Reason = strings.TrimSpace(strings.ToLower(mode)), strings.TrimSpace(input.ProviderProfileID), strings.TrimSpace(input.Name), strings.TrimSpace(input.Reason)
	if !input.Confirmed || !oneOfAdmin(mode, "chat", "image", "video", "music") || input.ProviderProfileID == "" || len(input.Name) < 3 || len(input.Name) > 80 || len(input.Reason) < 10 || len(input.Reason) > 500 || input.ExpectedVersion < 1 || input.TimeoutSeconds < 5 || input.TimeoutSeconds > 600 || input.MaxAttempts < 1 || input.MaxAttempts > 5 {
		return ModelRoutePolicy{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ModelRoutePolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var activeID uuid.UUID
	var version int
	if err := tx.QueryRow(ctx, `SELECT active_revision_id,version FROM model_route_state WHERE mode=$1 FOR UPDATE`, mode).Scan(&activeID, &version); errors.Is(err, pgx.ErrNoRows) {
		return ModelRoutePolicy{}, ErrNotFound
	} else if err != nil {
		return ModelRoutePolicy{}, err
	}
	if version != input.ExpectedVersion {
		return ModelRoutePolicy{}, ErrConflict
	}
	var profileMode, provider, modelName string
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT mode,provider,model_name,admin_enabled FROM provider_profiles WHERE id=$1`, input.ProviderProfileID).Scan(&profileMode, &provider, &modelName, &enabled); errors.Is(err, pgx.ErrNoRows) {
		return ModelRoutePolicy{}, ErrNotFound
	} else if err != nil {
		return ModelRoutePolicy{}, err
	}
	if profileMode != mode {
		return ModelRoutePolicy{}, ErrInvalid
	}
	if !enabled || !s.runtimes.Available(provider, profileMode, modelName) {
		return ModelRoutePolicy{}, ErrProviderConfig
	}
	newID, newVersion := uuid.New(), version+1
	if _, err := tx.Exec(ctx, `INSERT INTO model_route_revisions(id,mode,version,parent_revision_id,provider_profile_id,name,timeout_seconds,max_attempts,reason,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, newID, mode, newVersion, activeID, input.ProviderProfileID, input.Name, input.TimeoutSeconds, input.MaxAttempts, input.Reason, actorID); err != nil {
		return ModelRoutePolicy{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE model_route_state SET active_revision_id=$2,version=$3,updated_at=now() WHERE mode=$1`, mode, newID, newVersion); err != nil {
		return ModelRoutePolicy{}, err
	}
	if err := audit(ctx, tx, actorID, "admin.model_route_updated", "model_route_revision", newID, input.Reason, requestID, map[string]any{"mode": mode, "providerProfileId": input.ProviderProfileID, "previousRevisionId": activeID, "newVersion": newVersion}); err != nil {
		return ModelRoutePolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ModelRoutePolicy{}, err
	}
	return s.GetModelRoutePolicy(ctx)
}

func oneOfAdmin(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}
