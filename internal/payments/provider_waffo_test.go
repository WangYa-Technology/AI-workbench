package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

type waffoAvailabilityRuntime struct{}

func (*waffoAvailabilityRuntime) Provider() string { return "waffo_pancake" }
func (*waffoAvailabilityRuntime) CreateCheckout(context.Context, CheckoutRequest) (CheckoutSession, error) {
	return CheckoutSession{}, ErrProviderUnavailable
}
func (*waffoAvailabilityRuntime) CreateRefund(context.Context, RefundRequest) (Refund, error) {
	return Refund{}, ErrProviderUnavailable
}
func (*waffoAvailabilityRuntime) CreateTransfer(context.Context, TransferRequest) (Transfer, error) {
	return Transfer{}, ErrProviderUnavailable
}
func (*waffoAvailabilityRuntime) CreateConnectAccount(context.Context, ConnectAccountRequest) (ConnectAccount, error) {
	return ConnectAccount{}, ErrProviderUnavailable
}
func (*waffoAvailabilityRuntime) CreateAccountLink(context.Context, AccountLinkRequest) (AccountLink, error) {
	return AccountLink{}, ErrProviderUnavailable
}

func TestWaffoRuntimeCheckoutAndRefundContract(t *testing.T) {
	paymentID, resourceID, operationID := uuid.New(), uuid.New(), uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer connector-test-token" {
			t.Fatalf("missing connector authorization")
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode connector payload: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/checkout":
			if payload["productId"] != "PROD_test" || payload["amountCents"] != float64(1250) || payload["buyerIdentity"] != "buyer-123" {
				t.Fatalf("unexpected checkout payload: %#v", payload)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"providerId": "CHK_test_123", "checkoutUrl": "https://checkout.waffo.ai/session/CHK_test_123", "status": "open", "paymentStatus": "pending",
				"expiresAt": time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano), "liveMode": false,
			})
		case "/refund":
			if payload["providerPaymentId"] != "PAY_test_123" || payload["currency"] != "USD" || payload["storeId"] != "STO_test" {
				t.Fatalf("unexpected refund payload: %#v", payload)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"providerId": "RFD_test_123", "providerPaymentId": "PAY_test_123", "amountCents": 1250, "currency": "USD", "status": "pending", "paymentIdentity": payload["paymentIdentity"], "operationId": payload["operationId"], "refundContractVersion": payload["refundContractVersion"]})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	runtime := NewWaffoRuntime(WaffoRuntimeConfig{ConnectorURL: server.URL, ConnectorToken: "connector-test-token", Environment: "test", ProductIDOnetime: "PROD_test"})
	checkout, err := runtime.CreateCheckout(context.Background(), CheckoutRequest{PaymentID: paymentID, ResourceID: resourceID, Purpose: "product", AmountCents: 1250, Currency: "USD", SuccessURL: "https://app.example.test/success", CancelURL: "https://app.example.test/cancel", BuyerIdentity: "buyer-123", BuyerEmail: "buyer@example.test", ProductID: "PROD_test", ProductType: "onetime", OrderExternalID: paymentID.String()})
	if err != nil || checkout.ProviderID != "CHK_test_123" || checkout.LiveMode {
		t.Fatalf("checkout contract failed: %#v %v", checkout, err)
	}
	refund, err := runtime.CreateRefund(context.Background(), RefundRequest{PaymentID: paymentID, OperationID: operationID, ProviderPaymentID: "PAY_test_123", StoreID: "STO_test", BuyerIdentity: "buyer-123", PaymentIdentity: &ProductCheckoutIdentity{Provider: "waffo_pancake", MerchantID: "MER_test", StoreID: "STO_test", Endpoint: server.URL, APIVersion: waffoProductCheckoutAPI, RequestVersion: waffoProductCheckoutVersion}, AmountCents: 1250, Currency: "USD"})
	if err != nil || refund.ProviderID != "RFD_test_123" || refund.Status != "pending" {
		t.Fatalf("refund contract failed: %#v %v", refund, err)
	}
	if _, err := runtime.CreateTransfer(context.Background(), TransferRequest{PaymentID: paymentID}); err == nil || !strings.Contains(err.Error(), "payment_provider_unsupported") {
		t.Fatalf("transfer should be unsupported: %v", err)
	}
}

