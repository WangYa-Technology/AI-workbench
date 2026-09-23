package payments

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
)

// These fixtures start at an internally accepted frozen contract. These are not seller-to-payment acceptance or real money tests.
func bundleFulfillmentInput(t *testing.T, f bundleSnapshotFixture) (*Service, productdelivery.Snapshot, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	snapshot := f.reserve(t)
	payment := f.pendingPayment(t, snapshot.OrderID)
	if err := productdelivery.Ensure(ctx, f.pool, f.stores, snapshot.OrderID); err != nil {
		t.Fatal(err)
	}
	runtime := &guardedProductRuntime{}
	svc := newPaymentTestService(t, f.pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(runtime))
	now := time.Now().UTC().Truncate(time.Second)
	svc.verifier.now = func() time.Time { return now }
	receipt := receivePaymentWorkflowEvent(t, svc, productPaidEvent(payment, f.product, now.Unix(), 1900), now)
	return svc, snapshot, payment, receipt.EventID
}

func applyBundleFulfillment(ctx context.Context, f bundleSnapshotFixture, svc *Service, event, payment uuid.UUID) error {
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = svc.fulfillProductPaymentTx(ctx, tx, "stripe", event, payment, 1900, "USD", "pi_workflow123", nil); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE payment_provider_event_processing SET status='processed',processed_at=now() WHERE event_id=$1 AND status='received'`, event); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func TestProductBundleFulfillmentAndAuthorizedDelivery(t *testing.T) {
	f := newBundleSnapshotFixture(t)
	ctx := context.Background()
	svc, snapshot, payment, event := bundleFulfillmentInput(t, f)
	// The independent package must survive moving/removing today's originals.
	for _, key := range []string{f.first.String() + ".jpg", f.second.String() + ".txt"} {
		if err := f.store.Delete(ctx, key); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.pool.Exec(ctx, `UPDATE assets SET storage_key='moved-after-contract.txt' WHERE id=$1`, f.second); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := applyBundleFulfillment(ctx, f, svc, event, payment); err != nil {
			t.Fatal(err)
		}
	}
	var assetID uuid.UUID
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM entitlements WHERE order_id=$1`, snapshot.OrderID).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate entitlement", count, err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT asset_id FROM entitlements WHERE order_id=$1`, snapshot.OrderID).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	assetSvc := assets.NewService(f.pool, f.root)
	item, err := assetSvc.GetOwned(ctx, f.buyer, assetID)
	if err != nil || item.Kind != "document" || item.MimeType != "application/zip" || item.OriginAssetID != nil || item.Width != nil || item.Height != nil || item.SizeBytes == nil || *item.SizeBytes != snapshot.Size {
		t.Fatal("package pretends to be its first member", item, err)
	}
	p := item.Provenance.Purchase
	if p == nil || !p.CanDownload || p.CanReuse || p.Delivery == nil || len(p.Delivery.Files) != 2 || p.Delivery.Files[1].Name != "说明.txt" || p.Delivery.SHA256 != snapshot.SHA256 {
		t.Fatal("wrong buyer projection", p)
	}
	encoded, _ := json.Marshal(p.Delivery)
	for _, private := range []string{"storageKey", "storageBackend", f.first.String(), f.second.String(), snapshot.Key} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("private source evidence exposed", private)
		}
	}
	for _, viewer := range []uuid.UUID{uuid.Nil, f.seller, uuid.New()} {
		if _, err = assetSvc.ContentFile(ctx, viewer, assetID, 1); !errors.Is(err, assets.ErrForbidden) {
			t.Fatal("foreign member download", err)
		}
	}
	for _, index := range []int{-1, 2, 20} {
		if _, err = assetSvc.ContentFile(ctx, f.buyer, assetID, index); !errors.Is(err, assets.ErrInvalid) {
			t.Fatal("invalid index", err)
		}
	}
	member, err := assetSvc.ContentFile(ctx, f.buyer, assetID, 1)
	if err != nil || member.Name != "说明.txt" || member.MimeType != "text/plain" || !member.Attachment {
		t.Fatal("member headers", member, err)
	}
	object, err := member.Open(ctx, &media.ByteRange{Start: 1, End: 6})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(object.Body)
	_ = object.Body.Close()
	if err != nil || string(body) != "rivate" {
		t.Fatal("wrong member bytes", string(body), err)
	}
	creator := creation.NewService(f.pool, f.root, filepath.Join(t.TempDir(), "unused.jpg"), true)
	if _, err = creator.Submit(ctx, f.buyer, creation.SubmitInput{Mode: "chat", Prompt: "Read this package", SourceAssetIDs: []uuid.UUID{assetID}}); !errors.Is(err, creation.ErrInvalid) {
		t.Fatal("ZIP accepted as model context", err)
	}
	// Already-issued handles must not outlive current scans or purchase rights.
	if _, err = f.pool.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, f.second); err != nil {
		t.Fatal(err)
	}
	if _, err = member.Open(ctx, nil); !errors.Is(err, assets.ErrForbidden) {
		t.Fatal("cached handle bypassed secondary scan", err)
	}
	if _, err = assetSvc.Content(ctx, f.buyer, assetID); !errors.Is(err, assets.ErrForbidden) {
		t.Fatal("whole ZIP bypassed secondary scan", err)
	}
	item, err = assetSvc.GetOwned(ctx, f.buyer, assetID)
	if err != nil || item.Provenance.Purchase.CanDownload || item.Provenance.Purchase.CanReuse {
		t.Fatal("unsafe package advertised available", err)
	}
	if err = applyBundleFulfillment(ctx, f, svc, event, payment); err != nil {
		t.Fatal("payment replay depends on later scans", err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE assets SET scan_status='clean' WHERE id=$1`, f.second); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.BeginProductRefund(ctx, f.buyer, snapshot.OrderID, "bundle-refund-command", "test", "The licensed package is not suitable for my project."); err != nil {
		t.Fatal(err)
	}
	// Requesting an ordinary refund keeps rights until its confirmed result.
	if _, err = assetSvc.ContentFile(ctx, f.buyer, assetID, 1); err != nil {
		t.Fatal("refund request prematurely revoked rights", err)
	}
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE payment_intents SET provider_refund_id='re_bundle_confirmed' WHERE id=$1`, payment); err != nil {
		t.Fatal(err)
	}
	if err = refundProductPaymentTx(ctx, tx, "stripe", event, payment, "re_bundle_confirmed", "pi_workflow123", 1900, "USD"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = member.Open(ctx, nil); !errors.Is(err, assets.ErrForbidden) {
		t.Fatal("cached handle bypassed refund", err)
	}
	if _, err = assetSvc.ContentFile(ctx, f.buyer, assetID, 1); !errors.Is(err, assets.ErrForbidden) {
		t.Fatal("refunded member readable", err)
	}
	assertProductRefundState(t, f.pool, Checkout{PaymentID: payment, OrderID: snapshot.OrderID}, "refunded", "refunded", "refunded", 0, 0)
}

type bundleReadHookStore struct {
	media.Store
	afterOpen func(context.Context) error
}

func (s *bundleReadHookStore) Open(ctx context.Context, key string, requested *media.ByteRange) (media.Object, error) {
	object, err := s.Store.Open(ctx, key, requested)
	if err != nil {
		return object, err
	}
	if err = s.afterOpen(ctx); err != nil {
		_ = object.Body.Close()
		return media.Object{}, err
	}
	return object, nil
}

func TestProductBundleDeliveryRechecksRightsDuringRead(t *testing.T) {
	for _, change := range []string{"entitlement", "secondary_scan", "buyer_status"} {
		t.Run(change, func(t *testing.T) {
			f := newBundleSnapshotFixture(t)
			ctx := context.Background()
			svc, snapshot, payment, event := bundleFulfillmentInput(t, f)
			if err := applyBundleFulfillment(ctx, f, svc, event, payment); err != nil {
				t.Fatal(err)
			}
			var assetID uuid.UUID
			if err := f.pool.QueryRow(ctx, `SELECT asset_id FROM entitlements WHERE order_id=$1`, snapshot.OrderID).Scan(&assetID); err != nil {
				t.Fatal(err)
			}
			store := &bundleReadHookStore{Store: f.store, afterOpen: func(ctx context.Context) error {
				var err error
				switch change {
				case "entitlement":
					_, err = f.pool.Exec(ctx, `UPDATE entitlements SET status='revoked',revoked_at=now() WHERE order_id=$1`, snapshot.OrderID)
				case "secondary_scan":
					_, err = f.pool.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, f.second)
				case "buyer_status":
					_, err = f.pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, f.buyer)
				}
				return err
			}}
			assetSvc := assets.NewServiceWithMedia(f.pool, media.NewCatalog(store), nil)
			content, err := assetSvc.ContentFile(ctx, f.buyer, assetID, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = content.Open(ctx, nil); !errors.Is(err, assets.ErrForbidden) {
				t.Fatal("rights changed while staging but bytes returned", err)
			}
		})
	}
}

