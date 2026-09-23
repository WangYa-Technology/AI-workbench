package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/community"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestCommunityPostDetailHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	authorClient, viewerClient := testHTTPClient(t), testHTTPClient(t)
	author := registerGovernanceUser(t, authorClient, server.URL, "detail_author")
	viewer := registerGovernanceUser(t, viewerClient, server.URL, "detail_viewer")
	assetID, workID, postID, commentID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
		VALUES($1,$2,'image','Detail source','/media/community-detail.jpg','image/jpeg','clean','delivery','personal')`, assetID, author.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO works(id,author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at)
		VALUES($1,$2,$3,'Community detail work','Dedicated detail page contract.','Local Test','published','Created with the deterministic Local Test provider.',now())`, workID, author.ID, assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO posts(id,author_id,work_id,body,status,published_at)
		VALUES($1,$2,$3,'Full Community discussion body.','published',now())`, postID, author.ID, workID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO comments(id,post_id,author_id,body,status)
		VALUES($1,$2,$3,'A published detail-page comment.','published')`, commentID, postID, viewer.ID); err != nil {
		t.Fatal(err)
	}

	var publicDetail community.Post
	response := requestJSON(t, testHTTPClient(t), http.MethodGet, server.URL+"/api/v1/community/posts/"+postID.String(), nil, &publicDetail)
	if response.StatusCode != http.StatusOK || publicDetail.ID != postID || publicDetail.WorkID == nil || *publicDetail.WorkID != workID || publicDetail.CommentCount != 1 {
		t.Fatalf("public detail contract failed: status=%d post=%#v", response.StatusCode, publicDetail)
	}
	if publicDetail.ViewerLiked || publicDetail.ViewerBookmarked || publicDetail.ViewerFollowing {
		t.Fatalf("anonymous detail leaked viewer state: %#v", publicDetail)
	}

	for _, kind := range []string{"like", "bookmark"} {
		response = requestJSON(t, viewerClient, http.MethodPut, server.URL+"/api/v1/community/posts/"+postID.String()+"/reactions/"+kind, map[string]any{"active": true}, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s reaction failed: %d", kind, response.StatusCode)
		}
	}
	response = requestJSON(t, viewerClient, http.MethodPut, server.URL+"/api/v1/community/authors/"+author.ID.String()+"/follow", map[string]any{"active": true}, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("follow failed: %d", response.StatusCode)
	}

	var viewerDetail community.Post
	response = requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/community/posts/"+postID.String(), nil, &viewerDetail)
	if response.StatusCode != http.StatusOK || !viewerDetail.ViewerLiked || !viewerDetail.ViewerBookmarked || !viewerDetail.ViewerFollowing || viewerDetail.LikeCount != 1 || viewerDetail.BookmarkCount != 1 {
		t.Fatalf("viewer detail state failed: status=%d post=%#v", response.StatusCode, viewerDetail)
	}

	if _, err := pool.Exec(ctx, `UPDATE posts SET status='hidden' WHERE id=$1`, postID); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/community/posts/"+postID.String(), nil, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("hidden detail remained visible: %d", response.StatusCode)
	}
	response = requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/community/posts/"+uuid.NewString(), nil, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("missing detail status: %d", response.StatusCode)
	}
}
