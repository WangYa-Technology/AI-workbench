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

func TestAdminTaskDirectoryHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	memberClient, adminClient := testHTTPClient(t), testHTTPClient(t)
	client := registerGovernanceUser(t, memberClient, server.URL, "task_dir_member")
	administrator := registerGovernanceUser(t, adminClient, server.URL, "task_dir_admin")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	response := requestJSON(t, memberClient, http.MethodGet, server.URL+"/api/v1/admin/tasks", nil, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("member accessed task directory: %d", response.StatusCode)
	}

	ctx := context.Background()
	creatorID, targetTaskID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'HTTP Task Directory Creator','creator','active')`, creatorID, creatorID.String()+"@test.local", "http_task_directory_creator_"+creatorID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO demands(id,client_id,title,brief,deliverable_type,budget_cents,currency,deadline,status,assignee_id,summary,created_at,updated_at)
		VALUES($1,$2,'HTTP task directory target','A complete HTTP task directory target brief.','image',18000,'USD',now()+interval '7 days','disputed',$3,'HTTP task directory evidence.',now()-interval '2 hours',now()-interval '2 hours')`, targetTaskID, client.ID, creatorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO demands(client_id,title,brief,deliverable_type,budget_cents,currency,deadline,status,assignee_id,summary,created_at,updated_at)
		SELECT $1,'HTTP task directory pressure '||value,'A complete HTTP task directory pressure brief.','image',18000,'USD',now()+interval '7 days','disputed',$2,'HTTP task directory evidence.',now()-interval '1 hour',now()-interval '1 hour'
		FROM generate_series(1,20) value`, client.ID, creatorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO task_disputes(demand_id,opened_by,reason,status,idempotency_key,created_at)
		SELECT id,$2,'HTTP task directory dispute evidence.','open','http-task-directory-'||id::text,created_at FROM demands WHERE client_id=$1`, client.ID, client.ID); err != nil {
		t.Fatal(err)
	}

	type taskPage struct {
		Items      []admin.TaskOperation `json:"items"`
		NextCursor *string               `json:"nextCursor"`
	}
	path := server.URL + "/api/v1/admin/tasks?q=http+task+directory&status=disputed&disputeStatus=open&limit=10"
	var first taskPage
	response = requestJSON(t, adminClient, http.MethodGet, path, nil, &first)
	if response.StatusCode != http.StatusOK || len(first.Items) != 10 || first.NextCursor == nil {
		t.Fatalf("first task page failed: status=%d page=%#v", response.StatusCode, first)
	}
	var second taskPage
	response = requestJSON(t, adminClient, http.MethodGet, path+"&cursor="+url.QueryEscape(*first.NextCursor), nil, &second)
	if response.StatusCode != http.StatusOK || len(second.Items) != 10 || second.NextCursor == nil {
		t.Fatalf("second task page failed: status=%d page=%#v", response.StatusCode, second)
	}
	var third taskPage
	response = requestJSON(t, adminClient, http.MethodGet, path+"&cursor="+url.QueryEscape(*second.NextCursor), nil, &third)
	if response.StatusCode != http.StatusOK || len(third.Items) != 1 || third.Items[0].ID != targetTaskID || third.NextCursor != nil {
		t.Fatalf("third task page failed: status=%d page=%#v", response.StatusCode, third)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/tasks?disputeStatus=reviewing", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid task filter status: %d", response.StatusCode)
	}

	var resolved admin.TaskOperation
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/tasks/"+targetTaskID.String()+"/resolve", map[string]any{
		"decision": "cancel_without_settlement", "expectedVersion": 1, "reason": "Reviewed task evidence and confirmed cancellation.", "confirm": true}, &resolved)
	if response.StatusCode != http.StatusOK || resolved.ID != targetTaskID || resolved.Status != "cancelled" {
		t.Fatalf("exact HTTP task response failed: status=%d item=%#v", response.StatusCode, resolved)
	}
}
