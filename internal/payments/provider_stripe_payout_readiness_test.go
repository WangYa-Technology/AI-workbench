package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func TestStripePayoutBankTargetBeforeFunding(t *testing.T) {
	for _, scenario := range []string{"valid", "unnamed", "missing_last4", "long_last4", "unicode_last4", "control_name", "bidi_name", "long_name", "wrong_owner", "wrong_bank", "wrong_currency", "revoked", "automatic", "disabled", "wrong_merchant", "wrong_mode"} {
		t.Run(scenario, func(t *testing.T) {
			var input PayoutRequest
			var unexpected atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || (r.URL.Path == "/balance" && r.Header.Get("Stripe-Account") != "") {
					unexpected.Add(1)
					w.WriteHeader(500)
					return
				}
				if scenario == "wrong_merchant" && r.URL.Path == "/account" && r.Header.Get("Stripe-Account") == "" {
					_ = json.NewEncoder(w).Encode(map[string]any{"object": "account", "id": "acct_other"})
					return
				}
				if scenario == "wrong_mode" && r.URL.Path == "/balance" && r.Header.Get("Stripe-Account") == "" {
					_ = json.NewEncoder(w).Encode(map[string]any{"object": "balance", "livemode": true})
					return
				}
				if servePayoutIdentity(t, w, r) {
					return
				}
				if r.URL.Path == "/account" {
					interval := "manual"
					if scenario == "automatic" {
						interval = "daily"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"object": "account", "id": input.DestinationID, "payouts_enabled": scenario != "disabled",
						"settings": map[string]any{"payouts": map[string]any{"schedule": map[string]string{"interval": interval}}}})
					return
				}
				if r.URL.Path != "/accounts/"+input.DestinationID+"/external_accounts/"+input.BankDestinationID || r.Header.Get("Stripe-Account") != "" {
					unexpected.Add(1)
					w.WriteHeader(500)
					return
				}
				bank := map[string]any{"object": "bank_account", "id": input.BankDestinationID, "account": input.DestinationID, "currency": "usd", "status": "new", "bank_name": "Example Bank", "last4": "6789", "account_number": "never-persist-this"}
				switch scenario {
				case "unnamed":
					bank["bank_name"] = ""
				case "missing_last4":
					delete(bank, "last4")
				case "long_last4":
					bank["last4"] = "12345678"
				case "unicode_last4":
					bank["last4"] = "１２３４"
				case "control_name":
					bank["bank_name"] = "Bank\nspoof"
				case "bidi_name":
					bank["bank_name"] = "Bank\u202Espoof"
				case "long_name":
					bank["bank_name"] = strings.Repeat("银", 121)
				case "wrong_owner":
					bank["account"] = "acct_other"
				case "wrong_bank":
					bank["id"] = "ba_other"
				case "wrong_currency":
					bank["currency"] = "eur"
				case "revoked":
					bank["status"] = "errored"
				}
				_ = json.NewEncoder(w).Encode(bank)
			}))
			defer server.Close()
			input = payoutFixture(server.URL)
			started := time.Now()
			out, err := payoutRuntime(server).ReadPayoutBankTarget(t.Context(), PayoutBankTargetRequest{DestinationID: input.DestinationID, BankDestinationID: input.BankDestinationID, Currency: "USD", Identity: *input.Identity})
			if scenario == "valid" || scenario == "unnamed" {
				if err != nil || out.BankDestinationID != input.BankDestinationID || out.ObservedAt.Before(started) {
					t.Fatalf("bank: %+v %v", out, err)
				}
				expectedName := "Example Bank"
				if scenario == "unnamed" {
					expectedName = ""
				}
				if out.BankName != expectedName || out.Last4 != "6789" {
					t.Fatalf("wrong bank summary: %+v", out)
				}
				body, err := json.Marshal(out)
				if err != nil || strings.Contains(string(body), "never-persist-this") || strings.Contains(string(body), "account_number") {
					t.Fatal("private provider data leaked")
				}
			} else if err == nil {
				t.Fatal("invalid bank accepted")
			}
			if unexpected.Load() != 0 {
				t.Fatal("bank verification checked funded balance or made an unexpected request")
			}
		})
	}
}

