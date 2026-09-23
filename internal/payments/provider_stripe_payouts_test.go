package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func payoutFixture(endpoint string) PayoutRequest {
	return PayoutRequest{PayoutRequestID: uuid.New(), DestinationID: "acct_seller123", BankDestinationID: "ba_bank123",
		AmountCents: 1900, Currency: "USD", IdempotencyKey: "payout-" + uuid.NewString(), ReservedAt: time.Now().Add(-time.Minute),
		Identity: &ProductCheckoutIdentity{Provider: "stripe", MerchantID: "acct_original", Endpoint: endpoint,
			APIVersion: testStripeAPIVersion, RequestVersion: stripeProductCheckoutVersion}}
}

func payoutResponse(input PayoutRequest) map[string]any {
	return map[string]any{"object": "payout", "id": "po_payout123", "destination": input.BankDestinationID,
		"amount": input.AmountCents, "currency": "usd", "status": "pending", "livemode": false, "automatic": false,
		"created": input.ReservedAt.Add(time.Second).Unix(), "method": "standard", "type": "bank_account",
		"metadata": map[string]string{"hcai_payout_request_id": input.PayoutRequestID.String()}}
}

func payoutRuntime(server *httptest.Server) *StripeRuntime {
	return NewStripeRuntime(StripeRuntimeConfig{BaseURL: server.URL, SecretKey: "payout-test-secret",
		APIVersion: testStripeAPIVersion, HTTPClient: server.Client()})
}

func servePayoutIdentity(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.Header.Get("Authorization") != "Bearer payout-test-secret" || r.Header.Get("Stripe-Version") != testStripeAPIVersion {
		t.Error("missing authenticated versioned request")
	}
	if r.Header.Get("Stripe-Account") != "" {
		return false
	}
	if r.URL.Path != "/account" && r.URL.Path != "/balance" {
		return false
	}
	if r.Method != http.MethodGet || r.Header.Get("Stripe-Account") != "" || r.Header.Get("Idempotency-Key") != "" {
		t.Error("merchant authentication used connected-account scope or a write")
	}
	if r.URL.Path == "/account" {
		fmt.Fprint(w, `{"object":"account","id":"acct_original"}`)
	} else {
		fmt.Fprint(w, `{"object":"balance","livemode":false}`)
	}
	return true
}

func servePayoutReadiness(t *testing.T, w http.ResponseWriter, r *http.Request, input PayoutRequest) bool {
	t.Helper()
	if r.URL.Path != "/account" && r.URL.Path != "/balance" && !strings.Contains(r.URL.Path, "/external_accounts/") {
		return false
	}
	if r.Method != http.MethodGet || r.Header.Get("Idempotency-Key") != "" {
		t.Error("payout preflight must be read-only")
	}
	if r.URL.Path == "/account" || r.URL.Path == "/balance" {
		if r.Header.Get("Stripe-Account") != input.DestinationID {
			t.Error("preflight queried wrong account")
		}
	} else if r.Header.Get("Stripe-Account") != "" {
		t.Error("bank endpoint must use platform credentials")
	}
	switch r.URL.Path {
	case "/account":
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "account", "id": input.DestinationID, "payouts_enabled": true,
			"settings": map[string]any{"payouts": map[string]any{"schedule": map[string]string{"interval": "manual"}}}})
	case "/balance":
		fmt.Fprint(w, `{"object":"balance","livemode":false,"available":[{"currency":"usd","amount":1900}]}`)
	default:
		if r.URL.Path != "/accounts/"+input.DestinationID+"/external_accounts/"+input.BankDestinationID {
			t.Error("wrong bank route")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "bank_account", "id": input.BankDestinationID, "account": input.DestinationID, "currency": "usd", "status": "new", "bank_name": "Example Bank", "last4": "6789"})
	}
	return true
}

