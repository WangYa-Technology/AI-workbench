package payments

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Bank status is independent of source funding and of the queue's status.
// This projection deliberately excludes provider payloads, keys and operators.
type SellerBankPayoutResumeSummary struct {
	SellerBankPayoutResume
	JobStatus string `json:"jobStatus"`
}

type SellerBankPayoutSummary struct {
	CommandID      uuid.UUID                      `json:"commandId"`
	CreatedAt      time.Time                      `json:"createdAt"`
	JobID          *uuid.UUID                     `json:"jobId,omitempty"`
	JobStatus      *string                        `json:"jobStatus,omitempty"`
	StartedAt      *time.Time                     `json:"startedAt,omitempty"`
	ProviderStatus *string                        `json:"providerStatus,omitempty"`
	CheckedAt      *time.Time                     `json:"checkedAt,omitempty"`
	RequiresReview bool                           `json:"requiresReview"`
	Resume         *SellerBankPayoutResumeSummary `json:"resume,omitempty"`
}

type SellerBankPayoutOperation struct {
	Request   SellerPayoutReviewItem   `json:"request"`
	Bank      *SellerBankPayoutSummary `json:"bank,omitempty"`
	CanSubmit bool                     `json:"canSubmit"`
	CanResume bool                     `json:"canResume"`
}

type SellerBankPayoutSubmission struct {
	Command   SellerBankPayoutCommand   `json:"command"`
	JobID     uuid.UUID                 `json:"jobId"`
	Operation SellerBankPayoutOperation `json:"operation"`
	Replayed  bool                      `json:"replayed"`
}

