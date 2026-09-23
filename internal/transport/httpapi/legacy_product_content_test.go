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
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestHistoricalPurchasedContentHTTPPermissionsAndRange(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := context.Background()
	root := t.TempDir()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: root, WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	buyerClient, sellerClient, guest := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	buyer := registerGovernanceUser(t, buyerClient, server.URL, "legacy_content_buyer")
	seller := registerGovernanceUser(t, sellerClient, server.URL, "legacy_content_seller")
	original := []byte("Original licensed bytes")
	read := func(t *testing.T, client *http.Client, asset uuid.UUID, useRange bool, wantStatus int) {
		t.Helper()
		req, err := http.NewRequest("GET", server.URL+"/api/v1/assets/"+asset.String()+"/content", nil)
		if err != nil {
			t.Fatal(err)
		}
		if useRange {
			req.Header.Set("Range", "bytes=0-3")
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != wantStatus {
			t.Fatalf("range=%v status=%d want=%d body=%s", useRange, res.StatusCode, wantStatus, body)
		}
		if wantStatus == 200 || wantStatus == 206 {
			expected := string(original)
			if useRange {
				expected = expected[:4]
			}
			if string(body) != expected || res.Header.Get("Cache-Control") != "private, no-store" {
				t.Fatalf("wrong/private bytes %q headers=%v", body, res.Header)
			}
			if useRange && res.Header.Get("Content-Range") != "bytes 0-3/23" {
				t.Fatal("wrong range", res.Header)
			}
		}
	}
	for _, scenario := range []string{"refund", "external_without_contract", "suspended", "inconsistent_order"} {
		t.Run(scenario, func(t *testing.T) {
			source, product := uuid.New(), uuid.New()
			if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
    VALUES($1,$2,'image','Historical original',$3,'image/jpeg','clean','upload','hcai-commercial-standard-v1','local_file',$1::uuid::text||'.jpg')`, source, seller.ID, "/api/v1/assets/"+source.String()+"/content"); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status)
    VALUES($1,$2,$3,'Historical resource','Historical HTTP fixture','asset',1900,'USD','hcai-commercial-standard-v1','active')`, product, seller.ID, source); err != nil {
				t.Fatal(err)
			}
			if err := media.NewLocalStore(root).Put(ctx, source.String()+".jpg", original, "image/jpeg"); err != nil {
				t.Fatal(err)
			}
			f := testutil.SeedLegacyProductOrder(t, pool, buyer.ID, product)
			for _, useRange := range []bool{false, true} {
				status := 200
				if useRange {
					status = 206
				}
				read(t, buyerClient, f.AssetID, useRange, status)
				read(t, sellerClient, f.AssetID, useRange, 403)
				read(t, guest, f.AssetID, useRange, 403)
			}
			var err error
			switch scenario {
			case "refund":
				res := requestPaymentJSON(t, buyerClient, "POST", server.URL+"/api/v1/orders/"+f.OrderID.String()+"/refund", "legacy-content-refund", map[string]string{"reason": "I no longer need this historical resource."}, nil)
				if res.StatusCode != 200 {
					t.Fatal("refund failed", res.StatusCode)
				}
			case "external_without_contract":
				_, err = pool.Exec(ctx, `INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key)
     VALUES($1,'stripe','product',$2,$3,$4,$5,1900,'USD','paid',false,$1::uuid::text)`, uuid.New(), buyer.ID, seller.ID, product, f.OrderID)
			case "suspended":
				_, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, buyer.ID)
			case "inconsistent_order":
				_, err = pool.Exec(ctx, `UPDATE orders SET status='refunded' WHERE id=$1`, f.OrderID)
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, useRange := range []bool{false, true} {
				read(t, buyerClient, f.AssetID, useRange, 403)
			}
			if scenario == "suspended" {
				if _, err = pool.Exec(ctx, `UPDATE users SET status='active' WHERE id=$1`, buyer.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				var item assets.Asset
				res := requestJSON(t, buyerClient, "GET", server.URL+"/api/v1/assets/"+f.AssetID.String(), nil, &item)
				if res.StatusCode != 200 || item.Provenance == nil || item.Provenance.Purchase == nil || item.Provenance.Purchase.CanDownload || item.Provenance.Purchase.CanReuse {
					t.Fatalf("metadata capability mismatch: %d %+v", res.StatusCode, item.Provenance)
				}
			}
		})
	}
}
