package payments

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Check the frozen operation after any lock or merchant-identity wait, just
// before dispatch. A queue retry must not renew Stripe's idempotency lifetime,
// and an inferred historical timestamp cannot authorize a new money move.
func validateStripeProductRefundDispatchTx(ctx context.Context, tx pgx.Tx, paymentID, operationID uuid.UUID) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_refund_attempts a
 JOIN payment_intents pi ON pi.id=a.payment_id JOIN orders o ON o.id=pi.order_id
 WHERE a.payment_id=$1 AND a.operation_id=$2 AND a.provider='stripe' AND pi.provider=a.provider
 AND a.status='requested' AND a.provider_refund_id IS NULL
 AND o.refund_operation_id=a.operation_id AND o.refund_requested_at=a.requested_at
 AND a.provider_payment_id=pi.provider_payment_id AND a.amount_cents=pi.amount_cents AND a.currency=pi.currency
 AND a.correlation_enabled=o.refund_correlation_enabled
 AND a.requested_at<=clock_timestamp() AND a.requested_at>clock_timestamp()-interval '23 hours')`, paymentID, operationID).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrCheckoutReconciliation
	}
	return nil
}

// reserveWaffoRefundTx consumes the new operation's one-shot permit. The caller
// must COMMIT before calling the provider. A rollback/error after that commit
// cannot make the operation dispatchable again, including after process loss.
func reserveWaffoRefundTx(ctx context.Context, tx pgx.Tx, paymentID, operationID uuid.UUID) (int, error) {
	result, err := tx.Exec(ctx, `UPDATE product_refund_dispatches d SET reserved_at=now()
 WHERE d.operation_id=$1 AND d.contract_version=$2 AND d.reserved_at IS NULL
 AND EXISTS(SELECT 1 FROM product_refund_attempts a WHERE a.operation_id=d.operation_id
   AND a.payment_id=$3 AND a.provider='waffo_pancake' AND a.status='requested')`, operationID, waffoRefundContractVersion, paymentID)
	if err != nil {
		return 0, err
	}
	if result.RowsAffected() != 1 {
		return 0, newProviderFailure("payment_reconciliation_required", 0)
	}
	var version int
	if err := tx.QueryRow(ctx, `UPDATE payment_intents SET version=version+1,updated_at=now() WHERE id=$1 RETURNING version`, paymentID).Scan(&version); err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
 VALUES($1,'refund.dispatch_reserved','refund_pending','refund_pending',jsonb_build_object('operationId',$2::text,'contractVersion',$3::text))`, paymentID, operationID, waffoRefundContractVersion)
	return version, err
}

// Reacquire the payment/order locks after committing the reservation. Only the
// caller that committed it can reach here; retries stop at the combined gate.
// Keep the locks until the single remote call and response recording finish so
// another operation/result cannot invalidate the check during dispatch.
func lockWaffoRefundReservationTx(ctx context.Context, tx pgx.Tx, paymentID, operationID uuid.UUID, version int) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT pi.version=$3 AND pi.status='refund_pending'
 AND o.status='refund_requested' AND o.refund_operation_id=$2
 AND pi.provider='waffo_pancake' AND pi.provider_refund_id IS NULL
 FROM payment_intents pi JOIN orders o ON o.id=pi.order_id
 WHERE pi.id=$1 AND pi.purpose='product' FOR UPDATE OF pi,o`, paymentID, operationID, version).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_reconciliation_required", 0)
	}
	if err != nil {
		return err
	}
	if !valid {
		return newProviderFailure("payment_reconciliation_required", 0)
	}
	err = tx.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM product_refund_dispatches WHERE operation_id=$2 AND reserved_at IS NOT NULL
   AND responded_at IS NULL AND contract_version=$3)
 AND NOT EXISTS(SELECT 1 FROM product_refund_funds_review WHERE payment_id=$1)
 AND NOT EXISTS(SELECT 1 FROM product_webhook_quarantine_review WHERE payment_id=$1)
 AND NOT EXISTS(SELECT 1 FROM product_refund_dispatch_review WHERE payment_id=$1 AND operation_id<>$2)`, paymentID, operationID, waffoRefundContractVersion).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return newProviderFailure("payment_reconciliation_required", 0)
	}
	return nil
}
