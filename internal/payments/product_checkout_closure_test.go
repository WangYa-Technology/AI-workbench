package payments

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pendingProductClosure struct {
	pool                           *pgxpool.Pool
	service                        *Service
	store                          *failedDeliveryStore
	runtime                        *guardedProductRuntime
	buyer, product, order, payment uuid.UUID
	version                        int64
}

func newPendingProductClosure(t *testing.T) pendingProductClosure {
	t.Helper()
	pool, cleanup := paymentTestPool(t)
	t.Cleanup(cleanup)
	buyer, _, _, product := newProductCheckoutFixture(t, pool)
	store := &failedDeliveryStore{LocalStore: media.NewLocalStore(paymentTestRoot(t, pool)), fail: true}
	runtime := &guardedProductRuntime{}
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, MediaStores: media.NewCatalog(store), APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(runtime))
	f := pendingProductClosure{pool: pool, buyer: buyer, product: product, store: store, runtime: runtime, service: service}
	_, _, err := service.BeginProductCheckout(context.Background(), buyer, product, "original-closure-purchase", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product))
	if !errors.Is(err, ErrCheckoutPreparation) || runtime.calls.Load() != 0 {
		t.Fatalf("expected uncharged preparation failure: %v", err)
	}
	if err = pool.QueryRow(context.Background(), `SELECT id,order_id,version FROM payment_intents WHERE payer_id=$1`, buyer).Scan(&f.payment, &f.order, &f.version); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f pendingProductClosure) close(ctx context.Context, key string) error {
	return f.service.CloseProductCheckout(ctx, f.buyer, f.order, key, "close-test", CloseProductCheckoutInput{ExpectedVersion: f.version, Confirmed: true})
}

func TestProductCheckoutClosureLifecycle(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(fmt.Sprintf("ready_%t", ready), func(t *testing.T) {
			f := newPendingProductClosure(t)
			ctx := context.Background()
			f.store.fail = false
			if ready {
				if err := productdelivery.Ensure(ctx, f.pool, f.service.config.MediaStores, f.order); err != nil {
					t.Fatal(err)
				}
			}
			order, err := marketplace.NewService(f.pool).GetOrder(ctx, f.buyer, f.order)
			if err != nil || !order.CanCloseCheckout || order.PaymentVersion == nil || *order.PaymentVersion != f.version {
				t.Fatalf("capability: %+v %v", order, err)
			}
			// Closing does not depend on the currently enabled gateway.
			f.service.config.Enabled = false
			for range 2 {
				if err = f.close(ctx, "confirmed-close-command"); err != nil {
					t.Fatal(err)
				}
			}
			order, err = marketplace.NewService(f.pool).GetOrder(ctx, f.buyer, f.order)
			if err != nil || order.Status != "cancelled" || order.CanCloseCheckout || !order.CheckoutClosedBeforePayment {
				t.Fatalf("closed projection: %+v %v", order, err)
			}
			var closures, events, audits, queued int
			err = f.pool.QueryRow(ctx, `SELECT
		 (SELECT count(*) FROM product_checkout_closures WHERE order_id=$1),
		 (SELECT count(*) FROM payment_intent_events WHERE payment_id=$2 AND event_type='checkout.closed_before_dispatch'),
		 (SELECT count(*) FROM audit_events WHERE resource_id=$1 AND action='marketplace.checkout_closed'),
		 (SELECT count(*) FROM jobs WHERE kind='product.delivery_cleanup' AND payload->>'orderId'=$1::text)`, f.order, f.payment).Scan(&closures, &events, &audits, &queued)
			if err != nil || closures != 1 || events != 1 || audits != 1 || queued != 1 {
				t.Fatalf("duplicate/missing evidence %d %d %d %d: %v", closures, events, audits, queued, err)
			}
			var job jobs.Job
			if err = f.pool.QueryRow(ctx, `SELECT id,payload FROM jobs WHERE kind='product.delivery_cleanup' AND payload->>'orderId'=$1`, f.order.String()).Scan(&job.ID, &job.Payload); err != nil {
				t.Fatal(err)
			}
			if err = productdelivery.CleanupHandler(f.pool, f.service.config.MediaStores)(ctx, job); err != nil {
				t.Fatal(err)
			}
			snapshot, err := productdelivery.Load(ctx, f.pool, f.order)
			if err != nil || snapshot.State != "removed" {
				t.Fatalf("cleanup: %+v %v", snapshot, err)
			}
			if _, err = f.store.Stat(ctx, snapshot.Key); !errors.Is(err, media.ErrNotFound) {
				t.Fatalf("copy retained: %v", err)
			}
			f.service.config.Enabled = true
			begin := func(key string) (Checkout, error) {
				c, _, err := f.service.BeginProductCheckout(ctx, f.buyer, f.product, key, "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, f.pool, f.product))
				return c, err
			}
			if _, err = begin("original-closure-purchase"); !errors.Is(err, ErrCheckoutClosed) {
				t.Fatalf("old key reopened: %v", err)
			}
			fresh, err := begin("explicit-new-purchase")
			if err != nil || fresh.OrderID == f.order || f.runtime.calls.Load() != 1 {
				t.Fatalf("new purchase: %+v %v", fresh, err)
			}
		})
	}
}

