package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/reconciliation"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestAdminProviderCostReconciliationHTTPBoundary(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	providerCalls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		providerCalls++
		if request.URL.Path != "/v1/organization/costs" || request.Header.Get("Authorization") != "Bearer admin-cost-test" {
			t.Errorf("unexpected cost runtime request: %s %s", request.URL.Path, request.Header.Get("Authorization"))
		}
		_, _ = writer.Write([]byte(`{"data":[],"has_more":false,"next_page":""}`))
	}))
	defer provider.Close()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
		OpenAIReconciliationEnabled: true, OpenAIReconciliationApproved: true, OpenAIAdminAPIKey: "admin-cost-test",
		OpenAIProject: "proj_cost_http_test", OpenAIBaseURL: provider.URL + "/v1", OpenAIReconciliationOverageThresholdMicros: 10_000,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	memberClient, adminClient := testHTTPClient(t), testHTTPClient(t)
	member := registerGovernanceUser(t, memberClient, server.URL, "cost_http_member")
	administrator := registerGovernanceUser(t, adminClient, server.URL, "cost_http_admin")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, time.August, 3, 0, 0, 0, 0, time.UTC)
	input := map[string]any{
		"provider": "openai", "periodStart": start.Format(time.RFC3339), "periodEnd": start.Add(24 * time.Hour).Format(time.RFC3339),
		"reason": "Reconcile the approved isolated daily OpenAI cost boundary.", "confirmed": true,
	}
	response := requestJSON(t, memberClient, http.MethodPost, server.URL+"/api/v1/admin/provider-cost-reconciliations", input, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("member requested reconciliation: %d", response.StatusCode)
	}
	var item reconciliation.Reconciliation
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/provider-cost-reconciliations", input, &item)
	if response.StatusCode != http.StatusCreated || item.Status != "queued" || item.JobID == nil || providerCalls != 0 {
		t.Fatalf("admin reconciliation request mismatch: status=%d item=%#v calls=%d", response.StatusCode, item, providerCalls)
	}
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/provider-cost-reconciliations", input, nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate reconciliation request accepted: %d", response.StatusCode)
	}
	var page reconciliation.Page
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/provider-cost-reconciliations?status=queued", nil, &page)
	if response.StatusCode != http.StatusOK || len(page.Items) != 1 || page.Items[0].ID != item.ID {
		t.Fatalf("reconciliation list mismatch: status=%d page=%#v", response.StatusCode, page)
	}
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/provider-cost-reconciliations", map[string]any{
		"provider": "openai", "periodStart": start.Format(time.RFC3339), "periodEnd": start.Add(24 * time.Hour).Format(time.RFC3339), "reason": "This must not be accepted without an explicit confirmation.", "confirmed": false,
	}, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unconfirmed reconciliation request accepted: %d", response.StatusCode)
	}
	_ = member
}

func TestAdminProviderCostReconciliationStaysUnavailableByDefault(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	adminClient := testHTTPClient(t)
	administrator := registerGovernanceUser(t, adminClient, server.URL, "cost_disabled_admin")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	response := requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/provider-cost-reconciliations", nil, nil)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("disabled reconciliation endpoint did not fail closed: %d", response.StatusCode)
	}
}