func (s *Service) GetSellerBankPayoutOperation(ctx context.Context, actor, request uuid.UUID) (SellerBankPayoutOperation, error) {
	var out SellerBankPayoutOperation
	if s == nil || s.pool == nil {
		return out, ErrDisabled
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	if err := paymentFinanceAuthority(ctx, tx, actor, true); err != nil {
		return out, err
	}
	out, err = s.sellerBankPayoutOperationTx(ctx, tx, actor, request)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (s *Service) bankRuntimeAvailable() bool {
	runtime, err := s.runtimes.Runtime("stripe")
	if err != nil {
		return false
	}
	_, creates := runtime.(SellerPayoutRuntime)
	_, reads := runtime.(SellerPayoutReader)
	return creates && reads
}

func (s *Service) sellerBankPayoutOperationTx(ctx context.Context, tx pgx.Tx, actor, request uuid.UUID) (SellerBankPayoutOperation, error) {
	var out SellerBankPayoutOperation
	var err error
	out.Request, err = s.scanSellerPayoutReviewItem(actor, tx.QueryRow(ctx, sellerPayoutReviewSelect+` WHERE r.id=$1`, request))
	if err != nil {
		return out, err
	}
	var bank []byte
	err = tx.QueryRow(ctx, `SELECT (
 SELECT jsonb_strip_nulls(jsonb_build_object('commandId',c.id,'createdAt',c.created_at,'jobId',d.job_id,
 'jobStatus',j.status,'startedAt',d.started_at,'providerStatus',result.status,
 'resume',CASE WHEN continuation.id IS NOT NULL THEN jsonb_build_object(
  'id',continuation.id,'commandId',c.id,'revision',continuation.revision,
  'predecessorJobId',continuation.predecessor_job_id,'jobId',continuation.job_id,
  'createdAt',continuation.created_at,'jobStatus',current_job.status) END,
 'checkedAt',(SELECT max(finished_at) FROM seller_bank_payout_reads WHERE command_id=c.id),
 'requiresReview',EXISTS(SELECT 1 FROM seller_bank_payout_reads WHERE command_id=c.id AND
  (requires_review OR (finished_at IS NULL AND deadline_at+interval '5 seconds'<statement_timestamp())))
  OR result.status IN ('failed','canceled') OR r.status='reconciliation_required'))
 FROM seller_bank_payout_commands c JOIN seller_payout_requests r ON r.id=c.payout_request_id
 LEFT JOIN seller_bank_payout_active_dispatches d ON d.command_id=c.id LEFT JOIN jobs j ON j.id=d.job_id
 LEFT JOIN seller_bank_payout_resumes continuation ON continuation.id=d.resume_id
 LEFT JOIN jobs current_job ON current_job.id=d.execution_job_id
 LEFT JOIN LATERAL (SELECT status FROM seller_bank_payout_results WHERE command_id=c.id
  ORDER BY `+sellerBankResultOrder+` LIMIT 1) result ON true
 WHERE c.payout_request_id=$1),
 EXISTS(SELECT 1 FROM seller_payout_requests r
 JOIN seller_payout_transfers t ON t.payout_request_id=r.id
 JOIN seller_payout_funding_dispatches d ON d.transfer_id=t.id AND d.started_at IS NOT NULL
 JOIN seller_payout_funding_admissions a ON a.transfer_id=t.id AND a.payout_request_id=r.id
 JOIN seller_payout_reviews v ON v.id=a.review_id AND v.payout_request_id=r.id
 JOIN seller_payout_bank_targets b ON b.payout_request_id=r.id AND b.seller_id=r.seller_id
 JOIN product_settlements s ON s.id=t.settlement_id AND s.seller_id=r.seller_id
 JOIN seller_payout_request_allocations allocation ON allocation.payout_request_id=r.id AND allocation.settlement_id=s.id
 JOIN payment_intents p ON p.id=s.payment_id JOIN orders o ON o.id=s.order_id AND o.id=p.order_id
 JOIN product_checkout_requests original ON original.payment_id=p.id
 JOIN payment_destinations pd ON pd.user_id=r.seller_id AND pd.provider='stripe'
 JOIN users seller ON seller.id=r.seller_id AND seller.status='active'
 WHERE r.id=$1 AND r.status IN ('processing','reconciliation_required') AND t.status='succeeded'
 AND t.provider='stripe' AND t.provider_transfer_id IS NOT NULL AND v.decision='approved' AND v.actor_id<>r.seller_id
 AND NOT EXISTS(SELECT 1 FROM seller_payout_reviews newer WHERE newer.payout_request_id=r.id AND newer.revision>v.revision)
 AND b.bank_destination_id=v.bank_destination_id AND b.destination_id=t.destination_id AND pd.destination_id=t.destination_id
 AND s.status='available' AND s.available_at<=statement_timestamp() AND p.status='paid' AND o.status='fulfilled'
 AND allocation.released_at IS NULL AND allocation.amount_cents=r.amount_cents
 AND s.net_amount_cents=r.amount_cents AND t.amount_cents=r.amount_cents AND b.amount_cents=r.amount_cents AND v.amount_cents=r.amount_cents
 AND r.currency='USD' AND t.currency=r.currency AND s.currency=r.currency AND b.currency=r.currency AND v.currency=r.currency
 AND t.provider_identity=b.provider_identity AND t.provider_identity=original.identity
 AND pd.status='verified' AND pd.charges_enabled AND pd.payouts_enabled
 AND pd.original_merchant_id=original.identity->>'merchantId' AND pd.original_live_mode=s.live_mode
 AND COALESCE(pd.original_store_id,'')=COALESCE(original.identity->>'storeId','')
 AND pd.original_endpoint=original.identity->>'endpoint' AND pd.original_api_version=original.identity->>'apiVersion'
 AND pd.original_request_version=original.identity->>'requestVersion'
 AND EXISTS(SELECT 1 FROM product_settlement_settings WHERE singleton=true AND payout_mode='seller_payout')
 AND NOT seller_funds_recovery_blocks(r.seller_id,s.id)
 AND NOT EXISTS(SELECT 1 FROM product_refund_review WHERE payment_id=p.id)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_lookup_review WHERE payment_id=p.id)
 AND NOT EXISTS(SELECT 1 FROM seller_source_reversal_commands WHERE payout_request_id=r.id)
 AND NOT EXISTS(SELECT 1 FROM seller_payout_funding_reads WHERE transfer_id=t.id AND requires_review))`, request).Scan(&bank, &out.CanSubmit)
	if err != nil {
		return out, err
	}
	if bank != nil {
		if err := json.Unmarshal(bank, &out.Bank); err != nil {
			return out, err
		}
	}
	// A read-time hint only. Submission and first dispatch each validate the
	// complete financial graph under the original payment and seller locks.
	eligible := out.CanSubmit && actor != out.Request.SellerID && s.config.Enabled && s.bankRuntimeAvailable()
	out.CanSubmit = eligible && out.Bank == nil
	if eligible && out.Bank != nil {
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM seller_bank_payout_commands c
 JOIN seller_bank_payout_active_dispatches d ON d.command_id=c.id
 JOIN jobs j ON j.id=d.execution_job_id
 JOIN users u ON u.id=c.actor_id AND u.status='active'
 JOIN role_permissions permission ON permission.role=u.role AND permission.permission_id='admin:finance'
 WHERE c.id=$1 AND c.actor_id<>c.seller_id AND d.started_at IS NULL
 AND j.status IN ('failed','cancelled','succeeded')
 AND statement_timestamp()<c.created_at+interval '23 hours'
 AND NOT EXISTS(SELECT 1 FROM seller_bank_payout_reads WHERE command_id=c.id)
 AND NOT EXISTS(SELECT 1 FROM seller_bank_payout_results WHERE command_id=c.id))`, out.Bank.CommandID).Scan(&out.CanResume)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// SubmitSellerBankPayout atomically reserves and queues an explicitly confirmed
// command. There is no intermediate committed intent with a missing job.
func (s *Service) SubmitSellerBankPayout(ctx context.Context, actor, request uuid.UUID, input SellerBankPayoutInput, key, trace string) (SellerBankPayoutSubmission, error) {
	out, err := s.submitSellerBankPayout(ctx, actor, request, input, key, trace)
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && oneOf(pgerr.Code, "23505", "23514", "40001", "40P01") {
		err = ErrSellerBankPayoutConflict
	}
	return out, err
}

func (s *Service) submitSellerBankPayout(ctx context.Context, actor, request uuid.UUID, input SellerBankPayoutInput, key, trace string) (SellerBankPayoutSubmission, error) {
	var out SellerBankPayoutSubmission
	if s == nil || s.pool == nil {
		return out, ErrDisabled
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	reserved, err := s.reserveSellerBankPayoutTx(ctx, tx, actor, request, input, key, trace)
	if err != nil {
		return out, err
	}
	out.Command, out.Replayed = reserved.Command, reserved.Replayed
	if !out.Replayed && !s.bankRuntimeAvailable() {
		return out, ErrProviderUnavailable
	}
	out.JobID, err = s.queueSellerBankPayoutTx(ctx, tx, actor, out.Command.ID)
	if err != nil {
		return out, err
	}
	out.Operation, err = s.sellerBankPayoutOperationTx(ctx, tx, actor, request)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
