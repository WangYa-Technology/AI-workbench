package payments

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
)

func publishBundle(t *testing.T, f bundleSnapshotFixture) marketplace.SellerProduct {
	t.Helper()
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, f.buyer); err != nil {
		t.Fatal(err)
	}
	catalog := marketplace.NewService(f.pool)
	item, err := catalog.GetListing(ctx, f.seller, f.product, false)
	if err != nil {
		t.Fatal(err)
	}
	item, err = catalog.MutateListing(ctx, f.seller, f.product, "submit", uuid.NewString(), "test", marketplace.ListingMutation{ExpectedVersion: item.Version, RightsConfirmed: true})
	if err != nil {
		t.Fatal("submit bundle", err)
	}
	item, err = catalog.MutateListing(ctx, f.buyer, f.product, "approve", uuid.NewString(), "test", marketplace.ListingMutation{ExpectedVersion: item.Version, Confirmed: true, Reason: "All originals and the complete license were checked."})
	if err != nil {
		t.Fatal("approve bundle", err)
	}
	return item
}

// Uses seller submission and reviewer approval, public product projection, the
// real checkout service, signed receipt processing, refunds and physical cleanup.
// Only the outbound payment provider is simulated; no accepted-order fixture.
func TestProductBundlePublicPurchaseRefundAndCleanup(t *testing.T) {
	f := newBundleSnapshotFixture(t)
	ctx := context.Background()
	item := publishBundle(t, f)
	catalog := marketplace.NewService(f.pool)
	public, err := catalog.GetProduct(ctx, uuid.Nil, f.product)
	if err != nil || public.MediaKind != "document" || public.MediaURL != "" {
		t.Fatal("public bundle", public, err)
	}
	encoded, _ := json.Marshal(public)
	for _, secret := range []string{f.second.String(), "storageKey", "storageBackend", "\"files\""} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("public source leak", secret)
		}
	}
	runtime := &productCheckoutRuntime{}
	svc := newPaymentTestService(t, f.pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(runtime))
	version := productOfferVersion(t, f.pool, f.product)
	checkout, created, err := svc.BeginProductCheckout(ctx, f.buyer, f.product, "public-bundle-purchase", "test", "https://example.test/success", "https://example.test/cancel", true, version)
	if err != nil || !created || runtime.calls != 1 {
		t.Fatal("bundle checkout", checkout, created, err)
	}
	replay, created, err := svc.BeginProductCheckout(ctx, f.buyer, f.product, "public-bundle-purchase", "test", "https://example.test/success", "https://example.test/cancel", true, version)
	if err != nil || created || replay.PaymentID != checkout.PaymentID || runtime.calls != 1 {
		t.Fatal("bundle replay", err)
	}
	var count int
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM entitlements WHERE order_id=$1`, checkout.OrderID).Scan(&count); err != nil || count != 0 {
		t.Fatal("premature rights", count, err)
	}
	snapshot, err := productdelivery.Load(ctx, f.pool, checkout.OrderID)
	if err != nil || snapshot.Format != productdelivery.FormatZIPV1 || snapshot.State != "ready" {
		t.Fatal("snapshot", snapshot, err)
	}
	// Stop selling and edit the second member AFTER acceptance, before payment.
	item, err = catalog.MutateListing(ctx, f.seller, f.product, "pause", uuid.NewString(), "test", marketplace.ListingMutation{ExpectedVersion: item.Version})
	if err != nil {
		t.Fatal(err)
	}
	item.Files[1].Name = "changed-after-acceptance.txt"
	item.IncludedFiles[1] = item.Files[1].Name
	_, err = catalog.MutateListing(ctx, f.seller, f.product, "edit", uuid.NewString(), "test", marketplace.ListingMutation{ExpectedVersion: item.Version, Draft: &item.ProductDraft})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	svc.verifier.now = func() time.Time { return now }
	receipt := receivePaymentWorkflowEvent(t, svc, productPaidEvent(checkout.PaymentID, f.product, now.Unix(), 1900), now)
	paymentJob := jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}
	for range 2 {
		if err = svc.HandlePaymentEventJob(ctx, paymentJob); err != nil {
			t.Fatal("signed bundle fulfillment", err)
		}
	}
	order, err := catalog.GetOrder(ctx, f.buyer, checkout.OrderID)
	if err != nil || order.Status != "fulfilled" || order.AssetID == nil {
		t.Fatal("fulfilled order", order, err)
	}
	assetSvc := assets.NewServiceWithMedia(f.pool, f.stores, nil)
	asset, err := assetSvc.GetOwned(ctx, f.buyer, *order.AssetID)
	if err != nil || asset.Kind != "document" || asset.MimeType != "application/zip" || asset.Provenance.Purchase.CanReuse {
		t.Fatal("package shape", asset, err)
	}
	content, err := assetSvc.Content(ctx, f.buyer, *order.AssetID)
	if err != nil {
		t.Fatal(err)
	}
	object, err := content.Open(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(object.Body)
	object.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil || len(archive.File) != 3 || archive.File[1].Name != "说明.txt" {
		t.Fatal("accepted members changed", err)
	}
	member, err := assetSvc.ContentFile(ctx, f.buyer, *order.AssetID, 1)
	if err != nil {
		t.Fatal(err)
	}
	object, err = member.Open(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(object.Body)
	object.Body.Close()
	if err != nil || string(body) != "Private bundle notes" {
		t.Fatal("member bytes", string(body), err)
	}
	if _, err = svc.BeginProductRefund(ctx, f.buyer, checkout.OrderID, "public-bundle-refund", "test", "The delivered package does not meet my project requirements."); err != nil {
		t.Fatal(err)
	}
	if _, err = assetSvc.ContentFile(ctx, f.buyer, *order.AssetID, 1); err != nil {
		t.Fatal("early revocation", err)
	}
	if err = svc.HandleProductRefundJob(ctx, currentProductRefundJob(t, f.pool, checkout.PaymentID)); err != nil {
		t.Fatal("refund dispatch", err)
	}
	receipt = receivePaymentWorkflowEvent(t, svc, productRefundEvent("evt_bundle_public_refund", "re_workflow123", "succeeded", checkout.PaymentID, f.product, now.Unix(), 1900), now)
	if err = svc.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}); err != nil {
		t.Fatal("signed refund", err)
	}
	if _, err = member.Open(ctx, nil); !errors.Is(err, assets.ErrForbidden) {
		t.Fatal("cached download after refund", err)
	}
	assertProductRefundState(t, f.pool, checkout, "refunded", "refunded", "refunded", 0, 0)
	var cleanupJob jobs.Job
	if err = f.pool.QueryRow(ctx, `SELECT id,kind,payload FROM jobs WHERE kind=$1 AND payload->>'orderId'=$2 AND status='queued' ORDER BY created_at,id LIMIT 1`, productdelivery.CleanupJobKind, checkout.OrderID.String()).Scan(&cleanupJob.ID, &cleanupJob.Kind, &cleanupJob.Payload); err != nil {
		t.Fatal("refund did not dispatch cleanup", err)
	}
	if err = productdelivery.CleanupHandler(f.pool, f.stores)(ctx, cleanupJob); err != nil {
		t.Fatal("bundle cleanup", err)
	}
	snapshot, err = productdelivery.Load(ctx, f.pool, checkout.OrderID)
	if err != nil || snapshot.State != "removed" {
		t.Fatal("cleanup state", snapshot.State, err)
	}
	if _, err = f.store.Stat(ctx, snapshot.Key); !errors.Is(err, media.ErrNotFound) {
		t.Fatal("package still exists", err)
	}
	for _, key := range []string{f.first.String() + ".jpg", f.second.String() + ".txt"} {
		if _, err = f.store.Stat(ctx, key); err != nil {
			t.Fatal("seller original incorrectly removed", err)
		}
	}
}

func TestProductBundlePublicationRechecksAllMembers(t *testing.T) {
	f := newBundleSnapshotFixture(t)
	ctx := context.Background()
	catalog := marketplace.NewService(f.pool)
	if _, err := f.pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, f.buyer); err != nil {
		t.Fatal(err)
	}
	item, err := catalog.GetListing(ctx, f.seller, f.product, false)
	if err != nil {
		t.Fatal(err)
	}
	item, err = catalog.MutateListing(ctx, f.seller, f.product, "submit", uuid.NewString(), "test", marketplace.ListingMutation{ExpectedVersion: item.Version, RightsConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE assets SET scan_status='pending' WHERE id=$1`, f.second); err != nil {
		t.Fatal(err)
	}
	if _, err = catalog.MutateListing(ctx, f.buyer, f.product, "approve", uuid.NewString(), "test", marketplace.ListingMutation{ExpectedVersion: item.Version, Confirmed: true, Reason: "Reviewed all members and their usage rights."}); !errors.Is(err, marketplace.ErrListingConflict) {
		t.Fatal("stale second member approved", err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE assets SET scan_status='clean' WHERE id=$1`, f.second); err != nil {
		t.Fatal(err)
	}
	item, err = catalog.GetListing(ctx, f.seller, f.product, false)
	if err != nil {
		t.Fatal(err)
	}
	item, err = catalog.MutateListing(ctx, f.buyer, f.product, "approve", uuid.NewString(), "test", marketplace.ListingMutation{ExpectedVersion: item.Version, Confirmed: true, Reason: "Reviewed all members and their usage rights."})
	if err != nil {
		t.Fatal(err)
	}
	// Clean is insufficient: a second member's task grant, ownership or license
	// can make it ineligible without changing the first source.
	for _, change := range []string{"scan", "owner", "task_license"} {
		t.Run(change, func(t *testing.T) {
			tx, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			switch change {
			case "scan":
				_, err = tx.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, f.second)
			case "owner":
				_, err = tx.Exec(ctx, `UPDATE assets SET owner_id=$2 WHERE id=$1`, f.second, f.buyer)
			case "task_license":
				_, err = tx.Exec(ctx, `UPDATE assets SET license_code='task-contract' WHERE id=$1`, f.second)
			}
			if err != nil {
				t.Fatal(err)
			}
			var visible bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public_products WHERE id=$1)`, f.product).Scan(&visible); err != nil || visible {
				t.Fatal("secondary source bypass", change, visible, err)
			}
		})
	}
	down, err := os.ReadFile("../platform/database/migrations/0115_product_bundle_publication.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, string(down)); err == nil {
		t.Fatal("rollback erased bundle publication protection")
	}
}
