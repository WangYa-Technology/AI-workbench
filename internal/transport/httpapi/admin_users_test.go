package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestAdminUserDirectoryHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	memberClient, adminClient := testHTTPClient(t), testHTTPClient(t)
	_ = registerGovernanceUser(t, memberClient, server.URL, "directory_member")
	administrator := registerGovernanceUser(t, adminClient, server.URL, "directory_admin")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	targetID := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO users(id,email,handle,display_name,role,status,created_at)
		VALUES($1,$2,$3,'HTTP Directory Target','member','active',$4::timestamptz - interval '2 hours')`,
		targetID, targetID.String()+"@test.local", "http_directory_target_"+targetID.String()[:8], time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO users(id,email,handle,display_name,role,status,created_at)
		SELECT gen_random_uuid(), 'http_directory_'||value||'@test.local', 'http_directory_'||value,
		       'HTTP Directory '||value, 'member', 'active', now() - interval '1 hour'
		FROM generate_series(1,20) value`); err != nil {
		t.Fatal(err)
	}

	response := requestJSON(t, memberClient, http.MethodGet, server.URL+"/api/v1/admin/users", nil, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("member accessed user directory: %d", response.StatusCode)
	}
	type page struct {
		Items      []admin.User `json:"items"`
		NextCursor *string      `json:"nextCursor"`
	}
	var first page
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/users?q=http_directory_&role=member&status=active&limit=10", nil, &first)
	if response.StatusCode != http.StatusOK || len(first.Items) != 10 || first.NextCursor == nil {
		t.Fatalf("first user page failed: status=%d page=%#v", response.StatusCode, first)
	}
	var second page
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/users?q=http_directory_&role=member&status=active&limit=10&cursor="+url.QueryEscape(*first.NextCursor), nil, &second)
	if response.StatusCode != http.StatusOK || len(second.Items) != 10 || second.NextCursor == nil {
		t.Fatalf("second user page failed: status=%d page=%#v", response.StatusCode, second)
	}
	var third page
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/users?q=http_directory_&role=member&status=active&limit=10&cursor="+url.QueryEscape(*second.NextCursor), nil, &third)
	if response.StatusCode != http.StatusOK || len(third.Items) != 1 || third.Items[0].ID != targetID || third.NextCursor != nil {
		t.Fatalf("third user page failed: status=%d page=%#v", response.StatusCode, third)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/users?role=owner", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid user filter status: %d", response.StatusCode)
	}

	var updated admin.User
	response = requestJSON(t, adminClient, http.MethodPatch, server.URL+"/api/v1/admin/users/"+targetID.String(), map[string]any{
		"role": "creator", "status": "suspended"}, &updated)
	if response.StatusCode != http.StatusOK || updated.ID != targetID || updated.Status != "suspended" {
		t.Fatalf("exact user update response failed: status=%d user=%#v", response.StatusCode, updated)
	}
}
