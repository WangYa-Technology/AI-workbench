package payments

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
)

type failedDeliveryStore struct {
	*media.LocalStore
	fail bool
}

func (s *failedDeliveryStore) PutStream(ctx context.Context, key string, body io.ReadSeeker, size int64, digest, mime string) error {
	if s.fail {
		return errors.New("simulated copy outage")
	}
	return s.LocalStore.PutStream(ctx, key, body, size, digest, mime)
}

func TestProductDeliveryPreparationRecovery(t *testing.T) {
	for _, scenario := range []string{"copy_not_written", "ready_commit_lost"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			buyer, _, source, product := newProductCheckoutFixture(t, pool)
			store := &failedDeliveryStore{LocalStore: media.NewLocalStore(paymentTestRoot(t, pool)), fail: scenario == "copy_not_written"}
			runtime := &guardedProductRuntime{}
			service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, MediaStores: media.NewCatalog(store)}, NewRuntimeCatalog(runtime))
			if scenario == "ready_commit_lost" {
				if _, err := pool.Exec(ctx, `CREATE FUNCTION interrupt_ready_test() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'simulated state commit failure'; END $$ LANGUAGE plpgsql;
				 CREATE TRIGGER interrupt_ready_test BEFORE UPDATE ON product_delivery_snapshots FOR EACH ROW EXECUTE FUNCTION interrupt_ready_test()`); err != nil {
					t.Fatal(err)
				}
			}
			version := productOfferVersion(t, pool, product)
			begin := func() (Checkout, error) {
				c, _, err := service.BeginProductCheckout(ctx, buyer, product, "delivery-recovery-command", "test", "https://example.test/success", "https://example.test/cancel", true, version)
				return c, err
			}
			if _, err := begin(); err == nil || runtime.calls.Load() != 0 {
				t.Fatalf("charged before copy ready: %v calls=%d", err, runtime.calls.Load())
			}
			var checkout Checkout
			if err := pool.QueryRow(ctx, `SELECT id,order_id FROM payment_intents WHERE payer_id=$1`, buyer).Scan(&checkout.PaymentID, &checkout.OrderID); err != nil {
				t.Fatal(err)
			}
			snapshot, err := productdelivery.Load(ctx, pool, checkout.OrderID)
			if err != nil || snapshot.State != "prepared" {
				t.Fatalf("reservation lost: %+v %v", snapshot, err)
			}
			replacePaymentFixtureBytes(t, pool, source, []byte("changed after reservation"))
			store.fail = false
			if scenario == "copy_not_written" {
				if _, err = begin(); !errors.Is(err, ErrCheckoutPreparation) || runtime.calls.Load() != 0 {
					t.Fatalf("copied changed source: %v", err)
				}
				replacePaymentFixtureBytes(t, pool, source, []byte("The accepted purchased reference"))
			} else {
				if _, err = pool.Exec(ctx, `DROP TRIGGER interrupt_ready_test ON product_delivery_snapshots`); err != nil {
					t.Fatal(err)
				}
			}
			recovered, err := begin()
			if err != nil || recovered.PaymentID != checkout.PaymentID || runtime.calls.Load() != 1 {
				t.Fatalf("resume: %+v %v", recovered, err)
			}
			ready, err := productdelivery.Load(ctx, pool, checkout.OrderID)
			if err != nil || ready.State != "ready" || ready.Key != snapshot.Key || ready.SHA256 != snapshot.SHA256 {
				t.Fatalf("evidence replaced: %+v %v", ready, err)
			}
			object, err := ready.Open(ctx, service.config.MediaStores, nil)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(object.Body)
			_ = object.Body.Close()
			if err != nil || string(body) != "The accepted purchased reference" {
				t.Fatalf("wrong copy: %q %v", body, err)
			}
			for _, sql := range []string{
				`UPDATE product_delivery_snapshots SET sha256=repeat('0',64) WHERE order_id=$1`,
				`DELETE FROM product_delivery_snapshots WHERE order_id=$1`,
				`UPDATE orders SET delivery_snapshot_required=false WHERE id=$1`,
			} {
				if _, err = pool.Exec(ctx, sql, checkout.OrderID); err == nil {
					t.Fatalf("immutable evidence changed: %s", sql)
				}
			}
		})
	}
}

