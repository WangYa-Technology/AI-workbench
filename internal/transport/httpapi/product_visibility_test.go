package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/discovery"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestPublicProductVisibilityAcrossEntrypoints(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := context.Background()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	guest, buyerClient, sellerClient, adminClient := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	registerGovernanceUser(t, buyerClient, server.URL, "visibility_buyer")
	seller := registerGovernanceUser(t, sellerClient, server.URL, "visibility_seller")
	operator := registerGovernanceUser(t, adminClient, server.URL, "visibility_admin")
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, operator.ID); err != nil {
		t.Fatal(err)
	}
	origin, asset, product := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
		VALUES ($1,$3,'image','Visibility origin','/media/origin.jpg','image/jpeg','clean','upload','hcai-commercial-standard-v1','local_file','origin.jpg'),
		($2,$3,'image','Visibility source','/media/source.jpg','image/jpeg','clean','upload','hcai-commercial-standard-v1','local_file','source.jpg')`, origin, asset, seller.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE assets SET origin_asset_id=$2 WHERE id=$1`, asset, origin); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO products(id,seller_id,asset_id,title,description,product_type,category,price_cents,currency,license_code,status,ai_disclosure,included_files,compatibility)
		VALUES($1,$2,$3,'Visibility product','Public visibility regression','workflow','market_workflow',1200,'USD','hcai-commercial-standard-v1','active','AI assisted','[]','HCAI')`, product, seller.ID, asset); err != nil {
		t.Fatal(err)
	}

	assertPublic := func(t *testing.T, visible, sellerActive bool) {
		t.Helper()
		want := 0
		if visible {
			want = 1
		}
		for _, client := range []*http.Client{guest, buyerClient} {
			var page marketplace.ProductPage
			response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/products?q=visibility&category=market_workflow", nil, &page)
			if response.StatusCode != http.StatusOK || page.Total != want || len(page.Items) != want || page.CategoryCounts["market_workflow"] != want {
				t.Fatalf("catalog visibility: status=%d page=%+v want=%d", response.StatusCode, page, want)
			}
			detailStatus := http.StatusNotFound
			if visible {
				detailStatus = http.StatusOK
			}
			response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/products/"+product.String(), nil, nil)
			if response.StatusCode != detailStatus {
				t.Fatalf("detail status=%d want=%d", response.StatusCode, detailStatus)
			}
			for _, kind := range []string{"product", "creator"} {
				var results discovery.SearchPage
				response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/search?q=visibility&types="+kind, nil, &results)
				if response.StatusCode != http.StatusOK || results.Total != want || len(results.Items) != want {
					t.Fatalf("%s search status=%d results=%+v want=%d", kind, response.StatusCode, results, want)
				}
			}
			var profile discovery.CreatorProfile
			response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/creators/"+seller.Handle, nil, &profile)
			if !sellerActive {
				if response.StatusCode != http.StatusNotFound {
					t.Fatalf("inactive seller profile status=%d", response.StatusCode)
				}
			} else if response.StatusCode != http.StatusOK || profile.ProductsTotal != want || len(profile.Products) != want {
				t.Fatalf("creator catalog status=%d profile=%+v want=%d", response.StatusCode, profile, want)
			}
		}
		var index admin.DiscoveryIndexRun
		response := requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/discovery/index/analyze", nil, &index)
		if response.StatusCode != http.StatusOK || index.DocumentCounts["products"] != int64(want) || index.DocumentCounts["creators"] != int64(want) {
			t.Fatalf("index visibility: status=%d counts=%v want=%d", response.StatusCode, index.DocumentCounts, want)
		}
	}
	assertPublic(t, true, true)
	for _, change := range []struct {
		name, hide, restore string
		id                  uuid.UUID
	}{
		{"origin_review", `UPDATE assets SET scan_status='review' WHERE id=$1`, `UPDATE assets SET scan_status='clean' WHERE id=$1`, origin},
		{"origin_rejected", `UPDATE assets SET scan_status='rejected' WHERE id=$1`, `UPDATE assets SET scan_status='clean' WHERE id=$1`, origin},
		{"asset_rejected", `UPDATE assets SET scan_status='rejected' WHERE id=$1`, `UPDATE assets SET scan_status='clean' WHERE id=$1`, asset},
		{"seller_suspended", `UPDATE users SET status='suspended' WHERE id=$1`, `UPDATE users SET status='active' WHERE id=$1`, seller.ID},
		{"license_retired", `UPDATE licenses SET status='retired' WHERE code='hcai-commercial-standard-v1' AND $1::uuid IS NOT NULL`, `UPDATE licenses SET status='active' WHERE code='hcai-commercial-standard-v1' AND $1::uuid IS NOT NULL`, product},
		{"product_paused", `UPDATE products SET status='paused' WHERE id=$1`, `UPDATE products SET status='active' WHERE id=$1`, product},
		{"product_removed", `UPDATE products SET status='removed' WHERE id=$1`, `UPDATE products SET status='active' WHERE id=$1`, product},
	} {
		t.Run(change.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, change.hide, change.id); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := pool.Exec(ctx, change.restore, change.id); err != nil {
					t.Fatal(err)
				}
				assertPublic(t, true, true)
			}()
			assertPublic(t, false, change.name != "seller_suspended")
			// A hidden product cannot be attached by another user. Its active
			// seller can still ask for help about their own unpublished product.
			input := map[string]any{"category": "general_support", "subject": "Product visibility question", "details": "Please help review the visibility of this catalog product.", "relatedResourceType": "product", "relatedResourceId": product, "locale": "en-US"}
			response := requestJSON(t, buyerClient, http.MethodPost, server.URL+"/api/v1/support/cases", input, nil)
			if response.StatusCode != http.StatusNotFound {
				t.Fatalf("hidden product support association status=%d", response.StatusCode)
			}
			if change.name != "seller_suspended" {
				response = requestJSON(t, sellerClient, http.MethodPost, server.URL+"/api/v1/support/cases", input, nil)
				if response.StatusCode != http.StatusCreated {
					t.Fatalf("seller cannot reference own product: status=%d", response.StatusCode)
				}
			}
		})
	}
}
