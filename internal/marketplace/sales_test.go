package marketplace_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seedSellerSales(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID, uuid.UUID, []uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	seller, buyer, source, product := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedMarketplaceProduct(t, pool, seller, buyer, source, product)
	ids := []uuid.UUID{}
	at := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	for i := 0; i < 55; i++ {
		id := uuid.New()
		ids = append(ids, id)
		// Same timestamp intentionally exercises the UUID tie-breaker.
		if _, err := pool.Exec(ctx, `INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,license_accepted_at,idempotency_key,license_version,license_terms_snapshot,product_title_snapshot,license_name_snapshot,refund_window_days_snapshot,created_at,updated_at)
   VALUES($1,$2,$3,2500,'USD','cancelled',$4,$1::uuid::text,'1.0','Accepted terms, not the current license.','Historical title','Historical license',7,$4,$4)`, id, buyer, product, at); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key)
   VALUES($1,'stripe','product',$2,$3,$4,$5,2500,'USD','cancelled',$6,$1::uuid::text)`, uuid.New(), buyer, seller, product, id, i%2 == 0); err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			if _, err := pool.Exec(ctx, `INSERT INTO product_order_contracts(order_id,source_asset_id,root_asset_id,offer_version,contract) SELECT $1,source_asset_id,root_asset_id,offer_version,contract FROM product_offers WHERE product_id=$2`, id, product); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i := 1; i <= 55; i++ {
		if _, err := pool.Exec(ctx, `INSERT INTO order_events(order_id,actor_id,sequence,from_status,to_status,reason) VALUES($1,$2,$3,'payment_pending','cancelled','private-buyer-refund-reason')`, ids[0], buyer, i); err != nil {
			t.Fatal(err)
		}
	}
	return seller, buyer, product, ids
}

func TestSellerSalesHistoricalOwnershipPaginationAndPrivacy(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	seller, buyer, product, ids := seedSellerSales(t, pool)
	ctx := context.Background()
	svc := marketplace.NewService(pool)
	// Changing today's listing does not rewrite accepted sales, titles or terms.
	if _, err := pool.Exec(ctx, `UPDATE products SET seller_id=$2,title='Changed title',price_cents=9900 WHERE id=$1`, product, buyer); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE licenses SET terms='Changed terms'`); err != nil {
		t.Fatal(err)
	}
	seen := map[uuid.UUID]bool{}
	cursor := ""
	var firstCursor string
	for {
		page, err := svc.ListSales(ctx, seller, marketplace.SellerSalesFilter{Limit: 20, Cursor: cursor})
		if err != nil || page.Total != 55 {
			t.Fatal("page", page, err)
		}
		for _, item := range page.Items {
			if seen[item.OrderID] || item.Title != "Historical title" || item.AmountCents != 2500 {
				t.Fatal("unstable historical sale", item)
			}
			seen[item.OrderID] = true
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
		if firstCursor == "" {
			firstCursor = cursor
		}
	}
	if len(seen) != 55 {
		t.Fatal("lost sales", len(seen))
	}
	for _, f := range []marketplace.SellerSalesFilter{{Cursor: firstCursor, Status: "cancelled"}, {Cursor: firstCursor, Environment: "live"}, {Cursor: firstCursor, ProductID: product}, {Cursor: "bad"}, {Status: "unknown"}, {Environment: "unknown_env"}, {Limit: 51}, {Limit: -1}, {Cursor: strings.Repeat("a", 1025)}} {
		if _, err := svc.ListSales(ctx, seller, f); !errors.Is(err, marketplace.ErrInvalidSaleFilter) {
			t.Fatal("invalid scope accepted", f, err)
		}
	}
	if _, err := svc.ListSales(ctx, buyer, marketplace.SellerSalesFilter{Cursor: firstCursor}); !errors.Is(err, marketplace.ErrInvalidSaleFilter) {
		t.Fatal("foreign cursor", err)
	}
	for _, f := range []marketplace.SellerSalesFilter{{Environment: "live"}, {Environment: "test"}, {ProductID: product, Status: "cancelled"}, {Status: "fulfilled"}, {ProductID: uuid.New()}} {
		p, err := svc.ListSales(ctx, seller, f)
		if err != nil {
			t.Fatal(err)
		}
		expected := 0
		switch {
		case f.Environment == "live":
			expected = 28
		case f.Environment == "test":
			expected = 27
		case f.ProductID == product:
			expected = 55
		}
		if p.Total != expected {
			t.Fatal("filtered total", f, p.Total, expected)
		}
	}
	foreign, err := svc.ListSales(ctx, buyer, marketplace.SellerSalesFilter{})
	if err != nil || foreign.Total != 0 {
		t.Fatal("new owner inherited sales", foreign, err)
	}
	detail, err := svc.GetSale(ctx, seller, ids[0])
	if err != nil || detail.LicenseTerms != "Accepted terms, not the current license." || !detail.NeedsReview || !detail.HasContract {
		t.Fatal(detail, err)
	}
	body, _ := json.Marshal(detail)
	for _, value := range []string{buyer.String(), "buyerId", "email", "paymentId", "storageKey", "checkoutUrl", "private-buyer-refund-reason"} {
		if strings.Contains(string(body), value) {
			t.Fatal("private projection", value)
		}
	}
	if _, err = svc.GetSale(ctx, buyer, ids[0]); !errors.Is(err, marketplace.ErrNotFound) {
		t.Fatal("foreign detail", err)
	}
	if _, err = svc.SaleEvents(ctx, buyer, ids[0], "", 20); !errors.Is(err, marketplace.ErrNotFound) {
		t.Fatal("foreign events", err)
	}
	events, err := svc.SaleEvents(ctx, seller, ids[0], "", 20)
	if err != nil || len(events.Items) != 20 || events.NextCursor == nil {
		t.Fatal(events, err)
	}
	eventCursor := *events.NextCursor
	var malformed map[string]any
	decoded, _ := base64.RawURLEncoding.DecodeString(eventCursor)
	if err = json.Unmarshal(decoded, &malformed); err != nil {
		t.Fatal(err)
	}
	malformed["sequence"] = 2147483648
	encoded, _ := json.Marshal(malformed)
	if _, err = svc.SaleEvents(ctx, seller, ids[0], base64.RawURLEncoding.EncodeToString(encoded), 20); !errors.Is(err, marketplace.ErrInvalidSaleFilter) {
		t.Fatal("out-of-range event sequence", err)
	}
	if _, err = svc.SaleEvents(ctx, seller, ids[1], eventCursor, 20); !errors.Is(err, marketplace.ErrInvalidSaleFilter) {
		t.Fatal("cross order cursor", err)
	}
	if _, err = svc.SaleEvents(ctx, seller, ids[0], firstCursor, 20); !errors.Is(err, marketplace.ErrInvalidSaleFilter) {
		t.Fatal("list cursor as events", err)
	}
	if _, err = svc.ListSales(ctx, seller, marketplace.SellerSalesFilter{Cursor: eventCursor}); !errors.Is(err, marketplace.ErrInvalidSaleFilter) {
		t.Fatal("event cursor as list", err)
	}
	count := 0
	cursor = ""
	for {
		p, e := svc.SaleEvents(ctx, seller, ids[0], cursor, 20)
		if e != nil {
			t.Fatal(e)
		}
		for _, event := range p.Items {
			count++
			if event.Sequence != count {
				t.Fatal("event ordering", event)
			}
		}
		if p.NextCursor == nil {
			break
		}
		cursor = *p.NextCursor
	}
	if count != 55 {
		t.Fatal("history truncated", count)
	}
	// Conflicting payee evidence is flagged but cannot hand the accepted sale to
	// another account. The contract remains the authoritative attribution.
	if _, err = pool.Exec(ctx, `UPDATE payment_intents SET payee_id=$2 WHERE order_id=$1`, ids[0], buyer); err != nil {
		t.Fatal(err)
	}
	detail, err = svc.GetSale(ctx, seller, ids[0])
	if err != nil || !detail.NeedsReview {
		t.Fatal("conflict hidden", detail, err)
	}
	if _, err = svc.GetSale(ctx, buyer, ids[0]); !errors.Is(err, marketplace.ErrNotFound) {
		t.Fatal("payee overrode contract", err)
	}
	// Downgrade only derived data and indexes, then restore without losing orders.
	for _, direction := range []string{"down", "up"} {
		b, e := os.ReadFile(fmt.Sprintf("../platform/database/migrations/0094_seller_sales.%s.sql", direction))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, string(b)); e != nil {
			t.Fatal(e)
		}
	}
	p, err := svc.ListSales(ctx, seller, marketplace.SellerSalesFilter{})
	if err != nil || p.Total != 55 {
		t.Fatal("migration lost sales", p, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, seller); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.ListSales(ctx, seller, marketplace.SellerSalesFilter{}); !errors.Is(err, marketplace.ErrListingForbidden) {
		t.Fatal("inactive seller read", err)
	}
}

