package payments

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
)

// Register before remote I/O, under the payment and actual execution locks.
// The returned absolute deadline also fences a process paused before dispatch.
func (s *Service) beginRefundRead(ctx context.Context, checkID, paymentID uuid.UUID, execution *refundCheckExecution) (time.Time, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, paymentID).Scan(&id); err != nil {
		return time.Time{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT id FROM jobs WHERE id=$1 AND status=$2 AND status IN ('queued','running') AND attempts=$3 AND lease_token IS NOT DISTINCT FROM $4::uuid FOR UPDATE`, execution.jobID, execution.status, execution.attempts, execution.leaseToken).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, jobs.ErrLeaseLost
		}
		return time.Time{}, err
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_refund_checks c JOIN jobs j ON j.id=c.job_id
 WHERE c.id=$1 AND c.payment_id=$2 AND c.job_id=$3 AND c.status='requested'
 AND (j.status<>'running' OR (isfinite(j.lease_expires_at) AND j.lease_expires_at>clock_timestamp())))`, checkID, paymentID, execution.jobID).Scan(&valid); err != nil {
		return time.Time{}, err
	}
	if !valid {
		return time.Time{}, jobs.ErrLeaseLost
	}
	readID := uuid.New()
	var deadline time.Time
	if err = tx.QueryRow(ctx, `INSERT INTO product_refund_read_executions(id,check_id,attempt_number,lease_token) VALUES($1,$2,$3,$4) RETURNING read_deadline`, readID, checkID, execution.attempts, execution.leaseToken).Scan(&deadline); err != nil {
		return time.Time{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return time.Time{}, err
	}
	execution.readID = readID
	return deadline, nil
}

func recordRefundReadTx(ctx context.Context, tx pgx.Tx, checkID, paymentID uuid.UUID, execution *refundCheckExecution, readErr error) error {
	if execution == nil || execution.readID == uuid.Nil {
		return nil
	} // Trusted internal/imported observation, no remote dispatch.
	var code *string
	if readErr != nil {
		value := SanitizeProviderError(readErr).Error()
		code = &value
	}
	result, err := tx.Exec(ctx, `UPDATE product_refund_read_executions SET recorded_at=clock_timestamp(),complete=$3,error_code=$4
 WHERE id=$1 AND check_id=$2 AND recorded_at IS NULL`, execution.readID, checkID, readErr == nil, code)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		var same bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_refund_read_executions WHERE id=$1 AND check_id=$2 AND recorded_at IS NOT NULL AND complete=$3 AND error_code IS NOT DISTINCT FROM $4::text)`, execution.readID, checkID, readErr == nil, code).Scan(&same); err != nil {
			return err
		}
		if !same {
			return ErrRefundConflict
		}
	}
	if readErr != nil {
		return nil
	}
	// Recover only reads whose remote and persistence windows ended before this
	// complete read began, and whose original execution no longer holds a lease.
	// Saved positive observations keep their independent review requirement.
	recovered, err := tx.Exec(ctx, `INSERT INTO product_refund_read_recoveries(execution_id,recovery_execution_id)
 SELECT old.id,fresh.id FROM product_refund_read_executions old
 JOIN product_refund_checks c ON c.id=old.check_id AND c.payment_id=$1
 JOIN jobs j ON j.id=c.job_id
 JOIN product_refund_read_executions fresh ON fresh.id=$2
 WHERE old.recorded_at IS NULL AND fresh.started_at>old.read_deadline+interval '5 seconds'
 AND NOT(j.status='running' AND j.lease_token IS NOT DISTINCT FROM old.lease_token AND j.lease_expires_at>clock_timestamp())
 AND NOT EXISTS(SELECT 1 FROM product_refund_read_recoveries r WHERE r.execution_id=old.id)
 ON CONFLICT(execution_id) DO NOTHING`, paymentID, execution.readID)
	if err != nil || recovered.RowsAffected() == 0 {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,request_id,metadata)
 VALUES('payment.refund_read_recovered','payment',$1,$2,jsonb_build_object('readId',$3::uuid,'recoveredCount',$4::integer))`, paymentID, "refund-read-recovery:"+execution.readID.String(), execution.readID, recovered.RowsAffected())
	return err
}

func (s *Service) recordFailedRefundRead(ctx context.Context, checkID, paymentID uuid.UUID, execution *refundCheckExecution, cause error) error {
	_, err := retryPaymentEvidenceWrite(ctx, func(writeCtx context.Context) (bool, error) {
		tx, err := s.pool.BeginTx(writeCtx, pgx.TxOptions{IsoLevel: pgx.Serializable})
		if err != nil {
			return false, err
		}
		defer tx.Rollback(writeCtx)
		var id uuid.UUID
		if err = tx.QueryRow(writeCtx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, paymentID).Scan(&id); err != nil {
			return false, err
		}
		if err = recordRefundReadTx(writeCtx, tx, checkID, paymentID, execution, cause); err != nil {
			return false, err
		}
		return true, tx.Commit(writeCtx)
	})
	return err
}
