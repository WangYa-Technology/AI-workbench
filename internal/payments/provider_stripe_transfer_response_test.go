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

func stripeTransferResponse(input TransferRequest, live bool) map[string]any {
	return map[string]any{
		"id": "tr_response", "object": "transfer", "destination": input.DestinationID,
		"source_transaction": input.ProviderChargeID, "amount": input.AmountCents,
		"currency": strings.ToLower(strings.TrimSpace(input.Currency)), "transfer_group": transferGroup(input.PaymentID),
		"livemode": live, "created": time.Now().Add(-time.Minute).Unix(), "reversed": false, "amount_reversed": 0,
		"metadata": map[string]string{"hcai_payment_id": input.PaymentID.String()},
	}
}

func TestStripeCreateTransferResponseEvidence(t *testing.T) {
	for _, scenario := range []string{
		"valid_test", "valid_live", "wrong_charge", "wrong_metadata", "wrong_mode", "wrong_object",
		"wrong_destination", "wrong_amount", "wrong_currency", "wrong_group", "wrong_id",
		"zero_created", "future_created", "partial_reversal", "full_reversal", "negative_reversal", "excess_reversal", "inconsistent_reversal",
		"missing_livemode", "null_livemode", "missing_created", "null_created",
		"missing_reversed", "null_reversed", "missing_amount_reversed", "null_amount_reversed",
		"missing_metadata", "null_metadata", "missing_source_transaction", "missing_object",
	} {
		t.Run(scenario, func(t *testing.T) {
			input := TransferRequest{PaymentID: uuid.New(), ProviderChargeID: "ch_response", DestinationID: "acct_response", AmountCents: 1200, Currency: " USD "}
			live := scenario == "valid_live"
			response := stripeTransferResponse(input, live)
			switch scenario {
			case "wrong_charge":
				response["source_transaction"] = "ch_other"
			case "wrong_metadata":
				response["metadata"] = map[string]string{"hcai_payment_id": uuid.NewString()}
			case "wrong_mode":
				response["livemode"] = true
			case "wrong_object":
				response["object"] = "charge"
			case "wrong_destination":
				response["destination"] = "acct_other"
			case "wrong_amount":
				response["amount"] = 1100
			case "wrong_currency":
				response["currency"] = "eur"
			case "wrong_group":
				response["transfer_group"] = "other"
			case "wrong_id":
				response["id"] = "pi_other"
			case "zero_created":
				response["created"] = 0
			case "future_created":
				response["created"] = time.Now().Add(time.Hour).Unix()
			case "partial_reversal":
				response["amount_reversed"] = 100
			case "full_reversal":
				response["amount_reversed"], response["reversed"] = input.AmountCents, true
			case "negative_reversal":
				response["amount_reversed"] = -1
			case "excess_reversal":
				response["amount_reversed"] = input.AmountCents + 1
			case "inconsistent_reversal":
				response["reversed"] = true
			default:
				if field, ok := strings.CutPrefix(scenario, "missing_"); ok {
					delete(response, field)
				} else if field, ok := strings.CutPrefix(scenario, "null_"); ok {
					response[field] = nil
				}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/transfers" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				assertStripeHeaders(t, r, "transfer-"+input.PaymentID.String())
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			runtime := testStripeRuntime(server)
			runtime.config.LiveMode = live
			transfer, err := runtime.CreateTransfer(context.Background(), input)
			if strings.HasPrefix(scenario, "valid_") {
				if err != nil || transfer.ProviderID != "tr_response" || transfer.Currency != "USD" {
					t.Fatalf("valid response: transfer=%+v err=%v", transfer, err)
				}
			} else if err == nil || transfer != (Transfer{}) {
				t.Fatalf("untrusted response accepted: transfer=%+v err=%v", transfer, err)
			}
		})
	}
}
