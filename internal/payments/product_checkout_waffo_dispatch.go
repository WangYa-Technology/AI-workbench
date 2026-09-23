package payments

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Called with the payment locked. Only this invocation may use the returned
// version after committing the reservation. It must never be reconstructed by
// a retry: even a crash before sending leaves an uncertain remote outcome.
func reserveWaffoCheckoutTx(ctx context.Context, tx pgx.Tx, payment uuid.UUID) (int64, error) {
	var eligible bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_intents p
 JOIN product_checkout_requests r ON r.payment_id=p.id AND r.dispatch_protocol='guarded_v1'
 WHERE p.id=$1 AND p.purpose='product' AND p.provider='waffo_pancake' AND p.status='checkout_pending'
 AND p.provider_checkout_id IS NULL AND p.provider_payment_id IS NULL AND p.provider_charge_id IS NULL
 AND p.checkout_url IS NULL AND NOT EXISTS(SELECT 1 FROM product_checkout_dispatches d WHERE d.payment_id=p.id))`, payment).Scan(&eligible); err != nil {
		return 0, err
	}
	if !eligible {
		return 0, ErrCheckoutReconciliation
	}
	if err := reserveProductDispatchTx(ctx, tx, payment); err != nil {
		return 0, err
	}
	var version int64
	err := tx.QueryRow(ctx, `SELECT version FROM payment_intents WHERE id=$1`, payment).Scan(&version)
	return version, err
}

func validateWaffoCheckoutPermitTx(ctx context.Context, tx pgx.Tx, payment uuid.UUID, version int64) error {
	// Preserve independent lookup/quarantine holds, including ones recorded
	// between reservation commit and reacquiring the payment lock.
	if err := validateProductCheckoutEvidence(ctx, tx, payment); err != nil {
		return err
	}
	var valid bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_intents p
 JOIN product_checkout_requests r ON r.payment_id=p.id AND r.dispatch_protocol='guarded_v1'
 JOIN product_checkout_dispatches d ON d.payment_id=p.id
 WHERE p.id=$1 AND p.version=$2 AND $2>0 AND p.purpose='product' AND p.provider='waffo_pancake'
 AND p.status='checkout_pending' AND p.provider_checkout_id IS NULL AND p.provider_payment_id IS NULL
 AND p.provider_charge_id IS NULL AND p.checkout_url IS NULL
 AND d.request_sha256=encode(public.digest(r.request::text,'sha256'),'hex'))`, payment, version).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrCheckoutReconciliation
	}
	return nil
}
