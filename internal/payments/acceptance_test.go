package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type acceptanceFixture struct {
	mu            sync.Mutex
	calls         []string
	account       string
	link          func(http.ResponseWriter, *http.Request) bool
	delete        func(http.ResponseWriter, *http.Request) bool
	create        func(http.ResponseWriter, *http.Request) bool
	accountCreate func(http.ResponseWriter, *http.Request) bool
	modeRead      func(http.ResponseWriter, *http.Request) bool
	expire        func(http.ResponseWriter, *http.Request) bool
	observe       func(*http.Request)
}

func newAcceptanceFixture(t *testing.T, suffix string) (*acceptanceFixture, *StripeRuntime) {
	t.Helper()
	return newAcceptanceFixtureServer(t, suffix, false)
}

func newAcceptanceFixtureServer(t *testing.T, suffix string, tls bool) (*acceptanceFixture, *StripeRuntime) {
	t.Helper()
	f := &acceptanceFixture{account: "acct_acceptance" + suffix}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		if f.observe != nil {
			f.observe(r)
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/balance" && r.Method == http.MethodGet:
			if f.modeRead != nil && f.modeRead(w, r) {
				return
			}
			fmt.Fprint(w, `{"object":"balance","livemode":false}`)
		case r.URL.Path == "/v1/checkout/sessions":
			if f.create != nil && f.create(w, r) {
				return
			}
			fmt.Fprintf(w, `{"id":"cs_acceptance%s","url":"https://checkout.stripe.com/c/pay/test","status":"open","payment_status":"unpaid","expires_at":%d,"livemode":false,"amount_total":50,"currency":"usd","client_reference_id":%q}`, suffix, time.Now().Add(time.Hour).Unix(), r.Form.Get("client_reference_id"))
		case strings.HasSuffix(r.URL.Path, "/expire"):
			if f.expire != nil && f.expire(w, r) {
				return
			}
			fmt.Fprintf(w, `{"id":"cs_acceptance%s","status":"expired","livemode":false,"payment_status":"unpaid"}`, suffix)
		case r.URL.Path == "/v1/accounts" && r.Method == http.MethodPost:
			if f.accountCreate != nil && f.accountCreate(w, r) {
				return
			}
			fmt.Fprintf(w, `{"id":%q,"object":"account","type":"express","metadata":{"hcai_user_id":%q},"charges_enabled":false,"payouts_enabled":false,"details_submitted":false,"requirements":{"currently_due":[],"past_due":[],"pending_verification":[]}}`, f.account, r.PostForm.Get("metadata[hcai_user_id]"))
		case r.URL.Path == "/v1/account_links":
			if f.link != nil && f.link(w, r) {
				return
			}
			fmt.Fprintf(w, `{"object":"account_link","url":"https://connect.stripe.com/setup/c/acceptance","expires_at":%d}`, time.Now().Add(time.Minute).Unix())
		case r.Method == http.MethodDelete:
			if f.delete != nil && f.delete(w, r) {
				return
			}
			if r.URL.Path != "/v1/accounts/"+f.account {
				http.Error(w, "wrong account", http.StatusNotFound)
				return
			}
			fmt.Fprintf(w, `{"id":%q,"deleted":true}`, f.account)
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	if tls {
		server.EnableHTTP2 = true
		server.StartTLS()
	} else {
		server.Start()
	}
	t.Cleanup(server.Close)
	return f, testStripeRuntime(server)
}

func (f *acceptanceFixture) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func TestStripeAcceptanceConcurrentCleanupOwnership(t *testing.T) {
	a, first := newAcceptanceFixture(t, "first")
	_, second := newAcceptanceFixture(t, "second")
	ready, release := make(chan struct{}), make(chan struct{})
	a.link = func(w http.ResponseWriter, r *http.Request) bool {
		close(ready)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		http.Error(w, "link unavailable", http.StatusServiceUnavailable)
		return true
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	completed := make(chan error, 1)
	go func() {
		_, err := RunStripeStagingAcceptance(ctx, first)
		completed <- err
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("first acceptance never reached account link")
	}
	_, secondErr := RunStripeStagingAcceptance(ctx, second)
	close(release)
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	if err := <-completed; err == nil {
		t.Fatal("failed first acceptance reported success")
	}
	requests := a.requests()
	if got := requests[len(requests)-1]; got != "DELETE /v1/accounts/"+a.account {
		t.Fatalf("cross-run account cleanup: %s", got)
	}
}

func TestStripeAcceptanceDeleteFailureRespectsCallBudget(t *testing.T) {
	f, runtime := newAcceptanceFixture(t, "budget")
	f.delete = func(w http.ResponseWriter, _ *http.Request) bool {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return true
	}
	result, err := RunStripeStagingAcceptance(t.Context(), runtime)
	if err == nil || result.Status == "passed" {
		t.Fatal("failed account cleanup passed acceptance")
	}
	if got := len(f.requests()); got != StripeStagingAcceptanceRequestCount || result.RequestCount != got {
		t.Fatalf("approved budget=%d actual=%d reported=%d", StripeStagingAcceptanceRequestCount, got, result.RequestCount)
	}
	if result.CleanupComplete || len(result.PendingCleanup) != 1 || result.PendingCleanup[0].ID != f.account || result.PendingCleanup[0].Kind != "connected_account" {
		t.Fatalf("failed cleanup was not reported: %#v", result)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "sk_test_") || strings.Contains(string(encoded), "https://") {
		t.Fatal("summary exposed a credential or onboarding/payment URL")
	}
}

func TestStripeAcceptanceSuccessAndModeGuards(t *testing.T) {
	f, runtime := newAcceptanceFixture(t, "success")
	result, err := RunStripeStagingAcceptance(t.Context(), runtime)
	if err != nil || result.Status != "passed" || !result.CleanupComplete || !result.CheckoutExpired || !result.ConnectAccountDeleted || result.ChargeCreated || result.RequestCount != 6 || len(f.requests()) != 6 {
		t.Fatalf("acceptance result: %#v err=%v", result, err)
	}
	for _, invalid := range []*StripeRuntime{nil, NewStripeRuntime(StripeRuntimeConfig{SecretKey: "sk_live_never_use"}), NewStripeRuntime(StripeRuntimeConfig{SecretKey: "sk_test_never_use", LiveMode: true})} {
		result, err := RunStripeStagingAcceptance(t.Context(), invalid)
		if err == nil || result.RequestCount != 0 || result.Status == "passed" {
			t.Fatalf("invalid mode admitted: %#v %v", result, err)
		}
	}
}

func TestStripeAcceptanceFailedExpiryCleanupIsCounted(t *testing.T) {
	f, runtime := newAcceptanceFixture(t, "expire")
	attempts := 0
	f.expire = func(w http.ResponseWriter, _ *http.Request) bool {
		attempts++
		if attempts == 1 {
			http.Error(w, "response unavailable", http.StatusServiceUnavailable)
			return true
		}
		return false
	}
	result, err := RunStripeStagingAcceptance(t.Context(), runtime)
	if err == nil || result.Status != "failed" || !result.CheckoutExpired || !result.CleanupComplete || result.RequestCount != 4 || len(f.requests()) != 4 || result.ConnectAccountCreated {
		t.Fatalf("cleanup result: %#v err=%v", result, err)
	}
}

func TestStripeAcceptanceModeReadPrecedesAllWrites(t *testing.T) {
	for _, response := range []string{`{"object":"balance","livemode":true}`, `{"object":"balance"}`, `{"object":"account","livemode":false}`} {
		t.Run(response, func(t *testing.T) {
			f, runtime := newAcceptanceFixture(t, "mode")
			f.modeRead = func(w http.ResponseWriter, _ *http.Request) bool {
				fmt.Fprint(w, response)
				return true
			}
			result, err := RunStripeStagingAcceptance(t.Context(), runtime)
			requests := f.requests()
			if err == nil || result.RequestCount != 1 || len(requests) != 1 || requests[0] != "GET /v1/balance" ||
				!result.CleanupComplete || len(result.UnconfirmedCreations) != 0 || result.ConnectAccountCreated {
				t.Fatalf("mode preflight created objects: %+v requests=%v err=%v", result, requests, err)
			}
		})
	}
}

type acceptanceTransportFunc func(*http.Request) (*http.Response, error)

func (f acceptanceTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStripeAcceptanceCleanupAfterCallerCancellation(t *testing.T) {
	f, runtime := newAcceptanceFixture(t, "cancelled")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.link = func(w http.ResponseWriter, _ *http.Request) bool {
		cancel()
		http.Error(w, "interrupted", http.StatusServiceUnavailable)
		return true
	}
	result, err := RunStripeStagingAcceptance(ctx, runtime)
	if err == nil || !result.ConnectAccountDeleted || !result.CleanupComplete || result.Status != "failed" || result.RequestCount != 6 || len(f.requests()) != 6 {
		t.Fatalf("cancelled acceptance result: %#v err=%v", result, err)
	}
}

func TestStripeAcceptanceCleanupDeadline(t *testing.T) {
	f, runtime := newAcceptanceFixture(t, "deadline")
	runtime.client.Timeout = 0 // Cleanup must remain bounded without a client deadline.
	f.link = func(w http.ResponseWriter, _ *http.Request) bool {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return true
	}
	f.delete = func(w http.ResponseWriter, r *http.Request) bool {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * stripeStagingCleanupTimeout):
		}
		return true
	}
	started := time.Now()
	result, err := RunStripeStagingAcceptance(t.Context(), runtime)
	if elapsed := time.Since(started); elapsed < stripeStagingCleanupTimeout || elapsed > stripeStagingCleanupTimeout+3*time.Second {
		t.Errorf("cleanup deadline was not enforced: %s", elapsed)
	}
	if err == nil || result.CleanupComplete || result.ConnectAccountDeleted || len(result.PendingCleanup) != 1 || result.RequestCount != 6 || len(f.requests()) != 6 {
		t.Fatalf("timed-out cleanup result: %#v err=%v", result, err)
	}
}

func TestStripeAcceptanceRejectsUnboundedTransport(t *testing.T) {
	f, runtime := newAcceptanceFixture(t, "customtransport")
	runtime.client.Transport = acceptanceTransportFunc(func(*http.Request) (*http.Response, error) {
		t.Error("unsupported transport was invoked")
		return nil, fmt.Errorf("unsupported")
	})
	result, err := RunStripeStagingAcceptance(t.Context(), runtime)
	if err == nil || result.Status != "failed" || result.RequestCount != 0 || !result.CleanupComplete || len(f.requests()) != 0 {
		t.Fatalf("unbounded transport was admitted: %#v err=%v", result, err)
	}
}

func TestStripeAcceptanceCleanupFailureIsRetained(t *testing.T) {
	f, runtime := newAcceptanceFixture(t, "cleanupfailed")
	f.link = func(w http.ResponseWriter, _ *http.Request) bool {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return true
	}
	f.delete = func(w http.ResponseWriter, _ *http.Request) bool {
		http.Error(w, "also unavailable", http.StatusServiceUnavailable)
		return true
	}
	result, err := RunStripeStagingAcceptance(t.Context(), runtime)
	if err == nil || !strings.Contains(err.Error(), "create Stripe test Account Link") || !strings.Contains(err.Error(), "clean up Stripe test connected_account") || result.CleanupComplete || len(result.PendingCleanup) != 1 || result.RequestCount != 6 || len(f.requests()) != 6 {
		t.Fatalf("cleanup failure was hidden: %#v err=%v", result, err)
	}
}

func TestStripeAcceptanceUnknownCreationOutcomeKeepsCommandReference(t *testing.T) {
	for _, kind := range []string{"checkout_session", "connected_account"} {
		t.Run(kind, func(t *testing.T) {
			f, runtime := newAcceptanceFixture(t, strings.ReplaceAll(kind, "_", ""))
			var command string
			fail := func(w http.ResponseWriter, r *http.Request) bool {
				command = r.Header.Get("Idempotency-Key")
				http.Error(w, "request result unavailable", http.StatusServiceUnavailable)
				return true
			}
			wantCalls := 2
			if kind == "checkout_session" {
				f.create = fail
			} else {
				f.accountCreate = fail
				wantCalls = 4
			}
			result, err := RunStripeStagingAcceptance(t.Context(), runtime)
			if err == nil || result.CleanupComplete || len(result.UnconfirmedCreations) != 1 || result.UnconfirmedCreations[0].Kind != kind || command == "" || result.UnconfirmedCreations[0].IdempotencyKey != command || result.RequestCount != wantCalls || len(f.requests()) != wantCalls {
				t.Fatalf("uncertain creation evidence missing: %#v err=%v", result, err)
			}
		})
	}
}

func TestStripeAcceptanceDisconnectDoesNotReplay(t *testing.T) {
	for _, kind := range []string{"connected_account", "delete_account"} {
		t.Run(kind, func(t *testing.T) {
			f, runtime := newAcceptanceFixture(t, "disconnect")
			var attempts atomic.Int32
			disconnect := func(w http.ResponseWriter, r *http.Request) bool {
				if attempts.Add(1) != 1 {
					return false
				}
				// Simulate a provider accepting the operation but losing its
				// response on a connection used by the previous operation.
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return true
				}
				_ = conn.Close()
				return true
			}
			wantCalls := 4
			if kind == "connected_account" {
				f.accountCreate = disconnect
			} else {
				f.delete = disconnect
				wantCalls = 6
			}
			result, err := RunStripeStagingAcceptance(t.Context(), runtime)
			if err == nil || result.Status != "failed" || result.CleanupComplete || attempts.Load() != 1 || len(f.requests()) != wantCalls || result.RequestCount != wantCalls {
				t.Fatalf("transport replayed an uncertain operation: attempts=%d requests=%v result=%#v err=%v", attempts.Load(), f.requests(), result, err)
			}
		})
	}
}