func TestWaffoRuntimeRejectsInsecureRemoteConnector(t *testing.T) {
	runtime := NewWaffoRuntime(WaffoRuntimeConfig{ConnectorURL: "http://payments.example.test:8091", ConnectorToken: "connector-test-token", Environment: "test", ProductIDOnetime: "PROD_test"})
	_, err := runtime.CreateCheckout(context.Background(), CheckoutRequest{PaymentID: uuid.New(), ResourceID: uuid.New(), Purpose: "product", AmountCents: 1250, Currency: "USD", SuccessURL: "https://app.example.test/success", CancelURL: "https://app.example.test/cancel", ProductID: "PROD_test"})
	if err == nil || !strings.Contains(err.Error(), "payment_invalid_request") {
		t.Fatalf("remote HTTP connector should be rejected: %v", err)
	}
}

func TestWaffoRuntimeRejectsCheckoutResponseWithoutLiveMode(t *testing.T) {
	connector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/checkout" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"providerId": "CHK_test_123", "checkoutUrl": "https://checkout.waffo.ai/session/CHK_test_123", "status": "open", "paymentStatus": "pending",
			"expiresAt": time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano),
		})
	}))
	defer connector.Close()
	runtime := NewWaffoRuntime(WaffoRuntimeConfig{ConnectorURL: connector.URL, ConnectorToken: "connector-test-token", Environment: "test", ProductIDOnetime: "PROD_test"})
	_, err := runtime.CreateCheckout(context.Background(), CheckoutRequest{
		PaymentID: uuid.New(), ResourceID: uuid.New(), Purpose: "product", AmountCents: 1250, Currency: "USD",
		SuccessURL: "https://app.example.test/success", CancelURL: "https://app.example.test/cancel", ProductID: "PROD_test",
	})
	if err == nil || !strings.Contains(err.Error(), "payment_response_invalid") {
		t.Fatalf("missing liveMode response was accepted: %v", err)
	}
}

func TestWaffoRuntimeLookupProductCheckoutBindsOriginalOrder(t *testing.T) {
	paymentID, resourceID := uuid.New(), uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/checkout/lookup" || r.Header.Get("Authorization") != "Bearer connector-test-token" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload["orderMerchantExternalId"] != paymentID.String() || payload["storeId"] != "STO_test" {
			t.Fatalf("unexpected lookup payload: %#v %v", payload, err)
		}
		_ = json.NewEncoder(w).Encode(CheckoutLookupResult{
			Outcome: "found", Pages: 1, Scanned: 1, Matches: []string{"ORD_test_123"},
			Observation: &CheckoutObservation{ProviderCheckoutID: "ORD_test_123", ProviderPaymentID: "PAY_test_123", Status: "complete", PaymentStatus: "paid", AmountReceived: 1250, AmountCents: 1250, Currency: "USD", ExpiresAt: time.Now().UTC(), LiveMode: false},
		})
	}))
	defer server.Close()
	runtime := NewWaffoRuntime(WaffoRuntimeConfig{ConnectorURL: server.URL, ConnectorToken: "connector-test-token", Environment: "test", StoreID: "STO_test"})
	result, err := runtime.LookupProductCheckout(context.Background(), CheckoutLookupRequest{
		CheckoutReadRequest: CheckoutReadRequest{PaymentID: paymentID, ResourceID: resourceID, AmountCents: 1250, Currency: "USD", LiveMode: false},
		OrderExternalID:     paymentID.String(), BuyerIdentity: uuid.New().String(), StoreID: "STO_test", CreatedAfter: time.Now().UTC().Add(-time.Hour), CreatedBefore: time.Now().UTC().Add(time.Hour),
	})
	if err != nil || result.Outcome != "found" || result.Observation == nil || result.Observation.ProviderPaymentID != "PAY_test_123" {
		t.Fatalf("lookup contract failed: %#v %v", result, err)
	}
}

func TestResolveProductProviderUsesPersistedActiveProvider(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_provider_configs(provider,enabled,environment,merchant_id,store_id,product_id_onetime)
		VALUES('stripe',false,'test','','',''),('waffo_pancake',true,'test','MER_test','STO_test','PROD_test')`); err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithRuntimes(pool, ServiceConfig{Enabled: true, Provider: "stripe", WaffoEnvironment: "test"}, NewRuntimeCatalog())
	configured, err := service.resolveProductProvider(ctx)
	if err != nil || configured.Provider != "waffo_pancake" || configured.StoreID != "STO_test" || configured.ProductIDOnetime != "PROD_test" {
		t.Fatalf("persisted provider was not selected: %#v %v", configured, err)
	}
}

func TestResolveProductProviderRejectsMerchantMismatch(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_provider_configs(provider,enabled,environment,merchant_id,store_id,product_id_onetime)
		VALUES('waffo_pancake',true,'test','MER_persisted','STO_test','PROD_test')`); err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithRuntimes(pool, ServiceConfig{
		Enabled: true, Provider: "waffo_pancake", WaffoEnvironment: "test", WaffoMerchantID: "MER_deployment",
	}, NewRuntimeCatalog(&waffoAvailabilityRuntime{}))
	if _, err := service.resolveProductProvider(ctx); !errors.Is(err, ErrProviderConfigMismatch) {
		t.Fatalf("expected deployment/persisted merchant mismatch, got %v", err)
	}
}