func TestPurchasedDeliverySurvivesOriginalLossAndRejectsCorruption(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	fixture := newPurchasedReferenceFixture(t, pool, "video")
	if err := os.Remove(filepath.Join(fixture.root, fixture.source.String()+".jpg")); err != nil {
		t.Fatal(err)
	}
	assertPurchasedBytes(t, pool, fixture.root, fixture.buyer, fixture.purchased, fixture.content)
	runtime := &referenceWorkerRuntime{}
	creator := creation.NewServiceWithRuntimes(pool, fixture.root, creation.NewRuntimeCatalog(runtime))
	generation, err := creator.SubmitCommand(ctx, fixture.buyer, creation.SubmitInput{Mode: "video", Prompt: "Use the independently retained file", SourceAssetIDs: []uuid.UUID{fixture.purchased}}, "independent-reference", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = creator.HandleJob(ctx, generationWorkerJob(t, pool, generation.ID)); err != nil || runtime.calls != 1 {
		t.Fatalf("reference lost with original: %v", err)
	}
	if len(runtime.request.ReferenceAssets) != 1 || string(runtime.request.ReferenceAssets[0].Content) != string(fixture.content) {
		t.Fatal("provider did not receive retained bytes")
	}
	snapshot, err := productdelivery.Load(ctx, pool, fixture.order)
	if err != nil {
		t.Fatal(err)
	}
	// Re-registering the physical copy as a seller upload cannot bypass the
	// shared preview/public-work guard, even if its asset ID is unrelated.
	alias := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
	 SELECT $1,owner_id,kind,'Copy alias',$2,mime_type,'clean','upload',license_code,$3,$4 FROM assets WHERE id=$5`,
		alias, "/api/v1/assets/"+alias.String()+"/content", snapshot.Backend, snapshot.Key, fixture.source); err != nil {
		t.Fatal(err)
	}
	var candidate bool
	if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_preview_candidates WHERE asset_id=$1)`, alias).Scan(&candidate); err != nil || candidate {
		t.Fatalf("delivery copy entered public candidates: %v %v", candidate, err)
	}
	if _, err = assets.NewService(pool, fixture.root).Content(ctx, uuid.Nil, alias); !errors.Is(err, assets.ErrForbidden) {
		t.Fatalf("anonymous copy alias access: %v", err)
	}
	// Same length corruption, outside a subsequently requested byte range.
	corrupt := append([]byte(nil), fixture.content...)
	corrupt[len(corrupt)-1] ^= 1
	if err = os.WriteFile(filepath.Join(fixture.root, snapshot.Key), corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	content, err := assets.NewService(pool, fixture.root).Content(ctx, fixture.buyer, fixture.purchased)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = content.Open(ctx, &media.ByteRange{Start: 0, End: 3}); !errors.Is(err, media.ErrIntegrity) {
		t.Fatalf("corrupt range leaked: %v", err)
	}
	second, err := creator.SubmitCommand(ctx, fixture.buyer, creation.SubmitInput{Mode: "video", Prompt: "Reject corrupt retained file", SourceAssetIDs: []uuid.UUID{fixture.purchased}}, "corrupt-reference", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = creator.HandleJob(ctx, generationWorkerJob(t, pool, second.ID)); err == nil || runtime.calls != 1 {
		t.Fatalf("corrupt copy sent to provider: %v calls=%d", err, runtime.calls)
	}
	if err = os.Remove(filepath.Join(fixture.root, snapshot.Key)); err != nil {
		t.Fatal(err)
	}
	// Restoring the original cannot silently repair or replace a READY copy.
	replacePaymentFixtureBytes(t, pool, fixture.source, fixture.content)
	if _, err = content.Open(ctx, nil); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("fell back to mutable original: %v", err)
	}
	if err = productdelivery.Ensure(ctx, pool, media.NewCatalog(media.NewLocalStore(fixture.root)), fixture.order); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("recreated a lost ready copy: %v", err)
	}
}

