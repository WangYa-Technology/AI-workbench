package httpapi_test

import (
	"context"
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

func TestProductPreviewS3RejectsRedirectsAndWrongRanges(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := context.Background()
	var mode, targetCalls, objectCalls atomic.Int32
	original, sample, product := uuid.New(), uuid.New(), uuid.New()
	mutateDuringRead := func(phase string) error {
		var statement string
		switch mode.Load() {
		case 6:
			if phase == "get" {
				statement = `UPDATE products SET status='paused' WHERE id=$1`
			}
		case 7:
			if phase == "get" {
				statement = `UPDATE assets SET scan_status='rejected' WHERE id=(SELECT preview_asset_id FROM products WHERE id=$1)`
			}
		case 8:
			if phase == "get" {
				statement = `UPDATE users SET status='suspended' WHERE id=(SELECT seller_id FROM products WHERE id=$1)`
			}
		case 9:
			if phase == "get" {
				statement = `UPDATE assets SET storage_key='replaced-sample.txt' WHERE id=(SELECT preview_asset_id FROM products WHERE id=$1)`
			}
		case 10:
			if phase == "head" {
				statement = `UPDATE products SET status='paused' WHERE id=$1`
			}
		}
		if statement == "" {
			return nil
		}
		_, err := pool.Exec(ctx, statement, product)
		return err
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
		_, _ = io.WriteString(w, "untrusted redirected object")
	}))
	defer target.Close()
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead && r.URL.Path == "/private-media" {
			if r.Header.Get("Authorization") == "" {
				t.Error("unsigned bucket inspection")
			}
			switch mode.Load() {
			case 12, 14:
				w.WriteHeader(http.StatusNotFound)
			case 13:
				w.WriteHeader(http.StatusForbidden)
			default:
				w.WriteHeader(http.StatusOK)
			}
			return
		}
		if r.URL.Path != "/private-media/sample.txt" || r.Header.Get("Authorization") == "" {
			t.Error("unexpected object or unsigned storage request")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Method == http.MethodHead {
			if mode.Load() >= 11 && mode.Load() <= 13 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if err := mutateDuringRead("head"); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			w.Header().Set("Content-Length", "10")
			w.Header().Set("ETag", `"sample-v1"`)
			return
		}
		if r.Method != http.MethodGet || r.Header.Get("Range") != "bytes=3-6" {
			t.Error("unexpected sample read")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		objectCalls.Add(1)
		if mode.Load() == 14 || mode.Load() == 15 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := mutateDuringRead("get"); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("ETag", `"sample-v1"`)
		switch mode.Load() {
		case 1: // The store ignores Range and returns all bytes.
			w.Header().Set("Content-Length", "10")
			_, _ = io.WriteString(w, "0123456789")
		case 2: // A correctly sized body is still the wrong part of the object.
			w.Header().Set("Content-Length", "4")
			w.Header().Set("Content-Range", "bytes 0-3/10")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(w, "0123")
		case 3:
			w.Header().Set("Location", target.URL+"/other-object")
			w.WriteHeader(http.StatusTemporaryRedirect)
		case 4: // HEAD and GET disagree on the full object size.
			w.Header().Set("Content-Length", "4")
			w.Header().Set("Content-Range", "bytes 3-6/11")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(w, "3456")
		case 5: // Same size but a replacement appeared after HEAD.
			w.Header().Set("Content-Length", "4")
			w.Header().Set("Content-Range", "bytes 3-6/10")
			w.Header().Set("ETag", `"sample-v2"`)
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(w, "ABCD")
		default:
			w.Header().Set("Content-Length", "4")
			w.Header().Set("Content-Range", "bytes 3-6/10")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(w, "3456")
		}
	}))
	defer storage.Close()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
		MediaStorageAdapter: "s3", MediaS3Bucket: "private-media", MediaS3Region: "us-east-1",
		MediaS3Endpoint: storage.URL, MediaS3AccessKeyID: "sample-fixture-access", MediaS3SecretAccessKey: "sample-fixture-secret", MediaS3PathStyle: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	sellerClient, guest := testHTTPClient(t), testHTTPClient(t)
	seller := registerGovernanceUser(t, sellerClient, server.URL, "s3_sample_seller")
	for _, asset := range []struct {
		id  uuid.UUID
		key string
	}{{original, "original.txt"}, {sample, "sample.txt"}} {
		if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 VALUES($1,$2,'document','S3 sample boundary',$3,'text/plain','clean','upload','hcai-commercial-standard-v1','s3',$4)`,
			asset.id, seller.ID, "/api/v1/assets/"+asset.id.String()+"/content", asset.key); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status,ai_disclosure,included_files,compatibility)
 VALUES($1,$2,$3,'S3 sample product','Independent sample','workflow',1900,'USD','hcai-commercial-standard-v1','active','AI assisted','[]','HCAI')`, product, seller.ID, original); err != nil {
		t.Fatal(err)
	}
	var item marketplace.Product
	if response := requestJSON(t, guest, http.MethodGet, server.URL+"/api/v1/products/"+product.String(), nil, &item); response.StatusCode != http.StatusOK {
		t.Fatal("product unavailable", response.StatusCode)
	}
	if response := requestJSON(t, sellerClient, http.MethodPut, server.URL+"/api/v1/products/"+product.String()+"/preview",
		map[string]any{"previewAssetId": sample, "offerVersion": item.OfferVersion}, nil); response.StatusCode != http.StatusOK {
		t.Fatal("sample publication failed", response.StatusCode)
	}
	for _, tc := range []struct {
		name string
		mode int32
	}{{"valid_range", 0}, {"ignored_range", 1}, {"wrong_range", 2}, {"redirect", 3}, {"changed_size", 4}, {"changed_object", 5}, {"paused_during_get", 6}, {"scan_changed_during_get", 7}, {"suspended_during_get", 8}, {"locator_changed_during_get", 9}, {"paused_during_head", 10}, {"missing_object", 11}, {"missing_bucket", 12}, {"inaccessible_bucket", 13}, {"bucket_lost_during_get", 14}, {"object_lost_during_get", 15}, {"recovered_range", 0}} {
		t.Run(tc.name, func(t *testing.T) {
			mode.Store(tc.mode)
			beforeObjects := objectCalls.Load()
			if tc.mode >= 6 && tc.mode <= 10 {
				defer func() {
					for _, query := range []string{
						`UPDATE products SET status='active' WHERE id=$1`,
						`UPDATE assets SET scan_status='clean',storage_key='sample.txt' WHERE id=(SELECT preview_asset_id FROM products WHERE id=$1)`,
						`UPDATE users SET status='active' WHERE id=(SELECT seller_id FROM products WHERE id=$1)`,
					} {
						if _, err := pool.Exec(ctx, query, product); err != nil {
							t.Error(err)
						}
					}
				}()
			}
			req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/v1/assets/%s/content", server.URL, sample), nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Range", "bytes=3-6")
			response, err := guest.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if tc.mode == 0 {
				if response.StatusCode != http.StatusPartialContent || string(body) != "3456" || response.Header.Get("Content-Range") != "bytes 3-6/10" || response.Header.Get("Cache-Control") != "private, no-store" {
					t.Fatalf("valid sample range failed: status=%d body=%q", response.StatusCode, body)
				}
			} else if tc.mode >= 11 {
				want := http.StatusInternalServerError
				if tc.mode == 11 || tc.mode == 15 {
					want = http.StatusNotFound
				}
				if response.StatusCode != want || !strings.Contains(response.Header.Get("Content-Type"), "application/json") || response.Header.Get("Content-Range") != "" {
					t.Fatalf("storage outage confused with object absence: status=%d body=%q", response.StatusCode, body)
				}
				for _, private := range []string{"private-media", "sample-fixture-secret", storage.URL, "3456"} {
					if strings.Contains(string(body), private) {
						t.Fatal("storage failure exposed private content or provider details")
					}
				}
			} else if tc.mode >= 6 {
				if response.StatusCode != http.StatusForbidden || !strings.Contains(response.Header.Get("Content-Type"), "application/json") || response.Header.Get("Content-Range") != "" {
					t.Fatalf("revoked in-flight preview: status=%d body=%q", response.StatusCode, body)
				}
				wantCalls := int32(1)
				if tc.mode == 10 {
					wantCalls = 0
				}
				if objectCalls.Load()-beforeObjects != wantCalls {
					t.Fatal("unexpected object fetch after revocation", objectCalls.Load()-beforeObjects)
				}
			} else if response.StatusCode != http.StatusInternalServerError || !strings.Contains(response.Header.Get("Content-Type"), "application/json") ||
				strings.Contains(string(body), "0123") || strings.Contains(string(body), "redirected object") || response.Header.Get("Content-Range") != "" {
				t.Fatalf("invalid storage response exposed media: status=%d body=%q", response.StatusCode, body)
			}
			if targetCalls.Load() != 0 {
				t.Fatal("sample read reached redirect destination")
			}
		})
	}
}
