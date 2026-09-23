package httpapi_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestProductBundleBuyerContentHTTP(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := context.Background()
	root := t.TempDir()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: root, WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	sellerClient, buyerClient, guest := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	seller := registerGovernanceUser(t, sellerClient, server.URL, "bundle_http_seller")
	buyer := registerGovernanceUser(t, buyerClient, server.URL, "bundle_http_buyer")
	files := []marketplace.ProductFile{{AssetID: uuid.New(), Name: "first.txt"}, {AssetID: uuid.New(), Name: "说明.txt"}}
	store := media.NewLocalStore(root)
	stores := media.NewCatalog(store)
	for _, f := range files {
		if err := store.Put(ctx, f.AssetID.String()+".txt", []byte("Accepted bytes for "+f.Name), "text/plain"); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 VALUES($1,$2,'document','Private source',$3,'text/plain','clean','upload','hcai-commercial-standard-v1','local_file',$1::uuid::text||'.txt')`, f.AssetID, seller.ID, "/api/v1/assets/"+f.AssetID.String()+"/content"); err != nil {
			t.Fatal(err)
		}
	}
	draft := marketplace.ProductDraft{Title: "Complete licensed package", Description: "Two independent private originals.", ProductType: "asset", Category: "market_asset", AssetID: files[0].AssetID, PriceCents: 900, Currency: "USD", LicenseCode: "hcai-commercial-standard-v1", AIDisclosure: "Original seller-owned text.", IncludedFiles: []string{files[0].Name, files[1].Name}, Files: files}
	listing, err := marketplace.NewService(pool).MutateListing(ctx, seller.ID, uuid.Nil, "create", "buyer-http-package", "test", marketplace.ListingMutation{Draft: &draft})
	if err != nil {
		t.Fatal(err)
	}
	// Seed an accepted delivery to isolate HTTP authorization from the still
	// separate publication/checkout journey. No real or simulated payment is sent.
	order, purchased := uuid.New(), uuid.New()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,license_accepted_at,idempotency_key,license_version,license_terms_snapshot,product_title_snapshot,license_name_snapshot,refund_window_days_snapshot,delivery_snapshot_required)
 VALUES($1,$2,$3,900,'USD','payment_pending',now(),$1::uuid::text,'1.0','Accepted terms','Complete licensed package','Accepted license',7,true)`, order, buyer.ID, listing.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO product_order_contracts(order_id,source_asset_id,root_asset_id,offer_version,contract)
 SELECT $1,source_asset_id,root_asset_id,offer_version,contract FROM product_offers WHERE product_id=$2`, order, listing.ID); err != nil {
		t.Fatal(err)
	}
	if err = productdelivery.ReserveTx(ctx, tx, stores, order); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = productdelivery.Ensure(ctx, pool, stores, order); err != nil {
		t.Fatal(err)
	}
	snapshot, err := productdelivery.Load(ctx, pool, order)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,source_id,license_code,size_bytes)
 VALUES($1,$2,'document','Complete licensed package',$3,'application/zip','clean','purchase',$4,'hcai-commercial-standard-v1',$5)`, purchased, buyer.ID, "/api/v1/assets/"+purchased.String()+"/content", order, snapshot.Size); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO entitlements(user_id,product_id,order_id,asset_id,license_code,status)
 VALUES($1,$2,$3,$4,'hcai-commercial-standard-v1','active')`, buyer.ID, listing.ID, order, purchased); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE orders SET status='fulfilled' WHERE id=$1`, order); err != nil {
		t.Fatal(err)
	}
	base := server.URL + "/api/v1/assets/" + purchased.String() + "/content"
	read := func(client *http.Client, url, requested string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			t.Fatal(err)
		}
		if requested != "" {
			req.Header.Set("Range", requested)
		}
		r, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return r, body
	}
	for _, client := range []*http.Client{guest, sellerClient} {
		for _, url := range []string{base, base + "?fileIndex=0"} {
			r, body := read(client, url, "")
			if r.StatusCode != 403 || bytes.Contains(body, []byte("Accepted bytes")) {
				t.Fatal("foreign package download", r.StatusCode, string(body))
			}
		}
	}
	for _, q := range []string{"fileIndex=-1", "fileIndex=2", "fileIndex=20", "fileIndex=00", "fileIndex=+1", "fileIndex=1.0", "fileIndex=", "fileIndex=0&fileIndex=1", "fileIndex=0&key=original.txt", "fileIndex=1;key=original.txt", "fileIndex=%ZZ"} {
		r, _ := read(buyerClient, base+"?"+q, "")
		if r.StatusCode != 422 {
			t.Fatal("invalid index", q, r.StatusCode)
		}
	}
	if r, _ := read(sellerClient, server.URL+"/api/v1/assets/"+files[0].AssetID.String()+"/content?fileIndex=0", ""); r.StatusCode != 422 {
		t.Fatal("original treated as bundle", r.StatusCode)
	}
	r, body := read(buyerClient, base+"?fileIndex=1", "")
	disposition, params, err := mime.ParseMediaType(r.Header.Get("Content-Disposition"))
	if err != nil || r.StatusCode != 200 || string(body) != "Accepted bytes for 说明.txt" || r.Header.Get("Content-Type") != "text/plain" || r.Header.Get("Cache-Control") != "private, no-store" || disposition != "attachment" || params["filename"] != "说明.txt" || r.ContentLength != int64(len(body)) {
		t.Fatal("member response", r.StatusCode, r.Header, string(body), err)
	}
	r, body = read(buyerClient, base+"?fileIndex=1", "bytes=1-7")
	if r.StatusCode != 206 || string(body) != "ccepted" || r.Header.Get("Content-Range") != "bytes 1-7/"+strconv.FormatInt(snapshot.Manifest.Files[1].SizeBytes, 10) {
		t.Fatal("member range", r.StatusCode, r.Header, string(body))
	}
	for _, requested := range []string{"bytes=999-1000", "bytes=0-1,3-4"} {
		if r, _ := read(buyerClient, base+"?fileIndex=1", requested); r.StatusCode != 416 {
			t.Fatal("invalid range", r.StatusCode)
		}
	}
	r, body = read(buyerClient, base, "")
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "application/zip" || !strings.Contains(r.Header.Get("Content-Disposition"), ".zip") || len(body) != int(snapshot.Size) {
		t.Fatal("whole package", r.StatusCode, r.Header)
	}
	accepted := append([]byte(nil), body...)
	shortServer := shortMediaDeadlineServer(t, server.Config.Handler)
	shortBase := shortServer.URL + strings.TrimPrefix(base, server.URL)
	for _, test := range []struct {
		query, requested string
		status           int
		body             []byte
	}{
		{"", "", http.StatusOK, accepted},
		{"?fileIndex=1", "", http.StatusOK, []byte("Accepted bytes for 说明.txt")},
		{"?fileIndex=1", "bytes=1-7", http.StatusPartialContent, []byte("ccepted")},
	} {
		r, body := read(buyerClient, shortBase+test.query, test.requested)
		if r.StatusCode != test.status || !bytes.Equal(body, test.body) || r.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatal("media response lost its independent transfer budget", r.StatusCode, len(body))
		}
	}
	if response, err := buyerClient.Get(shortServer.URL + "/api/v1/assets/" + purchased.String()); err == nil {
		response.Body.Close()
		t.Fatal("ordinary JSON inherited the extended media deadline")
	}
	var projection assets.Asset
	if r := requestJSON(t, buyerClient, "GET", server.URL+"/api/v1/assets/"+purchased.String(), nil, &projection); r.StatusCode != 200 || projection.Provenance.Purchase.Delivery == nil || projection.Provenance.Purchase.CanReuse {
		t.Fatal("buyer manifest", r.StatusCode, projection)
	}
	// A small first-member range must not bypass corruption in another part.
	body[len(body)-1] ^= 1
	if err = os.WriteFile(filepath.Join(root, snapshot.Key), body, 0600); err != nil {
		t.Fatal(err)
	}
	if r, body := read(buyerClient, base+"?fileIndex=0", "bytes=0-1"); r.StatusCode != 500 || bytes.Contains(body, []byte("Accepted bytes")) {
		t.Fatal("corrupt package returned partial member", r.StatusCode, string(body))
	}
	if err = os.WriteFile(filepath.Join(root, snapshot.Key), accepted, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, files[1].AssetID); err != nil {
		t.Fatal(err)
	}
	for _, url := range []string{base, base + "?fileIndex=0", base + "?fileIndex=1"} {
		if r, _ := read(buyerClient, url, ""); r.StatusCode != 403 {
			t.Fatal("secondary scan bypassed", r.StatusCode)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE assets SET scan_status='clean' WHERE id=$1`, files[1].AssetID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE entitlements SET status='revoked',revoked_at=now() WHERE order_id=$1`, order); err != nil {
		t.Fatal(err)
	}
	if r, _ := read(buyerClient, base+"?fileIndex=1", ""); r.StatusCode != 403 {
		t.Fatal("revoked member", r.StatusCode)
	}
}
