package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStripeProductCheckoutRead(t *testing.T) {
	for _, scenario := range []string{"expired", "expired_intent", "paid", "pending", "open", "wrong_session", "wrong_reference", "wrong_metadata", "wrong_amount", "missing_mode", "wrong_mode", "wrong_purpose", "wrong_intent_metadata", "missing_received", "paid_without_charge", "partial_paid", "crossed_reads", "remote_error", "oversize", "live_expired"} {
		t.Run(scenario, func(t *testing.T) {
			input := CheckoutReadRequest{PaymentID: uuid.New(), ResourceID: uuid.New(), ProviderCheckoutID: "cs_checkoutread", AmountCents: 1900, Currency: "USD", LiveMode: scenario == "live_expired"}
			metadata := func() map[string]string {
				return map[string]string{"hcai_payment_id": input.PaymentID.String(), "hcai_resource_id": input.ResourceID.String(), "hcai_purpose": "product"}
			}
			session := map[string]any{"id": input.ProviderCheckoutID, "object": "checkout.session", "mode": "payment", "status": "expired", "payment_status": "unpaid", "payment_intent": nil, "amount_total": 1900, "currency": "usd", "livemode": input.LiveMode, "expires_at": time.Now().Add(-time.Minute).Unix(), "client_reference_id": input.PaymentID.String(), "metadata": metadata()}
			intent := map[string]any{"id": "pi_checkoutread", "object": "payment_intent", "status": "canceled", "amount": 1900, "amount_received": 0, "currency": "usd", "livemode": input.LiveMode, "latest_charge": nil, "metadata": metadata()}
			wantError := true
			switch scenario {
			case "expired", "live_expired":
				wantError = false
			case "expired_intent":
				session["payment_intent"] = "pi_checkoutread"
				wantError = false
			case "paid", "partial_paid", "paid_without_charge":
				session["status"], session["payment_status"], session["payment_intent"] = "complete", "paid", "pi_checkoutread"
				intent["status"], intent["amount_received"], intent["latest_charge"] = "succeeded", 1900, "ch_checkoutread"
				if scenario == "paid" {
					wantError = false
				}
				if scenario == "partial_paid" {
					intent["amount_received"] = 1800
				}
				if scenario == "paid_without_charge" {
					intent["latest_charge"] = nil
				}
			case "pending":
				session["status"], session["payment_intent"], intent["status"] = "complete", "pi_checkoutread", "processing"
				wantError = false
			case "open":
				session["status"] = "open"
				wantError = false
			case "wrong_session":
				session["id"] = "cs_unrelated"
			case "wrong_reference":
				session["client_reference_id"] = uuid.NewString()
			case "wrong_metadata":
				session["metadata"].(map[string]string)["hcai_resource_id"] = uuid.NewString()
			case "wrong_amount":
				session["amount_total"] = 1
			case "missing_mode":
				delete(session, "livemode")
			case "wrong_mode":
				session["livemode"] = true
			case "wrong_purpose":
				session["mode"] = "subscription"
			case "wrong_intent_metadata":
				session["payment_intent"] = "pi_checkoutread"
				intent["metadata"].(map[string]string)["hcai_payment_id"] = uuid.NewString()
			case "missing_received":
				session["payment_intent"] = "pi_checkoutread"
				delete(intent, "amount_received")
			case "crossed_reads":
				session["payment_intent"] = "pi_checkoutread"
				intent["status"], intent["amount_received"] = "succeeded", 1900
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer sk_test_checkout" || r.Header.Get("Stripe-Version") != testStripeAPIVersion || r.Header.Get("Idempotency-Key") != "" {
					t.Error("checkout reader made a mutation or unauthenticated request")
				}
				if scenario == "remote_error" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				if scenario == "oversize" {
					_, _ = w.Write([]byte(strings.Repeat(" ", maxStripeResponseBytes+1)))
					return
				}
				switch r.URL.Path {
				case "/checkout/sessions/cs_checkoutread":
					_ = json.NewEncoder(w).Encode(session)
				case "/payment_intents/pi_checkoutread":
					_ = json.NewEncoder(w).Encode(intent)
				default:
					t.Errorf("unexpected read: %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			runtime := NewStripeRuntime(StripeRuntimeConfig{SecretKey: "sk_test_checkout", BaseURL: server.URL, APIVersion: testStripeAPIVersion, LiveMode: input.LiveMode})
			result, err := runtime.ReadProductCheckout(context.Background(), input)
			if (err != nil) != wantError {
				t.Fatalf("result=%#v err=%v wantError=%t", result, err, wantError)
			}
			if !wantError && result.safelyExpired() != oneOf(scenario, "expired", "expired_intent", "live_expired") {
				t.Fatalf("unsafe closure classification: %#v", result)
			}
		})
	}
}
