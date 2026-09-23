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
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestProductFileDraftHTTPAndIndexedReview(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := context.Background()
	root := t.TempDir()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: root, WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	sellerClient, reviewerClient, guest := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	seller := registerGovernanceUser(t, sellerClient, server.URL, "files_seller")
	reviewer := registerGovernanceUser(t, reviewerClient, server.URL, "files_reviewer")
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, reviewer.ID); err != nil {
		t.Fatal(err)
	}
	files := []marketplace.ProductFile{{AssetID: uuid.New(), Name: "first.txt"}, {AssetID: uuid.New(), Name: "second.txt"}}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(root, file.AssetID.String()+".txt"), []byte("Original content of "+file.Name), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key) VALUES($1,$2,'document','Private source',$3,'text/plain','clean','upload','hcai-commercial-standard-v1','local_file',$1::uuid::text||'.txt')`, file.AssetID, seller.ID, "/api/v1/assets/"+file.AssetID.String()+"/content"); err != nil {
			t.Fatal(err)
		}
	}
	draft := marketplace.ProductDraft{Title: "True file list", Description: "Two independent original files.", ProductType: "asset", Category: "market_asset", AssetID: files[0].AssetID, PriceCents: 900, Currency: "USD", LicenseCode: "hcai-commercial-standard-v1", AIDisclosure: "Original text; seller-reviewed.", IncludedFiles: []string{files[0].Name, files[1].Name}, Files: files}
	endpoint := server.URL + "/api/v1/seller/products"
	var item marketplace.SellerProduct
	if r := requestPaymentJSON(t, sellerClient, "POST", endpoint, "create-real-file-list", marketplace.ListingMutation{Draft: &draft}, &item); r.StatusCode != 200 || len(item.Files) != 2 || r.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("create", r.StatusCode, item)
	}
	url := server.URL + "/api/v1/admin/products/" + item.ID.String() + "/content?kind=source&version=" + item.Version
	for _, q := range []string{"", "&fileIndex=2", "&fileIndex=-1", "&fileIndex=20", "&fileIndex=01", "&fileIndex=1.0", "&fileIndex=", "&fileIndex=1&fileIndex=0", "&fileIndex=1&key=arbitrary"} {
		if r := requestJSON(t, reviewerClient, "GET", url+q, nil, nil); r.StatusCode != 422 {
			t.Fatal("invalid selector", q, r.StatusCode)
		}
	}
	for _, c := range []struct {
		client *http.Client
		status int
	}{{guest, 401}, {sellerClient, 403}} {
		if r := requestJSON(t, c.client, "GET", url+"&fileIndex=1", nil, nil); r.StatusCode != c.status {
			t.Fatal("unauthorized file review", r.StatusCode)
		}
	}
	req, _ := http.NewRequest("GET", url+"&fileIndex=1", nil)
	req.Header.Set("Range", "bytes=0-15")
	r, err := reviewerClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil || r.StatusCode != 206 || string(body) != "Original content" || r.Header.Get("Cache-Control") != "private, no-store" || !strings.HasPrefix(r.Header.Get("Content-Disposition"), "attachment;") {
		t.Fatal("indexed range", r.StatusCode, string(body), err)
	}
	r, err = reviewerClient.Get(url + "&fileIndex=1")
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil || r.StatusCode != 200 || string(body) != "Original content of second.txt" {
		t.Fatal("wrong source member", r.StatusCode, string(body), err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='marketplace.listing_file_reviewed' AND resource_id=$1 AND metadata->>'fileIndex'='1' AND metadata->>'assetId'=$2`, item.ID, files[1].AssetID.String()).Scan(&count); err != nil || count != 2 {
		t.Fatal("member audit", count, err)
	}
	shortServer := shortMediaDeadlineServer(t, server.Config.Handler)
	for _, requested := range []string{"", "bytes=0-15"} {
		req, _ := http.NewRequest("GET", shortServer.URL+strings.TrimPrefix(url, server.URL)+"&fileIndex=1", nil)
		req.Header.Set("Range", requested)
		response, err := reviewerClient.Do(req)
		if err != nil {
			t.Fatal("review file retained the ordinary response deadline", err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		expectedStatus, expectedBody := http.StatusOK, "Original content of second.txt"
		if requested != "" {
			expectedStatus, expectedBody = http.StatusPartialContent, "Original content"
		}
		if err != nil || response.StatusCode != expectedStatus || string(body) != expectedBody || response.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatal("review file changed with its transfer budget", response.StatusCode, string(body), err)
		}
	}
	if r := requestPaymentJSON(t, sellerClient, "POST", endpoint+"/"+item.ID.String()+"/submit", "bundle-submit-guard", marketplace.ListingMutation{ExpectedVersion: item.Version, RightsConfirmed: true}, nil); r.StatusCode != 200 {
		t.Fatal("bundle review submission", r.StatusCode)
	}
	if r := requestJSON(t, guest, "GET", server.URL+"/api/v1/products/"+item.ID.String(), nil, nil); r.StatusCode != 404 {
		t.Fatal("draft public", r.StatusCode)
	}
	if r := requestJSON(t, guest, "GET", server.URL+"/api/v1/assets/"+files[1].AssetID.String()+"/content", nil, nil); r.StatusCode != 403 {
		t.Fatal("member public", r.StatusCode)
	}
	if _, err = pool.Exec(ctx, `UPDATE assets SET scan_status='pending' WHERE id=$1`, files[1].AssetID); err != nil {
		t.Fatal(err)
	}
	if r := requestJSON(t, reviewerClient, "GET", url+"&fileIndex=1", nil, nil); r.StatusCode != 409 {
		t.Fatal("stale member review", r.StatusCode)
	}
	if r := requestJSON(t, sellerClient, "GET", endpoint+"/"+item.ID.String(), nil, &item); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	current := server.URL + "/api/v1/admin/products/" + item.ID.String() + "/content?kind=source&fileIndex=1&version=" + item.Version
	if r := requestJSON(t, reviewerClient, "GET", current, nil, nil); r.StatusCode != 404 {
		t.Fatal("unscanned member downloaded", r.StatusCode)
	}
}
