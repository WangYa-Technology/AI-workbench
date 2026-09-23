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

func TestProductPublicationHTTPAndAuditedFiles(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := context.Background()
	root := t.TempDir()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: root, WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	guest, sellerClient, adminClient := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	seller := registerGovernanceUser(t, sellerClient, server.URL, "listing_seller")
	reviewer := registerGovernanceUser(t, adminClient, server.URL, "listing_review")
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, reviewer.ID); err != nil {
		t.Fatal(err)
	}
	asset := uuid.New()
	body := "A private original for the exact submitted listing."
	if err := os.WriteFile(filepath.Join(root, asset.String()+".txt"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key) VALUES($1,$2,'document','Private original',$3,'text/plain','clean','upload','hcai-commercial-standard-v1','local_file',$1::uuid::text||'.txt')`, asset, seller.ID, "/api/v1/assets/"+asset.String()+"/content"); err != nil {
		t.Fatal(err)
	}
	draft := marketplace.ProductDraft{Title: "HTTP seller product", Description: "One original file.", ProductType: "asset", Category: "market_asset", AssetID: asset, PriceCents: 900, Currency: "USD", LicenseCode: "hcai-commercial-standard-v1", AIDisclosure: "Original independently uploaded text.", IncludedFiles: []string{"original.txt"}}
	endpoint := server.URL + "/api/v1/seller/products"
	for _, c := range []struct {
		client *http.Client
		key    string
		want   int
	}{{guest, "guest-create-command", 401}, {sellerClient, "", 422}, {sellerClient, "short", 422}} {
		if r := requestPaymentJSON(t, c.client, "POST", endpoint, c.key, marketplace.ListingMutation{Draft: &draft}, nil); r.StatusCode != c.want {
			t.Fatal("create boundary", r.StatusCode, c.want)
		}
	}
	var item marketplace.SellerProduct
	if r := requestPaymentJSON(t, sellerClient, "POST", endpoint, "http-create-command", marketplace.ListingMutation{Draft: &draft}, &item); r.StatusCode != 200 || r.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("create", r.StatusCode, item)
	}
	id := item.ID.String()
	for _, q := range []string{"?limit=0", "?limit=", "?limit=1&limit=2", "?status=draft&status=pending", "?status=unknown", "?q=unsupported", "?cursor=bad"} {
		if r := requestJSON(t, sellerClient, "GET", endpoint+q, nil, nil); r.StatusCode != 422 {
			t.Fatal("invalid filter", q, r.StatusCode)
		}
	}
	if r := requestJSON(t, adminClient, "GET", endpoint+"/"+id, nil, nil); r.StatusCode != 404 {
		t.Fatal("foreign seller detail", r.StatusCode)
	}
	if r := requestJSON(t, sellerClient, "GET", server.URL+"/api/v1/admin/products", nil, nil); r.StatusCode != 403 {
		t.Fatal("seller accessed review queue", r.StatusCode)
	}
	if r := requestJSON(t, guest, "GET", server.URL+"/api/v1/products/"+id, nil, nil); r.StatusCode != 404 {
		t.Fatal("private draft public", r.StatusCode)
	}
	content := server.URL + "/api/v1/admin/products/" + id + "/content?kind=source&version=" + item.Version
	for _, client := range []*http.Client{guest, sellerClient} {
		if r := requestJSON(t, client, "GET", content, nil, nil); r.StatusCode != 401 && r.StatusCode != 403 {
			t.Fatal("review file unauthorized", r.StatusCode)
		}
	}
	req, _ := http.NewRequest("GET", content, nil)
	req.Header.Set("Range", "bytes=0-8")
	response, err := adminClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	received, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 206 || string(received) != body[:9] || !strings.HasPrefix(response.Header.Get("Content-Disposition"), "attachment;") || response.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("authorized bytes", response.StatusCode, string(received), response.Header)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='marketplace.listing_file_reviewed' AND resource_id=$1`, item.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("missing read audit", count, err)
	}
	if r := requestPaymentJSON(t, sellerClient, "POST", endpoint+"/"+id+"/submit", "submit-without-rights", marketplace.ListingMutation{ExpectedVersion: item.Version}, nil); r.StatusCode != 422 {
		t.Fatal("rights omitted", r.StatusCode)
	}
	if r := requestPaymentJSON(t, sellerClient, "POST", endpoint+"/"+id+"/submit", "http-submit-command", marketplace.ListingMutation{ExpectedVersion: item.Version, RightsConfirmed: true}, &item); r.StatusCode != 200 || item.ReviewStatus != "pending" {
		t.Fatal("submit", r.StatusCode, item)
	}
	if r := requestJSON(t, adminClient, "GET", content, nil, nil); r.StatusCode != 409 {
		t.Fatal("stale file review", r.StatusCode)
	}
	in := marketplace.ListingMutation{ExpectedVersion: item.Version, Confirmed: true, Reason: "Independent file and license rights reviewed."}
	if r := requestPaymentJSON(t, sellerClient, "POST", server.URL+"/api/v1/admin/products/"+id+"/approve", "unauthorized-approval", in, nil); r.StatusCode != 403 {
		t.Fatal("seller approval", r.StatusCode)
	}
	if r := requestPaymentJSON(t, adminClient, "POST", server.URL+"/api/v1/admin/products/"+id+"/approve", "http-approve-command", in, &item); r.StatusCode != 200 || item.Status != "active" {
		t.Fatal("approve", r.StatusCode, item)
	}
	if r := requestJSON(t, guest, "GET", server.URL+"/api/v1/products/"+id, nil, nil); r.StatusCode != 200 {
		t.Fatal("approved not public", r.StatusCode)
	}
	if r := requestJSON(t, guest, "GET", server.URL+"/api/v1/assets/"+asset.String()+"/content", nil, nil); r.StatusCode != 403 {
		t.Fatal("original exposed", r.StatusCode)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id=$1 AND kind='marketplace.listing_reviewed' AND target_path=$2`, seller.ID, "/workspace/products/"+id).Scan(&count); err != nil || count != 1 {
		t.Fatal("seller notification", count, err)
	}
	in = marketplace.ListingMutation{ExpectedVersion: item.Version, Confirmed: true, Reason: "Hold publication while rights evidence is checked."}
	if r := requestPaymentJSON(t, adminClient, "POST", server.URL+"/api/v1/admin/products/"+id+"/block", "http-block-command", in, &item); r.StatusCode != 200 {
		t.Fatal("block", r.StatusCode)
	}
	if r := requestPaymentJSON(t, sellerClient, "POST", endpoint+"/"+id+"/submit", "blocked-submit-command", marketplace.ListingMutation{ExpectedVersion: item.Version, RightsConfirmed: true}, nil); r.StatusCode != 409 {
		t.Fatal("block bypass", r.StatusCode)
	}
	in.ExpectedVersion = item.Version
	in.Reason = "Rights issue resolved; allow an edited resubmission."
	if r := requestPaymentJSON(t, adminClient, "POST", server.URL+"/api/v1/admin/products/"+id+"/reopen", "http-reopen-command", in, &item); r.StatusCode != 200 || item.Status != "paused" || item.ReviewStatus != "draft" {
		t.Fatal("reopen", r.StatusCode, item)
	}
	if r := requestJSON(t, guest, "GET", server.URL+"/api/v1/products/"+id, nil, nil); r.StatusCode != 404 {
		t.Fatal("reopen silently published", r.StatusCode)
	}
}