func TestStripeAcceptanceIsolatesHTTPSConnections(t *testing.T) {
	f, runtime := newAcceptanceFixtureServer(t, "tls", true)
	original := runtime.client.Transport.(*http.Transport)
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	original.Protocols = protocols
	original.TLSClientConfig.NextProtos = []string{"h2", "http/1.1"}
	var mu sync.Mutex
	connections := make(map[string]bool)
	f.observe = func(r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.ProtoMajor != 1 || r.TLS == nil || !r.Close || connections[r.RemoteAddr] {
			t.Errorf("acceptance did not use fresh HTTP/1 TLS connection: protocol=%s TLS=%t close=%t reused=%t", r.Proto, r.TLS != nil, r.Close, connections[r.RemoteAddr])
		}
		connections[r.RemoteAddr] = true
	}
	result, err := RunStripeStagingAcceptance(t.Context(), runtime)
	if err != nil || result.Status != "passed" || result.RequestCount != 6 {
		t.Fatalf("HTTPS acceptance failed: %#v err=%v", result, err)
	}
	if runtime.client.Transport != original || original.DisableKeepAlives || !original.Protocols.HTTP2() || strings.Join(original.TLSClientConfig.NextProtos, ",") != "h2,http/1.1" {
		t.Fatal("acceptance changed caller's connection/protocol configuration")
	}
}
