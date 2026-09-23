package httpapi_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestRecoveredPaidCheckoutFinanceRecoveryHTTP(t *testing.T) {
	for _, status := range []string{"checkout_open", "cancelled", "payment_failed"} {
		t.Run(status, func(t *testing.T) { recoveredPaidCheckoutFinanceRecoveryHTTP(t, status) })
	}
}
func recoveredPaidCheckoutFinanceRecoveryHTTP(t *testing.T, status string) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	asset, product, order, payment, recovery := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	operation := uuid.New()
	var providerReads atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerReads.Add(1)
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer historical-http-fixture" || r.Header.Get("Stripe-Version") != "2026-02-25.clover" {
			t.Error("unexpected financial command or unauthenticated read")
			http.Error(w, "rejected", 400)
			return
		}
		switch r.URL.Path {
		case "/v1/account":
			fmt.Fprint(w, `{"id":"acct_fixture","object":"account"}`)
		case "/v1/balance":
			fmt.Fprint(w, `{"object":"balance","livemode":false}`)
		case "/v1/payment_intents/pi_recovered_http":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "pi_recovered_http", "amount": 1900, "amount_received": 1900, "currency": "usd", "livemode": false, "status": "succeeded", "metadata": map[string]string{"hcai_payment_id": payment.String(), "hcai_resource_id": product.String(), "hcai_purpose": "product"}})
		case "/v1/refunds":
			if r.URL.Query().Get("payment_intent") != "pi_recovered_http" {
				t.Error("unscoped refund query")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "has_more": false, "data": []map[string]any{{"id": "re_historical_http", "payment_intent": "pi_recovered_http", "amount": 1900, "currency": "usd", "status": "succeeded", "metadata": map[string]string{"hcai_payment_id": payment.String(), "hcai_refund_operation_id": operation.String()}}}})
		default:
			t.Error("unexpected provider path")
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
		StripeEnabled: true, StripeSecretKey: "historical-http-fixture", StripeBaseURL: upstream.URL + "/v1", StripeAPIVersion: "2026-02-25.clover",
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	adminClient, memberClient := testHTTPClient(t), testHTTPClient(t)
	administrator := registerGovernanceUser(t, adminClient, server.URL, "recovered_admin")
	member := registerGovernanceUser(t, memberClient, server.URL, "recovered_member")
	ctx := t.Context()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE users SET role='admin' WHERE id=$1`, administrator.ID)
	exec(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
 VALUES($1,$2,'image','Recovered sale source','/media/recovered-sale.jpg','image/jpeg','clean','delivery','hcai-commercial-standard-v1')`, asset, member.ID)
	exec(`INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status)
 VALUES($1,$2,$3,'Recovered sale','Historical authenticated checkout fixture.','workflow',1900,'USD','hcai-commercial-standard-v1','active')`, product, member.ID, asset)
	exec(`INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,idempotency_key,product_title_snapshot,license_name_snapshot,license_version,license_terms_snapshot,refund_window_days_snapshot)
 VALUES($1,$2,$3,1900,'USD','payment_pending','recovered-order','Recovered sale','Commercial Standard','1.0','Fixture terms',14)`, order, administrator.ID, product)
	exec(`INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key,provider_checkout_id,checkout_url,checkout_expires_at)
 VALUES($1,'stripe','product',$2,$3,$4,$5,1900,'USD','checkout_open',false,'recovered-payment','cs_recovered_http',NULL,now()+interval '1 hour')`, payment, administrator.ID, member.ID, product, order)
	exec(`INSERT INTO jobs(id,kind,payload,status,max_attempts) VALUES($1,$2,jsonb_build_object('paymentId',$3::text,'actorId',$4::text),'succeeded',20)`, recovery, payments.ProductIdentityRecoveryJobKind, payment, administrator.ID)
	identityBody, _ := json.Marshal(payments.ProductCheckoutIdentity{Provider: "stripe", MerchantID: "acct_fixture", Endpoint: upstream.URL + "/v1", APIVersion: "2026-02-25.clover", RequestVersion: "stripe-product-checkout-v1"})
	bindingBody, _ := json.Marshal(payments.ProductPaymentBinding{PaymentID: payment, ResourceID: product, OrderID: order, BuyerID: administrator.ID, AmountCents: 1900, Currency: "USD", ProviderCheckoutID: "cs_recovered_http"})
	observationBody, _ := json.Marshal(payments.ProductPaymentIdentityObservation{Checkout: &payments.CheckoutObservation{ProviderCheckoutID: "cs_recovered_http", Status: "complete", PaymentStatus: "paid", ProviderPaymentID: "pi_recovered_http", ProviderChargeID: "ch_recovered_http", IntentStatus: "succeeded", AmountReceived: 1900, AmountCents: 1900, Currency: "USD", ExpiresAt: time.Now().Add(time.Hour)}})
	exec(`INSERT INTO product_payment_identity_recoveries(payment_id,job_id,requested_by,identity,binding,observation) VALUES($1,$2,$3,$4,$5,$6)`, payment, recovery, administrator.ID, identityBody, bindingBody, observationBody)
	attention := "none"
	if status != "checkout_open" {
		exec(`UPDATE payment_intents SET status=$2 WHERE id=$1`, payment, status)
		exec(`UPDATE orders SET status=$2 WHERE id=$1`, order, status)
		attention = "checkout_reconciliation_required"
	}
	listURL := server.URL + "/api/v1/admin/payments?q=" + payment.String()
	recoverURL := server.URL + "/api/v1/admin/payments/" + payment.String() + "/recover"
	var page admin.PaymentOperationPage
	response := requestJSON(t, adminClient, http.MethodGet, listURL, nil, &page)
	if response.StatusCode != http.StatusOK || len(page.Items) != 1 || !page.Items[0].CanCheckCheckout || page.Items[0].AttentionCode != attention {
		t.Fatal("saved paid session was not exposed for finance recovery", response.StatusCode, page)
	}
	input := map[string]any{"action": "check_checkout", "expectedVersion": 1}
	if response := requestJSON(t, memberClient, http.MethodPost, recoverURL, input, nil); response.StatusCode != http.StatusForbidden {
		t.Fatal("member recovered another payment", response.StatusCode)
	}
	if response := requestJSON(t, adminClient, http.MethodPost, recoverURL, map[string]any{"action": "check_checkout", "expectedVersion": 2}, nil); response.StatusCode != http.StatusConflict {
		t.Fatal("stale recovery version was accepted", response.StatusCode)
	}
	var result admin.PaymentOperation
	for version := 1; version <= 2; version++ {
		response := requestJSON(t, adminClient, http.MethodPost, recoverURL, map[string]any{"action": "check_checkout", "expectedVersion": version}, &result)
		if response.StatusCode != http.StatusOK || result.Status != status || result.Version != version+1 || result.Job == nil || result.Job.Kind != payments.ProductCheckoutCheckJobKind {
			t.Fatal("finance recovery did not schedule original payment check", response.StatusCode, result)
		}
		if duplicate := requestJSON(t, adminClient, http.MethodPost, recoverURL, map[string]any{"action": "check_checkout", "expectedVersion": result.Version}, nil); duplicate.StatusCode != http.StatusConflict {
			t.Fatal("duplicate active check accepted", duplicate.StatusCode)
		}
		exec(`UPDATE jobs SET status='failed',last_error_code='payment_request_failed' WHERE id=$1`, result.Job.ID)
	}

	if status != "checkout_open" {
		exec(`INSERT INTO product_refund_attempts(operation_id,payment_id,provider,provider_payment_id,amount_cents,currency,correlation_enabled,provider_refund_id,status,requested_at) VALUES($1,$2,'stripe','pi_recovered_http',1900,'USD',true,'re_historical_http','pending',now()-interval '1 day')`, operation, payment)
		exec(`UPDATE orders SET refund_operation_id=$2,refund_correlation_enabled=true,refund_requested_at=now()-interval '1 day' WHERE id=$1`, order, operation)
		var check jobs.Job
		if err := pool.QueryRow(ctx, `SELECT id,kind,payload FROM jobs WHERE id=$1`, result.Job.ID).Scan(&check.ID, &check.Kind, &check.Payload); err != nil {
			t.Fatal(err)
		}
		service := payments.NewServiceWithRuntimes(pool, payments.ServiceConfig{Enabled: true, APIVersion: "2026-02-25.clover"}, payments.NewRuntimeCatalog())
		if err := service.HandleProductCheckoutCheckJob(ctx, check); err != nil {
			t.Fatal(err)
		}
		var current string
		var refundJobs int
		if err := pool.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM jobs WHERE kind='payment.refund_product' AND payload->>'paymentId'=$1::uuid::text) FROM payment_intents WHERE id=$1::uuid`, payment).Scan(&current, &refundJobs); err != nil || current != status || refundJobs != 0 {
			t.Fatal("closed recovery changed money or order", current, refundJobs, err)
		}
		response = requestJSON(t, adminClient, http.MethodGet, listURL, nil, &page)
		if response.StatusCode != http.StatusOK || len(page.Items) != 1 || page.Items[0].CanCheckCheckout {
			t.Fatal("preserved payment still offered checkout recovery", page)
		}
		historyURL := server.URL + "/api/v1/admin/payments/" + payment.String() + "/refund-history"
		checksURL := server.URL + "/api/v1/admin/payments/" + payment.String() + "/refund-checks"
		var history payments.RefundHistory
		response = requestJSON(t, adminClient, http.MethodGet, historyURL, nil, &history)
		if response.StatusCode != http.StatusOK || history.LatestCheck == nil || history.CanCheck {
			t.Fatal("missing active history check", history)
		}
		if response := requestJSON(t, memberClient, http.MethodPost, checksURL, map[string]any{"expectedVersion": history.PaymentVersion}, nil); response.StatusCode != http.StatusForbidden {
			t.Fatal("member retried closed payment", response.StatusCode)
		}
		exec(`UPDATE jobs SET status='failed',last_error_code='payment_request_failed' WHERE id=(SELECT job_id FROM product_refund_checks WHERE id=$1)`, history.LatestCheck.ID)
		response = requestJSON(t, adminClient, http.MethodGet, historyURL, nil, &history)
		if response.StatusCode != http.StatusOK || !history.CanCheck {
			t.Fatal("closed payment cannot retry failed history query", history)
		}
		if response := requestJSON(t, adminClient, http.MethodPost, checksURL, map[string]any{"expectedVersion": history.PaymentVersion + 1}, nil); response.StatusCode != http.StatusConflict {
			t.Fatal("stale refund query accepted", response.StatusCode)
		}
		response = requestJSON(t, adminClient, http.MethodPost, checksURL, map[string]any{"expectedVersion": history.PaymentVersion}, &history)
		if response.StatusCode != http.StatusOK || history.LatestCheck == nil || history.LatestCheck.Status != "requested" || history.CanCheck {
			t.Fatal("closed payment history retry not queued", response.StatusCode, history)
		}
		if response := requestJSON(t, adminClient, http.MethodPost, checksURL, map[string]any{"expectedVersion": history.PaymentVersion}, nil); response.StatusCode != http.StatusConflict {
			t.Fatal("duplicate history query accepted", response.StatusCode)
		}
		if providerReads.Load() != 0 {
			t.Fatal("finance scheduling contacted payment provider")
		}
		var historyJob jobs.Job
		if err := pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM product_refund_checks c JOIN jobs j ON j.id=c.job_id WHERE c.id=$1`, history.LatestCheck.ID).Scan(&historyJob.ID, &historyJob.Kind, &historyJob.Payload); err != nil {
			t.Fatal(err)
		}
		reader := payments.NewStripeRuntime(payments.StripeRuntimeConfig{BaseURL: upstream.URL + "/v1", SecretKey: "historical-http-fixture", APIVersion: "2026-02-25.clover"})
		verified := payments.NewServiceWithRuntimes(pool, payments.ServiceConfig{Enabled: true, APIVersion: "2026-02-25.clover"}, payments.NewRuntimeCatalog(reader))
		if err := verified.HandleProductRefundCheckJob(ctx, historyJob); err != nil {
			t.Fatal(err)
		}
		if providerReads.Load() != 4 {
			t.Fatal("incomplete provider identity/refund verification", providerReads.Load())
		}
		var original jobs.Job
		if err := pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM product_closed_checkout_recoveries r JOIN jobs j ON j.payload->>'eventId'=r.event_id::text AND j.kind=$2 WHERE r.payment_id=$1`, payment, payments.PaymentEventJobKind).Scan(&original.ID, &original.Kind, &original.Payload); err != nil {
			t.Fatal(err)
		}
		if err := verified.HandlePaymentEventJob(ctx, original); err != nil {
			t.Fatal(err)
		}
		response = requestJSON(t, adminClient, http.MethodGet, listURL, nil, &page)
		if response.StatusCode != http.StatusOK || len(page.Items) != 1 || page.Items[0].Status != "refunded" || page.Items[0].CanCheckCheckout {
			t.Fatal("finance did not show confirmed original refund", response.StatusCode, page)
		}
		if r := requestJSON(t, adminClient, http.MethodPost, recoverURL, map[string]any{"action": "retry_refund", "expectedVersion": page.Items[0].Version}, nil); r.StatusCode != http.StatusConflict {
			t.Fatal("confirmed historical refund allowed outbound retry", r.StatusCode)
		}
		var buyerOrder marketplace.Order
		response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/orders/"+order.String(), nil, &buyerOrder)
		if response.StatusCode != http.StatusOK || buyerOrder.Status != "refunded" || buyerOrder.CanRequestRefund {
			t.Fatal("buyer did not see confirmed historical refund", response.StatusCode, buyerOrder)
		}
		if r := requestJSON(t, memberClient, http.MethodGet, server.URL+"/api/v1/orders/"+order.String(), nil, nil); r.StatusCode != http.StatusNotFound {
			t.Fatal("seller can access buyer order", r.StatusCode)
		}
		var outbound int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind='payment.refund_product' AND payload->>'paymentId'=$1`, payment.String()).Scan(&outbound); err != nil || outbound != 0 {
			t.Fatal("historical confirmation created refund jobs", outbound, err)
		}
		return
	}
	exec(`UPDATE payment_intents SET provider_checkout_id='cs_different' WHERE id=$1`, payment)
	response = requestJSON(t, adminClient, http.MethodGet, listURL, nil, &page)
	if response.StatusCode != http.StatusOK || len(page.Items) != 1 || page.Items[0].CanCheckCheckout || page.Items[0].AttentionCode != "checkout_reconciliation_required" {
		t.Fatal("unrelated saved session qualified current checkout", page)
	}
	if response := requestJSON(t, adminClient, http.MethodPost, recoverURL, map[string]any{"action": "check_checkout", "expectedVersion": 3}, nil); response.StatusCode != http.StatusConflict {
		t.Fatal("different checkout session bypassed expiry", response.StatusCode)
	}
	var events int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_provider_events WHERE payment_id=$1`, payment).Scan(&events); err != nil || events != 0 {
		t.Fatal("finance scheduling synthesized payment evidence", events, err)
	}
	// A saved query contradicting the restored receipt must remain visible to
	// finance, but scheduling another check cannot resolve that disagreement.
	exec(`UPDATE payment_intents SET provider_checkout_id='cs_recovered_http' WHERE id=$1`, payment)
	conflicting, _ := json.Marshal(payments.CheckoutObservation{ProviderCheckoutID: "cs_recovered_http", Status: "complete", PaymentStatus: "paid", ProviderPaymentID: "pi_recovered_http", ProviderChargeID: "ch_conflicting_http", IntentStatus: "succeeded", AmountReceived: 1900, AmountCents: 1900, Currency: "USD", ExpiresAt: time.Now().Add(time.Hour)})
	exec(`INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
 VALUES($1,'checkout.queried','checkout_open','checkout_open',jsonb_build_object('source','provider_query','jobId',$2::text,'observation',$3::jsonb))`, payment, result.Job.ID, conflicting)
	response = requestJSON(t, adminClient, http.MethodGet, listURL, nil, &page)
	if response.StatusCode != http.StatusOK || len(page.Items) != 1 || page.Items[0].CanCheckCheckout || page.Items[0].AttentionCode != "checkout_reconciliation_required" {
		t.Fatal("conflicting payment evidence was not exposed for finance review", response.StatusCode, page)
	}
	if response := requestJSON(t, adminClient, http.MethodPost, recoverURL, map[string]any{"action": "check_checkout", "expectedVersion": 3}, nil); response.StatusCode != http.StatusConflict {
		t.Fatal("conflicting evidence allowed another checkout check", response.StatusCode)
	}
}
