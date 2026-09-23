package payments

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
	"github.com/jackc/pgx/v5"
)

func (s *Service) saveBillingCheckoutRequestTx(ctx context.Context, tx pgx.Tx, checkout BillingCheckout, config persistedProviderConfig, successURL, cancelURL string) error {
	runtime, err := s.runtimes.Runtime(checkout.PaymentMode)
	if err != nil {
		return err
	}
	identity, err := checkoutIdentity(ctx, runtime)
	if err != nil {
		return err
	}
	if identity.LiveMode != checkout.LiveMode || (config.MerchantID != "" && identity.MerchantID != config.MerchantID) ||
		(config.StoreID != "" && identity.StoreID != config.StoreID) {
		return ErrProviderConfigMismatch
	}
	var buyerID uuid.UUID
	var email string
	if err := tx.QueryRow(ctx, `SELECT p.payer_id,u.email FROM payment_intents p JOIN users u ON u.id=p.payer_id AND u.status='active' WHERE p.id=$1`, checkout.PaymentID).Scan(&buyerID, &email); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidCheckout
		}
		return err
	}
	productID, productType := "", "onetime"
	if checkout.Purpose == "subscription" {
		productType = "subscription"
	}
	if checkout.PaymentMode == "waffo_pancake" {
		productID = config.ProductIDOnetime
		if productType == "subscription" {
			productID = config.ProductIDSubscription
		}
	}
	request := CheckoutRequest{PaymentID: checkout.PaymentID, ResourceID: checkout.ResourceID, Purpose: checkout.Purpose,
		Name: "HCAI CHAT " + checkout.Purpose, AmountCents: checkout.AmountCents, Currency: checkout.Currency,
		SuccessURL: billingCheckoutReturnURL(successURL, checkout.PaymentID), CancelURL: cancelURL,
		BuyerIdentity: buyerID.String(), BuyerEmail: email, ProductID: productID, ProductType: productType, OrderExternalID: checkout.PaymentID.String()}
	encodedIdentity, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	encodedRequest, err := json.Marshal(request)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO billing_checkout_requests(payment_id,identity,request) VALUES($1,$2,$3)`, checkout.PaymentID, encodedIdentity, encodedRequest)
	return err
}

const billingCheckoutSelect = `SELECT id,resource_id,purpose,status,checkout_url,checkout_expires_at,amount_cents,currency,live_mode,provider FROM payment_intents`

func scanBillingCheckout(row pgx.Row) (BillingCheckout, error) {
	var item BillingCheckout
	var url *string
	var expiry *time.Time
	err := row.Scan(&item.PaymentID, &item.ResourceID, &item.Purpose, &item.Status, &url, &expiry, &item.AmountCents, &item.Currency, &item.LiveMode, &item.PaymentMode)
	if err != nil {
		return BillingCheckout{}, err
	}
	if url != nil {
		item.CheckoutURL = *url
	}
	if expiry != nil {
		item.ExpiresAt = *expiry
	}
	item.RealCharge = item.LiveMode
	return item, nil
}

func lockBillingCheckoutTx(ctx context.Context, tx pgx.Tx, expected BillingCheckout) (BillingCheckout, error) {
	current, err := scanBillingCheckout(tx.QueryRow(ctx, billingCheckoutSelect+` WHERE id=$1 FOR UPDATE`, expected.PaymentID))
	if err != nil {
		return BillingCheckout{}, err
	}
	if !oneOf(current.Purpose, "wallet_topup", "subscription") || current.Purpose != expected.Purpose || current.ResourceID != expected.ResourceID ||
		current.PaymentMode != expected.PaymentMode || current.AmountCents != expected.AmountCents || current.Currency != expected.Currency || current.LiveMode != expected.LiveMode ||
		!oneOf(current.Status, "checkout_pending", "checkout_open") {
		return BillingCheckout{}, ErrCheckoutConflict
	}
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT COALESCE((CASE WHEN status='checkout_open' THEN
		checkout_url IS NOT NULL AND checkout_url<>'' AND provider_checkout_id IS NOT NULL AND checkout_expires_at>clock_timestamp()
		ELSE checkout_url IS NULL AND provider_checkout_id IS NULL AND checkout_expires_at IS NULL END
		AND provider_payment_id IS NULL AND provider_charge_id IS NULL),false)
		FROM payment_intents WHERE id=$1`, current.PaymentID).Scan(&valid); err != nil {
		return BillingCheckout{}, err
	}
	if !valid {
		return BillingCheckout{}, ErrCheckoutConflict
	}
	return current, nil
}