func TestProductCheckoutClosureRejectsInvalidAndUncertain(t *testing.T) {
	for _, scenario := range []string{"unconfirmed", "stale", "key", "short_unicode", "invalid_utf8", "null_byte", "long_unicode", "other_buyer", "inactive", "legacy", "dispatch"} {
		t.Run(scenario, func(t *testing.T) {
			f := newPendingProductClosure(t)
			ctx := context.Background()
			actor, key, input := f.buyer, "close-valid-command", CloseProductCheckoutInput{ExpectedVersion: f.version, Confirmed: true}
			want := ErrCheckoutCloseConflict
			switch scenario {
			case "unconfirmed":
				input.Confirmed = false
				want = ErrCheckoutCloseInvalid
			case "stale":
				input.ExpectedVersion++
			case "key":
				key = "short"
				want = ErrCheckoutCloseInvalid
			case "short_unicode":
				key = "短的键"
				want = ErrCheckoutCloseInvalid
			case "invalid_utf8":
				key = string([]byte{255}) + "closure-key"
				want = ErrCheckoutCloseInvalid
			case "null_byte":
				key = "closure" + string(rune(0)) + "key"
				want = ErrCheckoutCloseInvalid
			case "long_unicode":
				key = strings.Repeat("界", 129)
				want = ErrCheckoutCloseInvalid
			case "other_buyer":
				actor = uuid.New()
				want = ErrCheckoutOrderNotFound
			case "inactive":
				if _, err := f.pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, actor); err != nil {
					t.Fatal(err)
				}
				want = ErrCheckoutOrderNotFound
			case "legacy":
				// Only a fixture may remove immutability to model pre-protocol evidence.
				if _, err := f.pool.Exec(ctx, `ALTER TABLE product_checkout_requests DISABLE TRIGGER USER; UPDATE product_checkout_requests SET dispatch_protocol='legacy'; ALTER TABLE product_checkout_requests ENABLE TRIGGER USER`); err != nil {
					t.Fatal(err)
				}
			case "dispatch":
				f.store.fail = false
				f.runtime.failFirst = true
				_, _, err := f.service.createProviderCheckout(ctx, f.runtime, Checkout{PaymentID: f.payment, OrderID: f.order, ResourceID: f.product})
				if err == nil || f.runtime.calls.Load() != 1 {
					t.Fatalf("expected lost response: %v", err)
				}
				var fences int
				if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM product_checkout_dispatches WHERE payment_id=$1`, f.payment).Scan(&fences); err != nil || fences != 1 {
					t.Fatalf("dispatch rollback lost proof: %d %v", fences, err)
				}
				if err = f.pool.QueryRow(ctx, `SELECT version FROM payment_intents WHERE id=$1`, f.payment).Scan(&input.ExpectedVersion); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.service.CloseProductCheckout(ctx, actor, f.order, key, "test", input); !errors.Is(err, want) {
				t.Fatalf("%s: %v, want %v", scenario, err, want)
			}
			var state string
			if err := f.pool.QueryRow(ctx, `SELECT status FROM orders WHERE id=$1`, f.order).Scan(&state); err != nil || state != "payment_pending" {
				t.Fatalf("rejection changed order: %s %v", state, err)
			}
		})
	}
}

func TestProductCheckoutClosureAtomicity(t *testing.T) {
	for _, table := range []string{"audit_events", "jobs"} {
		t.Run(table, func(t *testing.T) {
			f := newPendingProductClosure(t)
			ctx := context.Background()
			if _, err := f.pool.Exec(ctx, `CREATE FUNCTION reject_closure_test() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'simulated failure'; END $$ LANGUAGE plpgsql; CREATE TRIGGER reject_closure_test BEFORE INSERT ON `+table+` FOR EACH ROW EXECUTE FUNCTION reject_closure_test()`); err != nil {
				t.Fatal(err)
			}
			if err := f.close(ctx, "atomic-close-command"); err == nil {
				t.Fatal("expected transactional failure")
			}
			var unchanged bool
			if err := f.pool.QueryRow(ctx, `SELECT o.status='payment_pending' AND p.status='checkout_pending' AND p.version=$2 AND NOT EXISTS(SELECT 1 FROM product_checkout_closures WHERE order_id=o.id) FROM orders o JOIN payment_intents p ON p.order_id=o.id WHERE o.id=$1`, f.order, f.version).Scan(&unchanged); err != nil || !unchanged {
				t.Fatalf("partial closure: %v", err)
			}
			if _, err := f.pool.Exec(ctx, `DROP TRIGGER reject_closure_test ON `+table); err != nil {
				t.Fatal(err)
			}
			if err := f.close(ctx, "atomic-close-command"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProductCheckoutClosureWinsBeforeDispatch(t *testing.T) {
	f := newPendingProductClosure(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f.store.fail = false
	gate, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(ctx)
	if _, err = gate.Exec(ctx, `SELECT order_id FROM product_delivery_snapshots WHERE order_id=$1 FOR UPDATE`, f.order); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- f.close(ctx, "racing-close-command") }()
	waitForProductBlockingTx(t, ctx, f.pool, int32(gate.Conn().PgConn().PID()))
	dispatched := make(chan error, 1)
	go func() {
		_, _, err := f.service.createProviderCheckout(ctx, f.runtime, Checkout{PaymentID: f.payment, OrderID: f.order, ResourceID: f.product})
		dispatched <- err
	}()
	if err = gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-closed; err != nil {
		t.Fatal(err)
	}
	if err = <-dispatched; err == nil || f.runtime.calls.Load() != 0 {
		t.Fatalf("dispatched after close: calls=%d %v", f.runtime.calls.Load(), err)
	}
}

func TestProductCheckoutClosureMigrationEvidence(t *testing.T) {
	f := newPendingProductClosure(t)
	ctx := context.Background()
	down, err := os.ReadFile("../platform/database/migrations/0091_product_checkout_local_closure.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, string(down)); err == nil {
		t.Fatal("discarded guarded request evidence")
	}
	if err = f.close(ctx, "immutable-close-command"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`DELETE FROM product_checkout_closures`, `UPDATE product_checkout_closures SET observed_version=999`} {
		if _, err = f.pool.Exec(ctx, query); err == nil {
			t.Fatal("closure evidence changed")
		}
	}
	if err = f.close(ctx, "immutable-close-command"); err != nil {
		t.Fatal(err)
	}
	if err = f.close(ctx, "different-close-command"); !errors.Is(err, ErrCheckoutCloseConflict) {
		t.Fatalf("second command accepted: %v", err)
	}
}

type blockedProductDispatch struct {
	productCheckoutRuntime
	started chan struct{}
	release chan struct{}
}

func (r *blockedProductDispatch) CreateCheckout(ctx context.Context, _ CheckoutRequest) (CheckoutSession, error) {
	close(r.started)
	select {
	case <-r.release:
		return CheckoutSession{}, errors.New("remote response lost")
	case <-ctx.Done():
		return CheckoutSession{}, ctx.Err()
	}
}

func TestProductCheckoutClosureRejectsInFlightDispatch(t *testing.T) {
	f := newPendingProductClosure(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f.store.fail = false
	runtime := &blockedProductDispatch{started: make(chan struct{}), release: make(chan struct{})}
	dispatched := make(chan error, 1)
	go func() {
		_, _, err := f.service.createProviderCheckout(ctx, runtime, Checkout{PaymentID: f.payment, OrderID: f.order, ResourceID: f.product})
		dispatched <- err
	}()
	select {
	case <-runtime.started:
	case <-ctx.Done():
		t.Fatal("dispatch did not start")
	}
	// Read from a separate connection while the outbound transaction is live.
	var fences int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM product_checkout_dispatches WHERE payment_id=$1`, f.payment).Scan(&fences); err != nil || fences != 1 {
		t.Fatalf("missing committed fence: %d %v", fences, err)
	}
	closed := make(chan error, 1)
	go func() { closed <- f.close(ctx, "inflight-close-command") }()
	close(runtime.release)
	if err := <-dispatched; err == nil {
		t.Fatal("expected uncertain dispatch")
	}
	if err := <-closed; !errors.Is(err, ErrCheckoutCloseConflict) {
		t.Fatalf("closed uncertain dispatch: %v", err)
	}
}

func TestProductCheckoutClosureLatePaymentCompensates(t *testing.T) {
	f := newPendingProductClosure(t)
	ctx := context.Background()
	if err := f.close(ctx, "before-late-payment"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	f.service.verifier.now = func() time.Time { return now }
	receipt := receivePaymentWorkflowEvent(t, f.service, productPaidEvent(f.payment, f.product, now.Unix(), 1900), now)
	if err := f.service.HandlePaymentEventJob(ctx, jobs.Job{Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID))}); err != nil {
		t.Fatal(err)
	}
	var state, reason string
	var rights int
	if err := f.pool.QueryRow(ctx, `SELECT status,compensation_reason,(SELECT count(*) FROM entitlements WHERE order_id=$2) FROM payment_intents WHERE id=$1`, f.payment, f.order).Scan(&state, &reason, &rights); err != nil || state != "refund_pending" || reason != "checkout_closed" || rights != 0 {
		t.Fatalf("late payment mishandled: %s %s %d %v", state, reason, rights, err)
	}
}

func TestProductCheckoutClosureConcurrentReplayAndKeyIsolation(t *testing.T) {
	f := newPendingProductClosure(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	results := make(chan error, 8)
	for range 8 {
		go func() { results <- f.close(ctx, "same-close-command") }()
	}
	for range 8 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM order_events WHERE order_id=$1 AND to_status='cancelled'`, f.order).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate closure: %d %v", count, err)
	}
	_, _, _, second := newProductCheckoutFixture(t, f.pool)
	if _, _, err := f.service.BeginProductCheckout(ctx, f.buyer, second, "second-product-command", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, f.pool, second)); !errors.Is(err, ErrCheckoutPreparation) {
		t.Fatalf("second fixture: %v", err)
	}
	var secondOrder uuid.UUID
	var version int64
	if err := f.pool.QueryRow(ctx, `SELECT order_id,version FROM payment_intents WHERE payer_id=$1 AND resource_id=$2`, f.buyer, second).Scan(&secondOrder, &version); err != nil {
		t.Fatal(err)
	}
	if err := f.service.CloseProductCheckout(ctx, f.buyer, secondOrder, "same-close-command", "test", CloseProductCheckoutInput{ExpectedVersion: version, Confirmed: true}); !errors.Is(err, ErrCheckoutCloseConflict) {
		t.Fatalf("key rebound to another order: %v", err)
	}
	current, err := marketplace.NewService(f.pool).GetOrder(ctx, f.buyer, secondOrder)
	if err != nil || current.Status != "payment_pending" || !current.CanCloseCheckout {
		t.Fatalf("reused key changed second order: %+v %v", current, err)
	}
}

func TestProductCheckoutClosureRejectsReceivedProviderEvidence(t *testing.T) {
	f := newPendingProductClosure(t)
	now := time.Now().UTC().Truncate(time.Second)
	f.service.verifier.now = func() time.Time { return now }
	receivePaymentWorkflowEvent(t, f.service, productPaidEvent(f.payment, f.product, now.Unix(), 1900), now)
	if err := f.close(context.Background(), "received-evidence-close"); !errors.Is(err, ErrCheckoutCloseConflict) {
		t.Fatalf("closed while verified event awaited processing: %v", err)
	}
}

func TestProductCheckoutClosureUnicodeKey(t *testing.T) {
	f := newPendingProductClosure(t)
	for range 2 {
		if err := f.close(context.Background(), strings.Repeat("界", 128)); err != nil {
			t.Fatal(err)
		}
	}
}
