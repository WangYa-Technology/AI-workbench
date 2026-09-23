package httpapi_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/tasks"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestStripeWebhookHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	const secret = "whsec_http_contract_secret"
	cfg := config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
		StripeEnabled: true, StripeWebhookSecret: secret, StripeAPIVersion: "2026-02-25.clover", StripeWebhookToleranceSeconds: 300,
	}
	server := httptest.NewServer(httpapi.New(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	now := time.Now().UTC().Unix()
	paymentID, resourceID := uuid.New(), uuid.New()
	buyer := registerGovernanceUser(t, testHTTPClient(t), server.URL, "receipt_buyer")
	seller := registerGovernanceUser(t, testHTTPClient(t), server.URL, "receipt_seller")
	assetID, orderID := uuid.New(), uuid.New()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Normal product admission needs an actual original local transaction.
	// Unknown signed claims are covered below as quarantined evidence.
	exec(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 VALUES($1,$2,'image','Receipt source','/private/source','image/jpeg','clean','upload','hcai-commercial-standard-v1','local_file','private/http-receipt.jpg')`, assetID, seller.ID)
	exec(`INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status)
 VALUES($1,$2,$3,'Receipt product','Original receipt product','asset',1900,'USD','hcai-commercial-standard-v1','active')`, resourceID, seller.ID, assetID)
	exec(`INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,license_accepted_at,idempotency_key,license_version,license_terms_snapshot,product_title_snapshot,license_name_snapshot,refund_window_days_snapshot)
 VALUES($1,$2,$3,1900,'USD','payment_pending',now(),'http-receipt-order','1.0','Accepted terms','Receipt product','Commercial Standard',7)`, orderID, buyer.ID, resourceID)
	exec(`INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key,provider_checkout_id)
 VALUES($1,'stripe','product',$2,$3,$4,$5,1900,'USD','checkout_pending',false,'http-receipt-payment','cs_httpcontract')`, paymentID, buyer.ID, seller.ID, resourceID, orderID)
	body := []byte(fmt.Sprintf(`{"id":"evt_httpcontract","object":"event","api_version":"2026-02-25.clover","created":%d,"livemode":false,"type":"checkout.session.completed","data":{"object":{"id":"cs_httpcontract","object":"checkout.session","status":"complete","payment_status":"paid","amount_total":1900,"currency":"usd","payment_intent":"pi_httpcontract","metadata":{"hcai_payment_id":%q,"hcai_resource_id":%q,"hcai_purpose":"product"}}}}`, now, paymentID.String(), resourceID.String()))

	var receipt payments.Receipt
	gate, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(t.Context())
	if _, err = gate.Exec(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "webhook-admission:stripe:evt_httpcontract"); err != nil {
		t.Fatal(err)
	}
	var busy struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if response := postStripeWebhook(t, server.URL, body, "t="+fmt.Sprint(now)+",v1="+paymentStripeSignature(secret, now, body), "application/json", &busy); response.StatusCode != 503 || busy.Error.Code != "payment_event_busy" || !busy.Error.Retryable {
		t.Fatalf("concurrent admission was not retryable: %d %#v", response.StatusCode, busy)
	}
	if err = gate.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	response := postStripeWebhook(t, server.URL, body, "t="+fmt.Sprint(now)+",v1="+paymentStripeSignature(secret, now, body), "application/json", &receipt)
	if response.StatusCode != http.StatusAccepted || receipt.Duplicate || receipt.Status != "received" || receipt.ProviderEventID != "evt_httpcontract" {
		t.Fatalf("signed Stripe event was not accepted: status=%d receipt=%#v", response.StatusCode, receipt)
	}
	receipt = payments.Receipt{}
	response = postStripeWebhook(t, server.URL, body, "t="+fmt.Sprint(now)+",v1="+paymentStripeSignature(secret, now, body), "application/json; charset=utf-8", &receipt)
	if response.StatusCode != http.StatusOK || !receipt.Duplicate || receipt.Status != "received" {
		t.Fatalf("duplicate Stripe event was not safely acknowledged: status=%d receipt=%#v", response.StatusCode, receipt)
	}
	unknown := []byte(strings.NewReplacer("evt_httpcontract", "evt_unknown_receipt", paymentID.String(), uuid.NewString()).Replace(string(body)))
	response = postStripeWebhook(t, server.URL, unknown, "t="+fmt.Sprint(now)+",v1="+paymentStripeSignature(secret, now, unknown), "application/json", nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unknown product payment was admitted: %d", response.StatusCode)
	}
	var quarantined int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM product_webhook_quarantines WHERE rejection_code='payment_unknown'`).Scan(&quarantined); err != nil || quarantined != 1 {
		t.Fatal(quarantined, err)
	}
	exec(`CREATE FUNCTION fail_quarantine_http_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private-storage-failure'; END $$;
 CREATE TRIGGER fail_quarantine_http_fixture BEFORE INSERT ON product_webhook_quarantines FOR EACH ROW EXECUTE FUNCTION fail_quarantine_http_fixture()`)
	failed := []byte(strings.Replace(string(unknown), "evt_unknown_receipt", "evt_failed_persistence", 1))
	var failure map[string]any
	response = postStripeWebhook(t, server.URL, failed, "t="+fmt.Sprint(now)+",v1="+paymentStripeSignature(secret, now, failed), "application/json", &failure)
	rawFailure, _ := json.Marshal(failure)
	if response.StatusCode != http.StatusInternalServerError || strings.Contains(string(rawFailure), "private-storage-failure") {
		t.Fatal(response.StatusCode, failure)
	}
	exec(`DROP TRIGGER fail_quarantine_http_fixture ON product_webhook_quarantines`)

	response = postStripeWebhook(t, server.URL, body, "t="+fmt.Sprint(now)+",v1="+strings.Repeat("0", 64), "application/json", nil)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid Stripe signature returned %d", response.StatusCode)
	}
	response = postStripeWebhook(t, server.URL, body, "", "text/plain", nil)
	if response.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("invalid Stripe content type returned %d", response.StatusCode)
	}
	oversized := bytes.Repeat([]byte("x"), 1024*1024+1)
	response = postStripeWebhook(t, server.URL, oversized, "t="+fmt.Sprint(now)+",v1="+strings.Repeat("0", 64), "application/json", nil)
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized Stripe event returned %d", response.StatusCode)
	}

	disabledServer := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer disabledServer.Close()
	response = postStripeWebhook(t, disabledServer.URL, body, "unused", "application/json", nil)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("disabled payment Provider returned %d", response.StatusCode)
	}
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM payment_provider_events WHERE provider_event_id='evt_httpcontract'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("HTTP event idempotency evidence mismatch: count=%d err=%v", count, err)
	}
}

func TestPayoutOnboardingHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	const secret = "whsec_payout_http_contract"
	var accountCalls, linkCalls int
	stripe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/v1/account":
			fmt.Fprint(w, `{"object":"account","id":"acct_payout_merchant"}`)
		case "/v1/balance":
			fmt.Fprint(w, `{"object":"balance","livemode":false}`)
		case "/v1/accounts":
			accountCalls++
			if r.Header.Get("Authorization") != "Bearer sk_test_payout_http_contract" || r.Header.Get("Stripe-Version") != "2026-02-25.clover" ||
				!strings.HasPrefix(r.Header.Get("Idempotency-Key"), "connect-account-") || r.PostForm.Get("type") != "express" ||
				r.PostForm.Get("capabilities[card_payments][requested]") != "true" || r.PostForm.Get("capabilities[transfers][requested]") != "true" ||
				r.PostForm.Get("metadata[hcai_user_id]") == "" || r.PostForm.Get("email") == "" {
				t.Fatalf("unexpected Connect account request: headers=%v form=%v", r.Header, r.PostForm)
			}
			fmt.Fprintf(w, `{"id":"acct_http_payout","object":"account","type":"express","metadata":{"hcai_user_id":%q},"charges_enabled":false,"payouts_enabled":false,"details_submitted":false,"requirements":{"currently_due":["individual.first_name"],"past_due":[],"pending_verification":[]}}`, r.PostForm.Get("metadata[hcai_user_id]"))
		case "/v1/account_links":
			linkCalls++
			if r.Header.Get("Authorization") != "Bearer sk_test_payout_http_contract" || r.Header.Get("Stripe-Version") != "2026-02-25.clover" ||
				!strings.HasPrefix(r.Header.Get("Idempotency-Key"), "connect-link-") || r.PostForm.Get("account") != "acct_http_payout" ||
				r.PostForm.Get("type") != "account_onboarding" || r.PostForm.Get("refresh_url") != "https://app.example.test/settings?section=payouts&connect=refresh" ||
				r.PostForm.Get("return_url") != "https://app.example.test/settings?section=payouts&connect=return" {
				t.Fatalf("unexpected Account Link request: headers=%v form=%v", r.Header, r.PostForm)
			}
			fmt.Fprintf(w, `{"object":"account_link","url":"https://connect.stripe.com/setup/c/http-payout-%d","expires_at":%d}`, linkCalls, time.Now().Add(10*time.Minute).Unix())
		default:
			http.NotFound(w, r)
		}
	}))
	defer stripe.Close()
	cfg := config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "https://app.example.test", LocalProviderEnabled: true,
		StripeEnabled: true, StripeSecretKey: "sk_test_payout_http_contract", StripeWebhookSecret: secret,
		StripeBaseURL: stripe.URL + "/v1", StripeAPIVersion: "2026-02-25.clover", StripeWebhookToleranceSeconds: 300,
	}
	server := httptest.NewServer(httpapi.New(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	anonymous := testHTTPClient(t)
	if response := requestPaymentJSON(t, anonymous, http.MethodGet, server.URL+"/api/v1/account/payouts", "", nil, nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous payout status returned %d", response.StatusCode)
	}
	if response := requestPaymentJSON(t, anonymous, http.MethodPost, server.URL+"/api/v1/account/payouts/onboarding", "", nil, nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous payout onboarding returned %d", response.StatusCode)
	}

	ownerClient, outsiderClient := testHTTPClient(t), testHTTPClient(t)
	owner := registerGovernanceUser(t, ownerClient, server.URL, "payout_http_owner")
	_ = registerGovernanceUser(t, outsiderClient, server.URL, "payout_http_outsider")
	var before payments.PayoutStatus
	if response := requestPaymentJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/account/payouts", "", nil, &before); response.StatusCode != http.StatusOK || !before.ProviderAvailable || before.Status != "not_started" || before.DestinationID != nil || !before.CanStartOnboarding {
		t.Fatalf("initial payout status mismatch: status=%d value=%#v", response.StatusCode, before)
	}
	var first, second payments.PayoutOnboardingLink
	if response := requestPaymentJSON(t, ownerClient, http.MethodPost, server.URL+"/api/v1/account/payouts/onboarding", "payout-http-1", nil, &first); response.StatusCode != http.StatusCreated || first.Status.Status != "pending_onboarding" || first.Status.DestinationID == nil || *first.Status.DestinationID != "acct_http_payout" || !strings.HasPrefix(first.URL, "https://connect.stripe.com/") {
		t.Fatalf("first payout onboarding mismatch: status=%d value=%#v", response.StatusCode, first)
	}
	if response := requestPaymentJSON(t, ownerClient, http.MethodPost, server.URL+"/api/v1/account/payouts/onboarding", "payout-http-2", nil, &second); response.StatusCode != http.StatusCreated || second.URL == first.URL || second.Status.DestinationID == nil || *second.Status.DestinationID != "acct_http_payout" || accountCalls != 1 || linkCalls != 2 {
		t.Fatalf("payout link renewal mismatch: status=%d first=%#v second=%#v accountCalls=%d linkCalls=%d", response.StatusCode, first, second, accountCalls, linkCalls)
	}
	var isolated payments.PayoutStatus
	if response := requestPaymentJSON(t, outsiderClient, http.MethodGet, server.URL+"/api/v1/account/payouts", "", nil, &isolated); response.StatusCode != http.StatusOK || isolated.Status != "not_started" || isolated.DestinationID != nil {
		t.Fatalf("payout status leaked between owners: status=%d value=%#v", response.StatusCode, isolated)
	}

	now := time.Now().UTC().Unix()
	body := []byte(fmt.Sprintf(`{"id":"evt_http_payout_verified","object":"event","api_version":"2026-02-25.clover","created":%d,"livemode":false,"type":"account.updated","data":{"object":{"id":"acct_http_payout","object":"account","charges_enabled":true,"payouts_enabled":true,"details_submitted":true,"metadata":{"hcai_user_id":%q},"requirements":{"currently_due":[],"past_due":[],"pending_verification":[]}}}}`, now, owner.ID.String()))
	var receipt payments.Receipt
	if response := postStripeWebhook(t, server.URL, body, "t="+fmt.Sprint(now)+",v1="+paymentStripeSignature(secret, now, body), "application/json", &receipt); response.StatusCode != http.StatusAccepted || receipt.EventType != "account.updated" || receipt.Duplicate {
		t.Fatalf("payout account.updated receipt mismatch: status=%d receipt=%#v", response.StatusCode, receipt)
	}
	// The event worker must use the same authenticated Stripe runtime as the
	// API process so account.updated can verify the persisted merchant binding.
	workerPayments := payments.NewServiceFromConfig(pool, cfg)
	if err := workerPayments.HandlePaymentEventJob(context.Background(), jobs.Job{Kind: payments.PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}); err != nil {
		t.Fatal(err)
	}
	var verified payments.PayoutStatus
	if response := requestPaymentJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/account/payouts", "", nil, &verified); response.StatusCode != http.StatusOK || verified.Status != "verified" || !verified.ChargesEnabled || !verified.PayoutsEnabled || !verified.DetailsSubmitted || verified.RequirementsDue || verified.VerifiedAt == nil {
		t.Fatalf("verified payout status mismatch: status=%d value=%#v", response.StatusCode, verified)
	}

	disabledServer := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "https://app.example.test", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer disabledServer.Close()
	disabledClient := testHTTPClient(t)
	_ = registerGovernanceUser(t, disabledClient, disabledServer.URL, "payout_http_disabled")
	if response := requestPaymentJSON(t, disabledClient, http.MethodPost, disabledServer.URL+"/api/v1/account/payouts/onboarding", "", nil, nil); response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("disabled payout onboarding returned %d", response.StatusCode)
	}
}

func TestProductCheckoutAndRefundHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	const secret = "whsec_product_http_contract"
	var checkoutCalls, refundCalls int
	var cancelURL, successURL string
	var loseCheckoutResponse atomic.Bool
	var checkoutMerchant atomic.Value
	checkoutMerchant.Store("acct_http_product")
	stripe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/v1/account":
			fmt.Fprintf(w, `{"object":"account","id":%q}`, checkoutMerchant.Load().(string))
		case "/v1/balance":
			fmt.Fprint(w, `{"object":"balance","livemode":false}`)
		case "/v1/checkout/sessions":
			checkoutCalls++
			if loseCheckoutResponse.Load() {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			cancelURL = r.PostForm.Get("cancel_url")
			successURL = r.PostForm.Get("success_url")
			paymentID := r.PostForm.Get("client_reference_id")
			amount := r.PostForm.Get("line_items[0][price_data][unit_amount]")
			fmt.Fprintf(w, `{"id":"cs_http_product","url":"https://checkout.stripe.com/c/pay/http-product","status":"open","payment_status":"unpaid","expires_at":%d,"livemode":false,"amount_total":%s,"currency":"usd","client_reference_id":%q}`, time.Now().Add(time.Hour).Unix(), amount, paymentID)
		case "/v1/refunds":
			refundCalls++
			fmt.Fprintf(w, `{"id":"re_http_product","payment_intent":%q,"amount":%s,"currency":"usd","status":"pending"}`, r.PostForm.Get("payment_intent"), r.PostForm.Get("amount"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer stripe.Close()
	cfg := config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
		StripeEnabled: true, StripeSecretKey: "sk_test_http_contract", StripeWebhookSecret: secret,
		StripeBaseURL: stripe.URL + "/v1", StripeAPIVersion: "2026-02-25.clover", StripeWebhookToleranceSeconds: 300,
	}
	server := httptest.NewServer(httpapi.New(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	sellerClient, buyerClient := testHTTPClient(t), testHTTPClient(t)
	seller := registerGovernanceUser(t, sellerClient, server.URL, "payment_http_seller")
	buyer := registerGovernanceUser(t, buyerClient, server.URL, "payment_http_buyer")
	assetID, productID := uuid.New(), uuid.New()
	licensedBytes := []byte("Private licensed delivery bytes for the signed checkout test.")
	if err := os.WriteFile(filepath.Join(cfg.MediaRoot, assetID.String()+".txt"), licensedBytes, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
		VALUES($1,$2,'document','Payment HTTP Asset',$3,'text/plain','clean','upload','hcai-commercial-standard-v1','local_file',$1::uuid::text||'.txt')`,
		assetID, seller.ID, "/api/v1/assets/"+assetID.String()+"/content"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status,ai_disclosure,included_files,compatibility)
		VALUES($1,$2,$3,'Payment HTTP workflow','HTTP checkout and refund evidence.','workflow',1900,'USD','hcai-commercial-standard-v1','active','AI-assisted.','[]','HCAI CHAT')`,
		productID, seller.ID, assetID); err != nil {
		t.Fatal(err)
	}

	var checkout payments.Checkout
	var product marketplace.Product
	assertCrossOriginRejected := func(path, key string, input any) {
		t.Helper()
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, server.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Origin", "https://other.example.test")
		req.Header.Set("Sec-Fetch-Site", "same-site")
		res, err := buyerClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var failure struct {
			Error struct{ Code string } `json:"error"`
		}
		if err := json.NewDecoder(res.Body).Decode(&failure); err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusForbidden || failure.Error.Code != "cross_origin_request" {
			t.Fatalf("foreign write reached transaction handler: status=%d code=%s", res.StatusCode, failure.Error.Code)
		}
	}
	if response := requestPaymentJSON(t, buyerClient, http.MethodGet, server.URL+"/api/v1/products/"+productID.String(), "", nil, &product); response.StatusCode != http.StatusOK || len(product.OfferVersion) != 64 {
		t.Fatalf("missing offer version: status=%d version=%q", response.StatusCode, product.OfferVersion)
	}
	for _, version := range []string{"", strings.Repeat("Z", 64), strings.Repeat("0", 64)} {
		want := http.StatusUnprocessableEntity
		if version == strings.Repeat("0", 64) {
			want = http.StatusConflict
		}
		response := requestPaymentJSON(t, buyerClient, http.MethodPost, server.URL+"/api/v1/products/"+productID.String()+"/checkout", "http-invalid-offer", map[string]any{"licenseAccepted": true, "offerVersion": version}, nil)
		if response.StatusCode != want || checkoutCalls != 0 {
			t.Fatalf("invalid offer had incorrect status or provider side effects: status=%d want=%d calls=%d", response.StatusCode, want, checkoutCalls)
		}
	}
	assertCrossOriginRejected("/api/v1/products/"+productID.String()+"/checkout", "foreign-checkout", map[string]any{"licenseAccepted": true, "offerVersion": product.OfferVersion})
	var foreignOrders int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM orders WHERE buyer_id=$1`, buyer.ID).Scan(&foreignOrders); err != nil || foreignOrders != 0 || checkoutCalls != 0 {
		t.Fatalf("foreign checkout left side effects: orders=%d calls=%d err=%v", foreignOrders, checkoutCalls, err)
	}
	response := requestPaymentJSON(t, buyerClient, http.MethodPost, server.URL+"/api/v1/products/"+productID.String()+"/checkout", "http-checkout-001", map[string]any{"licenseAccepted": true, "offerVersion": product.OfferVersion}, &checkout)
	if response.StatusCode != http.StatusCreated || checkout.PaymentMode != "stripe" || checkout.RealCharge || checkout.LiveMode || checkoutCalls != 1 {
		t.Fatalf("Provider checkout HTTP mismatch: status=%d checkout=%#v calls=%d", response.StatusCode, checkout, checkoutCalls)
	}
	for _, target := range []struct{ raw, path, state string }{
		{successURL, "/workspace/orders", "success"},
		{cancelURL, "/market/assets/" + productID.String(), "cancelled"},
	} {
		parsed, err := url.Parse(target.raw)
		if err != nil || parsed.Path != target.path || parsed.Query().Get("payment") != target.state || parsed.Query().Get("orderId") != checkout.OrderID.String() || parsed.Query().Get("paymentId") != checkout.PaymentID.String() {
			t.Fatalf("checkout return identifiers missing: %q %v", target.raw, err)
		}
	}
	var pendingOrder marketplace.Order
	response = requestPaymentJSON(t, buyerClient, http.MethodGet, server.URL+"/api/v1/orders/"+checkout.OrderID.String()+"?payment=success", "", nil, &pendingOrder)
	if response.StatusCode != http.StatusOK || pendingOrder.Status != "payment_pending" || pendingOrder.PaymentStatus != "checkout_open" || pendingOrder.PaymentID == nil || *pendingOrder.PaymentID != checkout.PaymentID || pendingOrder.CheckoutExpiresAt == nil || pendingOrder.AssetID != nil {
		t.Fatalf("return query falsely confirmed payment or lacks tracking fields: %+v", pendingOrder)
	}
	if response.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("private order tracking response is cacheable")
	}
	if response := requestPaymentJSON(t, sellerClient, http.MethodGet, server.URL+"/api/v1/orders/"+checkout.OrderID.String(), "", nil, nil); response.StatusCode != http.StatusNotFound {
		t.Fatalf("seller read buyer tracking data: %d", response.StatusCode)
	}
	response = requestPaymentJSON(t, buyerClient, http.MethodPost, server.URL+"/api/v1/products/"+productID.String()+"/checkout", "http-checkout-001", map[string]any{"licenseAccepted": true, "offerVersion": product.OfferVersion}, &checkout)
	if response.StatusCode != http.StatusOK || !checkout.AlreadyCreated || checkoutCalls != 1 {
		t.Fatalf("Provider checkout replay mismatch: status=%d checkout=%#v calls=%d", response.StatusCode, checkout, checkoutCalls)
	}

	now := time.Now().UTC().Unix()
	paidBody := []byte(fmt.Sprintf(`{"id":"evt_http_product_paid","object":"event","api_version":"2026-02-25.clover","created":%d,"livemode":false,"type":"checkout.session.completed","data":{"object":{"id":"cs_http_product","object":"checkout.session","status":"complete","payment_status":"paid","amount_total":1900,"currency":"usd","payment_intent":"pi_http_product","metadata":{"hcai_payment_id":%q,"hcai_resource_id":%q,"hcai_purpose":"product"}}}}`, now, checkout.PaymentID.String(), productID.String()))
	var paidReceipt payments.Receipt
	response = postStripeWebhook(t, server.URL, paidBody, "t="+fmt.Sprint(now)+",v1="+paymentStripeSignature(secret, now, paidBody), "application/json", &paidReceipt)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("signed checkout event returned %d", response.StatusCode)
	}
	workerPayments := payments.NewServiceFromConfig(pool, cfg)
	if err := workerPayments.HandlePaymentEventJob(context.Background(), jobs.Job{Kind: payments.PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, paidReceipt.EventID.String()))}); err != nil {
		t.Fatal(err)
	}

	var refundOrder marketplace.Order
	response = requestPaymentJSON(t, buyerClient, http.MethodGet, server.URL+"/api/v1/orders/"+checkout.OrderID.String(), "", nil, &refundOrder)
	if response.StatusCode != http.StatusOK || !refundOrder.CanRequestRefund || refundOrder.RefundDeadlineAt == nil || !refundOrder.RefundDeadlineAt.Equal(refundOrder.CreatedAt.Add(7*24*time.Hour)) {
		t.Fatalf("refund capability/deadline missing: status=%d order=%+v", response.StatusCode, refundOrder)
	}
	if refundOrder.Status != "fulfilled" || refundOrder.PaymentStatus != "paid" || refundOrder.PaymentID == nil || *refundOrder.PaymentID != checkout.PaymentID || refundOrder.AssetID == nil {
		t.Fatalf("signed fulfillment not reflected in tracked order: %+v", refundOrder)
	}
	assertPurchasePermission := func(allowed bool) {
		t.Helper()
		var projection struct {
			Provenance struct {
				Purchase struct {
					CanDownload bool `json:"canDownload"`
					CanReuse    bool `json:"canReuse"`
				} `json:"purchase"`
			} `json:"provenance"`
		}
		response := requestPaymentJSON(t, buyerClient, http.MethodGet, server.URL+"/api/v1/assets/"+refundOrder.AssetID.String(), "", nil, &projection)
		if response.StatusCode != http.StatusOK || projection.Provenance.Purchase.CanDownload != allowed || projection.Provenance.Purchase.CanReuse != allowed {
			t.Fatalf("purchase permission projection: status=%d projection=%+v want=%t", response.StatusCode, projection, allowed)
		}
		content, err := buyerClient.Get(server.URL + "/api/v1/assets/" + refundOrder.AssetID.String() + "/content")
		if err != nil {
			t.Fatal(err)
		}
		defer content.Body.Close()
		body, err := io.ReadAll(content.Body)
		if err != nil {
			t.Fatal(err)
		}
		if allowed {
			if content.StatusCode != http.StatusOK || !bytes.Equal(body, licensedBytes) || content.Header.Get("Cache-Control") != "private, no-store" {
				t.Fatalf("licensed bytes inaccessible or cacheable: status=%d body=%q", content.StatusCode, body)
			}
		} else if content.StatusCode != http.StatusForbidden {
			t.Fatalf("revoked media still accessible: %d", content.StatusCode)
		}
	}
	assertPurchasePermission(true)
	assertCrossOriginRejected("/api/v1/orders/"+checkout.OrderID.String()+"/refund", "foreign-refund", map[string]any{"reason": "A valid length refund reason"})
	var retainedStatus string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM orders WHERE id=$1`, checkout.OrderID).Scan(&retainedStatus); err != nil || retainedStatus != "fulfilled" || refundCalls != 0 {
		t.Fatalf("foreign refund changed the transaction: status=%s calls=%d err=%v", retainedStatus, refundCalls, err)
	}
	disabledConfig := cfg
	disabledConfig.StripeEnabled = false
	disabledServer := httptest.NewServer(httpapi.New(disabledConfig, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer disabledServer.Close()
	var disabledOrder marketplace.Order
	response = requestPaymentJSON(t, buyerClient, http.MethodGet, disabledServer.URL+"/api/v1/orders/"+checkout.OrderID.String(), "", nil, &disabledOrder)
	if response.StatusCode != http.StatusOK || disabledOrder.CanRequestRefund || disabledOrder.RefundUnavailableReason != "provider_unavailable" {
		t.Fatalf("disabled original provider advertised refund: status=%d order=%+v", response.StatusCode, disabledOrder)
	}
	var disabledPage marketplace.OrderPage
	response = requestPaymentJSON(t, buyerClient, http.MethodGet, disabledServer.URL+"/api/v1/orders", "", nil, &disabledPage)
	if response.StatusCode != http.StatusOK || len(disabledPage.Items) != 1 || disabledPage.Items[0].CanRequestRefund || disabledPage.Items[0].RefundUnavailableReason != "provider_unavailable" {
		t.Fatalf("list and detail refund capabilities disagree: status=%d page=%+v", response.StatusCode, disabledPage)
	}
	for _, reason := range []string{strings.Repeat("界", 9), strings.Repeat("😀", 501), "A long reason with a NUL\x00"} {
		response = requestPaymentJSON(t, buyerClient, http.MethodPost, server.URL+"/api/v1/orders/"+checkout.OrderID.String()+"/refund", "invalid-unicode-refund", map[string]any{"reason": reason}, nil)
		if response.StatusCode != http.StatusUnprocessableEntity || refundCalls != 0 {
			t.Fatalf("invalid Unicode reason accepted: status=%d calls=%d", response.StatusCode, refundCalls)
		}
	}
	if _, err := pool.Exec(context.Background(), `UPDATE orders SET created_at=now()-interval '8 days' WHERE id=$1`, checkout.OrderID); err != nil {
		t.Fatal(err)
	}
	var expiredOrder marketplace.Order
	response = requestPaymentJSON(t, buyerClient, http.MethodGet, server.URL+"/api/v1/orders/"+checkout.OrderID.String(), "", nil, &expiredOrder)
	if response.StatusCode != http.StatusOK || expiredOrder.CanRequestRefund || expiredOrder.RefundUnavailableReason != "window_expired" {
		t.Fatalf("expired refund advertised: status=%d order=%+v", response.StatusCode, expiredOrder)
	}
	response = requestPaymentJSON(t, buyerClient, http.MethodPost, server.URL+"/api/v1/orders/"+checkout.OrderID.String()+"/refund", "expired-refund-command", map[string]any{"reason": strings.Repeat("界", 10)}, nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("expired refund accepted: %d", response.StatusCode)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE orders SET created_at=$2 WHERE id=$1`, checkout.OrderID, refundOrder.CreatedAt); err != nil {
		t.Fatal(err)
	}
	response = requestPaymentJSON(t, buyerClient, http.MethodPost, server.URL+"/api/v1/orders/"+checkout.OrderID.String()+"/refund", "http-refund-001", map[string]any{"reason": strings.Repeat("😀", 500)}, &refundOrder)
	if response.StatusCode != http.StatusOK || refundOrder.Status != "refund_requested" || refundOrder.PaymentMode != "stripe" || refundOrder.RealCharge || refundCalls != 0 {
		t.Fatalf("Provider refund HTTP mismatch: status=%d order=%#v calls=%d", response.StatusCode, refundOrder, refundCalls)
	}
	var activeEntitlements, localEntries int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM entitlements WHERE order_id=$1 AND status='active'`, checkout.OrderID).Scan(&activeEntitlements); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT (SELECT count(*) FROM billing_entries WHERE user_id IN ($1,$2)) +
		       (SELECT count(*) FROM ledger_entries WHERE account_id IN ($1,$2))`, buyer.ID, seller.ID).Scan(&localEntries); err != nil {
		t.Fatal(err)
	}
	if activeEntitlements != 1 || localEntries != 0 {
		t.Fatalf("refund request changed rights or Local Test ledgers early: entitlements=%d entries=%d", activeEntitlements, localEntries)
	}
	if refundOrder.CanRequestRefund || refundOrder.RefundUnavailableReason != "order_state" {
		t.Fatalf("pending order advertised new refund: %+v", refundOrder)
	}
	assertPurchasePermission(true)
	response = requestPaymentJSON(t, buyerClient, http.MethodPost, server.URL+"/api/v1/orders/"+checkout.OrderID.String()+"/refund", "http-refund-001", map[string]any{"reason": "The licensed workflow did not meet the documented production requirement."}, &refundOrder)
	if response.StatusCode != http.StatusOK || refundOrder.Status != "refund_requested" || refundCalls != 0 {
		t.Fatalf("pending refund replay dispatched synchronously: status=%d order=%#v calls=%d", response.StatusCode, refundOrder, refundCalls)
	}
	refundJob := jobs.Job{Kind: payments.ProductRefundJobKind}
	if err := pool.QueryRow(context.Background(), `SELECT id,payload FROM jobs WHERE kind=$1 AND payload->>'paymentId'=$2`, payments.ProductRefundJobKind, checkout.PaymentID.String()).Scan(&refundJob.ID, &refundJob.Payload); err != nil {
		t.Fatal(err)
	}
	if err := workerPayments.HandleProductRefundJob(context.Background(), refundJob); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM entitlements WHERE order_id=$1 AND status='active'`, checkout.OrderID).Scan(&activeEntitlements); err != nil {
		t.Fatal(err)
	}
	if refundCalls != 1 || activeEntitlements != 1 {
		t.Fatalf("worker dispatch must preserve rights until signed confirmation: calls=%d entitlements=%d", refundCalls, activeEntitlements)
	}

	refundBody := []byte(fmt.Sprintf(`{"id":"evt_http_product_refunded","object":"event","api_version":"2026-02-25.clover","created":%d,"livemode":false,"type":"refund.updated","data":{"object":{"id":"re_http_product","object":"refund","status":"succeeded","amount":1900,"currency":"usd","payment_intent":"pi_http_product","metadata":{"hcai_payment_id":%q,"hcai_resource_id":%q,"hcai_purpose":"product"}}}}`, now, checkout.PaymentID.String(), productID.String()))
	var refundReceipt payments.Receipt
	response = postStripeWebhook(t, server.URL, refundBody, "t="+fmt.Sprint(now)+",v1="+paymentStripeSignature(secret, now, refundBody), "application/json", &refundReceipt)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("signed refund event returned %d", response.StatusCode)
	}
	if err := workerPayments.HandlePaymentEventJob(context.Background(), jobs.Job{Kind: payments.PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, refundReceipt.EventID.String()))}); err != nil {
		t.Fatal(err)
	}
	response = requestPaymentJSON(t, buyerClient, http.MethodGet, server.URL+"/api/v1/orders/"+checkout.OrderID.String(), "", nil, &refundOrder)
	if response.StatusCode != http.StatusOK || refundOrder.Status != "refunded" || refundOrder.RefundedAt == nil {
		t.Fatalf("signed refund did not finalize order: status=%d order=%#v", response.StatusCode, refundOrder)
	}
	assertPurchasePermission(false)

	// A new checkout whose provider response was lost must preserve its command
	// and return a distinct, non-retryable conflict after a merchant change.
	loseCheckoutResponse.Store(true)
	response = requestPaymentJSON(t, buyerClient, http.MethodPost, server.URL+"/api/v1/products/"+productID.String()+"/checkout", "http-request-evidence", map[string]any{"licenseAccepted": true, "offerVersion": product.OfferVersion}, nil)
	if response.StatusCode != http.StatusServiceUnavailable || checkoutCalls != 2 {
		t.Fatalf("lost checkout response: status=%d calls=%d", response.StatusCode, checkoutCalls)
	}
	checkoutMerchant.Store("acct_changed_merchant")
	for _, key := range []string{"http-request-evidence", "http-alternative-key"} {
		var failure struct {
			Error struct {
				Code      string `json:"code"`
				Retryable bool   `json:"retryable"`
			} `json:"error"`
		}
		response = requestPaymentJSON(t, buyerClient, http.MethodPost, server.URL+"/api/v1/products/"+productID.String()+"/checkout", key, map[string]any{"licenseAccepted": true, "offerVersion": product.OfferVersion}, &failure)
		if response.StatusCode != http.StatusConflict || failure.Error.Code != "payment_reconciliation_required" || failure.Error.Retryable || checkoutCalls != 2 {
			t.Fatalf("unsafe HTTP retry: status=%d code=%s calls=%d", response.StatusCode, failure.Error.Code, checkoutCalls)
		}
	}
}

func TestTaskCheckoutHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	const secret = "whsec_task_http_contract"
	var checkoutCalls int
	stripe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/account" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"acct_http_task","object":"account"}`)
			return
		}
		if r.URL.Path == "/v1/balance" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"balance","livemode":false}`)
			return
		}
		if r.URL.Path != "/v1/checkout/sessions" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		checkoutCalls++
		fmt.Fprintf(w, `{"id":"cs_http_task","url":"https://checkout.stripe.com/c/pay/http-task","status":"open","payment_status":"unpaid","expires_at":%d,"livemode":false,"amount_total":%s,"currency":"usd","client_reference_id":%q}`,
			time.Now().Add(time.Hour).Unix(), r.PostForm.Get("line_items[0][price_data][unit_amount]"), r.PostForm.Get("client_reference_id"))
	}))
	defer stripe.Close()
	cfg := config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
		StripeEnabled: true, StripeSecretKey: "sk_test_task_http_contract", StripeWebhookSecret: secret,
		StripeBaseURL: stripe.URL + "/v1", StripeAPIVersion: "2026-02-25.clover", StripeWebhookToleranceSeconds: 300,
	}
	server := httptest.NewServer(httpapi.New(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	commissionerClient, creatorClient := testHTTPClient(t), testHTTPClient(t)
	commissioner := registerGovernanceUser(t, commissionerClient, server.URL, "taskpayclient")
	creator := registerGovernanceUser(t, creatorClient, server.URL, "taskpaycreator")
	taskID, proposalID := uuid.New(), uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO demands(id,client_id,title,brief,deliverable_type,budget_cents,currency,deadline,status,summary,deliverables,acceptance_rules,rights_terms,ai_disclosure_requirement,allow_direct_accept)
		VALUES($1,$2,'Provider-funded launch system','Create a complete launch image system with clear production evidence.','image',80000,'USD',now()+interval '14 days','open','A controlled Provider-funded task.','["master image"]','["no third-party marks"]','Worldwide campaign use.','Disclose models and source media.',true);
		`, taskID, commissioner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO proposals(id,demand_id,creator_id,approach,deliverables,amount_cents,timeline_days,status)
		VALUES($1,$2,$3,'Deliver a controlled image family with recorded provenance.','Master image and production crops.',70000,6,'submitted')`,
		proposalID, taskID, creator.ID); err != nil {
		t.Fatal(err)
	}

	var checkout payments.TaskCheckout
	response := requestPaymentJSON(t, creatorClient, http.MethodPost, server.URL+"/api/v1/tasks/"+taskID.String()+"/checkout", "task-http-forbidden", map[string]any{"proposalId": proposalID}, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("non-commissioner task funding returned %d", response.StatusCode)
	}
	response = requestPaymentJSON(t, commissionerClient, http.MethodPost, server.URL+"/api/v1/tasks/"+taskID.String()+"/checkout", "task-http-checkout-001", map[string]any{"proposalId": proposalID}, &checkout)
	if response.StatusCode != http.StatusCreated || checkout.TaskID != taskID || checkout.ProposalID == nil || *checkout.ProposalID != proposalID || checkout.AmountCents != 70000 || checkout.RealCharge || checkoutCalls != 1 {
		t.Fatalf("task checkout HTTP mismatch: status=%d checkout=%#v calls=%d", response.StatusCode, checkout, checkoutCalls)
	}
	response = requestPaymentJSON(t, commissionerClient, http.MethodPost, server.URL+"/api/v1/tasks/"+taskID.String()+"/checkout", "task-http-checkout-001", map[string]any{"proposalId": proposalID}, &checkout)
	if response.StatusCode != http.StatusOK || !checkout.AlreadyCreated || checkoutCalls != 1 {
		t.Fatalf("task checkout replay mismatch: status=%d checkout=%#v calls=%d", response.StatusCode, checkout, checkoutCalls)
	}

	var commissionerView, creatorView tasks.Detail
	response = requestPaymentJSON(t, commissionerClient, http.MethodGet, server.URL+"/api/v1/tasks/"+taskID.String(), "", nil, &commissionerView)
	if response.StatusCode != http.StatusOK || commissionerView.Funding == nil || commissionerView.Funding.CheckoutURL == nil {
		t.Fatalf("commissioner funding projection mismatch: status=%d funding=%#v", response.StatusCode, commissionerView.Funding)
	}
	response = requestPaymentJSON(t, creatorClient, http.MethodGet, server.URL+"/api/v1/tasks/"+taskID.String(), "", nil, &creatorView)
	if response.StatusCode != http.StatusOK || creatorView.Funding == nil || creatorView.Funding.Status != "checkout_open" || creatorView.Funding.CheckoutURL != nil {
		t.Fatalf("creator funding projection mismatch: status=%d funding=%#v", response.StatusCode, creatorView.Funding)
	}

	now := time.Now().UTC().Unix()
	paidBody := []byte(fmt.Sprintf(`{"id":"evt_http_task_paid","object":"event","api_version":"2026-02-25.clover","created":%d,"livemode":false,"type":"payment_intent.succeeded","data":{"object":{"id":"pi_http_task","object":"payment_intent","status":"succeeded","amount_received":70000,"currency":"usd","latest_charge":"ch_http_task","metadata":{"hcai_payment_id":%q,"hcai_resource_id":%q,"hcai_purpose":"task"}}}}`, now, checkout.PaymentID.String(), taskID.String()))
	var receipt payments.Receipt
	response = postStripeWebhook(t, server.URL, paidBody, "t="+fmt.Sprint(now)+",v1="+paymentStripeSignature(secret, now, paidBody), "application/json", &receipt)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("signed task funding event returned %d", response.StatusCode)
	}
	workerPayments := payments.NewService(pool, payments.ServiceConfig{Enabled: true, LiveMode: false, APIVersion: cfg.StripeAPIVersion, WebhookSecret: secret, WebhookTolerance: 5 * time.Minute})
	if err := workerPayments.HandlePaymentEventJob(context.Background(), jobs.Job{Kind: payments.PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}); err != nil {
		t.Fatal(err)
	}
	commissionerView = tasks.Detail{}
	response = requestPaymentJSON(t, commissionerClient, http.MethodGet, server.URL+"/api/v1/tasks/"+taskID.String(), "", nil, &commissionerView)
	if response.StatusCode != http.StatusOK || commissionerView.Funding == nil || commissionerView.Funding.Status != "paid" || commissionerView.Funding.CheckoutURL != nil {
		t.Fatalf("confirmed task funding HTTP mismatch: status=%d funding=%#v", response.StatusCode, commissionerView.Funding)
	}
}

func postStripeWebhook(t *testing.T, serverURL string, body []byte, signature, contentType string, destination any) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, serverURL+"/api/v1/payments/webhooks/stripe", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Stripe-Signature", signature)
	// This exact callback route bypasses browser-origin checks, but must
	// continue enforcing signed payloads, size, mode and event contracts.
	request.Header.Set("Origin", "https://payment-provider.example.test")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if destination != nil {
		if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
			t.Fatalf("decode Stripe webhook response: %v", err)
		}
	}
	return response
}

func requestPaymentJSON(t *testing.T, client *http.Client, method, target, idempotencyKey string, input, output any) *http.Response {
	t.Helper()
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("User-Agent", "HCAI Payment HTTP Contract Test")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if output != nil {
		if err := json.NewDecoder(response.Body).Decode(output); err != nil {
			t.Fatalf("decode payment response: %v", err)
		}
	} else {
		_, _ = io.Copy(io.Discard, response.Body)
	}
	return response
}

func paymentStripeSignature(secret string, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.", timestamp)
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
