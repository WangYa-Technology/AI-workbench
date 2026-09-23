package payments

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type waffoBillingWebhookBinding struct {
	PaymentID, BuyerID          uuid.UUID
	OriginalMerchantID, StoreID string
	RequestSHA256               string
	LiveMode                    bool
}

// Receipt of an existing obligation must not depend on current sales settings,
// an active buyer profile, or the outbound checkout retry window.
func bindWaffoBillingWebhookTx(ctx context.Context, tx pgx.Tx, event minimizedProviderEvent) (*waffoBillingWebhookBinding, error) {
	if event.PaymentID == nil {
		return nil, ErrInvalidEvent
	}
	var b waffoBillingWebhookBinding
	var purpose, provider, currency string
	var resource uuid.UUID
	var amount int64
	err := tx.QueryRow(ctx, `SELECT id,purpose,provider,payer_id,resource_id,amount_cents,currency,live_mode
 FROM payment_intents WHERE id=$1`, event.PaymentID).Scan(&b.PaymentID, &purpose, &provider, &b.BuyerID, &resource, &amount, &currency, &b.LiveMode)
	if err != nil {
		return nil, err
	}
	if provider != "waffo_pancake" || !oneOf(purpose, "wallet_topup", "subscription") {
		return nil, ErrInvalidEvent
	}
	var identityJSON, requestJSON []byte
	var dispatchMatches bool
	err = tx.QueryRow(ctx, `SELECT r.identity,r.request,d.request_sha256,
 d.request_sha256=encode(public.digest(r.request::text,'sha256'),'hex')
 FROM billing_checkout_requests r JOIN billing_checkout_dispatches d ON d.payment_id=r.payment_id
 WHERE r.payment_id=$1`, b.PaymentID).Scan(&identityJSON, &requestJSON, &b.RequestSHA256, &dispatchMatches)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCheckoutReconciliation
	}
	if err != nil {
		return nil, err
	}
	var original ProductCheckoutIdentity
	var request CheckoutRequest
	productType := "onetime"
	if purpose == "subscription" {
		productType = "subscription"
	}
	if !dispatchMatches || json.Unmarshal(identityJSON, &original) != nil || json.Unmarshal(requestJSON, &request) != nil ||
		original.Provider != provider || original.MerchantID == "" || original.StoreID == "" || original.Endpoint == "" || original.LiveMode != b.LiveMode ||
		original.APIVersion == "" || original.RequestVersion == "" ||
		request.PaymentID != b.PaymentID || request.Purpose != purpose || request.ResourceID != resource || request.OrderExternalID != b.PaymentID.String() ||
		request.BuyerIdentity != b.BuyerID.String() || request.AmountCents != int(amount) || request.Currency != currency || request.ProductID == "" || request.ProductType != productType {
		return nil, ErrCheckoutReconciliation
	}
	if event.WaffoVerificationVersion != waffoWebhookContractVersion || event.WaffoStoreID != original.StoreID || event.LiveMode != b.LiveMode ||
		event.WaffoOrderExternalID != request.OrderExternalID || event.WaffoBuyerIdentity != request.BuyerIdentity ||
		event.ResourceID == nil || *event.ResourceID != resource || event.Purpose == nil || *event.Purpose != purpose ||
		event.AmountCents == nil || *event.AmountCents != amount || event.Currency == nil || *event.Currency != currency ||
		event.PaymentStatus == nil || *event.PaymentStatus != "succeeded" || event.ProviderPaymentID == nil || *event.ProviderPaymentID == "" ||
		!(event.EventType == "order.completed" || (purpose == "subscription" && oneOf(event.EventType, "subscription.activated", "subscription.payment_succeeded"))) {
		return nil, ErrInvalidEvent
	}
	b.OriginalMerchantID, b.StoreID = original.MerchantID, original.StoreID
	return &b, nil
}

func saveWaffoBillingWebhookBindingTx(ctx context.Context, tx pgx.Tx, eventID uuid.UUID, b waffoBillingWebhookBinding, event minimizedProviderEvent) error {
	if err := verifyStoredWaffoWebhookEventTx(ctx, tx, eventID, event); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO billing_waffo_webhook_bindings(event_id,payment_id,contract_version,original_merchant_id,store_id,live_mode,order_external_id,buyer_id,request_sha256)
 VALUES($1,$2,$3,$4,$5,$6,$2,$7,$8) ON CONFLICT(event_id) DO NOTHING`, eventID, b.PaymentID, waffoWebhookContractVersion, b.OriginalMerchantID, b.StoreID, b.LiveMode, b.BuyerID, b.RequestSHA256)
	if err != nil {
		return err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM billing_waffo_webhook_bindings WHERE event_id=$1 AND payment_id=$2
 AND contract_version=$3 AND original_merchant_id=$4 AND store_id=$5 AND live_mode=$6 AND order_external_id=$2 AND buyer_id=$7 AND request_sha256=$8)`,
		eventID, b.PaymentID, waffoWebhookContractVersion, b.OriginalMerchantID, b.StoreID, b.LiveMode, b.BuyerID, b.RequestSHA256).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrEventConflict
	}
	return nil
}

func verifyWaffoBillingWebhookBindingTx(ctx context.Context, tx pgx.Tx, eventID uuid.UUID) error {
	var event minimizedProviderEvent
	var merchant, requestSHA256 string
	err := tx.QueryRow(ctx, `SELECT e.payment_id,e.resource_id,e.purpose,e.amount_cents,e.currency,e.live_mode,e.event_type,e.payment_status,e.provider_payment_id,
 b.contract_version,b.original_merchant_id,b.store_id,b.order_external_id::text,b.buyer_id::text,b.request_sha256
 FROM payment_provider_events e JOIN billing_waffo_webhook_bindings b ON b.event_id=e.id AND b.payment_id=e.payment_id AND b.live_mode=e.live_mode
 WHERE e.id=$1 AND e.provider='waffo_pancake' AND e.evidence_source='webhook'`, eventID).Scan(
		&event.PaymentID, &event.ResourceID, &event.Purpose, &event.AmountCents, &event.Currency, &event.LiveMode, &event.EventType, &event.PaymentStatus, &event.ProviderPaymentID,
		&event.WaffoVerificationVersion, &merchant, &event.WaffoStoreID, &event.WaffoOrderExternalID, &event.WaffoBuyerIdentity, &requestSHA256)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCheckoutReconciliation
	}
	if err != nil {
		return err
	}
	b, err := bindWaffoBillingWebhookTx(ctx, tx, event)
	if err != nil {
		return err
	}
	if b.OriginalMerchantID != merchant || b.RequestSHA256 != requestSHA256 {
		return ErrCheckoutReconciliation
	}
	return nil
}
