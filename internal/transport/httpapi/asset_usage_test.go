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

func TestOwnedAssetUsageHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	ownerClient := testHTTPClient(t)
	owner := registerGovernanceUser(t, ownerClient, server.URL, "usage_http_owner")
	otherClient := testHTTPClient(t)
	registerGovernanceUser(t, otherClient, server.URL, "usage_http_other")

	ctx := context.Background()
	assetID, generationID, workID, productID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,family_id,version_number)
		VALUES($1,$2,'image','HTTP usage source','/media/http-usage.jpg','image/jpeg','clean','demo','hcai-personal-v1',$1,1)`, assetID, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO generations(id,owner_id,mode,provider,model_name,prompt,status,progress,estimated_cost_cents,source_asset_id)
		VALUES($1,$2,'image','local_test','HCAI Image Local','HTTP downstream generation','succeeded',100,35,$3)`, generationID, owner.ID, assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO works(id,author_id,asset_id,title,summary,model_name,status,ai_disclosure)
		VALUES($1,$2,$3,'Hidden historical Work','Historical usage remains auditable.','Imported asset','hidden','Owner supplied source.')`, workID, owner.ID, assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status)
		VALUES($1,$2,$3,'Paused historical product','Historical usage remains auditable.','asset',1200,'USD','hcai-personal-v1','paused')`, productID, owner.ID, assetID); err != nil {
		t.Fatal(err)
	}

	var item assets.Asset
	response := requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/assets/"+assetID.String(), nil, &item)
	if response.StatusCode != http.StatusOK || len(item.Usages) != 3 {
		t.Fatalf("owner usage response: status=%d usages=%#v", response.StatusCode, item.Usages)
	}
	want := map[string]uuid.UUID{"generation": generationID, "work": workID, "product": productID}
	for _, usage := range item.Usages {
		if want[usage.Kind] != usage.ResourceID || usage.AssetID != assetID || usage.AssetVersion != 1 {
			t.Fatalf("unexpected HTTP usage: %#v", usage)
		}
		if usage.Kind == "generation" && (usage.TargetPath == nil || *usage.TargetPath != "/workspace/generations?generationId="+generationID.String()) {
			t.Fatalf("generation deep link missing: %#v", usage)
		}
		if (usage.Kind == "work" || usage.Kind == "product") && usage.TargetPath != nil {
			t.Fatalf("unavailable historical resource exposed a deep link: %#v", usage)
		}
		delete(want, usage.Kind)
	}
	if len(want) != 0 {
		t.Fatalf("missing HTTP usage kinds: %#v", want)
	}
	var firstPage assets.UsagePage
	response = requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/assets/"+assetID.String()+"/usages?limit=2", nil, &firstPage)
	if response.StatusCode != http.StatusOK || len(firstPage.Items) != 2 || firstPage.NextCursor == nil {
		t.Fatalf("first usage page: status=%d page=%#v", response.StatusCode, firstPage)
	}
	var secondPage assets.UsagePage
	response = requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/assets/"+assetID.String()+"/usages?limit=2&cursor="+*firstPage.NextCursor, nil, &secondPage)
	if response.StatusCode != http.StatusOK || len(secondPage.Items) != 1 || secondPage.NextCursor != nil {
		t.Fatalf("second usage page: status=%d page=%#v", response.StatusCode, secondPage)
	}
	firstKeys := map[string]bool{}
	for _, usage := range firstPage.Items {
		firstKeys[usage.Kind+":"+usage.ResourceID.String()] = true
	}
	if firstKeys[secondPage.Items[0].Kind+":"+secondPage.Items[0].ResourceID.String()] {
		t.Fatalf("usage cursor returned a duplicate: %#v", secondPage.Items[0])
	}

	response = requestJSON(t, otherClient, http.MethodGet, server.URL+"/api/v1/assets/"+assetID.String(), nil, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("another account accessed owner usage graph: %d", response.StatusCode)
	}
	response = requestJSON(t, testHTTPClient(t), http.MethodGet, server.URL+"/api/v1/assets/"+assetID.String(), nil, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous account accessed owner usage graph: %d", response.StatusCode)
	}
	response = requestJSON(t, otherClient, http.MethodGet, server.URL+"/api/v1/assets/"+assetID.String()+"/usages?cursor=modified", nil, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("usage ownership was not checked before cursor parsing: %d", response.StatusCode)
	}
	response = requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/assets/"+assetID.String()+"/usages?cursor=modified", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("modified usage cursor status: %d", response.StatusCode)
	}
	response = requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/assets/"+assetID.String()+"/usages?limit=51", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("oversized usage page status: %d", response.StatusCode)
	}
}
