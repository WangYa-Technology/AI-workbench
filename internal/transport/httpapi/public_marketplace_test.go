package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/tasks"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestPublicMarketplaceReadAndAuthenticatedMutationBoundary(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	sellerClient := testHTTPClient(t)
	seller := registerGovernanceUser(t, sellerClient, server.URL, "public_market_seller")
	assetID, productID, taskID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
		VALUES($1,$2,'image','Public market Asset','/media/public-market.jpg','image/jpeg','clean','demo','hcai-commercial-standard-v1')`, assetID, seller.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status,ai_disclosure,included_files,compatibility)
		VALUES($1,$2,$3,'Public market workflow','A licensed workflow visible before sign-in.','workflow',1900,'USD','hcai-commercial-standard-v1','active','Local Test disclosure.','["Prompt guide"]','HCAI CHAT')`, productID, seller.ID, assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO demands(id,client_id,title,summary,brief,deliverable_type,budget_cents,currency,deadline,status,deliverables,acceptance_rules,rights_terms,ai_disclosure_requirement)
		VALUES($1,$2,'Public campaign brief','A production opportunity visible before sign-in.','Create a complete campaign image system for a global release.','image',42000,'USD',now()+interval '14 days','open','["Hero image"]','["Source files included"]','Commercial campaign rights.','Disclose AI-assisted production.')`, taskID, seller.ID); err != nil {
		t.Fatal(err)
	}

	anonymous := testHTTPClient(t)
	var products struct {
		Items []marketplace.Product `json:"items"`
	}
	response := requestJSON(t, anonymous, http.MethodGet, server.URL+"/api/v1/products", nil, &products)
	if response.StatusCode != http.StatusOK || len(products.Items) != 1 || products.Items[0].ID != productID || products.Items[0].OwnedAssetID != nil {
		t.Fatalf("anonymous product list lost its public projection: status=%d products=%#v", response.StatusCode, products.Items)
	}
	var product marketplace.Product
	response = requestJSON(t, anonymous, http.MethodGet, server.URL+"/api/v1/products/"+productID.String(), nil, &product)
	if response.StatusCode != http.StatusOK || product.ID != productID || product.License.Terms == "" {
		t.Fatalf("anonymous product detail missing license evidence: status=%d product=%#v", response.StatusCode, product)
	}

	var taskList struct {
		Items []tasks.Summary `json:"items"`
	}
	response = requestJSON(t, anonymous, http.MethodGet, server.URL+"/api/v1/tasks?status=open", nil, &taskList)
	if response.StatusCode != http.StatusOK || len(taskList.Items) != 1 || taskList.Items[0].ID != taskID {
		t.Fatalf("anonymous task list lost its public projection: status=%d tasks=%#v", response.StatusCode, taskList.Items)
	}
	var task tasks.Detail
	response = requestJSON(t, anonymous, http.MethodGet, server.URL+"/api/v1/tasks/"+taskID.String(), nil, &task)
	if response.StatusCode != http.StatusOK || task.ID != taskID || task.ViewerRole != "viewer" || len(task.Proposals) != 0 {
		t.Fatalf("anonymous task detail exposed the wrong role or proposals: status=%d task=%#v", response.StatusCode, task)
	}

	for _, target := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/v1/tasks?mine=true", nil},
		{http.MethodPost, "/api/v1/products/" + productID.String() + "/checkout", map[string]any{"licenseAccepted": true}},
		{http.MethodPost, "/api/v1/tasks/" + taskID.String() + "/proposals", map[string]any{"approach": "Private proposal"}},
		{http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Private mutation"}},
	} {
		response = requestJSON(t, anonymous, target.method, server.URL+target.path, target.body, nil)
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("anonymous mutation or private inventory was not rejected: %s %s -> %d", target.method, target.path, response.StatusCode)
		}
	}
}
