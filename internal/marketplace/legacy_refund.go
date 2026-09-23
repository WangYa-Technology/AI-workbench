package marketplace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/productpolicy"
	"github.com/hcai-chat/hcai-chat/internal/risk"
	"github.com/hcai-chat/hcai-chat/internal/webhooks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// RefundLegacyOrder only reverses a recorded internal balance purchase. It
// cannot create a purchase, charge a provider, or refund an external payment.
func (s *Service) RefundLegacyOrder(ctx context.Context, buyerID, orderID uuid.UUID, idempotencyKey, requestID, reason string) (Order, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	reason = strings.TrimSpace(reason)
	if orderID == uuid.Nil || len(idempotencyKey) < 8 || len(idempotencyKey) > 128 || !productpolicy.ValidRefundReason(reason) {
		return Order{}, ErrInvalidRefund
	}
	if requestID == "" {
		requestID = "marketplace-refund"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Order{}, fmt.Errorf("begin refund: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	var createdAt time.Time
	var refundWindowDays int
	var sellerID *uuid.UUID
	var assetID uuid.UUID
	var savedKey, savedReason, entitlementStatus string
	// Determine the original payee from accounting evidence, not current catalog.
	// Lock subjects before the order, matching account deletion's lock order.
	if err = tx.QueryRow(ctx, `SELECT proof.seller_id FROM legacy_product_refund_evidence proof JOIN orders o ON o.id=proof.order_id WHERE o.id=$1 AND o.buyer_id=$2`, orderID, buyerID).Scan(&sellerID); errors.Is(err, pgx.ErrNoRows) {
		var owned bool
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders WHERE id=$1 AND buyer_id=$2)`, orderID, buyerID).Scan(&owned); e != nil {
			return Order{}, e
		}
		if !owned {
			return Order{}, ErrOrderNotFound
		}
		return Order{}, ErrLegacyRefundEvidence
	} else if err != nil {
		return Order{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 OR id=$2 ORDER BY id FOR UPDATE`, buyerID, sellerID); err != nil {
		return Order{}, err
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND status='active')`, buyerID).Scan(&active); err != nil {
		return Order{}, err
	}
	if !active {
		return Order{}, ErrRefundConflict
	}
	err = tx.QueryRow(ctx, `
		SELECT o.status,o.created_at,o.refund_window_days_snapshot,e.asset_id,COALESCE(o.refund_idempotency_key,''),COALESCE(o.refund_reason,''),e.status
		FROM orders o
		JOIN entitlements e ON e.order_id=o.id
		WHERE o.id=$1 AND o.buyer_id=$2 FOR UPDATE OF o,e`, orderID, buyerID).Scan(
		&status, &createdAt, &refundWindowDays, &assetID, &savedKey, &savedReason, &entitlementStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrOrderNotFound
	}
	if err != nil {
		return Order{}, fmt.Errorf("load refund order: %w", err)
	}
	// Hold original evidence rows and then re-evaluate eligibility in a fresh
	// statement after waiting. Locking the order also blocks new FK references.
	if _, err = tx.Exec(ctx, `SELECT id FROM billing_entries WHERE operation_id=$1 ORDER BY id FOR SHARE`, orderID); err != nil {
		return Order{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM ledger_entries WHERE operation_id=$1 ORDER BY id FOR SHARE`, orderID); err != nil {
		return Order{}, err
	}
	var currentSeller *uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT seller_id FROM legacy_product_refund_evidence WHERE order_id=$1`, orderID).Scan(&currentSeller); errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrLegacyRefundEvidence
	} else if err != nil {
		return Order{}, err
	}
	if (sellerID == nil) != (currentSeller == nil) || (sellerID != nil && *sellerID != *currentSeller) {
		return Order{}, ErrLegacyRefundEvidence
	}
	if status == "test_refunded" {
		if savedKey != idempotencyKey || savedReason != reason {
			return Order{}, ErrIdempotencyConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return Order{}, fmt.Errorf("commit refund replay: %w", err)
		}
		return s.GetOrder(ctx, buyerID, orderID)
	}
	if status != "fulfilled" || entitlementStatus != "active" {
		return Order{}, ErrRefundConflict
	}
	if !productpolicy.RefundWindowOpen(createdAt, refundWindowDays, time.Now()) {
		return Order{}, ErrRefundWindowExpired
	}
	refundOperationID := uuid.New()
	refundedAt := time.Now()
	result, err := tx.Exec(ctx, `
		UPDATE orders SET status='test_refunded',refund_reason=$3,refund_idempotency_key=$4,refund_operation_id=$5,
		                  refund_requested_at=$6,refunded_at=$6,updated_at=$6
		WHERE id=$1 AND buyer_id=$2 AND status='fulfilled'`, orderID, buyerID, reason, idempotencyKey, refundOperationID, refundedAt)
	if err != nil {
		var constraint *pgconn.PgError
		if errors.As(err, &constraint) && constraint.Code == "23505" && constraint.ConstraintName == "orders_refund_idempotency_idx" {
			return Order{}, ErrIdempotencyConflict
		}
		return Order{}, fmt.Errorf("refund order: %w", err)
	}
	if result.RowsAffected() != 1 {
		return Order{}, ErrRefundConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE entitlements SET status='refunded',revoked_at=$2 WHERE order_id=$1 AND status='active'`, orderID, refundedAt); err != nil {
		return Order{}, fmt.Errorf("revoke entitlement: %w", err)
	}
	if err := datarights.EnqueueProductMediaCleanupTx(ctx, tx, orderID); err != nil {
		return Order{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_events(order_id,actor_id,from_status,to_status,reason,created_at,sequence)
		VALUES ($1,$2,'fulfilled','refund_requested',$3,$4,(SELECT COALESCE(max(sequence),0)+1 FROM order_events WHERE order_id=$1)),
		       ($1,$2,'refund_requested','test_refunded','Historical internal balance reversed; no provider funds moved.',$4,(SELECT COALESCE(max(sequence),0)+2 FROM order_events WHERE order_id=$1))`,
		orderID, buyerID, reason, refundedAt); err != nil {
		return Order{}, fmt.Errorf("record refund events: %w", err)
	}
	var amountCents int
	var currency, productTitle string
	if err := tx.QueryRow(ctx, `SELECT amount_cents,currency,product_title_snapshot FROM orders WHERE id=$1`, orderID).Scan(&amountCents, &currency, &productTitle); err != nil {
		return Order{}, fmt.Errorf("load refund amount: %w", err)
	}
	if amountCents > 0 {
		if err := billing.TransferTx(ctx, tx, *sellerID, buyerID, refundOperationID, amountCents, currency,
			"product_refund", "product_refund", "Historical internal product reversal: "+productTitle); err != nil {
			return Order{}, fmt.Errorf("apply product refund transfer: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries(account_id,operation_id,direction,amount_cents,currency,reason,created_at)
			VALUES ($1,$3,'debit',$4,$5,'test_refund_debit_no_real_payout',$6),
			       ($2,$3,'credit',$4,$5,'test_refund_credit_no_real_charge',$6)`,
			sellerID, buyerID, refundOperationID, amountCents, currency, refundedAt); err != nil {
			return Order{}, fmt.Errorf("record balanced test refund: %w", err)
		}
	}
	if _, err := risk.RecordTx(ctx, tx, risk.SignalInput{
		SourceKey: "order_refund:" + orderID.String(), ResourceType: "order", ResourceID: orderID,
		SubjectUserID: buyerID, ActorUserID: &buyerID, SignalType: "transaction_refund", Severity: "medium", Score: 55,
		Summary:  "Historical internal balance reversal.",
		Evidence: map[string]any{"orderStatus": "test_refunded", "amountCents": amountCents, "currency": currency, "paymentMode": "local_test"},
	}); err != nil {
		return Order{}, fmt.Errorf("record refund risk signal: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'marketplace.legacy_refund','order',$2,$3,jsonb_build_object('assetId',$4::text,'realCharge',false,'paymentMode','test'))`,
		buyerID, orderID, requestID, assetID); err != nil {
		return Order{}, fmt.Errorf("audit test refund: %w", err)
	}
	if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: buyerID, Kind: "marketplace.order_refunded", Title: "Historical balance reversal completed",
		Body:       "\u201c" + productTitle + "\u201d had its historical internal balance reversed. Its access and reuse rights were revoked.",
		TargetPath: "/workspace/orders", ResourceType: "order", ResourceID: &orderID,
		SourceKey: "marketplace:order:" + orderID.String() + ":refunded",
	}); err != nil {
		return Order{}, fmt.Errorf("notify refunded purchase: %w", err)
	}
	if err := webhooks.EnqueueTx(ctx, tx, webhooks.EventInput{OwnerID: buyerID, EventType: "marketplace.order.refunded", ResourceType: "order", ResourceID: &orderID, SourceKey: "marketplace:order:" + orderID.String() + ":refunded"}); err != nil {
		return Order{}, fmt.Errorf("enqueue refunded-order webhook: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Order{}, fmt.Errorf("commit refund: %w", err)
	}
	return s.GetOrder(ctx, buyerID, orderID)
}
