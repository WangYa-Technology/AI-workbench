package payments

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
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
			// Product financial writers lock payment, then order, then seller funds
			// and settlement. Keep the dispute path in that order as well.
			if _, err := tx.Exec(ctx, `SELECT id FROM orders WHERE id=$1 FOR UPDATE`, order); err != nil {
				return err
			}
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
	var tupleMatches bool
	err := tx.QueryRow(ctx, `
		SELECT id,payment_id,order_id,seller_id,settlement_id,provider_status,action_status,due_by,latest_event_at,latest_event_id,version,provider_payment_id=$3 AND provider_charge_id=$4 AND amount_cents=$5 AND currency=$6
		FROM product_payment_disputes
		WHERE provider='stripe' AND live_mode=$1 AND provider_dispute_id=$2
		FOR UPDATE`, liveMode, disputeID, *providerPaymentID, *providerChargeID, *amount, *currency).Scan(
		&storedDisputeID, &storedPaymentID, &storedOrderID, &storedSellerID, &storedSettlementID,
		&storedProviderStatus, &effectiveAction, &storedDueBy, &storedLatestEventAt, &storedLatestEventID, &storedVersion, &tupleMatches)
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

	if !newRoot && !tupleMatches {
		return newProviderFailure("payment_event_conflict", 0)
	}
	newer := newRoot || isNewerDisputeEvent(occurredAt, providerEventID, storedLatestEventAt, storedLatestEventID)
	if !newRoot && newer {
		effectiveAction = action
	}
	// Insert the immutable evidence before changing the mutable projection. A
	// duplicate provider event preserves the projection. A pre-0178 terminal
	// event may still need its missing adjudication applied during upgrade replay.
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
		err = nil
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
	// Conflicting terminal evidence is retained, regardless of delivery order.
	var conflict bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_payment_dispute_events a JOIN product_payment_dispute_events b ON a.dispute_id=b.dispute_id WHERE a.dispute_id=$1 AND a.provider_status='won' AND b.provider_status IN ('lost','charge_refunded'))`, storedDisputeID).Scan(&conflict); err != nil {
		return err
	}
	if conflict {
		effectiveAction = "requires_review"
	}
	if (newer || providerEventID == storedLatestEventID) && paymentID != nil && !conflict && oneOf(effectiveAction, "won", "lost", "charge_refunded") {
		if err := applyProductDisputeAdjudicationTx(ctx, tx, storedDisputeID, providerEventID, effectiveAction, *storedPaymentID, storedOrderID, storedSellerID); err != nil {
			return err
		}
	}
	var review bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_payment_dispute_adjudications WHERE dispute_id=$1 AND financial_effect='requires_review')`, storedDisputeID).Scan(&review); err != nil {
		return err
	}
	if effectiveAction == "won" && storedPaymentID != nil {
		var unsafeRelease bool
		if err := tx.QueryRow(ctx, `SELECT pi.status<>'paid' OR o.status<>'fulfilled' OR EXISTS(SELECT 1 FROM product_refund_attempts a WHERE a.payment_id=pi.id AND (a.status IN ('requested','pending') OR a.reconciliation_required)) OR EXISTS(SELECT 1 FROM product_refund_checks c WHERE c.payment_id=pi.id AND (c.status IN ('requested','observed') OR c.unresolved_count>0)) FROM payment_intents pi JOIN orders o ON o.id=pi.order_id WHERE pi.id=$1`, *storedPaymentID).Scan(&unsafeRelease); err != nil {
			return err
		}
		review = review || unsafeRelease
	}
	if review || conflict {
		effectiveAction = "requires_review"
		changed, err := tx.Exec(ctx, `UPDATE product_payment_disputes SET action_status='requires_review',version=version+1,updated_at=clock_timestamp() WHERE id=$1 AND action_status<>'requires_review'`, storedDisputeID)
		if err != nil {
			return err
		}
		if changed.RowsAffected() > 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,request_id,metadata) VALUES('marketplace.provider_dispute_review','product_payment_dispute',$1,$2,jsonb_build_object('providerEventId',$3::text,'terminalConflict',$4::boolean))`, storedDisputeID, "stripe-dispute:"+providerEventID.String(), providerEventID, conflict); err != nil {
				return err
			}
		}
	}
	if storedSellerID != nil {
		if err := lockSellerFundsTx(ctx, tx, *storedSellerID); err != nil {
			return err
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
	case "needs_response", "under_review", "warning_needs_response", "warning_under_review", "requires_review", "withdrawn":
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
	case "withdrawn":
		return "requires_review"
	default:
		return "requires_review"
	}
}

func applyProductDisputeAdjudicationTx(ctx context.Context, tx pgx.Tx, disputeID, providerEventID uuid.UUID, outcome string, paymentID uuid.UUID, orderID, sellerID *uuid.UUID) error {
	var effect string
	var refundUnresolved bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_refund_attempts WHERE payment_id=$1 AND (status IN ('requested','pending') OR reconciliation_required)) OR EXISTS(SELECT 1 FROM product_refund_checks WHERE payment_id=$1 AND (status IN ('requested','observed') OR unresolved_count>0))`, paymentID).Scan(&refundUnresolved); err != nil {
		return err
	}
	switch outcome {
	case "won":
		var paymentStatus, orderStatus string
		if err := tx.QueryRow(ctx, `SELECT pi.status,o.status FROM payment_intents pi JOIN orders o ON o.id=pi.order_id WHERE pi.id=$1 FOR UPDATE OF pi,o`, paymentID).Scan(&paymentStatus, &orderStatus); err != nil {
			return err
		}
		if paymentStatus != "paid" || orderStatus != "fulfilled" {
			effect = "requires_review"
		} else {
			var canRelease bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_settlements s WHERE s.payment_id=$1 AND s.status='dispute_hold' AND NOT EXISTS(SELECT 1 FROM product_payment_disputes d WHERE d.settlement_id=s.id AND d.id<>$2 AND d.action_status<>'won'))`, paymentID, disputeID).Scan(&canRelease); err != nil {
				return err
			}
			effect = "no_change"
			if canRelease {
				effect = "release_hold"
			}
		}
	case "lost", "charge_refunded":
		var paymentStatus, orderStatus string
		if err := tx.QueryRow(ctx, `SELECT pi.status,o.status FROM payment_intents pi JOIN orders o ON o.id=pi.order_id WHERE pi.id=$1 FOR UPDATE OF pi,o`, paymentID).Scan(&paymentStatus, &orderStatus); err != nil {
			return err
		}
		switch {
		case paymentStatus == "refunded" && orderStatus == "refunded":
			var priorLoss bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_intent_events WHERE payment_id=$1 AND event_type='dispute.loss.applied')`, paymentID).Scan(&priorLoss); err != nil {
				return err
			}
			effect = "requires_review"
			if priorLoss {
				effect = "no_change"
			}
		case paymentStatus == "paid" && orderStatus == "fulfilled":
			effect = "loss_applied"
		default:
			effect = "requires_review"
		}
	default:
		effect = "no_change"
	}
	if refundUnresolved {
		effect = "requires_review"
	}
	result, err := tx.Exec(ctx, `INSERT INTO product_payment_dispute_adjudications(dispute_id,provider_event_id,outcome,financial_effect)
		VALUES($1,$2,$3,$4) ON CONFLICT(dispute_id) DO NOTHING`, disputeID, providerEventID, outcome, effect)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,request_id,metadata) VALUES('marketplace.provider_dispute_adjudicated','product_payment_dispute',$1,$2,jsonb_build_object('providerEventId',$3::text,'outcome',$4::text,'financialEffect',$5::text))`, disputeID, "stripe-dispute:"+providerEventID.String(), providerEventID, outcome, effect); err != nil {
		return err
	}
	var recorded string
	if err := tx.QueryRow(ctx, `SELECT financial_effect FROM product_payment_dispute_adjudications WHERE dispute_id=$1`, disputeID).Scan(&recorded); err != nil {
		return err
	}
	if recorded != effect || effect != "loss_applied" || (outcome != "lost" && outcome != "charge_refunded") {
		return nil
	}
	if orderID == nil || sellerID == nil {
		return nil
	}
	var paymentStatus, orderStatus string
	var buyerID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT pi.status,o.status,o.buyer_id FROM payment_intents pi JOIN orders o ON o.id=pi.order_id WHERE pi.id=$1 FOR UPDATE OF pi,o`, paymentID).Scan(&paymentStatus, &orderStatus, &buyerID); err != nil {
		return err
	}
	if paymentStatus == "refunded" && orderStatus == "refunded" {
		return nil
	}
	if paymentStatus != "paid" || orderStatus != "fulfilled" {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE entitlements SET status='refunded',revoked_at=clock_timestamp() WHERE order_id=$1 AND status='active'`, *orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE orders SET status='refunded',refunded_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`, *orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_intents SET status='refunded',refunded_at=clock_timestamp(),updated_at=clock_timestamp(),version=version+1 WHERE id=$1`, paymentID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO order_events(order_id,from_status,to_status,reason,sequence)
		SELECT $1,'fulfilled','refunded','Stripe dispute lost; access revoked. No additional refund was initiated.',COALESCE(max(sequence),0)+1 FROM order_events WHERE order_id=$1`, *orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,provider_event_id,event_type,from_status,to_status,evidence)
		VALUES($1,$2,'dispute.loss.applied','paid','refunded',jsonb_build_object('disputeId',$3::text,'providerOutcome',$4::text)) ON CONFLICT DO NOTHING`, paymentID, providerEventID, disputeID, outcome); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'marketplace.provider_dispute_lost','order',$2,$3,jsonb_build_object('paymentId',$4::text,'disputeId',$5::text,'providerOutcome',$6::text,'providerRefundInitiated',false))`, buyerID, *orderID, "stripe-dispute:"+providerEventID.String(), paymentID, disputeID, outcome); err != nil {
		return err
	}
	if err := datarights.EnqueueProductMediaCleanupTx(ctx, tx, *orderID); err != nil {
		return err
	}
	if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{UserID: buyerID, Kind: "marketplace.order_refunded", Title: "Payment dispute lost", Body: "The payment Provider confirmed the dispute loss. Access and reuse rights were revoked; no additional refund was initiated.", TargetPath: "/workspace/orders", ResourceType: "order", ResourceID: orderID, SourceKey: "marketplace:order:" + orderID.String() + ":dispute-loss"}); err != nil {
		return err
	}
	return markProductSettlementReversalTx(ctx, tx, paymentID, "provider_dispute_loss", "dispute.loss.applied")
}
