package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func transferReversalFixture(endpoint string) TransferReversalRequest {
	bank := payoutFixture(endpoint)
	return TransferReversalRequest{
		TransferLookupRequest: TransferLookupRequest{TransferRequest: TransferRequest{PaymentID: uuid.New(),
			ProviderChargeID: "ch_source123", DestinationID: bank.DestinationID, AmountCents: 1900, Currency: "USD"}},
		CommandID: uuid.New(), PayoutRequestID: bank.PayoutRequestID, SourceTransferID: uuid.New(),
		ProviderTransferID: "tr_source123", ReverseAmountCents: 1900,
		IdempotencyKey: "source-reversal-" + uuid.NewString(), ReservedAt: bank.ReservedAt, Identity: bank.Identity,
	}
}

func reversalParentResponse(input TransferReversalRequest, reversed int) map[string]any {
	return map[string]any{"id": input.ProviderTransferID, "object": "transfer", "destination": input.DestinationID,
		"source_transaction": input.ProviderChargeID, "amount": input.AmountCents, "currency": "usd",
		"transfer_group": transferGroup(input.PaymentID), "livemode": input.LiveMode,
		"created": input.ReservedAt.Add(-time.Hour).Unix(), "reversed": reversed == input.AmountCents,
		"amount_reversed": reversed, "metadata": map[string]string{"hcai_payment_id": input.PaymentID.String()}}
}

func reversalResponse(input TransferReversalRequest) map[string]any {
	return map[string]any{"id": "trr_reversal123", "object": "transfer_reversal", "transfer": input.ProviderTransferID,
		"amount": input.ReverseAmountCents, "currency": "usd", "created": input.ReservedAt.Add(time.Second).Unix(),
		"metadata": map[string]string{"hcai_reversal_command_id": input.CommandID.String(),
			"hcai_payout_request_id": input.PayoutRequestID.String(), "hcai_source_transfer_id": input.SourceTransferID.String(),
			"hcai_payment_id": input.PaymentID.String()}}
}

func TestStripeTransferReversalInvalidInputNeverCallsProvider(t *testing.T) {
	changes := map[string]func(*TransferReversalRequest){
		"command":        func(x *TransferReversalRequest) { x.CommandID = uuid.Nil },
		"request":        func(x *TransferReversalRequest) { x.PayoutRequestID = uuid.Nil },
		"source":         func(x *TransferReversalRequest) { x.SourceTransferID = uuid.Nil },
		"payment":        func(x *TransferReversalRequest) { x.PaymentID = uuid.Nil },
		"transfer_path":  func(x *TransferReversalRequest) { x.ProviderTransferID = "tr_source/../other" },
		"charge":         func(x *TransferReversalRequest) { x.ProviderChargeID = "pi_wrong123" },
		"destination":    func(x *TransferReversalRequest) { x.DestinationID = x.Identity.MerchantID },
		"currency":       func(x *TransferReversalRequest) { x.Currency = "EUR" },
		"zero_amount":    func(x *TransferReversalRequest) { x.ReverseAmountCents = 0 },
		"excess_amount":  func(x *TransferReversalRequest) { x.ReverseAmountCents++ },
		"negative_prior": func(x *TransferReversalRequest) { x.PriorReversedCents = -1 },
		"fully_reversed": func(x *TransferReversalRequest) { x.PriorReversedCents = x.AmountCents },
		"nil_identity":   func(x *TransferReversalRequest) { x.Identity = nil },
		"mode":           func(x *TransferReversalRequest) { x.Identity.LiveMode = true },
		"provider":       func(x *TransferReversalRequest) { x.Identity.Provider = "waffo_pancake" },
		"endpoint":       func(x *TransferReversalRequest) { x.Identity.Endpoint += "/wrong" },
		"api":            func(x *TransferReversalRequest) { x.Identity.APIVersion = "other" },
		"serialization":  func(x *TransferReversalRequest) { x.Identity.RequestVersion = "other" },
		"store":          func(x *TransferReversalRequest) { x.Identity.StoreID = "other" },
		"key":            func(x *TransferReversalRequest) { x.IdempotencyKey = "bad\nkey" },
		"future":         func(x *TransferReversalRequest) { x.ReservedAt = time.Now().Add(time.Hour) },
		"missing_time":   func(x *TransferReversalRequest) { x.ReservedAt = time.Time{} },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
			defer server.Close()
			input := transferReversalFixture(server.URL)
			change(&input)
			runtime := payoutRuntime(server)
			if _, err := runtime.CreateTransferReversal(t.Context(), input); err == nil {
				t.Fatal("invalid write accepted")
			}
			if _, err := runtime.LookupTransferReversal(t.Context(), input); err == nil {
				t.Fatal("invalid read accepted")
			}
			if calls.Load() != 0 {
				t.Fatal("invalid input contacted provider")
			}
		})
	}
}

