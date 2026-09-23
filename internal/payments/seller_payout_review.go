package payments

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrSellerPayoutReviewInvalid  = errors.New("invalid seller payout review")
	ErrSellerPayoutReviewConflict = errors.New("seller payout review changed or is no longer actionable")
	sellerPayoutReviewKey         = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)
)

type SellerPayoutReviewInput struct {
	ExpectedRevision  int       `json:"expectedRevision"`
	SettlementID      uuid.UUID `json:"settlementId"`
	AmountCents       int       `json:"amountCents"`
	BankDestinationID string    `json:"bankDestinationId"`
	Decision          string    `json:"decision"`
	Reason            string    `json:"reason"`
	SellerMessage     string    `json:"sellerMessage"`
}

type SellerPayoutReview struct {
	ID              uuid.UUID `json:"id"`
	PayoutRequestID uuid.UUID `json:"payoutRequestId"`
	Revision        int       `json:"revision"`
	ActorID         uuid.UUID `json:"actorId"`
	Decision        string    `json:"decision"`
	Reason          string    `json:"reason"`
	SellerMessage   string    `json:"sellerMessage,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
}

type SellerPayoutFundingSummary struct {
	TransferID         uuid.UUID  `json:"transferId"`
	Status             string     `json:"status"`
	AdmissionID        *uuid.UUID `json:"admissionId,omitempty"`
	JobID              *uuid.UUID `json:"jobId,omitempty"`
	JobStatus          *string    `json:"jobStatus,omitempty"`
	StartedAt          *time.Time `json:"startedAt,omitempty"`
	ProviderTransferID *string    `json:"providerTransferId,omitempty"`
}

type SellerPayoutReviewItem struct {
	CanAdmitFunding   bool                        `json:"canAdmitFunding"`
	Funding           *SellerPayoutFundingSummary `json:"funding,omitempty"`
	BankName          string                      `json:"bankName,omitempty"`
	Last4             string                      `json:"last4,omitempty"`
	ID                uuid.UUID                   `json:"id"`
	SellerID          uuid.UUID                   `json:"sellerId"`
	SettlementID      uuid.UUID                   `json:"settlementId"`
	AmountCents       int                         `json:"amountCents"`
	Currency          string                      `json:"currency"`
	Status            string                      `json:"status"`
	Environment       string                      `json:"environment"`
	BankDestinationID string                      `json:"bankDestinationId"`
	CreatedAt         time.Time                   `json:"createdAt"`
	LatestReview      *SellerPayoutReview         `json:"latestReview,omitempty"`
}

type SellerPayoutReviewPage struct {
	Items      []SellerPayoutReviewItem `json:"items"`
	NextCursor string                   `json:"nextCursor,omitempty"`
}

type SellerPayoutReviewResult struct {
	Review   SellerPayoutReview     `json:"review"`
	Request  SellerPayoutReviewItem `json:"request"`
	Replayed bool                   `json:"replayed"`
}

const sellerPayoutReviewSelect = `SELECT r.id,r.seller_id,COALESCE(a.settlement_id,'00000000-0000-0000-0000-000000000000'::uuid),
 r.amount_cents,r.currency,r.status,
 CASE WHEN s.id IS NULL THEN 'unknown' WHEN s.live_mode THEN 'live' ELSE 'test' END,
 COALESCE(b.bank_destination_id,''),COALESCE(b.bank_name,''),COALESCE(b.last4,''),r.created_at,
 (SELECT jsonb_build_object('id',v.id,'payoutRequestId',v.payout_request_id,'revision',v.revision,'actorId',v.actor_id,
 'decision',v.decision,'reason',v.reason,'sellerMessage',v.seller_message,'createdAt',v.created_at)
 FROM seller_payout_reviews v WHERE v.payout_request_id=r.id ORDER BY v.revision DESC LIMIT 1),
 (SELECT jsonb_strip_nulls(jsonb_build_object('transferId',t.id,'status',t.status,'admissionId',ad.id,
 'jobId',d.job_id,'jobStatus',j.status,'startedAt',d.started_at,'providerTransferId',t.provider_transfer_id))
 FROM seller_payout_transfers t LEFT JOIN seller_payout_funding_admissions ad ON ad.transfer_id=t.id
 LEFT JOIN seller_payout_funding_dispatches d ON d.transfer_id=t.id LEFT JOIN jobs j ON j.id=d.job_id
 WHERE t.payout_request_id=r.id ORDER BY t.created_at,t.id LIMIT 1),
 EXISTS(SELECT 1 FROM seller_payout_reviews v
 JOIN product_checkout_requests original ON original.payment_id=s.payment_id
 JOIN payment_intents p ON p.id=s.payment_id JOIN orders o ON o.id=s.order_id
 JOIN product_sale_owners own ON own.order_id=o.id AND own.seller_id=r.seller_id::text
 JOIN users u ON u.id=r.seller_id AND u.status='active'
 JOIN payment_destinations pd ON pd.provider='stripe' AND pd.user_id=r.seller_id
 WHERE v.payout_request_id=r.id AND v.decision='approved' AND v.settlement_id=s.id
 AND v.revision=(SELECT max(v2.revision) FROM seller_payout_reviews v2 WHERE v2.payout_request_id=r.id)
 AND r.status IN ('requested','under_review') AND a.released_at IS NULL AND a.amount_cents=r.amount_cents
 AND s.status='available' AND s.available_at<=statement_timestamp() AND s.provider='stripe'
 AND s.net_amount_cents=r.amount_cents AND s.currency=r.currency
 AND b.settlement_id=s.id AND b.seller_id=r.seller_id AND b.payment_id=p.id
 AND b.bank_destination_id=v.bank_destination_id AND b.amount_cents=r.amount_cents AND b.currency=r.currency
 AND b.provider_identity=original.identity AND b.destination_id=pd.destination_id
 AND v.amount_cents=r.amount_cents AND p.status='paid' AND o.status='fulfilled'
 AND p.provider_charge_id ~ '^ch_[A-Za-z0-9_]{6,252}$'
 AND p.order_id=o.id AND p.resource_id=o.product_id AND p.payer_id=o.buyer_id
 AND p.payee_id=r.seller_id AND p.provider=s.provider AND p.live_mode=s.live_mode
 AND p.amount_cents=s.gross_amount_cents AND p.amount_cents=o.amount_cents AND p.currency=s.currency AND p.currency=o.currency
 AND pd.status='verified' AND pd.charges_enabled AND pd.payouts_enabled
 AND pd.original_merchant_id=original.identity->>'merchantId' AND pd.original_live_mode=s.live_mode
 AND COALESCE(pd.original_store_id,'')=COALESCE(original.identity->>'storeId','')
 AND pd.original_endpoint=original.identity->>'endpoint' AND pd.original_api_version=original.identity->>'apiVersion'
 AND pd.original_request_version=original.identity->>'requestVersion'
 AND EXISTS(SELECT 1 FROM product_settlement_settings WHERE singleton=true AND payout_mode='seller_payout')
 AND NOT EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=r.id)
 AND NOT seller_settlement_has_open_source(s.id)
 AND NOT EXISTS(SELECT 1 FROM product_settlement_dispatches WHERE settlement_id=s.id AND reserved_at IS NOT NULL)
 AND NOT EXISTS(SELECT 1 FROM product_refund_review WHERE payment_id=p.id)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_lookup_review WHERE payment_id=p.id)
 AND NOT seller_funds_recovery_blocks(r.seller_id,s.id))
 FROM seller_payout_requests r LEFT JOIN seller_payout_request_allocations a ON a.payout_request_id=r.id
 LEFT JOIN product_settlements s ON s.id=a.settlement_id AND s.seller_id=r.seller_id
 LEFT JOIN seller_payout_bank_targets b ON b.payout_request_id=r.id`

func (s *Service) scanSellerPayoutReviewItem(actorID uuid.UUID, row pgx.Row) (SellerPayoutReviewItem, error) {
	var out SellerPayoutReviewItem
	var review, funding []byte
	err := row.Scan(&out.ID, &out.SellerID, &out.SettlementID, &out.AmountCents, &out.Currency, &out.Status, &out.Environment, &out.BankDestinationID, &out.BankName, &out.Last4, &out.CreatedAt, &review, &funding, &out.CanAdmitFunding)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrSellerPayoutNotFound
	}
	if err != nil {
		return out, err
	}
	if review != nil {
		if err := json.Unmarshal(review, &out.LatestReview); err != nil {
			return out, err
		}
	}
	if funding != nil {
		if err := json.Unmarshal(funding, &out.Funding); err != nil {
			return out, err
		}
	}
	out.CanAdmitFunding = out.CanAdmitFunding && s.config.Enabled && out.SellerID != actorID
	return out, nil
}

func (s *Service) SellerPayoutReviewDirectory(ctx context.Context, actorID uuid.UUID, cursor string, limit int) (SellerPayoutReviewPage, error) {
	out := SellerPayoutReviewPage{Items: []SellerPayoutReviewItem{}}
	if s == nil || s.pool == nil {
		return out, ErrDisabled
	}
	after, limit, err := payoutPageLimit(cursor, limit)
	if err != nil {
		return out, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	if err := paymentFinanceAuthority(ctx, tx, actorID, true); err != nil {
		return out, err
	}
	var afterTime time.Time
	if after != uuid.Nil {
		if err := tx.QueryRow(ctx, `SELECT created_at FROM seller_payout_requests WHERE id=$1`, after).Scan(&afterTime); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return out, ErrSellerPayoutFilter
			}
			return out, err
		}
	}
	rows, err := tx.Query(ctx, sellerPayoutReviewSelect+` WHERE ($1::uuid='00000000-0000-0000-0000-000000000000'::uuid OR (r.created_at,r.id)<($2::timestamptz,$1::uuid)) ORDER BY r.created_at DESC,r.id DESC LIMIT $3`, after, afterTime, limit+1)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		item, err := s.scanSellerPayoutReviewItem(actorID, rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = out.Items[limit-1].ID.String()
	}
	return out, tx.Commit(ctx)
}

func (s *Service) GetSellerPayoutReview(ctx context.Context, actorID, requestID uuid.UUID) (SellerPayoutReviewItem, error) {
	if s == nil || s.pool == nil {
		return SellerPayoutReviewItem{}, ErrDisabled
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SellerPayoutReviewItem{}, err
	}
	defer tx.Rollback(context.Background())
	if err := paymentFinanceAuthority(ctx, tx, actorID, true); err != nil {
		return SellerPayoutReviewItem{}, err
	}
	return s.scanSellerPayoutReviewItem(actorID, tx.QueryRow(ctx, sellerPayoutReviewSelect+` WHERE r.id=$1`, requestID))
}

// Review records an immutable finance decision. It does not enqueue funding:
// bank dispatch and confirmation must be admitted separately against this
// decision and current payment, seller and bank authority.
func (s *Service) ReviewSellerPayout(ctx context.Context, actorID, requestID uuid.UUID, input SellerPayoutReviewInput, key, requestTrace string) (SellerPayoutReviewResult, error) {
	out, err := s.reviewSellerPayout(ctx, actorID, requestID, input, key, requestTrace)
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && (pgerr.Code == "40001" || pgerr.Code == "40P01") {
		err = ErrSellerPayoutReviewConflict
	}
	return out, err
}

func (s *Service) reviewSellerPayout(ctx context.Context, actorID, requestID uuid.UUID, input SellerPayoutReviewInput, key, requestTrace string) (SellerPayoutReviewResult, error) {
	var out SellerPayoutReviewResult
	if s == nil || s.pool == nil {
		return out, ErrDisabled
	}
	input.Reason = strings.TrimSpace(input.Reason)
	input.SellerMessage = strings.TrimSpace(input.SellerMessage)
	if actorID == uuid.Nil || requestID == uuid.Nil || !sellerPayoutReviewKey.MatchString(key) || input.ExpectedRevision < 0 ||
		input.ExpectedRevision >= 2147483647 || input.SettlementID == uuid.Nil || input.AmountCents <= 0 ||
		!oneOf(input.Decision, "approved", "rejected") || !utf8.ValidString(input.Reason) || strings.ContainsRune(input.Reason, 0) || utf8.RuneCountInString(input.Reason) < 10 || utf8.RuneCountInString(input.Reason) > 1000 ||
		(input.BankDestinationID != "" && !validStripeID(input.BankDestinationID, "ba_")) || (input.Decision == "approved" && input.BankDestinationID == "") {
		return out, ErrSellerPayoutReviewInvalid
	}
	if input.SellerMessage != "" && (!utf8.ValidString(input.SellerMessage) || strings.ContainsRune(input.SellerMessage, 0) || utf8.RuneCountInString(input.SellerMessage) < 10 || utf8.RuneCountInString(input.SellerMessage) > 1000) {
		return out, ErrSellerPayoutReviewInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	if err := paymentFinanceAuthority(ctx, tx, actorID, false); err != nil {
		return out, err
	}
	// Operator-scoped key serializes retries even when the target is changed.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "seller-payout-review:"+actorID.String()+":"+key); err != nil {
		return out, err
	}
	var oldSettlement uuid.UUID
	var oldAmount int
	var oldBank string
	err = tx.QueryRow(ctx, `SELECT id,payout_request_id,revision,actor_id,decision,reason,seller_message,created_at,settlement_id,amount_cents,bank_destination_id
 FROM seller_payout_reviews WHERE actor_id=$1 AND idempotency_key=$2`, actorID, key).Scan(&out.Review.ID, &out.Review.PayoutRequestID, &out.Review.Revision, &out.Review.ActorID, &out.Review.Decision, &out.Review.Reason, &out.Review.SellerMessage, &out.Review.CreatedAt, &oldSettlement, &oldAmount, &oldBank)
	if err == nil {
		if out.Review.PayoutRequestID != requestID || out.Review.Revision != input.ExpectedRevision+1 || out.Review.Decision != input.Decision || out.Review.Reason != input.Reason || out.Review.SellerMessage != input.SellerMessage || oldSettlement != input.SettlementID || oldAmount != input.AmountCents || oldBank != input.BankDestinationID {
			return SellerPayoutReviewResult{}, ErrSellerPayoutReviewConflict
		}
		if err := paymentFinanceAuthority(ctx, tx, actorID, true); err != nil {
			return SellerPayoutReviewResult{}, err
		}
		out.Request, err = s.scanSellerPayoutReviewItem(actorID, tx.QueryRow(ctx, sellerPayoutReviewSelect+` WHERE r.id=$1`, requestID))
		out.Replayed = true
		return out, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	// Empty messages are allowed only when replaying a pre-0161 decision. Never
	// expose its internal reason or invent a retrospective public explanation.
	if input.SellerMessage == "" {
		return out, ErrSellerPayoutReviewInvalid
	}
	// Read the immutable allocation, then use the original payment lock order.
	var seller, settlement uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT r.seller_id,a.settlement_id FROM seller_payout_requests r JOIN seller_payout_request_allocations a ON a.payout_request_id=r.id WHERE r.id=$1`, requestID).Scan(&seller, &settlement); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrSellerPayoutNotFound
		}
		return out, err
	}
	if seller == actorID {
		return out, ErrFinanceForbidden
	}
	if err := lockProductSettlementPaymentTx(ctx, tx, settlement); err != nil {
		return out, err
	}
	var locked uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM seller_payout_requests WHERE id=$1 FOR UPDATE`, requestID).Scan(&locked); err != nil {
		return out, err
	}
	item, err := s.scanSellerPayoutReviewItem(actorID, tx.QueryRow(ctx, sellerPayoutReviewSelect+` WHERE r.id=$1`, requestID))
	if err != nil {
		return out, err
	}
	revision := 0
	if item.LatestReview != nil {
		revision = item.LatestReview.Revision
	}
	if revision != input.ExpectedRevision || !oneOf(item.Status, "requested", "under_review") || item.SettlementID != input.SettlementID || item.AmountCents != input.AmountCents || item.BankDestinationID != input.BankDestinationID || (input.Decision == "approved" && item.LatestReview != nil && item.LatestReview.Decision == "approved") {
		return out, ErrSellerPayoutReviewConflict
	}
	var dispatched bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=$1)`, requestID).Scan(&dispatched); err != nil {
		return out, err
	}
	if dispatched {
		return out, ErrSellerPayoutReviewConflict
	}
	if input.Decision == "approved" {
		if !s.config.Enabled {
			return out, ErrDisabled
		}
		if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 AND status='active' FOR SHARE`, seller).Scan(&locked); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return out, ErrSellerPayoutReviewConflict
			}
			return out, err
		}
		state, err := sellerPayoutBankStateTx(ctx, tx, seller, requestID, settlement, input.BankDestinationID)
		if err != nil {
			return out, err
		}
		identity, err := json.Marshal(state.Input.Identity)
		if err != nil {
			return out, err
		}
		var matches bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM seller_payout_bank_targets
 WHERE payout_request_id=$1 AND seller_id=$2 AND settlement_id=$3 AND payment_id=$4
 AND destination_id=$5 AND bank_destination_id=$6 AND amount_cents=$7 AND currency=$8 AND provider_identity=$9::jsonb)`,
			requestID, seller, settlement, state.Settlement.PaymentID, state.Input.DestinationID, input.BankDestinationID, input.AmountCents, item.Currency, identity).Scan(&matches); err != nil {
			return out, err
		}
		if !matches {
			return out, ErrSellerPayoutReviewConflict
		}
	}
	// Revocation during any wait must win. The final check pins both the user
	// row and role-permission mapping through decision, cancellation and audit.
	if err := paymentFinanceAuthority(ctx, tx, actorID, true); err != nil {
		return out, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO seller_payout_reviews(payout_request_id,revision,actor_id,idempotency_key,decision,reason,settlement_id,amount_cents,currency,bank_destination_id,request_id,seller_message)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id,payout_request_id,revision,actor_id,decision,reason,seller_message,created_at`, requestID, revision+1, actorID, key, input.Decision, input.Reason, settlement, input.AmountCents, item.Currency, input.BankDestinationID, requestTrace, input.SellerMessage).
		Scan(&out.Review.ID, &out.Review.PayoutRequestID, &out.Review.Revision, &out.Review.ActorID, &out.Review.Decision, &out.Review.Reason, &out.Review.SellerMessage, &out.Review.CreatedAt)
	if err != nil {
		return out, err
	}
	to := item.Status
	if input.Decision == "rejected" {
		cancelled, err := cancelSellerPayoutRequestTx(ctx, tx, seller, requestID)
		if err != nil {
			return out, err
		}
		to = cancelled.Status
	}
	if err := recordSellerPayoutEventTx(ctx, tx, requestID, "review."+input.Decision, &item.Status, &to, "review:"+out.Review.ID.String(), map[string]any{"reviewId": out.Review.ID, "actorId": actorID, "revision": revision + 1}); err != nil {
		return out, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata)
 VALUES($1,$2,'seller_payout_request',$3,$4,$5,jsonb_build_object('reviewId',$6::text,'revision',$7::integer))`, actorID, "seller_payout.review_"+input.Decision, requestID, input.Reason, requestTrace, out.Review.ID, revision+1); err != nil {
		return out, err
	}
	if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: seller, Kind: "marketplace.payout_reviewed", Title: "Payout review updated",
		Body:       "A review decision is available. Open the request for its current status and explanation. Approval does not send a bank payout.",
		TargetPath: "/workspace/payouts/" + requestID.String(), ResourceType: "seller_payout_request", ResourceID: &requestID,
		SourceKey: "marketplace:payout-review:" + out.Review.ID.String(),
	}); err != nil {
		return out, err
	}
	out.Request, err = s.scanSellerPayoutReviewItem(actorID, tx.QueryRow(ctx, sellerPayoutReviewSelect+` WHERE r.id=$1`, requestID))
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
