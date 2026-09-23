package payments

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The execution control lock lasts until this transaction ends, so a cancellation that wins
// the lock prevents a first send. After a durable dispatch marker, callers
// retain reconciliation state instead of resetting it to try again. The control
// row is separate from the job lease, allowing heartbeats during provider calls.
func requireTransferExecutionJobTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, kind string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var executable bool
	if err := tx.QueryRow(ctx, `SELECT payment_transfer_job_executable($1,$2,$3::jsonb)`, id, kind, body).Scan(&executable); err != nil {
		return err
	}
	if !executable {
		return ErrCheckoutReconciliation
	}
	return nil
}
