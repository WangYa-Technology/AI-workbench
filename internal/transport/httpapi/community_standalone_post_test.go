package httpapi_test

import (
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

func TestStandaloneCommunityPostHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	authorClient, viewerClient := testHTTPClient(t), testHTTPClient(t)
	author := registerGovernanceUser(t, authorClient, server.URL, "post_author")
	registerGovernanceUser(t, viewerClient, server.URL, "post_viewer")
	endpoint := server.URL + "/api/v1/community/posts"

	response := requestJSON(t, testHTTPClient(t), http.MethodPost, endpoint, map[string]any{
		"title": "Unauthenticated discussion", "body": "This must not be published.",
	}, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous post creation returned %d", response.StatusCode)
	}
	response = requestJSON(t, authorClient, http.MethodPost, endpoint, map[string]any{"title": "x", "body": ""}, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid post creation returned %d", response.StatusCode)
	}

	var created community.Post
	response = requestJSON(t, authorClient, http.MethodPost, endpoint, map[string]any{
		"title": "How should a repeatable image workflow be documented?",
		"body":  "I am comparing prompt notes with generation evidence. Which details make the workflow easiest to reproduce?",
	}, &created)
	if response.StatusCode != http.StatusCreated || created.ID == uuid.Nil || created.Title == "" || created.AuthorID != author.ID || created.WorkID != nil || created.MediaURL != nil {
		t.Fatalf("standalone post creation failed: status=%d post=%#v", response.StatusCode, created)
	}
	if response.Header.Get("Location") != "/community/posts/"+created.ID.String() {
		t.Fatalf("unexpected post location: %q", response.Header.Get("Location"))
	}

	var mine community.PostPage
	response = requestJSON(t, authorClient, http.MethodGet, endpoint+"?mine=true", nil, &mine)
	if response.StatusCode != http.StatusOK || len(mine.Items) != 1 || mine.Items[0].ID != created.ID {
		t.Fatalf("author inventory failed: status=%d page=%#v", response.StatusCode, mine)
	}
	response = requestJSON(t, testHTTPClient(t), http.MethodGet, endpoint+"?mine=true", nil, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous author inventory returned %d", response.StatusCode)
	}
	var viewerMine community.PostPage
	response = requestJSON(t, viewerClient, http.MethodGet, endpoint+"?mine=true", nil, &viewerMine)
	if response.StatusCode != http.StatusOK || len(viewerMine.Items) != 0 {
		t.Fatalf("viewer inventory leaked another author: status=%d page=%#v", response.StatusCode, viewerMine)
	}

	var detail community.Post
	response = requestJSON(t, viewerClient, http.MethodGet, endpoint+"/"+created.ID.String(), nil, &detail)
	if response.StatusCode != http.StatusOK || detail.ID != created.ID || detail.Title != created.Title || detail.WorkID != nil {
		t.Fatalf("standalone post detail failed: status=%d post=%#v", response.StatusCode, detail)
	}
	response = requestJSON(t, viewerClient, http.MethodPost, endpoint+"/"+created.ID.String()+"/comments", map[string]any{
		"body": "Include the model, input parameters, and the selected output version.",
	}, nil)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("standalone post comment returned %d", response.StatusCode)
	}
	var interaction community.InteractionState
	response = requestJSON(t, viewerClient, http.MethodPut, endpoint+"/"+created.ID.String()+"/reactions/like", map[string]any{"active": true}, &interaction)
	if response.StatusCode != http.StatusOK || !interaction.ViewerLiked || interaction.LikeCount != 1 {
		t.Fatalf("standalone post reaction failed: status=%d state=%#v", response.StatusCode, interaction)
	}
}