func TestProductDeliveryMigrationPreservesEvidence(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	applyBundle := func(direction string) {
		body, err := os.ReadFile("../platform/database/migrations/0113_product_bundle_snapshots." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	restoreBundleLifecycle := rollbackBundleLifecycle(t, pool)
	// Waffo review references the 0091 dispatch table even in an empty schema.
	applyWaffoCheckout := func(direction string) {
		t.Helper()
		body, err := os.ReadFile("../platform/database/migrations/0120_waffo_checkout_dispatch." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	applyWaffoCheckout("down")
	applyBundle("down")
	down, err := os.ReadFile("../platform/database/migrations/0090_product_delivery_snapshots.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../platform/database/migrations/0090_product_delivery_snapshots.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	restoreReconciliation := rollbackCleanupReconciliationForMigrationTest(t, pool)
	// Roll back dependent migrations first; never drop their evidence to bypass protection.
	uploadDown, err := os.ReadFile("../platform/database/migrations/0096_product_delivery_upload.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	uploadUp, err := os.ReadFile("../platform/database/migrations/0096_product_delivery_upload.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(uploadDown)); err != nil {
		t.Fatal(err)
	}

	repairDown, err := os.ReadFile("../platform/database/migrations/0095_product_delivery_repair.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	repairUp, err := os.ReadFile("../platform/database/migrations/0095_product_delivery_repair.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(repairDown)); err != nil {
		t.Fatal(err)
	}
	publicationDown, err := os.ReadFile("../platform/database/migrations/0093_product_publication.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	publicationUp, err := os.ReadFile("../platform/database/migrations/0093_product_publication.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(publicationDown)); err != nil {
		t.Fatal(err)
	}
	policyDown, err := os.ReadFile("../platform/database/migrations/0092_product_delivery_cleanup_recovery.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	policyUp, err := os.ReadFile("../platform/database/migrations/0092_product_delivery_cleanup_recovery.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(policyDown)); err != nil {
		t.Fatal(err)
	}
	nextDown, err := os.ReadFile("../platform/database/migrations/0091_product_checkout_local_closure.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	nextUp, err := os.ReadFile("../platform/database/migrations/0091_product_checkout_local_closure.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(nextDown)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("empty rollback: %v", err)
	}
	if _, err = pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("reapply: %v", err)
	}
	if _, err = pool.Exec(ctx, string(nextUp)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(policyUp)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(publicationUp)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(repairUp)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(uploadUp)); err != nil {
		t.Fatal(err)
	}
	restoreReconciliation()
	applyBundle("up")
	applyWaffoCheckout("up")
	restoreBundleLifecycle()
	fixture := newPurchasedReferenceFixture(t, pool, "video")
	if _, err = pool.Exec(ctx, string(down)); err == nil {
		t.Fatal("rollback discarded delivery evidence")
	}
	assertPurchasedBytes(t, pool, fixture.root, fixture.buyer, fixture.purchased, fixture.content)
}

func TestProductDeliveryDeletionReceiptWaitsForCleanup(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	fixture := newPurchasedReferenceFixture(t, pool, "video")
	snapshot, err := productdelivery.Load(ctx, pool, fixture.order)
	if err != nil {
		t.Fatal(err)
	}
	failing := &failingDeletionStore{Store: media.NewLocalStore(fixture.root), fail: true}
	service := datarights.NewServiceWithMedia(pool, fixture.root, media.NewCatalog(failing))
	request, job := scheduleMarketplaceDeletion(t, pool, fixture.root, fixture.buyer)
	if err = service.HandleDeletionJob(ctx, job); err == nil {
		t.Fatal("copy deletion failure ignored")
	}
	var status string
	var receipts int
	if err = pool.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM data_rights_deletion_receipts WHERE request_id=$1) FROM data_rights_requests WHERE id=$1`, request).Scan(&status, &receipts); err != nil || status != "processing" || receipts != 0 {
		t.Fatalf("premature deletion receipt: %s %d %v", status, receipts, err)
	}
	if _, err = failing.Store.Stat(ctx, snapshot.Key); err != nil {
		t.Fatal(err)
	}
	failing.fail = false
	if err = service.HandleDeletionJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM data_rights_deletion_receipts WHERE request_id=$1) FROM data_rights_requests WHERE id=$1`, request).Scan(&status, &receipts); err != nil || status != "completed" || receipts != 1 {
		t.Fatalf("deletion did not complete: %s %d %v", status, receipts, err)
	}
	if _, err = failing.Store.Stat(ctx, snapshot.Key); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("completed deletion retained copy: %v", err)
	}
	if _, err = failing.Store.Stat(ctx, fixture.source.String()+".jpg"); err != nil {
		t.Fatalf("buyer deletion removed active seller original: %v", err)
	}
}

func TestProductDeliveryCorruptionBeforePaymentCompensates(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	buyer, _, _, product := newProductCheckoutFixture(t, pool)
	runtime := &productCheckoutRuntime{}
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(runtime))
	checkout, _, err := service.BeginProductCheckout(ctx, buyer, product, "corrupt-before-paid", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := productdelivery.Load(ctx, pool, checkout.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(paymentTestRoot(t, pool), snapshot.Key), []byte("corrupted copy"), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	service.verifier.now = func() time.Time { return now }
	r := receivePaymentWorkflowEvent(t, service, productPaidEvent(checkout.PaymentID, product, now.Unix(), 1900), now)
	if err = service.HandlePaymentEventJob(ctx, jobs.Job{Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, r.EventID))}); err != nil {
		t.Fatal(err)
	}
	var state, reason string
	var rights int
	if err = pool.QueryRow(ctx, `SELECT status,compensation_reason,(SELECT count(*) FROM entitlements WHERE order_id=$2) FROM payment_intents WHERE id=$1`, checkout.PaymentID, checkout.OrderID).Scan(&state, &reason, &rights); err != nil || state != "refund_pending" || reason != "source_unavailable" || rights != 0 {
		t.Fatalf("invalid fulfillment: %s %s %d %v", state, reason, rights, err)
	}
}

