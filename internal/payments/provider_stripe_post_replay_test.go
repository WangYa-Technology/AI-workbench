package payments

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestStripeSharedPostCannotBeTransportReplayed(t *testing.T) {
	for _, path := range []string{"/checkout/sessions", "/checkout/sessions/cs_test_original/expire", "/refunds", "/transfers", "/transfers/tr_original/reversals", "/accounts", "/account_links"} {
		t.Run(path, func(t *testing.T) {
			form := url.Values{"metadata[command]": {"original"}}
			if strings.HasSuffix(path, "/expire") {
				form = nil
			}
			calls := 0
			client := &http.Client{Transport: payoutTransportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.GetBody != nil || r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
					t.Error("financial command can be automatically replayed by net/http")
				}
				if r.Method != http.MethodPost || r.URL.Path != path || r.Header.Get("Idempotency-Key") != "original-command" {
					t.Error("financial command lost target or identity")
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != form.Encode() {
					t.Errorf("request body changed: %q %v", body, err)
				}
				return nil, errors.New("connection closed before response")
			})}
			runtime := NewStripeRuntime(StripeRuntimeConfig{BaseURL: "https://stripe.example", SecretKey: "test-secret", APIVersion: testStripeAPIVersion, HTTPClient: client})
			var response map[string]any
			if err := runtime.postForm(context.Background(), path, form, "original-command", &response); err == nil || calls != 1 {
				t.Fatalf("lost response err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestStripeLostResponseOnReusedConnectionDoesNotReplay(t *testing.T) {
	for _, empty := range []bool{false, true} {
		name := "with_body"
		if empty {
			name = "empty_body"
		}
		t.Run(name, func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, "ok")
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				if posts.Add(1) == 1 {
					// Accept the command, then drop its response. A reused idle
					// connection plus an idempotency key enables Go's retry path.
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
					return
				}
				_, _ = io.WriteString(w, `{"id":"unexpected_resend"}`)
			}))
			defer server.Close()
			client := server.Client()
			warm, err := client.Get(server.URL + "/warm")
			if err != nil {
				t.Fatal(err)
			}
			_, readErr := io.Copy(io.Discard, warm.Body)
			_ = warm.Body.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			var reused atomic.Bool
			ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
				if info.Reused {
					reused.Store(true)
				}
			}})
			form := url.Values{"amount": {"100"}}
			if empty {
				form = nil
			}
			runtime := NewStripeRuntime(StripeRuntimeConfig{BaseURL: server.URL, SecretKey: "test-secret", APIVersion: testStripeAPIVersion, HTTPClient: client})
			var response map[string]any
			err = runtime.postForm(ctx, "/command", form, "original-command", &response)
			if !reused.Load() || err == nil || posts.Load() != 1 {
				t.Fatalf("connection reused=%t err=%v actual commands=%d", reused.Load(), err, posts.Load())
			}
		})
	}
}
