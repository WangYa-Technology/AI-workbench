package payments

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/generationoutput"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
)

func TestGeneratedProductOutputJournalPreservesBuyerDelivery(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	buyer, seller, _, product := newProductCheckoutFixture(t, pool)
	root := paymentTestRoot(t, pool)
	content := []byte("Generated source accepted under a marketplace license")
	source := filepath.Join(t.TempDir(), "provider.jpg")
	if err := os.WriteFile(source, content, 0600); err != nil {
		t.Fatal(err)
	}
	creator := creation.NewService(pool, root, source, true)
	g, err := creator.SubmitCommand(ctx, seller, creation.SubmitInput{Mode: "image", Prompt: "Original market generation"}, "generated-product-source", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = creator.HandleJob(ctx, generationWorkerJob(t, pool, g.ID)); err != nil {
		t.Fatal(err)
	}
	g, err = creator.Get(ctx, seller, g.ID)
	if err != nil || g.OutputAssetID == nil {
		t.Fatal(g, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE products SET asset_id=$2 WHERE id=$1`, product, *g.OutputAssetID); err != nil {
		t.Fatal(err)
	}
	payment := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(&productCheckoutRuntime{}))
	checkout, _, err := payment.BeginProductCheckout(ctx, buyer, product, "journal-product-checkout", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	payment.verifier.now = func() time.Time { return now }
	receipt := receivePaymentWorkflowEvent(t, payment, productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents), now)
	if err = payment.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}); err != nil {
		t.Fatal(err)
	}
	var purchased uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT asset_id FROM entitlements WHERE order_id=$1 AND user_id=$2`, checkout.OrderID, buyer).Scan(&purchased); err != nil {
		t.Fatal(err)
	}
	assertPurchasedBytes(t, pool, root, buyer, purchased, content)
	cleanupService := generationoutput.NewService(pool, media.NewCatalog(media.NewLocalStore(root)))
	if n, err := cleanupService.Reconcile(ctx, 100); err != nil || n != 0 {
		t.Fatalf("formal source was swept: %d %v", n, err)
	}
	deleteMarketplaceAccount(t, pool, root, seller)
	assertPurchasedBytes(t, pool, root, buyer, purchased, content)
	if n, err := cleanupService.Reconcile(ctx, 100); err != nil || n != 0 {
		t.Fatalf("seller deletion exposed committed source to orphan sweep: %d %v", n, err)
	}
	var attached int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM generation_output_writes WHERE generation_id=$1 AND status='attached'`, g.ID).Scan(&attached); err != nil || attached != 1 {
		t.Fatal("ownership evidence changed", attached, err)
	}
}
