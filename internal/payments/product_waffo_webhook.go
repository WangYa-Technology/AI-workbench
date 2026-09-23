package payments

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const waffoWebhookContractVersion = "waffo-webhook-v1"

type waffoProductWebhookBinding struct {
	PaymentID, OrderID, BuyerID uuid.UUID
	OriginalMerchantID, StoreID string
	LiveMode                    bool
}

// Match signed delivery fields to the immutable outbound request, never current
// product/provider/user settings. A nil result belongs to the billing boundary.
func bindWaffoProductWebhookTx(ctx context.Context, tx pgx.Tx, event minimizedProviderEvent) (*waffoProductWebhookBinding, error) {
	if event.PaymentID == nil {
		return nil, ErrInvalidEvent
	}
	var purpose, provider, currency string
	var b waffoProductWebhookBinding
	var resource uuid.UUID
	var order *uuid.UUID
	var amount int64
	err := tx.QueryRow(ctx, `SELECT id,purpose,provider,order_id,payer_id,resource_id,amount_cents,currency,live_mode
 FROM payment_intents WHERE id=$1`, event.PaymentID).Scan(&b.PaymentID, &purpose, &provider, &order, &b.BuyerID, &resource, &amount, &currency, &b.LiveMode)
	if err != nil {
		return nil, err
	}
	if purpose != "product" {
		return nil, nil
	}
	if order == nil {
		return nil, ErrCheckoutReconciliation
	}
	b.OrderID = *order
	if provider != "waffo_pancake" {
		return nil, ErrInvalidEvent
	}
	var identityJSON, requestJSON []byte
	err = tx.QueryRow(ctx, `SELECT identity,request FROM product_checkout_requests WHERE payment_id=$1`, b.PaymentID).Scan(&identityJSON, &requestJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCheckoutReconciliation
	}
	if err != nil {
		return nil, err
	}
	var original ProductCheckoutIdentity
	var request CheckoutRequest
	if json.Unmarshal(identityJSON, &original) != nil || json.Unmarshal(requestJSON, &request) != nil ||
		original.Provider != provider || original.MerchantID == "" || original.StoreID == "" || original.Endpoint == "" || original.LiveMode != b.LiveMode ||
		original.APIVersion == "" || original.RequestVersion == "" ||
		request.PaymentID != b.PaymentID || request.Purpose != "product" || request.ResourceID != resource || request.OrderExternalID != b.OrderID.String() ||
		request.BuyerIdentity != b.BuyerID.String() || request.AmountCents != int(amount) || request.Currency != currency {
		return nil, ErrCheckoutReconciliation
	}
	if event.WaffoVerificationVersion != waffoWebhookContractVersion || event.WaffoStoreID != original.StoreID || event.LiveMode != b.LiveMode ||
		event.WaffoOrderExternalID != request.OrderExternalID || event.WaffoBuyerIdentity != request.BuyerIdentity ||
		event.ResourceID == nil || *event.ResourceID != resource || event.Purpose == nil || *event.Purpose != "product" ||
		event.AmountCents == nil || *event.AmountCents != amount || event.Currency == nil || *event.Currency != currency ||
		!oneOf(event.EventType, "order.completed", "refund.succeeded", "refund.failed") {
		return nil, ErrInvalidEvent
	}
	b.OriginalMerchantID, b.StoreID = original.MerchantID, original.StoreID
	return &b, nil
}

func verifyStoredWaffoWebhookEventTx(ctx context.Context, tx pgx.Tx, eventID uuid.UUID, event minimizedProviderEvent) error {
	// Reverification of an old identical raw delivery must not bless a different
	// old minimized financial record, even if its stored payload hash matches.
	var matches bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_provider_events WHERE id=$1
 AND provider='waffo_pancake' AND evidence_source='webhook'
 AND ROW(provider_event_id,event_type,live_mode,object_id,object_type,payment_id,resource_id,purpose,amount_cents,currency,payment_status,provider_payment_id,payload_sha256,api_version,occurred_at,refund_operation_id)
 IS NOT DISTINCT FROM ROW($2::text,$3::text,$4::boolean,$5::text,$6::text,$7::uuid,$8::uuid,$9::text,$10::bigint,$11::text,$12::text,$13::text,$14::text,$15::text,$16::timestamptz,$17::uuid))`,
		eventID, event.ProviderEventID, event.EventType, event.LiveMode, event.ObjectID, event.ObjectType, event.PaymentID, event.ResourceID, event.Purpose, event.AmountCents, event.Currency, event.PaymentStatus, event.ProviderPaymentID, event.PayloadSHA256, event.APIVersion, event.OccurredAt, event.RefundOperationID).Scan(&matches)
	if err != nil {
		return err
	}
	if !matches {
		return ErrEventConflict
	}
	return nil
}

func saveWaffoProductWebhookBindingTx(ctx context.Context, tx pgx.Tx, eventID uuid.UUID, b waffoProductWebhookBinding, event minimizedProviderEvent) error {
	if err := verifyStoredWaffoWebhookEventTx(ctx, tx, eventID, event); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO product_waffo_webhook_bindings(event_id,payment_id,contract_version,original_merchant_id,store_id,live_mode,order_id,buyer_id)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(event_id) DO NOTHING`, eventID, b.PaymentID, waffoWebhookContractVersion, b.OriginalMerchantID, b.StoreID, b.LiveMode, b.OrderID, b.BuyerID)
	if err != nil {
		return err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_waffo_webhook_bindings WHERE event_id=$1 AND payment_id=$2
 AND contract_version=$3 AND original_merchant_id=$4 AND store_id=$5 AND live_mode=$6 AND order_id=$7 AND buyer_id=$8)`, eventID, b.PaymentID, waffoWebhookContractVersion, b.OriginalMerchantID, b.StoreID, b.LiveMode, b.OrderID, b.BuyerID).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrInvalidEvent
	}
	return nil
}

func verifyWaffoProductWebhookBindingTx(ctx context.Context, tx pgx.Tx, eventID uuid.UUID) error {
	var event minimizedProviderEvent
	var merchant string
	err := tx.QueryRow(ctx, `SELECT e.payment_id,e.resource_id,e.purpose,e.amount_cents,e.currency,e.live_mode,e.event_type,
 b.contract_version,b.original_merchant_id,b.store_id,b.order_id::text,b.buyer_id::text
 FROM payment_provider_events e JOIN product_waffo_webhook_bindings b ON b.event_id=e.id AND b.payment_id=e.payment_id AND b.live_mode=e.live_mode
 WHERE e.id=$1 AND e.provider='waffo_pancake' AND e.evidence_source='webhook'`, eventID).Scan(
		&event.PaymentID, &event.ResourceID, &event.Purpose, &event.AmountCents, &event.Currency, &event.LiveMode, &event.EventType,
		&event.WaffoVerificationVersion, &merchant, &event.WaffoStoreID, &event.WaffoOrderExternalID, &event.WaffoBuyerIdentity)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCheckoutReconciliation
	}
	if err != nil {
		return err
	}
	b, err := bindWaffoProductWebhookTx(ctx, tx, event)
	if err != nil {
		return err
	}
	if b == nil || b.OriginalMerchantID != merchant {
		return ErrCheckoutReconciliation
	}
	return nil
}
