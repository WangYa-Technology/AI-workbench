package payments

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
)

func TestProductBundleCompletedPurchaseSellerDeletionAndBuyerHold(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("buyer_hold_%t", held), func(t *testing.T) {
			f := newBundleSnapshotFixture(t)
			ctx := context.Background()
			publishBundle(t, f)
			runtime := &productCheckoutRuntime{}
			svc := newPaymentTestService(t, f.pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(runtime))
			checkout, _, err := svc.BeginProductCheckout(ctx, f.buyer, f.product, "retention-public-bundle", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, f.pool, f.product))
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			svc.verifier.now = func() time.Time { return now }
			event := receivePaymentWorkflowEvent(t, svc, productPaidEvent(checkout.PaymentID, f.product, now.Unix(), 1900), now)
			if err = svc.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, event.EventID.String()))}); err != nil {
				t.Fatal(err)
			}
			snapshot, err := productdelivery.Load(ctx, f.pool, checkout.OrderID)
			if err != nil {
				t.Fatal(err)
			}
			var purchased assets.Asset
			assetSvc := assets.NewServiceWithMedia(f.pool, f.stores, nil)
			var assetID string
			if err = f.pool.QueryRow(ctx, `SELECT asset_id FROM entitlements WHERE order_id=$1`, checkout.OrderID).Scan(&assetID); err != nil {
				t.Fatal(err)
			}
			// Retention must be exercised with actual fulfilled rights, not a seeded
			// pending contract. Select the asset through the public service projection.
			page, err := assetSvc.List(ctx, f.buyer, assets.ListInput{Source: "purchase", Limit: 20})
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range page.Items {
				if item.ID.String() == assetID {
					purchased = item
				}
			}
			if purchased.ID.String() != assetID {
				t.Fatal("missing purchased bundle")
			}
			rights := datarights.NewService(f.pool, f.root)
			var release func()
			if held {
				hold, err := rights.CreateHold(ctx, f.buyer, datarights.HoldInput{UserID: f.buyer, AuthorityReference: "COMPLETED-BUNDLE-RETENTION"}, "test")
				if err != nil {
					t.Fatal(err)
				}
				release = func() {
					if _, err := rights.ReleaseHold(ctx, f.buyer, hold.ID); err != nil {
						t.Fatal(err)
					}
					if _, err := rights.ResumeLegalHoldCleanups(ctx, 100); err != nil {
						t.Fatal(err)
					}
					var cleanup jobs.Job
					if err := f.pool.QueryRow(ctx, `SELECT j.id,j.payload FROM jobs j JOIN legal_hold_cleanup_dispatches d ON d.job_id=j.id WHERE d.hold_id=$1 AND d.kind='account' AND d.user_id=$2`, hold.ID, f.seller).Scan(&cleanup.ID, &cleanup.Payload); err != nil {
						t.Fatal("ended hold lost seller cleanup", err)
					}
					if err := rights.HandleMediaCleanupJob(ctx, cleanup); err != nil {
						t.Fatal(err)
					}
				}
			}
			deleteMarketplaceAccount(t, f.pool, f.root, f.seller)
			for _, key := range []string{f.first.String() + ".jpg", f.second.String() + ".txt"} {
				_, err = f.store.Stat(ctx, key)
				if held && err != nil {
					t.Fatal("held original deleted", key, err)
				}
				if !held && !errors.Is(err, media.ErrNotFound) {
					t.Fatal("unneeded original retained", key, err)
				}
			}
			member, err := assetSvc.ContentFile(ctx, f.buyer, purchased.ID, 1)
			if err != nil {
				t.Fatal("seller deletion revoked buyer", err)
			}
			object, err := member.Open(ctx, nil)
			if err != nil {
				t.Fatal("retained package unusable", err)
			}
			body, err := io.ReadAll(object.Body)
			object.Body.Close()
			if err != nil || string(body) != "Private bundle notes" {
				t.Fatal("retained bytes", err)
			}
			if _, err = svc.BeginProductRefund(ctx, f.buyer, checkout.OrderID, "retained-bundle-refund", "test", "The licensed package is not suitable for my project."); err != nil {
				t.Fatal("refund after seller deletion", err)
			}
			if err = svc.HandleProductRefundJob(ctx, currentProductRefundJob(t, f.pool, checkout.PaymentID)); err != nil {
				t.Fatal(err)
			}
			event = receivePaymentWorkflowEvent(t, svc, productRefundEvent("evt_retained_bundle_refund", "re_workflow123", "succeeded", checkout.PaymentID, f.product, now.Unix(), 1900), now)
			if err = svc.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, event.EventID.String()))}); err != nil {
				t.Fatal(err)
			}
			if _, err = member.Open(ctx, nil); !errors.Is(err, assets.ErrForbidden) {
				t.Fatal("refund did not revoke cached reader", err)
			}
			var cleanup jobs.Job
			if err = f.pool.QueryRow(ctx, `SELECT id,payload FROM jobs WHERE kind=$1 AND payload->>'orderId'=$2 AND status='queued' ORDER BY created_at,id LIMIT 1`, productdelivery.CleanupJobKind, checkout.OrderID.String()).Scan(&cleanup.ID, &cleanup.Payload); err != nil {
				t.Fatal("refund cleanup missing", err)
			}
			err = productdelivery.CleanupHandler(f.pool, f.stores)(ctx, cleanup)
			if held {
				if !errors.Is(err, productdelivery.ErrLegalHold) {
					t.Fatal("refund deleted held delivery", err)
				}
				if _, err = f.store.Stat(ctx, snapshot.Key); err != nil {
					t.Fatal(err)
				}
				release()
				err = productdelivery.CleanupHandler(f.pool, f.stores)(ctx, cleanup)
			}
			if err != nil {
				t.Fatal("completed cleanup", err)
			}
			for _, key := range []string{f.first.String() + ".jpg", f.second.String() + ".txt", snapshot.Key} {
				if _, err = f.store.Stat(ctx, key); !errors.Is(err, media.ErrNotFound) {
					t.Fatal("retained file survived completed obligation", key, err)
				}
			}
			var receipts int
			if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM original_media_cleanup_receipts WHERE owner_id=$1 AND storage_key_sha256 IN (encode(public.digest($2::text,'sha256'),'hex'),encode(public.digest($3::text,'sha256'),'hex'))`, f.seller, f.first.String()+".jpg", f.second.String()+".txt").Scan(&receipts); err != nil || receipts != 2 {
				t.Fatal("missing per-member original cleanup proof", receipts, err)
			}
		})
	}
}
