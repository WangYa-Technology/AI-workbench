package httpapi_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/discovery"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestDiscoverBrowseFiltersAndEqualTimestampPagination(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := context.Background()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173"}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	user, asset := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,'browse@test.local','browse_author','Browse Author','creator','active')`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code) VALUES($1,$2,'image','Browse image','/media/test.jpg','image/jpeg','clean','delivery','personal')`, asset, user); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO works(author_id,asset_id,title,summary,model_name,status,ai_disclosure,prompt_visibility,published_at) SELECT $1,$2,'Browse target '||n,'Visible description','Model X','published','AI generated test image','public','2026-01-01T00:00:00Z' FROM generate_series(1,15) n`, user, asset); err != nil {
		t.Fatal(err)
	}
	client := testHTTPClient(t)
	base := server.URL + "/api/v1/works?q=Browse+target&kind=image&promptVisibility=public"
	var first, second discovery.Page
	if res := requestJSON(t, client, http.MethodGet, base, nil, &first); res.StatusCode != 200 || len(first.Items) != 12 || first.NextCursor == nil {
		t.Fatalf("first page: %d %#v", res.StatusCode, first)
	}
	if res := requestJSON(t, client, http.MethodGet, base+"&cursor="+url.QueryEscape(*first.NextCursor), nil, &second); res.StatusCode != 200 || len(second.Items) != 3 || second.NextCursor != nil {
		t.Fatalf("second page: %d %#v", res.StatusCode, second)
	}
	ids := map[uuid.UUID]bool{}
	for _, page := range []discovery.Page{first, second} {
		if page.CategoryCounts["image"] != 15 {
			t.Fatalf("counts must include all pages: %#v", page.CategoryCounts)
		}
		for _, w := range page.Items {
			if ids[w.ID] {
				t.Fatal("duplicate pagination item")
			}
			ids[w.ID] = true
		}
	}
	for _, query := range []string{"kind=video", "promptVisibility=private", "q=no-such-work"} {
		var page discovery.Page
		res := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/works?"+query, nil, &page)
		if res.StatusCode != 200 || len(page.Items) != 0 {
			t.Fatalf("filter ignored: %s", query)
		}
		if query == "kind=video" && page.CategoryCounts["image"] != 15 {
			t.Fatal("category selection changed counts")
		}
		if query != "kind=video" && len(page.CategoryCounts) != 0 {
			t.Fatal("counts ignored search or visibility")
		}
	}
	if res := requestJSON(t, client, http.MethodGet, base+"&cursor=2026-01-02T00:00:00Z", nil, nil); res.StatusCode != 400 {
		t.Fatal("unsafe legacy cursor accepted")
	}
	if res := requestJSON(t, client, http.MethodGet, base+"&cursor=2026-01-02T00:00:00Z%7Cbad", nil, nil); res.StatusCode != 400 {
		t.Fatal("invalid cursor accepted")
	}
}
