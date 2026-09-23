package payments

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
)

func TestSellerSalesTracksVerifiedPaymentRefundAndAcceptedSeller(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	runtime := &refundReadRuntime{}
	service, checkout, buyer, product, _ := fulfilledRefundFixture(t, pool, runtime)
	catalog := marketplace.NewService(pool)
	var seller uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT payee_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&seller); err != nil {
		t.Fatal(err)
	}
	sale, err := catalog.GetSale(ctx, seller, checkout.OrderID)
	if err != nil || sale.Status != "fulfilled" || sale.PaymentStatus != "paid" || sale.NeedsReview || !sale.HasContract || sale.PaidAt == nil || sale.Environment != "test" {
		t.Fatal("paid sale", sale, err)
	}
	title, terms := sale.Title, sale.LicenseTerms
	if _, err = pool.Exec(ctx, `UPDATE orders SET status='cancelled' WHERE id=$1`, checkout.OrderID); err != nil {
		t.Fatal(err)
	}
	conflicting, err := catalog.GetSale(ctx, seller, checkout.OrderID)
	if err != nil || !conflicting.NeedsReview {
		t.Fatal("order/payment state mismatch", conflicting, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE orders SET status='fulfilled' WHERE id=$1`, checkout.OrderID); err != nil {
		t.Fatal(err)
	}
	// An inconsistent financial row must be visible as requiring review, not as
	// available earnings, and must not grant its new payee historical ownership.
	stranger, _, _, _ := newProductCheckoutFixture(t, pool)
	if _, err = pool.Exec(ctx, `UPDATE payment_intents SET payee_id=$2 WHERE id=$1`, checkout.PaymentID, stranger); err != nil {
		t.Fatal(err)
	}
	sale, err = catalog.GetSale(ctx, seller, checkout.OrderID)
	if err != nil || !sale.NeedsReview {
		t.Fatal("payee mismatch", sale, err)
	}
	if _, err = catalog.GetSale(ctx, stranger, checkout.OrderID); !errors.Is(err, marketplace.ErrNotFound) {
		t.Fatal("payee mismatch leaked contract", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE payment_intents SET payee_id=$2 WHERE id=$1`, checkout.PaymentID, seller); err != nil {
		t.Fatal(err)
	}
	refundProductForExport(t, pool, service, runtime, checkout, buyer)
	sale, err = catalog.GetSale(ctx, seller, checkout.OrderID)
	if err != nil || sale.Status != "refunded" || sale.PaymentStatus != "refunded" || sale.NeedsReview || sale.RefundedAt == nil || sale.RefundRequestedAt == nil {
		t.Fatal("verified refund sale", sale, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE products SET seller_id=$2,title='New owner title',price_cents=3900 WHERE id=$1`, product, stranger); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE licenses SET terms='Changed license'`); err != nil {
		t.Fatal(err)
	}
	sale, err = catalog.GetSale(ctx, seller, checkout.OrderID)
	if err != nil || sale.Title != title || sale.LicenseTerms != terms || sale.AmountCents != 1900 {
		t.Fatal("mutable sale snapshot", sale, err)
	}
	events, err := catalog.SaleEvents(ctx, seller, checkout.OrderID, "", 50)
	if err != nil || len(events.Items) < 3 || events.Items[len(events.Items)-1].ToStatus != "refunded" {
		t.Fatal("verified history", events, err)
	}
	page, err := catalog.ListSales(ctx, seller, marketplace.SellerSalesFilter{Status: "refunded", ProductID: product})
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatal("refund filters", page, err)
	}
	export, _ := runProductExport(t, pool, seller)
	if len(export.Data.Marketplace.Data["sales"]) != 1 || export.Data.Marketplace.Data["sales"][0]["orderId"] != checkout.OrderID.String() {
		t.Fatal("export diverged from sales directory")
	}
	foreign, err := catalog.ListSales(ctx, stranger, marketplace.SellerSalesFilter{})
	if err != nil || foreign.Total != 0 {
		t.Fatal("new listing owner inherited sales", foreign, err)
	}
}
