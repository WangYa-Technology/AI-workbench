package payments

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A found paid lookup was authenticated against the original merchant before
// its immutable result and session binding committed together. Consume that
// receipt, including from a replacement check job, rather than asking a later
// remote response to re-establish money already observed. Never fall back to
// an unpaid query when a stored positive receipt fails local validation.
func paidCheckoutLookupEvidence(ctx context.Context, db productIdentityQuery, request CheckoutReadRequest) (CheckoutObservation, *uuid.UUID, error) {
	var lookupID uuid.UUID
	var encoded []byte
	var conflicting bool
	err := db.QueryRow(ctx, `SELECT l.job_id,l.result,EXISTS(
 SELECT 1 FROM product_checkout_lookups other WHERE other.payment_id=l.payment_id
 AND other.outcome='found' AND other.result->'observation'->>'paymentStatus'='paid'
 AND (other.result->>'outcome' IS DISTINCT FROM 'found'
 OR ((other.result->'observation')-'expiresAt') IS DISTINCT FROM ((l.result->'observation')-'expiresAt')))
 FROM product_checkout_lookups l
 WHERE l.payment_id=$1 AND l.outcome='found' AND l.result->'observation'->>'paymentStatus'='paid'
 ORDER BY l.created_at,l.job_id LIMIT 1`, request.PaymentID).Scan(&lookupID, &encoded, &conflicting)
	if errors.Is(err, pgx.ErrNoRows) {
		return CheckoutObservation{}, nil, nil
	}
	if err != nil {
		return CheckoutObservation{}, nil, err
	}
	var result CheckoutLookupResult
	if conflicting || json.Unmarshal(encoded, &result) != nil || result.Outcome != "found" || result.Observation == nil ||
		!validCheckoutLookupResult(CheckoutLookupRequest{CheckoutReadRequest: request}, result) {
		return CheckoutObservation{}, nil, ErrCheckoutReconciliation
	}
	// The original request also binds the buyer, order and resource, fields that
	// the minimized observation does not itself carry.
	identity, _, err := readOriginalProductPaymentIdentity(ctx, db, request.PaymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return CheckoutObservation{}, nil, ErrCheckoutReconciliation
	}
	if err != nil {
		return CheckoutObservation{}, nil, err
	}
	if identity.LiveMode != request.LiveMode || !validCheckoutObservationForProvider(request, *result.Observation, identity.Provider) {
		return CheckoutObservation{}, nil, ErrCheckoutReconciliation
	}
	return *result.Observation, &lookupID, nil
}