func TestStripePayoutRejectsInvalidRequestBeforeHTTP(t *testing.T) {
	cases := map[string]func(*PayoutRequest){
		"nil_identity":    func(p *PayoutRequest) { p.Identity = nil },
		"provider":        func(p *PayoutRequest) { p.Identity.Provider = "waffo" },
		"merchant":        func(p *PayoutRequest) { p.Identity.MerchantID = "" },
		"store":           func(p *PayoutRequest) { p.Identity.StoreID = "store_unexpected" },
		"mode":            func(p *PayoutRequest) { p.Identity.LiveMode = true },
		"endpoint":        func(p *PayoutRequest) { p.Identity.Endpoint += "/other" },
		"api_version":     func(p *PayoutRequest) { p.Identity.APIVersion = "other" },
		"request_version": func(p *PayoutRequest) { p.Identity.RequestVersion = "other" },
		"request_id":      func(p *PayoutRequest) { p.PayoutRequestID = uuid.Nil },
		"account":         func(p *PayoutRequest) { p.DestinationID = "acct_../other" },
		"bank":            func(p *PayoutRequest) { p.BankDestinationID = "" },
		"card":            func(p *PayoutRequest) { p.BankDestinationID = "card_card123" },
		"amount_zero":     func(p *PayoutRequest) { p.AmountCents = 0 },
		"amount_large":    func(p *PayoutRequest) { p.AmountCents = 100000000 },
		"currency":        func(p *PayoutRequest) { p.Currency = "EUR" },
		"key_short":       func(p *PayoutRequest) { p.IdempotencyKey = "short" },
		"key_long":        func(p *PayoutRequest) { p.IdempotencyKey = strings.Repeat("a", 161) },
		"key_control":     func(p *PayoutRequest) { p.IdempotencyKey += "\n" },
		"key_unicode":     func(p *PayoutRequest) { p.IdempotencyKey += "中" },
		"key_space":       func(p *PayoutRequest) { p.IdempotencyKey += " " },
		"time_zero":       func(p *PayoutRequest) { p.ReservedAt = time.Time{} },
		"time_future":     func(p *PayoutRequest) { p.ReservedAt = time.Now().Add(time.Hour) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
			defer server.Close()
			input := payoutFixture(server.URL)
			mutate(&input)
			runtime := payoutRuntime(server)
			_, createErr := runtime.CreatePayout(context.Background(), input)
			_, readErr := runtime.ReadPayout(context.Background(), input, "po_payout123")
			_, lookupErr := runtime.LookupPayout(context.Background(), input)
			if createErr == nil || readErr == nil || lookupErr == nil || calls.Load() != 0 {
				t.Fatalf("invalid request reached provider: create=%v read=%v lookup=%v calls=%d", createErr, readErr, lookupErr, calls.Load())
			}
		})
	}
	var absent *StripeRuntime
	if _, err := absent.CreatePayout(context.Background(), payoutFixture("https://invalid.test")); err == nil {
		t.Fatal("nil runtime accepted")
	}
}

func TestStripePayoutAuthenticatesMerchantAndMode(t *testing.T) {
	for _, mismatch := range []string{"merchant", "mode", "missing_mode"} {
		t.Run(mismatch, func(t *testing.T) {
			var financialCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/account":
					merchant := "acct_original"
					if mismatch == "merchant" {
						merchant = "acct_changed123"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"object": "account", "id": merchant})
				case "/balance":
					item := map[string]any{"object": "balance", "livemode": mismatch == "mode"}
					if mismatch == "missing_mode" {
						delete(item, "livemode")
					}
					_ = json.NewEncoder(w).Encode(item)
				default:
					financialCalls.Add(1)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			runtime, input := payoutRuntime(server), payoutFixture(server.URL)
			_, e1 := runtime.CreatePayout(context.Background(), input)
			_, e2 := runtime.ReadPayout(context.Background(), input, "po_payout123")
			_, e3 := runtime.LookupPayout(context.Background(), input)
			if e1 == nil || e2 == nil || e3 == nil || financialCalls.Load() != 0 {
				t.Fatalf("changed credential accepted: %v %v %v calls=%d", e1, e2, e3, financialCalls.Load())
			}
		})
	}
}

