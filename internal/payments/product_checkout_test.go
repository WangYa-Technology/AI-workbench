package payments

import (
	"context"
	"errors"
	"fmt"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type guardedProductRuntime struct {
	productCheckoutRuntime
	calls     atomic.Int32
	failFirst bool
}

func (r *guardedProductRuntime) CreateCheckout(_ context.Context, input CheckoutRequest) (CheckoutSession, error) {
	call := r.calls.Add(1)
	if r.failFirst && call == 1 {
		return CheckoutSession{}, errors.New("checkout response lost")
	}
	id := strings.ReplaceAll(input.PaymentID.String(), "-", "")
	return CheckoutSession{ProviderID: "cs_" + id, CheckoutURL: "https://checkout.stripe.com/c/pay/" + id,
		Status: "open", PaymentStatus: "unpaid", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func newProductCheckoutFixture(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	buyer, seller, asset, product := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES
		($1,$2,$3,'Buyer','member'),($4,$5,$6,'Seller','creator')`,
		buyer, buyer.String()+"@test.local", "b_"+buyer.String()[:8], seller, seller.String()+"@test.local", "s_"+seller.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
		VALUES($1,$2,'image','Checkout source',$3,'image/jpeg','clean','upload','hcai-commercial-standard-v1','local_file',$1::uuid::text||'.jpg')`,
		asset, seller, "/api/v1/assets/"+asset.String()+"/content"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status,ai_disclosure,included_files,compatibility)
		VALUES($1,$2,$3,'Checkout product','Contract fixture','workflow',1900,'USD','hcai-commercial-standard-v1','active','AI-assisted','[]','HCAI')`, product, seller, asset); err != nil {
		t.Fatal(err)
	}
	if err := media.NewLocalStore(paymentTestRoot(t, pool)).Put(ctx, asset.String()+".jpg", []byte("The accepted purchased reference"), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	return buyer, seller, asset, product
}

func productOfferVersion(t *testing.T, pool *pgxpool.Pool, productID uuid.UUID) string {
	t.Helper()
	var version string
	if err := pool.QueryRow(context.Background(), `SELECT offer_version FROM product_offers WHERE product_id=$1`, productID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}

func TestProductCheckoutConcurrencyAndRecovery(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, loseResponse := range []bool{false, true} {
		t.Run(fmt.Sprintf("lost_response_%t", loseResponse), func(t *testing.T) {
			buyer, _, _, product := newProductCheckoutFixture(t, pool)
			runtime := &guardedProductRuntime{failFirst: loseResponse}
			service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
			version := productOfferVersion(t, pool, product)
			begin := func(key string) (Checkout, error) {
				checkout, _, err := service.BeginProductCheckout(ctx, buyer, product, key, "test", "https://example.test/success", "https://example.test/cancel", true, version)
				return checkout, err
			}
			if loseResponse {
				if _, err := begin("initial-response-lost"); err == nil {
					t.Fatal("expected uncertain provider failure")
				}
			}
			type outcome struct {
				checkout Checkout
				err      error
			}
			results := make(chan outcome, 12)
			start := make(chan struct{})
			for i := range 12 {
				go func() {
					<-start
					checkout, err := begin(fmt.Sprintf("concurrent-command-%02d", i))
					results <- outcome{checkout, err}
				}()
			}
			close(start)
			var first Checkout
			for range 12 {
				result := <-results
				if result.err != nil {
					t.Errorf("concurrent checkout: %v", result.err)
					continue
				}
				if first.PaymentID == uuid.Nil {
					first = result.checkout
				}
				if result.checkout.PaymentID != first.PaymentID || result.checkout.CheckoutURL != first.CheckoutURL {
					t.Errorf("separate checkout created: %#v vs %#v", result.checkout, first)
				}
			}
			wantCalls := int32(1)
			if loseResponse {
				wantCalls++
			}
			if runtime.calls.Load() != wantCalls {
				t.Errorf("provider called %d times, want %d", runtime.calls.Load(), wantCalls)
			}
			var orders, intents int
			if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM orders WHERE buyer_id=$1 AND product_id=$2),
				(SELECT count(*) FROM payment_intents WHERE payer_id=$1 AND resource_id=$2)`, buyer, product).Scan(&orders, &intents); err != nil {
				t.Fatal(err)
			}
			if orders != 1 || intents != 1 {
				t.Fatalf("duplicate durable state: orders=%d intents=%d", orders, intents)
			}
			var ready int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_delivery_snapshots WHERE order_id=$1 AND state='ready'`, first.OrderID).Scan(&ready); err != nil || ready != 1 {
				t.Fatalf("concurrent copy state: %d %v", ready, err)
			}
			entries, err := os.ReadDir(paymentTestRoot(t, pool))
			if err != nil {
				t.Fatal(err)
			}
			var copies int
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "delivery-"+first.OrderID.String()) {
					copies++
				}
			}
			if copies != 1 {
				t.Fatalf("concurrent preparation wrote %d files", copies)
			}
		})
	}
}

func TestProductCheckoutReplayRejectsIneligibleState(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, scenario := range []string{"expired", "cancelled", "refunded", "paid", "provider_changed", "mode_changed", "seller_suspended", "origin_rejected"} {
		t.Run(scenario, func(t *testing.T) {
			buyer, seller, asset, product := newProductCheckoutFixture(t, pool)
			runtime := &guardedProductRuntime{}
			service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
			version := productOfferVersion(t, pool, product)
			first, _, err := service.BeginProductCheckout(ctx, buyer, product, "original-command", "test", "https://example.test/success", "https://example.test/cancel", true, version)
			if err != nil {
				t.Fatal(err)
			}
			want := ErrCheckoutConflict
			switch scenario {
			case "expired":
				_, err = pool.Exec(ctx, `UPDATE payment_intents SET checkout_expires_at=now()-interval '1 minute' WHERE id=$1`, first.PaymentID)
			case "provider_changed":
				_, err = pool.Exec(ctx, `UPDATE payment_intents SET provider='waffo_pancake' WHERE id=$1`, first.PaymentID)
			case "mode_changed":
				_, err = pool.Exec(ctx, `UPDATE payment_intents SET live_mode=true WHERE id=$1`, first.PaymentID)
			case "seller_suspended":
				_, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, seller)
				want = ErrInvalidCheckout
			case "origin_rejected":
				origin := uuid.New()
				_, err = pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
					VALUES($1,$2,'image','Unsafe origin','/unsafe','image/jpeg','rejected','upload','hcai-commercial-standard-v1','local_file',$1::uuid::text||'.jpg')`, origin, seller)
				if err == nil {
					_, err = pool.Exec(ctx, `UPDATE assets SET origin_asset_id=$2 WHERE id=$1`, asset, origin)
				}
				want = ErrInvalidCheckout
			default:
				_, err = pool.Exec(ctx, `UPDATE payment_intents SET status=$2 WHERE id=$1`, first.PaymentID, scenario)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := service.BeginProductCheckout(ctx, buyer, product, "original-command", "replay", "https://example.test/success", "https://example.test/cancel", true, version); !errors.Is(err, want) {
				t.Fatalf("replay: got %v, want %v", err, want)
			}
			if runtime.calls.Load() != 1 {
				t.Fatal("replay created another provider session")
			}
		})
	}
}

