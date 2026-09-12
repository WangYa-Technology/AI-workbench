package httpapi_test

import (
	"context"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/tasktypes"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCategoryAdminPermissionsAndIsolation(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	member, admin := testHTTPClient(t), testHTTPClient(t)
	registerGovernanceUser(t, member, server.URL, "category_member")
	user := registerGovernanceUser(t, admin, server.URL, "category_admin")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"code": "http_category", "nameZh": "测试", "nameEn": "Test", "icon": "mixed", "sortOrder": 1}
	base := server.URL + "/api/v1"
	for _, scope := range []string{"community", "marketplace"} {
		path := base + "/admin/task-types?scope=" + scope
		if response := requestJSON(t, member, http.MethodPost, path, body, nil); response.StatusCode != 403 {
			t.Fatalf("member mutation %d", response.StatusCode)
		}
		body["code"] = "http_" + scope
		if response := requestJSON(t, admin, http.MethodPost, path, body, nil); response.StatusCode != 201 {
			t.Fatalf("admin create %d", response.StatusCode)
		}
		var page struct {
			Items []tasktypes.Type `json:"items"`
		}
		if response := requestJSON(t, member, http.MethodGet, base+"/task-types?scope="+scope, nil, &page); response.StatusCode != 200 {
			t.Fatal(response.StatusCode)
		}
		found := false
		for _, item := range page.Items {
			if item.Scope != scope {
				t.Fatal("scope leak")
			}
			if item.Code == body["code"] {
				found = true
			}
		}
		if !found {
			t.Fatal("created category missing")
		}
		if response := requestJSON(t, admin, http.MethodPatch, base+"/admin/task-types/http_"+scope+"?scope=task", body, nil); response.StatusCode != 422 {
			t.Fatal("scope override accepted")
		}
		if response := requestJSON(t, admin, http.MethodDelete, base+"/admin/task-types/http_"+scope+"?scope="+scope, map[string]string{"replacement": ""}, nil); response.StatusCode != 200 {
			t.Fatal("delete failed")
		}
	}
	if response := requestJSON(t, member, http.MethodGet, base+"/admin/content-category?scope=community", nil, nil); response.StatusCode != 403 {
		t.Fatal("private content directory exposed")
	}
	if response := requestJSON(t, member, http.MethodPatch, base+"/admin/content-category/00000000-0000-0000-0000-000000000000?scope=community", map[string]string{"category": "community_general"}, nil); response.StatusCode != 403 {
		t.Fatal("member assignment accepted")
	}
}
