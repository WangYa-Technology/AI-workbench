package payments

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
			_ = json.NewEncoder(w).Encode(map[string]any{"providerId": "RFD_test_123", "providerPaymentId": "PAY_test_123", "amountCents": 1250, "currency": "USD", "status": "pending"})
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
	refund, err := runtime.CreateRefund(context.Background(), RefundRequest{PaymentID: paymentID, OperationID: operationID, ProviderPaymentID: "PAY_test_123", StoreID: "STO_test", AmountCents: 1250, Currency: "USD"})
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

func TestWaffoWebhookFollowsPersistedProviderSelection(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_provider_configs(provider,enabled,environment,merchant_id,store_id,product_id_onetime)
		VALUES('stripe',false,'test','','',''),('waffo_pancake',true,'test','MER_test','STO_test','PROD_test')`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	connector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/webhook/verify" || r.Header.Get("Authorization") != "Bearer connector-test-token" {
			t.Fatalf("unexpected Waffo verification request: path=%s authorization=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"event":{"id":"delivery-123456","timestamp":"` + now + `","eventType":"order.completed","eventId":"PAY_test_123","storeId":"STO_test","mode":"test","data":{"orderId":"ORD_test_123","currency":"USD","orderMetadata":{"hcaiPaymentId":"` + testPaymentID.String() + `","hcaiResourceId":"` + testResourceID.String() + `","hcaiPurpose":"product"},"amount":"12.50","paymentId":"PAY_test_123","paymentStatus":"succeeded"}}}`))
	}))
	defer connector.Close()
	service := NewServiceWithRuntimes(pool, ServiceConfig{
		Enabled: true, Provider: "stripe", WaffoWebhookURL: connector.URL, WaffoConnectorToken: "connector-test-token", WaffoEnvironment: "test",
	}, NewRuntimeCatalog(&waffoAvailabilityRuntime{}))
	receipt, err := service.ReceiveWaffoWebhook(ctx, []byte(`{"signed":"raw"}`), "t=1,v1=signature")
	if err != nil || receipt.ProviderEventID != "delivery-123456" || receipt.Status != "received" {
		t.Fatalf("persisted Waffo provider selection was not honored: receipt=%#v err=%v", receipt, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_provider_configs SET enabled=false WHERE provider='waffo_pancake'`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReceiveWaffoWebhook(ctx, []byte(`{"signed":"raw"}`), "t=1,v1=signature"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled persisted Waffo provider still accepted a webhook: %v", err)
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
