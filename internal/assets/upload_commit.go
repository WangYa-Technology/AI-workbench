package assets

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/accountlifecycle"
	"github.com/jackc/pgx/v5"
)

// Resolve only this invocation's immutable asset identity. Acquiring the same
// lifecycle lock waits for a still-finalizing COMMIT before checking absence.
// An unavailable database or conflicting row is never permission to delete.
func (s *Service) resolveUploadCommit(parent context.Context, owner uuid.UUID, item Asset, backend, key string) (Asset, bool, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Asset{}, false, err
	}
	defer tx.Rollback(ctx)
	if err = accountlifecycle.Lock(ctx, tx, owner); err != nil {
		return Asset{}, false, err
	}
	var storedOwner uuid.UUID
	var storedBackend, storedKey, source string
	err = tx.QueryRow(ctx, `SELECT owner_id,storage_backend,storage_key,source_type,created_at FROM assets WHERE id=$1 FOR SHARE`, item.ID).Scan(&storedOwner, &storedBackend, &storedKey, &source, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, false, nil
	}
	if err != nil {
		return Asset{}, false, err
	}
	if storedOwner != owner || storedBackend != backend || storedKey != key || source != "upload" {
		return Asset{}, false, ErrConflict
	}
	// The original commit atomically included the scan binding and audit. This is
	// a response recovery, not a second insert, storage write or scan dispatch.
	return item, true, nil
}
