package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestSavedWorksHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	authorClient, viewerClient, outsiderClient := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	author := registerGovernanceUser(t, authorClient, server.URL, "savedauthor")
	registerGovernanceUser(t, viewerClient, server.URL, "savedviewer")
	registerGovernanceUser(t, outsiderClient, server.URL, "savedoutsider")
	assetID, workID, postID, secondWorkID, secondPostID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
		VALUES($1,$2,'image','HTTP saved source','/media/http-saved.jpg','image/jpeg','clean','demo','personal')`, assetID, author.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO works(id,author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at)
		VALUES($1,$2,$3,'HTTP saved work','Owner-scoped saved projection.','Local Test','published','Local Test AI disclosure.',now()),
		      ($4,$2,$3,'HTTP saved work two','Second stable saved projection.','Local Test','published','Local Test AI disclosure.',now())`, workID, author.ID, assetID, secondWorkID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO posts(id,author_id,work_id,body,status,published_at)
		VALUES($1,$2,$3,'HTTP saved post','published',now()),
		      ($4,$2,$5,'HTTP saved post two','published',now())`, postID, author.ID, workID, secondPostID, secondWorkID); err != nil {
		t.Fatal(err)
	}

	response := requestJSON(t, viewerClient, http.MethodPut, server.URL+"/api/v1/community/posts/"+postID.String()+"/reactions/bookmark", map[string]any{"active": true}, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("save status: %d", response.StatusCode)
	}
	response = requestJSON(t, viewerClient, http.MethodPut, server.URL+"/api/v1/community/posts/"+secondPostID.String()+"/reactions/bookmark", map[string]any{"active": true}, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("second save status: %d", response.StatusCode)
	}
	var viewerResult struct {
		Items []assets.SavedWork `json:"items"`
	}
	response = requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/assets/saved-works", nil, &viewerResult)
	if response.StatusCode != http.StatusOK || len(viewerResult.Items) != 2 {
		t.Fatalf("viewer saved works: status=%d items=%#v", response.StatusCode, viewerResult.Items)
	}
	var firstPage assets.SavedWorkPage
	response = requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/assets/saved-works?limit=1", nil, &firstPage)
	if response.StatusCode != http.StatusOK || len(firstPage.Items) != 1 || firstPage.NextCursor == nil {
		t.Fatalf("first saved-work page: status=%d page=%#v", response.StatusCode, firstPage)
	}
	var secondPage assets.SavedWorkPage
	response = requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/assets/saved-works?limit=1&cursor="+*firstPage.NextCursor, nil, &secondPage)
	if response.StatusCode != http.StatusOK || len(secondPage.Items) != 1 || secondPage.NextCursor != nil || secondPage.Items[0].PostID == firstPage.Items[0].PostID {
		t.Fatalf("second saved-work page: status=%d page=%#v", response.StatusCode, secondPage)
	}
	var outsiderResult struct {
		Items []assets.SavedWork `json:"items"`
	}
	response = requestJSON(t, outsiderClient, http.MethodGet, server.URL+"/api/v1/assets/saved-works", nil, &outsiderResult)
	if response.StatusCode != http.StatusOK || len(outsiderResult.Items) != 0 {
		t.Fatalf("outsider saved works: status=%d items=%#v", response.StatusCode, outsiderResult.Items)
	}
	response = requestJSON(t, testHTTPClient(t), http.MethodGet, server.URL+"/api/v1/assets/saved-works", nil, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous saved works status: %d", response.StatusCode)
	}
	response = requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/assets/saved-works?cursor=modified", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("modified saved-work cursor status: %d", response.StatusCode)
	}
	response = requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/assets/saved-works?limit=51", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("oversized saved-work page status: %d", response.StatusCode)
	}

	if _, err := pool.Exec(context.Background(), `UPDATE posts SET status='hidden' WHERE id=$1`, postID); err != nil {
		t.Fatal(err)
	}
	viewerResult.Items = nil
	response = requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/assets/saved-works", nil, &viewerResult)
	if response.StatusCode != http.StatusOK || len(viewerResult.Items) != 1 || viewerResult.Items[0].PostID != secondPostID {
		t.Fatalf("hidden post remained visible: status=%d items=%#v", response.StatusCode, viewerResult.Items)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE posts SET status='published' WHERE id=$1`, postID); err != nil {
		t.Fatal(err)
	}
	viewerResult.Items = nil
	response = requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/assets/saved-works", nil, &viewerResult)
	if response.StatusCode != http.StatusOK || len(viewerResult.Items) != 2 {
		t.Fatalf("restored post missing: status=%d items=%#v", response.StatusCode, viewerResult.Items)
	}
}
