package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestStripeRefundReadBoundary(t *testing.T) {
	for _, scenario := range []string{"pages", "empty", "wrong_mode", "wrong_payment_metadata", "missing_mode", "wrong_refund_payment", "duplicate_page", "partial_error", "first_page_error", "later_wrong_payment", "later_invalid_metadata", "later_invalid_envelope", "unknown_status", "too_many_pages"} {
		t.Run(scenario, func(t *testing.T) {
			payment, resource, operation := uuid.New(), uuid.New(), uuid.New()
			pages := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer test-secret" || r.Header.Get("Idempotency-Key") != "" || r.Header.Get("Stripe-Version") != testStripeAPIVersion {
					t.Errorf("unsafe query: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(400)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/payment_intents/pi_readtest" {
					metadata := map[string]string{"hcai_payment_id": payment.String(), "hcai_resource_id": resource.String(), "hcai_purpose": "product"}
					if scenario == "wrong_payment_metadata" {
						metadata["hcai_payment_id"] = uuid.NewString()
					}
					item := map[string]any{"id": "pi_readtest", "amount": 1900, "amount_received": 1900, "currency": "usd", "livemode": scenario == "wrong_mode", "status": "succeeded", "metadata": metadata}
					if scenario == "missing_mode" {
						delete(item, "livemode")
					}
					_ = json.NewEncoder(w).Encode(item)
					return
				}
				if r.URL.Path != "/refunds" || r.URL.Query().Get("payment_intent") != "pi_readtest" || r.URL.Query().Get("limit") != "100" {
					t.Error("wrong refund scope")
					w.WriteHeader(400)
					return
				}
				pages++
				if pages > 1 && r.URL.Query().Get("starting_after") == "" {
					t.Error("missing pagination cursor")
				}
				if scenario == "first_page_error" || scenario == "partial_error" && pages > 1 {
					http.Error(w, "private provider detail", 503)
					return
				}
				list := []map[string]any{}
				id := "re_firstread"
				more := pages == 1
				if pages > 1 {
					id = "re_secondread"
				}
				if scenario == "duplicate_page" {
					id = "re_firstread"
				}
				if scenario == "too_many_pages" {
					id = "re_readpage" + strings.Repeat("x", pages)
					more = true
				}
				if scenario == "empty" {
					more = false
				} else {
					item := map[string]any{"id": id, "payment_intent": "pi_readtest", "amount": 1900, "currency": "usd", "status": "succeeded", "metadata": map[string]string{"hcai_payment_id": payment.String(), "hcai_refund_operation_id": operation.String()}}
					if scenario == "wrong_refund_payment" || scenario == "later_wrong_payment" && pages > 1 {
						item["payment_intent"] = "pi_unrelated"
					}
					if scenario == "unknown_status" {
						item["status"] = "custom_unknown"
					}
					if scenario == "later_invalid_metadata" && pages > 1 {
						item["metadata"] = map[string]string{"hcai_payment_id": uuid.NewString()}
					}
					list = append(list, item)
				}
				envelope := map[string]any{"object": "list", "has_more": more, "data": list}
				if scenario == "later_invalid_envelope" && pages > 1 {
					delete(envelope, "has_more")
				}
				_ = json.NewEncoder(w).Encode(envelope)
			}))
			defer server.Close()
			runtime := NewStripeRuntime(StripeRuntimeConfig{BaseURL: server.URL, SecretKey: "test-secret", APIVersion: testStripeAPIVersion, HTTPClient: server.Client()})
			result, err := runtime.ReadProductRefunds(context.Background(), RefundReadRequest{PaymentID: payment, ResourceID: resource, ProviderPaymentID: "pi_readtest", AmountCents: 1900, Currency: "USD"})
			if scenario == "pages" {
				if err != nil || len(result) != 2 || pages != 2 || result[0].OperationID == nil || *result[0].OperationID != operation {
					t.Fatalf("query result: %#v %v", result, err)
				}
			} else if scenario == "empty" {
				if err != nil || len(result) != 0 {
					t.Fatalf("empty result: %#v %v", result, err)
				}
			} else {
				expected := 0
				switch scenario {
				case "duplicate_page", "partial_error", "later_wrong_payment", "later_invalid_metadata", "later_invalid_envelope":
					expected = 1
				case "too_many_pages":
					expected = 10
				}
				if err == nil || len(result) != expected || strings.Contains(err.Error(), "private") {
					t.Fatalf("invalid result exposed: %#v %v", result, err)
				}
			}
			if pages > 10 {
				t.Fatal("unbounded pagination")
			}
		})
	}
}
