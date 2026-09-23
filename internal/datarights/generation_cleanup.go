package datarights

import (
	"context"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/jackc/pgx/v5"
)

// The caller holds the shared account lifecycle lock. Mark work terminal and
// release its reservations before redacting prompts; in-flight Provider returns
// then cannot recreate the account's results or charge these reservations.
func cancelAccountGenerations(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	// Bound the application-side buffer even when a large queue is retired.
	// All batches remain in the deletion transaction, so failure is atomic.
	for {
		rows, err := tx.Query(ctx, `SELECT id FROM generations WHERE owner_id=$1 AND status IN ('queued','running') ORDER BY id LIMIT 100 FOR UPDATE`, userID)
		if err != nil {
			return err
		}
		ids := make([]uuid.UUID, 0, 100)
		for rows.Next() {
			var id uuid.UUID
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if err = billing.ReleaseGenerationPointsTx(ctx, tx, id, "account deletion"); err != nil {
				return err
			}
			if err = billing.ReleaseGenerationTx(ctx, tx, id, "account deletion"); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE generations SET status='cancelled',progress=0,cancelled_at=COALESCE(cancelled_at,now()),cancel_reason=NULL,error_code=NULL,error_message=NULL,updated_at=now() WHERE id=$1`, id); err != nil {
				return err
			}
		}
	}
	_, err := tx.Exec(ctx, `UPDATE jobs SET status='cancelled',lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now()
 WHERE kind IN ('generation.generate','creation.failure_evidence') AND status IN ('queued','running')
 AND payload->>'generationId' IN (SELECT id::text FROM generations WHERE owner_id=$1)`, userID)
	return err
}
