package payments

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
)

func TestProductCatalogRemovalPreservesContractualAccess(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	_, checkout, buyer, product, _ := fulfilledRefundFixture(t, pool, &durableProductRefundRuntime{})
	market := marketplace.NewService(pool)
	media := assets.NewService(pool, t.TempDir())
	var purchased uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT asset_id FROM entitlements WHERE order_id=$1`, checkout.OrderID).Scan(&purchased); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct{ name, hide, restore string }{
		{"paused", `UPDATE products SET status='paused' WHERE id=$1`, `UPDATE products SET status='active' WHERE id=$1`},
		{"removed", `UPDATE products SET status='removed' WHERE id=$1`, `UPDATE products SET status='active' WHERE id=$1`},
		{"seller_suspended", `UPDATE users SET status='suspended' WHERE id=(SELECT seller_id FROM products WHERE id=$1)`, `UPDATE users SET status='active' WHERE id=(SELECT seller_id FROM products WHERE id=$1)`},
		{"license_retired", `UPDATE licenses SET status='retired' WHERE code=(SELECT license_code FROM products WHERE id=$1)`, `UPDATE licenses SET status='active' WHERE code=(SELECT license_code FROM products WHERE id=$1)`},
	} {
		t.Run(change.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, change.hide, product); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := pool.Exec(ctx, change.restore, product); err != nil {
					t.Fatal(err)
				}
			}()
			if _, err := market.GetProduct(ctx, buyer, product); !errors.Is(err, marketplace.ErrNotFound) {
				t.Fatalf("hidden product visible: %v", err)
			}
			order, err := market.GetOrder(ctx, buyer, checkout.OrderID)
			if err != nil || order.Status != "fulfilled" || order.AssetID == nil || *order.AssetID != purchased || !order.CanRequestRefund {
				t.Fatalf("contract disappeared with catalog item: %+v %v", order, err)
			}
			owned, err := media.GetOwned(ctx, buyer, purchased)
			if err != nil || owned.Provenance == nil || owned.Provenance.Purchase == nil || !owned.Provenance.Purchase.CanDownload || !owned.Provenance.Purchase.CanReuse {
				t.Fatalf("rights changed with catalog: %+v %v", owned.Provenance, err)
			}
			if _, err := media.Content(ctx, buyer, purchased); err != nil {
				t.Fatalf("catalog removal blocked licensed media: %v", err)
			}
			if _, err := media.Content(ctx, uuid.Nil, purchased); !errors.Is(err, assets.ErrForbidden) {
				t.Fatalf("catalog removal exposed private media: %v", err)
			}
		})
	}
}
