package payments

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const stripeBillingWebhookVersion = "stripe-billing-webhook-v1"

type stripeBillingWebhookBinding struct {
	PaymentID                                        uuid.UUID
	MerchantID, CheckoutID, CaptureID, RequestSHA256 string
	LiveMode                                         bool
}

// The retained endpoint verifier authenticates delivery. The stored Checkout
// identifier then ties the delivery to the original merchant's request. Do not
// infer this relation from metadata alone, including after a lost response.
func bindStripeBillingWebhookTx(ctx context.Context, tx pgx.Tx, event minimizedProviderEvent) (*stripeBillingWebhookBinding, error) {
	if event.PaymentID == nil {
		return nil, nil
	}
	var b stripeBillingWebhookBinding
	var provider, purpose, currency, capture, charge string
	var buyer, resource uuid.UUID
	var amount int64
	err := tx.QueryRow(ctx, `SELECT id,provider,purpose,payer_id,resource_id,amount_cents,currency,live_mode,
 COALESCE(provider_checkout_id,''),COALESCE(provider_payment_id,''),COALESCE(provider_charge_id,'')
 FROM payment_intents WHERE id=$1 AND purpose IN ('wallet_topup','subscription')`, event.PaymentID).Scan(
		&b.PaymentID, &provider, &purpose, &buyer, &resource, &amount, &currency, &b.LiveMode, &b.CheckoutID, &capture, &charge)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if provider != "stripe" || event.StripeVerificationVersion != stripeBillingWebhookVersion || event.LiveMode != b.LiveMode ||
		event.Purpose == nil || *event.Purpose != purpose || event.ResourceID == nil || *event.ResourceID != resource ||
		event.AmountCents == nil || *event.AmountCents != amount || event.Currency == nil || *event.Currency != currency ||
		event.ProviderPaymentID == nil || !validStripeID(*event.ProviderPaymentID, "pi_") || event.PaymentStatus == nil {
		return nil, ErrInvalidEvent
	}
	b.CaptureID = *event.ProviderPaymentID
	if capture != "" && capture != b.CaptureID {
		return nil, ErrInvalidEvent
	}
	failed := oneOf(event.EventType, "checkout.session.async_payment_failed", "payment_intent.payment_failed")
	if !failed && charge != "" && event.ProviderChargeID != nil && charge != *event.ProviderChargeID {
		return nil, ErrInvalidEvent
	}
	var identityJSON, requestJSON []byte
	var matches bool
	err = tx.QueryRow(ctx, `SELECT r.identity,r.request,d.request_sha256,
 d.request_sha256=encode(public.digest(r.request::text,'sha256'),'hex') FROM billing_checkout_requests r
 JOIN billing_checkout_dispatches d ON d.payment_id=r.payment_id WHERE r.payment_id=$1`, b.PaymentID).Scan(&identityJSON, &requestJSON, &b.RequestSHA256, &matches)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCheckoutReconciliation
	}
	if err != nil {
		return nil, err
	}
	var identity ProductCheckoutIdentity
	var request CheckoutRequest
	if !matches || json.Unmarshal(identityJSON, &identity) != nil || json.Unmarshal(requestJSON, &request) != nil ||
		identity.Provider != provider || identity.LiveMode != b.LiveMode || identity.MerchantID == "" || identity.Endpoint == "" || identity.APIVersion == "" || identity.RequestVersion == "" ||
		request.PaymentID != b.PaymentID || request.ResourceID != resource || request.Purpose != purpose || request.AmountCents != int(amount) || request.Currency != currency ||
		request.BuyerIdentity != buyer.String() || request.OrderExternalID != b.PaymentID.String() || !validStripeID(b.CheckoutID, "cs_") {
		return nil, ErrCheckoutReconciliation
	}
	b.MerchantID = identity.MerchantID
	switch event.EventType {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded", "checkout.session.async_payment_failed":
		if event.ObjectType != "checkout.session" || event.ObjectID != b.CheckoutID {
			return nil, ErrInvalidEvent
		}
		if event.EventType == "checkout.session.completed" && !oneOf(*event.PaymentStatus, "paid", "unpaid") ||
			event.EventType == "checkout.session.async_payment_succeeded" && *event.PaymentStatus != "paid" ||
			event.EventType == "checkout.session.async_payment_failed" && *event.PaymentStatus != "unpaid" {
			return nil, ErrInvalidEvent
		}
	case "payment_intent.succeeded", "payment_intent.payment_failed":
		if event.ObjectType != "payment_intent" || event.ObjectID != b.CaptureID ||
			event.EventType == "payment_intent.succeeded" && (*event.PaymentStatus != "succeeded" || event.ProviderChargeID == nil || !validStripeID(*event.ProviderChargeID, "ch_")) ||
			event.EventType == "payment_intent.payment_failed" && !oneOf(*event.PaymentStatus, "requires_payment_method", "canceled") {
			return nil, ErrInvalidEvent
		}
		if err := verifyStripeBillingCheckoutBindingTx(ctx, tx, b); err != nil {
			return nil, err
		}
	default:
		return nil, ErrInvalidEvent
	}
	return &b, nil
}

