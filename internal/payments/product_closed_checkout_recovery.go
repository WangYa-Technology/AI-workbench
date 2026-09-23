package payments

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A saved paid session for a closed order proves receipt, not the absence of a
// historical refund. Bind its original payment and queue a read before allowing
// the existing compensation workflow to create a refund obligation.
func preserveClosedCheckoutPaymentTx(ctx context.Context, tx pgx.Tx, paymentID, eventID, jobID uuid.UUID, status, orderStatus string, observation CheckoutObservation) error {
	if _, err := tx.Exec(ctx, `INSERT INTO product_closed_checkout_recoveries(payment_id,event_id,checkout_job_id,from_payment_status,from_order_status)
 VALUES($1,$2,$3,$4,$5)`, paymentID, eventID, jobID, status, orderStatus); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_intents SET provider_payment_id=$2,provider_charge_id=NULLIF($3,''),version=version+1,updated_at=now() WHERE id=$1`, paymentID, observation.ProviderPaymentID, observation.ProviderChargeID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
 VALUES($1,'checkout.closed_payment_recovered',$2,$2,jsonb_build_object('eventId',$3::text,'jobId',$4::text,'next','refund_history_check'))`, paymentID, status, eventID, jobID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,request_id,metadata)
 VALUES('payment.closed_checkout_recovered','payment',$1,$2,jsonb_build_object('eventId',$3::uuid,'checkoutJobId',$4::uuid))`, paymentID, "closed-checkout:"+jobID.String(), eventID, jobID); err != nil {
		return err
	}
	return enqueueClosedCheckoutHistoryTx(ctx, tx, paymentID)
}

func enqueueClosedCheckoutHistoryTx(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID) error {
	var needed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_closed_checkout_recoveries r WHERE r.payment_id=$1
 AND NOT EXISTS(SELECT 1 FROM product_closed_checkout_funds_ready f WHERE f.payment_id=r.payment_id)
 AND NOT EXISTS(SELECT 1 FROM product_refund_checks c WHERE c.payment_id=r.payment_id AND c.created_at>=r.created_at))
 AND NOT EXISTS(SELECT 1 FROM product_refund_checks c WHERE c.payment_id=$1 AND c.status IN ('requested','observed'))`, paymentID).Scan(&needed); err != nil {
		return err
	}
	if !needed {
		return nil
	}
	_, err := insertProductRefundCheckWithOriginTx(ctx, tx, nil, paymentID, "automatic")
	return err
}

func enqueueCheckedClosedCheckoutTx(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID) error {
	if err := enqueueClosedCheckoutHistoryTx(ctx, tx, paymentID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts)
 SELECT $2,jsonb_build_object('eventId',r.event_id::text),20 FROM product_closed_checkout_funds_ready r
 JOIN payment_provider_event_processing p ON p.event_id=r.event_id AND p.status='received'
 WHERE r.payment_id=$1 AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.kind=$2 AND j.payload->>'eventId'=r.event_id::text)`, paymentID, PaymentEventJobKind)
	return err
}

func validateClosedCheckoutFundsTx(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID) error {
	var unverified bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_closed_checkout_recoveries r WHERE r.payment_id=$1
 AND NOT EXISTS(SELECT 1 FROM product_closed_checkout_funds_ready f WHERE f.payment_id=r.payment_id))
 OR EXISTS(SELECT 1 FROM payment_intents p WHERE p.id=$1 AND p.status IN ('cancelled','payment_failed')
 AND EXISTS(SELECT 1 FROM product_checkout_session_evidence e WHERE e.payment_id=p.id AND e.payment_status='paid')
 AND NOT EXISTS(SELECT 1 FROM product_closed_checkout_recoveries r WHERE r.payment_id=p.id))`, paymentID).Scan(&unverified); err != nil {
		return err
	}
	if unverified {
		return ErrCheckoutReconciliation
	}
	return nil
}