func TestResolveProductProviderRejectsStripeEnvironmentMismatch(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_provider_configs(provider,enabled,environment)
		VALUES('stripe',true,'prod')`); err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithRuntimes(pool, ServiceConfig{Enabled: true, Provider: "stripe", LiveMode: false}, NewRuntimeCatalog())
	if _, err := service.resolveProductProvider(ctx); !errors.Is(err, ErrProviderConfigMismatch) {
		t.Fatalf("expected Stripe environment mismatch, got %v", err)
	}
}

func TestProductProviderStatusFailsClosedForIncompleteWaffoConfig(t *testing.T) {
	for name, row := range map[string]string{
		"missing store":   `('waffo_pancake',true,'test','MER_test','','PROD_test')`,
		"missing product": `('waffo_pancake',true,'test','MER_test','STO_test','')`,
	} {
		t.Run(name, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			if _, err := pool.Exec(ctx, `INSERT INTO payment_provider_configs(provider,enabled,environment,merchant_id,store_id,product_id_onetime) VALUES`+row); err != nil {
				t.Fatal(err)
			}
			service := NewServiceWithRuntimes(pool, ServiceConfig{Enabled: true, Provider: "waffo_pancake", WaffoEnvironment: "test", WaffoMerchantID: "MER_test"}, NewRuntimeCatalog(&waffoAvailabilityRuntime{}))
			provider, enabled, liveMode := service.ProductProviderStatus(ctx)
			if provider != "waffo_pancake" || enabled || liveMode {
				t.Fatalf("incomplete Waffo configuration was advertised as ready: provider=%s enabled=%t live=%t", provider, enabled, liveMode)
			}
		})
	}
}

func TestMinimizeWaffoEventUsesDeliveryIDAndExactAmount(t *testing.T) {
	paymentID := uuid.New()
	envelope := waffoWebhookEnvelope{
		ID: "delivery-123456", Timestamp: time.Now().UTC().Format(time.RFC3339Nano), EventType: "order.completed", EventID: "order-event-123", StoreID: "STO_test", Mode: "test",
		Data: struct {
			OrderID                        string            `json:"orderId"`
			OrderStatus                    string            `json:"orderStatus"`
			BuyerEmail                     string            `json:"buyerEmail"`
			MerchantProvidedBuyerIdentity  string            `json:"merchantProvidedBuyerIdentity"`
			OrderMerchantExternalID        string            `json:"orderMerchantExternalId"`
			RefundTicketMerchantExternalID string            `json:"refundTicketMerchantExternalId"`
			Currency                       string            `json:"currency"`
			OrderMetadata                  map[string]string `json:"orderMetadata"`
			Amount                         string            `json:"amount"`
			PaymentID                      string            `json:"paymentId"`
			PaymentStatus                  string            `json:"paymentStatus"`
			RefundStatus                   string            `json:"refundStatus"`
		}{OrderID: "order-123456", Currency: "USD", OrderMetadata: map[string]string{"hcaiPaymentId": paymentID.String(), "hcaiPurpose": "product"}, Amount: "12.50", PaymentID: "PAY_test_123", PaymentStatus: "succeeded"},
	}
	event, err := minimizeWaffoEvent(envelope, []byte(`{"event":"test"}`), "test", "STO_test")
	if err != nil || event.ProviderEventID != "delivery-123456" || event.AmountCents == nil || *event.AmountCents != 1250 || event.PaymentID == nil || *event.PaymentID != paymentID {
		t.Fatalf("unexpected minimized Waffo event: %#v %v", event, err)
	}
	envelope.EventType = "subscription.payment_succeeded"
	envelope.Data.OrderMetadata["hcaiPurpose"] = "subscription"
	subscriptionEvent, err := minimizeWaffoEvent(envelope, []byte(`{"event":"subscription"}`), "test", "STO_test")
	if err != nil || !subscriptionEvent.Supported || subscriptionEvent.EventType != "subscription.payment_succeeded" || subscriptionEvent.PaymentStatus == nil || *subscriptionEvent.PaymentStatus != "succeeded" {
		t.Fatalf("subscription payment event was not minimized: %#v %v", subscriptionEvent, err)
	}
	envelope.EventType = "subscription.activated"
	activationEvent, err := minimizeWaffoEvent(envelope, []byte(`{"event":"activation"}`), "test", "STO_test")
	if err != nil || !activationEvent.Supported || activationEvent.EventType != "subscription.activated" || activationEvent.PaymentStatus == nil || *activationEvent.PaymentStatus != "succeeded" {
		t.Fatalf("subscription activation event was not minimized: %#v %v", activationEvent, err)
	}
}

func TestWaffoRefundRejectsUnboundResponses(t *testing.T) {
	cases := map[string]func(map[string]any){
		"old connector":     func(v map[string]any) { delete(v, "refundContractVersion") },
		"wrong operation":   func(v map[string]any) { v["operationId"] = uuid.NewString() },
		"missing operation": func(v map[string]any) { delete(v, "operationId") },
		"wrong payment":     func(v map[string]any) { v["providerPaymentId"] = "PAY_other" },
		"wrong amount":      func(v map[string]any) { v["amountCents"] = 1249 },
		"wrong currency":    func(v map[string]any) { v["currency"] = "EUR" },
		"missing identity":  func(v map[string]any) { delete(v, "paymentIdentity") },
		"changed merchant":  func(v map[string]any) { v["paymentIdentity"].(map[string]any)["merchantId"] = "MER_changed" },
		"unsafe ticket":     func(v map[string]any) { v["providerId"] = "ticket/unsafe" },
		"invented state":    func(v map[string]any) { v["status"] = "complete" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			connector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if body["refundContractVersion"] != waffoRefundContractVersion {
					t.Error("missing outbound contract")
				}
				result := map[string]any{"operationId": body["operationId"], "refundContractVersion": body["refundContractVersion"], "providerId": "TKT_original", "providerPaymentId": "PAY_original", "amountCents": 1250, "currency": "USD", "status": "pending", "paymentIdentity": body["paymentIdentity"]}
				mutate(result)
				_ = json.NewEncoder(w).Encode(result)
			}))
			defer connector.Close()
			runtime := NewWaffoRuntime(WaffoRuntimeConfig{ConnectorURL: connector.URL, ConnectorToken: "connector-test-token", Environment: "test"})
			_, err := runtime.CreateRefund(context.Background(), RefundRequest{PaymentID: uuid.New(), OperationID: uuid.New(), ProviderPaymentID: "PAY_original", AmountCents: 1250, Currency: "USD", BuyerIdentity: "original-buyer", StoreID: "STO_original", PaymentIdentity: &ProductCheckoutIdentity{Provider: "waffo_pancake", MerchantID: "MER_original", StoreID: "STO_original", Endpoint: connector.URL, APIVersion: waffoProductCheckoutAPI, RequestVersion: waffoProductCheckoutVersion}})
			if err == nil || !strings.Contains(err.Error(), "payment_response_invalid") {
				t.Fatalf("unbound response accepted: %v", err)
			}
		})
	}
}

func TestWaffoRuntimeRejectsRefundRedirect(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var redirects, posts atomic.Int32
			connector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/refund" {
					posts.Add(1)
					http.Redirect(w, r, "/again", code)
					return
				}
				redirects.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			defer connector.Close()
			original := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
				t.Error("injected redirect policy should not control financial dispatch")
				return nil
			}}
			runtime := NewWaffoRuntime(WaffoRuntimeConfig{ConnectorURL: connector.URL, ConnectorToken: "connector-test-token", Environment: "test", HTTPClient: original})
			if runtime.client == original {
				t.Fatal("caller-owned HTTP client was mutated")
			}
			_, err := runtime.CreateRefund(context.Background(), RefundRequest{PaymentID: uuid.New(), OperationID: uuid.New(), ProviderPaymentID: "PAY_original", AmountCents: 1250, Currency: "USD", BuyerIdentity: "original-buyer", StoreID: "STO_original", PaymentIdentity: &ProductCheckoutIdentity{Provider: "waffo_pancake", MerchantID: "MER_original", StoreID: "STO_original", Endpoint: connector.URL, APIVersion: waffoProductCheckoutAPI, RequestVersion: waffoProductCheckoutVersion}})
			if err == nil || posts.Load() != 1 || redirects.Load() != 0 {
				t.Fatalf("financial redirect followed: err=%v posts=%d redirects=%d", err, posts.Load(), redirects.Load())
			}
		})
	}
}
