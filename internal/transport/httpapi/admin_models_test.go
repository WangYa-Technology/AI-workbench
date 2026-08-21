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

func TestAdminModelRoutesHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	memberClient, adminClient := testHTTPClient(t), testHTTPClient(t)
	_ = registerGovernanceUser(t, memberClient, server.URL, "models_member")
	administrator := registerGovernanceUser(t, adminClient, server.URL, "models_admin")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	response := requestJSON(t, memberClient, http.MethodGet, server.URL+"/api/v1/admin/models/routes", nil, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("member accessed model routes: %d", response.StatusCode)
	}
	var initial admin.ModelRoutePolicy
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/models/routes", nil, &initial)
	if response.StatusCode != http.StatusOK || len(initial.Routes) != 4 {
		t.Fatalf("initial route contract: %d %#v", response.StatusCode, initial)
	}
	input := map[string]any{"providerProfileId": "local-chat-v1", "name": "HTTP chat route", "timeoutSeconds": 75, "maxAttempts": 2, "expectedVersion": 1}
	var updated admin.ModelRoutePolicy
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/models/routes/chat", input, &updated)
	if response.StatusCode != http.StatusOK || updated.Routes["chat"].Version != 2 {
		t.Fatalf("route update contract: %d %#v", response.StatusCode, updated)
	}
	parentID := updated.Routes["chat"].ID
	for version := 3; version <= 22; version++ {
		id := uuid.New()
		if _, err := pool.Exec(context.Background(), `INSERT INTO model_route_revisions(id,mode,version,parent_revision_id,provider_profile_id,name,timeout_seconds,max_attempts,reason,created_by) VALUES($1,'chat',$2,$3,'local-chat-v1','HTTP paginated chat route',75,2,'HTTP model route pagination evidence.',$4)`, id, version, parentID, administrator.ID); err != nil {
			t.Fatal(err)
		}
		parentID = id
	}
	if _, err := pool.Exec(context.Background(), `UPDATE model_route_state SET active_revision_id=$1,version=22 WHERE mode='chat'`, parentID); err != nil {
		t.Fatal(err)
	}
	var first, second admin.ModelRoutePolicy
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/models/routes?mode=chat&limit=20", nil, &first)
	if response.StatusCode != http.StatusOK || len(first.History["chat"]) != 20 || first.NextCursors["chat"] == "" || first.Routes["chat"].Version != 22 {
		t.Fatalf("first model route history page: status=%d policy=%#v", response.StatusCode, first)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/models/routes?mode=chat&limit=20&cursor="+url.QueryEscape(first.NextCursors["chat"]), nil, &second)
	if response.StatusCode != http.StatusOK || len(second.History["chat"]) != 2 || second.NextCursors["chat"] != "" || second.Routes["chat"].Version != 22 {
		t.Fatalf("second model route history page: status=%d policy=%#v", response.StatusCode, second)
	}
	for _, path := range []string{
		"/api/v1/admin/models/routes?mode=chat&limit=51",
		"/api/v1/admin/models/routes?mode=chat&cursor=modified",
		"/api/v1/admin/models/routes?cursor=modified",
	} {
		if response = requestJSON(t, adminClient, http.MethodGet, server.URL+path, nil, nil); response.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("invalid model route history accepted for %s: %d", path, response.StatusCode)
		}
	}
}
