package payments

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type checkoutEvidenceSource struct {
	LookupJobID           uuid.UUID
	IdentityRecoveryJobID uuid.UUID
}

func (s checkoutEvidenceSource) present() bool {
	return s.LookupJobID != uuid.Nil || s.IdentityRecoveryJobID != uuid.Nil
}

// Both recovery workflows authenticate their observations before saving them.
// Combining their receipts must never choose arbitrarily between contradictory
// paid evidence. Only the non-financial expiry timestamp may differ.
func paidCheckoutEvidence(ctx context.Context, db productIdentityQuery, request CheckoutReadRequest) (CheckoutObservation, checkoutEvidenceSource, error) {
	var source checkoutEvidenceSource
	if err := validatePaidCheckoutEvidenceAgreement(ctx, db, request.PaymentID); err != nil {
		return CheckoutObservation{}, source, err
	}
	lookup, lookupID, err := paidCheckoutLookupEvidence(ctx, db, request)
	if err != nil {
		return CheckoutObservation{}, source, err
	}
	recovery, recoveryID, err := paidRecoveredCheckoutEvidence(ctx, db, request)
	if err != nil {
		return CheckoutObservation{}, source, err
	}
	if lookupID != nil {
		source.LookupJobID = *lookupID
	}
	if recoveryID != nil {
		source.IdentityRecoveryJobID = *recoveryID
	}
	if lookupID != nil && recoveryID != nil {
		if !sameCheckoutPaymentObservation(lookup, recovery) {
			return CheckoutObservation{}, checkoutEvidenceSource{}, ErrCheckoutReconciliation
		}
	}
	if lookupID != nil {
		return lookup, source, nil
	}
	return recovery, source, nil
}

func sameCheckoutPaymentObservation(left, right CheckoutObservation) bool {
	left.ExpiresAt, right.ExpiresAt = time.Time{}, time.Time{}
	left.CheckoutURL, right.CheckoutURL = "", ""
	return left == right
}

func validatePaidCheckoutEvidenceAgreement(ctx context.Context, db productIdentityQuery, paymentID uuid.UUID) error {
	var conflict bool
	if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_checkout_evidence_conflicts WHERE payment_id=$1)`, paymentID).Scan(&conflict); err != nil {
		return err
	}
	if conflict {
		return ErrCheckoutReconciliation
	}
	return nil
}

func paidRecoveredCheckoutEvidence(ctx context.Context, db productIdentityQuery, request CheckoutReadRequest) (CheckoutObservation, *uuid.UUID, error) {
	var jobID uuid.UUID
	var encoded []byte
	err := db.QueryRow(ctx, `SELECT job_id,observation FROM product_payment_identity_recoveries
 WHERE payment_id=$1 AND observation->'checkout'->>'paymentStatus'='paid'`, request.PaymentID).Scan(&jobID, &encoded)
	if errors.Is(err, pgx.ErrNoRows) {
		return CheckoutObservation{}, nil, nil
	}
	if err != nil {
		return CheckoutObservation{}, nil, err
	}
	var observation ProductPaymentIdentityObservation
	if json.Unmarshal(encoded, &observation) != nil || observation.Checkout == nil || !validCheckoutObservation(request, *observation.Checkout) {
		return CheckoutObservation{}, nil, ErrCheckoutReconciliation
	}
	identity, binding, err := readRecoveredProductPaymentIdentity(ctx, db, request.PaymentID)
	if err != nil {
		return CheckoutObservation{}, nil, err
	}
	if identity.LiveMode != request.LiveMode || !validIdentityObservation(binding, observation) {
		return CheckoutObservation{}, nil, ErrCheckoutReconciliation
	}
	return *observation.Checkout, &jobID, nil
}
