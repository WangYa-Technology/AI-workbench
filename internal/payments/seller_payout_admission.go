package payments

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrSellerFundingAdmissionInvalid  = errors.New("invalid seller funding admission")
	ErrSellerFundingAdmissionConflict = errors.New("seller funding admission changed or no longer eligible")
)

type SellerFundingAdmissionInput struct {
	ReviewID          uuid.UUID `json:"reviewId"`
	ExpectedRevision  int       `json:"expectedRevision"`
	SettlementID      uuid.UUID `json:"settlementId"`
	AmountCents       int       `json:"amountCents"`
	BankDestinationID string    `json:"bankDestinationId"`
	Reason            string    `json:"reason"`
	Confirmed         bool      `json:"confirmed"`
}

type SellerFundingAdmission struct {
	ID              uuid.UUID `json:"id"`
	PayoutRequestID uuid.UUID `json:"payoutRequestId"`
	ReviewID        uuid.UUID `json:"reviewId"`
	TransferID      uuid.UUID `json:"transferId"`
	ActorID         uuid.UUID `json:"actorId"`
	Reason          string    `json:"reason"`
	CreatedAt       time.Time `json:"createdAt"`
}

type SellerFundingAdmissionResult struct {
	Admission SellerFundingAdmission `json:"admission"`
	JobID     uuid.UUID              `json:"jobId"`
	Request   SellerPayoutReviewItem `json:"request"`
	Replayed  bool                   `json:"replayed"`
}

// AdmitSellerPayoutFunding consumes a reviewed request for exactly one durable
// platform-to-Connect source transfer. Bank dispatch is a separate lifecycle.
// The finance HTTP command requires explicit confirmation. Review alone must
// never enqueue this operation or send money.
func (s *Service) AdmitSellerPayoutFunding(ctx context.Context, actorID, requestID uuid.UUID, input SellerFundingAdmissionInput, key, trace string) (SellerFundingAdmissionResult, error) {
	out, err := s.admitSellerPayoutFunding(ctx, actorID, requestID, input, key, trace)
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && oneOf(pgerr.Code, "23505", "23514", "40001", "40P01") {
		err = ErrSellerFundingAdmissionConflict
	}
	return out, err
}

