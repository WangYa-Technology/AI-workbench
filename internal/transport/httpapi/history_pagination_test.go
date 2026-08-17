package httpapi_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestOwnerHistoryPaginationValidationHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	client := testHTTPClient(t)
	registerGovernanceUser(t, client, server.URL, "history_http_owner")
	postID := uuid.NewString()
	paths := []string{
		"/api/v1/community/posts",
		"/api/v1/community/posts/" + postID + "/comments",
		"/api/v1/content-drafts",
		"/api/v1/orders",
		"/api/v1/account/sessions",
		"/api/v1/notification-deliveries",
	}
	for _, path := range paths {
		response := requestJSON(t, client, http.MethodGet, server.URL+path+"?limit=51", nil, nil)
		if response.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("oversized history page accepted for %s: %d", path, response.StatusCode)
		}
		response = requestJSON(t, client, http.MethodGet, server.URL+path+"?cursor=modified", nil, nil)
		if response.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("modified history cursor accepted for %s: %d", path, response.StatusCode)
		}
	}
	response := requestJSON(t, testHTTPClient(t), http.MethodGet, server.URL+"/api/v1/community/posts?limit=20", nil, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("anonymous Community feed access failed: %d", response.StatusCode)
	}
	response = requestJSON(t, testHTTPClient(t), http.MethodGet, server.URL+"/api/v1/notification-deliveries", nil, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous delivery evidence access succeeded: %d", response.StatusCode)
	}
}
