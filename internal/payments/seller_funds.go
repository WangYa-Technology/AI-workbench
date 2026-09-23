package payments

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/sellerfunds"
	"github.com/jackc/pgx/v5"
)

var (
	ErrSellerFundsReconciliation  = errors.New("seller funds scope requires reconciliation")
	ErrSellerFundsInsufficient    = errors.New("seller funds insufficient")
	ErrSellerFundsRecoveryDue     = errors.New("seller recovery obligation outstanding")
	ErrSellerPayoutConflict       = errors.New("seller payout idempotency conflict")
	ErrInvalidSellerPayout        = errors.New("invalid seller payout request")
	ErrSellerPayoutNotCancellable = errors.New("seller payout request is not cancellable")
	ErrSellerPayoutNotFound       = errors.New("seller payout request not found")
	ErrSellerPayoutModeDisabled   = errors.New("seller payout mode is not enabled")
	ErrSellerPayoutAllocation     = errors.New("seller payout amount must match complete available settlements")
)

// Money is never totaled across merchant/provider/environment boundaries.
type SellerFundsBalance struct {
	SellerID          uuid.UUID            `json:"sellerId"`
	Accounts          []SellerFundsAccount `json:"accounts"`
	UnresolvedRecords int                  `json:"unresolvedRecords"`
	AsOf              time.Time            `json:"asOf"`
}

type SellerFundsAccount struct {
	AccountID         string `json:"accountId"`
	Provider          string `json:"provider"`
	Environment       string `json:"environment"`
	Currency          string `json:"currency"`
	PendingCents      int    `json:"pendingCents"`
	AvailableCents    int    `json:"availableCents"`
	ReservedCents     int    `json:"reservedCents"`
	RecoveryDueCents  int    `json:"recoveryDueCents"`
	WithdrawableCents int    `json:"withdrawableCents"`
}

type SellerPayoutRequest struct {
	ID             uuid.UUID `json:"id"`
	SellerID       uuid.UUID `json:"sellerId"`
	AmountCents    int       `json:"amountCents"`
	Currency       string    `json:"currency"`
	IdempotencyKey string    `json:"idempotencyKey"`
	Status         string    `json:"status"`
	FailureCode    *string   `json:"failureCode,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

func (s *Service) GetSellerFunds(ctx context.Context, sellerID uuid.UUID) (SellerFundsBalance, error) {
	if s == nil || s.pool == nil || sellerID == uuid.Nil {
		return SellerFundsBalance{}, ErrDisabled
	}
	out := SellerFundsBalance{SellerID: sellerID, Accounts: []SellerFundsAccount{}}
	var accounts []byte
	err := s.pool.QueryRow(ctx, `WITH accounts AS (`+sellerfunds.AccountsQuery+`)
 SELECT COALESCE(jsonb_agg(jsonb_build_object('accountId',a.account_id,'provider',a.provider,
 'environment',CASE WHEN a.live_mode THEN 'live' ELSE 'test' END,'currency',a.currency,
 'pendingCents',a.pending_cents,'availableCents',a.available_cents,'reservedCents',a.reserved_cents,
 'recoveryDueCents',a.recovery_due_cents,'withdrawableCents',a.withdrawable_cents)
 ORDER BY a.live_mode DESC,a.provider,a.account_id),'[]'::jsonb),
 (SELECT count(*) FROM seller_funds_unscoped WHERE seller_id=$1),transaction_timestamp()
 FROM accounts a`, sellerID).Scan(&accounts, &out.UnresolvedRecords, &out.AsOf)
	if err != nil {
		return SellerFundsBalance{}, err
	}
	if err := json.Unmarshal(accounts, &out.Accounts); err != nil {
		return SellerFundsBalance{}, err
	}
	return out, nil
}

// Payment writers and new payout requests lock payment/order before seller
// funds, then settlements. Cancellation needs only the seller/request locks.
func lockSellerFundsTx(ctx context.Context, tx pgx.Tx, sellerID uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, sellerID.String())
	return err
}

// ProjectSellerSettlementCredit records one immutable ledger credit. It is
// safe to call from fulfillment/reconciliation replays because settlement_id
// and the idempotency key are both unique.
func (s *Service) ProjectSellerSettlementCredit(ctx context.Context, settlementID uuid.UUID) error {
	if s == nil || s.pool == nil || settlementID == uuid.Nil {
		return ErrDisabled
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var seller uuid.UUID
	var amount int
	var available time.Time
	var status, currency string
	err = tx.QueryRow(ctx, `SELECT seller_id,net_amount_cents,available_at,status,currency FROM product_settlements WHERE id=$1 FOR SHARE`, settlementID).
		Scan(&seller, &amount, &available, &status, &currency)
	if err != nil {
		return err
	}
	if currency != "USD" || amount <= 0 || status == "cancelled" || status == "refund_hold" {
		return nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO seller_ledger_entries(seller_id,entry_type,amount_cents,currency,settlement_id,idempotency_key,available_at,evidence)
VALUES($1,'settlement_credit',$2,$3,$4,$5,$6,jsonb_build_object('status',$7::text)) ON CONFLICT DO NOTHING`, seller, amount, currency, settlementID, "settlement-credit:"+settlementID.String(), available, status)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) CreateSellerPayoutRequestForSettlement(ctx context.Context, sellerID, settlementID uuid.UUID, amountCents int, key string) (SellerPayoutRequest, error) {
	if settlementID == uuid.Nil {
		return SellerPayoutRequest{}, ErrInvalidSellerPayout
	}
	return s.createSellerPayoutRequest(ctx, sellerID, settlementID, amountCents, key)
}

