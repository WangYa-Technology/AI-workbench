package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestForwardedRegistrationAndLoginPreserveNetworkEvidence(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := context.Background()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173",
		TrustedProxyCIDRs: []netip.Prefix{
			netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("10.20.0.0/16"),
		},
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	// Requests emulate the headers from a trusted append-only proxy. The
	// leftmost field belongs to the caller; the later client hop does not.
	client := testHTTPClient(t)
	send := func(path string, input any, forged string) {
		t.Helper()
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, server.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Add("X-Forwarded-For", forged)
		req.Header.Add("X-Forwarded-For", "198.51.100.18, 10.20.1.2")
		req.Header.Set("X-Real-IP", forged)
		req.Header.Set("Forwarded", "for="+forged)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			t.Fatal(err)
		}
		want := http.StatusCreated
		if path == "/api/v1/auth/login" {
			want = http.StatusOK
		}
		if resp.StatusCode != want {
			t.Fatalf("identity request %s: got %d, want %d", path, resp.StatusCode, want)
		}
		cookies := resp.Cookies()
		if len(cookies) != 1 || cookies[0].Name != identity.SessionCookie {
			t.Fatal("identity request did not issue a session")
		}
		var networkHash string
		if err := pool.QueryRow(ctx, `SELECT network_hash FROM sessions WHERE token_hash=$1`,
			identity.HashToken(cookies[0].Value)).Scan(&networkHash); err != nil {
			t.Fatal(err)
		}
		if networkHash != identity.HashNetwork("198.51.100.18") {
			t.Error("session evidence was taken from an untrusted forwarded prefix")
		}
	}

	for i := 1; i <= 3; i++ {
		handle := fmt.Sprintf("forwarded_account_%d", i)
		send("/api/v1/auth/register", map[string]any{
			"email": handle + "@example.test", "password": "forwarding-test-password",
			"handle": handle, "displayName": "Forwarding Test", "locale": "en-US", "timezone": "UTC",
		}, fmt.Sprintf("203.0.113.%d", i))
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM risk_signals WHERE signal_type='account_link'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("changing caller-controlled prefixes bypassed account-link evidence: signals=%d", count)
	}
	send("/api/v1/auth/login", map[string]any{
		"email": "forwarded_account_3@example.test", "password": "forwarding-test-password",
	}, "203.0.113.99")
}
