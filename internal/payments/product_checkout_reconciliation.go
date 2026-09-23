package payments

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ReconcileProductCheckouts restores only missing authenticated reads. Any
// prior check (including a failed/cancelled check) belongs to explicit recovery.
// No provider calls, reconstructed merchant identity or financial action here.
func (s *Service) ReconcileProductCheckouts(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, ErrInvalidCheckout
	}
	if s == nil || s.pool == nil {
		return 0, ErrDisabled
	}
	if !s.config.Enabled {
		return 0, nil
	}
	scan := &s.checkoutReconciliation
	if !scan.mu.TryLock() {
		return 0, nil
	}
	defer scan.mu.Unlock()
	read := func() ([]uuid.UUID, error) {
		rows, err := s.pool.Query(ctx, `SELECT c.payment_id FROM product_checkout_check_candidates c
 WHERE c.payment_id>$1 AND c.due_at<=now()
 ORDER BY c.payment_id LIMIT $2`, scan.after, limit)
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
		changed, err := s.reconcileProductCheckout(ctx, id)
		scan.after = id // Fairness hint only; persistent evidence decides eligibility.
		if err != nil {
			failures = append(failures, err)
		} else if changed {
			processed++
		}
	}
	return processed, errors.Join(failures...)
}

func (s *Service) reconcileProductCheckout(ctx context.Context, paymentID uuid.UUID) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var orderID uuid.UUID
	var paymentStatus, provider string
	err = tx.QueryRow(ctx, `SELECT order_id,status,provider FROM payment_intents WHERE id=$1 AND purpose='product' FOR UPDATE SKIP LOCKED`, paymentID).Scan(&orderID, &paymentStatus, &provider)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var savedPayment bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_checkout_session_evidence WHERE payment_id=$1 AND payment_status='paid')`, paymentID).Scan(&savedPayment); err != nil {
		return false, err
	}
	if !savedPayment {
		runtime, runtimeErr := s.runtimes.Runtime(provider)
		if _, canRead := runtime.(CheckoutReader); runtimeErr != nil || !canRead {
			return false, nil
		}
	}
	var locked uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM orders WHERE id=$1 FOR UPDATE SKIP LOCKED`, orderID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var version int
	var due time.Time
	err = tx.QueryRow(ctx, `SELECT payment_version,due_at FROM product_checkout_check_candidates WHERE payment_id=$1 AND due_at<=now()`, paymentID).Scan(&version, &due)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var jobID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts)
 VALUES($1,jsonb_build_object('paymentId',$2::text),20) RETURNING id`, ProductCheckoutCheckJobKind, paymentID).Scan(&jobID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO product_checkout_check_dispatches(payment_id,job_id,payment_version,due_at) VALUES($1,$2,$3,$4)`, paymentID, jobID, version, due); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_intents SET version=version+1,updated_at=now() WHERE id=$1`, paymentID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
 VALUES($1,'checkout.check_scheduled',$3,$3,jsonb_build_object('jobId',$2::text,'origin','automatic_missing_check'))`, paymentID, jobID, paymentStatus); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,request_id,metadata)
 VALUES('payment.checkout_check_scheduled','payment',$1,$2,jsonb_build_object('jobId',$3::uuid,'origin','automatic_missing_check'))`, paymentID, "checkout-check:"+jobID.String(), jobID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
