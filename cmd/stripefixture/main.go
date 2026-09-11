package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type fixtureState struct {
	mu                    sync.Mutex
	CheckoutCalls         int    `json:"checkoutCalls"`
	RefundCalls           int    `json:"refundCalls"`
	ConnectAccountCalls   int    `json:"connectAccountCalls"`
	AccountLinkCalls      int    `json:"accountLinkCalls"`
	LastCheckoutReference string `json:"lastCheckoutReference"`
	LastCheckoutKey       string `json:"lastCheckoutIdempotencyKey"`
	LastRefundPayment     string `json:"lastRefundPaymentIntent"`
	LastRefundKey         string `json:"lastRefundIdempotencyKey"`
	LastConnectUserID     string `json:"lastConnectUserId"`
	LastConnectKey        string `json:"lastConnectIdempotencyKey"`
	LastAccountLinkID     string `json:"lastAccountLinkId"`
	LastAccountLinkKey    string `json:"lastAccountLinkIdempotencyKey"`
}

func main() {
	address := flag.String("addr", "127.0.0.1:18083", "loopback listen address")
	secretKey := flag.String("secret-key", "sk_test_payment_drill", "expected test secret")
	apiVersion := flag.String("api-version", "2026-02-25.clover", "expected pinned Stripe API version")
	flag.Parse()

	host, _, err := net.SplitHostPort(*address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		log.Fatal("stripe fixture must bind to an explicit loopback IP and port")
	}
	state := &fixtureState{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /__fixture/state", func(w http.ResponseWriter, _ *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		writeJSON(w, http.StatusOK, state)
	})
	mux.HandleFunc("POST /v1/checkout/sessions", func(w http.ResponseWriter, r *http.Request) {
		if !validHeaders(r, *secretKey, *apiVersion) || r.ParseForm() != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "fixture_request_invalid"})
			return
		}
		amount, err := strconv.Atoi(r.PostForm.Get("line_items[0][price_data][unit_amount]"))
		reference := r.PostForm.Get("client_reference_id")
		if err != nil || amount < 50 || reference == "" || r.PostForm.Get("mode") != "payment" ||
			r.PostForm.Get("line_items[0][price_data][currency]") != "usd" {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "fixture_checkout_invalid"})
			return
		}
		state.mu.Lock()
		state.CheckoutCalls++
		checkoutNumber := state.CheckoutCalls
		state.LastCheckoutReference = reference
		state.LastCheckoutKey = r.Header.Get("Idempotency-Key")
		state.mu.Unlock()
		checkoutID := fmt.Sprintf("cs_test_paymentdrill%03d", checkoutNumber)
		writeJSON(w, http.StatusOK, map[string]any{
			"id": checkoutID, "url": "https://checkout.stripe.com/c/pay/payment-drill-" + strconv.Itoa(checkoutNumber),
			"status": "open", "payment_status": "unpaid", "expires_at": time.Now().Add(time.Hour).Unix(),
			"livemode": false, "amount_total": amount, "currency": "usd", "client_reference_id": reference,
		})
	})
	mux.HandleFunc("POST /v1/refunds", func(w http.ResponseWriter, r *http.Request) {
		if !validHeaders(r, *secretKey, *apiVersion) || r.ParseForm() != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "fixture_request_invalid"})
			return
		}
		amount, err := strconv.Atoi(r.PostForm.Get("amount"))
		paymentIntent := r.PostForm.Get("payment_intent")
		if err != nil || amount < 50 || !strings.HasPrefix(paymentIntent, "pi_") {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "fixture_refund_invalid"})
			return
		}
		state.mu.Lock()
		state.RefundCalls++
		state.LastRefundPayment = paymentIntent
		state.LastRefundKey = r.Header.Get("Idempotency-Key")
		state.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"id": "re_test_paymentdrill001", "payment_intent": paymentIntent, "amount": amount,
			"currency": "usd", "status": "pending",
		})
	})
	mux.HandleFunc("POST /v1/accounts", func(w http.ResponseWriter, r *http.Request) {
		if !validHeaders(r, *secretKey, *apiVersion) || r.ParseForm() != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "fixture_request_invalid"})
			return
		}
		userID := r.PostForm.Get("metadata[hcai_user_id]")
		if userID == "" || r.PostForm.Get("type") != "express" ||
			r.PostForm.Get("capabilities[card_payments][requested]") != "true" ||
			r.PostForm.Get("capabilities[transfers][requested]") != "true" ||
			r.PostForm.Get("email") == "" || !strings.HasPrefix(r.Header.Get("Idempotency-Key"), "connect-account-") {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "fixture_connect_account_invalid"})
			return
		}
		state.mu.Lock()
		state.ConnectAccountCalls++
		state.LastConnectUserID = userID
		state.LastConnectKey = r.Header.Get("Idempotency-Key")
		state.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"id": "acct_test_paymentdrill001", "object": "account", "charges_enabled": false, "payouts_enabled": false,
			"details_submitted": false, "livemode": false,
			"requirements": map[string]any{"currently_due": []string{"individual.first_name"}, "past_due": []string{}, "pending_verification": []string{}},
		})
	})
	mux.HandleFunc("POST /v1/account_links", func(w http.ResponseWriter, r *http.Request) {
		if !validHeaders(r, *secretKey, *apiVersion) || r.ParseForm() != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "fixture_request_invalid"})
			return
		}
		if r.PostForm.Get("account") != "acct_test_paymentdrill001" || r.PostForm.Get("type") != "account_onboarding" ||
			!strings.HasPrefix(r.PostForm.Get("refresh_url"), "http://127.0.0.1:") || !strings.HasPrefix(r.PostForm.Get("return_url"), "http://127.0.0.1:") ||
			!strings.HasPrefix(r.Header.Get("Idempotency-Key"), "connect-link-") {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "fixture_account_link_invalid"})
			return
		}
		state.mu.Lock()
		state.AccountLinkCalls++
		state.LastAccountLinkID = r.PostForm.Get("account")
		state.LastAccountLinkKey = r.Header.Get("Idempotency-Key")
		linkNumber := state.AccountLinkCalls
		state.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"object": "account_link", "url": fmt.Sprintf("https://connect.stripe.com/setup/c/payment-drill-%d", linkNumber), "expires_at": time.Now().Add(10 * time.Minute).Unix(),
		})
	})

	server := &http.Server{Addr: *address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("stripe fixture listening on %s", *address)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func validHeaders(r *http.Request, secretKey, apiVersion string) bool {
	return r.Header.Get("Authorization") == "Bearer "+secretKey &&
		r.Header.Get("Stripe-Version") == apiVersion &&
		len(r.Header.Get("Idempotency-Key")) >= 8 &&
		strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("encode fixture response: %v", err)
	}
}

func (s *fixtureState) MarshalJSON() ([]byte, error) {
	type state fixtureState
	return json.Marshal((*state)(s))
}

func (s *fixtureState) String() string {
	return fmt.Sprintf("checkout=%d refund=%d connectAccounts=%d accountLinks=%d", s.CheckoutCalls, s.RefundCalls, s.ConnectAccountCalls, s.AccountLinkCalls)
}
