package httpapi_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestExternalBillingCheckoutHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	const secret = "whsec_billing_http_contract"
	const version = "2026-02-25.clover"
	checkoutCalls := 0
	stripe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk_test_billing_http_contract" || r.Header.Get("Stripe-Version") != version {
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/account" {
			fmt.Fprint(w, `{"id":"acct_billing_http","object":"account"}`)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/balance" {
			fmt.Fprint(w, `{"object":"balance","livemode":false}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/checkout/sessions" || r.ParseForm() != nil {
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		amount := r.PostForm.Get("line_items[0][price_data][unit_amount]")
		if r.PostForm.Get("mode") != "payment" || r.PostForm.Get("line_items[0][price_data][currency]") != "usd" || r.PostForm.Get("client_reference_id") == "" || amount == "" {
			http.Error(w, "invalid checkout request", http.StatusUnprocessableEntity)
			return
		}
		checkoutCalls++
		checkoutID := fmt.Sprintf("cs_http_billing_%d", checkoutCalls)
		fmt.Fprintf(w, `{"id":%q,"url":%q,"status":"open","payment_status":"unpaid","expires_at":%d,"livemode":false,"amount_total":%s,"currency":"usd","client_reference_id":%q}`,
			checkoutID, "https://checkout.stripe.com/c/pay/"+checkoutID, time.Now().Add(time.Hour).Unix(), amount, r.PostForm.Get("client_reference_id"))
	}))
	defer stripe.Close()
	cfg := config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "https://app.example.test", LocalProviderEnabled: true,
		StripeEnabled: true, StripeSecretKey: "sk_test_billing_http_contract", StripeWebhookSecret: secret,
		StripeBaseURL: stripe.URL + "/v1", StripeAPIVersion: version, StripeWebhookToleranceSeconds: 300,
	}
	server := httptest.NewServer(httpapi.New(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	anonymous := testHTTPClient(t)
	if response := requestPaymentJSON(t, anonymous, http.MethodPost, server.URL+"/api/v1/billing/topups/checkout", "billing-anonymous-topup", map[string]any{"amountCents": 4200}, nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous top-up returned %d", response.StatusCode)
	}
	if response := requestPaymentJSON(t, anonymous, http.MethodPost, server.URL+"/api/v1/billing/subscriptions/checkout", "billing-anonymous-subscription", map[string]any{"planId": uuid.New()}, nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous subscription checkout returned %d", response.StatusCode)
	}

	ownerClient := testHTTPClient(t)
	owner := registerGovernanceUser(t, ownerClient, server.URL, "billing_http_owner")
	if response := requestPaymentJSON(t, ownerClient, http.MethodPost, server.URL+"/api/v1/billing/subscriptions", "billing-direct-subscription", map[string]any{"planId": uuid.New()}, nil); response.StatusCode != http.StatusNotFound {
		t.Fatalf("legacy direct subscription route returned %d", response.StatusCode)
	}
	var beforeStatement billing.Statement
	if response := requestPaymentJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/billing/statement", "", nil, &beforeStatement); response.StatusCode != http.StatusOK {
		t.Fatalf("initial billing statement returned %d", response.StatusCode)
	}
	var beforePoints billing.PointOverview
	if response := requestPaymentJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/billing/points", "", nil, &beforePoints); response.StatusCode != http.StatusOK || len(beforePoints.Plans) < 3 {
		t.Fatalf("initial point overview mismatch: status=%d points=%#v", response.StatusCode, beforePoints)
	}
	var creatorPlan, studioPlan billing.SubscriptionPlan
	for _, plan := range beforePoints.Plans {
		if plan.TierCode == "creator" {
			creatorPlan = plan
		}
		if plan.TierCode == "studio" {
			studioPlan = plan
		}
	}
	if creatorPlan.ID == uuid.Nil || studioPlan.ID == uuid.Nil {
		t.Fatalf("seeded paid subscription plans are missing: %#v", beforePoints.Plans)
	}

	for _, test := range []struct {
		name string
		path string
		key  string
		body map[string]any
	}{
		{name: "amount", path: "/api/v1/billing/topups/checkout", key: "billing-invalid-amount", body: map[string]any{"amountCents": 49}},
		{name: "missing key", path: "/api/v1/billing/topups/checkout", body: map[string]any{"amountCents": 4200}},
		{name: "plan", path: "/api/v1/billing/subscriptions/checkout", key: "billing-invalid-plan", body: map[string]any{"planId": uuid.New()}},
	} {
		response := requestPaymentJSON(t, ownerClient, http.MethodPost, server.URL+test.path, test.key, test.body, nil)
		if response.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("invalid %s request returned %d", test.name, response.StatusCode)
		}
	}

	disabledServer := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "https://app.example.test", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer disabledServer.Close()
	if response := requestPaymentJSON(t, ownerClient, http.MethodPost, disabledServer.URL+"/api/v1/billing/topups/checkout", "billing-disabled-topup", map[string]any{"amountCents": 4200}, nil); response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("disabled top-up returned %d", response.StatusCode)
	}

	var topup payments.BillingCheckout
	response := requestPaymentJSON(t, ownerClient, http.MethodPost, server.URL+"/api/v1/billing/topups/checkout", "billing-topup-001", map[string]any{"amountCents": 4200}, &topup)
	if response.StatusCode != http.StatusCreated || topup.Purpose != "wallet_topup" || topup.AmountCents != 4200 || topup.PaymentMode != "stripe" || topup.RealCharge || topup.LiveMode || !strings.HasPrefix(topup.CheckoutURL, "https://checkout.stripe.com/") || checkoutCalls != 1 {
		t.Fatalf("top-up checkout mismatch: status=%d checkout=%#v calls=%d", response.StatusCode, topup, checkoutCalls)
	}
	var topupReplay payments.BillingCheckout
	if _, err := pool.Exec(t.Context(), `UPDATE wallet_topup_settings SET minimum_amount_cents=5000,preset_amounts_cents=ARRAY[5000,10000],version=version+1`); err != nil {
		t.Fatal(err)
	}
	var minimumError struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if rejected := requestPaymentJSON(t, ownerClient, http.MethodPost, server.URL+"/api/v1/billing/topups/checkout", "billing-new-below-minimum", map[string]any{"amountCents": 4200}, &minimumError); rejected.StatusCode != 422 || minimumError.Error.Code != "wallet_topup_amount_out_of_range" || checkoutCalls != 1 {
		t.Fatalf("minimum status=%d error=%+v calls=%d", rejected.StatusCode, minimumError, checkoutCalls)
	}
	response = requestPaymentJSON(t, ownerClient, http.MethodPost, server.URL+"/api/v1/billing/topups/checkout", "billing-topup-001", map[string]any{"amountCents": 4200}, &topupReplay)
	if response.StatusCode != http.StatusOK || !topupReplay.AlreadyCreated || topupReplay.PaymentID != topup.PaymentID || checkoutCalls != 1 {
		t.Fatalf("top-up replay mismatch: status=%d checkout=%#v calls=%d", response.StatusCode, topupReplay, checkoutCalls)
	}
	if response = requestPaymentJSON(t, ownerClient, http.MethodPost, server.URL+"/api/v1/billing/topups/checkout", "billing-topup-001", map[string]any{"amountCents": 4300}, nil); response.StatusCode != http.StatusConflict {
		t.Fatalf("top-up idempotency conflict returned %d", response.StatusCode)
	}

	now := time.Now().UTC().Unix()
	topupBody := []byte(fmt.Sprintf(`{"id":"evt_http_billing_topup","object":"event","api_version":%q,"created":%d,"livemode":false,"type":"checkout.session.completed","data":{"object":{"id":"cs_http_billing_1","object":"checkout.session","status":"complete","payment_status":"paid","amount_total":4200,"currency":"usd","payment_intent":"pi_http_billing_topup","metadata":{"hcai_payment_id":%q,"hcai_resource_id":%q,"hcai_purpose":"wallet_topup"}}}}`, version, now, topup.PaymentID, owner.ID))
	var topupReceipt payments.Receipt
	response = postStripeWebhook(t, server.URL, topupBody, "t="+fmt.Sprint(now)+",v1="+paymentStripeSignature(secret, now, topupBody), "application/json", &topupReceipt)
	if response.StatusCode != http.StatusAccepted || topupReceipt.Duplicate {
		t.Fatalf("top-up webhook mismatch: status=%d receipt=%#v", response.StatusCode, topupReceipt)
	}
	workerPayments := payments.NewService(pool, payments.ServiceConfig{Enabled: true, LiveMode: false, APIVersion: version, WebhookSecret: secret, WebhookTolerance: 5 * time.Minute})
	if err := workerPayments.HandlePaymentEventJob(context.Background(), jobs.Job{Kind: payments.PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, topupReceipt.EventID))}); err != nil {
		t.Fatal(err)
	}
	var topupDuplicate payments.Receipt
	response = postStripeWebhook(t, server.URL, topupBody, "t="+fmt.Sprint(now)+",v1="+paymentStripeSignature(secret, now, topupBody), "application/json", &topupDuplicate)
	if response.StatusCode != http.StatusOK || !topupDuplicate.Duplicate {
		t.Fatalf("top-up webhook replay mismatch: status=%d receipt=%#v", response.StatusCode, topupDuplicate)
	}
	var afterTopup billing.Statement
	if response = requestPaymentJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/billing/statement", "", nil, &afterTopup); response.StatusCode != http.StatusOK || afterTopup.Account.BalanceCents != beforeStatement.Account.BalanceCents+4200 {
		t.Fatalf("top-up balance mismatch: status=%d before=%d after=%d", response.StatusCode, beforeStatement.Account.BalanceCents, afterTopup.Account.BalanceCents)
	}
	var topupEntryCount int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM billing_entries WHERE user_id=$1 AND operation_id=$2 AND entry_type='wallet_topup'`, owner.ID, topup.PaymentID).Scan(&topupEntryCount); err != nil || topupEntryCount != 1 {
		t.Fatalf("top-up ledger idempotency mismatch: count=%d err=%v", topupEntryCount, err)
	}

	var subscription payments.BillingCheckout
	response = requestPaymentJSON(t, ownerClient, http.MethodPost, server.URL+"/api/v1/billing/subscriptions/checkout", "billing-subscription-001", map[string]any{"planId": creatorPlan.ID}, &subscription)
	if response.StatusCode != http.StatusCreated || subscription.Purpose != "subscription" || subscription.ResourceID != creatorPlan.ID || subscription.AmountCents != creatorPlan.PriceCents || subscription.PaymentMode != "stripe" || checkoutCalls != 2 {
		t.Fatalf("subscription checkout mismatch: status=%d checkout=%#v calls=%d", response.StatusCode, subscription, checkoutCalls)
	}
	var subscriptionReplay payments.BillingCheckout
	response = requestPaymentJSON(t, ownerClient, http.MethodPost, server.URL+"/api/v1/billing/subscriptions/checkout", "billing-subscription-001", map[string]any{"planId": creatorPlan.ID}, &subscriptionReplay)
	if response.StatusCode != http.StatusOK || !subscriptionReplay.AlreadyCreated || subscriptionReplay.PaymentID != subscription.PaymentID || checkoutCalls != 2 {
		t.Fatalf("subscription replay mismatch: status=%d checkout=%#v calls=%d", response.StatusCode, subscriptionReplay, checkoutCalls)
	}
	if response = requestPaymentJSON(t, ownerClient, http.MethodPost, server.URL+"/api/v1/billing/subscriptions/checkout", "billing-subscription-001", map[string]any{"planId": studioPlan.ID}, nil); response.StatusCode != http.StatusConflict {
		t.Fatalf("subscription idempotency conflict returned %d", response.StatusCode)
	}

	subscriptionBody := []byte(fmt.Sprintf(`{"id":"evt_http_billing_subscription","object":"event","api_version":%q,"created":%d,"livemode":false,"type":"checkout.session.completed","data":{"object":{"id":"cs_http_billing_2","object":"checkout.session","status":"complete","payment_status":"paid","amount_total":%d,"currency":"usd","payment_intent":"pi_http_billing_subscription","metadata":{"hcai_payment_id":%q,"hcai_resource_id":%q,"hcai_purpose":"subscription"}}}}`, version, now+1, creatorPlan.PriceCents, subscription.PaymentID, creatorPlan.ID))
	var subscriptionReceipt payments.Receipt
	response = postStripeWebhook(t, server.URL, subscriptionBody, "t="+fmt.Sprint(now+1)+",v1="+paymentStripeSignature(secret, now+1, subscriptionBody), "application/json", &subscriptionReceipt)
	if response.StatusCode != http.StatusAccepted || subscriptionReceipt.Duplicate {
		t.Fatalf("subscription webhook mismatch: status=%d receipt=%#v", response.StatusCode, subscriptionReceipt)
	}
	if err := workerPayments.HandlePaymentEventJob(context.Background(), jobs.Job{Kind: payments.PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, subscriptionReceipt.EventID))}); err != nil {
		t.Fatal(err)
	}
	var subscriptionDuplicate payments.Receipt
	response = postStripeWebhook(t, server.URL, subscriptionBody, "t="+fmt.Sprint(now+1)+",v1="+paymentStripeSignature(secret, now+1, subscriptionBody), "application/json", &subscriptionDuplicate)
	if response.StatusCode != http.StatusOK || !subscriptionDuplicate.Duplicate {
		t.Fatalf("subscription webhook replay mismatch: status=%d receipt=%#v", response.StatusCode, subscriptionDuplicate)
	}
	var afterPoints billing.PointOverview
	if response = requestPaymentJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/billing/points", "", nil, &afterPoints); response.StatusCode != http.StatusOK || afterPoints.Account.BalancePoints != beforePoints.Account.BalancePoints+creatorPlan.IncludedPoints || afterPoints.CurrentSubscription == nil || afterPoints.CurrentSubscription.TierCode != "creator" {
		t.Fatalf("subscription fulfillment mismatch: status=%d points=%#v", response.StatusCode, afterPoints)
	}
	var pointEntryCount, subscriptionCount int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM point_entries WHERE user_id=$1 AND operation_id=$2 AND entry_type='subscription_credit'`, owner.ID, subscription.PaymentID).Scan(&pointEntryCount); err != nil || pointEntryCount != 1 {
		t.Fatalf("subscription points idempotency mismatch: count=%d err=%v", pointEntryCount, err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM user_subscriptions WHERE user_id=$1 AND purchase_operation_id=$2`, owner.ID, subscription.PaymentID).Scan(&subscriptionCount); err != nil || subscriptionCount != 1 {
		t.Fatalf("subscription record idempotency mismatch: count=%d err=%v", subscriptionCount, err)
	}
}
