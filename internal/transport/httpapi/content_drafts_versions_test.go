package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/community"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestContentDraftAndAssetVersionHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	mediaRoot := t.TempDir()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: mediaRoot, WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	client := testHTTPClient(t)
	owner := registerGovernanceUser(t, client, server.URL, "draft_version_owner")
	otherClient := testHTTPClient(t)
	registerGovernanceUser(t, otherClient, server.URL, "draft_version_other")

	root := uploadAssetPart(t, client, server.URL+"/api/v1/assets/uploads", map[string]string{"title": "Versioned HTTP Asset"}, "file", "version-v1.txt", "Original version content.")
	processAssetScan(t, pool, mediaRoot)
	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/assets/"+root.ID.String(), nil, &root)
	if response.StatusCode != http.StatusOK || root.VersionNumber != 1 || root.FamilyID != root.ID {
		t.Fatalf("root Asset version response: status=%d item=%#v", response.StatusCode, root)
	}
	version := uploadAssetPart(t, client, server.URL+"/api/v1/assets/"+root.ID.String()+"/versions", map[string]string{
		"title": "Versioned HTTP Asset revised", "note": "Approved copy after editorial review.",
	}, "file", "version-v2.txt", "Revised version content.")
	if version.VersionNumber != 2 || version.FamilyID != root.ID || version.SupersedesAssetID == nil || *version.SupersedesAssetID != root.ID {
		t.Fatalf("new Asset version response: %#v", version)
	}
	processAssetScan(t, pool, mediaRoot)
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/assets/"+version.ID.String(), nil, &version)
	if response.StatusCode != http.StatusOK || len(version.Versions) != 2 || !version.IsLatestVersion {
		t.Fatalf("Asset version history response: status=%d item=%#v", response.StatusCode, version)
	}
	extraAssetID := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,created_at)
		VALUES($1,$2,'image','HTTP paged Asset','/media/http-paged-asset.jpg','image/jpeg','clean','demo','hcai-personal-v1',now()+interval '1 hour')`, extraAssetID, owner.ID); err != nil {
		t.Fatal(err)
	}
	var firstPage assets.AssetPage
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/assets?limit=1", nil, &firstPage)
	if response.StatusCode != http.StatusOK || len(firstPage.Items) != 1 || firstPage.Items[0].ID != extraAssetID || firstPage.NextCursor == nil {
		t.Fatalf("first Asset page: status=%d page=%#v", response.StatusCode, firstPage)
	}
	var secondPage assets.AssetPage
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/assets?limit=1&cursor="+*firstPage.NextCursor, nil, &secondPage)
	if response.StatusCode != http.StatusOK || len(secondPage.Items) != 1 || secondPage.Items[0].ID != version.ID || secondPage.NextCursor != nil {
		t.Fatalf("second latest-only Asset page: status=%d page=%#v", response.StatusCode, secondPage)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/assets?cursor=modified", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("modified Asset cursor status: %d", response.StatusCode)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/assets?limit=51", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("oversized Asset page status: %d", response.StatusCode)
	}

	var draft community.Draft
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/content-drafts", map[string]any{
		"assetId": version.ID, "promptVisibility": "private",
	}, &draft)
	if response.StatusCode != http.StatusCreated || draft.Version != 1 || draft.AssetID != version.ID {
		t.Fatalf("create content draft: status=%d item=%#v", response.StatusCode, draft)
	}
	response = requestJSON(t, otherClient, http.MethodGet, server.URL+"/api/v1/content-drafts/"+draft.ID.String(), nil, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("another account read private draft: %d", response.StatusCode)
	}
	response = requestJSON(t, client, http.MethodPatch, server.URL+"/api/v1/content-drafts/"+draft.ID.String(), map[string]any{
		"assetId": version.ID, "title": "Durable HTTP content draft", "summary": "Saved before a controlled publication.",
		"prompt": "HTTP draft prompt", "promptVisibility": "partial", "aiDisclosure": "Created with the deterministic Local Test Provider.",
		"body": "This post was restored from a private server-side draft.", "expectedVersion": draft.Version,
	}, &draft)
	if response.StatusCode != http.StatusOK || draft.Version != 2 {
		t.Fatalf("update content draft: status=%d item=%#v", response.StatusCode, draft)
	}
	response = requestJSON(t, client, http.MethodPatch, server.URL+"/api/v1/content-drafts/"+draft.ID.String(), map[string]any{
		"assetId": version.ID, "promptVisibility": "public", "expectedVersion": 1,
	}, nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale content draft update was accepted: %d", response.StatusCode)
	}
	var publication community.Publication
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/content-drafts/"+draft.ID.String()+"/publish", map[string]any{"expectedVersion": draft.Version}, &publication)
	if response.StatusCode != http.StatusCreated || publication.WorkID != draft.ID || publication.PostID != draft.PostID {
		t.Fatalf("publish content draft: status=%d item=%#v", response.StatusCode, publication)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/content-drafts/"+draft.ID.String(), nil, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("published draft remained editable: %d", response.StatusCode)
	}
}

func uploadAssetPart(t *testing.T, client *http.Client, target string, fields map[string]string, field, filename, content string) assets.Asset {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, target, &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var item assets.Asset
	if err := json.NewDecoder(response.Body).Decode(&item); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("multipart Asset request failed: status=%d item=%#v", response.StatusCode, item)
	}
	return item
}

func processAssetScan(t *testing.T, pool *pgxpool.Pool, mediaRoot string) {
	t.Helper()
	repository := jobs.NewRepository(pool)
	service := assets.NewService(pool, mediaRoot)
	job := claimHTTPJobKind(t, context.Background(), pool, "draft-version-http-worker", assets.ScanJobKind)
	if err := service.HandleScanJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(context.Background(), job, "draft-version-http-worker"); err != nil {
		t.Fatal(err)
	}
}
