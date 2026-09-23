package marketplace_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/jackc/pgx/v5"
)

func TestOrderHistoryStablePaginationOwnershipAndEvents(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	sellerID, buyerID, outsiderID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES
		($1,$2,$3,'Order Seller','creator','active'),
		($4,$5,$6,'Order Buyer','member','active'),
		($7,$8,$9,'Order Outsider','member','active')`,
		sellerID, sellerID.String()+"@test.local", "order_seller_"+sellerID.String()[:8],
		buyerID, buyerID.String()+"@test.local", "order_buyer_"+buyerID.String()[:8],
		outsiderID, outsiderID.String()+"@test.local", "order_outsider_"+outsiderID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-time.Hour)
	batch := &pgx.Batch{}
	for index := 0; index < 106; index++ {
		sourceAssetID, productID, orderID, purchasedAssetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		createdAt := base.Add(-time.Duration(index) * time.Second)
		batch.Queue(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code) VALUES($1,$2,'image','Order source','/media/order-source.jpg','image/jpeg','clean','delivery','hcai-commercial-standard-v1')`, sourceAssetID, sellerID)
		batch.Queue(`INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status) VALUES($1,$2,$3,'Paginated order product','Order history evidence.','asset',1200,'USD','hcai-commercial-standard-v1','active')`, productID, sellerID, sourceAssetID)
		batch.Queue(`INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,license_accepted_at,idempotency_key,license_version,license_terms_snapshot,product_title_snapshot,license_name_snapshot,refund_window_days_snapshot,created_at,updated_at) VALUES($1,$2,$3,1200,'USD','fulfilled',$4,$5,'1.0','Commercial test terms.','Paginated order product','HCAI Commercial Standard',7,$4,$4)`, orderID, buyerID, productID, createdAt, "order-history-"+orderID.String())
		batch.Queue(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,source_id,license_code) VALUES($1,$2,'image','Purchased order Asset','/media/order-purchase.jpg','image/jpeg','clean','purchase',$3,'hcai-commercial-standard-v1')`, purchasedAssetID, buyerID, orderID)
		batch.Queue(`INSERT INTO entitlements(user_id,product_id,order_id,asset_id,license_code,status,granted_at) VALUES($1,$2,$3,$4,'hcai-commercial-standard-v1','active',$5)`, buyerID, productID, orderID, purchasedAssetID, createdAt)
		batch.Queue(`INSERT INTO order_events(order_id,actor_id,to_status,reason,created_at,sequence) VALUES($1,$2,'fulfilled','Stable event evidence.',$3,1)`, orderID, buyerID, createdAt)
	}
	results := pool.SendBatch(ctx, batch)
	if err := results.Close(); err != nil {
		t.Fatal(err)
	}

	service := marketplace.NewService(pool)
	seen := make(map[uuid.UUID]struct{})
	cursor := ""
	for {
		page, err := service.ListOrders(ctx, buyerID, marketplace.OrderListInput{Cursor: cursor, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if _, duplicate := seen[item.ID]; duplicate {
				t.Fatalf("duplicate order %s", item.ID)
			}
			if len(item.Events) != 1 || item.Events[0].ToStatus != "fulfilled" {
				t.Fatalf("order event evidence missing: %#v", item)
			}
			seen[item.ID] = struct{}{}
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 106 {
		t.Fatalf("expected 106 owned orders, got %d", len(seen))
	}
	if _, err := service.ListOrders(ctx, buyerID, marketplace.OrderListInput{Cursor: cursor + "modified", Limit: 50}); !errors.Is(err, marketplace.ErrInvalidOrderFilter) {
		t.Fatalf("modified order cursor was accepted: %v", err)
	}
	if _, err := service.ListOrders(ctx, buyerID, marketplace.OrderListInput{Limit: 51}); !errors.Is(err, marketplace.ErrInvalidOrderFilter) {
		t.Fatalf("oversized order page was accepted: %v", err)
	}
	outsider, err := service.ListOrders(ctx, outsiderID, marketplace.OrderListInput{})
	if err != nil || len(outsider.Items) != 0 {
		t.Fatalf("buyer order history leaked: %#v %v", outsider, err)
	}
}
