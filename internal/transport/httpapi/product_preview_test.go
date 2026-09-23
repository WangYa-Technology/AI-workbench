package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/discovery"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestProductPreviewSeparatesPublicSampleFromDelivery(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := context.Background()
	root := t.TempDir()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: root, WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	guest, sellerClient, otherClient := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	seller := registerGovernanceUser(t, sellerClient, server.URL, "sample_seller")
	other := registerGovernanceUser(t, otherClient, server.URL, "sample_other")
	original, sample, product, work := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, f := range []struct {
		id   uuid.UUID
		body string
	}{{original, "PRIVATE original delivery. Never publish this file."}, {sample, "Public sample only."}} {
		if err := os.WriteFile(filepath.Join(root, f.id.String()+".txt"), []byte(f.body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
		 VALUES($1,$2,'document','Separate sample test',$3,'text/plain','clean','upload','hcai-commercial-standard-v1','local_file',$1::uuid::text||'.txt')`, f.id, seller.ID, "/api/v1/assets/"+f.id.String()+"/content"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status,ai_disclosure,included_files,compatibility)
	 VALUES($1,$2,$3,'Separate sample workflow','Private delivery with public sample','workflow',1900,'USD','hcai-commercial-standard-v1','active','AI assisted','[]','HCAI')`, product, seller.ID, original); err != nil {
		t.Fatal(err)
	}
	// Legacy public references must not override the private-delivery boundary.
	if _, err := pool.Exec(ctx, `INSERT INTO works(id,author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at)
	 VALUES($1,$2,$3,'Legacy source work','Legacy shared original','Imported','published','AI assisted',now())`, work, seller.ID, original); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE system_settings SET site_configuration=jsonb_set(site_configuration,'{siteIconUrl}',to_jsonb($1::text)) WHERE singleton=true`, "/api/v1/assets/"+original.String()+"/content"); err != nil {
		t.Fatal(err)
	}
	getProduct := func() marketplace.Product {
		t.Helper()
		var result marketplace.Product
		if response := requestJSON(t, guest, http.MethodGet, server.URL+"/api/v1/products/"+product.String(), nil, &result); response.StatusCode != http.StatusOK {
			t.Fatalf("product status=%d", response.StatusCode)
		}
		return result
	}
	initial := getProduct()
	if initial.MediaURL != "" || initial.PreviewAssetID != nil {
		t.Fatalf("delivery leaked as fallback: %+v", initial)
	}
	endpoint := server.URL + "/api/v1/products/" + product.String() + "/preview"
	input := map[string]any{"previewAssetId": sample, "offerVersion": initial.OfferVersion}
	var candidates assets.AssetPage
	candidateURL := server.URL + "/api/v1/assets?purpose=product_preview"
	if response := requestJSON(t, guest, http.MethodGet, candidateURL, nil, nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatal("guest read private candidates", response.StatusCode)
	}
	if response := requestJSON(t, sellerClient, http.MethodGet, candidateURL, nil, &candidates); response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "private, no-store" || candidates.Total != 1 || len(candidates.Items) != 1 || candidates.Items[0].ID != sample {
		t.Fatal("candidate projection", response.StatusCode, candidates)
	}
	if response := requestJSON(t, otherClient, http.MethodGet, candidateURL, nil, &candidates); response.StatusCode != http.StatusOK || candidates.Total != 0 || len(candidates.Items) != 0 {
		t.Fatal("candidate owner isolation", response.StatusCode, candidates)
	}
	for _, query := range []string{"&source=purchase", "&purpose=unknown", "&limit=0", "&limit=", "&limit=-1", "&limit=51", "&limit=1&limit=2", "&cursor=bad"} {
		if response := requestJSON(t, sellerClient, http.MethodGet, candidateURL+query, nil, nil); response.StatusCode != http.StatusUnprocessableEntity {
			t.Fatal("invalid candidates query accepted", query, response.StatusCode)
		}
	}
	for _, c := range []struct {
		client   *http.Client
		expected int
	}{{guest, http.StatusUnauthorized}, {otherClient, http.StatusNotFound}} {
		if response := requestJSON(t, c.client, http.MethodPut, endpoint, input, nil); response.StatusCode != c.expected {
			t.Fatalf("preview mutation boundary status=%d want=%d", response.StatusCode, c.expected)
		}
	}
	for _, bad := range []map[string]any{
		{"previewAssetId": original, "offerVersion": initial.OfferVersion},
		{"previewAssetId": uuid.New(), "offerVersion": initial.OfferVersion},
		{"offerVersion": initial.OfferVersion},
	} {
		if response := requestJSON(t, sellerClient, http.MethodPut, endpoint, bad, nil); response.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("invalid preview accepted: %d", response.StatusCode)
		}
	}
	// A valid UUID alone must not publish another user's or unsafe asset.
	for _, state := range []struct {
		name   string
		owner  uuid.UUID
		scan   string
		origin *uuid.UUID
	}{{"foreign owner", other.ID, "clean", nil}, {"pending scan", seller.ID, "pending", nil},
		{"review scan", seller.ID, "review", nil}, {"delivery alias", seller.ID, "clean", &original}} {
		if _, err := pool.Exec(ctx, `UPDATE assets SET owner_id=$2,scan_status=$3,origin_asset_id=$4 WHERE id=$1`, sample, state.owner, state.scan, state.origin); err != nil {
			t.Fatal(err)
		}
		if response := requestJSON(t, sellerClient, http.MethodPut, endpoint, input, nil); response.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%s accepted as preview: %d", state.name, response.StatusCode)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE assets SET owner_id=$2,scan_status='clean',origin_asset_id=NULL WHERE id=$1`, sample, seller.ID); err != nil {
		t.Fatal(err)
	}
	var updated marketplace.PreviewUpdate
	if response := requestJSON(t, sellerClient, http.MethodPut, endpoint, input, &updated); response.StatusCode != http.StatusOK || updated.OfferVersion == initial.OfferVersion {
		t.Fatalf("sample not published/versioned: %d %+v", response.StatusCode, updated)
	}
	if response := requestJSON(t, sellerClient, http.MethodPut, endpoint, map[string]any{"previewAssetId": nil, "offerVersion": initial.OfferVersion}, nil); response.StatusCode != http.StatusConflict {
		t.Fatalf("stale selection overwrote sample: %d", response.StatusCode)
	}
	current := getProduct()
	previewURL := "/api/v1/assets/" + sample.String() + "/content"
	if current.MediaURL != previewURL || current.PreviewAssetID == nil || *current.PreviewAssetID != sample {
		t.Fatalf("wrong preview projection: %+v", current)
	}
	var catalog marketplace.ProductPage
	response := requestJSON(t, guest, http.MethodGet, server.URL+"/api/v1/products", nil, &catalog)
	if response.StatusCode != http.StatusOK || len(catalog.Items) != 1 || catalog.Items[0].MediaURL != previewURL {
		t.Fatalf("catalog leaked original: %+v", catalog)
	}
	var creator discovery.CreatorProfile
	response = requestJSON(t, guest, http.MethodGet, server.URL+"/api/v1/creators/"+seller.Handle, nil, &creator)
	if response.StatusCode != http.StatusOK || len(creator.Products) != 1 || creator.Products[0].MediaURL != previewURL || creator.WorksTotal != 0 {
		t.Fatalf("creator leaked original: %+v", creator)
	}
	var search discovery.SearchPage
	response = requestJSON(t, guest, http.MethodGet, server.URL+"/api/v1/search?q=sample&types=product", nil, &search)
	if response.StatusCode != http.StatusOK || len(search.Items) != 1 || search.Items[0].MediaURL == nil || *search.Items[0].MediaURL != previewURL {
		t.Fatalf("search leaked original: %+v", search)
	}
	read := func(client *http.Client, id uuid.UUID, rangeValue string, want int, body string) {
		t.Helper()
		request, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/assets/"+id.String()+"/content", nil)
		if err != nil {
			t.Fatal(err)
		}
		if rangeValue != "" {
			request.Header.Set("Range", rangeValue)
		}
		result, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer result.Body.Close()
		data, err := io.ReadAll(result.Body)
		if err != nil {
			t.Fatal(err)
		}
		if result.StatusCode != want || (body != "" && string(data) != body) {
			t.Fatalf("media status=%d want=%d body=%q", result.StatusCode, want, data)
		}
		if want == http.StatusOK || want == http.StatusPartialContent {
			if result.Header.Get("Cache-Control") != "private, no-store" {
				t.Fatal("revocable media was cacheable")
			}
		}
	}
	read(guest, sample, "", http.StatusOK, "Public sample only.")
	read(guest, sample, "bytes=0-5", http.StatusPartialContent, "Public")
	read(guest, original, "", http.StatusForbidden, "")
	read(otherClient, original, "bytes=0-5", http.StatusForbidden, "")
	read(sellerClient, original, "", http.StatusOK, "PRIVATE original delivery. Never publish this file.")
	for _, kind := range []string{"symlink", "hardlink"} {
		t.Run("filesystem_alias_"+kind, func(t *testing.T) {
			samplePath := filepath.Join(root, sample.String()+".txt")
			originalPath := filepath.Join(root, original.String()+".txt")
			if err := os.Remove(samplePath); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := os.Remove(samplePath); err != nil {
					t.Error(err)
				}
				if err := os.WriteFile(samplePath, []byte("Public sample only."), 0600); err != nil {
					t.Error(err)
				}
			}()
			link := os.Link
			if kind == "symlink" {
				link = os.Symlink
			}
			if err := link(originalPath, samplePath); err != nil {
				t.Fatal(err)
			}
			for _, rangeValue := range []string{"", "bytes=0-5"} {
				req, err := http.NewRequest(http.MethodGet, server.URL+previewURL, nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Range", rangeValue)
				response, err := guest.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				body, readErr := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if readErr != nil || response.StatusCode != http.StatusInternalServerError || strings.Contains(string(body), "PRIVATE") {
					t.Fatalf("private original leaked through sample alias: status=%d body=%q err=%v", response.StatusCode, body, readErr)
				}
			}
		})
	}
	read(guest, sample, "bytes=0-5", http.StatusPartialContent, "Public")
	read(sellerClient, original, "", http.StatusOK, "PRIVATE original delivery. Never publish this file.")
	// Revoking any catalog visibility prerequisite revokes this sample's public grant.
	for _, mutation := range []struct {
		hide, restore string
		id            uuid.UUID
	}{
		{`UPDATE products SET status='paused' WHERE id=$1`, `UPDATE products SET status='active' WHERE id=$1`, product},
		{`UPDATE users SET status='suspended' WHERE id=$1`, `UPDATE users SET status='active' WHERE id=$1`, seller.ID},
		{`UPDATE licenses SET status='retired' WHERE code=(SELECT license_code FROM products WHERE id=$1)`, `UPDATE licenses SET status='active' WHERE code=(SELECT license_code FROM products WHERE id=$1)`, product},
	} {
		if _, err := pool.Exec(ctx, mutation.hide, mutation.id); err != nil {
			t.Fatal(err)
		}
		read(guest, sample, "", http.StatusForbidden, "")
		if response := requestJSON(t, guest, http.MethodGet, server.URL+"/api/v1/products/"+product.String(), nil, nil); response.StatusCode != http.StatusNotFound {
			t.Fatalf("hidden product still visible: %d", response.StatusCode)
		}
		if _, err := pool.Exec(ctx, mutation.restore, mutation.id); err != nil {
			t.Fatal(err)
		}
		read(guest, sample, "", http.StatusOK, "Public sample only.")
	}
	if response := requestJSON(t, guest, http.MethodGet, server.URL+"/api/v1/works/"+work.String(), nil, nil); response.StatusCode != http.StatusNotFound {
		t.Fatalf("private original remained a public work: %d", response.StatusCode)
	}
	if _, err := pool.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, sample); err != nil {
		t.Fatal(err)
	}
	if getProduct().MediaURL != "" {
		t.Fatal("rejected sample leaked in product")
	}
	read(guest, sample, "", http.StatusNotFound, "")
	if _, err := pool.Exec(ctx, `UPDATE assets SET scan_status='clean' WHERE id=$1`, sample); err != nil {
		t.Fatal(err)
	}
	if response := requestJSON(t, sellerClient, http.MethodPut, endpoint, map[string]any{"previewAssetId": nil, "offerVersion": updated.OfferVersion}, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("remove sample: %d", response.StatusCode)
	}
	if getProduct().MediaURL != "" {
		t.Fatal("removed sample fell back to original")
	}
	read(guest, sample, "", http.StatusForbidden, "")
	read(guest, original, "", http.StatusForbidden, "")
}
