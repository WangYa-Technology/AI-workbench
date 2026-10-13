package httpapi_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestOrderListFilterHTTP(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	client := testHTTPClient(t)
	registerGovernanceUser(t, client, server.URL, "order_filter_buyer")
	for _, query := range []string{"?limit=1&limit=2", "?cursor=&cursor=x", "?cursor=" + strings.Repeat("A", 513), "?limit=abc", "?limit=51", "?cursor=modified", "?limit=1;limit=2"} {
		r := requestJSON(t, client, "GET", server.URL+"/api/v1/orders"+query, nil, nil)
		if r.StatusCode != 422 || r.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("invalid filter accepted: status=%d query length=%d", r.StatusCode, len(query))
		}
	}
	if r := requestJSON(t, client, "GET", server.URL+"/api/v1/orders?limit=20", nil, nil); r.StatusCode != 200 {
		t.Fatal("valid filter rejected", r.StatusCode)
	}
}
