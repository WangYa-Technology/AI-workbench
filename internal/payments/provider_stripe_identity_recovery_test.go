package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestStripeLegacyIdentityProof(t *testing.T) {
	for _, field := range []string{"valid", "id", "object", "status", "amount", "amount_received", "currency", "livemode", "missing_mode", "missing_amount", "latest_charge", "known_charge", "payment_metadata", "resource_metadata", "purpose_metadata", "wrong_account"} {
		t.Run(field, func(t *testing.T) {
			input := ProductPaymentBinding{PaymentID: uuid.New(), ResourceID: uuid.New(), OrderID: uuid.New(), BuyerID: uuid.New(), AmountCents: 1900, Currency: "USD", ProviderPaymentID: "pi_original", ProviderChargeID: "ch_original"}
			metadata := map[string]string{"hcai_payment_id": input.PaymentID.String(), "hcai_resource_id": input.ResourceID.String(), "hcai_purpose": "product"}
			body := map[string]any{"id": "pi_original", "object": "payment_intent", "status": "succeeded", "amount": 1900, "amount_received": 1900, "currency": "usd", "livemode": false, "latest_charge": "ch_original", "metadata": metadata}
			switch field {
			case "id":
				body[field] = "pi_other"
			case "object":
				body[field] = "charge"
			case "status":
				body[field] = "processing"
			case "amount", "amount_received":
				body[field] = 1899
			case "currency":
				body[field] = "eur"
			case "livemode":
				body[field] = true
			case "missing_mode":
				delete(body, "livemode")
			case "missing_amount":
				delete(body, "amount")
			case "latest_charge":
				body[field] = ""
			case "known_charge":
				body["latest_charge"] = "ch_other"
			case "payment_metadata":
				metadata["hcai_payment_id"] = uuid.NewString()
			case "resource_metadata":
				metadata["hcai_resource_id"] = uuid.NewString()
			case "purpose_metadata":
				metadata["hcai_purpose"] = "task"
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.Path != "/payment_intents/pi_original" || r.Header.Get("Authorization") != "Bearer fixture-key" {
					t.Error("unexpected remote request")
				}
				if field == "wrong_account" {
					http.Error(w, "not found in this account", 404)
					return
				}
				json.NewEncoder(w).Encode(body)
			}))
			defer server.Close()
			runtime := NewStripeRuntime(StripeRuntimeConfig{BaseURL: server.URL, SecretKey: "fixture-key", APIVersion: testStripeAPIVersion, HTTPClient: server.Client()})
			observation, err := runtime.ReadProductPaymentIdentity(context.Background(), input)
			if field == "valid" {
				if err != nil || !validIdentityObservation(input, observation) {
					t.Fatal(observation, err)
				}
			} else if err == nil {
				t.Fatal("accepted mismatched identity proof")
			}
			if calls != 1 {
				t.Fatal("unexpected calls", calls)
			}
		})
	}
}
