package payments

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func handleProductDisputeEventTx(ctx context.Context, tx pgx.Tx, providerEventID uuid.UUID, paymentID *uuid.UUID, eventType, disputeID string, liveMode bool, amount *int64, currency, providerPaymentID, providerChargeID, disputeStatus, disputeReason, networkReason *string, dueBy *time.Time, occurredAt time.Time) error {
	if providerEventID == uuid.Nil || !validStripeID(disputeID, "dp_") || amount == nil || currency == nil || *currency != "USD" || providerPaymentID == nil || providerChargeID == nil || disputeStatus == nil || disputeReason == nil || networkReason == nil || dueBy == nil {
		return newProviderFailure("payment_response_invalid", 0)
	}
	var orderID, sellerID, settlementID *uuid.UUID
	if paymentID != nil {
		var order, seller uuid.UUID
		var settlement *uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT pi.order_id,pi.payee_id,ps.id
			FROM payment_intents pi LEFT JOIN product_settlements ps ON ps.payment_id=pi.id
			WHERE pi.id=$1 AND pi.purpose='product' AND pi.provider='stripe' AND pi.live_mode=$2
			  AND pi.provider_payment_id=$3 AND pi.provider_charge_id=$4 AND pi.amount_cents=$5 AND pi.currency=$6
			FOR UPDATE OF pi`, *paymentID, liveMode, *providerPaymentID, *providerChargeID, *amount, *currency).Scan(&order, &seller, &settlement)
		if errors.Is(err, pgx.ErrNoRows) {
			paymentID = nil
		} else if err != nil {
			return err
		} else {
			orderID, sellerID, settlementID = &order, &seller, settlement
		}
	}
	action := disputeAction(*disputeStatus, orderID != nil)
	var storedDisputeID uuid.UUID
	var effectiveAction string
	var storedSettlementID *uuid.UUID
	var storedPaymentID, storedOrderID, storedSellerID *uuid.UUID
	var storedProviderStatus string
	var storedDueBy, storedLatestEventAt time.Time
	var storedLatestEventID uuid.UUID
	var storedVersion int64
	err := tx.QueryRow(ctx, `
		SELECT id,payment_id,order_id,seller_id,settlement_id,provider_status,action_status,due_by,latest_event_at,latest_event_id,version
		FROM product_payment_disputes
		WHERE provider='stripe' AND live_mode=$1 AND provider_dispute_id=$2
		FOR UPDATE`, liveMode, disputeID).Scan(
		&storedDisputeID, &storedPaymentID, &storedOrderID, &storedSellerID, &storedSettlementID,
		&storedProviderStatus, &effectiveAction, &storedDueBy, &storedLatestEventAt, &storedLatestEventID, &storedVersion)
	newRoot := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !newRoot {
		return err
	}
	if newRoot {
		effectiveAction = action
		storedPaymentID = paymentID
		storedOrderID = orderID
		storedSellerID = sellerID
		storedSettlementID = settlementID
		storedProviderStatus = *disputeStatus
		storedDueBy = *dueBy
		storedLatestEventAt = occurredAt
		storedLatestEventID = providerEventID
		storedVersion = 1
		if err := tx.QueryRow(ctx, `
			INSERT INTO product_payment_disputes(provider,live_mode,provider_dispute_id,payment_id,order_id,seller_id,settlement_id,
			 provider_payment_id,provider_charge_id,amount_cents,currency,provider_status,action_status,due_by,latest_event_at,latest_event_id,version,created_at,updated_at)
			VALUES('stripe',$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,1,clock_timestamp(),clock_timestamp())
			RETURNING id`, liveMode, disputeID, paymentID, orderID, sellerID, settlementID, *providerPaymentID, *providerChargeID, *amount, *currency, *disputeStatus, action, *dueBy, occurredAt, providerEventID).Scan(&storedDisputeID); err != nil {
			return err
		}
	}

	newer := newRoot || isNewerDisputeEvent(occurredAt, providerEventID, storedLatestEventAt, storedLatestEventID)
	if !newRoot && newer {
		effectiveAction = action
	}
	// Insert the immutable evidence before changing the mutable projection. A
	// duplicate provider event must be a complete no-op, including version and
	// settlement events.
	var insertedEventID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO product_payment_dispute_events(dispute_id,provider_event_id,event_type,provider_status,reason,network_reason_code,due_by,occurred_at,applied)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT(provider_event_id) DO NOTHING
		RETURNING id`, storedDisputeID, providerEventID, eventType, *disputeStatus, *disputeReason, *networkReason, *dueBy, occurredAt, newer).Scan(&insertedEventID)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingDisputeID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT dispute_id FROM product_payment_dispute_events WHERE provider_event_id=$1`, providerEventID).Scan(&existingDisputeID); err != nil {
			return err
		}
		if existingDisputeID != storedDisputeID {
			return newProviderFailure("payment_event_conflict", 0)
		}
		return nil
	}
	if err != nil {
		return err
	}
	_ = insertedEventID

	if !newRoot {
		bindingChanged := (storedPaymentID == nil && paymentID != nil) || (storedOrderID == nil && orderID != nil) ||
			(storedSellerID == nil && sellerID != nil) || (storedSettlementID == nil && settlementID != nil)
		if newer || bindingChanged {
			if newer {
				storedProviderStatus = *disputeStatus
				storedDueBy = minTime(storedDueBy, *dueBy)
				storedLatestEventAt = occurredAt
				storedLatestEventID = providerEventID
			} else {
				storedDueBy = minTime(storedDueBy, *dueBy)
			}
			if _, err := tx.Exec(ctx, `
				UPDATE product_payment_disputes SET
				 payment_id=COALESCE(payment_id,$2),order_id=COALESCE(order_id,$3),seller_id=COALESCE(seller_id,$4),settlement_id=COALESCE(settlement_id,$5),
				 provider_status=CASE WHEN $6 THEN $7 ELSE provider_status END,
				 action_status=CASE WHEN $6 THEN $8 ELSE action_status END,
				 due_by=$9,latest_event_at=CASE WHEN $6 THEN $10 ELSE latest_event_at END,
				 latest_event_id=CASE WHEN $6 THEN $11 ELSE latest_event_id END,
				 version=version+1,updated_at=clock_timestamp()
				WHERE id=$1`, storedDisputeID, paymentID, orderID, sellerID, settlementID, newer, storedProviderStatus, effectiveAction, storedDueBy, storedLatestEventAt, storedLatestEventID); err != nil {
				return err
			}
			if storedPaymentID == nil {
				storedPaymentID = paymentID
			}
			if storedOrderID == nil {
				storedOrderID = orderID
			}
			if storedSellerID == nil {
				storedSellerID = sellerID
			}
			if storedSettlementID == nil {
				storedSettlementID = settlementID
			}
		}
	}
	// The stored binding is authoritative. A later malformed or temporarily
	// unmatchable event must not erase an earlier safe binding or release its
	// hold merely because the incoming tuple cannot be resolved.
	settlementID = storedSettlementID
	if settlementID == nil {
		return nil
	}
	var settlementStatus string
	var availableAt time.Time
	if err := tx.QueryRow(ctx, `SELECT status,available_at FROM product_settlements WHERE id=$1 FOR UPDATE`, *settlementID).Scan(&settlementStatus, &availableAt); err != nil {
		return err
	}
	var otherOpen bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM product_payment_disputes
		WHERE settlement_id=$1 AND id<>$2 AND action_status <> 'won'
	)`, *settlementID, storedDisputeID).Scan(&otherOpen); err != nil {
		return err
	}
	to, holdReason := productDisputeSettlementTransition(effectiveAction, settlementStatus, availableAt, time.Now().UTC(), otherOpen)
	if to == settlementStatus && holdReason == "" {
		return nil
	}
	if to != settlementStatus {
		if _, err := tx.Exec(ctx, `UPDATE product_settlements SET status=$2,hold_reason=$3,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, *settlementID, to, holdReason); err != nil {
			return err
		}
		if err := recordProductSettlementEventTx(ctx, tx, *settlementID, "dispute."+strings.ReplaceAll(effectiveAction, "_", "."), settlementStatus, to, map[string]any{"disputeId": disputeID, "providerEventId": providerEventID}); err != nil {
			return err
		}
	}
	return nil
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func isNewerDisputeEvent(occurredAt time.Time, eventID uuid.UUID, latestAt time.Time, latestID uuid.UUID) bool {
	if occurredAt.After(latestAt) {
		return true
	}
	return occurredAt.Equal(latestAt) && bytes.Compare(eventID[:], latestID[:]) > 0
}

// productDisputeSettlementTransition is deliberately conservative. A winning
// dispute can release an unpaid hold only when no other dispute remains open;
// a transfer that already entered execution/recovery is never reopened by a
// provider status update.
func productDisputeSettlementTransition(action, settlementStatus string, availableAt, now time.Time, otherOpen bool) (string, string) {
	to := settlementStatus
	holdReason := ""
	switch action {
	case "needs_response", "under_review", "warning_needs_response", "warning_under_review", "requires_review":
		if oneOf(settlementStatus, "pending_hold", "available") {
			to = "dispute_hold"
			holdReason = "provider_dispute"
		} else if oneOf(settlementStatus, "transfer_pending", "transferred") {
			to = "recovery_required"
			holdReason = "provider_dispute"
		}
	case "lost", "charge_refunded", "prevented":
		if oneOf(settlementStatus, "pending_hold", "available", "dispute_hold") {
			to = "refund_hold"
			holdReason = "provider_dispute"
		} else if oneOf(settlementStatus, "transfer_pending", "transferred") {
			to = "recovery_required"
			holdReason = "provider_dispute"
		}
	case "won":
		if settlementStatus == "dispute_hold" && !otherOpen {
			if now.Before(availableAt) {
				to, holdReason = "pending_hold", "refund_window"
			} else {
				to, holdReason = "available", ""
			}
		}
	}
	return to, holdReason
}

func disputeAction(status string, bound bool) string {
	if !bound {
		return "requires_review"
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "needs_response":
		return "needs_response"
	case "warning_needs_response":
		return "warning_needs_response"
	case "under_review":
		return "under_review"
	case "warning_under_review":
		return "warning_under_review"
	case "won":
		return "won"
	case "lost":
		return "lost"
	case "charge_refunded":
		return "charge_refunded"
	case "prevented":
		return "prevented"
	default:
		return "requires_review"
	}
}
