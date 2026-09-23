package payments

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
)

func TestWaffoCheckoutDispatchUncertainty(t *testing.T) {
	for _, mode := range []string{"response_lost", "concurrent_first", "crash_before_send", "legacy", "missing_request", "remote_without_dispatch"} {
		t.Run(mode, func(t *testing.T) {
			f := newWaffoRefundFixture(t, "checkout_lost")
			ctx := t.Context()
			exec := func(sql string, args ...any) {
				t.Helper()
				if _, err := f.pool.Exec(ctx, sql, args...); err != nil {
					t.Fatal(err)
				}
			}
			wantCalls := int32(1)
			if mode != "response_lost" {
				// Reconstruct pre-dispatch/legacy states only in this test schema.
				exec(`TRUNCATE product_checkout_dispatches`)
				f.checkoutCalls.Store(0)
				wantCalls = 0
				switch mode {
				case "concurrent_first":
					wantCalls = 1
				case "crash_before_send":
					tx, err := f.pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback(ctx)
					if _, err = tx.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, f.checkout.PaymentID); err != nil {
						t.Fatal(err)
					}
					if _, err = reserveWaffoCheckoutTx(ctx, tx, f.checkout.PaymentID); err != nil {
						t.Fatal(err)
					}
					if err = tx.Commit(ctx); err != nil {
						t.Fatal(err)
					}
				case "legacy", "missing_request":
					exec(`CREATE TABLE checkout_fixture AS SELECT * FROM product_checkout_requests;
 TRUNCATE product_checkout_dispatches,product_checkout_requests`)
					if mode == "legacy" {
						exec(`INSERT INTO product_checkout_requests(payment_id,identity,request,created_at,dispatch_protocol)
 SELECT payment_id,identity,request,created_at,'legacy' FROM checkout_fixture`)
					}
				case "remote_without_dispatch":
					exec(`UPDATE payment_intents SET provider_checkout_id='CHK_unknown' WHERE id=$1`, f.checkout.PaymentID)
				}
			}
			// A new service instance has no in-memory knowledge of the first call.
			restarted := NewServiceWithRuntimes(f.pool, f.service.config, f.service.runtimes)
			version := productOfferVersion(t, f.pool, f.product)
			out := make(chan error, 6)
			for i := range 6 {
				go func() {
					key := "waffo-dispatch-checkout"
					if i%2 == 1 {
						key = fmt.Sprintf("new-checkout-key-%d", i)
					}
					_, _, err := restarted.BeginProductCheckout(ctx, f.buyer, f.product, key, "restart", "https://example.test/success", "https://example.test/cancel", true, version)
					out <- err
				}()
			}
			uncertainCalls := 0
			for range 6 {
				err := <-out
				if mode == "concurrent_first" && err != nil && !errors.Is(err, ErrCheckoutReconciliation) {
					uncertainCalls++
					continue
				}
				if !errors.Is(err, ErrCheckoutReconciliation) {
					t.Fatalf("unsafe replay: %v", err)
				}
			}
			if mode == "concurrent_first" && uncertainCalls != 1 {
				t.Fatalf("first dispatch failures=%d", uncertainCalls)
			}
			if f.checkoutCalls.Load() != wantCalls {
				t.Fatalf("remote checkout count=%d want=%d", f.checkoutCalls.Load(), wantCalls)
			}
			orders := marketplace.NewService(f.pool)
			order, err := orders.GetOrder(ctx, f.buyer, f.checkout.OrderID)
			if err != nil || !order.CheckoutReconciliationRequired || order.CanCloseCheckout {
				t.Fatalf("buyer projection: %+v %v", order, err)
			}
			if err = restarted.CloseProductCheckout(ctx, f.buyer, order.ID, "close-uncertain-order", "test", CloseProductCheckoutInput{ExpectedVersion: *order.PaymentVersion, Confirmed: true}); !errors.Is(err, ErrCheckoutCloseConflict) {
				t.Fatalf("uncertain checkout closed: %v", err)
			}
			var seller uuid.UUID
			if err = f.pool.QueryRow(ctx, `SELECT seller_id FROM products WHERE id=$1`, f.product).Scan(&seller); err != nil {
				t.Fatal(err)
			}
			sale, err := orders.GetSale(ctx, seller, order.ID)
			if err != nil || !sale.NeedsReview {
				t.Fatalf("seller projection: %+v %v", sale, err)
			}
			var retained bool
			if err = f.pool.QueryRow(ctx, `SELECT needed FROM product_delivery_cleanup_policy WHERE order_id=$1`, order.ID).Scan(&retained); err != nil || !retained {
				t.Fatalf("lost pending delivery retention: %t %v", retained, err)
			}
			down, err := os.ReadFile("../platform/database/migrations/0120_waffo_checkout_dispatch.down.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot remove unresolved Waffo checkout") {
				t.Fatalf("unsafe rollback: %v", err)
			}
			if mode == "response_lost" || mode == "crash_before_send" {
				f.event(t, "order.completed", uuid.Nil)
				f.event(t, "order.completed", uuid.Nil)
				order, err = orders.GetOrder(ctx, f.buyer, order.ID)
				if err != nil || order.Status != "fulfilled" || order.CheckoutReconciliationRequired || order.AssetID == nil {
					t.Fatalf("late payment failed: %+v %v", order, err)
				}
				var rights int
				if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM entitlements WHERE order_id=$1 AND status='active'`, order.ID).Scan(&rights); err != nil || rights != 1 {
					t.Fatalf("duplicate fulfillment: %d %v", rights, err)
				}
				if f.checkoutCalls.Load() != wantCalls {
					t.Fatal("late event created another checkout")
				}
			}
		})
	}
}

func TestWaffoCheckoutSavedURLAndPermit(t *testing.T) {
	f := newWaffoRefundFixture(t, "await_payment")
	ctx := t.Context()
	for _, key := range []string{"waffo-dispatch-checkout", "another-checkout-key"} {
		checkout, _, err := f.service.BeginProductCheckout(ctx, f.buyer, f.product, key, "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, f.pool, f.product))
		if err != nil || checkout.CheckoutURL != f.checkout.CheckoutURL || !checkout.AlreadyCreated {
			t.Fatalf("saved URL: %+v %v", checkout, err)
		}
	}
	if f.checkoutCalls.Load() != 1 {
		t.Fatal("saved URL dispatched checkout again")
	}
	order, err := marketplace.NewService(f.pool).GetOrder(ctx, f.buyer, f.checkout.OrderID)
	if err != nil || order.CheckoutReconciliationRequired {
		t.Fatalf("saved URL incorrectly held: %+v %v", order, err)
	}
	// An invocation permit cannot authorize an already completed checkout.
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = validateWaffoCheckoutPermitTx(ctx, tx, f.checkout.PaymentID, *order.PaymentVersion); !errors.Is(err, ErrCheckoutReconciliation) {
		t.Fatalf("reused permit: %v", err)
	}
}

func TestWaffoCheckoutPermitRechecksEvidence(t *testing.T) {
	for _, changed := range []string{"none", "version", "lookup_hold"} {
		t.Run(changed, func(t *testing.T) {
			f := newWaffoRefundFixture(t, "checkout_lost")
			ctx := t.Context()
			if _, err := f.pool.Exec(ctx, `TRUNCATE product_checkout_dispatches`); err != nil {
				t.Fatal(err)
			}
			tx, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err = tx.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, f.checkout.PaymentID); err != nil {
				t.Fatal(err)
			}
			version, err := reserveWaffoCheckoutTx(ctx, tx, f.checkout.PaymentID)
			if err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if changed == "version" {
				_, err = f.pool.Exec(ctx, `UPDATE payment_intents SET version=version+1 WHERE id=$1`, f.checkout.PaymentID)
			} else if changed == "lookup_hold" {
				_, err = f.pool.Exec(ctx, `WITH j AS (INSERT INTO jobs(kind,payload) VALUES('payment.locate_product_checkout','{}') RETURNING id)
 INSERT INTO product_checkout_lookups(job_id,payment_id,requested_by,outcome,searched_after,searched_before,result)
 SELECT id,$1,$2,'ambiguous',now()-interval '1 hour',now(),'{"outcome":"ambiguous"}' FROM j`, f.checkout.PaymentID, f.buyer)
			}
			if err != nil {
				t.Fatal(err)
			}
			next, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Rollback(ctx)
			if _, err = next.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, f.checkout.PaymentID); err != nil {
				t.Fatal(err)
			}
			err = validateWaffoCheckoutPermitTx(ctx, next, f.checkout.PaymentID, version)
			if (changed == "none" && err != nil) || (changed != "none" && !errors.Is(err, ErrCheckoutReconciliation)) {
				t.Fatalf("permit %s: %v", changed, err)
			}
		})
	}
}
