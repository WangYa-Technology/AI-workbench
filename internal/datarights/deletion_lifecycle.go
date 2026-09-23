package datarights

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/accountlifecycle"
	"github.com/jackc/pgx/v5"
)

// lockDeletionSubject serializes request creation/cancellation, legal holds and
// both deletion phases before they take any row locks. Media/payment row lock
// order is deliberately unchanged: taking the user row before payment locks
// during physical cleanup would invert product delivery's locking protocol.
func lockDeletionSubject(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	return accountlifecycle.Lock(ctx, tx, userID)
}

func deletionSubjectLockKey(userID uuid.UUID) string {
	return accountlifecycle.Key(userID)
}

// Read the immutable owner binding first, serialize, then let the caller read
// the current request state under its row lock. Never act on the pre-wait state.
func lockDeletionRequest(ctx context.Context, tx pgx.Tx, requestID uuid.UUID) error {
	var userID uuid.UUID
	err := tx.QueryRow(ctx, `SELECT user_id FROM data_rights_requests WHERE id=$1 AND request_type='account_deletion'`, requestID).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return lockDeletionSubject(ctx, tx, userID)
}
