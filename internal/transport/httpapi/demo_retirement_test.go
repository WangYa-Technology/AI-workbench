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

func TestDevelopmentHasNoDemoLoginOrUnverifiedRegistration(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	cfg := config.Config{Environment: "development", MediaRoot: t.TempDir(), WebOrigin: "http://127.0.0.1:5173", EmailDeliveryMode: "disabled", EmailActionKey: []byte("01234567890123456789012345678901")}
	server := httptest.NewServer(httpapi.New(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	client := testHTTPClient(t)
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/demo", map[string]any{"actor": "admin"}, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("demo login still available: %d", response.StatusCode)
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/register", map[string]any{}, nil)
	if response.StatusCode != http.StatusGone {
		t.Fatalf("unverified registration still available: %d", response.StatusCode)
	}
}
