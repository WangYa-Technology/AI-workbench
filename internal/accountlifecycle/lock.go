// Package accountlifecycle coordinates writes with account deletion without
// changing the payment/subject/source row-lock order used by media cleanup.
package accountlifecycle

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Key preserves the existing deletion lock namespace.
func Key(userID uuid.UUID) string { return "data-rights-deletion:" + userID.String() }

// Lock must precede all row locks in transactions that create/redact account
// content. Never hold it across an external generation Provider request.
func Lock(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, Key(userID))
	return err
}