func TestSellerSalesNeverInventsLegacyOwnershipOrAcceptance(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	seller, buyer, product, ids := seedSellerSales(t, pool)
	ctx := context.Background()
	withContract, withoutEvidence := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{withContract, withoutEvidence} {
		if _, err := pool.Exec(ctx, `INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,idempotency_key,license_version,license_terms_snapshot,product_title_snapshot,license_name_snapshot,refund_window_days_snapshot)
 SELECT $1,buyer_id,product_id,amount_cents,currency,status,$1::uuid::text,license_version,license_terms_snapshot,product_title_snapshot,license_name_snapshot,refund_window_days_snapshot FROM orders WHERE id=$2`, id, ids[0]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO product_order_contracts(order_id,source_asset_id,root_asset_id,offer_version,contract) SELECT $1,source_asset_id,root_asset_id,offer_version,contract FROM product_offers WHERE product_id=$2`, withContract, product); err != nil {
		t.Fatal(err)
	}
	svc := marketplace.NewService(pool)
	item, err := svc.GetSale(ctx, seller, withContract)
	if err != nil || item.Environment != "unknown" || !item.NeedsReview || item.LicenseAcceptedAt != nil || item.PaymentStatus != "" {
		t.Fatal("missing evidence invented", item, err)
	}
	encoded, _ := json.Marshal(item)
	if strings.Contains(string(encoded), "licenseAcceptedAt") || strings.Contains(string(encoded), "0001-") {
		t.Fatal("invented acceptance date", string(encoded))
	}
	page, err := svc.ListSales(ctx, seller, marketplace.SellerSalesFilter{Environment: "unknown"})
	if err != nil || page.Total != 1 || page.Items[0].OrderID != withContract {
		t.Fatal("unknown environment", page, err)
	}
	for _, actor := range []uuid.UUID{seller, buyer} {
		if _, err := svc.GetSale(ctx, actor, withoutEvidence); !errors.Is(err, marketplace.ErrNotFound) {
			t.Fatal("guessed owner of unproven sale", err)
		}
	}
}
