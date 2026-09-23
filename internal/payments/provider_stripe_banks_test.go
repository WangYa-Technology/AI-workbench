package payments

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStripePayoutBankDirectoryIsCompletePrivateAndReadOnly(t *testing.T) {
	for _, scenario := range []string{"valid", "empty", "foreign", "duplicate", "malformed", "missing_more", "partial", "endless", "bad_last4", "unsafe_name", "wrong_merchant", "automatic"} {
		t.Run(scenario, func(t *testing.T) {
			var input PayoutRequest
			pages := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("Idempotency-Key") != "" {
					t.Error("bank directory attempted a write")
				}
				if scenario == "wrong_merchant" && r.URL.Path == "/account" && r.Header.Get("Stripe-Account") == "" {
					fmt.Fprint(w, `{"object":"account","id":"acct_wrong123"}`)
					return
				}
				if servePayoutIdentity(t, w, r) {
					return
				}
				if scenario == "automatic" && r.URL.Path == "/account" {
					fmt.Fprintf(w, `{"object":"account","id":%q,"payouts_enabled":true,"settings":{"payouts":{"schedule":{"interval":"daily"}}}}`, input.DestinationID)
					return
				}
				if servePayoutReadiness(t, w, r, input) {
					return
				}
				if r.URL.Path != "/accounts/"+input.DestinationID+"/external_accounts" || r.Header.Get("Stripe-Account") != "" || r.URL.Query().Get("object") != "bank_account" || r.URL.Query().Get("limit") != "100" {
					t.Errorf("incorrect bank directory request: %s", r.URL)
					w.WriteHeader(500)
					return
				}
				pages++
				bank := map[string]any{"id": fmt.Sprintf("ba_bank%06d", pages), "object": "bank_account", "account": input.DestinationID,
					"currency": "usd", "status": "verified", "bank_name": "Example Bank", "last4": "1234", "account_number": "DO_NOT_EXPOSE", "account_holder_name": "PRIVATE_HOLDER"}
				if pages > 1 && r.URL.Query().Get("starting_after") != fmt.Sprintf("ba_bank%06d", pages-1) {
					t.Error("wrong pagination cursor")
				}
				data := []map[string]any{bank}
				page := map[string]any{"object": "list", "has_more": pages == 1, "data": data}
				switch scenario {
				case "valid":
					if pages == 1 {
						bank["currency"] = "eur"
					} else {
						data = append(data, map[string]any{"id": "ba_disabled123", "object": "bank_account", "account": input.DestinationID, "currency": "usd", "status": "errored"})
						page["data"] = data
					}
				case "empty":
					page["data"] = []any{}
					page["has_more"] = false
				case "foreign":
					bank["account"] = "acct_foreign123"
				case "duplicate":
					if pages == 2 {
						bank["id"] = "ba_bank000001"
					}
				case "malformed":
					page["data"] = nil
				case "missing_more":
					delete(page, "has_more")
				case "partial":
					if pages == 2 {
						w.WriteHeader(503)
						return
					}
				case "endless":
					page["has_more"] = true
				case "bad_last4":
					bank["last4"] = "12345"
				case "unsafe_name":
					bank["bank_name"] = "Bank\u202eNAME"
				}
				_ = json.NewEncoder(w).Encode(page)
			}))
			defer server.Close()
			input = payoutFixture(server.URL)
			out, err := payoutRuntime(server).ListPayoutBanks(t.Context(), PayoutBankTargetRequest{Identity: *input.Identity, DestinationID: input.DestinationID, Currency: input.Currency})
			if scenario != "valid" && scenario != "empty" {
				if err == nil || len(out.Items) != 0 {
					t.Fatalf("invalid directory accepted: %+v %v", out, err)
				}
				if scenario == "endless" && pages != 10 {
					t.Fatalf("unbounded pages: %d", pages)
				}
				return
			}
			if err != nil || out.Items == nil || out.ObservedAt.IsZero() {
				t.Fatalf("directory: %+v %v", out, err)
			}
			if scenario == "valid" && (pages != 2 || len(out.Items) != 1 || out.Items[0].BankDestinationID != "ba_bank000002") {
				t.Fatalf("filter/pagination: %+v pages=%d", out, pages)
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "DO_NOT_EXPOSE") || strings.Contains(string(body), "PRIVATE_HOLDER") {
				t.Fatal("private bank fields exposed")
			}
		})
	}
}
