package payments

import (
	"context"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/jackc/pgx/v5"
)

// The signed payment and its refund obligation commit together. No purchased
// asset or entitlement is created for an order that cannot be delivered.
func compensateProductPaymentTx(ctx context.Context, tx pgx.Tx, provider string, eventID, paymentID, orderID, buyerID uuid.UUID, fromIntent, fromOrder, reason, providerPaymentID string, providerChargeID *string) error {
	operationID := uuid.New()
	if _, err := tx.Exec(ctx, `UPDATE payment_intents SET status='refund_pending',compensation_reason=$2,
		provider_payment_id=$3,provider_charge_id=COALESCE($4,provider_charge_id),paid_at=now(),updated_at=now(),version=version+1
		WHERE id=$1`, paymentID, reason, providerPaymentID, providerChargeID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE orders SET status='refund_requested',refund_reason=$2,
		refund_idempotency_key=$3,refund_operation_id=$4,refund_correlation_enabled=true,refund_requested_at=now(),updated_at=now() WHERE id=$1`,
		orderID, "Automatic refund: "+reason, "product-compensation:"+paymentID.String(), operationID); err != nil {
		return err
	}
	if err := RecordNewProductRefundAttemptTx(ctx, tx, paymentID, operationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO order_events(order_id,from_status,to_status,reason,sequence)
		SELECT $1,$2,'refund_requested',$3,COALESCE(max(sequence),0)+1 FROM order_events WHERE order_id=$1`,
		orderID, fromOrder, "Signed payment received; automatic refund required: "+reason); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status,evidence)
		VALUES($1,$2,'payment.compensation_required',$3,'refund_pending',jsonb_build_object('orderId',$4::text,'reason',$5::text))`,
		paymentID, eventID, fromIntent, orderID, reason); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts)
		VALUES($1,jsonb_build_object('paymentId',$2::text,'operationId',$3::text),20)`, ProductRefundJobKind, paymentID, operationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'marketplace.compensation_required','order',$2,$3,jsonb_build_object('paymentId',$4::text,'reason',$5::text))`,
		buyerID, orderID, providerEventRequestID(provider, eventID), paymentID, reason); err != nil {
		return err
	}
	return notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: buyerID, Kind: "marketplace.refund_requested", Title: "Purchase refund started",
		Body:       "Your payment was received, but this order cannot be delivered. A full refund has been requested.",
		TargetPath: "/workspace/orders", ResourceType: "order", ResourceID: &orderID,
		SourceKey: "marketplace:order:" + orderID.String() + ":compensation",
	})
}

func failProductCompensationTx(ctx context.Context, tx pgx.Tx, provider string, eventID, paymentID, orderID, buyerID uuid.UUID, providerStatus string) error {
	result, err := tx.Exec(ctx, `UPDATE payment_intents SET status='refund_failed',updated_at=now(),version=version+1 WHERE id=$1 AND status='refund_pending'`, paymentID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status,evidence)
		VALUES($1,$2,'refund.failed','refund_pending','refund_failed',jsonb_build_object('orderId',$3::text,'providerStatus',$4::text))`,
		paymentID, eventID, orderID, providerStatus); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO order_events(order_id,from_status,to_status,reason,sequence)
		SELECT $1,'refund_requested','refund_requested','Automatic refund did not complete; payment recovery is required.',COALESCE(max(sequence),0)+1 FROM order_events WHERE order_id=$1`, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'marketplace.compensation_failed','order',$2,$3,jsonb_build_object('paymentId',$4::text,'providerStatus',$5::text))`,
		buyerID, orderID, providerEventRequestID(provider, eventID), paymentID, providerStatus); err != nil {
		return err
	}
	return notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: buyerID, Kind: "marketplace.compensation_failed", Title: "Purchase refund needs attention",
		Body:       "Your order could not be delivered and its refund did not complete. Your payment remains due for refund. Contact support for assistance.",
		TargetPath: "/workspace/orders", ResourceType: "order", ResourceID: &orderID,
		SourceKey: "marketplace:order:" + orderID.String() + ":compensation-failed:" + eventID.String(),
	})
}
