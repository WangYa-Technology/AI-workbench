package payments

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
)

func TestProductFileDraftCheckoutBlockedAndOwnerExport(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	buyer, seller, first, _ := newProductCheckoutFixture(t, pool)
	second := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key) VALUES($1,$2,'document','Second private file',$3,'text/plain','clean','upload','hcai-commercial-standard-v1','local_file','private-bundle-export-key.txt')`, second, seller, "/api/v1/assets/"+second.String()+"/content"); err != nil {
		t.Fatal(err)
	}
	draft := marketplace.ProductDraft{Title: "Exportable bundle draft", Description: "Two real independent files", ProductType: "asset", Category: "market_asset", AssetID: first, PriceCents: 1900, Currency: "USD", LicenseCode: "hcai-commercial-standard-v1", AIDisclosure: "Author-owned originals.", IncludedFiles: []string{"image.jpg", "notes.txt"}, Files: []marketplace.ProductFile{{AssetID: first, Name: "image.jpg"}, {AssetID: second, Name: "notes.txt"}}}
	listing, err := marketplace.NewService(pool).MutateListing(ctx, seller, uuid.Nil, "create", uuid.NewString(), "file-export", marketplace.ListingMutation{Draft: &draft})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &guardedProductRuntime{}
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(runtime))
	_, _, err = service.BeginProductCheckout(ctx, buyer, listing.ID, "bundle-must-not-checkout", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, listing.ID))
	if !errors.Is(err, ErrInvalidCheckout) {
		t.Fatal("unfinished bundle checkout", err)
	}
	var orders, payments int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM orders WHERE product_id=$1),(SELECT count(*) FROM payment_intents WHERE resource_id=$1)`, listing.ID).Scan(&orders, &payments); err != nil || orders != 0 || payments != 0 || runtime.calls.Load() != 0 {
		t.Fatal("bundle dispatched partial purchase", orders, payments, runtime.calls.Load(), err)
	}
	pkg, body := runProductExport(t, pool, seller)
	var product map[string]any
	for _, row := range pkg.Data.Marketplace.Data["products"] {
		if row["id"] == listing.ID.String() {
			product = row
		}
	}
	if product == nil {
		t.Fatal("missing seller bundle")
	}
	for _, row := range []map[string]any{product, pkg.Data.Marketplace.Data["listingHistory"][0]} {
		files, ok := row["files"].([]any)
		if !ok || len(files) != 2 {
			t.Fatal("missing real files", row)
		}
		for i, f := range files {
			file := f.(map[string]any)
			if len(file) != 2 || file["assetId"] != draft.Files[i].AssetID.String() || file["name"] != draft.Files[i].Name {
				t.Fatal("file evidence mismatch", file)
			}
		}
	}
	for _, forbidden := range []string{"private-bundle-export-key", "storageKey", "storageBackend"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatal("private source locator in export", forbidden)
		}
	}
	foreign, _ := runProductExport(t, pool, buyer)
	if len(foreign.Data.Marketplace.Data["products"]) != 0 || len(foreign.Data.Marketplace.Data["listingHistory"]) != 0 {
		t.Fatal("foreign draft exported")
	}
}