func TestStripeTransferReversalCreateAndEvidence(t *testing.T) {
	for _, scenario := range []string{"full", "partial", "merchant", "credential_mode", "parent_amount", "parent_charge", "parent_destination", "parent_group", "parent_mode", "prior_changed", "expired", "post_error", "lost_response", "reversal_amount", "reversal_transfer", "reversal_command", "reversal_source", "reversal_request", "reversal_payment", "reversal_created", "missing_created", "parent_lags", "final_read_error"} {
		t.Run(scenario, func(t *testing.T) {
			var input TransferReversalRequest
			var posts, parents atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "merchant" && r.URL.Path == "/account" {
					fmt.Fprint(w, `{"object":"account","id":"acct_different"}`)
					return
				}
				if scenario == "credential_mode" && r.URL.Path == "/balance" {
					fmt.Fprint(w, `{"object":"balance","livemode":true}`)
					return
				}
				if servePayoutIdentity(t, w, r) {
					return
				}
				if r.Header.Get("Stripe-Account") != "" {
					t.Error("reversal used connected-account scope")
				}
				if r.URL.Path == "/transfers/"+input.ProviderTransferID && r.Method == http.MethodGet {
					n := parents.Add(1)
					reversed := input.PriorReversedCents
					if n > 1 && scenario != "parent_lags" {
						reversed += input.ReverseAmountCents
					}
					if scenario == "prior_changed" {
						reversed++
					}
					if n > 1 && scenario == "final_read_error" {
						http.Error(w, "private provider failure", 503)
						return
					}
					body := reversalParentResponse(input, reversed)
					for test, field := range map[string]string{"parent_amount": "amount", "parent_charge": "source_transaction", "parent_destination": "destination", "parent_group": "transfer_group", "parent_mode": "livemode"} {
						if scenario == test {
							switch field {
							case "amount":
								body[field] = 1901
							case "livemode":
								body[field] = true
							default:
								body[field] = "other_original_value"
							}
						}
					}
					_ = json.NewEncoder(w).Encode(body)
					return
				}
				if r.URL.Path != "/transfers/"+input.ProviderTransferID+"/reversals" || r.Method != http.MethodPost {
					t.Error("unexpected reversal route", r.Method, r.URL.Path)
					http.Error(w, "unexpected", 500)
					return
				}
				posts.Add(1)
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if len(r.PostForm) != 6 || r.PostForm.Get("amount") != fmt.Sprint(input.ReverseAmountCents) || r.PostForm.Get("refund_application_fee") != "false" ||
					r.Header.Get("Idempotency-Key") != input.IdempotencyKey || r.PostForm.Get("metadata[hcai_reversal_command_id]") != input.CommandID.String() ||
					r.PostForm.Get("metadata[hcai_payment_id]") != input.PaymentID.String() || r.PostForm.Get("metadata[hcai_source_transfer_id]") != input.SourceTransferID.String() ||
					r.PostForm.Get("metadata[hcai_payout_request_id]") != input.PayoutRequestID.String() {
					t.Error("reversal command lost its frozen bindings", r.PostForm)
				}
				if scenario == "post_error" {
					http.Error(w, "private provider failure", 503)
					return
				}
				if scenario == "lost_response" {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
					return
				}
				body := reversalResponse(input)
				switch scenario {
				case "reversal_amount":
					body["amount"] = 1
				case "reversal_transfer":
					body["transfer"] = "tr_unrelated"
				case "reversal_created":
					body["created"] = time.Now().Add(time.Hour).Unix()
				case "missing_created":
					delete(body, "created")
				}
				for test, field := range map[string]string{"reversal_command": "hcai_reversal_command_id", "reversal_source": "hcai_source_transfer_id", "reversal_request": "hcai_payout_request_id", "reversal_payment": "hcai_payment_id"} {
					if scenario == test {
						body["metadata"].(map[string]string)[field] = uuid.NewString()
					}
				}
				_ = json.NewEncoder(w).Encode(body)
			}))
			defer server.Close()
			input = transferReversalFixture(server.URL)
			if scenario == "partial" {
				input.PriorReversedCents, input.ReverseAmountCents = 200, 600
			}
			if scenario == "expired" {
				input.ReservedAt = time.Now().Add(-24 * time.Hour)
			}
			out, err := payoutRuntime(server).CreateTransferReversal(t.Context(), input)
			valid := scenario == "full" || scenario == "partial"
			if (err == nil) != valid {
				t.Fatalf("result=%+v error=%v", out, err)
			}
			if valid && (out.Outcome != "found" || len(out.Observations) != 1 || out.Transfer.AmountReversed != input.PriorReversedCents+input.ReverseAmountCents || posts.Load() != 1) {
				t.Fatal("reversal did not match its transfer", out, posts.Load())
			}
			preflight := oneOf(scenario, "merchant", "credential_mode", "prior_changed", "expired") || strings.HasPrefix(scenario, "parent_") && scenario != "parent_lags"
			if preflight && posts.Load() != 0 || posts.Load() > 1 {
				t.Fatal("unsafe reversal POST count", posts.Load())
			}
			if oneOf(scenario, "parent_lags", "final_read_error") && (out.Outcome == "found" || len(out.Observations) != 1) {
				t.Fatal("lost valid response or trusted incomplete aggregate", out)
			}
		})
	}
}

