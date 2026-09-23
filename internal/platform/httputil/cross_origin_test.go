package httputil_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

func TestBrowserWritesCheckOriginAndKeepExactCallbackExceptions(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, origin, site string
		allowed                          bool
	}{
		{"same-origin page", "POST", "/api/v1/auth/logout", "https://app.example.test", "same-origin", true},
		{"configured frontend on separate host", "POST", "/api/v1/orders/id/refund", "https://app.example.test", "cross-site", true},
		{"sibling domain", "POST", "/api/v1/auth/logout", "https://other.example.test", "same-site", false},
		{"foreign site", "POST", "/api/v1/auth/login", "https://evil.test", "cross-site", false},
		{"opaque origin", "POST", "/api/v1/auth/login", "null", "cross-site", false},
		{"legacy browser origin", "POST", "/api/v1/auth/logout", "https://evil.test", "", false},
		{"legacy configured frontend", "POST", "/api/v1/orders/id/refund", "https://app.example.test", "", true},
		{"metadata without origin", "DELETE", "/api/v1/account/session/id", "", "cross-site", false},
		{"patch write", "PATCH", "/api/v1/account/profile", "https://evil.test", "cross-site", false},
		{"put write", "PUT", "/api/v1/seller/products/id", "https://evil.test", "cross-site", false},
		{"non-browser client", "POST", "/api/v1/auth/login", "", "", true},
		{"safe catalog read", "GET", "/api/v1/products", "https://evil.test", "cross-site", true},
		{"safe head", "HEAD", "/api/v1/assets/id/content", "https://evil.test", "cross-site", true},
		{"safe preflight", "OPTIONS", "/api/v1/orders/id/refund", "https://evil.test", "cross-site", true},
		{"signed callback path", "POST", "/api/v1/payments/webhooks/stripe", "https://provider.test", "cross-site", true},
		{"callback descendant is not exempt", "POST", "/api/v1/payments/webhooks/stripe/extra", "https://evil.test", "cross-site", false},
		{"callback prefix is not exempt", "POST", "/api/v1/payments/webhooks", "https://evil.test", "cross-site", false},
		{"callback wrong method is not exempt", "PUT", "/api/v1/payments/webhooks/stripe", "https://evil.test", "cross-site", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			handler := httputil.ProtectBrowserWrites("https://app.example.test/", "POST /api/v1/payments/webhooks/stripe")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(tc.method, "https://api.example.test"+tc.path, nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				req.Header.Set("Sec-Fetch-Site", tc.site)
			}
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if called != tc.allowed {
				t.Fatalf("business handler called=%t, allowed=%t", called, tc.allowed)
			}
			if tc.allowed {
				if res.Code != http.StatusNoContent {
					t.Fatalf("allowed request status=%d", res.Code)
				}
				return
			}
			var failure struct {
				Error httputil.Error `json:"error"`
			}
			if err := json.Unmarshal(res.Body.Bytes(), &failure); err != nil {
				t.Fatal(err)
			}
			if res.Code != http.StatusForbidden || failure.Error.Code != "cross_origin_request" || failure.Error.Retryable || res.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatalf("denial contract: status=%d error=%+v", res.Code, failure.Error)
			}
		})
	}
}
