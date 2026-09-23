package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestAdminGovernanceDirectoryHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	memberClient, adminClient := testHTTPClient(t), testHTTPClient(t)
	_ = registerGovernanceUser(t, memberClient, server.URL, "gov_directory_member")
	administrator := registerGovernanceUser(t, adminClient, server.URL, "gov_directory_admin")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/admin/governance/reports", "/api/v1/admin/governance/appeals"} {
		response := requestJSON(t, memberClient, http.MethodGet, server.URL+path, nil, nil)
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("member accessed %s: %d", path, response.StatusCode)
		}
	}

	ctx := context.Background()
	subjectID, appellantID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status,created_at) VALUES
		($1,$2,$3,'HTTP Governance Subject','creator','active',now() - interval '4 hours'),
		($4,$5,$6,'HTTP Governance Appellant','creator','active',now() - interval '4 hours')`,
		subjectID, subjectID.String()+"@test.local", "http_gov_subject_"+subjectID.String()[:8],
		appellantID, appellantID.String()+"@test.local", "http_gov_appellant_"+appellantID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(email,handle,display_name,role,status,created_at)
		SELECT 'http-gov-'||value||'@test.local','http_gov_reporter_'||value,'HTTP Governance Reporter '||value,'member','active',now() - interval '3 hours'
		FROM generate_series(1,21) value`); err != nil {
		t.Fatal(err)
	}
	assetID, workID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,created_at)
		VALUES($1,$2,'image','HTTP governance source','/media/http-governance-source.jpg','image/jpeg','clean','delivery','personal',now() - interval '4 hours')`, assetID, subjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO works(id,author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at,created_at,updated_at)
		VALUES($1,$2,$3,'HTTP governance backlog','HTTP governance evidence','Local Test','published','HTTP governance disclosure',now() - interval '3 hours',now() - interval '3 hours',now() - interval '3 hours')`, workID, subjectID, assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO content_reports(reporter_id,resource_type,resource_id,subject_author_id,category,details,status,created_at,updated_at)
		SELECT id,'work',$1,$2,'spam','HTTP governance backlog details','open',
		       CASE WHEN handle='http_gov_reporter_21' THEN now() - interval '2 hours' ELSE now() - interval '1 hour' END,
		       CASE WHEN handle='http_gov_reporter_21' THEN now() - interval '2 hours' ELSE now() - interval '1 hour' END
		FROM users WHERE handle LIKE 'http_gov_reporter_%'`, workID, subjectID); err != nil {
		t.Fatal(err)
	}
	var targetReportID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT r.id FROM content_reports r JOIN users u ON u.id=r.reporter_id WHERE u.handle='http_gov_reporter_21'`).Scan(&targetReportID); err != nil {
		t.Fatal(err)
	}

	type reportPage struct {
		Items      []admin.GovernanceReport `json:"items"`
		NextCursor *string                  `json:"nextCursor"`
	}
	var firstReport reportPage
	response := requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/governance/reports?q=http+governance&type=work&category=spam&status=open&limit=10", nil, &firstReport)
	if response.StatusCode != http.StatusOK || len(firstReport.Items) != 10 || firstReport.NextCursor == nil {
		t.Fatalf("first report page failed: status=%d page=%#v", response.StatusCode, firstReport)
	}
	var secondReport reportPage
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/governance/reports?q=http+governance&type=work&category=spam&status=open&limit=10&cursor="+url.QueryEscape(*firstReport.NextCursor), nil, &secondReport)
	if response.StatusCode != http.StatusOK || len(secondReport.Items) != 10 || secondReport.NextCursor == nil {
		t.Fatalf("second report page failed: status=%d page=%#v", response.StatusCode, secondReport)
	}
	var thirdReport reportPage
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/governance/reports?q=http+governance&type=work&category=spam&status=open&limit=10&cursor="+url.QueryEscape(*secondReport.NextCursor), nil, &thirdReport)
	if response.StatusCode != http.StatusOK || len(thirdReport.Items) != 1 || thirdReport.Items[0].ID != targetReportID || thirdReport.NextCursor != nil {
		t.Fatalf("third report page failed: status=%d page=%#v", response.StatusCode, thirdReport)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/governance/reports?category=malware", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid report filter status: %d", response.StatusCode)
	}
	var resolvedReport admin.GovernanceReport
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/governance/reports/"+targetReportID.String()+"/resolve", map[string]any{
		"reason": "Reviewed the evidence and selected this decision.", "confirm": true, "expectedVersion": 1, "outcome": "no_action"}, &resolvedReport)
	if response.StatusCode != http.StatusOK || resolvedReport.ID != targetReportID || resolvedReport.Status != "dismissed" {
		t.Fatalf("exact HTTP report response failed: status=%d item=%#v", response.StatusCode, resolvedReport)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE content_reports SET status='resolved',outcome='hidden',previous_status='published',moderator_id=$1,
		resolution_reason='Prepared HTTP appeal queue.',resolved_at=now(),updated_at=now() WHERE id<>$2`, administrator.ID, targetReportID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO moderation_appeals(report_id,appellant_id,reason,status,created_at)
		SELECT id,$1,'HTTP governance backlog appeal','pending',CASE WHEN id=$2 THEN now() - interval '2 hours' ELSE now() - interval '1 hour' END
		FROM content_reports`, appellantID, targetReportID); err != nil {
		t.Fatal(err)
	}
	var targetAppealID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM moderation_appeals WHERE report_id=$1`, targetReportID).Scan(&targetAppealID); err != nil {
		t.Fatal(err)
	}

	type appealPage struct {
		Items      []admin.GovernanceAppeal `json:"items"`
		NextCursor *string                  `json:"nextCursor"`
	}
	var firstAppeal appealPage
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/governance/appeals?q=http+governance&type=work&status=pending&limit=10", nil, &firstAppeal)
	if response.StatusCode != http.StatusOK || len(firstAppeal.Items) != 10 || firstAppeal.NextCursor == nil {
		t.Fatalf("first appeal page failed: status=%d page=%#v", response.StatusCode, firstAppeal)
	}
	var secondAppeal appealPage
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/governance/appeals?q=http+governance&type=work&status=pending&limit=10&cursor="+url.QueryEscape(*firstAppeal.NextCursor), nil, &secondAppeal)
	if response.StatusCode != http.StatusOK || len(secondAppeal.Items) != 10 || secondAppeal.NextCursor == nil {
		t.Fatalf("second appeal page failed: status=%d page=%#v", response.StatusCode, secondAppeal)
	}
	var thirdAppeal appealPage
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/governance/appeals?q=http+governance&type=work&status=pending&limit=10&cursor="+url.QueryEscape(*secondAppeal.NextCursor), nil, &thirdAppeal)
	if response.StatusCode != http.StatusOK || len(thirdAppeal.Items) != 1 || thirdAppeal.Items[0].ID != targetAppealID || thirdAppeal.NextCursor != nil {
		t.Fatalf("third appeal page failed: status=%d page=%#v", response.StatusCode, thirdAppeal)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/governance/appeals?status=reviewing", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid appeal filter status: %d", response.StatusCode)
	}
	var resolvedAppeal admin.GovernanceAppeal
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/governance/appeals/"+targetAppealID.String()+"/resolve", map[string]any{
		"reason": "Reviewed the evidence and selected this decision.", "confirm": true, "expectedVersion": 1, "decision": "denied"}, &resolvedAppeal)
	if response.StatusCode != http.StatusOK || resolvedAppeal.ID != targetAppealID || resolvedAppeal.Status != "denied" {
		t.Fatalf("exact HTTP appeal response failed: status=%d item=%#v", response.StatusCode, resolvedAppeal)
	}
}
