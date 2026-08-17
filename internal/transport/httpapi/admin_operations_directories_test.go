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

func TestAdminOperationsDirectoriesHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	memberClient, adminClient := testHTTPClient(t), testHTTPClient(t)
	member := registerGovernanceUser(t, memberClient, server.URL, "ops_dir_member")
	administrator := registerGovernanceUser(t, adminClient, server.URL, "ops_dir_admin")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/admin/generations", "/api/v1/admin/finance/accounts", "/api/v1/admin/audit"} {
		response := requestJSON(t, memberClient, http.MethodGet, server.URL+path, nil, nil)
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("member accessed %s: %d", path, response.StatusCode)
		}
	}

	targetGenerationID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO generations(id,owner_id,mode,provider,model_name,prompt,status,estimated_cost_cents,created_at,updated_at)
		VALUES($1,$2,'video','local_test','hcai-local-video-v1','HTTP operations directory target','queued',12,now()-interval '2 hours',now()-interval '2 hours')`, targetGenerationID, member.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO generations(owner_id,mode,provider,model_name,prompt,status,estimated_cost_cents,created_at,updated_at)
		SELECT $1,'video','local_test','hcai-local-video-v1','HTTP operations directory pressure '||value,'queued',12,now()-interval '1 hour',now()-interval '1 hour'
		FROM generate_series(1,20) value`, member.ID); err != nil {
		t.Fatal(err)
	}
	type generationPage struct {
		Items      []admin.GenerationItem `json:"items"`
		NextCursor *string                `json:"nextCursor"`
	}
	generationPath := server.URL + "/api/v1/admin/generations?q=http+operations+directory&mode=video&status=queued&limit=10"
	var generationsOne generationPage
	response := requestJSON(t, adminClient, http.MethodGet, generationPath, nil, &generationsOne)
	if response.StatusCode != http.StatusOK || len(generationsOne.Items) != 10 || generationsOne.NextCursor == nil {
		t.Fatalf("first generation page failed: status=%d page=%#v", response.StatusCode, generationsOne)
	}
	var generationsTwo generationPage
	response = requestJSON(t, adminClient, http.MethodGet, generationPath+"&cursor="+url.QueryEscape(*generationsOne.NextCursor), nil, &generationsTwo)
	if response.StatusCode != http.StatusOK || len(generationsTwo.Items) != 10 || generationsTwo.NextCursor == nil {
		t.Fatalf("second generation page failed: status=%d page=%#v", response.StatusCode, generationsTwo)
	}
	var generationsThree generationPage
	response = requestJSON(t, adminClient, http.MethodGet, generationPath+"&cursor="+url.QueryEscape(*generationsTwo.NextCursor), nil, &generationsThree)
	if response.StatusCode != http.StatusOK || len(generationsThree.Items) != 1 || generationsThree.Items[0].ID != targetGenerationID || generationsThree.NextCursor != nil {
		t.Fatalf("third generation page failed: status=%d page=%#v", response.StatusCode, generationsThree)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/generations?mode=voice", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid generation filter status: %d", response.StatusCode)
	}
	var cancelled admin.GenerationItem
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/generations/"+targetGenerationID.String()+"/cancel", map[string]any{
		"reason": "Verified exact HTTP generation retrieval beyond the first pages.", "confirmed": true,
	}, &cancelled)
	if response.StatusCode != http.StatusOK || cancelled.ID != targetGenerationID || cancelled.Status != "cancelled" {
		t.Fatalf("exact generation response failed: status=%d item=%#v", response.StatusCode, cancelled)
	}

	targetFinanceID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'HTTP Finance Directory Target','creator','active')`,
		targetFinanceID, targetFinanceID.String()+"@test.local", "http_finance_target_"+targetFinanceID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(email,handle,display_name,role,status)
		SELECT 'http-finance-'||value||'@test.local','http_finance_'||value,'HTTP Finance Directory Pressure '||value,'creator','active'
		FROM generate_series(1,20) value`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE billing_accounts b SET updated_at=CASE WHEN b.user_id=$1 THEN now()-interval '2 hours' ELSE now()-interval '1 hour' END
		FROM users u WHERE u.id=b.user_id AND (u.id=$1 OR u.handle LIKE 'http_finance_%')`, targetFinanceID); err != nil {
		t.Fatal(err)
	}
	type financePage struct {
		Items      []admin.FinanceAccount `json:"items"`
		NextCursor *string                `json:"nextCursor"`
	}
	financePath := server.URL + "/api/v1/admin/finance/accounts?q=http+finance+directory&state=available&limit=10"
	var financeOne financePage
	response = requestJSON(t, adminClient, http.MethodGet, financePath, nil, &financeOne)
	if response.StatusCode != http.StatusOK || len(financeOne.Items) != 10 || financeOne.NextCursor == nil {
		t.Fatalf("first finance page failed: status=%d page=%#v", response.StatusCode, financeOne)
	}
	var financeTwo financePage
	response = requestJSON(t, adminClient, http.MethodGet, financePath+"&cursor="+url.QueryEscape(*financeOne.NextCursor), nil, &financeTwo)
	if response.StatusCode != http.StatusOK || len(financeTwo.Items) != 10 || financeTwo.NextCursor == nil {
		t.Fatalf("second finance page failed: status=%d page=%#v", response.StatusCode, financeTwo)
	}
	var financeThree financePage
	response = requestJSON(t, adminClient, http.MethodGet, financePath+"&cursor="+url.QueryEscape(*financeTwo.NextCursor), nil, &financeThree)
	if response.StatusCode != http.StatusOK || len(financeThree.Items) != 1 || financeThree.Items[0].UserID != targetFinanceID || financeThree.NextCursor != nil {
		t.Fatalf("third finance page failed: status=%d page=%#v", response.StatusCode, financeThree)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/finance/accounts?state=overdrawn", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid finance filter status: %d", response.StatusCode)
	}
	var adjusted admin.FinanceAccount
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/finance/accounts/"+targetFinanceID.String()+"/adjust", map[string]any{
		"deltaCents": 1, "currency": "USD", "reason": "Verified exact HTTP finance retrieval beyond the first pages.", "confirmed": true,
	}, &adjusted)
	if response.StatusCode != http.StatusOK || adjusted.UserID != targetFinanceID || adjusted.BalanceCents != 250001 {
		t.Fatalf("exact finance response failed: status=%d item=%#v", response.StatusCode, adjusted)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_events(actor_id,action,resource_type,reason,request_id,metadata)
		SELECT $1,'admin.http_directory_probe','http_probe','HTTP audit directory backlog '||value,'http-audit-'||value,jsonb_build_object('ordinal',value)
		FROM generate_series(1,21) value`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	type auditPage struct {
		Items      []admin.AuditEvent `json:"items"`
		NextCursor *string            `json:"nextCursor"`
	}
	auditPath := server.URL + "/api/v1/admin/audit?q=http+audit+directory&action=admin.http_directory_probe&resourceType=http_probe&limit=10"
	var auditOne auditPage
	response = requestJSON(t, adminClient, http.MethodGet, auditPath, nil, &auditOne)
	if response.StatusCode != http.StatusOK || len(auditOne.Items) != 10 || auditOne.NextCursor == nil {
		t.Fatalf("first audit page failed: status=%d page=%#v", response.StatusCode, auditOne)
	}
	var auditTwo auditPage
	response = requestJSON(t, adminClient, http.MethodGet, auditPath+"&cursor="+url.QueryEscape(*auditOne.NextCursor), nil, &auditTwo)
	if response.StatusCode != http.StatusOK || len(auditTwo.Items) != 10 || auditTwo.NextCursor == nil {
		t.Fatalf("second audit page failed: status=%d page=%#v", response.StatusCode, auditTwo)
	}
	var auditThree auditPage
	response = requestJSON(t, adminClient, http.MethodGet, auditPath+"&cursor="+url.QueryEscape(*auditTwo.NextCursor), nil, &auditThree)
	if response.StatusCode != http.StatusOK || len(auditThree.Items) != 1 || auditThree.NextCursor != nil {
		t.Fatalf("third audit page failed: status=%d page=%#v", response.StatusCode, auditThree)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/audit?action=contains+spaces", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid audit filter status: %d", response.StatusCode)
	}
}
