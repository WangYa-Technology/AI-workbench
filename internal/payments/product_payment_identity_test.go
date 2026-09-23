package payments

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

// Route the original logical provider endpoint to an isolated HTTP fixture.
// Changing credentials in a test must not also change its original merchant
// endpoint, which is now part of the immutable transaction identity.
type merchantFixtureTransport struct {
	target    *url.URL
	transport http.RoundTripper
}

func (t merchantFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	copy.URL.Scheme, copy.URL.Host = t.target.Scheme, t.target.Host
	copy.URL.Path = strings.TrimPrefix(copy.URL.Path, "/v1")
	copy.Host = t.target.Host
	return t.transport.RoundTrip(copy)
}
func originalMerchantHTTPRuntime(t *testing.T, server *httptest.Server, key string) *StripeRuntime {
	t.Helper()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := (&productCheckoutRuntime{}).ProductCheckoutIdentity(context.Background())
	return NewStripeRuntime(StripeRuntimeConfig{BaseURL: identity.Endpoint, SecretKey: key, APIVersion: identity.APIVersion,
		HTTPClient: &http.Client{Transport: merchantFixtureTransport{target: target, transport: server.Client().Transport}}})
}

type merchantBoundRuntime struct {
	ProviderRuntime
	identity      ProductCheckoutIdentity
	refundCalls   int
	checkoutReads int
	lastRefund    RefundRequest
}

func (r *merchantBoundRuntime) ProductCheckoutIdentity(context.Context) (ProductCheckoutIdentity, error) {
	return r.identity, nil
}
func (r *merchantBoundRuntime) CreateRefund(ctx context.Context, request RefundRequest) (Refund, error) {
	r.refundCalls++
	r.lastRefund = request
	return r.ProviderRuntime.CreateRefund(ctx, request)
}
func (r *merchantBoundRuntime) ReadProductCheckout(ctx context.Context, request CheckoutReadRequest) (CheckoutObservation, error) {
	r.checkoutReads++
	return r.ProviderRuntime.(CheckoutReader).ReadProductCheckout(ctx, request)
}
func (r *merchantBoundRuntime) ReadProductRefunds(ctx context.Context, request RefundReadRequest) ([]RefundObservation, error) {
	return r.ProviderRuntime.(RefundReader).ReadProductRefunds(ctx, request)
}

func TestProductRefundRequiresOriginalMerchant(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	base := &durableProductRefundRuntime{}
	service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, base)
	// Historical age must stop new checkout creation, not prevent an ordinary
	// refund within its contract window. Build aged evidence in this test schema.
	if _, err := pool.Exec(ctx, `CREATE TABLE original_request_fixture AS SELECT * FROM product_checkout_requests;
		TRUNCATE product_checkout_dispatches, product_checkout_requests;
		INSERT INTO product_checkout_requests SELECT payment_id,identity,request,now()-interval '2 days' FROM original_request_fixture;
		DROP TABLE original_request_fixture`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE orders SET created_at=now()-interval '2 days' WHERE id=$1`, checkout.OrderID); err != nil {
		t.Fatal(err)
	}
	identity, _ := base.ProductCheckoutIdentity(ctx)
	runtime := &merchantBoundRuntime{ProviderRuntime: base, identity: identity}
	service.runtimes = NewRuntimeCatalog(runtime)
	if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "bound-refund-command", "test", "The supplied resource does not meet its stated contract."); err != nil {
		t.Fatal(err)
	}
	job := currentProductRefundJob(t, pool, checkout.PaymentID)
	for _, field := range []string{"merchant", "store", "environment", "endpoint"} {
		runtime.identity = identity
		switch field {
		case "merchant":
			runtime.identity.MerchantID = "acct_changed123"
		case "store":
			runtime.identity.StoreID = "changed-store"
		case "environment":
			runtime.identity.LiveMode = true
		case "endpoint":
			runtime.identity.Endpoint = "https://different.example.test/v1"
		}
		if err := service.HandleProductRefundJob(ctx, job); !errors.Is(err, ErrCheckoutReconciliation) || runtime.refundCalls != 0 {
			t.Fatalf("%s change dispatched refund: %v", field, err)
		}
	}
	var originalEmail string
	if err := pool.QueryRow(ctx, `SELECT email FROM users WHERE id=$1`, buyer).Scan(&originalEmail); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET email=$2 WHERE id=$1`, buyer, buyer.String()+"@changed.test"); err != nil {
		t.Fatal(err)
	}
	runtime.identity = identity
	// A checkout serializer upgrade is not a new refund or a different merchant.
	runtime.identity.RequestVersion = "checkout-next-version"
	if err := service.HandleProductRefundJob(ctx, job); err != nil || runtime.refundCalls != 1 {
		t.Fatalf("original merchant refund: %v", err)
	}
	if runtime.lastRefund.BuyerEmail != originalEmail || runtime.lastRefund.BuyerIdentity != buyer.String() || runtime.lastRefund.PaymentIdentity == nil || runtime.lastRefund.PaymentIdentity.MerchantID != identity.MerchantID {
		t.Fatal("refund used current buyer/routing data")
	}
	if err := service.HandleProductRefundJob(ctx, job); err != nil || runtime.refundCalls != 1 {
		t.Fatalf("recorded refund was dispatched again: %v", err)
	}
}

