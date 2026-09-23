package payments

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RecordProductRefundAttemptTx snapshots the current operation while its payment
// and order are locked. Use RecordNewProductRefundAttemptTx when creating a
// new operation. Replays never rewrite the original request identity.
func RecordProductRefundAttemptTx(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID) error {
	return recordProductRefundAttemptTx(ctx, tx, paymentID, uuid.Nil)
}

// RecordNewProductRefundAttemptTx is only called in the transaction creating a
// freshly generated operation. It must never upgrade an existing operation:
// absence of an old dispatch record cannot establish that no request was sent.
func RecordNewProductRefundAttemptTx(ctx context.Context, tx pgx.Tx, paymentID, operationID uuid.UUID) error {
	if operationID == uuid.Nil {
		return ErrRefundConflict
	}
	return recordProductRefundAttemptTx(ctx, tx, paymentID, operationID)
}

func recordProductRefundAttemptTx(ctx context.Context, tx pgx.Tx, paymentID, newOperation uuid.UUID) error {
	var recorded uuid.UUID
	var provider, status string
	err := tx.QueryRow(ctx, `INSERT INTO product_refund_attempts(operation_id,payment_id,provider,provider_payment_id,
   amount_cents,currency,correlation_enabled,idempotency_key,provider_refund_id,status,requested_at)
 SELECT o.refund_operation_id,pi.id,pi.provider,pi.provider_payment_id,pi.amount_cents,pi.currency,
   o.refund_correlation_enabled,o.refund_idempotency_key,pi.provider_refund_id,
   CASE WHEN pi.status='refunded' THEN 'succeeded' WHEN pi.status IN ('paid','refund_failed') THEN 'failed'
     WHEN pi.provider_refund_id IS NOT NULL THEN 'pending' ELSE 'requested' END,
   COALESCE(o.refund_requested_at,o.updated_at)
 FROM payment_intents pi JOIN orders o ON o.id=pi.order_id
 WHERE pi.id=$1 AND pi.purpose='product' AND o.refund_operation_id IS NOT NULL AND pi.provider_payment_id IS NOT NULL
 ON CONFLICT(operation_id) DO NOTHING RETURNING operation_id,provider,status`, paymentID).Scan(&recorded, &provider, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		if newOperation != uuid.Nil {
			return ErrRefundConflict
		}
		return nil
	}
	if err != nil {
		return err
	}
	if newOperation == uuid.Nil {
		return nil
	}
	if recorded != newOperation || status != "requested" {
		return ErrRefundConflict
	}
	if provider != "waffo_pancake" {
		return nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_refund_dispatches(operation_id,contract_version) VALUES($1,$2)`, recorded, waffoRefundContractVersion)
	return err
}

// recordProductRefundEventTx validates verified provider evidence against its own operation,
// not whichever operation the order currently points to. The bool says whether
// the event should also advance the order projection. All changes commit with
// event processing and entitlement changes in the caller's transaction.
func recordProductRefundEventTx(ctx context.Context, tx pgx.Tx, eventID, paymentID uuid.UUID, provider, refundID, providerPaymentID string, amount int, currency, result string) (bool, error) {
	if !oneOf(result, "succeeded", "failed", "canceled", "pending", "requires_action") {
		return false, newProviderFailure("payment_response_invalid", 0)
	}
	var current *uuid.UUID
	var intentStatus string
	if err := tx.QueryRow(ctx, `SELECT o.refund_operation_id,pi.status FROM payment_intents pi
   JOIN orders o ON o.id=pi.order_id WHERE pi.id=$1 AND pi.purpose='product' AND pi.provider=$2
   FOR UPDATE OF pi,o`, paymentID, provider).Scan(&current, &intentStatus); err != nil {
		return false, err
	}
	if err := RecordProductRefundAttemptTx(ctx, tx, paymentID); err != nil {
		return false, err
	}
	var operation *uuid.UUID
	if provider == "stripe" {
		if err := tx.QueryRow(ctx, `SELECT refund_operation_id FROM payment_provider_events
    WHERE id=$1 AND payment_id=$2 AND provider='stripe' AND (event_type='refund.updated' OR (event_type='refund.observed' AND evidence_source='provider_query'))`, eventID, paymentID).Scan(&operation); err != nil {
			return false, err
		}
		// Legacy callbacks carry no operation metadata. Only a previously bound
		// provider refund ID may identify them, including after a later retry.
		if operation == nil {
			var id uuid.UUID
			err := tx.QueryRow(ctx, `SELECT operation_id FROM product_refund_attempts WHERE payment_id=$1 AND provider_refund_id=$2`, paymentID, refundID).Scan(&id)
			if errors.Is(err, pgx.ErrNoRows) {
				return false, newProviderFailure("payment_response_invalid", 0)
			}
			if err != nil {
				return false, err
			}
			operation = &id
		}
	} else if provider == "waffo_pancake" {
		id, err := uuid.Parse(refundID)
		if err != nil || id == uuid.Nil {
			return false, newProviderFailure("payment_response_invalid", 0)
		}
		operation = &id
	} else {
		return false, newProviderFailure("payment_response_invalid", 0)
	}
	var bound *string
	var expectedProvider, expectedPayment, expectedCurrency, prior string
	var expectedAmount int
	var correlation bool
	err := tx.QueryRow(ctx, `SELECT provider,provider_payment_id,amount_cents,currency,correlation_enabled,provider_refund_id,status
   FROM product_refund_attempts WHERE operation_id=$1 AND payment_id=$2 FOR UPDATE`, operation, paymentID).Scan(
		&expectedProvider, &expectedPayment, &expectedAmount, &expectedCurrency, &correlation, &bound, &prior)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, newProviderFailure("payment_response_invalid", 0)
	}
	if err != nil {
		return false, err
	}
	if expectedProvider != provider || expectedPayment != providerPaymentID || expectedAmount != amount || expectedCurrency != currency {
		return false, newProviderFailure("payment_response_invalid", 0)
	}
	if provider == "stripe" {
		if bound != nil && *bound != refundID || bound == nil && !correlation {
			return false, newProviderFailure("payment_response_invalid", 0)
		}
		if bound == nil {
			if _, err := tx.Exec(ctx, `UPDATE product_refund_attempts SET provider_refund_id=$2,updated_at=now() WHERE operation_id=$1`, operation, refundID); err != nil {
				return false, err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status,evidence)
     VALUES($1,$2,'refund.provider_correlated',$3,$3,jsonb_build_object('operationId',$4::text,'providerRefundId',$5::text))`, paymentID, eventID, intentStatus, operation, refundID); err != nil {
				return false, err
			}
			bound = &refundID
		}
	}
	next := "pending"
	if result == "succeeded" {
		next = "succeeded"
	} else if result == "failed" || result == "canceled" {
		next = "failed"
	}
	// Success is irreversible; a known failure also cannot regress to pending.
	if prior == "succeeded" || prior == "failed" && next == "pending" {
		next = prior
	}
	if _, err := tx.Exec(ctx, `UPDATE product_refund_attempts SET status=$2,updated_at=now(),
    reconciliation_required=CASE WHEN status<>'succeeded' AND $2 IN ('failed','succeeded') THEN false ELSE reconciliation_required END
    WHERE operation_id=$1`, operation, next); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status,evidence)
   VALUES($1,$2,'refund.operation_observed',$3,$3,jsonb_build_object('operationId',$4::text,'providerStatus',$5::text))`, paymentID, eventID, intentStatus, operation, result); err != nil {
		return false, err
	}
	if oneOf(intentStatus, "cancelled", "payment_failed") {
		var closedRecovery bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_closed_checkout_recoveries WHERE payment_id=$1)`, paymentID).Scan(&closedRecovery); err != nil {
			return false, err
		}
		if closedRecovery {
			// Preserve the actual operation outcome first. Only a subsequent full
			// history reconciliation can advance this closed order, including when
			// another unknown or pending refund exists in the same read.
			return false, nil
		}
	}
	if result == "succeeded" {
		// Other remotely uncertain operations remain visible for reconciliation;
		// they must not be dispatched again after this order becomes refunded.
		if _, err := tx.Exec(ctx, `UPDATE product_refund_attempts SET reconciliation_required=true,updated_at=now()
    WHERE payment_id=$1 AND operation_id<>$2 AND status IN ('requested','pending')`, paymentID, operation); err != nil {
			return false, err
		}
		if intentStatus == "refunded" {
			if prior != "succeeded" {
				if _, err := tx.Exec(ctx, `UPDATE product_refund_attempts SET reconciliation_required=true WHERE payment_id=$1 AND status='succeeded'`, paymentID); err != nil {
					return false, err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status,evidence)
      VALUES($1,$2,'refund.multiple_successes','refunded','refunded',jsonb_build_object('operationId',$3::text))`, paymentID, eventID, operation); err != nil {
					return false, err
				}
			}
			return false, nil
		}
		// For Waffo the signed identifier is the external operation ID, whereas the
		// stored refund ID is the optional provider ticket returned by dispatch.
		if _, err := tx.Exec(ctx, `UPDATE payment_intents SET provider_refund_id=$2 WHERE id=$1`, paymentID, bound); err != nil {
			return false, err
		}
		return true, nil
	}
	if intentStatus == "refunded" || current == nil || *current != *operation || prior == "failed" || prior == "succeeded" {
		return false, nil
	}
	if provider == "stripe" {
		if _, err := tx.Exec(ctx, `UPDATE payment_intents SET provider_refund_id=$2 WHERE id=$1`, paymentID, bound); err != nil {
			return false, err
		}
	}
	return true, nil
}