func TestStripePayoutCreateAndKnownRead(t *testing.T) {
	for _, status := range []string{"pending", "in_transit", "paid", "failed", "canceled"} {
		t.Run(status, func(t *testing.T) {
			var input PayoutRequest
			var writes, reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if servePayoutIdentity(t, w, r) {
					return
				}
				if servePayoutReadiness(t, w, r, input) {
					return
				}
				if r.Header.Get("Stripe-Account") != input.DestinationID {
					t.Error("wrong connected account")
				}
				if r.Method == http.MethodPost {
					writes.Add(1)
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
					if r.URL.Path != "/payouts" || r.Header.Get("Idempotency-Key") != input.IdempotencyKey ||
						r.Form.Get("destination") != input.BankDestinationID || r.Form.Get("amount") != "1900" ||
						r.Form.Get("currency") != "usd" || r.Form.Get("method") != "standard" ||
						r.Form.Get("metadata[hcai_payout_request_id]") != input.PayoutRequestID.String() || len(r.Form) != 5 {
						t.Error("payout request lost its immutable binding")
					}
				} else {
					reads.Add(1)
					if r.Method != http.MethodGet || r.URL.Path != "/payouts/po_payout123" || r.Header.Get("Idempotency-Key") != "" {
						t.Error("unsafe read")
					}
				}
				item := payoutResponse(input)
				item["status"] = status
				if status == "failed" {
					item["failure_code"] = "account_closed"
				}
				_ = json.NewEncoder(w).Encode(item)
			}))
			defer server.Close()
			input = payoutFixture(server.URL)
			runtime := payoutRuntime(server)
			item, err := runtime.CreatePayout(context.Background(), input)
			if err != nil || item.Status != status || item.BankDestinationID != input.BankDestinationID || item.Destination != input.DestinationID || writes.Load() != 1 {
				t.Fatalf("create: %+v %v", item, err)
			}
			// Recovery must remain possible after the write window has closed.
			input.ReservedAt = time.Now().Add(-48 * time.Hour)
			item, err = runtime.ReadPayout(context.Background(), input, "po_payout123")
			if err != nil || item.Status != status || reads.Load() != 1 || writes.Load() != 1 {
				t.Fatalf("read: %+v %v", item, err)
			}
			if _, err := runtime.CreatePayout(context.Background(), input); err == nil || writes.Load() != 1 {
				t.Fatal("expired reservation dispatched")
			}
		})
	}
}

func TestStripePayoutRejectsUnboundResponse(t *testing.T) {
	cases := map[string]any{"id": "po_other123", "object": "transfer", "amount": 1901, "currency": "eur",
		"destination": "ba_changed123", "livemode": true, "automatic": true, "method": "instant", "type": "card",
		"metadata": map[string]string{}, "created": int64(1), "status": "unknown", "failure_code": "private detail"}
	for field, value := range cases {
		t.Run(field, func(t *testing.T) {
			var input PayoutRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if servePayoutIdentity(t, w, r) {
					return
				}
				if servePayoutReadiness(t, w, r, input) {
					return
				}
				item := payoutResponse(input)
				item[field] = value
				_ = json.NewEncoder(w).Encode(item)
			}))
			defer server.Close()
			input = payoutFixture(server.URL)
			if _, err := payoutRuntime(server).ReadPayout(context.Background(), input, "po_payout123"); err == nil {
				t.Fatal("unbound payout accepted")
			}
			if field != "id" {
				if _, err := payoutRuntime(server).CreatePayout(context.Background(), input); err == nil {
					t.Fatal("unbound creation accepted")
				}
			}
		})
	}
	for _, field := range []string{"livemode", "automatic", "created", "destination", "metadata", "method", "type", "status", "id", "object", "currency", "amount"} {
		t.Run("missing_"+field, func(t *testing.T) {
			input := payoutFixture("https://invalid.test")
			item := payoutResponse(input)
			delete(item, field)
			body, _ := json.Marshal(item)
			var raw stripePayoutResponse
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Fatal(err)
			}
			if _, err := raw.observation(input); err == nil {
				t.Fatal("missing evidence accepted")
			}
		})
	}
}