func TestProductDeliveryCleanupRightsHoldsAndEvidence(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	fixture := newPurchasedReferenceFixture(t, pool, "video")
	stores := media.NewCatalog(media.NewLocalStore(fixture.root))
	snapshot, err := productdelivery.Load(ctx, pool, fixture.order)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = productdelivery.EnqueueCleanupTx(ctx, tx, fixture.order); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var job jobs.Job
	if err = pool.QueryRow(ctx, `SELECT id,payload FROM jobs WHERE kind=$1 AND payload->>'orderId'=$2`, productdelivery.CleanupJobKind, fixture.order.String()).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	// The runner marks a claimed job running before invoking its handler.
	// Leave it running after its no-op retention check, modelling the gap before
	// Complete. A refund in that gap still needs a separate queued cleanup.
	if _, err = pool.Exec(ctx, `UPDATE jobs SET status='running' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	handler := productdelivery.CleanupHandler(pool, stores)
	if err = handler(ctx, job); err != nil {
		t.Fatal(err)
	}
	assertPurchasedBytes(t, pool, fixture.root, fixture.buyer, fixture.purchased, fixture.content)
	// A genuine signed refund schedules cleanup even while the seller is active.
	var product, payment uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT resource_id,id FROM payment_intents WHERE order_id=$1`, fixture.order).Scan(&product, &payment); err != nil {
		t.Fatal(err)
	}
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(&productCheckoutRuntime{}))
	if _, err = service.BeginProductRefund(ctx, fixture.buyer, fixture.order, "delivery-refund-cleanup", "test", "This resource did not meet the stated requirements."); err != nil {
		t.Fatal(err)
	}
	if err = service.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, payment)); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	service.verifier.now = func() time.Time { return now }
	r := receivePaymentWorkflowEvent(t, service, productRefundEvent("evt_copy_refunded", "re_workflow123", "succeeded", payment, product, now.Unix(), 1900), now)
	if err = service.HandlePaymentEventJob(ctx, jobs.Job{Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, r.EventID))}); err != nil {
		t.Fatal(err)
	}
	var queued int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind=$1 AND payload->>'orderId'=$2`, productdelivery.CleanupJobKind, fixture.order.String()).Scan(&queued); err != nil || queued != 2 {
		t.Fatalf("refund did not queue cleanup: %d %v", queued, err)
	}
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(ctx)
	if _, err = gate.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, fixture.buyer); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- handler(ctx, job) }()
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	waitForProductBlockingTx(t, waitCtx, pool, int32(gate.Conn().PgConn().PID()))
	if _, err = gate.Exec(ctx, `INSERT INTO data_rights_legal_holds(user_id,reason,authority_reference_hash,review_at,expires_at,created_by)
	 VALUES($1,'Preserve transaction evidence',$2,now()+interval '1 day',now()+interval '2 days',$1)`, fixture.buyer, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err = gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, productdelivery.ErrLegalHold) {
		t.Fatalf("cleanup bypassed concurrent hold: %v", err)
	}
	if _, err = stores.Primary().Stat(ctx, snapshot.Key); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE data_rights_legal_holds SET status='released',released_by=$1,released_at=now() WHERE user_id=$1`, fixture.buyer); err != nil {
		t.Fatal(err)
	}
	// Physical Delete cannot roll back. A database failure afterwards must
	// retain evidence and allow idempotent cleanup without restoring access.
	if _, err = pool.Exec(ctx, `CREATE FUNCTION interrupt_removed_test() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'simulated removal commit failure'; END $$ LANGUAGE plpgsql;
	 CREATE TRIGGER interrupt_removed_test BEFORE UPDATE ON product_delivery_snapshots FOR EACH ROW WHEN (NEW.state='removed') EXECUTE FUNCTION interrupt_removed_test()`); err != nil {
		t.Fatal(err)
	}
	if err = handler(ctx, job); err == nil {
		t.Fatal("removal commit failure ignored")
	}
	if _, err = stores.Primary().Stat(ctx, snapshot.Key); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("physical removal did not happen: %v", err)
	}
	if interrupted, loadErr := productdelivery.Load(ctx, pool, fixture.order); loadErr != nil || interrupted.State != "ready" {
		t.Fatalf("failed commit changed evidence: %+v %v", interrupted, loadErr)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER interrupt_removed_test ON product_delivery_snapshots`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = handler(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = stores.Primary().Stat(ctx, snapshot.Key); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("copy retained: %v", err)
	}
	if _, err = stores.Primary().Stat(ctx, fixture.source.String()+".jpg"); err != nil {
		t.Fatalf("active seller original deleted: %v", err)
	}
	after, err := productdelivery.Load(ctx, pool, fixture.order)
	if err != nil || after.State != "removed" || after.SHA256 != snapshot.SHA256 {
		t.Fatalf("cleanup erased evidence: %+v %v", after, err)
	}
}
