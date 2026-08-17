package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestAdminOperationalDiagnosticsHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	memberClient, adminClient := testHTTPClient(t), testHTTPClient(t)
	_ = registerGovernanceUser(t, memberClient, server.URL, "observability_member")
	administrator := registerGovernanceUser(t, adminClient, server.URL, "observability_admin")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	if response := requestJSON(t, memberClient, http.MethodGet, server.URL+"/api/v1/admin/observability", nil, nil); response.StatusCode != http.StatusForbidden {
		t.Fatalf("member accessed diagnostics: %d", response.StatusCode)
	}
	var diagnostics admin.OperationalDiagnostics
	response := requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/observability", nil, &diagnostics)
	if response.StatusCode != http.StatusOK || !diagnostics.DatabaseReady || !diagnostics.Audit.Valid || diagnostics.Requests.Total < 1 || diagnostics.Requests.ByStatus["4xx"] < 1 {
		t.Fatalf("diagnostics contract mismatch: status=%d item=%#v", response.StatusCode, diagnostics)
	}
	if diagnostics.Jobs.ByAttemptStatus == nil || diagnostics.Jobs.AttemptsLast24Hours < 0 || diagnostics.Jobs.LeaseRenewalsLast24Hours < 0 || diagnostics.Jobs.LeaseExpirationsLast24Hours < 0 || diagnostics.Jobs.TerminalFailuresLast24Hours < 0 {
		t.Fatalf("durable attempt diagnostics contract mismatch: %#v", diagnostics.Jobs)
	}
}
