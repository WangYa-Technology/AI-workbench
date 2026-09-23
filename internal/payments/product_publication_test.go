package payments

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func TestManagedPublicationPreservesAcceptedDeliveryAndExports(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	buyer, seller, source, _ := newProductCheckoutFixture(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, buyer); err != nil {
		t.Fatal(err)
	}
	catalog := marketplace.NewService(pool)
	draft := marketplace.ProductDraft{Title: "Accepted original", Description: "Frozen offer", ProductType: "asset", Category: "market_asset", AssetID: source, PriceCents: 1900, Currency: "USD", LicenseCode: "hcai-commercial-standard-v1", AIDisclosure: "AI-assisted original; no reference inputs.", IncludedFiles: []string{"original.jpg"}}
	item, err := catalog.MutateListing(ctx, seller, uuid.Nil, "create", uuid.NewString(), "test", marketplace.ListingMutation{Draft: &draft})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &productCheckoutRuntime{}
	payment := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(runtime))
	// Even a caller possessing the offer hash cannot buy an unreviewed draft.
	if _, _, err = payment.BeginProductCheckout(ctx, buyer, item.ID, "unapproved-buy-command", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, item.ID)); !errors.Is(err, ErrInvalidCheckout) {
		t.Fatal("draft checkout", err)
	}
	item, err = catalog.MutateListing(ctx, seller, item.ID, "submit", uuid.NewString(), "test", marketplace.ListingMutation{ExpectedVersion: item.Version, RightsConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	item, err = catalog.MutateListing(ctx, buyer, item.ID, "approve", uuid.NewString(), "test", marketplace.ListingMutation{ExpectedVersion: item.Version, Confirmed: true, Reason: "Original content and licensing evidence checked."})
	if err != nil {
		t.Fatal(err)
	}
	checkout, _, err := payment.BeginProductCheckout(ctx, buyer, item.ID, "reviewed-buy-command", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, item.ID))
	if err != nil {
		t.Fatal(err)
	}
	var contractBefore string
	if err = pool.QueryRow(ctx, `SELECT contract::text FROM product_order_contracts WHERE order_id=$1`, checkout.OrderID).Scan(&contractBefore); err != nil {
		t.Fatal(err)
	}
	item, err = catalog.MutateListing(ctx, seller, item.ID, "pause", uuid.NewString(), "test", marketplace.ListingMutation{ExpectedVersion: item.Version})
	if err != nil {
		t.Fatal(err)
	}
	draft.Title = "Revised title"
	draft.PriceCents = 3900
	item, err = catalog.MutateListing(ctx, seller, item.ID, "edit", uuid.NewString(), "test", marketplace.ListingMutation{ExpectedVersion: item.Version, Draft: &draft})
	if err != nil {
		t.Fatal(err)
	}
	// Payment arrives after pause/edit. It must fulfill the accepted contract,
	// never use the new draft or deny a legitimate existing buyer.
	now := time.Now().UTC().Truncate(time.Second)
	payment.verifier.now = func() time.Time { return now }
	receipt := receivePaymentWorkflowEvent(t, payment, productPaidEvent(checkout.PaymentID, item.ID, now.Unix(), 1900), now)
	if err = payment.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}); err != nil {
		t.Fatal(err)
	}
	order, err := catalog.GetOrder(ctx, buyer, checkout.OrderID)
	if err != nil || order.Status != "fulfilled" || order.ProductTitle != "Accepted original" || order.AmountCents != 1900 || order.AssetID == nil {
		t.Fatal("contract fulfillment", order, err)
	}
	var contractAfter string
	if err = pool.QueryRow(ctx, `SELECT contract::text FROM product_order_contracts WHERE order_id=$1`, checkout.OrderID).Scan(&contractAfter); err != nil || contractAfter != contractBefore {
		t.Fatal("accepted contract overwritten", err)
	}
	assetService := assets.NewService(pool, paymentTestRoot(t, pool))
	content, err := assetService.Content(ctx, buyer, *order.AssetID)
	if err != nil {
		t.Fatal(err)
	}
	file, err := content.Open(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(file.Body)
	file.Body.Close()
	if err != nil || string(body) != "The accepted purchased reference" {
		t.Fatal("delivery bytes changed", string(body), err)
	}
	// Mutating current license terms also invalidates publication, but not the
	// already purchased contract. Seller command history is exported privately.
	item, err = catalog.MutateListing(ctx, seller, item.ID, "submit", uuid.NewString(), "test", marketplace.ListingMutation{ExpectedVersion: item.Version, RightsConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	item, err = catalog.MutateListing(ctx, buyer, item.ID, "approve", uuid.NewString(), "test", marketplace.ListingMutation{ExpectedVersion: item.Version, Confirmed: true, Reason: "Revised description and price checked against the file."})
	if err != nil {
		t.Fatal(err)
	}
	newBuyer, _, _, _ := newProductCheckoutFixture(t, pool)
	if _, err = pool.Exec(ctx, `UPDATE licenses SET terms=terms||' New mandatory condition.' WHERE code=$1`, draft.LicenseCode); err != nil {
		t.Fatal(err)
	}
	if _, _, err = payment.BeginProductCheckout(ctx, newBuyer, item.ID, "unreviewed-terms-buy", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, item.ID)); !errors.Is(err, ErrInvalidCheckout) {
		t.Fatal("approval bypass at checkout", err)
	}
	sellerExport, _ := runProductExport(t, pool, seller)
	history := sellerExport.Data.Marketplace.Data["listingHistory"]
	if len(history) != 7 {
		t.Fatal("seller history incomplete", len(history))
	}
	for _, record := range history {
		for _, private := range []string{"actorId", "keySha256", "requestSha256", "storageKey", "storageBackend"} {
			if _, ok := record[private]; ok {
				t.Fatal("private review evidence exposed", private)
			}
		}
	}
	buyerExport, _ := runProductExport(t, pool, buyer)
	if len(buyerExport.Data.Marketplace.Data["listingHistory"]) != 0 {
		t.Fatal("buyer/reviewer exported another seller's private versions")
	}
}
