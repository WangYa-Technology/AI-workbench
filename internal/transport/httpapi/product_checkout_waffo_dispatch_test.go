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
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

type uncertainWaffoCheckoutRuntime struct {
	payments.ProviderRuntime
	calls atomic.Int32
}

func (*uncertainWaffoCheckoutRuntime) Provider() string { return "waffo_pancake" }
func (*uncertainWaffoCheckoutRuntime) ProductCheckoutIdentity(context.Context) (payments.ProductCheckoutIdentity, error) {
	return payments.ProductCheckoutIdentity{Provider: "waffo_pancake", MerchantID: "MER_fixture", StoreID: "STO_fixture", Endpoint: "http://127.0.0.1:8091", APIVersion: "pancake-ts-0.19.1", RequestVersion: "waffo-product-checkout-v1"}, nil
}
func (r *uncertainWaffoCheckoutRuntime) CreateCheckout(context.Context, payments.CheckoutRequest) (payments.CheckoutSession, error) {
	r.calls.Add(1)
	return payments.CheckoutSession{}, errors.New("fixture response lost")
}

func TestWaffoCheckoutReconciliationHTTPAndOperations(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := t.Context()
	root := t.TempDir()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: root, WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	guest, buyerClient, sellerClient := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	buyer := registerGovernanceUser(t, buyerClient, server.URL, "waffo_buyer")
	seller := registerGovernanceUser(t, sellerClient, server.URL, "waffo_seller")
	asset, product := uuid.New(), uuid.New()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, asset.String()+".txt"), []byte("Original licensed content"), 0600); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 VALUES($1,$2,'document','Original','/private.txt','text/plain','clean','upload','hcai-commercial-standard-v1','local_file',$3)`, asset, seller.ID, asset.String()+".txt")
	exec(`INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status)
 VALUES($1,$2,$3,'Uncertain product','Original content','prompt',1900,'USD','hcai-commercial-standard-v1','active')`, product, seller.ID, asset)
	market := marketplace.NewService(pool)
	offer, err := market.GetProduct(ctx, buyer.ID, product)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &uncertainWaffoCheckoutRuntime{}
	service := payments.NewServiceWithRuntimes(pool, payments.ServiceConfig{Enabled: true, Provider: "waffo_pancake", WaffoMerchantID: "MER_fixture", WaffoStoreID: "STO_fixture", WaffoProductIDOnetime: "PROD_fixture", WaffoEnvironment: "test", MediaStores: media.NewCatalog(media.NewLocalStore(root))}, payments.NewRuntimeCatalog(runtime))
	if _, _, err = service.BeginProductCheckout(ctx, buyer.ID, product, "waffo-http-checkout", "test", "https://example.test/success", "https://example.test/cancel", true, offer.OfferVersion); err == nil {
		t.Fatal("expected uncertain result")
	}
	var order, payment uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT order_id,id FROM payment_intents WHERE payer_id=$1`, buyer.ID).Scan(&order, &payment); err != nil {
		t.Fatal(err)
	}
	endpoint := server.URL + "/api/v1/orders/" + order.String()
	var current marketplace.Order
	if response := requestJSON(t, buyerClient, http.MethodGet, endpoint, nil, &current); response.StatusCode != 200 || !current.CheckoutReconciliationRequired || current.CanCloseCheckout {
		t.Fatalf("buyer HTTP projection: %d %+v", response.StatusCode, current)
	}
	var list marketplace.OrderPage
	if response := requestJSON(t, buyerClient, http.MethodGet, server.URL+"/api/v1/orders", nil, &list); response.StatusCode != 200 || len(list.Items) != 1 || !list.Items[0].CheckoutReconciliationRequired {
		t.Fatalf("list HTTP projection: %d %+v", response.StatusCode, list)
	}
	for _, other := range []struct {
		client *http.Client
		status int
	}{{guest, 401}, {sellerClient, 404}} {
		var body map[string]any
		if response := requestJSON(t, other.client, http.MethodGet, endpoint, nil, &body); response.StatusCode != other.status {
			t.Fatalf("unauthorized order: %d", response.StatusCode)
		}
	}
	var sale marketplace.SellerSaleDetail
	if response := requestJSON(t, sellerClient, http.MethodGet, server.URL+"/api/v1/seller/sales/"+order.String(), nil, &sale); response.StatusCode != 200 || !sale.NeedsReview {
		t.Fatalf("seller HTTP projection: %d %+v", response.StatusCode, sale)
	}
	operations := admin.NewService(pool, true)
	page, err := operations.ListPaymentOperations(ctx, admin.PaymentOperationListInput{Query: payment.String(), Attention: "needs_attention"})
	if err != nil || len(page.Items) != 1 || page.Items[0].AttentionCode != "checkout_reconciliation_required" || page.Items[0].CanCheckCheckout || page.Items[0].CanLocateCheckout {
		t.Fatalf("admin projection: %+v %v", page, err)
	}
	for _, action := range []string{"check_checkout", "locate_checkout", "retry_refund"} {
		if _, err = operations.RecoverPayment(ctx, seller.ID, payment, admin.PaymentRecovery{Action: action, ExpectedVersion: int(*current.PaymentVersion)}, "unsafe-recovery"); err == nil {
			t.Fatalf("unsupported Waffo recovery accepted: %s", action)
		}
	}
	if runtime.calls.Load() != 1 {
		t.Fatalf("unexpected dispatches: %d", runtime.calls.Load())
	}
}
