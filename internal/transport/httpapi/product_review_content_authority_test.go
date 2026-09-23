package httpapi_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestProductReviewContentRechecksAuthorityDuringStorage(t *testing.T) {
	for _, phase := range []string{"head", "get"} {
		for _, change := range []string{"role", "suspended", "permission", "listing_version", "seller_suspended", "scan_rejected"} {
			t.Run(phase+"/"+change, func(t *testing.T) {
				pool, cleanup := httpTestPool(t)
				t.Cleanup(cleanup)
				ctx := t.Context()
				var actor, product, sellerID, asset uuid.UUID
				var enabled, changed atomic.Bool
				var gets atomic.Int32
				body := "Private accepted seller original"
				storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") == "" {
						t.Error("unsigned storage request")
						w.WriteHeader(403)
						return
					}
					if r.Method == http.MethodHead && r.URL.Path == "/review-media" {
						return
					}
					if r.URL.Path != "/review-media/original.txt" {
						t.Error("unexpected storage key", r.URL.Path)
						w.WriteHeader(404)
						return
					}
					if enabled.Load() && strings.EqualFold(r.Method, phase) && changed.CompareAndSwap(false, true) {
						var err error
						switch change {
						case "role":
							_, err = pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor)
						case "suspended":
							_, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, actor)
						case "permission":
							_, err = pool.Exec(ctx, `DELETE FROM role_permissions WHERE role='admin' AND permission_id='admin:content'`)
						case "listing_version":
							_, err = pool.Exec(ctx, `UPDATE products SET title='Changed reviewed version' WHERE id=$1`, product)
						case "seller_suspended":
							_, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, sellerID)
						case "scan_rejected":
							_, err = pool.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, asset)
						}
						if err != nil {
							t.Error(err)
							w.WriteHeader(500)
							return
						}
					}
					w.Header().Set("ETag", `"original-v1"`)
					w.Header().Set("Content-Length", fmt.Sprint(len(body)))
					if r.Method == http.MethodHead {
						return
					}
					if r.Method != http.MethodGet {
						t.Error("unexpected storage write", r.Method)
						w.WriteHeader(405)
						return
					}
					gets.Add(1)
					if r.Header.Get("Range") == "bytes=0-6" {
						w.Header().Set("Content-Length", "7")
						w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-6/%d", len(body)))
						w.WriteHeader(206)
						_, _ = io.WriteString(w, body[:7])
						return
					}
					_, _ = io.WriteString(w, body)
				}))
				t.Cleanup(storage.Close)
				server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
					MediaStorageAdapter: "s3", MediaS3Bucket: "review-media", MediaS3Region: "us-east-1", MediaS3Endpoint: storage.URL, MediaS3AccessKeyID: "review-fixture", MediaS3SecretAccessKey: "review-fixture-secret", MediaS3PathStyle: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
				t.Cleanup(server.Close)
				sellerClient, reviewClient := testHTTPClient(t), testHTTPClient(t)
				seller := registerGovernanceUser(t, sellerClient, server.URL, "review_content_seller")
				sellerID = seller.ID
				reviewer := registerGovernanceUser(t, reviewClient, server.URL, "review_content_actor")
				actor = reviewer.ID
				if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, actor); err != nil {
					t.Fatal(err)
				}
				asset = uuid.New()
				if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key) VALUES($1,$2,'document','Private review source','/private','text/plain','clean','upload','hcai-commercial-standard-v1','s3','original.txt')`, asset, seller.ID); err != nil {
					t.Fatal(err)
				}
				draft := marketplace.ProductDraft{Title: "Review authorization fixture", Description: "One independent private source.", ProductType: "asset", Category: "market_asset", AssetID: asset, PriceCents: 1900, Currency: "USD", LicenseCode: "hcai-commercial-standard-v1", AIDisclosure: "Seller owned original text.", IncludedFiles: []string{"original.txt"}}
				item, err := marketplace.NewService(pool).MutateListing(ctx, seller.ID, uuid.Nil, "create", "review-content-create", "fixture", marketplace.ListingMutation{Draft: &draft})
				if err != nil {
					t.Fatal(err)
				}
				product = item.ID
				enabled.Store(true)
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/admin/products/"+product.String()+"/content?kind=source&version="+item.Version, nil)
				if err != nil {
					t.Fatal(err)
				}
				if phase == "get" {
					req.Header.Set("Range", "bytes=0-6")
				}
				resp, err := reviewClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if !changed.Load() {
					t.Fatal("storage mutation not reached")
				}
				if resp.StatusCode != 403 || strings.Contains(string(raw), "Private") || resp.Header.Get("Content-Range") != "" || resp.Header.Get("ETag") != "" {
					t.Errorf("stale review delivered bytes/metadata: %d %s %v", resp.StatusCode, raw, resp.Header)
				}
				if phase == "head" && gets.Load() != 0 {
					t.Error("opened object after failed stat authorization", gets.Load())
				}
				var audits int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE resource_id=$1 AND action='marketplace.listing_file_reviewed'`, product).Scan(&audits); err != nil || audits != 1 {
					t.Fatal("review grant audit lost or duplicated", audits, err)
				}
			})
		}
	}
}
