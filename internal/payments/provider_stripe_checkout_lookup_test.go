package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStripeCheckoutLookup(t *testing.T) {
	for _, scenario := range []string{"open", "paid", "expired", "not_found", "ambiguous", "incomplete", "pagination", "duplicate_cursor", "missing_more", "wrong_created", "wrong_mode", "bad_metadata", "bad_amount", "unsafe_url", "missing_url", "url_credentials", "url_port", "remote_failure"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			input := CheckoutLookupRequest{CheckoutReadRequest: CheckoutReadRequest{PaymentID: uuid.New(), ResourceID: uuid.New(), AmountCents: 1900, Currency: "USD"}, CreatedAfter: now.Add(-time.Hour), CreatedBefore: now.Add(time.Hour)}
			metadata := map[string]string{"hcai_payment_id": input.PaymentID.String(), "hcai_resource_id": input.ResourceID.String(), "hcai_purpose": "product"}
			page, reads := 0, 0
			session := map[string]any{"id": "cs_lookup_original", "object": "checkout.session", "mode": "payment", "status": "open", "payment_status": "unpaid", "payment_intent": nil, "amount_total": 1900, "currency": "usd", "livemode": false, "expires_at": now.Add(time.Hour).Unix(), "client_reference_id": input.PaymentID.String(), "metadata": metadata, "url": "https://checkout.stripe.com/c/pay/original#valid-stripe-fragment"}
			switch scenario {
			case "paid":
				session["status"] = "complete"
				session["payment_status"] = "paid"
				session["payment_intent"] = "pi_lookup_original"
				session["url"] = nil
			case "expired":
				session["status"] = "expired"
				session["expires_at"] = now.Add(-time.Minute).Unix()
				session["url"] = nil
			case "bad_metadata":
				metadata["hcai_resource_id"] = uuid.NewString()
			case "bad_amount":
				session["amount_total"] = 1800
			case "unsafe_url":
				session["url"] = "https://checkout.stripe.com.attacker.test/steal"
			case "missing_url":
				session["url"] = nil
			case "url_credentials":
				session["url"] = "https://someone:secret@checkout.stripe.com/pay"
			case "url_port":
				session["url"] = "https://checkout.stripe.com:444/pay"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads++
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer lookup-fixture" || r.Header.Get("Stripe-Version") != testStripeAPIVersion {
					t.Error("lookup made unauthenticated/mutating call")
				}
				if scenario == "remote_failure" {
					http.Error(w, "unavailable", 503)
					return
				}
				switch r.URL.Path {
				case "/checkout/sessions":
					page++
					q := r.URL.Query()
					if q.Get("limit") != "100" || q.Get("created[gte]") != strconv.FormatInt(input.CreatedAfter.Unix(), 10) || q.Get("created[lte]") != strconv.FormatInt(input.CreatedBefore.Unix(), 10) {
						t.Error("missing bounded time filter")
					}
					item := map[string]any{"id": "cs_lookup_original", "object": "checkout.session", "created": now.Unix(), "livemode": false, "client_reference_id": input.PaymentID.String(), "metadata": metadata}
					items := []any{item}
					more := false
					switch scenario {
					case "not_found":
						item["client_reference_id"] = "unrelated"
						item["metadata"] = map[string]string{}
					case "ambiguous":
						items = append(items, map[string]any{"id": "cs_lookup_duplicate", "object": "checkout.session", "created": now.Unix(), "livemode": false, "client_reference_id": input.PaymentID.String()})
					case "incomplete":
						more = true
						item["id"] = fmt.Sprintf("cs_unrelated_%06d", page)
						item["client_reference_id"] = "unrelated"
						item["metadata"] = map[string]string{}
					case "pagination", "duplicate_cursor":
						if page == 1 {
							more = true
							item["id"] = "cs_unrelated_previous"
							item["client_reference_id"] = "unrelated"
							item["metadata"] = map[string]string{}
						}
						if page == 2 && q.Get("starting_after") != "cs_unrelated_previous" {
							t.Error("cursor not advanced")
						}
						if scenario == "duplicate_cursor" && page == 2 {
							item["id"] = "cs_unrelated_previous"
						}
					case "wrong_created":
						item["created"] = now.Add(-2 * time.Hour).Unix()
					case "wrong_mode":
						item["livemode"] = true
					}
					result := map[string]any{"object": "list", "data": items, "has_more": more}
					if scenario == "missing_more" {
						delete(result, "has_more")
					}
					json.NewEncoder(w).Encode(result)
				case "/checkout/sessions/cs_lookup_original":
					json.NewEncoder(w).Encode(session)
				case "/payment_intents/pi_lookup_original":
					json.NewEncoder(w).Encode(map[string]any{"id": "pi_lookup_original", "object": "payment_intent", "status": "succeeded", "amount": 1900, "amount_received": 1900, "currency": "usd", "livemode": false, "latest_charge": "ch_lookup_original", "metadata": metadata})
				default:
					t.Error("unexpected lookup endpoint", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			runtime := NewStripeRuntime(StripeRuntimeConfig{BaseURL: server.URL, SecretKey: "lookup-fixture", APIVersion: testStripeAPIVersion, HTTPClient: server.Client()})
			result, err := runtime.LookupProductCheckout(context.Background(), input)
			wantError := !oneOf(scenario, "open", "paid", "expired", "not_found", "ambiguous", "incomplete", "pagination")
			if (err != nil) != wantError {
				t.Fatalf("%s result=%#v err=%v", scenario, result, err)
			}
			if !wantError && !validCheckoutLookupResult(input, result) {
				t.Fatal("unusable lookup result", result)
			}
			if oneOf(scenario, "not_found", "ambiguous", "incomplete") {
				if result.Outcome != scenario || result.Observation != nil {
					t.Fatal("uncertain lookup selected a transaction", result)
				}
				if scenario == "incomplete" && (page != 10 || reads != 10) {
					t.Fatal("unbounded or incomplete scan accepted", page, reads)
				}
			}
			if result.Outcome == "found" {
				encoded, _ := json.Marshal(result)
				if strings.Contains(string(encoded), "checkout.stripe.com") || strings.Contains(string(encoded), "lookup-fixture") || strings.Contains(string(encoded), "unrelated") {
					t.Fatal("lookup evidence leaked checkout URL, credential or unrelated transactions")
				}
			}
		})
	}
}