func (s *Service) admitSellerPayoutFunding(ctx context.Context, actorID, requestID uuid.UUID, input SellerFundingAdmissionInput, key, trace string) (SellerFundingAdmissionResult, error) {
	var out SellerFundingAdmissionResult
	if s == nil || s.pool == nil {
		return out, ErrDisabled
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if actorID == uuid.Nil || requestID == uuid.Nil || input.ReviewID == uuid.Nil || input.SettlementID == uuid.Nil ||
		input.ExpectedRevision < 1 || input.ExpectedRevision >= 2147483647 || input.AmountCents < 1 || !input.Confirmed ||
		!validStripeID(input.BankDestinationID, "ba_") || !sellerPayoutReviewKey.MatchString(key) ||
		!utf8.ValidString(input.Reason) || strings.ContainsRune(input.Reason, 0) || utf8.RuneCountInString(input.Reason) < 10 || utf8.RuneCountInString(input.Reason) > 1000 {
		return out, ErrSellerFundingAdmissionInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	if err := paymentFinanceAuthority(ctx, tx, actorID, false); err != nil {
		return out, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "seller-funding-admission:"+actorID.String()+":"+key); err != nil {
		return out, err
	}
	var revision, amount int
	var settlement uuid.UUID
	var bank string
	err = tx.QueryRow(ctx, `SELECT a.id,a.payout_request_id,a.review_id,a.transfer_id,a.actor_id,a.reason,a.created_at,
 d.job_id,v.revision,v.settlement_id,v.amount_cents,v.bank_destination_id
 FROM seller_payout_funding_admissions a JOIN seller_payout_reviews v ON v.id=a.review_id
 JOIN seller_payout_funding_dispatches d ON d.transfer_id=a.transfer_id
 WHERE a.actor_id=$1 AND a.idempotency_key=$2`, actorID, key).Scan(
		&out.Admission.ID, &out.Admission.PayoutRequestID, &out.Admission.ReviewID, &out.Admission.TransferID, &out.Admission.ActorID, &out.Admission.Reason, &out.Admission.CreatedAt,
		&out.JobID, &revision, &settlement, &amount, &bank)
	if err == nil {
		if out.Admission.PayoutRequestID != requestID || out.Admission.ReviewID != input.ReviewID || out.Admission.Reason != input.Reason ||
			revision != input.ExpectedRevision || settlement != input.SettlementID || amount != input.AmountCents || bank != input.BankDestinationID {
			return SellerFundingAdmissionResult{}, ErrSellerFundingAdmissionConflict
		}
		if err := lockProductSettlementPaymentTx(ctx, tx, settlement); err != nil {
			return out, err
		}
		if err := paymentFinanceAuthority(ctx, tx, actorID, true); err != nil {
			return out, err
		}
		out.Request, err = s.scanSellerPayoutReviewItem(actorID, tx.QueryRow(ctx, sellerPayoutReviewSelect+` WHERE r.id=$1`, requestID))
		if err != nil {
			return out, err
		}
		out.Replayed = true
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if !s.config.Enabled {
		return out, ErrDisabled
	}
	var seller uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT seller_id FROM seller_payout_requests WHERE id=$1`, requestID).Scan(&seller); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrSellerPayoutNotFound
		}
		return out, err
	}
	if seller == actorID {
		return out, ErrFinanceForbidden
	}
	settlement, err = lockSellerPayoutBankRequest(ctx, tx, seller, requestID)
	if err != nil {
		return out, err
	}
	item, err := s.scanSellerPayoutReviewItem(actorID, tx.QueryRow(ctx, sellerPayoutReviewSelect+` WHERE r.id=$1`, requestID))
	if err != nil {
		return out, err
	}
	if item.LatestReview == nil || item.LatestReview.ID != input.ReviewID || item.LatestReview.Revision != input.ExpectedRevision ||
		item.LatestReview.Decision != "approved" || item.SettlementID != input.SettlementID || item.AmountCents != input.AmountCents || item.BankDestinationID != input.BankDestinationID {
		return out, ErrSellerFundingAdmissionConflict
	}
	state, err := sellerPayoutBankStateTx(ctx, tx, seller, requestID, settlement, input.BankDestinationID)
	if err != nil {
		return out, err
	}
	if err := paymentFinanceAuthority(ctx, tx, actorID, true); err != nil {
		return out, err
	}
	// A paid legacy intent may still lack the charge needed to freeze the
	// funding source. Reject it before creating evidence, rather than leaking a
	// NOT NULL violation as a server error or exposing an unusable action.
	var charge string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(provider_charge_id,'') FROM payment_intents WHERE id=$1`, state.Settlement.PaymentID).Scan(&charge); err != nil {
		return out, err
	}
	if !validStripeID(charge, "ch_") {
		return out, ErrCheckoutReconciliation
	}
	identity, err := json.Marshal(state.Input.Identity)
	if err != nil {
		return out, err
	}
	// Preserve the original key for the first source and for legacy replays.
	// Only a fully returned, consumed source permits a fresh provider operation.
	dispatch := TransferRequest{PaymentID: state.Settlement.PaymentID}
	var hasClosedSource bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM seller_source_reversal_closures WHERE settlement_id=$1)`, settlement).Scan(&hasClosedSource); err != nil {
		return out, err
	}
	if hasClosedSource {
		dispatch.SourceRequestID = requestID
	}
	if err := tx.QueryRow(ctx, `INSERT INTO seller_payout_transfers(payout_request_id,settlement_id,provider,live_mode,destination_id,amount_cents,currency,provider_identity,dispatch_key)
 VALUES($1,$2,'stripe',$3,$4,$5,$6,$7,$8) RETURNING id`, requestID, settlement, state.Input.Identity.LiveMode, state.Input.DestinationID,
		input.AmountCents, item.Currency, identity, transferDispatchKey(dispatch)).Scan(&out.Admission.TransferID); err != nil {
		return out, err
	}
	if err := tx.QueryRow(ctx, `INSERT INTO seller_payout_funding_admissions(payout_request_id,review_id,transfer_id,actor_id,idempotency_key,reason,request_id)
 VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id,payout_request_id,review_id,actor_id,reason,created_at`, requestID, input.ReviewID,
		out.Admission.TransferID, actorID, key, input.Reason, trace).Scan(&out.Admission.ID, &out.Admission.PayoutRequestID, &out.Admission.ReviewID,
		&out.Admission.ActorID, &out.Admission.Reason, &out.Admission.CreatedAt); err != nil {
		return out, err
	}
	if out.JobID, err = enqueueSellerPayoutFundingTx(ctx, tx, out.Admission.TransferID); err != nil {
		return out, err
	}
	if err := recordSellerPayoutEventTx(ctx, tx, requestID, "funding.admitted", &state.Status, &state.Status, "funding-admission:"+out.Admission.ID.String(), map[string]any{
		"admissionId": out.Admission.ID, "reviewId": input.ReviewID, "transferId": out.Admission.TransferID, "jobId": out.JobID,
	}); err != nil {
		return out, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata)
 VALUES($1,'seller_payout.funding_admitted','seller_payout_request',$2,$3,$4,jsonb_build_object('admissionId',$5::text,'reviewId',$6::text))`,
		actorID, requestID, input.Reason, trace, out.Admission.ID, input.ReviewID); err != nil {
		return out, err
	}
	// Return the committed candidate state including its new source evidence.
	out.Request, err = s.scanSellerPayoutReviewItem(actorID, tx.QueryRow(ctx, sellerPayoutReviewSelect+` WHERE r.id=$1`, requestID))
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
