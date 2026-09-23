package payments

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrCheckoutReconciliation means retrying might create a different remote
// command. Preserve the order and idempotency key until evidence resolves it.
var ErrCheckoutReconciliation = newProviderFailure("payment_reconciliation_required", 0)

// ProductCheckoutIdentity contains no credential or customer information. The
// runtime authenticates the merchant before an intent can create a checkout.
// Wire parameter changes must introduce a new RequestVersion, so old requests
// cannot silently be replayed through a different serialization contract.
type ProductCheckoutIdentity struct {
	Provider       string `json:"provider"`
	MerchantID     string `json:"merchantId"`
	StoreID        string `json:"storeId,omitempty"`
	LiveMode       bool   `json:"liveMode"`
	Endpoint       string `json:"endpoint"`
	APIVersion     string `json:"apiVersion"`
	RequestVersion string `json:"requestVersion"`
}

type productCheckoutIdentityReader interface {
	ProductCheckoutIdentity(context.Context) (ProductCheckoutIdentity, error)
}

func checkoutIdentity(ctx context.Context, runtime ProviderRuntime) (ProductCheckoutIdentity, error) {
	reader, ok := runtime.(productCheckoutIdentityReader)
	if !ok {
		return ProductCheckoutIdentity{}, newProviderFailure("payment_provider_unsupported", 0)
	}
	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	identity, err := reader.ProductCheckoutIdentity(readCtx)
	if err != nil {
		return ProductCheckoutIdentity{}, SanitizeProviderError(err)
	}
	if identity.Provider != runtime.Provider() || identity.MerchantID == "" || identity.Endpoint == "" || identity.APIVersion == "" || identity.RequestVersion == "" {
		return ProductCheckoutIdentity{}, newProviderFailure("payment_response_invalid", 0)
	}
	return identity, nil
}

func saveProductCheckoutRequestTx(ctx context.Context, tx pgx.Tx, identity ProductCheckoutIdentity, request CheckoutRequest) error {
	encodedIdentity, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	encodedRequest, err := json.Marshal(request)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_checkout_requests(payment_id,identity,request,dispatch_protocol) VALUES($1,$2,$3,'guarded_v1')`, request.PaymentID, encodedIdentity, encodedRequest)
	return err
}

func loadProductCheckoutRequestTx(ctx context.Context, tx pgx.Tx, checkout Checkout, identity ProductCheckoutIdentity) (CheckoutRequest, error) {
	var encodedIdentity, encodedRequest []byte
	var withinRetryWindow bool
	// Use the same database clock that timestamps the request, rather than a
	// possibly skewed API host clock. Future-dated evidence also fails closed.
	err := tx.QueryRow(ctx, `SELECT identity,request,
		created_at <= clock_timestamp() AND created_at > clock_timestamp()-interval '23 hours'
		FROM product_checkout_requests WHERE payment_id=$1`, checkout.PaymentID).Scan(&encodedIdentity, &encodedRequest, &withinRetryWindow)
	if errors.Is(err, pgx.ErrNoRows) {
		return CheckoutRequest{}, newProviderFailure("payment_reconciliation_required", 0)
	}
	if err != nil {
		return CheckoutRequest{}, err
	}
	var originalIdentity ProductCheckoutIdentity
	var request CheckoutRequest
	if json.Unmarshal(encodedIdentity, &originalIdentity) != nil || json.Unmarshal(encodedRequest, &request) != nil || originalIdentity != identity || identity.LiveMode != checkout.LiveMode || request.PaymentID != checkout.PaymentID || request.ResourceID != checkout.ResourceID || request.Purpose != "product" || request.OrderExternalID != checkout.OrderID.String() || request.AmountCents != checkout.AmountCents || request.Currency != checkout.Currency {
		return CheckoutRequest{}, newProviderFailure("payment_reconciliation_required", 0)
	}
	// Stripe may prune idempotency keys after 24 hours. An uncertain request is
	// never sent again outside a conservative window; recovery must first find
	// its original remote session/payment instead of creating another charge.
	if identity.Provider == "stripe" && !withinRetryWindow {
		return CheckoutRequest{}, newProviderFailure("payment_reconciliation_required", 0)
	}
	if buyer, err := uuid.Parse(request.BuyerIdentity); err != nil || buyer == uuid.Nil {
		return CheckoutRequest{}, newProviderFailure("payment_response_invalid", 0)
	}
	return request, nil
}

type productIdentityQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type productPaymentCustomer struct {
	BuyerIdentity string
	BuyerEmail    string
}

// verifyProductPaymentIdentity authenticates the runtime before reading or
// refunding an original payment. Checkout serialization and its retry deadline
// are deliberately not reused here: observing/refunding an old payment must not
// be mistaken for creating that checkout again.
func verifyProductPaymentIdentity(ctx context.Context, db productIdentityQuery, runtime ProviderRuntime, paymentID uuid.UUID) (ProductCheckoutIdentity, productPaymentCustomer, error) {
	original, customer, err := readOriginalProductPaymentIdentity(ctx, db, paymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return verifyRecoveredProductPaymentIdentity(ctx, db, runtime, paymentID)
	}
	if err != nil {
		return ProductCheckoutIdentity{}, productPaymentCustomer{}, err
	}
	current, err := checkoutIdentity(ctx, runtime)
	if err != nil {
		return ProductCheckoutIdentity{}, productPaymentCustomer{}, err
	}
	if current.Provider != original.Provider || current.MerchantID != original.MerchantID || current.StoreID != original.StoreID ||
		current.LiveMode != original.LiveMode || current.Endpoint != original.Endpoint {
		return ProductCheckoutIdentity{}, productPaymentCustomer{}, ErrCheckoutReconciliation
	}
	return current, customer, nil
}

// Local binding validation is also required when consuming an immutable,
// previously authenticated observation without making another provider call.
func readOriginalProductPaymentIdentity(ctx context.Context, db productIdentityQuery, paymentID uuid.UUID) (ProductCheckoutIdentity, productPaymentCustomer, error) {
	var encodedIdentity, encodedRequest []byte
	var provider, currency, buyer string
	var orderID, resourceID uuid.UUID
	var amount int
	var live bool
	err := db.QueryRow(ctx, `SELECT r.identity,r.request,p.provider,p.live_mode,p.payer_id::text,p.order_id,p.resource_id,p.amount_cents,p.currency
		FROM payment_intents p JOIN product_checkout_requests r ON r.payment_id=p.id
		WHERE p.id=$1 AND p.purpose='product'`, paymentID).Scan(&encodedIdentity, &encodedRequest, &provider, &live, &buyer, &orderID, &resourceID, &amount, &currency)
	if err != nil {
		return ProductCheckoutIdentity{}, productPaymentCustomer{}, err
	}
	var original ProductCheckoutIdentity
	var request CheckoutRequest
	if json.Unmarshal(encodedIdentity, &original) != nil || json.Unmarshal(encodedRequest, &request) != nil ||
		original.Provider != provider || original.LiveMode != live || original.MerchantID == "" || original.Endpoint == "" ||
		request.PaymentID != paymentID || request.Purpose != "product" || request.OrderExternalID != orderID.String() ||
		request.ResourceID != resourceID || request.BuyerIdentity != buyer || request.AmountCents != amount || request.Currency != currency {
		return ProductCheckoutIdentity{}, productPaymentCustomer{}, ErrCheckoutReconciliation
	}
	return original, productPaymentCustomer{BuyerIdentity: request.BuyerIdentity, BuyerEmail: request.BuyerEmail}, nil
}
