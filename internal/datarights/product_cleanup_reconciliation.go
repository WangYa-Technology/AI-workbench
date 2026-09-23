package datarights

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ReconcileProductCleanups repairs absent dispatch without resetting exhausted
// jobs. Running jobs are revisited after acknowledgement; a successful no-op
// does not permanently suppress cleanup after the obligation ends.
func (s *Service) ReconcileProductCleanups(ctx context.Context, limit int) (int, error) {
	const query = `SELECT order_id,buyer_id,created_at FROM product_cleanup_reconciliation_candidates
 WHERE ($2::timestamptz IS NULL OR (created_at,order_id)>($2,$3::uuid))
 ORDER BY created_at,order_id LIMIT $1`
	return s.productCleanup.run(ctx, s.pool, query, limit, s.reconcileProductCleanup)
}

func (s *Service) reconcileProductCleanup(ctx context.Context, orderID, buyerID uuid.UUID) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	// Match refund, automatic dispatch and operator recovery lock ordering. Busy
	// transactions must not consume the entire scan budget or starve later orders.
	if _, err = tx.Exec(ctx, `SELECT id FROM payment_intents WHERE order_id=$1 ORDER BY id FOR UPDATE NOWAIT`, orderID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
			return false, nil
		}
		return false, err
	}
	var previous *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT previous_job_id FROM product_cleanup_reconciliation_candidates WHERE order_id=$1 AND buyer_id=$2`, orderID, buyerID).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// No physical deletion here. The existing worker obtains subject/snapshot
	// locks and reevaluates current rights, legal holds and every repair location.
	var jobID uuid.UUID
	if err = tx.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts)
 VALUES($1,jsonb_build_object('orderId',$2::text,'retentionCheck','resolved_order'),20) RETURNING id`, productdelivery.CleanupJobKind, orderID).Scan(&jobID); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO product_cleanup_reconciliations(job_id,order_id,previous_job_id) VALUES($1,$2,$3)`, jobID, orderID, previous); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,reason,request_id,metadata)
 VALUES('product.cleanup_reconciled','order',$1,'Resolved order requires an independently retained delivery cleanup','product-cleanup-reconciliation',
 jsonb_build_object('jobId',$2::uuid,'previousJobId',$3::uuid))`, orderID, jobID, previous); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
