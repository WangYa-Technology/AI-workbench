package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/community"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestCommunityHardeningHTTPPermissionsAndLifecycle(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	author, stranger, guest := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	registerGovernanceUser(t, author, server.URL, "hard_author")
	registerGovernanceUser(t, stranger, server.URL, "hard_stranger")
	base := server.URL + "/api/v1"
	check := func(client *http.Client, method, path string, input, output any, want int) {
		t.Helper()
		if got := requestJSON(t, client, method, base+path, input, output).StatusCode; got != want {
			t.Fatalf("%s %s: got %d want %d", method, path, got, want)
		}
	}
	check(guest, "GET", "/community/posts?view=drafts", nil, nil, 401)
	// The generic test helper adds keys; construct this request directly.
	req, err := http.NewRequest("POST", base+"/community/posts", strings.NewReader(`{"title":"No key","body":"No duplicate creation"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := author.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 422 {
		t.Fatalf("missing key: %d", response.StatusCode)
	}
	check(author, "POST", "/community/posts", map[string]any{"title": "非法分类", "body": "不能使用资源市场分类", "category": "market_asset"}, nil, 422)
	check(author, "POST", "/community/posts", map[string]any{"title": strings.Repeat("界", 121), "body": "标题过长"}, nil, 422)
	var published community.Post
	check(author, "POST", "/community/posts", map[string]any{"title": strings.Repeat("界", 120), "body": "正确处理多字节文本🙂"}, &published, 201)
	ctx := context.Background()
	if _, err = pool.Exec(ctx, `UPDATE system_settings SET publishing_enabled=false`); err != nil {
		t.Fatal(err)
	}
	var caps map[string]bool
	check(author, "GET", "/community/capabilities", nil, &caps, 200)
	if caps["canPublish"] || !caps["canSaveDraft"] || !caps["canInteract"] {
		t.Fatalf("paused capabilities: %v", caps)
	}
	check(author, "POST", "/community/posts", map[string]any{"title": "Paused publish", "body": "Should be blocked"}, nil, 503)
	var draft community.Post
	check(author, "POST", "/community/posts", map[string]any{"title": "", "body": "", "draft": true}, &draft, 201)
	check(stranger, "GET", "/community/posts/"+draft.ID.String()+"/owned", nil, nil, 404)
	check(guest, "GET", "/community/posts/"+draft.ID.String(), nil, nil, 404)
	check(author, "PATCH", "/community/posts/"+draft.ID.String(), map[string]any{"title": "Publish later", "body": "Valid draft text", "expectedVersion": draft.Version}, nil, 503)
	check(stranger, "POST", "/community/posts/"+published.ID.String()+"/comments", map[string]any{"body": "暂停发布期间仍可讨论🙂"}, nil, 201)
	if _, err = pool.Exec(ctx, `DELETE FROM role_permissions WHERE role='member' AND permission_id='community:publish'`); err != nil {
		t.Fatal(err)
	}
	check(author, "GET", "/community/capabilities", nil, &caps, 200)
	if caps["canPublish"] || caps["canSaveDraft"] || !caps["canInteract"] {
		t.Fatalf("revoked capabilities: %v", caps)
	}
	check(author, "POST", "/community/posts", map[string]any{"draft": true}, nil, 403)
	check(author, "POST", "/content-drafts", map[string]any{}, nil, 403)
	check(author, "POST", "/publications", map[string]any{}, nil, 403)
	check(stranger, "DELETE", "/community/posts/"+draft.ID.String(), map[string]any{"expectedVersion": draft.Version}, nil, 404)
	check(author, "DELETE", "/community/posts/"+draft.ID.String(), map[string]any{"expectedVersion": draft.Version + 1}, nil, 409)
	// Revoking publishing does not prevent an owner deleting their private data.
	check(author, "DELETE", "/community/posts/"+draft.ID.String(), map[string]any{"expectedVersion": draft.Version}, nil, 204)
}
