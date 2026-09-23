package payments

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrSellerPayoutBankConflict = errors.New("seller payout bank target conflicts with request")

type SellerPayoutBankTarget struct {
	PayoutRequestID uuid.UUID `json:"payoutRequestId"`
	PayoutBankTarget
	CreatedAt time.Time `json:"createdAt"`
}

// Read the frozen selection, not current bank eligibility. Dispatch must
// independently recheck remote readiness using this original target.
func (s *Service) GetSellerPayoutBankTarget(ctx context.Context, sellerID, requestID uuid.UUID) (SellerPayoutBankTarget, error) {
	if s == nil || s.pool == nil || sellerID == uuid.Nil {
		return SellerPayoutBankTarget{}, ErrDisabled
	}
	return readSellerPayoutBankTarget(ctx, s.pool, sellerID, requestID)
}

func readSellerPayoutBankTarget(ctx context.Context, query interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, sellerID, requestID uuid.UUID) (SellerPayoutBankTarget, error) {
	var out SellerPayoutBankTarget
	err := query.QueryRow(ctx, `SELECT b.payout_request_id,b.destination_id,b.bank_destination_id,b.currency,b.observed_at,b.created_at,COALESCE(b.bank_name,''),COALESCE(b.last4,'')
 FROM seller_payout_bank_targets b JOIN seller_payout_requests r ON r.id=b.payout_request_id
 JOIN users u ON u.id=r.seller_id
 WHERE b.payout_request_id=$1 AND b.seller_id=$2 AND r.seller_id=$2 AND u.status='active'`, requestID, sellerID).
		Scan(&out.PayoutRequestID, &out.DestinationID, &out.BankDestinationID, &out.Currency, &out.ObservedAt, &out.CreatedAt, &out.BankName, &out.Last4)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrSellerPayoutNotFound
	}
	return out, err
}

type sellerPayoutBankState struct {
	Input       PayoutBankTargetRequest
	Settlement  ProductSettlement
	AmountCents int
	Status      string
}

// The preliminary lookup never locks seller funds ahead of the payment.
// Refund, cancellation and source dispatch use the same serialization order.
func lockSellerPayoutBankRequest(ctx context.Context, tx pgx.Tx, sellerID, requestID uuid.UUID) (uuid.UUID, error) {
	var settlement uuid.UUID
	err := tx.QueryRow(ctx, `SELECT a.settlement_id FROM seller_payout_requests r
 JOIN seller_payout_request_allocations a ON a.payout_request_id=r.id
 WHERE r.id=$1 AND r.seller_id=$2`, requestID, sellerID).Scan(&settlement)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrSellerPayoutNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	if err := lockProductSettlementPaymentTx(ctx, tx, settlement); err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM seller_payout_requests WHERE id=$1 AND seller_id=$2 FOR UPDATE`, requestID, sellerID).Scan(&id); err != nil {
		return uuid.Nil, err
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 AND status='active' FOR SHARE`, sellerID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrSellerPayoutNotFound
		}
		return uuid.Nil, err
	}
	return settlement, nil
}

