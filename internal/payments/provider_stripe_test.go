package payments

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	testPaymentID  = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	testResourceID = uuid.MustParse("22222222-2222-4222-8222-222222222222")
)

func TestStripeCreateCheckoutContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/checkout/sessions" {
			t.Fatalf("unexpected request target: %s %s", r.Method, r.URL.Path)
		}
		assertStripeHeaders(t, r, "checkout-"+testPaymentID.String())
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse checkout form: %v", err)
		}
		want := map[string]string{
			"mode": "payment", "client_reference_id": testPaymentID.String(),
			"line_items[0][price_data][currency]":           "usd",
			"line_items[0][price_data][product_data][name]": "Production workflow",
			"line_items[0][price_data][unit_amount]":        "1250", "line_items[0][quantity]": "1",
			"payment_intent_data[transfer_group]":             transferGroup(testPaymentID),
			"payment_intent_data[metadata][hcai_payment_id]":  testPaymentID.String(),
			"payment_intent_data[metadata][hcai_resource_id]": testResourceID.String(),
			"payment_intent_data[metadata][hcai_purpose]":     "product",
			"metadata[hcai_payment_id]":                       testPaymentID.String(),
			"metadata[hcai_resource_id]":                      testResourceID.String(),
			"metadata[hcai_purpose]":                          "product",
			"success_url":                                     "https://app.example.com/orders/success",
			"cancel_url":                                      "https://app.example.com/orders/cancel",
		}
		for key, value := range want {
			if got := r.PostForm.Get(key); got != value {
				t.Errorf("form %s=%q, want %q", key, got, value)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"cs_test_contract","url":"https://checkout.stripe.com/c/pay/test","status":"open","payment_status":"unpaid","expires_at":1893456000,"livemode":false,"amount_total":1250,"currency":"usd","client_reference_id":%q}`, testPaymentID.String())
	}))
	defer server.Close()

	runtime := testStripeRuntime(server)
	session, err := runtime.CreateCheckout(context.Background(), CheckoutRequest{
		PaymentID: testPaymentID, ResourceID: testResourceID, Purpose: " Product ", Name: " Production workflow ",
		AmountCents: 1250, Currency: " USD ", SuccessURL: "https://app.example.com/orders/success", CancelURL: "https://app.example.com/orders/cancel",
	})
	if err != nil {
		t.Fatalf("create checkout: %v", err)
	}
	if session.ProviderID != "cs_test_contract" || session.Status != "open" || session.PaymentStatus != "unpaid" || session.LiveMode {
		t.Fatalf("unexpected checkout projection: %#v", session)
	}
}

func TestStripeRefundAndTransferContracts(t *testing.T) {
	t.Run("legacy refund parameters", func(t *testing.T) {
		operationID := uuid.New()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assertStripeHeaders(t, r, "refund-"+operationID.String())
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.PostForm.Has("metadata[hcai_refund_operation_id]") {
				t.Fatal("retry changed the parameters of a legacy idempotency key")
			}
			fmt.Fprint(w, `{"id":"re_legacy123","payment_intent":"pi_contract123","amount":625,"currency":"usd","status":"pending"}`)
		}))
		defer server.Close()
		if _, err := testStripeRuntime(server).CreateRefund(context.Background(), RefundRequest{PaymentID: testPaymentID, OperationID: operationID, ProviderPaymentID: "pi_contract123", AmountCents: 625}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("refund", func(t *testing.T) {
		operationID := uuid.New()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assertStripeHeaders(t, r, "refund-"+operationID.String())
			if r.URL.Path != "/v1/refunds" {
				t.Fatalf("unexpected refund path: %s", r.URL.Path)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.PostForm.Get("payment_intent") != "pi_contract123" || r.PostForm.Get("amount") != "625" || r.PostForm.Get("reason") != "requested_by_customer" {
				t.Fatalf("unexpected refund form: %v", r.PostForm)
			}
			if r.PostForm.Get("metadata[hcai_refund_operation_id]") != operationID.String() || r.PostForm.Get("metadata[hcai_payment_id]") != testPaymentID.String() {
				t.Fatal("refund request omitted durable correlation metadata")
			}
			fmt.Fprint(w, `{"id":"re_contract123","payment_intent":"pi_contract123","amount":625,"currency":"usd","status":"succeeded"}`)
		}))
		defer server.Close()
		refund, err := testStripeRuntime(server).CreateRefund(context.Background(), RefundRequest{PaymentID: testPaymentID, OperationID: operationID, IncludeOperationMetadata: true, ProviderPaymentID: "pi_contract123", AmountCents: 625})
		if err != nil || refund.ProviderID != "re_contract123" || refund.Currency != "USD" {
			t.Fatalf("unexpected refund: %#v err=%v", refund, err)
		}
	})

	t.Run("transfer", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assertStripeHeaders(t, r, "transfer-"+testPaymentID.String())
			if r.URL.Path != "/v1/transfers" {
				t.Fatalf("unexpected transfer path: %s", r.URL.Path)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.PostForm.Get("source_transaction") != "ch_contract123" || r.PostForm.Get("destination") != "acct_contract123" || r.PostForm.Get("amount") != "1000" || r.PostForm.Get("transfer_group") != transferGroup(testPaymentID) {
				t.Fatalf("unexpected transfer form: %v", r.PostForm)
			}
			fmt.Fprintf(w, `{"id":"tr_contract123","object":"transfer","destination":"acct_contract123","source_transaction":"ch_contract123","amount":1000,"currency":"usd","transfer_group":%q,"livemode":false,"created":%d,"reversed":false,"amount_reversed":0,"metadata":{"hcai_payment_id":%q}}`, transferGroup(testPaymentID), time.Now().Unix(), testPaymentID.String())
		}))
		defer server.Close()
		transfer, err := testStripeRuntime(server).CreateTransfer(context.Background(), TransferRequest{PaymentID: testPaymentID, ProviderChargeID: "ch_contract123", DestinationID: "acct_contract123", AmountCents: 1000, Currency: "USD"})
		if err != nil || transfer.ProviderID != "tr_contract123" || transfer.Currency != "USD" {
			t.Fatalf("unexpected transfer: %#v err=%v", transfer, err)
		}
	})
}

func TestStripeConnectAccountAndOnboardingLinkContracts(t *testing.T) {
	userID := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	expiresAt := time.Now().Add(5 * time.Minute).UTC().Unix()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/v1/balance":
			fmt.Fprint(w, `{"object":"balance","livemode":false}`)
		case "/v1/accounts":
			assertStripeHeaders(t, r, "connect-account-"+userID.String())
			if r.PostForm.Get("type") != "express" || r.PostForm.Get("capabilities[card_payments][requested]") != "true" || r.PostForm.Get("capabilities[transfers][requested]") != "true" || r.PostForm.Get("metadata[hcai_user_id]") != userID.String() || r.PostForm.Get("email") != "creator@example.test" {
				t.Fatalf("unexpected Connect account form: %v", r.PostForm)
			}
			fmt.Fprintf(w, `{"id":"acct_connect_contract","object":"account","type":"express","metadata":{"hcai_user_id":%q},"charges_enabled":false,"payouts_enabled":false,"details_submitted":false,"requirements":{"currently_due":["individual.first_name"],"past_due":[],"pending_verification":[]}}`, userID.String())
		case "/v1/account_links":
			if !strings.HasPrefix(r.Header.Get("Idempotency-Key"), "connect-link-") || r.Header.Get("Authorization") != "Bearer sk_test_contract" || r.Header.Get("Stripe-Version") != "2026-02-25.clover" {
				t.Fatalf("unexpected Account Link headers: %v", r.Header)
			}
			if r.PostForm.Get("account") != "acct_connect_contract" || r.PostForm.Get("type") != "account_onboarding" || r.PostForm.Get("refresh_url") != "https://app.example.com/settings?section=payouts&connect=refresh" || r.PostForm.Get("return_url") != "https://app.example.com/settings?section=payouts&connect=return" {
				t.Fatalf("unexpected Account Link form: %v", r.PostForm)
			}
			fmt.Fprintf(w, `{"object":"account_link","url":"https://connect.stripe.com/setup/c/test-contract","expires_at":%d}`, expiresAt)
		default:
			t.Fatalf("unexpected Connect target: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	runtime := testStripeRuntime(server)
	account, err := runtime.CreateConnectAccount(context.Background(), ConnectAccountRequest{UserID: userID, Email: "creator@example.test"})
	if err != nil || account.ID != "acct_connect_contract" || account.DetailsSubmitted || !account.RequirementsDue || account.LiveMode {
		t.Fatalf("unexpected Connect account projection: %#v err=%v", account, err)
	}
	link, err := runtime.CreateAccountLink(context.Background(), AccountLinkRequest{
		DestinationID: account.ID,
		RefreshURL:    "https://app.example.com/settings?section=payouts&connect=refresh",
		ReturnURL:     "https://app.example.com/settings?section=payouts&connect=return",
	})
	if err != nil || link.URL != "https://connect.stripe.com/setup/c/test-contract" || link.ExpiresAt.Unix() != expiresAt {
		t.Fatalf("unexpected Account Link projection: %#v err=%v", link, err)
	}
}

func TestStripeFailureClassificationAndBounds(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		retryable bool
		code      string
	}{
		{name: "authentication", status: http.StatusUnauthorized, code: "payment_authentication"},
		{name: "invalid", status: http.StatusBadRequest, code: "payment_invalid_request"},
		{name: "rate_limit", status: http.StatusTooManyRequests, code: "payment_rate_limited", retryable: true},
		{name: "unavailable", status: http.StatusServiceUnavailable, code: "payment_provider_unavailable", retryable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "12")
				w.WriteHeader(test.status)
				fmt.Fprint(w, `{"error":{"message":"upstream secret detail"}}`)
			}))
			defer server.Close()
			_, err := testStripeRuntime(server).CreateCheckout(context.Background(), checkoutInput())
			assertProviderFailure(t, err, test.code, test.retryable)
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("upstream body leaked: %v", err)
			}
		})
	}

	t.Run("malformed success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{`) }))
		defer server.Close()
		_, err := testStripeRuntime(server).CreateCheckout(context.Background(), checkoutInput())
		assertProviderFailure(t, err, "payment_response_invalid", false)
	})

	t.Run("oversized success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, strings.Repeat("x", maxStripeResponseBytes+1))
		}))
		defer server.Close()
		_, err := testStripeRuntime(server).CreateCheckout(context.Background(), checkoutInput())
		assertProviderFailure(t, err, "payment_response_invalid", false)
	})

	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(50 * time.Millisecond)
			fmt.Fprint(w, `{}`)
		}))
		defer server.Close()
		runtime := testStripeRuntime(server)
		runtime.client = &http.Client{Timeout: 10 * time.Millisecond}
		_, err := runtime.CreateCheckout(context.Background(), checkoutInput())
		assertProviderFailure(t, err, "payment_timeout", true)
	})
}

