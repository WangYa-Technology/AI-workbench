package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestHistoricalProductRefundHTTPRequiresEvidenceAndPreservesRights(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := context.Background()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	buyerClient, sellerClient, guest := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	buyer := registerGovernanceUser(t, buyerClient, server.URL, "legacy_refund_buyer")
	seller := registerGovernanceUser(t, sellerClient, server.URL, "legacy_refund_seller")
	source, product := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key) VALUES($1,$2,'image','Historical source','/historical.jpg','image/jpeg','clean','upload','hcai-commercial-standard-v1','local_file','historical.jpg')`, source, seller.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status) VALUES($1,$2,$3,'Historical resource','An accepted historical order.','asset',1900,'USD','hcai-commercial-standard-v1','active')`, product, seller.ID, source); err != nil {
		t.Fatal(err)
	}
	f := testutil.SeedLegacyProductOrder(t, pool, buyer.ID, product)
	base := server.URL + "/api/v1/orders/" + f.OrderID.String()
	input := map[string]string{"reason": "Reverse the original internal balance payment."}
	if r := requestPaymentJSON(t, guest, "POST", base+"/refund", "guest-refund-command", input, nil); r.StatusCode != 401 {
		t.Fatal(r.StatusCode)
	}
	if r := requestPaymentJSON(t, sellerClient, "POST", base+"/refund", "foreign-refund-command", input, nil); r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
	var order marketplace.Order
	if r := requestJSON(t, buyerClient, "GET", base, nil, &order); r.StatusCode != 200 || !order.CanRequestRefund {
		t.Fatal("historical capability", r.StatusCode, order)
	}
	// Without the original debit evidence, missing provider identity must never
	// be interpreted as permission to refund through a synthetic balance.
	if _, err := pool.Exec(ctx, `DELETE FROM ledger_entries WHERE operation_id=$1 AND direction='debit'`, f.OrderID); err != nil {
		t.Fatal(err)
	}
	if r := requestJSON(t, buyerClient, "GET", base, nil, &order); r.StatusCode != 200 || order.CanRequestRefund || order.RefundUnavailableReason != "reconciliation_required" || order.PaymentMode != "unverified" {
		t.Fatal("unverified capability", r.StatusCode, order)
	}
	if r := requestPaymentJSON(t, buyerClient, "POST", base+"/refund", "missing-proof-command", input, nil); r.StatusCode != 409 {
		t.Fatal("unverified reversal", r.StatusCode)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ledger_entries(account_id,operation_id,direction,amount_cents,currency,reason) VALUES($1,$2,'debit',1900,'USD','test_purchase_debit_no_real_charge')`, buyer.ID, f.OrderID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if r := requestPaymentJSON(t, buyerClient, "POST", base+"/refund", "confirmed-historical-refund", input, &order); r.StatusCode != 200 || order.Status != "test_refunded" || order.CanRequestRefund {
			t.Fatal("refund/replay", r.StatusCode, order)
		}
	}
	if r := requestPaymentJSON(t, buyerClient, "POST", base+"/refund", "different-historical-refund", input, nil); r.StatusCode != 409 {
		t.Fatal("changed replay", r.StatusCode)
	}
	var external, balanced int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM payment_intents WHERE order_id=$1),(SELECT count(*) FROM billing_entries WHERE entry_type='product_refund')`, f.OrderID).Scan(&external, &balanced); err != nil || external != 0 || balanced != 2 {
		t.Fatalf("unexpected finance mutation %d %d %v", external, balanced, err)
	}
}