func TestStripePayoutPreflight(t *testing.T) {
	for _, scenario := range []string{"ready", "validated", "verified", "wrong_account", "missing_enabled", "disabled",
		"automatic", "missing_schedule", "wrong_bank", "wrong_bank_owner", "wrong_bank_currency", "bank_errored",
		"bank_unverified", "bank_revoked", "bank_missing_status", "missing_balance_mode", "wrong_balance_mode",
		"missing_balance", "missing_amount", "insufficient", "negative", "missing_usd", "duplicate_currency"} {
		t.Run(scenario, func(t *testing.T) {
			var input PayoutRequest
			var writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if servePayoutIdentity(t, w, r) {
					return
				}
				if r.Method != http.MethodGet || r.Header.Get("Idempotency-Key") != "" {
					writes.Add(1)
					w.WriteHeader(500)
					return
				}
				var item map[string]any
				switch r.URL.Path {
				case "/account":
					if r.Header.Get("Stripe-Account") != input.DestinationID {
						t.Error("wrong account scope")
					}
					interval := "manual"
					if scenario == "automatic" {
						interval = "daily"
					}
					item = map[string]any{"id": input.DestinationID, "object": "account", "payouts_enabled": scenario != "disabled",
						"settings": map[string]any{"payouts": map[string]any{"schedule": map[string]string{"interval": interval}}}}
					if scenario == "wrong_account" {
						item["id"] = "acct_other123"
					}
					if scenario == "missing_enabled" {
						delete(item, "payouts_enabled")
					}
					if scenario == "missing_schedule" {
						delete(item, "settings")
					}
				case "/balance":
					if r.Header.Get("Stripe-Account") != input.DestinationID {
						t.Error("wrong balance scope")
					}
					funds := map[string]any{"amount": 1900, "currency": "usd"}
					if scenario == "insufficient" {
						funds["amount"] = 1899
					}
					if scenario == "negative" {
						funds["amount"] = -1
					}
					if scenario == "missing_amount" {
						delete(funds, "amount")
					}
					if scenario == "missing_usd" {
						funds["currency"] = "eur"
					}
					available := []map[string]any{funds}
					if scenario == "duplicate_currency" {
						available = append(available, funds)
					}
					item = map[string]any{"object": "balance", "livemode": scenario == "wrong_balance_mode", "available": available}
					if scenario == "missing_balance_mode" {
						delete(item, "livemode")
					}
					if scenario == "missing_balance" {
						delete(item, "available")
					}
				default:
					if r.URL.Path != "/accounts/"+input.DestinationID+"/external_accounts/"+input.BankDestinationID || r.Header.Get("Stripe-Account") != "" {
						t.Error("bank ownership was not read through original platform")
					}
					item = map[string]any{"id": input.BankDestinationID, "object": "bank_account", "account": input.DestinationID, "currency": "usd", "status": "new", "bank_name": "Example Bank", "last4": "6789"}
					switch scenario {
					case "validated", "verified":
						item["status"] = scenario
					case "wrong_bank":
						item["id"] = "ba_changed123"
					case "wrong_bank_owner":
						item["account"] = "acct_other123"
					case "wrong_bank_currency":
						item["currency"] = "eur"
					case "bank_errored":
						item["status"] = "errored"
					case "bank_unverified":
						item["status"] = "verification_failed"
					case "bank_revoked":
						item["status"] = "tokenized_account_number_deactivated"
					case "bank_missing_status":
						delete(item, "status")
					}
				}
				_ = json.NewEncoder(w).Encode(item)
			}))
			defer server.Close()
			input = payoutFixture(server.URL)
			runtime := payoutRuntime(server)
			result, err := runtime.ReadPayoutReadiness(context.Background(), input)
			if oneOf(scenario, "ready", "validated", "verified") {
				if err != nil || result.AvailableCents != 1900 || result.DestinationID != input.DestinationID ||
					result.BankDestinationID != input.BankDestinationID || result.Currency != "USD" || result.ObservedAt.IsZero() {
					t.Fatalf("preflight: %+v %v", result, err)
				}
			} else {
				if err == nil {
					t.Fatal("unready account passed preflight")
				}
				if strings.Contains(scenario, "insufficient") || scenario == "negative" || scenario == "missing_usd" {
					if !jobs.ShouldRetry(err) {
						t.Fatal("settling balance cannot be checked later")
					}
				}
				if _, err := runtime.CreatePayout(context.Background(), input); err == nil {
					t.Fatal("creation bypassed readiness")
				}
			}
			if writes.Load() != 0 {
				t.Fatal("preflight changed remote financial state")
			}
		})
	}
}

func TestStripePayoutRecoveryDoesNotDependOnCurrentBankReadiness(t *testing.T) {
	var input PayoutRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if servePayoutIdentity(t, w, r) {
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/payouts/po_payout123" {
			t.Error("recovery was gated on bank readiness")
			w.WriteHeader(403)
			return
		}
		item := payoutResponse(input)
		item["status"] = "failed"
		item["failure_code"] = "account_closed"
		_ = json.NewEncoder(w).Encode(item)
	}))
	defer server.Close()
	input = payoutFixture(server.URL)
	result, err := payoutRuntime(server).ReadPayout(context.Background(), input, "po_payout123")
	if err != nil || result.Status != "failed" || result.FailureCode != "account_closed" {
		t.Fatalf("lost failure evidence: %+v %v", result, err)
	}
}