func testStripeRuntime(server *httptest.Server) *StripeRuntime {
	return NewStripeRuntime(StripeRuntimeConfig{SecretKey: "sk_test_contract", BaseURL: server.URL + "/v1", APIVersion: "2026-02-25.clover", HTTPClient: server.Client()})
}

func checkoutInput() CheckoutRequest {
	return CheckoutRequest{PaymentID: testPaymentID, ResourceID: testResourceID, Purpose: "product", Name: "Production workflow", AmountCents: 1250, Currency: "usd", SuccessURL: "https://app.example.com/orders/success", CancelURL: "https://app.example.com/orders/cancel"}
}

func assertStripeHeaders(t *testing.T, r *http.Request, idempotencyKey string) {
	t.Helper()
	if r.Header.Get("Authorization") != "Bearer sk_test_contract" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.Header.Get("Stripe-Version") != "2026-02-25.clover" || r.Header.Get("Idempotency-Key") != idempotencyKey {
		t.Fatalf("unexpected Stripe headers: %v", r.Header)
	}
}

func assertProviderFailure(t *testing.T, err error, code string, retryable bool) {
	t.Helper()
	if err == nil || err.Error() != code {
		t.Fatalf("provider error=%v, want %s", err, code)
	}
	var classified interface{ Retryable() bool }
	if !errors.As(err, &classified) || classified.Retryable() != retryable {
		t.Fatalf("provider retry classification mismatch: %v", err)
	}
}
