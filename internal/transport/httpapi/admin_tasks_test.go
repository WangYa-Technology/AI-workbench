package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestAdminTaskOperationsHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	memberClient, adminClient := testHTTPClient(t), testHTTPClient(t)
	member := registerGovernanceUser(t, memberClient, server.URL, "task_ops_member")
	administrator := registerGovernanceUser(t, adminClient, server.URL, "task_ops_admin")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	creatorID, taskID, assetID, disputeID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'HTTP Task Creator','creator','active')`, creatorID, creatorID.String()+"@test.local", "http_creator_"+creatorID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code) VALUES($1,$2,'image','HTTP disputed delivery','/media/task-http.jpg','image/jpeg','clean','delivery','personal')`, assetID, creatorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO demands(id,client_id,title,brief,deliverable_type,budget_cents,currency,deadline,status,assignee_id,summary) VALUES($1,$2,'HTTP disputed task','A complete HTTP task operations contract brief.','image',18000,'USD',now()+interval '7 days','disputed',$3,'HTTP operations task.')`, taskID, member.ID, creatorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO proposals(demand_id,creator_id,approach,amount_cents,status) VALUES($1,$2,'HTTP verified approach',18000,'accepted')`, taskID, creatorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO deliveries(demand_id,creator_id,asset_id,note,status,version) VALUES($1,$2,$3,'HTTP delivery evidence','disputed',1)`, taskID, creatorID, assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO task_disputes(id,demand_id,opened_by,reason,status,idempotency_key) VALUES($1,$2,$3,'HTTP evidence requires a bounded operations decision.','open',$4)`, disputeID, taskID, member.ID, "http-dispute-"+taskID.String()); err != nil {
		t.Fatal(err)
	}

	response := requestJSON(t, memberClient, http.MethodGet, server.URL+"/api/v1/admin/tasks", nil, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("member accessed task operations: %d", response.StatusCode)
	}
	var queue struct {
		Items []admin.TaskOperation `json:"items"`
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/tasks", nil, &queue)
	if response.StatusCode != http.StatusOK || len(queue.Items) != 1 || queue.Items[0].ID != taskID || queue.Items[0].DisputeVersion == nil {
		t.Fatalf("task operations queue failed: status=%d items=%#v", response.StatusCode, queue.Items)
	}
	var resolved admin.TaskOperation
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/tasks/"+taskID.String()+"/resolve", map[string]any{
		"decision": "cancel_without_settlement", "expectedVersion": 1}, &resolved)
	if response.StatusCode != http.StatusOK || resolved.Status != "cancelled" || resolved.DisputeStatus == nil || *resolved.DisputeStatus != "resolved_client" {
		t.Fatalf("task resolution contract failed: status=%d item=%#v", response.StatusCode, resolved)
	}
}
