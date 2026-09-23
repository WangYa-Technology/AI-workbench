package payments

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A historical digest alone cannot bless an inconsistent minimized record.
// Rechecking must admit the same financial fields, not just the same event ID.
func verifyStoredStripeWebhookEventTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, e minimizedProviderEvent) error {
	var matches bool
	err := tx.QueryRow(ctx, `SELECT evidence_source='webhook' AND
 ROW(event_type,api_version,live_mode,occurred_at,object_id,object_type,payment_id,resource_id,purpose,
 amount_cents,currency,payment_status,provider_payment_id,provider_charge_id,refund_operation_id,dispute_status,dispute_reason,dispute_network_reason_code,dispute_due_by)
 IS NOT DISTINCT FROM ROW($2::text,$3::text,$4::boolean,$5::timestamptz,$6::text,$7::text,$8::uuid,$9::uuid,$10::text,
 $11::bigint,$12::text,$13::text,$14::text,$15::text,$16::uuid,$17::text,$18::text,$19::text,$20::timestamptz)
 FROM payment_provider_events WHERE id=$1`, id, e.EventType, e.APIVersion, e.LiveMode, e.OccurredAt, e.ObjectID, e.ObjectType, e.PaymentID, e.ResourceID, e.Purpose,
		e.AmountCents, e.Currency, e.PaymentStatus, e.ProviderPaymentID, e.ProviderChargeID, e.RefundOperationID, e.DisputeStatus, e.DisputeReason, e.DisputeNetworkReasonCode, e.DisputeDueBy).Scan(&matches)
	if err != nil {
		return err
	}
	if !matches {
		return ErrEventConflict
	}
	return nil
}

// validateStripeProductEvent separates existing product obligations from new
// sales selection. Call only after signature verification (ingress) or while
// reading immutable provider evidence (worker). It does not authenticate a new
// merchant or permit replacing the original webhook secret/environment.
func validateStripeProductEvent(ctx context.Context, db productIdentityQuery, event minimizedProviderEvent) (bool, error) {
	if event.PaymentID == nil {
		return false, nil
	}
	var b ProductPaymentBinding
	var provider string
	err := db.QueryRow(ctx, `SELECT provider,id,resource_id,order_id,payer_id,amount_cents,currency,live_mode,
 COALESCE(provider_checkout_id,''),COALESCE(provider_payment_id,''),COALESCE(provider_charge_id,'')
 FROM payment_intents WHERE id=$1 AND purpose='product'`, *event.PaymentID).Scan(
		&provider, &b.PaymentID, &b.ResourceID, &b.OrderID, &b.BuyerID, &b.AmountCents, &b.Currency, &b.LiveMode,
		&b.ProviderCheckoutID, &b.ProviderPaymentID, &b.ProviderChargeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if provider != "stripe" || event.LiveMode != b.LiveMode ||
		(event.ResourceID != nil && *event.ResourceID != b.ResourceID) ||
		(event.Purpose != nil && *event.Purpose != "product") {
		return true, ErrInvalidEvent
	}
	// Unhandled event types remain subject to the normal routing/ignore policy;
	// metadata alone must not expand the disabled provider's reception boundary.
	if !oneOf(event.EventType, "checkout.session.completed", "checkout.session.async_payment_succeeded", "checkout.session.async_payment_failed",
		"payment_intent.succeeded", "payment_intent.payment_failed", "refund.updated", "checkout.observed", "refund.observed") {
		return false, nil
	}
	failedAttempt := oneOf(event.EventType, "payment_intent.payment_failed", "checkout.session.async_payment_failed")
	if event.AmountCents == nil || *event.AmountCents != int64(b.AmountCents) || event.Currency == nil || *event.Currency != b.Currency ||
		event.ProviderPaymentID == nil || (b.ProviderPaymentID != "" && *event.ProviderPaymentID != b.ProviderPaymentID) ||
		(!failedAttempt && event.ProviderChargeID != nil && b.ProviderChargeID != "" && *event.ProviderChargeID != b.ProviderChargeID) {
		return true, ErrInvalidEvent
	}
	// One PaymentIntent can contain a failed attempt before its successful
	// Charge. Late failure evidence must not be confused with replacing the
	// confirmed Charge; failProductPaymentTx records it without changing funds.
	switch event.EventType {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded", "checkout.session.async_payment_failed", "checkout.observed":
		if event.ObjectType != "checkout.session" || event.ResourceID == nil || event.Purpose == nil ||
			(b.ProviderCheckoutID != "" && event.ObjectID != b.ProviderCheckoutID) {
			return true, ErrInvalidEvent
		}
	case "payment_intent.succeeded", "payment_intent.payment_failed":
		if event.ObjectType != "payment_intent" || event.ObjectID != *event.ProviderPaymentID || event.ResourceID == nil || event.Purpose == nil {
			return true, ErrInvalidEvent
		}
	case "refund.updated", "refund.observed":
		// Stripe refund metadata contains payment/operation IDs, not necessarily
		// product/purpose. The original remote PaymentIntent is mandatory here.
		if event.ObjectType != "refund" || b.ProviderPaymentID == "" {
			return true, ErrInvalidEvent
		}
	}
	return true, nil
}
