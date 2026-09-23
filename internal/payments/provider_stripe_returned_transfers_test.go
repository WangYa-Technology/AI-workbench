package payments

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStripeProductTransferLookupConsumedReturns(t *testing.T) {
	for _, scenario := range []string{"returned_only", "new_transfer", "pagination", "new_destination", "partial", "time", "destination", "amount", "currency", "mode", "charge", "metadata", "duplicate_remote", "unknown_return", "duplicate_input", "bad_input", "too_many_input"} {
		t.Run(scenario, func(t *testing.T) {
			input := TransferLookupRequest{TransferRequest: TransferRequest{PaymentID: uuid.New(), ProviderChargeID: "ch_original", DestinationID: "acct_current", AmountCents: 1900, Currency: "USD"}}
			created := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			prior := TransferObservation{Transfer: Transfer{ProviderID: "tr_returned", DestinationID: input.DestinationID, AmountCents: 1900, Currency: "USD", TransferGroup: transferGroup(input.PaymentID)}, PaymentID: input.PaymentID, ProviderChargeID: input.ProviderChargeID, CreatedAt: created, AmountReversed: 1900}
			if scenario == "new_destination" {
				prior.DestinationID = "acct_previous"
			}
			input.ReturnedSources = []TransferObservation{prior}
			old := map[string]any{"id": prior.ProviderID, "object": "transfer", "destination": prior.DestinationID, "source_transaction": prior.ProviderChargeID, "amount": 1900, "currency": "usd", "transfer_group": prior.TransferGroup, "livemode": false, "created": created.Unix(), "reversed": true, "amount_reversed": 1900, "metadata": map[string]string{"hcai_payment_id": input.PaymentID.String()}}
			fresh := map[string]any{"id": "tr_current123", "object": "transfer", "destination": input.DestinationID, "source_transaction": input.ProviderChargeID, "amount": 1900, "currency": "usd", "transfer_group": prior.TransferGroup, "livemode": false, "created": created.Add(time.Minute).Unix(), "reversed": false, "amount_reversed": 0, "metadata": map[string]string{"hcai_payment_id": input.PaymentID.String()}}
			switch scenario {
			case "partial":
				old["amount_reversed"] = 1800
				old["reversed"] = false
			case "time":
				old["created"] = created.Add(time.Second).Unix()
			case "destination":
				old["destination"] = "acct_changed"
			case "amount":
				old["amount"] = 1800
				old["amount_reversed"] = 1800
			case "currency":
				old["currency"] = "eur"
			case "mode":
				old["livemode"] = true
			case "charge":
				old["source_transaction"] = "ch_other"
			case "metadata":
				old["metadata"] = map[string]string{"hcai_payment_id": uuid.NewString()}
			case "unknown_return":
				old["id"] = "tr_unknown"
			case "duplicate_input":
				input.ReturnedSources = append(input.ReturnedSources, prior)
			case "bad_input":
				input.ReturnedSources[0].AmountReversed = 100
			case "too_many_input":
				input.ReturnedSources = make([]TransferObservation, 1001)
			}
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads++
				if r.Method != "GET" || r.URL.Path != "/transfers" || r.Header.Get("Authorization") != "Bearer returned-fixture" || r.Header.Get("Stripe-Version") != testStripeAPIVersion {
					t.Error("unexpected provider request")
				}
				if r.URL.Query().Get("transfer_group") != prior.TransferGroup {
					t.Error("unscoped query")
				}
				items := []any{old}
				more := false
				switch scenario {
				case "new_transfer", "new_destination":
					items = append(items, fresh)
				case "duplicate_remote":
					items = append(items, old)
				case "pagination":
					if reads == 1 {
						more = true
					} else {
						if r.URL.Query().Get("starting_after") != prior.ProviderID {
							t.Error("excluded item lost pagination cursor")
						}
						items = []any{fresh}
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": items, "has_more": more})
			}))
			defer server.Close()
			runtime := NewStripeRuntime(StripeRuntimeConfig{BaseURL: server.URL, SecretKey: "returned-fixture", APIVersion: testStripeAPIVersion, HTTPClient: server.Client()})
			out, err := runtime.LookupProductTransfer(t.Context(), input)
			valid := oneOf(scenario, "returned_only", "new_transfer", "pagination", "new_destination", "unknown_return")
			if valid != (err == nil) {
				t.Fatalf("result %+v error %v", out, err)
			}
			if valid {
				if scenario == "returned_only" {
					if out.Outcome != "not_found" || len(out.Observations) != 0 {
						t.Fatal("consumed return counted as a current transfer", out)
					}
				} else {
					want := "tr_current123"
					if scenario == "unknown_return" {
						want = "tr_unknown"
					}
					if out.Outcome != "found" || len(out.Observations) != 1 || out.Observations[0].ProviderID != want {
						t.Fatal("wrong current transfer", out)
					}
					if scenario == "unknown_return" && out.Observations[0].AmountReversed != 1900 {
						t.Fatal("unknown return evidence lost")
					}
				}
			}
			wantReads := 1
			if scenario == "pagination" {
				wantReads = 2
			}
			if oneOf(scenario, "duplicate_input", "bad_input", "too_many_input") {
				wantReads = 0
			}
			if reads != wantReads {
				t.Fatal("unexpected reads", reads, wantReads)
			}
		})
	}
}

