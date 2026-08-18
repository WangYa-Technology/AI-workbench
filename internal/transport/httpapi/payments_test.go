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
	"strings"
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
	body := []byte(fmt.Sprintf(`{"id":"evt_httpcontract","object":"event","api_version":"2026-02-25.clover","created":%d,"livemode":false,"type":"checkout.session.completed","data":{"object":{"id":"cs_httpcontract","object":"checkout.session","status":"complete","payment_status":"paid","amount_total":1900,"currency":"usd","payment_intent":"pi_httpcontract","metadata":{"hcai_payment_id":%q,"hcai_resource_id":%q,"hcai_purpose":"product"}}}}`, now, paymentID.String(), resourceID.String()))

	var receipt payments.Receipt
	response := postStripeWebhook(t, server.URL, body, "t="+fmt.Sprint(now)+",v1="+paymentStripeSignature(secret, now, body), "application/json", &receipt)
	if response.StatusCode != http.StatusAccepted || receipt.Duplicate || receipt.Status != "received" || receipt.ProviderEventID != "evt_httpcontract" {
		t.Fatalf("signed Stripe event was not accepted: status=%d receipt=%#v", response.StatusCode, receipt)
	}
	receipt = payments.Receipt{}
	response = postStripeWebhook(t, server.URL, body, "t="+fmt.Sprint(now)+",v1="+paymentStripeSignature(secret, now, body), "application/json; charset=utf-8", &receipt)
	if response.StatusCode != http.StatusOK || !receipt.Duplicate || receipt.Status != "received" {
		t.Fatalf("duplicate Stripe event was not safely acknowledged: status=%d receipt=%#v", response.StatusCode, receipt)
	}

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
		case "/v1/accounts":
			accountCalls++
			if r.Header.Get("Authorization") != "Bearer sk_test_payout_http_contract" || r.Header.Get("Stripe-Version") != "2026-02-25.clover" ||
				!strings.HasPrefix(r.Header.Get("Idempotency-Key"), "connect-account-") || r.PostForm.Get("type") != "express" ||
				r.PostForm.Get("capabilities[card_payments][requested]") != "true" || r.PostForm.Get("capabilities[transfers][requested]") != "true" ||
				r.PostForm.Get("metadata[hcai_user_id]") == "" || r.PostForm.Get("email") == "" {
				t.Fatalf("unexpected Connect account request: headers=%v form=%v", r.Header, r.PostForm)
			}
			fmt.Fprint(w, `{"id":"acct_http_payout","charges_enabled":false,"payouts_enabled":false,"details_submitted":false,"livemode":false,"requirements":{"currently_due":["individual.first_name"],"past_due":[],"pending_verification":[]}}`)
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
	workerPayments := payments.NewService(pool, payments.ServiceConfig{Enabled: true, LiveMode: false, APIVersion: cfg.StripeAPIVersion, WebhookSecret: secret, WebhookTolerance: 5 * time.Minute})
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
	stripe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/v1/checkout/sessions":
			checkoutCalls++
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
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
		VALUES($1,$2,'image','Payment HTTP Asset',$3,'image/jpeg','clean','demo','hcai-commercial-standard-v1')`,
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
	response := requestPaymentJSON(t, buyerClient, http.MethodPost, server.URL+"/api/v1/products/"+productID.String()+"/checkout", "http-checkout-001", map[string]any{"licenseAccepted": true}, &checkout)
	if response.StatusCode != http.StatusCreated || checkout.PaymentMode != "stripe" || checkout.RealCharge || checkout.LiveMode || checkoutCalls != 1 {
		t.Fatalf("Provider checkout HTTP mismatch: status=%d checkout=%#v calls=%d", response.StatusCode, checkout, checkoutCalls)
	}
	response = requestPaymentJSON(t, buyerClient, http.MethodPost, server.URL+"/api/v1/products/"+productID.String()+"/checkout", "http-checkout-001", map[string]any{"licenseAccepted": true}, &checkout)
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
	workerPayments := payments.NewService(pool, payments.ServiceConfig{Enabled: true, LiveMode: false, APIVersion: cfg.StripeAPIVersion, WebhookSecret: secret, WebhookTolerance: 5 * time.Minute})
	if err := workerPayments.HandlePaymentEventJob(context.Background(), jobs.Job{Kind: payments.PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, paidReceipt.EventID.String()))}); err != nil {
		t.Fatal(err)
	}

	var refundOrder marketplace.Order
	response = requestPaymentJSON(t, buyerClient, http.MethodPost, server.URL+"/api/v1/orders/"+checkout.OrderID.String()+"/refund", "http-refund-001", map[string]any{"reason": "The licensed workflow did not meet the documented production requirement."}, &refundOrder)
	if response.StatusCode != http.StatusOK || refundOrder.Status != "refund_requested" || refundOrder.PaymentMode != "stripe" || refundOrder.RealCharge || refundCalls != 1 {
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
}

func TestTaskCheckoutHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	const secret = "whsec_task_http_contract"
	var checkoutCalls int
	stripe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
