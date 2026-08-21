package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/support"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestAdminSupportAndRiskDirectoriesTraverseBeyondLegacyWindows(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	memberClient := testHTTPClient(t)
	member := registerGovernanceUser(t, memberClient, server.URL, "cp37_member")
	adminClient := testHTTPClient(t)
	administrator := registerGovernanceUser(t, adminClient, server.URL, "cp37_admin")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO support_cases(requester_id,category,subject,details,locale,status,version,created_at,updated_at)
		SELECT $1,'billing','CP37 support case '||value::text,
		       'CP37 support directory evidence with enough detail for a bounded operations review.','en-US','open',1,
		       now()-make_interval(secs=>value),now()-make_interval(secs=>value)
		FROM generate_series(1,206) value`, member.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO risk_signals(source_key,resource_type,resource_id,subject_user_id,signal_type,severity,score,status,summary,evidence,detected_at,updated_at)
		SELECT 'cp37-risk:'||value::text,'order',gen_random_uuid(),$1,'transaction_refund','high',80,'open',
		       'CP37 risk directory evidence '||value::text,'{}'::jsonb,
		       now()-make_interval(secs=>value),now()-make_interval(secs=>value)
		FROM generate_series(1,206) value`, member.ID); err != nil {
		t.Fatal(err)
	}

	denied := requestJSON(t, memberClient, http.MethodGet, server.URL+"/api/v1/admin/support/cases", nil, nil)
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("member opened Admin support directory: %d", denied.StatusCode)
	}
	denied = requestJSON(t, memberClient, http.MethodGet, server.URL+"/api/v1/admin/risk/signals", nil, nil)
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("member opened Admin risk directory: %d", denied.StatusCode)
	}

	type supportPage struct {
		Items      []support.Case `json:"items"`
		NextCursor *string        `json:"nextCursor"`
	}
	supportIDs := make(map[string]struct{})
	var oldestSupport support.Case
	cursor := ""
	for pageNumber := 0; ; pageNumber++ {
		var page supportPage
		endpoint := server.URL + "/api/v1/admin/support/cases?q=" + url.QueryEscape("cp37 support") + "&category=billing&limit=50"
		if cursor != "" {
			endpoint += "&cursor=" + url.QueryEscape(cursor)
		}
		response := requestJSON(t, adminClient, http.MethodGet, endpoint, nil, &page)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("support page %d: status=%d", pageNumber+1, response.StatusCode)
		}
		for _, item := range page.Items {
			if _, duplicate := supportIDs[item.ID.String()]; duplicate {
				t.Fatalf("duplicate support case %s", item.ID)
			}
			supportIDs[item.ID.String()] = struct{}{}
			oldestSupport = item
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(supportIDs) != 206 {
		t.Fatalf("support traversal returned %d cases", len(supportIDs))
	}
	invalid := requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/support/cases?cursor="+url.QueryEscape(cursor+"modified"), nil, nil)
	if invalid.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("modified support cursor accepted: %d", invalid.StatusCode)
	}
	var updatedSupport support.Case
	response := requestJSON(t, adminClient, http.MethodPatch, server.URL+"/api/v1/admin/support/cases/"+oldestSupport.ID.String(), map[string]any{
		"status": "in_review", "resolutionCode": "", "expectedVersion": 1}, &updatedSupport)
	if response.StatusCode != http.StatusOK || updatedSupport.ID != oldestSupport.ID || updatedSupport.Status != "in_review" {
		t.Fatalf("oldest support operation failed: status=%d item=%#v", response.StatusCode, updatedSupport)
	}

	type riskPage struct {
		Items      []admin.RiskSignal `json:"items"`
		NextCursor *string            `json:"nextCursor"`
	}
	riskIDs := make(map[string]struct{})
	var oldestRisk admin.RiskSignal
	cursor = ""
	for pageNumber := 0; ; pageNumber++ {
		var page riskPage
		endpoint := server.URL + "/api/v1/admin/risk/signals?q=" + url.QueryEscape("cp37 risk") + "&status=open&severity=high&limit=50"
		if cursor != "" {
			endpoint += "&cursor=" + url.QueryEscape(cursor)
		}
		response = requestJSON(t, adminClient, http.MethodGet, endpoint, nil, &page)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("risk page %d: status=%d", pageNumber+1, response.StatusCode)
		}
		for _, item := range page.Items {
			if _, duplicate := riskIDs[item.ID.String()]; duplicate {
				t.Fatalf("duplicate risk signal %s", item.ID)
			}
			riskIDs[item.ID.String()] = struct{}{}
			oldestRisk = item
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(riskIDs) != 206 {
		t.Fatalf("risk traversal returned %d signals", len(riskIDs))
	}
	invalid = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/risk/signals?cursor="+url.QueryEscape(cursor+"modified"), nil, nil)
	if invalid.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("modified risk cursor accepted: %d", invalid.StatusCode)
	}
	var reviewed admin.RiskSignal
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/risk/signals/"+oldestRisk.ID.String()+"/review", map[string]any{
		"decision": "monitor", "expectedVersion": 1}, &reviewed)
	if response.StatusCode != http.StatusOK || reviewed.ID != oldestRisk.ID || reviewed.Status != "reviewing" {
		t.Fatalf("oldest risk operation failed: status=%d item=%#v", response.StatusCode, reviewed)
	}
}