func TestProductCheckoutRejectsSuspendedSellerBeforeCreatingOrder(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	buyer, seller, _, product := newProductCheckoutFixture(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, seller); err != nil {
		t.Fatal(err)
	}
	runtime := &guardedProductRuntime{}
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
	if _, _, err := service.BeginProductCheckout(ctx, buyer, product, "suspended-seller-command", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product)); !errors.Is(err, ErrInvalidCheckout) {
		t.Fatalf("got %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE buyer_id=$1`, buyer).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 || runtime.calls.Load() != 0 {
		t.Fatal("ineligible checkout had side effects")
	}
}

func TestProductCheckoutRetryKeysRemainBoundToOriginalIntent(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	buyer, _, _, product := newProductCheckoutFixture(t, pool)
	_, _, _, other := newProductCheckoutFixture(t, pool)
	runtime := &guardedProductRuntime{}
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
	begin := func(id uuid.UUID, key string) (Checkout, error) {
		checkout, _, err := service.BeginProductCheckout(ctx, buyer, id, key, "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, id))
		return checkout, err
	}
	original, err := begin(product, "original-command")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := begin(product, "alternate-command")
	if err != nil || retry.PaymentID != original.PaymentID {
		t.Fatalf("retry did not reuse intent: %v", err)
	}
	for _, key := range []string{"original-command", "alternate-command"} {
		if _, err := begin(other, key); !errors.Is(err, ErrCheckoutConflict) {
			t.Fatalf("key %s was reused for a different product: %v", key, err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET status='cancelled' WHERE id=$1`, original.PaymentID); err != nil {
		t.Fatal(err)
	}
	if _, err := begin(product, "alternate-command"); !errors.Is(err, ErrCheckoutConflict) {
		t.Fatalf("terminal intent was reopened: %v", err)
	}
	if runtime.calls.Load() != 1 {
		t.Fatal("retry opened a separate provider checkout")
	}
}

func TestProductCheckoutConcurrentKeyCannotBindTwoProducts(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	buyer, _, _, first := newProductCheckoutFixture(t, pool)
	_, _, _, second := newProductCheckoutFixture(t, pool)
	runtime := &guardedProductRuntime{}
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, product := range []uuid.UUID{first, second} {
		version := productOfferVersion(t, pool, product)
		go func() {
			<-start
			_, _, err := service.BeginProductCheckout(ctx, buyer, product, "shared-command", "test", "https://example.test/success", "https://example.test/cancel", true, version)
			results <- err
		}()
	}
	close(start)
	successes, conflicts := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrCheckoutConflict):
			conflicts++
		default:
			t.Errorf("unexpected checkout failure: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 || runtime.calls.Load() != 1 {
		t.Fatalf("successes=%d conflicts=%d provider calls=%d", successes, conflicts, runtime.calls.Load())
	}
}

var paymentMediaRoots sync.Map

func paymentTestRoot(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	if root, ok := paymentMediaRoots.Load(pool); ok {
		return root.(string)
	}
	root := t.TempDir()
	actual, loaded := paymentMediaRoots.LoadOrStore(pool, root)
	if !loaded {
		t.Cleanup(func() { paymentMediaRoots.Delete(pool) })
	}
	return actual.(string)
}
func newPaymentTestService(t *testing.T, pool *pgxpool.Pool, cfg ServiceConfig, runtimes *RuntimeCatalog) *Service {
	t.Helper()
	if cfg.MediaStores == nil {
		cfg.MediaStores = media.NewCatalog(media.NewLocalStore(paymentTestRoot(t, pool)))
	}
	return NewServiceWithRuntimes(pool, cfg, runtimes)
}
func replacePaymentFixtureBytes(t *testing.T, pool *pgxpool.Pool, asset uuid.UUID, body []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(paymentTestRoot(t, pool), asset.String()+".jpg"), body, 0600); err != nil {
		t.Fatal(err)
	}
}