func verifyStripeBillingCheckoutBindingTx(ctx context.Context, tx pgx.Tx, b stripeBillingWebhookBinding) error {
	// The immutable anchor is a verified original-session event, not a metadata
	// claim from a standalone PaymentIntent or an old unverified queue row.
	var valid bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM billing_stripe_checkout_bindings b
 JOIN billing_stripe_webhook_bindings v ON v.event_id=b.anchor_event_id AND v.payment_id=b.payment_id
 JOIN payment_provider_events e ON e.id=v.event_id AND e.payment_id=b.payment_id
 JOIN payment_intents p ON p.id=b.payment_id
 WHERE b.payment_id=$1 AND b.original_merchant_id=$2 AND b.live_mode=$3 AND b.checkout_id=$4 AND b.provider_payment_id=$5 AND b.request_sha256=$6
 AND v.contract_version=$7 AND e.provider='stripe' AND e.evidence_source='webhook' AND e.live_mode=b.live_mode
 AND e.object_type='checkout.session' AND e.object_id=b.checkout_id AND e.provider_payment_id=b.provider_payment_id
 AND e.resource_id=p.resource_id AND e.purpose=p.purpose AND e.amount_cents=p.amount_cents AND e.currency=p.currency
 AND ((e.event_type='checkout.session.completed' AND e.payment_status IN ('paid','unpaid'))
 OR (e.event_type='checkout.session.async_payment_succeeded' AND e.payment_status='paid')
 OR (e.event_type='checkout.session.async_payment_failed' AND e.payment_status='unpaid')))`,
		b.PaymentID, b.MerchantID, b.LiveMode, b.CheckoutID, b.CaptureID, b.RequestSHA256, stripeBillingWebhookVersion).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrCheckoutReconciliation
	}
	return nil
}

func saveStripeBillingWebhookBindingTx(ctx context.Context, tx pgx.Tx, eventID uuid.UUID, b stripeBillingWebhookBinding, event minimizedProviderEvent) error {
	if err := verifyStoredStripeWebhookEventTx(ctx, tx, eventID, event); err != nil {
		return err
	}
	if event.ObjectType == "checkout.session" {
		_, err := tx.Exec(ctx, `INSERT INTO billing_stripe_checkout_bindings(payment_id,original_merchant_id,live_mode,checkout_id,provider_payment_id,request_sha256,anchor_event_id)
 VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, b.PaymentID, b.MerchantID, b.LiveMode, b.CheckoutID, b.CaptureID, b.RequestSHA256, eventID)
		if err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `INSERT INTO billing_stripe_webhook_bindings(event_id,payment_id,contract_version)
 VALUES($1,$2,$3) ON CONFLICT(event_id) DO NOTHING`, eventID, b.PaymentID, stripeBillingWebhookVersion)
	if err != nil {
		return err
	}
	if err := verifyStripeBillingCheckoutBindingTx(ctx, tx, b); err != nil {
		return err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM billing_stripe_webhook_bindings WHERE event_id=$1 AND payment_id=$2 AND contract_version=$3)`, eventID, b.PaymentID, stripeBillingWebhookVersion).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrEventConflict
	}
	return nil
}

func verifyStripeBillingWebhookBindingTx(ctx context.Context, tx pgx.Tx, eventID uuid.UUID) error {
	var event minimizedProviderEvent
	err := tx.QueryRow(ctx, `SELECT e.payment_id,e.resource_id,e.purpose,e.amount_cents,e.currency,e.live_mode,e.event_type,e.payment_status,e.provider_payment_id,e.provider_charge_id,e.object_id,e.object_type,v.contract_version
 FROM payment_provider_events e JOIN billing_stripe_webhook_bindings v ON v.event_id=e.id AND v.payment_id=e.payment_id
 WHERE e.id=$1 AND e.provider='stripe' AND e.evidence_source='webhook'`, eventID).Scan(
		&event.PaymentID, &event.ResourceID, &event.Purpose, &event.AmountCents, &event.Currency, &event.LiveMode, &event.EventType, &event.PaymentStatus, &event.ProviderPaymentID, &event.ProviderChargeID, &event.ObjectID, &event.ObjectType, &event.StripeVerificationVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCheckoutReconciliation
	}
	if err != nil {
		return err
	}
	b, err := bindStripeBillingWebhookTx(ctx, tx, event)
	if err != nil {
		return err
	}
	if b == nil {
		return ErrCheckoutReconciliation
	}
	return verifyStripeBillingCheckoutBindingTx(ctx, tx, *b)
}
