package payments

import (
	"context"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/assets"
)

func TestPurchaseAccessListTracksDetailAndRevocation(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	f := legacyAccessFixture(t, pool, "internal")
	svc := assets.NewService(pool, f.root)
	check := func(status string, allowed, visible bool) {
		t.Helper()
		detail, err := svc.GetOwned(ctx, f.buyer, f.purchased)
		if err != nil {
			t.Fatal(err)
		}
		a := detail.PurchaseAccess
		if a == nil || a.Status != status || a.CanDownload != allowed || a.CanReuse != allowed || a.OrderID == nil || *a.OrderID != f.order {
			t.Fatalf("detail access: %+v", a)
		}
		if detail.Provenance.Purchase.CanDownload != a.CanDownload || detail.Provenance.Purchase.CanReuse != a.CanReuse {
			t.Fatal("detail capabilities disagree")
		}
		page, err := svc.List(ctx, f.buyer, assets.ListInput{Source: "purchase", Query: detail.Title, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range page.Items {
			if item.ID == f.purchased {
				found = true
				if item.PurchaseAccess == nil || *item.PurchaseAccess.OrderID != *a.OrderID || item.PurchaseAccess.Status != a.Status || item.PurchaseAccess.CanDownload != a.CanDownload || item.PurchaseAccess.CanReuse != a.CanReuse {
					t.Fatalf("list differs: %+v", item.PurchaseAccess)
				}
			}
		}
		if found != visible {
			t.Fatalf("visible=%v want=%v", found, visible)
		}
	}
	check("available", true, true)
	if _, err := pool.Exec(ctx, `UPDATE orders SET status='refund_requested' WHERE id=$1`, f.order); err != nil {
		t.Fatal(err)
	}
	check("refund_pending", true, true)
	if _, err := pool.Exec(ctx, `UPDATE assets SET scan_status='review' WHERE id=$1`, f.purchased); err != nil {
		t.Fatal(err)
	}
	check("scan_blocked", false, true)
	if _, err := pool.Exec(ctx, `UPDATE assets SET scan_status='clean' WHERE id=$1`, f.purchased); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE entitlements SET status='refunded',revoked_at=now() WHERE asset_id=$1`, f.purchased); err != nil {
		t.Fatal(err)
	}
	check("revoked", false, false)
}