func sellerPayoutBankStateTx(ctx context.Context, tx pgx.Tx, sellerID, requestID, settlementID uuid.UUID, bankID string) (sellerPayoutBankState, error) {
	state := sellerPayoutBankState{Input: PayoutBankTargetRequest{BankDestinationID: bankID}}
	var status, mode string
	var destination string
	if err := tx.QueryRow(ctx, `SELECT destination_id FROM payment_destinations WHERE provider='stripe' AND user_id=$1 FOR SHARE`, sellerID).Scan(&destination); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return state, ErrSellerPayoutBankConflict
		}
		return state, err
	}
	if err := tx.QueryRow(ctx, `SELECT payout_mode FROM product_settlement_settings WHERE singleton=true FOR SHARE`).Scan(&mode); err != nil {
		return state, err
	}
	if mode != "seller_payout" {
		return state, ErrSellerPayoutModeDisabled
	}
	err := tx.QueryRow(ctx, `SELECT s.id,s.payment_id,s.provider,s.live_mode,s.gross_amount_cents,s.currency,s.seller_id,r.amount_cents,r.status
 FROM seller_payout_requests r JOIN seller_payout_request_allocations a ON a.payout_request_id=r.id AND a.released_at IS NULL
 JOIN product_settlements s ON s.id=a.settlement_id JOIN payment_intents p ON p.id=s.payment_id
 JOIN orders o ON o.id=s.order_id
 WHERE r.id=$1 AND r.seller_id=$2 AND s.id=$3 AND s.seller_id=$2
 AND s.status='available' AND s.available_at<=clock_timestamp() AND s.provider='stripe'
 AND s.net_amount_cents=r.amount_cents AND a.amount_cents=r.amount_cents AND r.currency=s.currency
 AND p.status='paid' AND o.status='fulfilled'
 AND NOT EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=r.id)
 AND NOT EXISTS(SELECT 1 FROM product_settlement_dispatches WHERE settlement_id=s.id AND reserved_at IS NOT NULL)
 AND NOT seller_funds_recovery_blocks($2,s.id)
 FOR UPDATE OF s`, requestID, sellerID, settlementID).Scan(&state.Settlement.ID, &state.Settlement.PaymentID,
		&state.Settlement.Provider, &state.Settlement.LiveMode, &state.Settlement.GrossAmountCents, &state.Settlement.Currency,
		&state.Settlement.SellerID, &state.AmountCents, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, ErrSellerPayoutBankConflict
	}
	if err != nil {
		return state, err
	}
	if !oneOf(status, "requested", "under_review") {
		return state, ErrSellerPayoutBankConflict
	}
	if err := validateProductSettlementFundsTx(ctx, tx, state.Settlement); err != nil {
		return state, err
	}
	identity, _, err := readOriginalProductPaymentIdentity(ctx, tx, state.Settlement.PaymentID)
	if err != nil {
		return state, err
	}
	matched, err := destinationIdentityMatchesTx(ctx, tx, "stripe", sellerID, destination, identity)
	if err != nil {
		return state, err
	}
	var eligible bool
	if err := tx.QueryRow(ctx, `SELECT status='verified' AND charges_enabled AND payouts_enabled FROM payment_destinations WHERE provider='stripe' AND user_id=$1`, sellerID).Scan(&eligible); err != nil {
		return state, err
	}
	if !matched || !eligible || identity.Provider != "stripe" || identity.LiveMode != state.Settlement.LiveMode {
		return state, ErrSellerPayoutBankConflict
	}
	state.Input.DestinationID, state.Input.Currency, state.Input.Identity = destination, state.Settlement.Currency, identity
	state.Status = status
	return state, nil
}

