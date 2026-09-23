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

func TestStripeConnectAccountEvidence(t *testing.T) {
	for _, scenario := range []string{"valid_test", "valid_live", "valid_verified", "wrong_mode", "missing_mode", "null_mode", "wrong_balance_object", "balance_unavailable",
		"wrong_user", "wrong_object", "wrong_type", "missing_metadata", "missing_charges_enabled", "null_payouts_enabled", "missing_details_submitted", "missing_requirements", "null_requirements", "empty_requirements"} {
		t.Run(scenario, func(t *testing.T) {
			user := uuid.New()
			live := scenario == "valid_live"
			balance := map[string]any{"object": "balance", "livemode": live}
			response := map[string]any{"id": "acct_evidence", "object": "account", "type": "express",
				"metadata": map[string]string{"hcai_user_id": user.String()}, "charges_enabled": false,
				"payouts_enabled": false, "details_submitted": false,
				"requirements": map[string]any{"currently_due": []string{"individual.first_name"}, "past_due": []string{}, "pending_verification": []string{}, "disabled_reason": nil}}
			switch scenario {
			case "valid_verified":
				response["charges_enabled"], response["payouts_enabled"], response["details_submitted"] = true, true, true
				response["requirements"].(map[string]any)["currently_due"] = []string{}
			case "empty_requirements":
				response["requirements"] = map[string]any{}
			case "wrong_mode":
				balance["livemode"] = true
			case "missing_mode":
				delete(balance, "livemode")
			case "null_mode":
				balance["livemode"] = nil
			case "wrong_balance_object":
				balance["object"] = "account"
			case "wrong_user":
				response["metadata"] = map[string]string{"hcai_user_id": uuid.NewString()}
			case "wrong_object":
				response["object"] = "customer"
			case "wrong_type":
				response["type"] = "standard"
			default:
				if field, ok := strings.CutPrefix(scenario, "missing_"); ok {
					delete(response, field)
				} else if field, ok := strings.CutPrefix(scenario, "null_"); ok {
					response[field] = nil
				}
			}
			reads, writes := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/balance":
					reads++
					if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer sk_test_contract" || r.Header.Get("Stripe-Version") != testStripeAPIVersion {
						t.Error("invalid authenticated mode read")
					}
					if scenario == "balance_unavailable" {
						http.Error(w, "unavailable", http.StatusServiceUnavailable)
						return
					}
					_ = json.NewEncoder(w).Encode(balance)
				case "/v1/accounts":
					writes++
					if r.Method != http.MethodPost {
						t.Error("invalid account creation method")
					}
					assertStripeHeaders(t, r, "connect-account-"+user.String())
					_ = json.NewEncoder(w).Encode(response)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			runtime := testStripeRuntime(server)
			runtime.config.LiveMode = live
			account, err := runtime.CreateConnectAccount(context.Background(), ConnectAccountRequest{UserID: user})
			valid := strings.HasPrefix(scenario, "valid_") || scenario == "missing_requirements" || scenario == "null_requirements" || scenario == "empty_requirements"
			if valid {
				if err != nil || account.ID != "acct_evidence" || account.LiveMode != live || account.RequirementsDue != (scenario != "valid_verified") ||
					account.ChargesEnabled != (scenario == "valid_verified") || account.PayoutsEnabled != (scenario == "valid_verified") || account.DetailsSubmitted != (scenario == "valid_verified") {
					t.Errorf("valid official account shape rejected: %+v err=%v", account, err)
				}
			} else if err == nil || account != (ConnectAccount{}) {
				t.Errorf("untrusted account accepted: %+v err=%v", account, err)
			}
			wantWrites := 1
			if scenario == "wrong_mode" || scenario == "missing_mode" || scenario == "null_mode" || scenario == "wrong_balance_object" || scenario == "balance_unavailable" {
				wantWrites = 0
			}
			if reads != 1 || writes != wantWrites {
				t.Errorf("reads=%d writes=%d wantWrites=%d", reads, writes, wantWrites)
			}
		})
	}
}
