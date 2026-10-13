package assets_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
)

func TestTaskDeliveryAssetEligibilityBeforePagination(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := t.Context()
	owner, other := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{owner, other} {
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name) VALUES($1,$2,$3,'Delivery creator')`, id, id.String()+"@test.local", "task_"+id.String()[:8]); err != nil {
			t.Fatal(err)
		}
	}
	good := make(map[uuid.UUID]bool)
	var goodID, pendingID, productAsset uuid.UUID
	for i := 0; i < 70; i++ {
		id := uuid.New()
		scan, source, license, user := "clean", "upload", "creator-owned", owner
		switch {
		case i < 25:
			good[id] = true
			goodID = id
		case i < 45:
			scan = "pending"
			pendingID = id
		case i < 55:
			license = "task-contract"
			source = "delivery"
		case i < 65:
			source = "purchase"
		default:
			user = other
		}
		if i == 25 {
			scan = "clean"
			productAsset = id
		}
		if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key) VALUES($1::uuid,$2,'image',$3,'/fixture','image/png',$4,$5,$6,CASE WHEN $5='upload' THEN 'local_file' END,CASE WHEN $5='upload' THEN $1::uuid::text||'.png' END)`, id, user, fmt.Sprintf("Delivery image %02d", i), scan, source, license); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status)
 VALUES($1,$2,$3,'Protected original','Private product original','asset',1200,'USD','hcai-commercial-standard-v1','active')`, uuid.New(), owner, productAsset); err != nil {
		t.Fatal(err)
	}
	service := assets.NewService(pool, t.TempDir())
	filter := assets.ListInput{Purpose: "task_delivery", Limit: 20}
	first, err := service.List(ctx, owner, filter)
	if err != nil || len(first.Items) != 20 || first.Total != 25 || first.NextCursor == nil {
		t.Fatal(first, err)
	}
	filter.Cursor = *first.NextCursor
	second, err := service.List(ctx, owner, filter)
	if err != nil || len(second.Items) != 5 || second.Total != 25 || second.NextCursor != nil {
		t.Fatal(second, err)
	}
	for _, item := range append(first.Items, second.Items...) {
		if !good[item.ID] {
			t.Fatal("ineligible or duplicate", item)
		}
		delete(good, item.ID)
	}
	if len(good) != 0 {
		t.Fatal("lost eligible items", len(good))
	}
	filter.Purpose = ""
	if _, err = service.List(ctx, owner, filter); !errors.Is(err, assets.ErrInvalidList) {
		t.Fatal("cursor crossed eligibility scope", err)
	}
	for _, tc := range []struct {
		id      uuid.UUID
		allowed bool
	}{{goodID, true}, {pendingID, false}, {productAsset, false}} {
		item, e := service.GetOwned(ctx, owner, tc.id)
		if e != nil || item.TaskDeliveryEligible == nil || *item.TaskDeliveryEligible != tc.allowed {
			t.Fatal(item, e)
		}
	}
	if _, err = service.GetOwned(ctx, other, goodID); !errors.Is(err, assets.ErrForbidden) {
		t.Fatal("foreign pinned asset exposed", err)
	}
	search, err := service.List(ctx, owner, assets.ListInput{Purpose: "task_delivery", Query: "image 00"})
	if err != nil || search.Total != 1 {
		t.Fatal(search, err)
	}
}
