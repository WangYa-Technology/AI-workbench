package payments

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/jackc/pgx/v5/pgxpool"
)

func rollbackBundleLifecycle(t *testing.T, pool *pgxpool.Pool) func() {
	t.Helper()
	// Older lifecycle migrations replace the shared cleanup policy. Remove
	// and restore its newer financial protections in dependency order too.
	applyRefundObservationMigration(t, pool, "down")
	apply := func(direction string) {
		t.Helper()
		body, err := os.ReadFile("../platform/database/migrations/0114_product_bundle_lifecycle." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(context.Background(), string(body)); err != nil {
			t.Fatal("bundle lifecycle "+direction, err)
		}
	}
	apply("down")
	return func() {
		t.Helper()
		apply("up")
		applyRefundObservationMigration(t, pool, "up")
	}
}

// Internal lifecycle fixture only: publication is tested separately, and no Provider
// call is made. A pending obligation exercises retention without issuing rights.
func (f bundleSnapshotFixture) pendingPayment(t *testing.T, order uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	id := uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key)
 VALUES($1,'stripe','product',$2,$3,$4,$5,1900,'USD','checkout_pending',false,$1::uuid::text)`, id, f.buyer, f.seller, f.product, order); err != nil {
		t.Fatal(err)
	}
	// Persist the original test merchant/request with the pending obligation.
	// Without it, even a cancelled payment correctly retains its delivery for
	// financial review. This fixture performs no external Provider operation.
	identity, err := checkoutIdentity(ctx, &guardedProductRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	if err = saveProductCheckoutRequestTx(ctx, tx, identity, CheckoutRequest{PaymentID: id, ResourceID: f.product, Purpose: "product", AmountCents: 1900, Currency: "USD", BuyerIdentity: f.buyer.String(), BuyerEmail: f.buyer.String() + "@test.local", OrderExternalID: order.String(), Name: "Internal accepted bundle", SuccessURL: "https://example.test/success", CancelURL: "https://example.test/cancel"}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f bundleSnapshotFixture) endPayment(t *testing.T, order uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE payment_intents SET status='cancelled' WHERE order_id=$1`, order); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE orders SET status='cancelled' WHERE id=$1`, order); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func bundleCleanupJob(t *testing.T, f bundleSnapshotFixture, order uuid.UUID) jobs.Job {
	t.Helper()
	var job jobs.Job
	if err := f.pool.QueryRow(context.Background(), `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('orderId',$2::text),20) RETURNING id,payload`, productdelivery.CleanupJobKind, order).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	return job
}

func TestProductBundleRepairReconstructionUploadAndCleanup(t *testing.T) {
	f := newBundleSnapshotFixture(t)
	ctx := context.Background()
	original := f.reserve(t)
	f.pendingPayment(t, original.OrderID)
	if err := productdelivery.Ensure(ctx, f.pool, f.stores, original.OrderID); err != nil {
		t.Fatal(err)
	}
	accepted, err := os.ReadFile(filepath.Join(f.root, original.Key))
	if err != nil {
		t.Fatal(err)
	}
	actor := repairActor(t, f.pool)
	store := &failedDeliveryStore{LocalStore: f.store, fail: true}
	svc := productdelivery.NewRepairService(f.pool, media.NewCatalog(store))
	if err = os.WriteFile(filepath.Join(f.root, original.Key), []byte("damaged ZIP"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Repair(ctx, f.buyer, original.OrderID, "buyer-bundle-repair", "test", repairCommand(0, nil)); !errors.Is(err, productdelivery.ErrRepairForbidden) {
		t.Fatal("unauthorized repair", err)
	}
	if err = os.WriteFile(filepath.Join(f.root, f.second.String()+".txt"), []byte("Changed bundle notes"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Repair(ctx, actor, original.OrderID, "changed-bundle-original", "test", repairCommand(0, nil)); !errors.Is(err, media.ErrIntegrity) {
		t.Fatal("changed member accepted", err)
	}
	var repairs int
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM product_delivery_repairs WHERE order_id=$1`, original.OrderID).Scan(&repairs); err != nil || repairs != 0 {
		t.Fatal("reserved invalid reconstruction", repairs, err)
	}
	if err = os.WriteFile(filepath.Join(f.root, f.second.String()+".txt"), []byte("Private bundle notes"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Repair(ctx, actor, original.OrderID, "resume-bundle-original", "test", repairCommand(0, nil)); err == nil {
		t.Fatal("missing simulated outage")
	}
	status, err := svc.Inspect(ctx, actor, original.OrderID)
	if err != nil || status.PendingID == nil || status.PendingSource != "bundle" {
		t.Fatal("missing bundle recovery reservation", status, err)
	}
	var nullSource bool
	if err = f.pool.QueryRow(ctx, `SELECT source_backend IS NULL AND source_key IS NULL AND source_asset_id IS NULL FROM product_delivery_repairs WHERE id=$1`, status.PendingID).Scan(&nullSource); err != nil || !nullSource {
		t.Fatal("fabricated single repair source", err)
	}
	// Reconstruct from accepted locators even when the current second locator moves.
	if _, err = f.pool.Exec(ctx, `UPDATE assets SET storage_key='moved-current-notes.txt' WHERE id=$1`, f.second); err != nil {
		t.Fatal(err)
	}
	store.fail = false
	status, err = svc.Resume(ctx, actor, original.OrderID, *status.PendingID, "test")
	if err != nil || status.Health != "healthy" || status.Revision != 1 {
		t.Fatal("bundle resume", status, err)
	}
	repaired, err := productdelivery.Load(ctx, f.pool, original.OrderID)
	if err != nil || repaired.Key == original.Key || repaired.SHA256 != original.SHA256 {
		t.Fatal("changed original evidence", err)
	}
	member, err := repaired.OpenFile(ctx, f.stores, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(member.Body)
	_ = member.Body.Close()
	if err != nil || string(body) != "Private bundle notes" {
		t.Fatal("repaired member", string(body), err)
	}
	if _, err = svc.Repair(ctx, actor, original.OrderID, "resume-bundle-original", "test", repairCommand(0, nil)); err != nil {
		t.Fatal("replay", err)
	}
	if err = f.store.Delete(ctx, f.second.String()+".txt"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(f.root, repaired.Key), []byte("damaged again"), 0600); err != nil {
		t.Fatal(err)
	}
	status, err = svc.PrepareUpload(ctx, actor, original.OrderID, "upload-complete-bundle", "test", repairCommand(1, nil))
	if err != nil || status.PendingID == nil || status.PendingSource != "upload" {
		t.Fatal("ZIP upload reservation", status, err)
	}
	uploadID := *status.PendingID
	bad := append([]byte(nil), accepted...)
	bad[len(bad)-1] ^= 1
	if _, err = svc.Upload(ctx, actor, original.OrderID, uploadID, true, "test", bytes.NewReader(bad)); !errors.Is(err, media.ErrIntegrity) {
		t.Fatal("corrupt ZIP upload accepted", err)
	}
	status, err = svc.Upload(ctx, actor, original.OrderID, uploadID, true, "test", bytes.NewReader(accepted))
	if err != nil || status.Health != "healthy" || status.Revision != 2 {
		t.Fatal("exact ZIP recovery", status, err)
	}
	uploaded, err := productdelivery.Load(ctx, f.pool, original.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	if uploaded.SHA256 != original.SHA256 || uploaded.Key == repaired.Key {
		t.Fatal("upload rewrote evidence")
	}
	f.endPayment(t, original.OrderID)
	job := bundleCleanupJob(t, f, original.OrderID)
	if err = productdelivery.CleanupHandler(f.pool, f.stores)(ctx, job); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{original.Key, repaired.Key, uploaded.Key} {
		if _, err = f.store.Stat(ctx, key); !errors.Is(err, media.ErrNotFound) {
			t.Fatal("unremoved ZIP location", key, err)
		}
	}
	removed, err := productdelivery.Load(ctx, f.pool, original.OrderID)
	if err != nil || removed.State != "removed" {
		t.Fatal("missing cleanup state", err)
	}
	if _, err = removed.OpenFile(ctx, f.stores, 1, nil); !errors.Is(err, productdelivery.ErrUnavailable) {
		t.Fatal("removed member readable", err)
	}
}

func TestProductBundlePendingOriginalsSurviveSellerDeletion(t *testing.T) {
	f := newBundleSnapshotFixture(t)
	ctx := context.Background()
	s := f.reserve(t)
	f.pendingPayment(t, s.OrderID)
	deleteMarketplaceAccount(t, f.pool, f.root, f.seller)
	for _, key := range []string{f.first.String() + ".jpg", f.second.String() + ".txt"} {
		if _, err := f.store.Stat(ctx, key); err != nil {
			t.Fatal("pending bundle source removed", key, err)
		}
	}
	var clean int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE id=ANY($1::uuid[]) AND scan_status='clean'`, []uuid.UUID{f.first, f.second}).Scan(&clean); err != nil || clean != 2 {
		t.Fatal("lost member scan eligibility", clean, err)
	}
	if err := productdelivery.Ensure(ctx, f.pool, f.stores, s.OrderID); err != nil {
		t.Fatal("cannot finish accepted bundle", err)
	}
	// A verified independent copy makes the mutable originals unnecessary.
	rights := datarights.NewService(f.pool, f.root)
	if err := rights.HandleMediaCleanupJob(ctx, originalCleanupJob(t, f.pool, f.seller)); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{f.first.String() + ".jpg", f.second.String() + ".txt"} {
		if _, err := f.store.Stat(ctx, key); !errors.Is(err, media.ErrNotFound) {
			t.Fatal("unneeded original retained", key, err)
		}
	}
	ready, err := productdelivery.Load(ctx, f.pool, s.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	member, err := ready.OpenFile(ctx, f.stores, 1, nil)
	if err != nil {
		t.Fatal("retained delivery lost", err)
	}
	_ = member.Body.Close()
}

func TestProductBundleBuyerHoldRetainsEveryHistoricalSource(t *testing.T) {
	f := newBundleSnapshotFixture(t)
	ctx := context.Background()
	s := f.reserve(t)
	f.pendingPayment(t, s.OrderID)
	if err := productdelivery.Ensure(ctx, f.pool, f.stores, s.OrderID); err != nil {
		t.Fatal(err)
	}
	rights := datarights.NewService(f.pool, f.root)
	hold, err := rights.CreateHold(ctx, f.buyer, datarights.HoldInput{UserID: f.buyer, AuthorityReference: "BUNDLE-FROZEN-ORIGINALS"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	newKey := "replacement-notes.txt"
	if err = f.store.Put(ctx, newKey, []byte("Private bundle notes"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE assets SET storage_key=$2 WHERE id=$1`, f.second, newKey); err != nil {
		t.Fatal(err)
	}
	deleteMarketplaceAccount(t, f.pool, f.root, f.seller)
	f.endPayment(t, s.OrderID)
	for _, key := range []string{f.first.String() + ".jpg", f.second.String() + ".txt", s.Key} {
		if _, err = f.store.Stat(ctx, key); err != nil {
			t.Fatal("held historical member removed", key, err)
		}
	}
	job := bundleCleanupJob(t, f, s.OrderID)
	if err = productdelivery.CleanupHandler(f.pool, f.stores)(ctx, job); !errors.Is(err, productdelivery.ErrLegalHold) {
		t.Fatal("held ZIP removed", err)
	}
	if _, err = rights.ReleaseHold(ctx, f.buyer, hold.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = rights.ResumeLegalHoldCleanups(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var accountJob jobs.Job
	if err = f.pool.QueryRow(ctx, `SELECT j.id,j.payload FROM jobs j JOIN legal_hold_cleanup_dispatches d ON d.job_id=j.id WHERE d.hold_id=$1 AND d.kind='account' AND d.user_id=$2`, hold.ID, f.seller).Scan(&accountJob.ID, &accountJob.Payload); err != nil {
		t.Fatal("missing owner cleanup after ended hold", err)
	}
	if err = rights.HandleMediaCleanupJob(ctx, accountJob); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{f.first.String() + ".jpg", f.second.String() + ".txt", newKey, s.Key} {
		if _, err = f.store.Stat(ctx, key); !errors.Is(err, media.ErrNotFound) {
			t.Fatal("ended hold left bundle media", key, err)
		}
	}
	var proofs int
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM original_media_cleanup_receipts WHERE owner_id=$1
 AND storage_key_sha256 IN (encode(public.digest($2::text,'sha256'),'hex'),encode(public.digest($3::text,'sha256'),'hex'),encode(public.digest($4::text,'sha256'),'hex'))`, f.seller, f.first.String()+".jpg", f.second.String()+".txt", newKey).Scan(&proofs); err != nil || proofs != 3 {
		t.Fatal("missing historical member deletion evidence", proofs, err)
	}
}

func TestProductBundleCleanupWaitsForSecondaryOwnerHold(t *testing.T) {
	f := newBundleSnapshotFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s := f.reserve(t)
	f.pendingPayment(t, s.OrderID)
	if err := productdelivery.Ensure(ctx, f.pool, f.stores, s.OrderID); err != nil {
		t.Fatal(err)
	}
	f.endPayment(t, s.OrderID)
	other := repairActor(t, f.pool)
	if _, err := f.pool.Exec(ctx, `UPDATE assets SET owner_id=$2 WHERE id=$1`, f.second, other); err != nil {
		t.Fatal(err)
	}
	gate, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err = gate.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, other); err != nil {
		t.Fatal(err)
	}
	job := bundleCleanupJob(t, f, s.OrderID)
	done := make(chan error, 1)
	go func() { done <- productdelivery.CleanupHandler(f.pool, f.stores)(ctx, job) }()
	waitForProductBlockingTx(t, ctx, f.pool, int32(gate.Conn().PgConn().PID()))
	if _, err = gate.Exec(ctx, `INSERT INTO data_rights_legal_holds(user_id,created_by,reason,authority_reference_hash,review_at,expires_at) VALUES($1,$1,'Retain second source evidence',repeat('b',64),now()+interval '1 day',now()+interval '2 days')`, other); err != nil {
		t.Fatal(err)
	}
	if err = gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, productdelivery.ErrLegalHold) {
		t.Fatal("concurrent secondary hold bypassed", err)
	}
	if _, err = f.store.Stat(ctx, s.Key); err != nil {
		t.Fatal("deleted held bundle", err)
	}
}

func TestProductBundleRepairRechecksPermissionAfterLockWait(t *testing.T) {
	for _, mode := range []string{"prepare", "resume"} {
		t.Run(mode, func(t *testing.T) {
			f := newBundleSnapshotFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			s := f.reserve(t)
			payment := f.pendingPayment(t, s.OrderID)
			if err := productdelivery.Ensure(ctx, f.pool, f.stores, s.OrderID); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(f.root, s.Key), []byte("damaged ZIP"), 0600); err != nil {
				t.Fatal(err)
			}
			actor := repairActor(t, f.pool)
			store := &failedDeliveryStore{LocalStore: f.store, fail: true}
			svc := productdelivery.NewRepairService(f.pool, media.NewCatalog(store))
			run := func() error {
				_, err := svc.Repair(ctx, actor, s.OrderID, "blocked-bundle-repair", "test", repairCommand(0, nil))
				return err
			}
			if mode == "resume" {
				if err := run(); err == nil {
					t.Fatal("missing write outage")
				}
				status, err := svc.Inspect(ctx, actor, s.OrderID)
				if err != nil || status.PendingID == nil {
					t.Fatal(status, err)
				}
				run = func() error { _, err := svc.Resume(ctx, actor, s.OrderID, *status.PendingID, "test"); return err }
			}
			store.fail = false
			gate, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Rollback(context.Background())
			if _, err = gate.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, payment); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- run() }()
			waitForProductBlockingTx(t, ctx, f.pool, int32(gate.Conn().PgConn().PID()))
			if _, err = gate.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor); err != nil {
				t.Fatal(err)
			}
			if err = gate.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-done; !errors.Is(err, productdelivery.ErrRepairForbidden) {
				t.Fatal("revoked permission reused", err)
			}
		})
	}
}

func TestProductBundleRepairLostCommitUsesVerifiedTarget(t *testing.T) {
	f := newBundleSnapshotFixture(t)
	ctx := context.Background()
	s := f.reserve(t)
	f.pendingPayment(t, s.OrderID)
	if err := productdelivery.Ensure(ctx, f.pool, f.stores, s.OrderID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, s.Key), []byte("damaged ZIP"), 0600); err != nil {
		t.Fatal(err)
	}
	actor := repairActor(t, f.pool)
	svc := productdelivery.NewRepairService(f.pool, f.stores)
	if _, err := f.pool.Exec(ctx, `CREATE FUNCTION bundle_repair_lost_commit() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'simulated repair commit lost'; END $$ LANGUAGE plpgsql;
 CREATE TRIGGER bundle_repair_lost_commit BEFORE UPDATE ON product_delivery_repairs FOR EACH ROW EXECUTE FUNCTION bundle_repair_lost_commit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Repair(ctx, actor, s.OrderID, "bundle-repair-lost-commit", "test", repairCommand(0, nil)); err == nil {
		t.Fatal("missing interrupted commit")
	}
	status, err := svc.Inspect(ctx, actor, s.OrderID)
	if err != nil || status.PendingID == nil || status.PendingSource != "bundle" {
		t.Fatal(status, err)
	}
	var key string
	if err = f.pool.QueryRow(ctx, `SELECT storage_key FROM product_delivery_repairs WHERE id=$1`, status.PendingID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Stat(ctx, key); err != nil {
		t.Fatal("no written target before lost commit", err)
	}
	if err = f.store.Delete(ctx, f.second.String()+".txt"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `DROP TRIGGER bundle_repair_lost_commit ON product_delivery_repairs`); err != nil {
		t.Fatal(err)
	}
	status, err = svc.Resume(ctx, actor, s.OrderID, *status.PendingID, "test")
	if err != nil || status.Health != "healthy" {
		t.Fatal("verified target depended on missing original", status, err)
	}
	got, err := productdelivery.Load(ctx, f.pool, s.OrderID)
	if err != nil || got.Key != key || got.SHA256 != s.SHA256 {
		t.Fatal("recovered target changed", err)
	}
}

func TestProductBundleStoredBackupRecoveryAndRollbackGuard(t *testing.T) {
	f := newBundleSnapshotFixture(t)
	ctx := context.Background()
	s := f.reserve(t)
	f.pendingPayment(t, s.OrderID)
	if err := productdelivery.Ensure(ctx, f.pool, f.stores, s.OrderID); err != nil {
		t.Fatal(err)
	}
	accepted, err := os.ReadFile(filepath.Join(f.root, s.Key))
	if err != nil {
		t.Fatal(err)
	}
	actor := repairActor(t, f.pool)
	backup := uuid.New()
	key := backup.String() + ".zip"
	if err = f.store.Put(ctx, key, accepted, productdelivery.BundleMIME); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 VALUES($1,$2,'document','Complete ZIP backup',$3,'application/zip','clean','upload','creator-owned','local_file',$4)`, backup, actor, "/api/v1/assets/"+backup.String()+"/content", key); err != nil {
		t.Fatal(err)
	}
	if err = f.store.Delete(ctx, f.second.String()+".txt"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(f.root, s.Key), []byte("damaged ZIP"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := productdelivery.NewRepairService(f.pool, f.stores)
	status, err := svc.Repair(ctx, actor, s.OrderID, "stored-bundle-backup", "test", repairCommand(0, &backup))
	if err != nil || status.Health != "healthy" {
		t.Fatal("stored ZIP repair", status, err)
	}
	var kind string
	if err = f.pool.QueryRow(ctx, `SELECT source_kind FROM product_delivery_repairs WHERE order_id=$1`, s.OrderID).Scan(&kind); err != nil || kind != "stored" {
		t.Fatal("confused backup with original reconstruction", kind, err)
	}
	down, err := os.ReadFile("../platform/database/migrations/0114_product_bundle_lifecycle.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, string(down)); err == nil {
		t.Fatal("rollback removed bundle retention boundary")
	}
	var n int
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM product_order_media_sources WHERE order_id=$1`, s.OrderID).Scan(&n); err != nil || n != 2 {
		t.Fatal("rollback changed source evidence", n, err)
	}
}

func TestProductBundleRepairModeRejectsSingleSnapshot(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	f := newPurchasedReferenceFixture(t, pool, "video")
	actor := repairActor(t, pool)
	_, err := pool.Exec(ctx, `INSERT INTO product_delivery_repairs(id,order_id,revision,actor_id,key_sha256,request_sha256,
 source_kind,source_backend,source_key,storage_backend,storage_key,reason)
 VALUES($1,$2,1,$3,repeat('a',64),repeat('b',64),'bundle',NULL,NULL,'local_file','invalid-bundle-repair.zip','Reject an incompatible reconstruction format')`, uuid.New(), f.order, actor)
	if err == nil {
		t.Fatal("single snapshot accepted bundle reconstruction")
	}
	var n int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM product_delivery_repairs WHERE order_id=$1`, f.order).Scan(&n); err != nil || n != 0 {
		t.Fatal("invalid repair evidence remained", n, err)
	}
}

func TestProductBundleHistoricalMemberReconciliation(t *testing.T) {
	f := newBundleSnapshotFixture(t)
	ctx := context.Background()
	s := f.reserve(t)
	f.pendingPayment(t, s.OrderID)
	if err := productdelivery.Ensure(ctx, f.pool, f.stores, s.OrderID); err != nil {
		t.Fatal(err)
	}
	rights := datarights.NewService(f.pool, f.root)
	hold, err := rights.CreateHold(ctx, f.buyer, datarights.HoldInput{UserID: f.buyer, AuthorityReference: "BUNDLE-MISSING-SECOND-CLEANUP"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	newKey := "moved-second-original.txt"
	if err = f.store.Put(ctx, newKey, []byte("Private bundle notes"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE assets SET storage_key=$2 WHERE id=$1`, f.second, newKey); err != nil {
		t.Fatal(err)
	}
	deleteMarketplaceAccount(t, f.pool, f.root, f.seller)
	if _, err = rights.ReleaseHold(ctx, f.buyer, hold.ID); err != nil {
		t.Fatal(err)
	}
	// Model known successful partial cleanup without rewriting any immutable
	// evidence. Only the second member's historical location remains unhandled.
	for _, key := range []string{f.first.String() + ".jpg", newKey} {
		info, err := f.store.Stat(ctx, key)
		outcome := "removed"
		var size any = info.Size
		if errors.Is(err, media.ErrNotFound) {
			outcome = "already_absent"
			size = nil
		} else if err != nil {
			t.Fatal(err)
		}
		if err = media.DeleteVerified(ctx, f.store, key); err != nil {
			t.Fatal(err)
		}
		if _, err = f.pool.Exec(ctx, `INSERT INTO original_media_cleanup_receipts(owner_id,initiator_user_id,storage_backend,storage_key_sha256,outcome,size_bytes,request_id)
 SELECT $1,$1,'local_file',encode(public.digest($2::text,'sha256'),'hex'),$3,$4,id FROM data_rights_requests WHERE user_id=$1 AND request_type='account_deletion' AND status='completed'
 ON CONFLICT(owner_id,storage_backend,storage_key_sha256) DO NOTHING`, f.seller, key, outcome, size); err != nil {
			t.Fatal(err)
		}
	}
	var remaining string
	if err = f.pool.QueryRow(ctx, `SELECT l.storage_key FROM original_media_cleanup_locations l WHERE l.owner_id=$1
 AND NOT EXISTS(SELECT 1 FROM original_media_cleanup_receipts r WHERE r.owner_id=l.owner_id AND r.storage_backend=l.storage_backend AND r.storage_key_sha256=encode(public.digest(l.storage_key,'sha256'),'hex'))`, f.seller).Scan(&remaining); err != nil || remaining != f.second.String()+".txt" {
		t.Fatal("historical secondary location not independently discoverable", remaining, err)
	}
	if n, err := rights.ReconcileOriginalMediaCleanups(ctx, 100); err != nil || n != 1 {
		t.Fatal("secondary-only cleanup not reconciled", n, err)
	}
	var job jobs.Job
	if err = f.pool.QueryRow(ctx, `SELECT j.id,j.payload FROM jobs j JOIN original_media_cleanup_reconciliations r ON r.job_id=j.id WHERE r.user_id=$1`, f.seller).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	if err = rights.HandleMediaCleanupJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Stat(ctx, remaining); !errors.Is(err, media.ErrNotFound) {
		t.Fatal("historical member still exists", err)
	}
	var proof bool
	if err = f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM original_media_cleanup_receipts WHERE owner_id=$1 AND storage_key_sha256=encode(public.digest($2::text,'sha256'),'hex') AND job_id=$3)`, f.seller, remaining, job.ID).Scan(&proof); err != nil || !proof {
		t.Fatal("no reconciled member receipt", err)
	}
	if _, err = f.store.Stat(ctx, s.Key); err != nil {
		t.Fatal("reconciliation removed required ZIP", err)
	}
}