// Bind once per request. A changed bank requires cancelling an unstarted
// request and creating a new one; old financial evidence is never rewritten.
func (s *Service) BindSellerPayoutBankTarget(ctx context.Context, sellerID, requestID uuid.UUID, bankID string) (SellerPayoutBankTarget, error) {
	if s == nil || s.pool == nil || sellerID == uuid.Nil {
		return SellerPayoutBankTarget{}, ErrDisabled
	}
	if requestID == uuid.Nil || !validStripeID(bankID, "ba_") {
		return SellerPayoutBankTarget{}, ErrInvalidSellerPayout
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SellerPayoutBankTarget{}, err
	}
	defer tx.Rollback(context.Background())
	settlementID, err := lockSellerPayoutBankRequest(ctx, tx, sellerID, requestID)
	if err != nil {
		return SellerPayoutBankTarget{}, err
	}
	existing, err := readSellerPayoutBankTarget(ctx, tx, sellerID, requestID)
	if err == nil {
		if existing.BankDestinationID != bankID {
			return SellerPayoutBankTarget{}, ErrSellerPayoutBankConflict
		}
		return existing, nil
	}
	if !errors.Is(err, ErrSellerPayoutNotFound) {
		return SellerPayoutBankTarget{}, err
	}
	if !s.config.Enabled {
		return SellerPayoutBankTarget{}, ErrDisabled
	}
	state, err := sellerPayoutBankStateTx(ctx, tx, sellerID, requestID, settlementID, bankID)
	if err != nil {
		return SellerPayoutBankTarget{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SellerPayoutBankTarget{}, err
	}
	runtime, err := s.runtimes.Runtime("stripe")
	if err != nil {
		return SellerPayoutBankTarget{}, err
	}
	reader, ok := runtime.(SellerPayoutBankReader)
	if !ok {
		return SellerPayoutBankTarget{}, ErrProviderUnavailable
	}
	started := time.Now()
	observed, err := reader.ReadPayoutBankTarget(ctx, state.Input)
	if err != nil {
		return SellerPayoutBankTarget{}, err
	}
	if err := ctx.Err(); err != nil {
		return SellerPayoutBankTarget{}, err
	}
	if !validPayoutBankOption(PayoutBankOption{BankDestinationID: observed.BankDestinationID, BankName: observed.BankName, Last4: observed.Last4, Currency: observed.Currency}) || observed.DestinationID != state.Input.DestinationID || observed.BankDestinationID != bankID ||
		observed.Currency != state.Input.Currency || observed.ObservedAt.Before(started) || observed.ObservedAt.After(time.Now()) {
		return SellerPayoutBankTarget{}, ErrSellerPayoutBankConflict
	}
	// No database locks are held during the remote read. Recheck all mutable
	// facts after reacquiring locks; cancellation/revocation wins if committed.
	tx, err = s.pool.Begin(ctx)
	if err != nil {
		return SellerPayoutBankTarget{}, err
	}
	defer tx.Rollback(context.Background())
	if _, err := lockSellerPayoutBankRequest(ctx, tx, sellerID, requestID); err != nil {
		return SellerPayoutBankTarget{}, err
	}
	existing, err = readSellerPayoutBankTarget(ctx, tx, sellerID, requestID)
	if err == nil {
		if existing.BankDestinationID != bankID {
			return SellerPayoutBankTarget{}, ErrSellerPayoutBankConflict
		}
		return existing, nil
	}
	if !errors.Is(err, ErrSellerPayoutNotFound) {
		return SellerPayoutBankTarget{}, err
	}
	current, err := sellerPayoutBankStateTx(ctx, tx, sellerID, requestID, settlementID, bankID)
	if err != nil {
		return SellerPayoutBankTarget{}, err
	}
	if current.Input != state.Input || current.AmountCents != state.AmountCents {
		return SellerPayoutBankTarget{}, ErrSellerPayoutBankConflict
	}
	identity, err := json.Marshal(state.Input.Identity)
	if err != nil {
		return SellerPayoutBankTarget{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO seller_payout_bank_targets(payout_request_id,seller_id,settlement_id,payment_id,provider_identity,
 destination_id,bank_destination_id,amount_cents,currency,observed_at,bank_name,last4) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		requestID, sellerID, settlementID, state.Settlement.PaymentID, identity, observed.DestinationID, bankID, state.AmountCents, observed.Currency, observed.ObservedAt, observed.BankName, observed.Last4)
	if err != nil {
		return SellerPayoutBankTarget{}, err
	}
	if err := recordSellerPayoutEventTx(ctx, tx, requestID, "payout.bank_bound", &current.Status, &current.Status, "bank-bound:"+requestID.String(), map[string]any{
		"destinationId": observed.DestinationID, "bankDestinationId": bankID, "observedAt": observed.ObservedAt,
	}); err != nil {
		return SellerPayoutBankTarget{}, err
	}
	out, err := readSellerPayoutBankTarget(ctx, tx, sellerID, requestID)
	if err != nil {
		return SellerPayoutBankTarget{}, err
	}
	return out, tx.Commit(ctx)
}