func TestProductQueriesRequireOriginalMerchant(t *testing.T) {
	t.Run("checkout", func(t *testing.T) {
		pool, service, base, checkout, _, job := checkoutCheckFixture(t)
		ctx := context.Background()
		identity, _ := base.ProductCheckoutIdentity(ctx)
		runtime := &merchantBoundRuntime{ProviderRuntime: base, identity: identity}
		runtime.identity.MerchantID = "acct_wrong123"
		service.runtimes = NewRuntimeCatalog(runtime)
		if err := service.HandleProductCheckoutCheckJob(ctx, job); !errors.Is(err, ErrCheckoutReconciliation) {
			t.Fatalf("wrong merchant query: %v", err)
		}
		if runtime.checkoutReads != 0 {
			t.Fatal("queried a different merchant's session")
		}
		assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
		runtime.identity = identity
		if err := service.HandleProductCheckoutCheckJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		assertCheckoutState(t, pool, checkout, "cancelled", "cancelled", 0)
	})
	t.Run("refund", func(t *testing.T) {
		pool, cleanup := paymentTestPool(t)
		defer cleanup()
		ctx := context.Background()
		base := &refundReadRuntime{}
		service, checkout, _, _, _ := fulfilledRefundFixture(t, pool, base)
		identity, _ := base.ProductCheckoutIdentity(ctx)
		runtime := &merchantBoundRuntime{ProviderRuntime: base, identity: identity}
		runtime.identity.MerchantID = "acct_wrong123"
		service.runtimes = NewRuntimeCatalog(runtime)
		history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
		if err != nil {
			t.Fatal(err)
		}
		history, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion)
		if err != nil {
			t.Fatal(err)
		}
		var job jobs.Job
		if err := pool.QueryRow(ctx, `SELECT j.id,j.payload FROM product_refund_checks c JOIN jobs j ON j.id=c.job_id WHERE c.id=$1`, history.LatestCheck.ID).Scan(&job.ID, &job.Payload); err != nil {
			t.Fatal(err)
		}
		if err := service.HandleProductRefundCheckJob(ctx, job); !errors.Is(err, ErrCheckoutReconciliation) {
			t.Fatalf("wrong refund merchant query: %v", err)
		}
		if base.reads != 0 {
			t.Fatal("queried another merchant's refunds")
		}
		var status, code string
		if err := pool.QueryRow(ctx, `SELECT status,error_code FROM product_refund_checks WHERE id=$1`, history.LatestCheck.ID).Scan(&status, &code); err != nil || status != "failed" || code != "payment_reconciliation_required" {
			t.Fatalf("missing query failure evidence: %s %s %v", status, code, err)
		}
	})
}

func TestProductMissingMerchantEvidenceDoesNotDispatch(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	base := &durableProductRefundRuntime{}
	service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, base)
	if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "legacy-refund-evidence", "test", "The supplied resource does not meet its stated contract."); err != nil {
		t.Fatal(err)
	}
	// Represent a pre-0085 order in this isolated fixture schema. Production
	// migration intentionally does not manufacture historical request evidence.
	if _, err := pool.Exec(ctx, `TRUNCATE product_checkout_dispatches, product_checkout_requests`); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, checkout.PaymentID)); !errors.Is(err, ErrCheckoutReconciliation) || len(base.operations) != 0 {
		t.Fatalf("missing evidence refund: %v", err)
	}
	var entitlement uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM entitlements WHERE order_id=$1 AND status='active'`, checkout.OrderID).Scan(&entitlement); err != nil {
		t.Fatal("identity uncertainty revoked buyer rights", err)
	}
}