func TestStripePayoutLookupPreservesEvidence(t *testing.T) {
	for _, scenario := range []string{"found", "empty", "unrelated", "ambiguous", "duplicate", "partial_error", "malformed", "page_limit", "wrong_mode", "wrong_bank", "missing_more", "missing_data"} {
		t.Run(scenario, func(t *testing.T) {
			var input PayoutRequest
			var pages atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if servePayoutIdentity(t, w, r) {
					return
				}
				page := int(pages.Add(1))
				if r.Method != http.MethodGet || r.URL.Path != "/payouts" || r.Header.Get("Idempotency-Key") != "" ||
					r.Header.Get("Stripe-Account") != input.DestinationID || r.URL.Query().Get("limit") != "100" ||
					r.URL.Query().Get("created[gte]") != strconv.FormatInt(input.ReservedAt.Add(-5*time.Minute).Unix(), 10) ||
					r.URL.Query().Get("created[lte]") != strconv.FormatInt(input.ReservedAt.Add(stripePayoutDispatchWindow+5*time.Minute).Unix(), 10) {
					t.Error("unsafe payout lookup")
				}
				if page > 1 && r.URL.Query().Get("starting_after") != fmt.Sprintf("po_page00%d", page-1) {
					t.Error("lost pagination cursor")
				}
				if scenario == "partial_error" && page == 2 {
					http.Error(w, "private provider details", 503)
					return
				}
				item := payoutResponse(input)
				item["id"] = fmt.Sprintf("po_page00%d", page)
				more := page == 1
				if scenario == "page_limit" {
					more = true
				}
				if (page > 1 && scenario != "ambiguous" && scenario != "duplicate") || scenario == "unrelated" || scenario == "page_limit" {
					item["metadata"] = map[string]string{}
				}
				if scenario == "duplicate" {
					item["id"] = "po_page001"
				}
				if scenario == "wrong_mode" {
					item["livemode"] = true
				}
				if scenario == "wrong_bank" {
					item["destination"] = "ba_other123"
				}
				if scenario == "malformed" && page == 2 {
					delete(item, "created")
				}
				items := []map[string]any{item}
				if scenario == "empty" {
					items = []map[string]any{}
					more = false
				}
				response := map[string]any{"object": "list", "has_more": more, "data": items}
				if scenario == "missing_more" {
					delete(response, "has_more")
				}
				if scenario == "missing_data" {
					delete(response, "data")
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			input = payoutFixture(server.URL)
			result, err := payoutRuntime(server).LookupPayout(context.Background(), input)
			wantOutcome, wantCount, wantErr := "incomplete", 0, true
			switch scenario {
			case "found":
				wantOutcome, wantCount, wantErr = "found", 1, false
			case "empty", "unrelated":
				wantOutcome, wantErr = "not_found", false
			case "ambiguous":
				wantOutcome, wantCount, wantErr = "ambiguous", 2, false
			case "partial_error", "duplicate", "malformed":
				wantCount = 1
			case "page_limit":
				wantErr = false
			}
			if result.Outcome != wantOutcome || len(result.Observations) != wantCount || (err != nil) != wantErr || pages.Load() > 10 {
				t.Fatalf("lookup: %+v %v pages=%d", result, err, pages.Load())
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("provider detail leaked")
			}
		})
	}
}

func TestStripePayoutDeadlineDuringIdentity(t *testing.T) {
	var financialCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/account" {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
		if servePayoutIdentity(t, w, r) {
			return
		}
		financialCalls.Add(1)
		w.WriteHeader(500)
	}))
	defer server.Close()
	input := payoutFixture(server.URL)
	input.ReservedAt = time.Now().Add(-stripePayoutDispatchWindow + 80*time.Millisecond)
	if _, err := payoutRuntime(server).CreatePayout(context.Background(), input); err == nil || financialCalls.Load() != 0 {
		t.Fatalf("deadline crossed into payout: %v", err)
	}
}

func TestStripePayoutTransportBoundary(t *testing.T) {
	for _, scenario := range []string{"redirect", "too_large", "invalid_json", "provider_error", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			var input PayoutRequest
			var calls, targets atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if servePayoutIdentity(t, w, r) {
					return
				}
				if servePayoutReadiness(t, w, r, input) {
					return
				}
				if r.URL.Path == "/redirect" {
					targets.Add(1)
					w.WriteHeader(500)
					return
				}
				calls.Add(1)
				switch scenario {
				case "redirect":
					w.Header().Set("Location", "/redirect")
					w.WriteHeader(307)
				case "too_large":
					fmt.Fprint(w, strings.Repeat("x", maxStripeResponseBytes+1))
				case "invalid_json":
					fmt.Fprint(w, "private provider details")
				default:
					http.Error(w, "private provider details", 503)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			input = payoutFixture(server.URL)
			_, err := payoutRuntime(server).CreatePayout(ctx, input)
			wantCalls := int32(1)
			if scenario == "cancelled" {
				wantCalls = 0
			}
			if err == nil || strings.Contains(err.Error(), "private") || calls.Load() != wantCalls || targets.Load() != 0 {
				t.Fatalf("unsafe transport: err=%v calls=%d targets=%d", err, calls.Load(), targets.Load())
			}
		})
	}
}

type payoutCapabilityOnlyRuntime struct{ catalogRuntime }

func (payoutCapabilityOnlyRuntime) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{Payout: true}
}

