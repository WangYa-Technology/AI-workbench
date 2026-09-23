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

func TestStripeProductTransferLookup(t *testing.T) {
	for _, scenario := range []string{"found", "empty", "pagination", "ambiguous", "duplicate", "duplicate_page", "partial_reversal", "full_reversal",
		"amount", "currency", "destination", "charge", "group", "metadata", "mode", "future", "zero_created", "negative_reversal", "excess_reversal", "inconsistent_reversal",
		"missing_mode", "missing_created", "missing_reversed", "missing_amount_reversed", "bad_id", "bad_object", "missing_more", "missing_data", "null_data", "bad_envelope", "empty_more", "oversized", "remote_error", "malformed"} {
		t.Run(scenario, func(t *testing.T) {
			input := TransferLookupRequest{TransferRequest: TransferRequest{PaymentID: uuid.New(), ProviderChargeID: "ch_original", DestinationID: "acct_seller", AmountCents: 1900, Currency: "USD"}}
			item := map[string]any{"id": "tr_original", "object": "transfer", "destination": input.DestinationID, "source_transaction": input.ProviderChargeID,
				"amount": input.AmountCents, "currency": "usd", "transfer_group": transferGroup(input.PaymentID), "livemode": false,
				"created": time.Now().Add(-time.Minute).Unix(), "reversed": false, "amount_reversed": 0, "metadata": map[string]string{"hcai_payment_id": input.PaymentID.String()}}
			switch scenario {
			case "amount":
				item["amount"] = 1800
			case "currency":
				item["currency"] = "eur"
			case "destination":
				item["destination"] = "acct_other"
			case "charge":
				item["source_transaction"] = "ch_other"
			case "group":
				item["transfer_group"] = "other"
			case "metadata":
				item["metadata"] = map[string]string{"hcai_payment_id": uuid.NewString()}
			case "mode":
				item["livemode"] = true
			case "future":
				item["created"] = time.Now().Add(time.Hour).Unix()
			case "zero_created":
				item["created"] = 0
			case "partial_reversal":
				item["amount_reversed"] = 100
			case "full_reversal":
				item["amount_reversed"], item["reversed"] = 1900, true
			case "negative_reversal":
				item["amount_reversed"] = -1
			case "excess_reversal":
				item["amount_reversed"] = 1901
			case "inconsistent_reversal":
				item["reversed"] = true
			case "missing_mode":
				delete(item, "livemode")
			case "missing_created":
				delete(item, "created")
			case "missing_reversed":
				delete(item, "reversed")
			case "missing_amount_reversed":
				delete(item, "amount_reversed")
			case "bad_id":
				item["id"] = "pi_wrong"
			case "bad_object":
				item["object"] = "charge"
			}
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads++
				if r.Method != http.MethodGet || r.URL.Path != "/transfers" || r.Header.Get("Authorization") != "Bearer transfer-fixture" || r.Header.Get("Stripe-Version") != testStripeAPIVersion {
					t.Error("transfer lookup made an unexpected or unauthenticated request")
				}
				q := r.URL.Query()
				if q.Get("limit") != "100" || q.Get("transfer_group") != transferGroup(input.PaymentID) {
					t.Error("transfer lookup not scoped to original payment")
				}
				if reads > 1 && q.Get("starting_after") != "tr_original" {
					t.Error("pagination cursor lost")
				}
				if scenario == "remote_error" {
					http.Error(w, "private provider error", http.StatusServiceUnavailable)
					return
				}
				if scenario == "malformed" {
					_, _ = w.Write([]byte(`{"data":`))
					return
				}
				items, more := []any{item}, false
				switch scenario {
				case "empty", "empty_more":
					items = []any{}
					more = scenario == "empty_more"
				case "pagination", "duplicate_page":
					more = reads == 1
					if reads > 1 && scenario == "pagination" {
						items = []any{}
					}
				case "duplicate", "ambiguous":
					second := map[string]any{}
					for k, v := range item {
						second[k] = v
					}
					if scenario == "ambiguous" {
						second["id"] = "tr_second"
					}
					items = append(items, second)
				case "oversized":
					items = make([]any, 101)
					for i := range items {
						items[i] = item
					}
				}
				response := map[string]any{"object": "list", "data": items, "has_more": more, "private_provider_field": "must-not-persist"}
				switch scenario {
				case "missing_more":
					delete(response, "has_more")
				case "missing_data":
					delete(response, "data")
				case "null_data":
					response["data"] = nil
				case "bad_envelope":
					response["object"] = "transfer"
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			runtime := NewStripeRuntime(StripeRuntimeConfig{BaseURL: server.URL, SecretKey: "transfer-fixture", APIVersion: testStripeAPIVersion, HTTPClient: server.Client()})
			result, err := runtime.LookupProductTransfer(context.Background(), input)
			wantError := !oneOf(scenario, "found", "empty", "pagination", "ambiguous", "partial_reversal", "full_reversal")
			if (err != nil) != wantError {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if !wantError {
				outcome, observations := "found", 1
				if scenario == "empty" {
					outcome, observations = "not_found", 0
				} else if scenario == "ambiguous" {
					outcome, observations = "ambiguous", 2
				}
				if result.Outcome != outcome || len(result.Observations) != observations || result.Pages != reads {
					t.Fatalf("incorrect result: %+v reads=%d", result, reads)
				}
				if scenario == "partial_reversal" && result.Observations[0].AmountReversed != 100 || scenario == "full_reversal" && result.Observations[0].AmountReversed != 1900 {
					t.Fatal("lost reversal evidence")
				}
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "must-not-persist") || strings.Contains(string(encoded), "transfer-fixture") || err != nil && strings.Contains(err.Error(), "private provider error") {
				t.Fatal("provider secrets leaked into evidence")
			}
			wantReads := 1
			if oneOf(scenario, "pagination", "duplicate_page") {
				wantReads = 2
			}
			if reads != wantReads {
				t.Fatalf("reads=%d want=%d", reads, wantReads)
			}
		})
	}
}

func TestStripeProductTransferLookupRejectsInvalidInputAndRedirect(t *testing.T) {
	redirects, calls := 0, 0
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirects++ }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	runtime := NewStripeRuntime(StripeRuntimeConfig{BaseURL: server.URL, SecretKey: "transfer-fixture", APIVersion: testStripeAPIVersion, HTTPClient: server.Client()})
	valid := TransferLookupRequest{TransferRequest: TransferRequest{PaymentID: uuid.New(), ProviderChargeID: "ch_original", DestinationID: "acct_seller", AmountCents: 1900, Currency: "USD"}}
	for _, mutate := range []func(*TransferLookupRequest){
		func(r *TransferLookupRequest) { r.PaymentID = uuid.Nil },
		func(r *TransferLookupRequest) { r.ProviderChargeID = "pi_other" },
		func(r *TransferLookupRequest) { r.DestinationID = "acct_../../other" },
		func(r *TransferLookupRequest) { r.AmountCents = 0 },
		func(r *TransferLookupRequest) { r.AmountCents = 100000000 },
		func(r *TransferLookupRequest) { r.Currency = "EUR" },
		func(r *TransferLookupRequest) { r.LiveMode = true },
	} {
		input := valid
		mutate(&input)
		if _, err := runtime.LookupProductTransfer(context.Background(), input); err == nil {
			t.Fatalf("invalid input accepted: %+v", input)
		}
	}
	if calls != 0 {
		t.Fatal("invalid input reached provider")
	}
	if _, err := runtime.LookupProductTransfer(context.Background(), valid); err == nil || calls != 1 || redirects != 0 {
		t.Fatalf("redirect followed or accepted: err=%v calls=%d target=%d", err, calls, redirects)
	}
}
