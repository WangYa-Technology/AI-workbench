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

func TestAdminContentAndMediaDirectoryHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	memberClient, adminClient := testHTTPClient(t), testHTTPClient(t)
	_ = registerGovernanceUser(t, memberClient, server.URL, "inventory_member")
	administrator := registerGovernanceUser(t, adminClient, server.URL, "inventory_admin")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/admin/content", "/api/v1/admin/media"} {
		response := requestJSON(t, memberClient, http.MethodGet, server.URL+path, nil, nil)
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("member accessed %s: %d", path, response.StatusCode)
		}
	}

	ctx := context.Background()
	sourceAssetID, oldWorkID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,created_at)
		VALUES($1,$2,'image','HTTP inventory source','/media/http-inventory-source.jpg','image/jpeg','clean','demo','demo',now() - interval '3 hours')`, sourceAssetID, administrator.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO works(id,author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at,created_at,updated_at)
		VALUES($1,$2,$3,'HTTP content target','Older HTTP evidence','Local Test','published','HTTP content disclosure',now() - interval '2 hours',now() - interval '2 hours',now() - interval '2 hours')`, oldWorkID, administrator.ID, sourceAssetID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO works(author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at,created_at,updated_at)
		SELECT $1,$2,'HTTP content pressure '||value,'Newer HTTP evidence','Local Test','published','HTTP content disclosure',now() - interval '1 hour',now() - interval '1 hour',now() - interval '1 hour'
		FROM generate_series(1,20) value`, administrator.ID, sourceAssetID); err != nil {
		t.Fatal(err)
	}

	type contentPage struct {
		Items      []admin.ContentItem `json:"items"`
		NextCursor *string             `json:"nextCursor"`
	}
	var firstContent contentPage
	response := requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/content?q=http+content&type=work&status=published&limit=10", nil, &firstContent)
	if response.StatusCode != http.StatusOK || len(firstContent.Items) != 10 || firstContent.NextCursor == nil {
		t.Fatalf("first content page failed: status=%d page=%#v", response.StatusCode, firstContent)
	}
	var secondContent contentPage
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/content?q=http+content&type=work&status=published&limit=10&cursor="+url.QueryEscape(*firstContent.NextCursor), nil, &secondContent)
	if response.StatusCode != http.StatusOK || len(secondContent.Items) != 10 || secondContent.NextCursor == nil {
		t.Fatalf("second content page failed: status=%d page=%#v", response.StatusCode, secondContent)
	}
	var thirdContent contentPage
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/content?q=http+content&type=work&status=published&limit=10&cursor="+url.QueryEscape(*secondContent.NextCursor), nil, &thirdContent)
	if response.StatusCode != http.StatusOK || len(thirdContent.Items) != 1 || thirdContent.Items[0].ID != oldWorkID || thirdContent.NextCursor != nil {
		t.Fatalf("third content page failed: status=%d page=%#v", response.StatusCode, thirdContent)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/content?status=open", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid content filter status: %d", response.StatusCode)
	}
	var moderated admin.ContentItem
	response = requestJSON(t, adminClient, http.MethodPatch, server.URL+"/api/v1/admin/content/"+oldWorkID.String(), map[string]any{
		"status": "hidden"}, &moderated)
	if response.StatusCode != http.StatusOK || moderated.ID != oldWorkID || moderated.Status != "hidden" {
		t.Fatalf("exact HTTP content response failed: status=%d item=%#v", response.StatusCode, moderated)
	}

	oldMediaID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,uploaded_filename,size_bytes,storage_backend,storage_key,created_at)
		VALUES($1,$2,'image','HTTP media target','/media/http-media-target.jpg','image/jpeg','review','upload','personal','http-media-target.jpg',128,'local_file',$3,now() - interval '2 hours')`, oldMediaID, administrator.ID, oldMediaID.String()+".jpg"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,uploaded_filename,size_bytes,storage_backend,storage_key,created_at)
		SELECT $1,'image','HTTP media pressure '||value,'/media/http-media-pressure-'||value||'.jpg','image/jpeg','review','upload','personal','http-media-pressure-'||value||'.jpg',128,'local_file','http-media-pressure-'||value||'.jpg',now() - interval '1 hour'
		FROM generate_series(1,20) value`, administrator.ID); err != nil {
		t.Fatal(err)
	}

	type mediaPage struct {
		Items      []admin.MediaItem `json:"items"`
		NextCursor *string           `json:"nextCursor"`
	}
	var firstMedia mediaPage
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/media?q=http+media&kind=image&status=review&limit=10", nil, &firstMedia)
	if response.StatusCode != http.StatusOK || len(firstMedia.Items) != 10 || firstMedia.NextCursor == nil {
		t.Fatalf("first media page failed: status=%d page=%#v", response.StatusCode, firstMedia)
	}
	var secondMedia mediaPage
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/media?q=http+media&kind=image&status=review&limit=10&cursor="+url.QueryEscape(*firstMedia.NextCursor), nil, &secondMedia)
	if response.StatusCode != http.StatusOK || len(secondMedia.Items) != 10 || secondMedia.NextCursor == nil {
		t.Fatalf("second media page failed: status=%d page=%#v", response.StatusCode, secondMedia)
	}
	var thirdMedia mediaPage
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/media?q=http+media&kind=image&status=review&limit=10&cursor="+url.QueryEscape(*secondMedia.NextCursor), nil, &thirdMedia)
	if response.StatusCode != http.StatusOK || len(thirdMedia.Items) != 1 || thirdMedia.Items[0].ID != oldMediaID || thirdMedia.NextCursor != nil {
		t.Fatalf("third media page failed: status=%d page=%#v", response.StatusCode, thirdMedia)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/media?kind=archive", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid media filter status: %d", response.StatusCode)
	}
	var reviewed admin.MediaItem
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/media/"+oldMediaID.String()+"/review", map[string]any{
		"status": "rejected"}, &reviewed)
	if response.StatusCode != http.StatusOK || reviewed.ID != oldMediaID || reviewed.ScanStatus != "rejected" || reviewed.ScannedAt == nil {
		t.Fatalf("exact HTTP media response failed: status=%d item=%#v", response.StatusCode, reviewed)
	}
	if reviewed.ScannedAt.Before(time.Now().Add(-time.Minute)) {
		t.Fatalf("HTTP media review timestamp was not refreshed: %v", reviewed.ScannedAt)
	}
}