func TestStripeTransferReversalPOSTCannotBeReplayed(t *testing.T) {
	input := transferReversalFixture("https://stripe.example")
	var posts atomic.Int32
	client := &http.Client{Transport: payoutTransportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			if r.GetBody != nil || r.Header.Get("Idempotency-Key") != input.IdempotencyKey {
				t.Error("reversal can be transport-replayed")
			}
			return nil, errors.New("response lost")
		}
		body := map[string]any{}
		switch r.URL.Path {
		case "/account":
			body = map[string]any{"object": "account", "id": "acct_original"}
		case "/balance":
			body = map[string]any{"object": "balance", "livemode": false}
		default:
			body = reversalParentResponse(input, 0)
		}
		data, _ := json.Marshal(body)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(data))), Request: r}, nil
	})}
	runtime := NewStripeRuntime(StripeRuntimeConfig{BaseURL: input.Identity.Endpoint, SecretKey: "test-reversal", APIVersion: testStripeAPIVersion, HTTPClient: client})
	if _, err := runtime.CreateTransferReversal(context.Background(), input); err == nil || posts.Load() != 1 {
		t.Fatal("POST replay boundary", err, posts.Load())
	}
}

func TestStripeTransferReversalCancellationAndPOSTRedirect(t *testing.T) {
	for _, scenario := range []string{"cancelled", "deadline", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			var input TransferReversalRequest
			var posts, forwarded atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "deadline" {
					<-r.Context().Done()
					return
				}
				if servePayoutIdentity(t, w, r) {
					return
				}
				if r.URL.Path == "/transfers/"+input.ProviderTransferID {
					_ = json.NewEncoder(w).Encode(reversalParentResponse(input, 0))
					return
				}
				if r.URL.Path == "/unexpected-reversal" {
					forwarded.Add(1)
					return
				}
				posts.Add(1)
				http.Redirect(w, r, "/unexpected-reversal", 307)
			}))
			defer server.Close()
			input = transferReversalFixture(server.URL)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "deadline" {
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
			}
			if scenario == "cancelled" {
				cancel()
			}
			out, err := payoutRuntime(server).CreateTransferReversal(ctx, input)
			if err == nil || out.Outcome == "found" || forwarded.Load() != 0 {
				t.Fatal("cancel/redirect boundary", out, err, forwarded.Load())
			}
			want := int32(0)
			if scenario == "redirect" {
				want = 1
			}
			if posts.Load() != want {
				t.Fatal("unexpected reversal after interruption", posts.Load())
			}
		})
	}
}

