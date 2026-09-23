package marketplace_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
)

func TestProductCatalogStablePagesFiltersAndCounts(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	seller, buyer, asset, original := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedMarketplaceProduct(t, pool, seller, buyer, asset, original)
	if _, err := pool.Exec(ctx, `UPDATE products SET status='removed' WHERE id=$1`, original); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO products(id,seller_id,asset_id,title,description,product_type,category,
		price_cents,currency,license_code,status,ai_disclosure,included_files,compatibility,created_at)
		SELECT ('10000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,$1,$2,'Catalog '||i,
		'Catalogue regression',CASE WHEN i%2=0 THEN 'asset' ELSE 'workflow' END,
		CASE WHEN i%2=0 THEN 'market_asset' ELSE 'market_workflow' END,(i%3+1)*100,'USD',
		'hcai-commercial-standard-v1','active','AI assisted','[]','HCAI',
		'2026-01-01T00:00:00Z'::timestamptz FROM generate_series(1,73) i`, seller, asset); err != nil {
		t.Fatal(err)
	}
	service := marketplace.NewService(pool)
	first, err := service.ListProducts(ctx, buyer, marketplace.ListFilter{})
	if err != nil || len(first.Items) != 50 || first.Total != 73 || first.NextCursor == nil || first.CategoryCounts["market_asset"] != 36 || first.CategoryCounts["market_workflow"] != 37 {
		t.Fatalf("default page does not cover whole catalogue: %+v %v", first, err)
	}
	for _, order := range []string{"newest", "price_asc", "price_desc"} {
		t.Run(order, func(t *testing.T) {
			f := marketplace.ListFilter{Sort: order, Limit: 11}
			var all []marketplace.Product
			seen := map[uuid.UUID]bool{}
			for pageNumber := 0; ; pageNumber++ {
				if pageNumber > 10 {
					t.Fatal("pagination did not terminate")
				}
				page, err := service.ListProducts(ctx, buyer, f)
				if err != nil || page.Total != 73 || len(page.Items) == 0 || len(page.Items) > f.Limit {
					t.Fatalf("invalid page: %+v %v", page, err)
				}
				for _, item := range page.Items {
					if seen[item.ID] {
						t.Fatalf("duplicate product %s", item.ID)
					}
					seen[item.ID] = true
					all = append(all, item)
				}
				if page.NextCursor == nil {
					break
				}
				f.Cursor = *page.NextCursor
			}
			if len(all) != 73 {
				t.Fatalf("lost products across pages: %d", len(all))
			}
			for i := 1; i < len(all); i++ {
				a, b := all[i-1], all[i]
				if order == "price_asc" && a.PriceCents > b.PriceCents || order == "price_desc" && a.PriceCents < b.PriceCents {
					t.Fatalf("incorrect price order: %d then %d", a.PriceCents, b.PriceCents)
				}
				if (order == "newest" || a.PriceCents == b.PriceCents) && a.ID.String() <= b.ID.String() {
					t.Fatalf("unstable equal-time tie: %s then %s", a.ID, b.ID)
				}
			}
		})
	}

	filtered, err := service.ListProducts(ctx, buyer, marketplace.ListFilter{Category: "market_asset", Limit: 7})
	if err != nil || filtered.Total != 36 || filtered.CategoryCounts["market_workflow"] != 37 || len(filtered.Items) != 7 {
		t.Fatalf("selected category changed facet counts: %+v %v", filtered, err)
	}
	typed, err := service.ListProducts(ctx, buyer, marketplace.ListFilter{ProductType: "workflow", Limit: 7})
	if err != nil || typed.Total != 37 || typed.CategoryCounts["market_asset"] != 0 {
		t.Fatalf("type did not filter facets: %+v %v", typed, err)
	}
	for _, filter := range []marketplace.ListFilter{
		{Query: "different"}, {Category: "market_asset"}, {ProductType: "workflow"},
		{LicenseCode: "other-license"}, {Sort: "price_asc"},
	} {
		filter.Cursor = *first.NextCursor
		if _, err := service.ListProducts(ctx, buyer, filter); !errors.Is(err, marketplace.ErrInvalidProductFilter) {
			t.Fatalf("cross-filter cursor accepted: %+v %v", filter, err)
		}
	}
	if _, err := service.ListProducts(ctx, uuid.Nil, marketplace.ListFilter{Cursor: *first.NextCursor}); !errors.Is(err, marketplace.ErrInvalidProductFilter) {
		t.Fatalf("cross-viewer cursor accepted: %v", err)
	}
	// Continuation uses the captured sort values, even if its boundary row disappears.
	if _, err := pool.Exec(ctx, `UPDATE products SET status='removed' WHERE id=$1`, first.Items[49].ID); err != nil {
		t.Fatal(err)
	}
	rest, err := service.ListProducts(ctx, buyer, marketplace.ListFilter{Cursor: *first.NextCursor})
	if err != nil || len(rest.Items) != 23 || rest.Total != 72 || rest.NextCursor != nil {
		t.Fatalf("removed boundary broke continuation: %+v %v", rest, err)
	}
	for _, change := range []struct{ hide, restore string }{
		{`UPDATE users SET status='suspended' WHERE id=$1`, `UPDATE users SET status='active' WHERE id=$1`},
		{`UPDATE assets SET scan_status='rejected' WHERE owner_id=$1`, `UPDATE assets SET scan_status='clean' WHERE owner_id=$1`},
		{`UPDATE licenses SET status='retired' WHERE code='hcai-commercial-standard-v1' AND $1::uuid IS NOT NULL`, `UPDATE licenses SET status='active' WHERE code='hcai-commercial-standard-v1' AND $1::uuid IS NOT NULL`},
	} {
		if _, err := pool.Exec(ctx, change.hide, seller); err != nil {
			t.Fatal(err)
		}
		page, err := service.ListProducts(ctx, buyer, marketplace.ListFilter{})
		if err != nil || page.Total != 0 || len(page.Items) != 0 || len(page.CategoryCounts) != 0 || page.NextCursor != nil {
			t.Fatalf("ineligible catalogue remained visible after %s: %+v %v", change.hide, page, err)
		}
		if _, err := pool.Exec(ctx, change.restore, seller); err != nil {
			t.Fatal(err)
		}
		page, err = service.ListProducts(ctx, buyer, marketplace.ListFilter{})
		if err != nil || page.Total != 72 || len(page.Items) != 50 || page.NextCursor == nil {
			t.Fatalf("restored catalogue remained hidden: %+v %v", page, err)
		}
	}
}

func TestProductCatalogLiteralSearchAndValidation(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	seller, buyer, asset, product := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedMarketplaceProduct(t, pool, seller, buyer, asset, product)
	service := marketplace.NewService(pool)
	for _, query := range []string{"%", "_", `\`} {
		page, err := service.ListProducts(ctx, buyer, marketplace.ListFilter{Query: query})
		if err != nil || page.Total != 0 || len(page.Items) != 0 || len(page.CategoryCounts) != 0 {
			t.Fatalf("literal %q expanded into a wildcard: %+v %v", query, page, err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE products SET title=$2 WHERE id=$1`, product, `100%_Study\source`); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"%", "_", `\`, `  100%_STUDY\SOURCE  `, strings.Repeat("界", 120)} {
		page, err := service.ListProducts(ctx, buyer, marketplace.ListFilter{Query: query})
		want := 1
		if strings.HasPrefix(query, "界") {
			want = 0
		}
		if err != nil || len(page.Items) != want || page.Total != want {
			t.Fatalf("literal search %q: %+v %v", query, page, err)
		}
	}
	for i, filter := range []marketplace.ListFilter{
		{Query: strings.Repeat("界", 121)}, {Query: string([]byte{0xff})}, {Query: "\x00"},
		{Limit: -1}, {Limit: 101}, {Sort: "recent"}, {ProductType: "video"},
		{Category: "bad category"}, {LicenseCode: strings.Repeat("a", 101)},
		{Cursor: "%%%"}, {Cursor: "e30"}, {Cursor: strings.Repeat("a", 1025)},
	} {
		t.Run(fmt.Sprintf("invalid_%d", i), func(t *testing.T) {
			if _, err := service.ListProducts(ctx, buyer, filter); !errors.Is(err, marketplace.ErrInvalidProductFilter) {
				t.Fatalf("invalid filter accepted: %+v %v", filter, err)
			}
		})
	}
}