func TestProductBundleFulfillmentRejectsIncompleteDelivery(t *testing.T) {
	for _, state := range []string{"secondary_rejected", "package_missing", "package_corrupt"} {
		t.Run(state, func(t *testing.T) {
			f := newBundleSnapshotFixture(t)
			ctx := context.Background()
			svc, snapshot, payment, event := bundleFulfillmentInput(t, f)
			switch state {
			case "secondary_rejected":
				if _, err := f.pool.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, f.second); err != nil {
					t.Fatal(err)
				}
			case "package_missing":
				if err := f.store.Delete(ctx, snapshot.Key); err != nil {
					t.Fatal(err)
				}
			case "package_corrupt":
				if err := f.store.Delete(ctx, snapshot.Key); err != nil {
					t.Fatal(err)
				}
				if err := f.store.Put(ctx, snapshot.Key, []byte("Partial package"), "application/zip"); err != nil {
					t.Fatal(err)
				}
			}
			if err := applyBundleFulfillment(ctx, f, svc, event, payment); err != nil {
				t.Fatal(err)
			}
			var count int
			var status, reason string
			if err := f.pool.QueryRow(ctx, `SELECT status,compensation_reason,(SELECT count(*) FROM entitlements WHERE order_id=$2) FROM payment_intents WHERE id=$1`, payment, snapshot.OrderID).Scan(&status, &reason, &count); err != nil || status != "refund_pending" || reason != "source_unavailable" || count != 0 {
				t.Fatal("partial fulfillment granted rights", status, reason, count, err)
			}
		})
	}
}

func TestProductBundleFulfillmentWaitsForAllScans(t *testing.T) {
	f := newBundleSnapshotFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	svc, snapshot, payment, event := bundleFulfillmentInput(t, f)
	gate, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err = gate.Exec(ctx, `SELECT id FROM assets WHERE id=$1 FOR UPDATE`, f.second); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- applyBundleFulfillment(ctx, f, svc, event, payment) }()
	waitForProductBlockingTx(t, ctx, f.pool, int32(gate.Conn().PgConn().PID()))
	if _, err = gate.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, f.second); err != nil {
		t.Fatal(err)
	}
	if err = gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	var pending bool
	if err = f.pool.QueryRow(ctx, `SELECT status='refund_pending' AND compensation_reason='source_unavailable'
 AND NOT EXISTS(SELECT 1 FROM entitlements WHERE order_id=$2) FROM payment_intents WHERE id=$1`, payment, snapshot.OrderID).Scan(&pending); err != nil || !pending {
		t.Fatal("accepted stale scan for later member", pending, err)
	}
}
