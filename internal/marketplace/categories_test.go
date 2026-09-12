package marketplace_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/tasktypes"
	"testing"
)

func TestMarketplaceBusinessCategoryPreservesProduct(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	seller, buyer, asset, product := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedMarketplaceProduct(t, pool, seller, buyer, asset, product)
	directory := tasktypes.NewService(pool)
	service := marketplace.NewService(pool)
	category, err := directory.Create(ctx, tasktypes.Type{Code: "editorial", NameZh: "编辑精选", NameEn: "Editorial", Icon: "image", Scope: "marketplace", SortOrder: 1})
	if err != nil {
		t.Fatal(err)
	}
	before, err := service.ListProducts(ctx, buyer, marketplace.ListFilter{})
	if err != nil || len(before) != 1 {
		t.Fatalf("fixture %v %v", before, err)
	}
	if err = directory.Assign(ctx, "marketplace", product.String(), category.Code); err != nil {
		t.Fatal(err)
	}
	after, err := service.ListProducts(ctx, buyer, marketplace.ListFilter{Category: category.Code})
	if err != nil || len(after) != 1 {
		t.Fatalf("filter %v %v", after, err)
	}
	if after[0].ProductType != before[0].ProductType || after[0].MediaKind != before[0].MediaKind || after[0].PriceCents != before[0].PriceCents || after[0].AssetID != before[0].AssetID {
		t.Fatal("classification changed product semantics")
	}
	empty, err := service.ListProducts(ctx, buyer, marketplace.ListFilter{Category: "market_asset"})
	if err != nil || len(empty) != 0 {
		t.Fatal("category filtering ignored")
	}
	if err = directory.Delete(ctx, category.Code, "community_general", "marketplace"); err == nil {
		t.Fatal("cross scope replacement allowed")
	}
	if err = directory.Delete(ctx, category.Code, "market_asset", "marketplace"); err != nil {
		t.Fatal(err)
	}
	after, err = service.ListProducts(ctx, buyer, marketplace.ListFilter{Category: "market_asset"})
	if err != nil || len(after) != 1 || after[0].ID != product {
		t.Fatal("transfer lost product")
	}
}
