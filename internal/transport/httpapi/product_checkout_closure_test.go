package httpapi_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

type closureFixtureRuntime struct{ payments.ProviderRuntime }

func (*closureFixtureRuntime) Provider() string { return "stripe" }
func (*closureFixtureRuntime) ProductCheckoutIdentity(context.Context) (payments.ProductCheckoutIdentity, error) {
	return payments.ProductCheckoutIdentity{Provider: "stripe", MerchantID: "acct_closure", Endpoint: "https://api.stripe.com/v1", APIVersion: "2026-02-25.clover", RequestVersion: "stripe-product-checkout-v1"}, nil
}
func (*closureFixtureRuntime) CreateCheckout(context.Context, payments.CheckoutRequest) (payments.CheckoutSession, error) {
	return payments.CheckoutSession{}, errors.New("unexpected checkout dispatch")
}

type closureFixtureStore struct{ *media.LocalStore }

func (*closureFixtureStore) PutStream(context.Context, string, io.ReadSeeker, int64, string, string) error {
	return errors.New("injected copy outage")
}

func TestProductCheckoutClosureHTTP(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := context.Background()
	root := t.TempDir()
	// HTTP payment creation is disabled: safe local closure must remain available.
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: root, WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	guest, buyerClient, sellerClient := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	buyer := registerGovernanceUser(t, buyerClient, server.URL, "closure_buyer")
	seller := registerGovernanceUser(t, sellerClient, server.URL, "closure_seller")
	asset, product := uuid.New(), uuid.New()
	if err := os.WriteFile(filepath.Join(root, asset.String()+".txt"), []byte("private purchase"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
	 VALUES($1,$2,'document','Closure source',$3,'text/plain','clean','upload','hcai-commercial-standard-v1','local_file',$1::uuid::text||'.txt')`, asset, seller.ID, "/api/v1/assets/"+asset.String()+"/content"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status,ai_disclosure,included_files,compatibility)
	 VALUES($1,$2,$3,'Closure product','Pending delivery','workflow',1900,'USD','hcai-commercial-standard-v1','active','AI assisted','[]','HCAI')`, product, seller.ID, asset); err != nil {
		t.Fatal(err)
	}
	offer, err := marketplace.NewService(pool).GetProduct(ctx, buyer.ID, product)
	if err != nil {
		t.Fatal(err)
	}
	service := payments.NewServiceWithRuntimes(pool, payments.ServiceConfig{Enabled: true, MediaStores: media.NewCatalog(&closureFixtureStore{media.NewLocalStore(root)})}, payments.NewRuntimeCatalog(&closureFixtureRuntime{}))
	if _, _, err = service.BeginProductCheckout(ctx, buyer.ID, product, "http-closure-purchase", "test", "https://example.test/success", "https://example.test/cancel", true, offer.OfferVersion); !errors.Is(err, payments.ErrCheckoutPreparation) {
		t.Fatalf("preparation fixture: %v", err)
	}
	var order uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT order_id FROM payment_intents WHERE payer_id=$1`, buyer.ID).Scan(&order); err != nil {
		t.Fatal(err)
	}
	endpoint := server.URL + "/api/v1/orders/" + order.String()
	var current marketplace.Order
	if response := requestJSON(t, buyerClient, http.MethodGet, endpoint, nil, &current); response.StatusCode != 200 || !current.CanCloseCheckout || current.PaymentVersion == nil {
		t.Fatalf("missing closure projection: %d %+v", response.StatusCode, current)
	}
	input := map[string]any{"expectedVersion": *current.PaymentVersion, "confirmed": true}
	for _, c := range []struct {
		client *http.Client
		key    string
		body   any
		status int
	}{
		{guest, "guest-close-command", input, 401},
		{sellerClient, "other-close-command", input, 404},
		{buyerClient, "missing-confirm-command", map[string]any{"expectedVersion": *current.PaymentVersion}, 422},
		{buyerClient, "stale-close-command", map[string]any{"expectedVersion": *current.PaymentVersion + 1, "confirmed": true}, 409},
		{buyerClient, "x", input, 422},
	} {
		if response := requestPaymentJSON(t, c.client, http.MethodPost, endpoint+"/close-checkout", c.key, c.body, nil); response.StatusCode != c.status {
			t.Fatalf("boundary status=%d want=%d", response.StatusCode, c.status)
		}
	}
	for range 2 {
		var result marketplace.Order
		response := requestPaymentJSON(t, buyerClient, http.MethodPost, endpoint+"/close-checkout", "confirmed-http-close", input, &result)
		if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "private, no-store" || result.Status != "cancelled" || result.CanCloseCheckout || !result.CheckoutClosedBeforePayment || result.PaymentVersion == nil || *result.PaymentVersion != *current.PaymentVersion+1 {
			t.Fatalf("closure response: %d %+v", response.StatusCode, result)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, seller.ID); err != nil {
		t.Fatal(err)
	}
	var failedJob uuid.UUID
	if err = pool.QueryRow(ctx, `UPDATE jobs SET status='failed',attempts=20,last_error='private/store/path',last_error_code='handler_failed'
	 WHERE kind='product.delivery_cleanup' AND payload->>'orderId'=$1 RETURNING id`, order.String()).Scan(&failedJob); err != nil {
		t.Fatal(err)
	}
	queue := server.URL + "/api/v1/admin/data-rights/media-cleanups"
	var page datarights.MediaCleanupPage
	if response := requestJSON(t, buyerClient, http.MethodGet, queue+"?kind=product", nil, nil); response.StatusCode != 403 {
		t.Fatal("buyer accessed recovery queue", response.StatusCode)
	}
	if response := requestJSON(t, sellerClient, http.MethodGet, queue+"?kind=account", nil, &page); response.StatusCode != 200 || len(page.Items) != 0 {
		t.Fatalf("product job leaked into account filter: %+v", page)
	}
	if response := requestJSON(t, sellerClient, http.MethodGet, queue+"?kind=product", nil, &page); response.StatusCode != 200 || len(page.Items) != 1 || !page.Items[0].CanRetry || page.Items[0].Kind != "product" || page.Items[0].OrderID == nil || *page.Items[0].OrderID != order {
		t.Fatalf("product recovery capability: %+v", page)
	}
	var replacement datarights.MediaCleanup
	command := map[string]any{"expectedAttempts": 20, "confirmed": true, "reason": "Storage access was repaired and checked."}
	response := requestJSON(t, sellerClient, http.MethodPost, queue+"/"+failedJob.String()+"/retry", command, &replacement)
	if response.StatusCode != 201 || response.Header.Get("Cache-Control") != "private, no-store" || replacement.Kind != "product" || replacement.OrderID == nil || *replacement.OrderID != order || replacement.RetryOf == nil || *replacement.RetryOf != failedJob {
		t.Fatalf("wrong recovery response: %d %+v", response.StatusCode, replacement)
	}
	if response = requestJSON(t, sellerClient, http.MethodPost, queue+"/"+failedJob.String()+"/retry", command, nil); response.StatusCode != 409 {
		t.Fatal("duplicate product recovery accepted", response.StatusCode)
	}

}
