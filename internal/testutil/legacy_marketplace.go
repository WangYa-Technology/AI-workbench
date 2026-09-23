package testutil

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type LegacyProductOrder struct{ OrderID, AssetID uuid.UUID }

// SeedLegacyProductOrder creates static historical evidence only inside an
// isolated integration schema. Production code must never mint local purchases.
func SeedLegacyProductOrder(t *testing.T, pool *pgxpool.Pool, buyer, product uuid.UUID) LegacyProductOrder {
	t.Helper()
	return seedLegacyProductOrder(t, pool, buyer, product, true)
}

func SeedLegacyProductOrderWithoutAudit(t *testing.T, pool *pgxpool.Pool, buyer, product uuid.UUID) LegacyProductOrder {
	t.Helper()
	return seedLegacyProductOrder(t, pool, buyer, product, false)
}

func seedLegacyProductOrder(t *testing.T, pool *pgxpool.Pool, buyer, product uuid.UUID, withAudit bool) LegacyProductOrder {
	t.Helper()
	ctx := context.Background()
	var schema string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(schema, "test_") {
		t.Fatal("legacy fixture requires an isolated test schema")
	}
	f := LegacyProductOrder{OrderID: uuid.New(), AssetID: uuid.New()}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	statements := []string{
		`INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,license_accepted_at,idempotency_key,product_title_snapshot,license_name_snapshot,license_version,license_terms_snapshot,refund_window_days_snapshot)
  SELECT $1,$2,p.id,p.price_cents,p.currency,'fulfilled',now(),$1::uuid::text,p.title,l.name,l.version,l.terms,l.refund_window_days FROM products p JOIN licenses l ON l.code=p.license_code WHERE p.id=$3`,
		`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,width,height,scan_status,source_type,source_id,license_code,origin_asset_id)
  SELECT $4,$2,a.kind,a.title,CASE WHEN a.media_url LIKE '/api/v1/assets/%' THEN '/api/v1/assets/'||$4::uuid::text||'/content' ELSE a.media_url END,a.mime_type,a.width,a.height,'clean','purchase',$1,p.license_code,COALESCE(a.origin_asset_id,a.id) FROM products p JOIN assets a ON a.id=p.asset_id WHERE p.id=$3`,
		`INSERT INTO entitlements(user_id,product_id,order_id,asset_id,license_code,status)
  SELECT $2,$3,$1,$4,license_code,'active' FROM products WHERE id=$3`,
		`INSERT INTO order_events(order_id,actor_id,from_status,to_status,reason,sequence) VALUES
  ($1,$2,NULL,'test_pending','Historical fixture: license accepted.',1),
  ($1,$2,'test_pending','test_paid','Historical fixture: internal balance payment.',2),
  ($1,$2,'test_paid','fulfilled','Historical fixture: access granted.',3)`,
		`UPDATE billing_accounts b SET balance_cents=10000 + CASE WHEN b.user_id=$2 THEN -p.price_cents ELSE p.price_cents END FROM products p WHERE p.id=$3 AND b.user_id IN ($2,p.seller_id) AND b.currency=p.currency`,
		`INSERT INTO billing_entries(user_id,operation_id,entry_type,direction,amount_cents,currency,balance_after_cents,description)
  SELECT b.user_id,$1,CASE WHEN b.user_id=$2 THEN 'product_purchase' ELSE 'product_sale' END,CASE WHEN b.user_id=$2 THEN 'debit' ELSE 'credit' END,p.price_cents,p.currency,b.balance_cents,'Historical fixture evidence'
  FROM products p JOIN billing_accounts b ON b.user_id IN ($2,p.seller_id) AND b.currency=p.currency WHERE p.id=$3 AND p.price_cents>0`,
		`INSERT INTO ledger_entries(account_id,operation_id,direction,amount_cents,currency,reason)
  SELECT b.user_id,$1,b.direction,b.amount_cents,b.currency,CASE WHEN b.user_id=$2 THEN 'test_purchase_debit_no_real_charge' ELSE 'test_purchase_credit_no_real_payout' END FROM billing_entries b WHERE b.operation_id=$1`,
		`INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
  VALUES($2,'marketplace.test_purchase','order',$1,'isolated-historical-fixture',jsonb_build_object('productId',$3::uuid::text,'realCharge',false,'paymentMode','test'))`,
	}
	for _, sql := range statements {
		if !withAudit && strings.Contains(sql, "INSERT INTO audit_events") {
			continue
		}
		// Every statement shares typed fixture parameters; the unused parameter CTE
		// keeps PostgreSQL's prepared-statement parameter types deterministic.
		sql = "WITH fixture_args AS (SELECT $1::uuid,$2::uuid,$3::uuid,$4::uuid) " + sql
		if _, err = tx.Exec(ctx, sql, f.OrderID, buyer, product, f.AssetID); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return f
}
