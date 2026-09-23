package marketplace_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
)

func TestHistoricalRefundRequiresOriginalEvidence(t *testing.T) {
	for _, scenario := range []string{"missing_audit", "missing_ledger", "unbalanced", "extra_entry", "missing_billing", "external_intent", "zero_price"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := marketplaceTestPool(t)
			defer cleanup()
			ctx := context.Background()
			seller, buyer, source, product := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			seedMarketplaceProduct(t, pool, seller, buyer, source, product)
			if scenario == "zero_price" {
				if _, err := pool.Exec(ctx, `UPDATE products SET price_cents=0 WHERE id=$1`, product); err != nil {
					t.Fatal(err)
				}
			}
			var f testutil.LegacyProductOrder
			if scenario == "missing_audit" {
				f = testutil.SeedLegacyProductOrderWithoutAudit(t, pool, buyer, product)
			} else {
				f = testutil.SeedLegacyProductOrder(t, pool, buyer, product)
			}
			var sql string
			switch scenario {
			case "missing_ledger":
				sql = `DELETE FROM ledger_entries WHERE operation_id=$1`
			case "unbalanced":
				sql = `UPDATE ledger_entries SET amount_cents=amount_cents+1 WHERE operation_id=$1 AND direction='credit'`
			case "extra_entry":
				sql = `INSERT INTO ledger_entries(account_id,operation_id,direction,amount_cents,currency,reason) SELECT account_id,operation_id,direction,amount_cents,currency,'unexplained duplicate' FROM ledger_entries WHERE operation_id=$1 AND direction='credit'`
			case "missing_billing":
				sql = `DELETE FROM billing_entries WHERE operation_id=$1 AND direction='credit'`
			case "external_intent":
				sql = `INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key) SELECT gen_random_uuid(),'stripe','product',o.buyer_id,p.seller_id,p.id,o.id,o.amount_cents,o.currency,'paid',true,o.id::text FROM orders o JOIN products p ON p.id=o.product_id WHERE o.id=$1`
			}
			if sql != "" {
				if _, err := pool.Exec(ctx, sql, f.OrderID); err != nil {
					t.Fatal(err)
				}
			}
			svc := marketplace.NewService(pool)
			order, err := svc.GetOrder(ctx, buyer, f.OrderID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "zero_price" {
				if !order.CanRequestRefund {
					t.Fatal("free historical grant cannot be reversed", order)
				}
				if _, err = svc.RefundLegacyOrder(ctx, buyer, f.OrderID, "free-legacy-refund", "test", "Return the historical free grant."); err != nil {
					t.Fatal(err)
				}
				return
			}
			if scenario != "external_intent" {
				asset, e := assets.NewService(pool, t.TempDir()).GetOwned(ctx, buyer, f.AssetID)
				if e != nil || asset.Provenance == nil || asset.Provenance.Purchase == nil || asset.Provenance.Purchase.PaymentMode != "unverified" || asset.Provenance.Purchase.SellerID != nil {
					t.Fatalf("unverified purchase fabricated seller: %+v %v", asset.Provenance, e)
				}
			}
			if scenario != "external_intent" && (order.CanRequestRefund || order.RefundUnavailableReason != "reconciliation_required") {
				t.Fatalf("advertised unverified refund: %+v", order)
			}
			if _, err = svc.RefundLegacyOrder(ctx, buyer, f.OrderID, "unverified-refund-command", "test", "Reconcile the historical original payment."); !errors.Is(err, marketplace.ErrLegacyRefundEvidence) {
				t.Fatal("unsafe local refund accepted", err)
			}
			var state string
			var operations int
			if err = pool.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM billing_entries WHERE entry_type='product_refund') FROM entitlements WHERE order_id=$1`, f.OrderID).Scan(&state, &operations); err != nil || state != "active" || operations != 0 {
				t.Fatalf("rejected command changed access or balance: %s %d %v", state, operations, err)
			}
		})
	}
}

func TestHistoricalRefundUsesOriginalPayeeAndSerializesReplay(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	seller, buyer, source, product := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedMarketplaceProduct(t, pool, seller, buyer, source, product)
	f := testutil.SeedLegacyProductOrder(t, pool, buyer, product)
	replacement := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name) VALUES($1,$1::uuid::text||'@test.local','new_seller','New catalogue seller')`, replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE products SET seller_id=$1 WHERE id=$2`, replacement, product); err != nil {
		t.Fatal(err)
	}
	item, err := assets.NewService(pool, t.TempDir()).GetOwned(ctx, buyer, f.AssetID)
	if err != nil || item.Provenance == nil || item.Provenance.Purchase == nil || item.Provenance.Purchase.SellerID == nil || *item.Provenance.Purchase.SellerID != seller {
		t.Fatalf("mutable provenance seller: %+v %v", item.Provenance, err)
	}
	// Original event history need not stop at sequence 3.
	if _, err := pool.Exec(ctx, `INSERT INTO order_events(order_id,actor_id,from_status,to_status,reason,sequence) VALUES($1,$2,'fulfilled','fulfilled','Historical support note.',4)`, f.OrderID, buyer); err != nil {
		t.Fatal(err)
	}
	var replacementBefore int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM billing_accounts WHERE user_id=$1`, replacement).Scan(&replacementBefore); err != nil {
		t.Fatal(err)
	}
	svc := marketplace.NewService(pool)
	reason := "Restore the original recorded internal balance."
	if _, err := svc.RefundLegacyOrder(ctx, replacement, f.OrderID, "foreign-refund-command", "test", reason); !errors.Is(err, marketplace.ErrOrderNotFound) {
		t.Fatal("cross-user access", err)
	}
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := svc.RefundLegacyOrder(ctx, buyer, f.OrderID, "historical-refund-command", "test", reason)
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	for _, change := range []struct{ key, reason string }{{"historical-refund-command", reason + " Changed."}, {"different-refund-command", reason}} {
		if _, err := svc.RefundLegacyOrder(ctx, buyer, f.OrderID, change.key, "test", change.reason); !errors.Is(err, marketplace.ErrIdempotencyConflict) {
			t.Fatal("changed replay accepted", err)
		}
	}
	var buyerBalance, sellerBalance, replacementAfter int64
	var completed, entries, sequence int
	if err := pool.QueryRow(ctx, `SELECT
 (SELECT balance_cents FROM billing_accounts WHERE user_id=$1),
 (SELECT balance_cents FROM billing_accounts WHERE user_id=$2),
 (SELECT balance_cents FROM billing_accounts WHERE user_id=$3),
 (SELECT count(*) FROM audit_events WHERE resource_id=$4 AND action='marketplace.legacy_refund'),
 (SELECT count(*) FROM billing_entries WHERE entry_type='product_refund'),
 (SELECT max(sequence) FROM order_events WHERE order_id=$4)`, buyer, seller, replacement, f.OrderID).Scan(&buyerBalance, &sellerBalance, &replacementAfter, &completed, &entries, &sequence); err != nil {
		t.Fatal(err)
	}
	if buyerBalance != 10000 || sellerBalance != 10000 || replacementAfter != replacementBefore || completed != 1 || entries != 2 || sequence != 6 {
		t.Fatalf("wrong reversal: buyer=%d original=%d new=%d/%d audit=%d entries=%d sequence=%d", buyerBalance, sellerBalance, replacementAfter, replacementBefore, completed, entries, sequence)
	}
	otherProduct := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status) SELECT $1,seller_id,asset_id,'Other resource',description,product_type,price_cents,currency,license_code,status FROM products WHERE id=$2`, otherProduct, product); err != nil {
		t.Fatal(err)
	}
	other := testutil.SeedLegacyProductOrder(t, pool, buyer, otherProduct)
	if _, err := svc.RefundLegacyOrder(ctx, buyer, other.OrderID, "historical-refund-command", "test", reason); !errors.Is(err, marketplace.ErrIdempotencyConflict) {
		t.Fatal("cross-order key reuse", err)
	}
	var state string
	if err := pool.QueryRow(ctx, `SELECT status FROM entitlements WHERE order_id=$1`, other.OrderID).Scan(&state); err != nil || state != "active" {
		t.Fatal("conflicting key revoked another purchase", err)
	}
}

func TestHistoricalRefundEvidenceViewRollbackPreservesRecords(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	ctx := context.Background()
	seller, buyer, source, product := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedMarketplaceProduct(t, pool, seller, buyer, source, product)
	f := testutil.SeedLegacyProductOrder(t, pool, buyer, product)
	for _, direction := range []string{"down", "up"} {
		sql, err := os.ReadFile(fmt.Sprintf("../platform/database/migrations/0097_legacy_product_refund_evidence.%s.sql", direction))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	var payee uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT seller_id FROM legacy_product_refund_evidence WHERE order_id=$1`, f.OrderID).Scan(&payee); err != nil || payee != seller {
		t.Fatal("lost historical evidence", err)
	}
}