func TestPayoutCapabilityRequiresImplementation(t *testing.T) {
	if runtimeCapabilities(catalogRuntime{provider: "stripe"}).Payout ||
		runtimeCapabilities(payoutCapabilityOnlyRuntime{catalogRuntime{provider: "stripe"}}).Payout {
		t.Fatal("runtime without bank payout implementation advertised bank payout")
	}
	if !runtimeCapabilities(NewStripeRuntime(StripeRuntimeConfig{})).Payout {
		t.Fatal("bank payout implementation not detected")
	}
}

func TestStripePayoutObservationTimeBounds(t *testing.T) {
	input := payoutFixture("https://invalid.test")
	input.ReservedAt = time.Now().Add(-48 * time.Hour)
	for _, created := range []time.Time{input.ReservedAt.Add(-6 * time.Minute), input.ReservedAt.Add(24 * time.Hour), time.Now().Add(time.Hour)} {
		item := payoutResponse(input)
		item["created"] = created.Unix()
		body, _ := json.Marshal(item)
		var raw stripePayoutResponse
		if err := json.Unmarshal(body, &raw); err != nil {
			t.Fatal(err)
		}
		if _, err := raw.observation(input); err == nil {
			t.Fatalf("accepted timestamp outside original dispatch window: %s", created)
		}
	}
}

type payoutTransportFunc func(*http.Request) (*http.Response, error)

func (f payoutTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStripePayoutPostCannotBeTransportReplayed(t *testing.T) {
	var input PayoutRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if servePayoutIdentity(t, w, r) {
			return
		}
		if servePayoutReadiness(t, w, r, input) {
			return
		}
		_ = json.NewEncoder(w).Encode(payoutResponse(input))
	}))
	defer server.Close()
	input = payoutFixture(server.URL)
	client := server.Client()
	underlying := client.Transport
	var calls atomic.Int32
	client.Transport = payoutTransportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			calls.Add(1)
			if r.GetBody != nil || r.ContentLength <= 0 {
				t.Error("transport can automatically replay payout POST")
			}
		}
		return underlying.RoundTrip(r)
	})
	runtime := NewStripeRuntime(StripeRuntimeConfig{BaseURL: server.URL, SecretKey: "payout-test-secret", APIVersion: testStripeAPIVersion, HTTPClient: client})
	if _, err := runtime.CreatePayout(context.Background(), input); err != nil || calls.Load() != 1 {
		t.Fatalf("payout: %v calls=%d", err, calls.Load())
	}
}

func TestStripePayoutIdentityReadHasFiniteDeadline(t *testing.T) {
	// A stalled platform identity read must not hang a payout recovery worker.
	// Exercise every public entry point with a client that has no timeout.
	for _, operation := range []string{"create", "read", "lookup", "readiness"} {
		for _, shortParent := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/short_parent_%t", operation, shortParent), func(t *testing.T) {
				ctx := context.Background()
				var parentDeadline time.Time
				if shortParent {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, time.Second)
					defer cancel()
					parentDeadline, _ = ctx.Deadline()
				}
				calls := 0
				client := &http.Client{Transport: payoutTransportFunc(func(req *http.Request) (*http.Response, error) {
					calls++
					if req.Method != http.MethodGet || req.URL.Path != "/account" || req.Header.Get("Stripe-Account") != "" {
						t.Error("continued to connected-account operations after identity timeout")
					}
					deadline, ok := req.Context().Deadline()
					if !ok || time.Until(deadline) > 10*time.Second {
						t.Error("platform identity read has no bounded deadline")
					}
					if shortParent && deadline.After(parentDeadline) {
						t.Error("identity read extended caller deadline")
					}
					return nil, context.DeadlineExceeded
				})}
				input := payoutFixture("https://payout.invalid")
				runtime := NewStripeRuntime(StripeRuntimeConfig{BaseURL: input.Identity.Endpoint, SecretKey: "payout-test-secret",
					APIVersion: testStripeAPIVersion, HTTPClient: client})
				var err error
				switch operation {
				case "create":
					_, err = runtime.CreatePayout(ctx, input)
				case "read":
					_, err = runtime.ReadPayout(ctx, input, "po_payout123")
				case "lookup":
					_, err = runtime.LookupPayout(ctx, input)
				case "readiness":
					_, err = runtime.ReadPayoutReadiness(ctx, input)
				}
				assertProviderFailure(t, err, "payment_timeout", true)
				if calls != 1 {
					t.Fatalf("identity timeout was not contained: %v, calls=%d", err, calls)
				}
			})
		}
	}
}