func TestStripeTransferReversalLookupNeverWrites(t *testing.T) {
	for _, scenario := range []string{"found", "empty", "foreign_command", "pagination", "ambiguous", "duplicate", "incomplete", "expired_read", "parent_lags", "partial_scan", "missing_more", "missing_data", "null_data", "bad_envelope", "empty_more", "oversized", "malformed", "remote_error", "redirect", "wrong_transfer", "wrong_currency", "bad_id", "bad_object", "negative_amount", "missing_created", "wrong_source"} {
		t.Run(scenario, func(t *testing.T) {
			var input TransferReversalRequest
			var reads, writes, unexpected atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("Idempotency-Key") != "" || r.Header.Get("Stripe-Account") != "" {
					writes.Add(1)
				}
				if servePayoutIdentity(t, w, r) {
					return
				}
				if r.URL.Path == "/transfers/"+input.ProviderTransferID {
					total := input.PriorReversedCents + input.ReverseAmountCents
					if scenario == "empty" || scenario == "parent_lags" {
						total = 0
					}
					if scenario == "ambiguous" {
						total = 1800
					}
					_ = json.NewEncoder(w).Encode(reversalParentResponse(input, total))
					return
				}
				if r.URL.Path != "/transfers/"+input.ProviderTransferID+"/reversals" {
					unexpected.Add(1)
					http.Error(w, "unexpected", 500)
					return
				}
				page := reads.Add(1)
				if r.URL.Query().Get("limit") != "100" {
					t.Error("unbounded reversal query")
				}
				if scenario == "redirect" {
					http.Redirect(w, r, "/unexpected-reversal", 307)
					return
				}
				if scenario == "remote_error" {
					http.Error(w, "private provider failure", 503)
					return
				}
				if scenario == "malformed" {
					fmt.Fprint(w, `{"object":`)
					return
				}
				body := reversalResponse(input)
				switch scenario {
				case "wrong_transfer":
					body["transfer"] = "tr_some_other"
				case "wrong_currency":
					body["currency"] = "eur"
				case "bad_id":
					body["id"] = "re_other123"
				case "bad_object":
					body["object"] = "refund"
				case "negative_amount":
					body["amount"] = -1
				case "missing_created":
					delete(body, "created")
				case "wrong_source":
					body["metadata"].(map[string]string)["hcai_source_transfer_id"] = uuid.NewString()
				case "foreign_command":
					body["metadata"].(map[string]string)["hcai_reversal_command_id"] = uuid.NewString()
				}
				items, more := []any{body}, false
				switch scenario {
				case "empty", "empty_more":
					items = []any{}
					more = scenario == "empty_more"
				case "duplicate":
					items = append(items, body)
				case "ambiguous":
					second := reversalResponse(input)
					second["id"] = "trr_second123"
					items = append(items, second)
				case "oversized":
					items = make([]any, 101)
					for i := range items {
						items[i] = body
					}
				case "pagination", "partial_scan":
					if page == 1 {
						body["id"], body["amount"], body["metadata"] = "trr_foreign123", 200, map[string]string{}
						more = scenario == "pagination"
					} else if r.URL.Query().Get("starting_after") != "trr_foreign123" {
						t.Error("lost reversal cursor")
					}
				case "incomplete":
					body["id"], body["amount"], body["metadata"] = fmt.Sprintf("trr_page%06d", page), 1, map[string]string{}
					more = true
					if page > 1 && r.URL.Query().Get("starting_after") != fmt.Sprintf("trr_page%06d", page-1) {
						t.Error("bad page bound cursor")
					}
				}
				envelope := map[string]any{"object": "list", "data": items, "has_more": more}
				switch scenario {
				case "missing_more":
					delete(envelope, "has_more")
				case "missing_data":
					delete(envelope, "data")
				case "null_data":
					envelope["data"] = nil
				case "bad_envelope":
					envelope["object"] = "refund"
				}
				_ = json.NewEncoder(w).Encode(envelope)
			}))
			defer server.Close()
			input = transferReversalFixture(server.URL)
			if scenario == "pagination" {
				input.PriorReversedCents, input.ReverseAmountCents = 200, 1700
			}
			if scenario == "ambiguous" {
				input.ReverseAmountCents = 900
			}
			if scenario == "expired_read" {
				input.ReservedAt = time.Now().Add(-48 * time.Hour)
			}
			out, err := payoutRuntime(server).LookupTransferReversal(t.Context(), input)
			valid := oneOf(scenario, "found", "empty", "foreign_command", "pagination", "ambiguous", "incomplete", "expired_read")
			if (err == nil) != valid {
				t.Fatalf("lookup=%+v error=%v", out, err)
			}
			if writes.Load() != 0 || unexpected.Load() != 0 {
				t.Fatal("read recovery wrote or followed redirect", writes.Load(), unexpected.Load())
			}
			if valid {
				want := "found"
				switch scenario {
				case "empty", "foreign_command":
					want = "not_found"
				case "incomplete":
					want = "incomplete"
				case "ambiguous":
					want = "ambiguous"
				}
				if out.Outcome != want {
					t.Fatal("wrong recovery outcome", out)
				}
				if scenario == "incomplete" && reads.Load() != 10 {
					t.Fatal("scan was not bounded", reads.Load())
				}
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("provider error leaked")
			}
		})
	}
}