func (s *Service) createSellerPayoutRequest(ctx context.Context, sellerID, settlementID uuid.UUID, amountCents int, idempotencyKey string) (SellerPayoutRequest, error) {
	if s == nil || s.pool == nil || sellerID == uuid.Nil {
		return SellerPayoutRequest{}, ErrDisabled
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if settlementID == uuid.Nil || amountCents <= 0 || len(idempotencyKey) < 8 || len(idempotencyKey) > 160 {
		return SellerPayoutRequest{}, ErrInvalidSellerPayout
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SellerPayoutRequest{}, err
	}
	defer tx.Rollback(context.Background())
	// On replay, lock the original payment even if the caller changed the
	// selection. Recheck the command under the seller lock below, so changed
	// or nonexistent selections still produce the same idempotency conflict.
	lockSettlementID := settlementID
	var originalSettlementID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT a.settlement_id FROM seller_payout_requests r
 JOIN seller_payout_request_allocations a ON a.payout_request_id=r.id
 WHERE r.seller_id=$1 AND r.idempotency_key=$2`, sellerID, idempotencyKey).Scan(&originalSettlementID)
	if err == nil {
		lockSettlementID = originalSettlementID
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return SellerPayoutRequest{}, err
	} else if !s.config.Enabled {
		// New commands fail at the feature gate; existing commands can still
		// be recovered below after the provider has been disabled.
		return SellerPayoutRequest{}, ErrDisabled
	}
	// Serialize with refunds before taking the seller lock. The owner predicate
	// prevents a foreign settlement ID from locking another seller's payment.
	var owner uuid.UUID
	err = tx.QueryRow(ctx, `SELECT ps.seller_id FROM product_settlements ps
 JOIN payment_intents p ON p.id=ps.payment_id JOIN orders o ON o.id=ps.order_id AND p.order_id=o.id
 WHERE ps.id=$1 AND ps.seller_id=$2 FOR UPDATE OF p,o`, lockSettlementID, sellerID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return SellerPayoutRequest{}, ErrSellerPayoutAllocation
	}
	if err != nil {
		return SellerPayoutRequest{}, err
	}
	if err = lockSellerFundsTx(ctx, tx, sellerID); err != nil {
		return SellerPayoutRequest{}, err
	}
	var activeID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 AND status='active' FOR SHARE`, sellerID).Scan(&activeID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SellerPayoutRequest{}, ErrSellerPayoutNotFound
		}
		return SellerPayoutRequest{}, err
	}
	var existing SellerPayoutRequest
	err = tx.QueryRow(ctx, `SELECT id,seller_id,amount_cents,currency,idempotency_key,status,failure_code,created_at,updated_at FROM seller_payout_requests WHERE seller_id=$1 AND idempotency_key=$2`, sellerID, idempotencyKey).
		Scan(&existing.ID, &existing.SellerID, &existing.AmountCents, &existing.Currency, &existing.IdempotencyKey, &existing.Status, &existing.FailureCode, &existing.CreatedAt, &existing.UpdatedAt)
	if err == nil {
		if existing.AmountCents != amountCents {
			return SellerPayoutRequest{}, ErrSellerPayoutConflict
		}
		var matches bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM seller_payout_request_allocations WHERE payout_request_id=$1 AND settlement_id=$2)`, existing.ID, settlementID).Scan(&matches); err != nil {
			return SellerPayoutRequest{}, err
		}
		if !matches {
			return SellerPayoutRequest{}, ErrSellerPayoutConflict
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return SellerPayoutRequest{}, err
	}
	if !s.config.Enabled {
		return SellerPayoutRequest{}, ErrDisabled
	}
	var available int
	var recovery, unresolved bool
	err = tx.QueryRow(ctx, `SELECT
    COALESCE((SELECT SUM(e.amount_cents) FROM seller_ledger_entries e JOIN product_settlements ps ON ps.id=e.settlement_id
      WHERE e.seller_id=$1 AND ps.seller_id=$1 AND ps.id=$2 AND e.entry_type='settlement_credit' AND e.currency=ps.currency
      AND e.amount_cents=ps.net_amount_cents AND e.available_at<=clock_timestamp() AND ps.status='available'),0)
    - COALESCE((SELECT SUM(e.amount_cents) FROM seller_ledger_entries e JOIN seller_payout_requests r ON r.id=e.payout_request_id
      JOIN seller_payout_request_allocations a ON a.payout_request_id=r.id
      WHERE e.seller_id=$1 AND r.seller_id=$1 AND a.settlement_id=$2 AND e.entry_type='payout_reservation'
      AND r.status IN ('requested','under_review','processing','reconciliation_required')),0),
    seller_funds_recovery_blocks($1,$2), EXISTS(SELECT 1 FROM seller_funds_unscoped WHERE seller_id=$1)`, sellerID, settlementID).Scan(&available, &recovery, &unresolved)
	if err != nil {
		return SellerPayoutRequest{}, err
	}
	if unresolved {
		return SellerPayoutRequest{}, ErrSellerFundsReconciliation
	}
	if recovery {
		return SellerPayoutRequest{}, ErrSellerFundsRecoveryDue
	}
	if available < amountCents {
		return SellerPayoutRequest{}, ErrSellerFundsInsufficient
	}
	var payoutMode string
	if err := tx.QueryRow(ctx, `SELECT payout_mode FROM product_settlement_settings WHERE singleton=true FOR SHARE`).Scan(&payoutMode); err != nil {
		return SellerPayoutRequest{}, err
	}
	// Automatic settlement already sends each available settlement through the
	// provider. Refuse a second funds outlet until the dedicated seller payout
	// dispatcher is enabled; accepting the request here would reserve money that
	// can also be transferred by HandleProductSettlementJob.
	if payoutMode != "seller_payout" {
		return SellerPayoutRequest{}, ErrSellerPayoutModeDisabled
	}
	// A provider transfer is tied to one original charge. Until partial and
	// aggregate seller payouts have their own immutable accounting model, one
	// request must match one complete available settlement exactly.
	var allocationID uuid.UUID
	var allocationAmount int
	if err := tx.QueryRow(ctx, `
		SELECT e.settlement_id,e.amount_cents
		FROM seller_ledger_entries e
		JOIN product_settlements ps ON ps.id=e.settlement_id
		WHERE e.seller_id=$1 AND e.entry_type='settlement_credit' AND e.currency='USD'
		  AND e.amount_cents=$2 AND e.available_at<=clock_timestamp() AND ps.status='available'
		  AND ps.id=$3
		  AND ps.available_at<=clock_timestamp()
		  AND NOT EXISTS (SELECT 1 FROM product_settlement_dispatches WHERE settlement_id=ps.id AND reserved_at IS NOT NULL)
		  AND NOT seller_settlement_has_open_source(ps.id)
		  AND NOT EXISTS (
			SELECT 1 FROM seller_payout_request_allocations a
			WHERE a.settlement_id=e.settlement_id AND a.released_at IS NULL
		  )
		ORDER BY e.available_at,e.settlement_id
		LIMIT 1 FOR UPDATE OF e,ps`, sellerID, amountCents, settlementID).Scan(&allocationID, &allocationAmount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SellerPayoutRequest{}, ErrSellerPayoutAllocation
		}
		return SellerPayoutRequest{}, err
	}
	if allocationAmount != amountCents {
		return SellerPayoutRequest{}, ErrSellerPayoutAllocation
	}
	var out SellerPayoutRequest
	err = tx.QueryRow(ctx, `INSERT INTO seller_payout_requests(seller_id,amount_cents,currency,idempotency_key,status) VALUES($1,$2,'USD',$3,'under_review') RETURNING id,seller_id,amount_cents,currency,idempotency_key,status,failure_code,created_at,updated_at`, sellerID, amountCents, idempotencyKey).
		Scan(&out.ID, &out.SellerID, &out.AmountCents, &out.Currency, &out.IdempotencyKey, &out.Status, &out.FailureCode, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return SellerPayoutRequest{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO seller_payout_request_allocations(payout_request_id,settlement_id,amount_cents) VALUES($1,$2,$3)`, out.ID, allocationID, allocationAmount); err != nil {
		return SellerPayoutRequest{}, err
	}
	// The directory is advisory. Reuse the bank-binding eligibility check while
	// payment/order, seller and allocation locks are held, before reserving funds.
	// This performs local evidence checks only; no provider request is made.
	if _, err := sellerPayoutBankStateTx(ctx, tx, sellerID, out.ID, allocationID, ""); err != nil {
		if errors.Is(err, ErrSellerPayoutBankConflict) || errors.Is(err, ErrCheckoutReconciliation) || errors.Is(err, pgx.ErrNoRows) {
			return SellerPayoutRequest{}, ErrSellerPayoutAllocation
		}
		return SellerPayoutRequest{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO seller_ledger_entries(seller_id,entry_type,amount_cents,currency,payout_request_id,idempotency_key,evidence) VALUES($1,'payout_reservation',$2,'USD',$3,$4,jsonb_build_object('status','under_review'))`, sellerID, amountCents, out.ID, "payout-reservation:"+out.ID.String())
	if err != nil {
		return SellerPayoutRequest{}, err
	}
	initialStatus := out.Status
	if err := recordSellerPayoutEventTx(ctx, tx, out.ID, "payout.requested", nil, &initialStatus, "request:"+out.ID.String(), map[string]any{"amountCents": amountCents}); err != nil {
		return SellerPayoutRequest{}, err
	}
	return out, tx.Commit(ctx)
}

func recordSellerPayoutEventTx(ctx context.Context, tx pgx.Tx, requestID uuid.UUID, eventType string, from, to *string, eventKey string, evidence map[string]any) error {
	body, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO seller_payout_request_events(payout_request_id,event_type,from_status,to_status,event_key,evidence) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, requestID, eventType, from, to, eventKey, body)
	return err
}

func (s *Service) CancelSellerPayoutRequest(ctx context.Context, sellerID, requestID uuid.UUID) (SellerPayoutRequest, error) {
	if s == nil || s.pool == nil || sellerID == uuid.Nil || requestID == uuid.Nil {
		return SellerPayoutRequest{}, ErrDisabled
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SellerPayoutRequest{}, err
	}
	defer tx.Rollback(context.Background())
	if err := lockSellerFundsTx(ctx, tx, sellerID); err != nil {
		return SellerPayoutRequest{}, err
	}
	out, err := cancelSellerPayoutRequestTx(ctx, tx, sellerID, requestID)
	if err != nil {
		return SellerPayoutRequest{}, err
	}
	return out, tx.Commit(ctx)
}

// The caller holds the seller lock. Both seller cancellation and a finance
// rejection use the same release and dispatch-evidence checks in one transaction.
func cancelSellerPayoutRequestTx(ctx context.Context, tx pgx.Tx, sellerID, requestID uuid.UUID) (SellerPayoutRequest, error) {
	var out SellerPayoutRequest
	if err := tx.QueryRow(ctx, `SELECT id,seller_id,amount_cents,currency,idempotency_key,status,failure_code,created_at,updated_at FROM seller_payout_requests WHERE id=$1 AND seller_id=$2 FOR UPDATE`, requestID, sellerID).
		Scan(&out.ID, &out.SellerID, &out.AmountCents, &out.Currency, &out.IdempotencyKey, &out.Status, &out.FailureCode, &out.CreatedAt, &out.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SellerPayoutRequest{}, ErrSellerPayoutNotFound
		}
		return SellerPayoutRequest{}, err
	}
	if out.Status == "cancelled" {
		return out, nil
	}
	if out.Status != "requested" && out.Status != "under_review" {
		return SellerPayoutRequest{}, ErrSellerPayoutNotCancellable
	}
	var dispatched bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=$1)`, requestID).Scan(&dispatched); err != nil {
		return SellerPayoutRequest{}, err
	}
	if dispatched {
		return SellerPayoutRequest{}, ErrSellerPayoutNotCancellable
	}
	from, to := out.Status, "cancelled"
	if _, err := tx.Exec(ctx, `UPDATE seller_payout_requests SET status=$2,updated_at=clock_timestamp() WHERE id=$1`, requestID, to); err != nil {
		return SellerPayoutRequest{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO seller_ledger_entries(seller_id,entry_type,amount_cents,currency,payout_request_id,idempotency_key,evidence) VALUES($1,'payout_release',$2,'USD',$3,$4,jsonb_build_object('fromStatus',$5::text,'toStatus','cancelled')) ON CONFLICT DO NOTHING`, sellerID, out.AmountCents, requestID, "payout-release:"+requestID.String(), from); err != nil {
		return SellerPayoutRequest{}, err
	}
	if err := recordSellerPayoutEventTx(ctx, tx, requestID, "payout.cancelled", &from, &to, "cancelled:"+requestID.String(), map[string]any{"releasedCents": out.AmountCents}); err != nil {
		return SellerPayoutRequest{}, err
	}
	out.Status = to
	out.UpdatedAt = time.Now().UTC()
	return out, nil
}
