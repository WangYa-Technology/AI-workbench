package creation

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/accountlifecycle"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
)

// The lifecycle lock coordinates with deletion's existing lock before any
// rows. SHARE additionally serializes an administrative status update. Callers
// must lock the account before generation/reservation rows, not afterwards.
func lockGenerationAccount(ctx context.Context, tx pgx.Tx, owner uuid.UUID) (string, error) {
	if err := accountlifecycle.Lock(ctx, tx, owner); err != nil {
		return "", err
	}
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR SHARE`, owner).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrForbidden
	}
	return status, err
}

func requireActiveGenerationAccount(ctx context.Context, tx pgx.Tx, owner uuid.UUID) error {
	status, err := lockGenerationAccount(ctx, tx, owner)
	if err != nil {
		return err
	}
	if status != "active" {
		return ErrForbidden
	}
	return nil
}

func generationOwner(ctx context.Context, tx pgx.Tx, id uuid.UUID) (uuid.UUID, error) {
	var owner uuid.UUID
	err := tx.QueryRow(ctx, `SELECT owner_id FROM generations WHERE id=$1`, id).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return owner, err
}

// No transaction/lock is held during a Provider call. Recheck immediately
// before dispatch and on return; an already external request cannot be recalled.
func (s *Service) generationMayContinue(ctx context.Context, id, owner uuid.UUID, job jobs.Job) (bool, error) {
	var state, account string
	err := s.pool.QueryRow(ctx, `SELECT g.status,u.status FROM generations g JOIN users u ON u.id=g.owner_id
 WHERE g.id=$1 AND g.owner_id=$2`, id, owner).Scan(&state, &account)
	if err != nil {
		return false, fmt.Errorf("check generation lifecycle: %w", err)
	}
	if state == "succeeded" || state == "cancelled" || state == "failed" {
		return false, nil
	}
	if account != "active" {
		return false, ErrAccountUnavailable
	}
	_, err = checkExecution(ctx, s.pool, id, job)
	return err == nil, err
}