func TestStripeProductTransferReturnedSourceUsesNewFrozenKey(t *testing.T) {
	payment, request, next := uuid.New(), uuid.New(), uuid.New()
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/transfers" {
			t.Error("unexpected request")
		}
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.PostForm.Get("source_transaction") != "ch_original" || r.PostForm.Get("transfer_group") != transferGroup(payment) {
			t.Error("new key changed original payment binding")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "tr_newsource123", "object": "transfer", "destination": "acct_current", "source_transaction": "ch_original", "amount": 1900, "currency": "usd", "transfer_group": transferGroup(payment), "livemode": false, "created": time.Now().Unix(), "reversed": false, "amount_reversed": 0, "metadata": map[string]string{"hcai_payment_id": payment.String()}})
	}))
	defer server.Close()
	runtime := NewStripeRuntime(StripeRuntimeConfig{BaseURL: server.URL, SecretKey: "new-source-fixture", APIVersion: testStripeAPIVersion, HTTPClient: server.Client()})
	for _, source := range []uuid.UUID{uuid.Nil, request, request, next} {
		if _, err := runtime.CreateTransfer(t.Context(), TransferRequest{PaymentID: payment, SourceRequestID: source, ProviderChargeID: "ch_original", DestinationID: "acct_current", AmountCents: 1900, Currency: "USD"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(keys) != 4 || keys[0] != "transfer-"+payment.String() || keys[1] != "seller-source-"+request.String() || keys[2] != keys[1] || keys[3] != "seller-source-"+next.String() {
		t.Fatal("wrong keys", keys)
	}
	batch := uuid.New()
	for range 2 {
		if _, err := runtime.CreateTransfer(t.Context(), TransferRequest{PaymentID: payment, SettlementBatchID: batch, ProviderChargeID: "ch_original", DestinationID: "acct_current", AmountCents: 1900, Currency: "USD"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(keys) != 6 || keys[4] != "settlement-batch-"+batch.String() || keys[5] != keys[4] {
		t.Fatal("automatic batch key not stable", keys)
	}
	if _, err := runtime.CreateTransfer(t.Context(), TransferRequest{PaymentID: payment, SourceRequestID: request, SettlementBatchID: batch, ProviderChargeID: "ch_original", DestinationID: "acct_current", AmountCents: 1900, Currency: "USD"}); err == nil || len(keys) != 6 {
		t.Fatal("mixed operation identities sent", err, keys)
	}
}
