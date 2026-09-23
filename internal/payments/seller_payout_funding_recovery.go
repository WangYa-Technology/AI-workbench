package payments

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
)

const SellerPayoutFundingCheckJobKind = "payment.check_seller_payout_funding"

func (s *Service) HandleSellerPayoutFundingCheckJob(ctx context.Context, job jobs.Job) error {
	return s.handleSellerPayoutFundingJob(ctx, job, true)
}

func (s *Service) ReconcileSellerPayoutFunding(ctx context.Context, limit int) (int, error) {
	return s.reconcileSellerPayoutFunding(ctx, limit, time.Now().UTC())
}

func (s *Service) reconcileSellerPayoutFunding(ctx context.Context, limit int, asOf time.Time) (int, error) {
	if s == nil || s.pool == nil {
		return 0, ErrDisabled
	}
	if limit < 1 || limit > 100 {
		return 0, newProviderFailure("payment_invalid_request", 0)
	}
	scan := &s.sellerFundingReconciliation
	if !scan.mu.TryLock() {
		return 0, nil
	}
	defer scan.mu.Unlock()
	read := func() ([]uuid.UUID, error) {
		rows, err := s.pool.Query(ctx, `SELECT transfer_id FROM seller_payout_funding_check_candidates
 WHERE transfer_id>$1 AND due_at<=$3 ORDER BY transfer_id LIMIT $2`, scan.after, limit, asOf)
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
	count := 0
	var failures []error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return count, errors.Join(append(failures, err)...)
		}
		changed, err := s.scheduleSellerFundingCheck(ctx, id, asOf)
		scan.after = id
		if err != nil {
			failures = append(failures, err)
		} else if changed {
			count++
		}
	}
	return count, errors.Join(failures...)
}

func (s *Service) scheduleSellerFundingCheck(ctx context.Context, transferID uuid.UUID, asOf time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.Background())
	// Use the first lock in the handler's financial lock order. Busy transfers
	// must not consume the scan deadline or starve later candidates.
	var paymentID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT p.id FROM payment_intents p
 JOIN seller_payout_funding_dispatches d ON d.payment_id=p.id
 WHERE d.transfer_id=$1 FOR UPDATE OF p SKIP LOCKED`, transferID).Scan(&paymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var eligible bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM seller_payout_funding_check_candidates
 WHERE transfer_id=$1 AND due_at<=$2)`, transferID, asOf).Scan(&eligible); err != nil || !eligible {
		return false, err
	}
	body, err := json.Marshal(sellerPayoutFundingPayload{TransferID: transferID})
	if err != nil {
		return false, err
	}
	var jobID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO jobs(kind,payload) VALUES($1,$2) RETURNING id`, SellerPayoutFundingCheckJobKind, body).Scan(&jobID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO seller_payout_funding_checks(job_id,transfer_id) VALUES($1,$2)`, jobID, transferID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,request_id,metadata)
 VALUES('marketplace.seller_funding_check_scheduled','seller_payout_transfer',$1,$2,
 jsonb_build_object('jobId',$3::uuid,'paymentId',$4::uuid))`, transferID, "seller-funding-check:"+jobID.String(), jobID, paymentID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
