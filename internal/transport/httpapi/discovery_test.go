package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/discovery"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestDiscoverySearchAndCreatorPublicBoundary(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	viewerClient := testHTTPClient(t)
	viewer := registerGovernanceUser(t, viewerClient, server.URL, "discovery_viewer")
	authorClient := testHTTPClient(t)
	author := registerGovernanceUser(t, authorClient, server.URL, "signal_author")
	ctx := context.Background()
	cleanAssetID, reviewAssetID, draftAssetID := uuid.New(), uuid.New(), uuid.New()
	workID, reviewWorkID, draftWorkID := uuid.New(), uuid.New(), uuid.New()
	productID, pausedProductID := uuid.New(), uuid.New()
	productAssetID := uuid.New()
	openDemandID, assignedDemandID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code) VALUES
		 ($1,$4,'image','Cinematic clean asset','/media/clean.jpg','image/jpeg','clean','delivery','hcai-commercial-standard-v1'),
		 ($2,$4,'image','Cinematic review asset','/media/review.jpg','image/jpeg','review','delivery','hcai-commercial-standard-v1'),
		 ($3,$4,'image','Cinematic draft asset','/media/draft.jpg','image/jpeg','clean','delivery','hcai-commercial-standard-v1')`,
		cleanAssetID, reviewAssetID, draftAssetID, author.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO works(id,author_id,asset_id,title,summary,prompt,prompt_visibility,model_name,status,ai_disclosure,published_at) VALUES
		 ($1,$4,$5,'Cinematic Signal Study','Public architectural signal','cinematic signal prompt','public','Local Test','published','Deterministic local test media.',now()),
		 ($2,$4,$6,'Cinematic Unsafe Review','Must stay private','secret cinematic prompt','public','Local Test','published','Under review.',now()),
		 ($3,$4,$7,'Cinematic Draft Study','Must stay private','secret cinematic prompt','public','Local Test','draft','Draft.',NULL)`,
		workID, reviewWorkID, draftWorkID, author.ID, cleanAssetID, reviewAssetID, draftAssetID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
	 VALUES($1,$2,'image','Private product source','/media/private.jpg','image/jpeg','clean','delivery','hcai-commercial-standard-v1')`, productAssetID, author.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status,ai_disclosure,included_files,compatibility) VALUES
		 ($1,$3,$4,'Cinematic Signal Workflow','Reusable cinematic production workflow','workflow',2400,'USD','hcai-commercial-standard-v1','active','Deterministic local test media.','[]','HCAI'),
		 ($2,$3,$4,'Cinematic Paused Workflow','Must stay private','workflow',2400,'USD','hcai-commercial-standard-v1','paused','Paused.','[]','HCAI')`,
		productID, pausedProductID, author.ID, productAssetID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO demands(id,client_id,title,summary,brief,deliverable_type,budget_cents,currency,deadline,status) VALUES
		 ($1,$3,'Cinematic Campaign Brief','Open cinematic production brief','Create a controlled cinematic launch film.','video',50000,'USD',now()+interval '30 days','open'),
		 ($2,$3,'Cinematic Assigned Brief','Must stay private','Assigned private work.','video',50000,'USD',now()+interval '30 days','assigned')`,
		openDemandID, assignedDemandID, viewer.ID); err != nil {
		t.Fatal(err)
	}

	var page discovery.SearchPage
	response := requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/search?q=cinematic&limit=2", nil, &page)
	if response.StatusCode != http.StatusOK || page.Total != 3 || len(page.Items) != 2 || !page.HasMore || page.PolicyVersion != 1 || page.PolicyName == "" {
		t.Fatalf("unexpected first search page: status=%d page=%#v", response.StatusCode, page)
	}
	for _, item := range page.Items {
		if item.ID == reviewWorkID || item.ID == draftWorkID || item.ID == pausedProductID || item.ID == assignedDemandID || len(item.RankSignals) == 0 {
			t.Fatalf("private result or missing ranking evidence: %#v", item)
		}
	}
	response = requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/search?q=cinematic&types=work&page=1&limit=12", nil, &page)
	if response.StatusCode != http.StatusOK || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Path != "/works/"+workID.String() {
		t.Fatalf("typed work search failed: status=%d page=%#v", response.StatusCode, page)
	}
	response = requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/search?q=cinematic&page=100&limit=12", nil, &page)
	if response.StatusCode != http.StatusOK || page.Total != 3 || len(page.Items) != 0 || page.HasMore {
		t.Fatalf("out-of-range page lost bounded count evidence: status=%d page=%#v", response.StatusCode, page)
	}
	response = requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/search?q=x", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("short search query was accepted: %d", response.StatusCode)
	}

	var profile discovery.CreatorProfile
	response = requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/creators/"+author.Handle, nil, &profile)
	if response.StatusCode != http.StatusOK || len(profile.Works) != 1 || len(profile.Products) != 1 || profile.Works[0].ID != workID || profile.Products[0].ID != productID {
		t.Fatalf("creator public projection failed: status=%d profile=%#v", response.StatusCode, profile)
	}
	response = requestJSON(t, viewerClient, http.MethodPut, server.URL+"/api/v1/community/authors/"+author.ID.String()+"/follow", map[string]any{"active": true}, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("follow creator: %d", response.StatusCode)
	}
	response = requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/creators/"+author.Handle, nil, &profile)
	if response.StatusCode != http.StatusOK || !profile.ViewerFollowing || profile.FollowerCount != 1 {
		t.Fatalf("creator relationship state missing: status=%d profile=%#v", response.StatusCode, profile)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET status='deleted' WHERE id=$1`, author.ID); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, viewerClient, http.MethodGet, server.URL+"/api/v1/creators/"+author.Handle, nil, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted creator remained public: %d", response.StatusCode)
	}
}
