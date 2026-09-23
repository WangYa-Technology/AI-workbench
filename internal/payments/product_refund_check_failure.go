package payments

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Capture the actual execution even for an internal unleased invocation. A
// later claim/reclaim must not inherit its stale terminal error.
type refundCheckExecution struct {
	readID     uuid.UUID
	jobID      uuid.UUID
	status     string
	attempts   int
	leaseToken *uuid.UUID
}

func (s *Service) failRefundCheckExecution(ctx context.Context, execution refundCheckExecution, checkID uuid.UUID, expectedState, code string) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	// Serialize with claim, heartbeat and expired-lease recovery. No remote I/O
	// occurs under this lock. Lock the check next, then evaluate actual wall time;
	// checking expiry before either lock wait can accept an expired execution.
	err = tx.QueryRow(ctx, `SELECT id FROM jobs WHERE id=$1 AND status=$2 AND attempts=$3
 AND lease_token IS NOT DISTINCT FROM $4::uuid FOR UPDATE`, execution.jobID, execution.status, execution.attempts, execution.leaseToken).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	err = tx.QueryRow(ctx, `SELECT id FROM product_refund_checks WHERE id=$1 AND job_id=$2 AND status=$3 FOR UPDATE`, checkID, execution.jobID, expectedState).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	result, err := tx.Exec(ctx, `UPDATE product_refund_checks c SET status='failed',error_code=$4,completed_at=now()
 FROM jobs j WHERE c.id=$1 AND c.job_id=$2 AND c.status=$3 AND j.id=c.job_id
 AND (j.status<>'running' OR (isfinite(j.lease_expires_at) AND j.lease_expires_at>clock_timestamp()))`, checkID, execution.jobID, expectedState, code)
	if err != nil {
		return false, err
	}
	changed := result.RowsAffected() == 1
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return changed, nil
}