func loadBillingCheckoutRequestTx(ctx context.Context, tx pgx.Tx, checkout BillingCheckout) (ProductCheckoutIdentity, CheckoutRequest, error) {
	var encodedIdentity, encodedRequest []byte
	var buyer string
	var active, withinRetryWindow bool
	err := tx.QueryRow(ctx, `SELECT r.identity,r.request,p.payer_id::text,u.status='active',
		r.created_at<=clock_timestamp() AND r.created_at>clock_timestamp()-interval '23 hours'
		FROM billing_checkout_requests r JOIN payment_intents p ON p.id=r.payment_id JOIN users u ON u.id=p.payer_id
		WHERE r.payment_id=$1`, checkout.PaymentID).Scan(&encodedIdentity, &encodedRequest, &buyer, &active, &withinRetryWindow)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProductCheckoutIdentity{}, CheckoutRequest{}, ErrCheckoutReconciliation
	}
	if err != nil {
		return ProductCheckoutIdentity{}, CheckoutRequest{}, err
	}
	var identity ProductCheckoutIdentity
	var request CheckoutRequest
	if json.Unmarshal(encodedIdentity, &identity) != nil || json.Unmarshal(encodedRequest, &request) != nil ||
		identity.Provider != checkout.PaymentMode || identity.LiveMode != checkout.LiveMode || identity.MerchantID == "" || identity.Endpoint == "" || identity.APIVersion == "" || identity.RequestVersion == "" ||
		request.PaymentID != checkout.PaymentID || request.ResourceID != checkout.ResourceID || request.Purpose != checkout.Purpose || request.BuyerIdentity != buyer ||
		request.OrderExternalID != checkout.PaymentID.String() || request.AmountCents != checkout.AmountCents || request.Currency != checkout.Currency ||
		(checkout.PaymentMode == "stripe" && checkout.CheckoutURL == "" && !withinRetryWindow) {
		return ProductCheckoutIdentity{}, CheckoutRequest{}, ErrCheckoutReconciliation
	}
	if !active {
		return ProductCheckoutIdentity{}, CheckoutRequest{}, ErrInvalidCheckout
	}
	if checkout.Purpose == "subscription" {
		if _, err := loadSubscriptionContractTx(ctx, tx, checkout.PaymentID); err != nil {
			return ProductCheckoutIdentity{}, CheckoutRequest{}, err
		}
	}
	return identity, request, nil
}

func (s *Service) createBillingProviderCheckout(ctx context.Context, checkout BillingCheckout) (BillingCheckout, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BillingCheckout{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	checkout, err = lockBillingCheckoutTx(ctx, tx, checkout)
	if err != nil {
		return BillingCheckout{}, false, err
	}
	identity, _, err := loadBillingCheckoutRequestTx(ctx, tx, checkout)
	if err != nil {
		return BillingCheckout{}, false, err
	}
	if checkout.CheckoutURL != "" {
		checkout.AlreadyCreated = true
		return checkout, false, tx.Commit(ctx)
	}
	runtime, err := s.runtimes.Runtime(checkout.PaymentMode)
	if err != nil {
		return BillingCheckout{}, false, err
	}
	currentIdentity, err := checkoutIdentity(ctx, runtime)
	if err != nil {
		return BillingCheckout{}, false, err
	}
	if currentIdentity != identity {
		return BillingCheckout{}, false, ErrCheckoutReconciliation
	}
	if err := systemsettings.RequireTx(ctx, tx, systemsettings.Checkout); err != nil {
		return BillingCheckout{}, false, err
	}
	// A Waffo permit belongs only to this invocation. A retry must not infer
	// that an earlier committed reservation never reached the remote server.
	result, err := tx.Exec(ctx, `INSERT INTO billing_checkout_dispatches(payment_id,request_sha256,payment_version)
		SELECT p.id,encode(public.digest(r.request::text,'sha256'),'hex'),p.version
		FROM payment_intents p JOIN billing_checkout_requests r ON r.payment_id=p.id WHERE p.id=$1
		ON CONFLICT(payment_id) DO NOTHING`, checkout.PaymentID)
	if err != nil {
		return BillingCheckout{}, false, err
	}
	if checkout.PaymentMode == "waffo_pancake" && result.RowsAffected() != 1 {
		return BillingCheckout{}, false, ErrCheckoutReconciliation
	}
	if err := tx.Commit(ctx); err != nil {
		return BillingCheckout{}, false, err
	}
	// The dispatch record survives a lost response or a rollback below.
	next, err := s.pool.Begin(ctx)
	if err != nil {
		return BillingCheckout{}, false, err
	}
	tx = next
	checkout, err = lockBillingCheckoutTx(ctx, tx, checkout)
	if err != nil {
		return BillingCheckout{}, false, err
	}
	storedIdentity, request, err := loadBillingCheckoutRequestTx(ctx, tx, checkout)
	if err != nil {
		return BillingCheckout{}, false, err
	}
	if storedIdentity != identity {
		return BillingCheckout{}, false, ErrCheckoutReconciliation
	}
	if checkout.CheckoutURL != "" {
		checkout.AlreadyCreated = true
		return checkout, false, tx.Commit(ctx)
	}
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM billing_checkout_dispatches d
		JOIN billing_checkout_requests r ON r.payment_id=d.payment_id JOIN payment_intents p ON p.id=d.payment_id
		WHERE p.id=$1 AND d.payment_version=p.version AND d.request_sha256=encode(public.digest(r.request::text,'sha256'),'hex'))`, checkout.PaymentID).Scan(&valid); err != nil {
		return BillingCheckout{}, false, err
	}
	if !valid {
		return BillingCheckout{}, false, ErrCheckoutReconciliation
	}
	if err := systemsettings.RequireTx(ctx, tx, systemsettings.Checkout); err != nil {
		return BillingCheckout{}, false, err
	}
	request.CheckoutIdentity = &identity
	dispatchCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	session, err := runtime.CreateCheckout(dispatchCtx, request)
	if err != nil {
		return BillingCheckout{}, false, SanitizeProviderError(err)
	}
	if session.LiveMode != checkout.LiveMode || session.ProviderID == "" || session.CheckoutURL == "" || !session.ExpiresAt.After(time.Now()) {
		return BillingCheckout{}, false, newProviderFailure("payment_response_invalid", 0)
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_intents SET status='checkout_open',provider_checkout_id=$2,checkout_url=$3,checkout_expires_at=$4,updated_at=now(),version=version+1 WHERE id=$1`, checkout.PaymentID, session.ProviderID, session.CheckoutURL, session.ExpiresAt); err != nil {
		return BillingCheckout{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BillingCheckout{}, false, err
	}
	checkout.Status, checkout.CheckoutURL, checkout.ExpiresAt = "checkout_open", session.CheckoutURL, session.ExpiresAt
	return checkout, true, nil
}
