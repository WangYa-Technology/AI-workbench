package payments

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type paymentReconciliationScan struct {
	mu    sync.Mutex
	after uuid.UUID
}

// ReconcileProductRefunds only enqueues authenticated reads. The existing check
// handler owns observation validation and refund-result application. No money
// movement, synthetic success, user identity or reset of failed jobs here.
func (s *Service) ReconcileProductRefunds(ctx context.Context, limit int) (int, error) {
	return s.reconcileProductRefunds(ctx, limit, time.Now().UTC())
}

func (s *Service) reconcileProductRefunds(ctx context.Context, limit int, asOf time.Time) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, ErrInvalidRefund
	}
	if s == nil || s.pool == nil {
		return 0, ErrDisabled
	}
	if _, ok := s.refundReader("stripe"); !ok {
		return 0, nil
	}
	scan := &s.refundReconciliation
	if !scan.mu.TryLock() {
		return 0, nil
	}
	defer scan.mu.Unlock()
	const query = `SELECT payment_id FROM product_refund_reconciliation_candidates
 WHERE payment_id>$1 AND due_at<=$2 ORDER BY payment_id LIMIT $3`
	read := func() ([]uuid.UUID, error) {
		rows, err := s.pool.Query(ctx, query, scan.after, asOf, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, rows.Err()
	}
	ids, err := read()
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 && scan.after != uuid.Nil {
		scan.after = uuid.Nil
		ids, err = read()
		if err != nil {
			return 0, err
		}
	}
	processed := 0
	var failures []error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return processed, errors.Join(append(failures, err)...)
		}
		changed, err := s.reconcileProductRefund(ctx, id, asOf)
		// Fairness hint only: restart and wrap always consult persistent evidence.
		scan.after = id
		if err != nil {
			failures = append(failures, err)
		} else if changed {
			processed++
		}
	}
	return processed, errors.Join(failures...)
}

func (s *Service) reconcileProductRefund(ctx context.Context, paymentID uuid.UUID, asOf time.Time) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE SKIP LOCKED`, paymentID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var eligible bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_refund_reconciliation_candidates WHERE payment_id=$1 AND due_at<=$2)`, paymentID, asOf).Scan(&eligible); err != nil || !eligible {
		return false, err
	}
	checkID, err := insertProductRefundCheckWithOriginTx(ctx, tx, nil, paymentID, "automatic")
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_intents SET version=version+1,updated_at=now() WHERE id=$1`, paymentID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,request_id,metadata)
 VALUES('payment.refund_check_scheduled','payment',$1,$2,jsonb_build_object('checkId',$3::uuid,'origin','automatic'))`, paymentID, "refund-check:"+checkID.String(), checkID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
