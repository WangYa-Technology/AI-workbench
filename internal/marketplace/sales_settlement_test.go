package marketplace_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/jackc/pgx/v5/pgxpool"
)

func assignSettlementTestBuyer(t *testing.T, pool *pgxpool.Pool, order uuid.UUID) uuid.UUID {
	t.Helper()
	buyer := uuid.New()
	handle := "settlement_" + strings.ReplaceAll(buyer.String(), "-", "")[:12]
	if _, err := pool.Exec(t.Context(), `INSERT INTO users(id,email,handle,display_name,role,status)
 VALUES($1,$2,$3,'Settlement Buyer','member','active')`, buyer, handle+"@test.local", handle); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE orders SET buyer_id=$2 WHERE id=$1`, order, buyer); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE payment_intents SET payer_id=$2 WHERE order_id=$1`, order, buyer); err != nil {
		t.Fatal(err)
	}
	return buyer
}

func TestSellerSalesSettlementProjection(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	seller, buyer, product, orders := seedSellerSales(t, pool)
	svc := marketplace.NewService(pool)
	execute := func(t *testing.T, query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	states := []string{"pending_hold", "available", "transfer_pending", "transferred", "refund_hold", "recovery_required", "provider_unsupported", "cancelled"}
	for i, state := range states {
		t.Run(state, func(t *testing.T) {
			order := orders[i]
			saleBuyer := assignSettlementTestBuyer(t, pool, order)
			execute(t, `UPDATE orders SET status='fulfilled' WHERE id=$1`, order)
			execute(t, `UPDATE payment_intents SET status='paid' WHERE order_id=$1`, order)
			if state == "refund_hold" || state == "recovery_required" || state == "cancelled" {
				execute(t, `UPDATE orders SET status=$2 WHERE id=$1`, order, map[bool]string{true: "refund_requested", false: "refunded"}[state == "refund_hold"])
				execute(t, `UPDATE payment_intents SET status=$2 WHERE order_id=$1`, order, map[bool]string{true: "refund_pending", false: "refunded"}[state == "refund_hold"])
			}
			execute(t, `INSERT INTO product_settlements(order_id,payment_id,seller_id,provider,live_mode,gross_amount_cents,fee_bps,fee_cents,net_amount_cents,currency,status,available_at,
 transfer_idempotency_key,destination_id,provider_transfer_id,transferred_at,recovery_amount_cents,hold_reason)
 SELECT order_id,id,payee_id,provider,live_mode,2500,250,63,2437,'USD',$2,now()+interval '7 days','private-transfer-'||id::text,'acct_private_destination',
 CASE WHEN $2 IN ('transferred','recovery_required') THEN 'tr_private_'||id::text END,
 CASE WHEN $2 IN ('transferred','recovery_required') THEN now() END,
 CASE WHEN $2='recovery_required' THEN 2437 ELSE 0 END,'private-internal-reason'
 FROM payment_intents WHERE order_id=$1`, order, state)
			detail, err := svc.GetSale(t.Context(), seller, order)
			if err != nil || detail.Settlement == nil {
				t.Fatal("missing settlement", detail, err)
			}
			s := detail.Settlement
			if s.Status != state || s.Environment != detail.Environment || s.GrossAmountCents != 2500 || s.FeeBPS != 250 || s.FeeCents != 63 || s.NetAmountCents != 2437 || s.Currency != "USD" || s.AvailableAt == nil || s.AvailableAt.Before(time.Now()) {
				t.Fatal("incorrect frozen economics", s)
			}
			if (s.TransferredAt != nil) != (state == "transferred" || state == "recovery_required") || (s.RecoveryAmountCents == 2437) != (state == "recovery_required") {
				t.Fatal("lost transfer or refund recovery evidence", s)
			}
			if (state == "recovery_required" || state == "provider_unsupported") && !detail.NeedsReview {
				t.Fatal("hidden settlement review", detail)
			}
			raw, err := json.Marshal(detail)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"acct_private", "tr_private", "private-transfer", "private-internal", "destinationId", "providerTransferId", "paymentId", saleBuyer.String()} {
				if strings.Contains(string(raw), secret) {
					t.Fatalf("leaked %s", secret)
				}
			}
			if _, err := svc.GetSale(t.Context(), saleBuyer, order); !errors.Is(err, marketplace.ErrNotFound) {
				t.Fatal("foreign settlement", err)
			}
		})
	}
	// Current listing ownership and fee settings cannot rewrite frozen settlement economics.
	execute(t, `UPDATE product_settlement_settings SET platform_fee_bps=9000,hold_days=365`)
	execute(t, `UPDATE products SET seller_id=$2 WHERE id=$1`, product, buyer)
	page, err := svc.ListSales(t.Context(), seller, marketplace.SellerSalesFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uuid.UUID]bool{}
	for {
		for _, item := range page.Items {
			if item.Settlement != nil {
				seen[item.OrderID] = true
				if item.Settlement.FeeBPS != 250 || item.Settlement.NetAmountCents != 2437 {
					t.Fatal("changed settlement", item)
				}
			}
		}
		if page.NextCursor == nil {
			break
		}
		page, err = svc.ListSales(t.Context(), seller, marketplace.SellerSalesFilter{Limit: 50, Cursor: *page.NextCursor})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != len(states) {
		t.Fatal("lost paginated settlement", len(seen))
	}
	foreign, err := svc.ListSales(t.Context(), buyer, marketplace.SellerSalesFilter{})
	if err != nil || foreign.Total != 0 {
		t.Fatal("new listing owner inherited settlements", foreign, err)
	}
}

func TestSellerSalesSettlementMissingAndConflicts(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	defer cleanup()
	seller, buyer, _, orders := seedSellerSales(t, pool)
	svc := marketplace.NewService(pool)
	execute := func(t *testing.T, query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for i, conflict := range []string{"missing", "seller", "payment", "mode", "provider", "amount", "time", "transfer_time"} {
		t.Run(conflict, func(t *testing.T) {
			order := orders[i]
			assignSettlementTestBuyer(t, pool, order)
			execute(t, `UPDATE orders SET status='fulfilled' WHERE id=$1`, order)
			execute(t, `UPDATE payment_intents SET status='paid' WHERE order_id=$1`, order)
			if conflict != "missing" {
				_, insertErr := pool.Exec(t.Context(), `INSERT INTO product_settlements(order_id,payment_id,seller_id,provider,live_mode,gross_amount_cents,fee_bps,fee_cents,net_amount_cents,
 currency,status,available_at,transfer_idempotency_key,provider_transfer_id,transferred_at)
 SELECT order_id,CASE WHEN $2='payment' THEN (SELECT id FROM payment_intents WHERE order_id=$4) ELSE id END,
 CASE WHEN $2='seller' THEN $3 ELSE payee_id END,
 CASE WHEN $2='provider' THEN 'waffo_pancake' ELSE provider END,
 CASE WHEN $2='mode' THEN NOT live_mode ELSE live_mode END,
 CASE WHEN $2='amount' THEN 2501 ELSE 2500 END,0,0,CASE WHEN $2='amount' THEN 2501 ELSE 2500 END,
 'USD',CASE WHEN $2='transfer_time' THEN 'transferred' ELSE 'pending_hold' END,
 CASE WHEN $2='time' THEN 'infinity'::timestamptz ELSE now() END,'private-'||id::text,
 CASE WHEN $2='transfer_time' THEN 'tr_invalid_time' END,CASE WHEN $2='transfer_time' THEN 'infinity'::timestamptz END
	 FROM payment_intents WHERE order_id=$1`, order, conflict, buyer, orders[54])
				if conflict == "payment" {
					if insertErr == nil {
						t.Fatal("database accepted settlement payment/order mismatch")
					}
				} else if insertErr != nil {
					t.Fatal(insertErr)
				}
			}
			detail, err := svc.GetSale(t.Context(), seller, order)
			if err != nil || !detail.NeedsReview {
				t.Fatal("conflict hidden", detail, err)
			}
			if conflict == "time" || conflict == "transfer_time" {
				if detail.Settlement == nil || detail.Settlement.TransferredAt != nil || (conflict == "time" && detail.Settlement.AvailableAt != nil) {
					t.Fatal("invalid clock not sanitized", detail)
				}
			} else if detail.Settlement != nil {
				t.Fatal("unbound financial projection", detail.Settlement)
			}
		})
	}
	// A cancelled order without fulfillment has no inferred settlement amount.
	// The legacy payment still needs review because it has no checkout request.
	detail, err := svc.GetSale(t.Context(), seller, orders[10])
	if err != nil || detail.Settlement != nil || !detail.NeedsReview {
		t.Fatal("invented settlement", detail, err)
	}
	// Historical fulfillment stays visible after a refund even without a snapshot.
	execute(t, `UPDATE orders SET status='refunded' WHERE id=$1`, orders[10])
	execute(t, `UPDATE payment_intents SET status='refunded' WHERE order_id=$1`, orders[10])
	execute(t, `INSERT INTO order_events(order_id,actor_id,sequence,to_status,reason) VALUES($1,$2,1,'fulfilled','historical fulfillment evidence')`, orders[10], seller)
	detail, err = svc.GetSale(t.Context(), seller, orders[10])
	if err != nil || detail.Settlement != nil || !detail.NeedsReview {
		t.Fatal("historical gap hidden", detail, err)
	}
}
