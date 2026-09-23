package payments

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/jackc/pgx/v5"
)

// Once a complete read has reconciled every original operation, an existing
// full refund may close a historical order. No outgoing refund or local
// operation is created: the immutable confirmation points to the actual read,
// processed refund event and original operation.
func confirmClosedCheckoutRefundTx(ctx context.Context, tx pgx.Tx, paymentID, checkID uuid.UUID) error {
	var eventID, operationID uuid.UUID
	var providerPayment, providerRefund, currency string
	var amount int
	err := tx.QueryRow(ctx, `SELECT event_id,operation_id,provider_payment_id,provider_refund_id,amount_cents,currency
 FROM product_closed_checkout_refund_candidates WHERE payment_id=$1 AND check_id=$2`, paymentID, checkID).Scan(
		&eventID, &operationID, &providerPayment, &providerRefund, &amount, &currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO product_closed_checkout_refund_confirmations(payment_id,check_id,event_id,operation_id)
 VALUES($1,$2,$3,$4)`, paymentID, checkID, eventID, operationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_intents SET provider_refund_id=$2 WHERE id=$1`, paymentID, providerRefund); err != nil {
		return err
	}
	return refundProductPaymentTx(ctx, tx, "stripe", eventID, paymentID, providerRefund, providerPayment, amount, currency)
}

func closedCheckoutRefundConfirmedTx(ctx context.Context, tx pgx.Tx, paymentID, eventID uuid.UUID) (bool, error) {
	var confirmed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_closed_checkout_refund_confirmations
 WHERE payment_id=$1 AND event_id=$2)`, paymentID, eventID).Scan(&confirmed)
	return confirmed, err
}

// A cleanup job may have run before the original paid event cleared its
// recovery hold. Re-evaluate cleanup when that event finishes as well.
func enqueueConfirmedClosedCheckoutCleanupTx(ctx context.Context, tx pgx.Tx, paymentID, eventID uuid.UUID) error {
	var orderID uuid.UUID
	err := tx.QueryRow(ctx, `SELECT p.order_id FROM product_closed_checkout_refund_confirmations f
 JOIN product_closed_checkout_recoveries r ON r.payment_id=f.payment_id
 JOIN payment_intents p ON p.id=f.payment_id AND p.status='refunded'
 WHERE f.payment_id=$1 AND r.event_id=$2`, paymentID, eventID).Scan(&orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return datarights.EnqueueProductMediaCleanupTx(ctx, tx, orderID)
}
