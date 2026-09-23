package payments

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/jackc/pgx/v5"
)

var (
	ErrCheckoutClosed        = errors.New("checkout closed before payment dispatch")
	ErrCheckoutCloseInvalid  = errors.New("invalid checkout closure command")
	ErrCheckoutCloseConflict = errors.New("checkout can no longer be closed locally")
	ErrCheckoutOrderNotFound = errors.New("checkout order not found")
	ErrCheckoutPreparation   = errors.New("product delivery preparation failed")
)

// reserveProductDispatchTx commits before the remote call. Its presence means
// the request MAY have reached the Provider, even if every response was lost.
func reserveProductDispatchTx(ctx context.Context, tx pgx.Tx, payment uuid.UUID) error {
	result, err := tx.Exec(ctx, `INSERT INTO product_checkout_dispatches(payment_id,request_sha256)
	 SELECT payment_id,encode(public.digest(request::text,'sha256'),'hex') FROM product_checkout_requests WHERE payment_id=$1
	 ON CONFLICT DO NOTHING`, payment)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return nil
	}
	if _, err = tx.Exec(ctx, `UPDATE payment_intents SET version=version+1,updated_at=now() WHERE id=$1`, payment); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
	 VALUES($1,'checkout.dispatch_reserved','checkout_pending','checkout_pending','{}')`, payment)
	return err
}

type CloseProductCheckoutInput struct {
	ExpectedVersion int64 `json:"expectedVersion"`
	Confirmed       bool  `json:"confirmed"`
}

// CloseProductCheckout never contacts the Provider and must remain available
// when checkout is disabled. Only write-ahead dispatch evidence can prove a
// new-protocol order never reached checkout; missing legacy IDs cannot.
func (s *Service) CloseProductCheckout(ctx context.Context, buyer, order uuid.UUID, key, requestID string, input CloseProductCheckoutInput) error {
	key = strings.TrimSpace(key)
	if buyer == uuid.Nil || order == uuid.Nil || !utf8.ValidString(key) || strings.ContainsRune(key, 0) || utf8.RuneCountInString(key) < 8 || utf8.RuneCountInString(key) > 128 || input.ExpectedVersion < 1 || !input.Confirmed {
		return ErrCheckoutCloseInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var product, payment uuid.UUID
	err = tx.QueryRow(ctx, `SELECT o.product_id,p.id FROM orders o JOIN payment_intents p ON p.order_id=o.id AND p.purpose='product'
	 JOIN users u ON u.id=o.buyer_id AND u.status='active' WHERE o.id=$1 AND o.buyer_id=$2`, order, buyer).Scan(&product, &payment)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCheckoutOrderNotFound
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "product-close:"+buyer.String()+":"+key); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "product-checkout:"+buyer.String()+":"+product.String()); err != nil {
		return err
	}
	var version int64
	if err = tx.QueryRow(ctx, `SELECT p.version FROM payment_intents p JOIN orders o ON o.id=p.order_id WHERE p.id=$1 FOR UPDATE OF p,o`, payment).Scan(&version); err != nil {
		return err
	}
	var previousOrder uuid.UUID
	var previousVersion int64
	err = tx.QueryRow(ctx, `SELECT order_id,observed_version FROM product_checkout_closures WHERE buyer_id=$1 AND idempotency_key=$2`, buyer, key).Scan(&previousOrder, &previousVersion)
	if err == nil {
		if previousOrder != order || previousVersion != input.ExpectedVersion {
			return ErrCheckoutCloseConflict
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	// Wait for a copy in progress before making the final eligibility decision.
	// Its creator must recheck the payment row before reserving remote dispatch.
	if _, err = tx.Exec(ctx, `SELECT order_id FROM product_delivery_snapshots WHERE order_id=$1 FOR UPDATE`, order); err != nil {
		return err
	}
	var eligible bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_checkout_locally_closable WHERE payment_id=$1)`, payment).Scan(&eligible); err != nil {
		return err
	}
	if !eligible || version != input.ExpectedVersion {
		return ErrCheckoutCloseConflict
	}
	if _, err = tx.Exec(ctx, `INSERT INTO product_checkout_closures(payment_id,order_id,buyer_id,idempotency_key,observed_version) VALUES($1,$2,$3,$4,$5)`, payment, order, buyer, key, version); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE orders SET status='cancelled',updated_at=now() WHERE id=$1`, order); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE payment_intents SET status='cancelled',version=version+1,updated_at=now() WHERE id=$1`, payment); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO order_events(order_id,actor_id,from_status,to_status,reason,sequence)
	 SELECT $1,$2,'payment_pending','cancelled','Buyer closed this order before checkout dispatch.',COALESCE(max(sequence),0)+1 FROM order_events WHERE order_id=$1`, order, buyer); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
	 VALUES($1,'checkout.closed_before_dispatch','checkout_pending','cancelled',jsonb_build_object('orderId',$2::text))`, payment, order); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
	 VALUES($1,'marketplace.checkout_closed','order',$2,$3,jsonb_build_object('paymentId',$4::text,'observedVersion',$5::bigint))`, buyer, order, requestID, payment, version); err != nil {
		return err
	}
	if err = datarights.EnqueueProductMediaCleanupTx(ctx, tx, order); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
