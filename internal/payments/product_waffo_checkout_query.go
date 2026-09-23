package payments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A connector query is not a signed webhook. Require its immutable lookup,
// the consuming check job, and the exact observation that produced the event.
func verifyWaffoProductCheckoutQueryTx(ctx context.Context, tx pgx.Tx, eventID uuid.UUID) error {
	var request CheckoutReadRequest
	var checkID uuid.UUID
	var remotePayment, digest string
	err := tx.QueryRow(ctx, `SELECT p.id,p.resource_id,p.provider_checkout_id,p.amount_cents,p.currency,p.live_mode,
 e.checkout_job_id,e.provider_payment_id,e.payload_sha256
 FROM payment_provider_events e JOIN payment_intents p ON p.id=e.payment_id
 JOIN orders o ON o.id=p.order_id AND o.buyer_id=p.payer_id AND o.product_id=p.resource_id
 JOIN jobs j ON j.id=e.checkout_job_id AND j.kind=$2 AND j.payload->>'paymentId'=p.id::text
 WHERE e.id=$1 AND e.provider='waffo_pancake' AND p.provider=e.provider AND p.purpose='product'
 AND e.evidence_source='provider_query' AND e.event_type='checkout.observed' AND e.api_version=$3
 AND e.object_type='checkout.order' AND e.object_id=p.provider_checkout_id
 AND e.purpose=p.purpose AND e.resource_id=p.resource_id AND e.amount_cents=p.amount_cents
 AND e.currency=p.currency AND e.live_mode=p.live_mode AND e.payment_status='paid'
 AND e.provider_payment_id IS NOT NULL AND e.provider_charge_id IS NULL AND e.refund_check_id IS NULL`,
		eventID, ProductCheckoutCheckJobKind, waffoCheckoutLookupContractVersion).Scan(&request.PaymentID, &request.ResourceID, &request.ProviderCheckoutID, &request.AmountCents, &request.Currency, &request.LiveMode, &checkID, &remotePayment, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCheckoutReconciliation
	}
	if err != nil {
		return err
	}
	paid, source, err := paidCheckoutEvidence(ctx, tx, request)
	if err != nil {
		return err
	}
	if source.LookupJobID == uuid.Nil || source.IdentityRecoveryJobID != uuid.Nil || paid.ProviderPaymentID != remotePayment {
		return ErrCheckoutReconciliation
	}
	// Use the job's original receipt: a later consistent lookup may have a
	// different expiry timestamp, but must not change the event's digest.
	var encoded []byte
	err = tx.QueryRow(ctx, `SELECT q.evidence->'observation' FROM payment_intent_events q
 JOIN product_checkout_lookups l ON l.job_id::text=q.evidence->>'lookupJobId' AND l.payment_id=q.payment_id
 WHERE q.payment_id=$1 AND q.event_type='checkout.queried' AND q.evidence->>'source'='provider_query'
 AND q.evidence->>'jobId'=$2 AND l.outcome='found' AND l.result->>'outcome'='found'
 AND q.evidence->'observation'=l.result->'observation'
 ORDER BY q.created_at,q.id LIMIT 1`, request.PaymentID, checkID.String()).Scan(&encoded)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCheckoutReconciliation
	}
	if err != nil {
		return err
	}
	var observed CheckoutObservation
	if json.Unmarshal(encoded, &observed) != nil || !validCheckoutObservationForProvider(request, observed, "waffo_pancake") || !sameCheckoutPaymentObservation(paid, observed) {
		return ErrCheckoutReconciliation
	}
	body, err := json.Marshal(observed)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != digest {
		return ErrCheckoutReconciliation
	}
	return nil
}
