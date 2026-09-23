package assets_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
)

func TestProductPreviewCandidatesRespectDeliveryEvidenceAndPagination(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := context.Background()
	seller, other := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{seller, other} {
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name) VALUES($1,$2,$3,'Preview owner')`, id, id.String()+"@test.local", "preview_"+id.String()[:8]); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	service := assets.NewService(pool, root)
	market := marketplace.NewService(pool)
	addAsset := func(owner uuid.UUID, title, key string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if key == "" {
			key = id.String() + ".txt"
		}
		if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,source_type,scan_status,storage_backend,storage_key,license_code)
		 VALUES($1,$2,'document',$3,$4,'text/plain','upload','clean','local_file',$5,'hcai-commercial-standard-v1')`, id, owner, title, "/api/v1/assets/"+id.String()+"/content", key); err != nil {
			t.Fatal(err)
		}
		return id
	}
	addProduct := func(source uuid.UUID) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status)
		 VALUES($1,$2,$3,'Protected delivery','Private deliverable','asset',1200,'USD','hcai-commercial-standard-v1','active')`, id, seller, source); err != nil {
			t.Fatal(err)
		}
		return id
	}
	sample1 := addAsset(seller, "Eligible first sample", "")
	sample2 := addAsset(seller, "Eligible second sample", "")
	original := addAsset(seller, "Current original", "original-delivery.txt")
	product := addProduct(original)
	foreign := addAsset(other, "Not owned", "")
	unsafe := addAsset(seller, "Awaiting scan", "")
	derived := addAsset(seller, "Has origin", "")
	if _, err := pool.Exec(ctx, `UPDATE assets SET scan_status='pending' WHERE id=$1`, unsafe); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE assets SET origin_asset_id=$2 WHERE id=$1`, derived, original); err != nil {
		t.Fatal(err)
	}
	// Ineligible originals are newer and outnumber a page; filtering must happen
	// before pagination and counts rather than hide an already-limited page.
	for i := 0; i < 52; i++ {
		addProduct(addAsset(seller, "Other product original", ""))
	}
	first, err := service.List(ctx, seller, assets.ListInput{Purpose: "product_preview", Limit: 1})
	if err != nil || first.Total != 2 || len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("candidate first page: %#v %v", first, err)
	}
	last, err := service.List(ctx, seller, assets.ListInput{Purpose: "product_preview", Limit: 1, Cursor: *first.NextCursor})
	if err != nil || last.Total != 2 || len(last.Items) != 1 || last.NextCursor != nil {
		t.Fatalf("candidate last page: %#v %v", last, err)
	}
	seen := map[uuid.UUID]bool{first.Items[0].ID: true, last.Items[0].ID: true}
	if len(seen) != 2 || !seen[sample1] || !seen[sample2] {
		t.Fatal("invalid candidate contents", seen)
	}
	for _, input := range []assets.ListInput{{Cursor: *first.NextCursor}, {Purpose: "product_preview", Source: "purchase"}, {Purpose: "unknown"}, {Purpose: "product_preview", Cursor: strings.Repeat("x", 1025)}} {
		if _, err := service.List(ctx, seller, input); !errors.Is(err, assets.ErrInvalidList) {
			t.Fatal("invalid scope accepted", err)
		}
	}
	if _, err := service.List(ctx, other, assets.ListInput{Purpose: "product_preview", Cursor: *first.NextCursor}); !errors.Is(err, assets.ErrInvalidList) {
		t.Fatal("cross-owner cursor accepted", err)
	}
	if _, err := service.Content(ctx, uuid.Nil, sample1); !errors.Is(err, assets.ErrForbidden) {
		t.Fatal("listing published a candidate", err)
	}
	offer, err := market.GetProduct(ctx, seller, product)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{original, foreign, unsafe, derived} {
		if _, err := market.SetPreview(ctx, seller, product, marketplace.PreviewUpdate{PreviewAssetID: &id, OfferVersion: offer.OfferVersion}, "bad-preview"); !errors.Is(err, marketplace.ErrInvalidPreview) {
			t.Fatal("invalid sample saved", id, err)
		}
	}
	if _, err := market.SetPreview(ctx, seller, product, marketplace.PreviewUpdate{PreviewAssetID: &sample1, OfferVersion: offer.OfferVersion}, "valid-preview"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, sample1.String()+".txt"), []byte("Public excerpt"), 0600); err != nil {
		t.Fatal(err)
	}
	content, err := service.Content(ctx, uuid.Nil, sample1)
	if err != nil {
		t.Fatal(err)
	}
	object, err := content.Open(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(object.Body)
	_ = object.Body.Close()
	if err != nil || string(data) != "Public excerpt" {
		t.Fatal("wrong sample bytes", err)
	}
	// Ownership-safe selectors also reflect eligibility changes after an earlier
	// selection: making the sample a paid original removes its public grant.
	addProduct(sample1)
	if _, err := service.Content(ctx, uuid.Nil, sample1); !errors.Is(err, assets.ErrForbidden) {
		t.Fatal("new delivery retained public preview access", err)
	}
	page, err := service.List(ctx, seller, assets.ListInput{Purpose: "product_preview"})
	if err != nil || page.Total != 1 || page.Items[0].ID != sample2 {
		t.Fatal("new delivery remains selectable", page, err)
	}
	// A historical contract protects the accepted location, including a new
	// asset ID registered there after the original source changes location.
	order := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,license_accepted_at,idempotency_key,license_version,license_terms_snapshot,product_title_snapshot,license_name_snapshot,refund_window_days_snapshot)
	 VALUES($1,$2,$3,1200,'USD','fulfilled',now(),$1::uuid::text,'1.0','Accepted terms','Protected delivery','Commercial',7)`, order, other, product); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO product_order_contracts(order_id,source_asset_id,root_asset_id,offer_version,contract) SELECT $1,source_asset_id,root_asset_id,offer_version,contract FROM product_offers WHERE product_id=$2`, order, product); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE assets SET storage_key='relocated-delivery.txt' WHERE id=$1`, original); err != nil {
		t.Fatal(err)
	}
	alias := addAsset(seller, "Historical storage alias", "original-delivery.txt")
	offer, err = market.GetProduct(ctx, seller, product)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := market.SetPreview(ctx, seller, product, marketplace.PreviewUpdate{PreviewAssetID: &alias, OfferVersion: offer.OfferVersion}, "historical-alias"); !errors.Is(err, marketplace.ErrInvalidPreview) {
		t.Fatal("historical delivery was selectable", err)
	}
	page, err = service.List(ctx, seller, assets.ListInput{Purpose: "product_preview"})
	if err != nil || page.Total != 1 || page.Items[0].ID != sample2 {
		t.Fatal("historical alias leaked into selector", page, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE products SET preview_asset_id=$2 WHERE id=$1`, product, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO works(author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at) VALUES($1,$2,'Historical alias work','Legacy public alias','Imported','published','AI assisted',now())`, seller, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE system_settings SET site_configuration=jsonb_set(site_configuration,'{siteIconUrl}',to_jsonb($1::text)) WHERE singleton=true`, "/api/v1/assets/"+alias.String()+"/content"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Content(ctx, uuid.Nil, alias); !errors.Is(err, assets.ErrForbidden) {
		t.Fatal("alias bypassed media protection", err)
	}
	var public int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM public_product_previews WHERE asset_id=$1)+(SELECT count(*) FROM public_works WHERE asset_id=$1)`, alias).Scan(&public); err != nil || public != 0 {
		t.Fatal("historical alias publicly projected", public, err)
	}
	// The existing object uniqueness constraint protects exact live aliases.
	if _, err := pool.Exec(ctx, `UPDATE assets SET storage_key='original-delivery.txt' WHERE id=$1`, sample2); err == nil {
		t.Fatal("duplicate live storage object accepted")
	}
	validProduct := addProduct(original)
	if _, err := pool.Exec(ctx, `UPDATE products SET preview_asset_id=$2 WHERE id=$1`, validProduct, sample2); err != nil {
		t.Fatal(err)
	}
	// Both directions retain the public predicate and all transaction evidence.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, direction := range []string{"down", "up"} {
		body, err := os.ReadFile("../platform/database/migrations/0089_product_preview_candidates." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			t.Fatal(direction, err)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM public_product_previews WHERE asset_id=$1`, alias).Scan(&public); err != nil || public != 0 {
			t.Fatal("rollback exposed delivery", direction, err)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM public_product_previews WHERE product_id=$1 AND asset_id=$2`, validProduct, sample2).Scan(&public); err != nil || public != 1 {
			t.Fatal("migration lost eligible preview", direction, err)
		}
		var contracts int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM product_order_contracts WHERE order_id=$1`, order).Scan(&contracts); err != nil || contracts != 1 {
			t.Fatal("migration changed contracts", direction, err)
		}
	}
}
