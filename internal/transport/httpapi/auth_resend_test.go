package httpapi_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestAuthCodeResendReturnsActionableRateLimit(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "development", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173",
		EmailDeliveryMode: "local_file", EmailActionKey: []byte("01234567890123456789012345678901"),
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	client := testHTTPClient(t)
	input := map[string]any{"email": "resend-http@example.test", "locale": "en-US"}
	var started struct {
		Challenge struct {
			ResendAfterSeconds int `json:"resendAfterSeconds"`
		} `json:"challenge"`
	}
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/unified/start", input, &started)
	if response.StatusCode != http.StatusOK || started.Challenge.ResendAfterSeconds != 30 {
		t.Fatalf("initial challenge response: %d %+v", response.StatusCode, started)
	}
	for _, route := range []string{"start", "send-code"} {
		t.Run(route, func(t *testing.T) {
			body := map[string]any{"email": " RESEND-HTTP@example.test ", "locale": "en-US"}
			if route == "send-code" {
				body["purpose"] = "registration_code"
			}
			var failure struct {
				Error struct {
					Code      string `json:"code"`
					Retryable bool   `json:"retryable"`
				} `json:"error"`
			}
			response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/unified/"+route, body, &failure)
			if response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") != "30" || failure.Error.Code != "auth_code_resend_limited" || !failure.Error.Retryable {
				t.Fatalf("resend guidance mismatch: status=%d retryAfter=%q error=%+v", response.StatusCode, response.Header.Get("Retry-After"), failure.Error)
			}
		})
	}
}
