package httpapi_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestPointEntriesHTTPPagination(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	client := testHTTPClient(t)
	owner := registerGovernanceUser(t, client, server.URL, "pointpages")
	if _, err := pool.Exec(t.Context(), `INSERT INTO point_entries(user_id,operation_id,entry_type,direction,amount_points,balance_after_points,description) SELECT $1,gen_random_uuid(),'admin_adjustment','credit',1,1,'Point page' FROM generate_series(1,3)`, owner.ID); err != nil {
		t.Fatal(err)
	}
	var first billing.PointOverview
	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/billing/points?limit=2", nil, &first)
	if response.StatusCode != http.StatusOK || len(first.Entries) != 2 || first.NextEntryCursor == nil {
		t.Fatalf("first page: status=%d page=%#v", response.StatusCode, first)
	}
	var second billing.PointOverview
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/billing/points?limit=2&cursor="+url.QueryEscape(*first.NextEntryCursor), nil, &second)
	if response.StatusCode != http.StatusOK || len(second.Entries) == 0 {
		t.Fatalf("second page: status=%d page=%#v", response.StatusCode, second)
	}
	for _, entry := range second.Entries {
		for _, earlier := range first.Entries {
			if entry.ID == earlier.ID {
				t.Fatal("duplicate paged point entry")
			}
		}
	}
	for _, query := range []string{"limit=0", "limit=-1", "limit=51", "limit=invalid", "cursor=invalid"} {
		response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/billing/points?"+query, nil, nil)
		if response.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("query %s: %d", query, response.StatusCode)
		}
	}
	response = requestJSON(t, testHTTPClient(t), http.MethodGet, server.URL+"/api/v1/billing/points", nil, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous access: %d", response.StatusCode)
	}
}
