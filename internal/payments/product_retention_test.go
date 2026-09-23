package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func scheduleMarketplaceDeletion(t *testing.T, pool *pgxpool.Pool, root string, actor uuid.UUID) (uuid.UUID, jobs.Job) {
	t.Helper()
	ctx := context.Background()
	token := uuid.NewString()
	var handle string
	if err := pool.QueryRow(ctx, `SELECT handle FROM users WHERE id=$1`, actor).Scan(&handle); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sessions(user_id,token_hash,expires_at) VALUES($1,$2,now()+interval '1 day')`, actor, identity.HashToken(token)); err != nil {
		t.Fatal(err)
	}
	rights := datarights.NewService(pool, root)
	request, err := rights.Create(ctx, actor, token, datarights.CreateInput{RequestType: "account_deletion", IdentityConfirmation: handle}, "product-retention-deletion")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET execute_after=now()-interval '1 minute',cancel_until=now()-interval '1 minute' WHERE id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"requestId": request.ID})
	return request.ID, jobs.Job{Payload: payload}
}

func deleteMarketplaceAccount(t *testing.T, pool *pgxpool.Pool, root string, actor uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	requestID, job := scheduleMarketplaceDeletion(t, pool, root, actor)
	rights := datarights.NewService(pool, root)
	for range 2 {
		if err := rights.HandleDeletionJob(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	var receipt []byte
	if err := pool.QueryRow(ctx, `SELECT receipt FROM data_rights_deletion_receipts WHERE request_id=$1`, requestID).Scan(&receipt); err != nil || !strings.Contains(string(receipt), "erased_except_required_task_and_product_contracts") {
		t.Fatalf("missing accurate media retention receipt: %s, %v", receipt, err)
	}
}

func TestLegacyProductMediaRetainedAfterSellerDeletion(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	buyer, seller, source, product := newProductCheckoutFixture(t, pool)
	root := paymentTestRoot(t, pool)
	content := []byte("Legacy purchased content")
	replacePaymentFixtureBytes(t, pool, source, content)
	purchase := testutil.SeedLegacyProductOrder(t, pool, buyer, product)
	deleteMarketplaceAccount(t, pool, root, seller)
	assertPurchasedBytes(t, pool, root, buyer, purchase.AssetID, content)
	deleteMarketplaceAccount(t, pool, root, buyer)
	if _, err := media.NewLocalStore(root).Stat(ctx, source.String()+".jpg"); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("legacy source not cleaned after final buyer deletion: %v", err)
	}
}

type failingDeletionStore struct {
	media.Store
	fail bool
}

func (s *failingDeletionStore) Delete(ctx context.Context, key string) error {
	if s.fail {
		return errors.New("injected storage deletion failure")
	}
	return s.Store.Delete(ctx, key)
}

func waitForProductBlockingTx(t *testing.T, ctx context.Context, pool *pgxpool.Pool, blocker int32) int32 {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var pid int32
		err := pool.QueryRow(ctx, `SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) LIMIT 1`, blocker).Scan(&pid)
		if err == nil {
			return pid
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("expected transaction lock was not observed")
		case <-ticker.C:
		}
	}
}

func TestRefundConcurrentWithSellerDeletionSchedulesCleanup(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture := newPurchasedReferenceFixture(t, pool, "video")
	var seller, product, paymentID uuid.UUID
	var amount int
	if err := pool.QueryRow(ctx, `SELECT pi.payee_id,o.product_id,pi.id,pi.amount_cents
		FROM payment_intents pi JOIN orders o ON o.id=pi.order_id WHERE o.id=$1`, fixture.order).Scan(&seller, &product, &paymentID, &amount); err != nil {
		t.Fatal(err)
	}
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(&productCheckoutRuntime{}))
	if _, err := service.BeginProductRefund(ctx, fixture.buyer, fixture.order, "concurrent-retention-refund", "test", "The resource did not meet the stated requirements."); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, paymentID)); err != nil {
		t.Fatal(err)
	}
	_, deletion := scheduleMarketplaceDeletion(t, pool, fixture.root, seller)
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Rollback(context.Background()) }()
	lockID := time.Now().UnixNano()
	if _, err := gate.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE FUNCTION pause_seller_deletion_test() RETURNS trigger AS $$
		BEGIN PERFORM pg_advisory_xact_lock(%d::bigint); RETURN NEW; END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER pause_seller_deletion_test BEFORE UPDATE OF status ON users
		FOR EACH ROW WHEN (NEW.status='deleted') EXECUTE FUNCTION pause_seller_deletion_test()`, lockID)); err != nil {
		t.Fatal(err)
	}
	deletionDone := make(chan error, 1)
	go func() { deletionDone <- datarights.NewService(pool, fixture.root).HandleDeletionJob(ctx, deletion) }()
	deletionPID := waitForProductBlockingTx(t, ctx, pool, int32(gate.Conn().PgConn().PID()))
	now := time.Now().UTC().Truncate(time.Second)
	service.verifier.now = func() time.Time { return now }
	receipt := receivePaymentWorkflowEvent(t, service, productRefundEvent("evt_concurrent_refund", "re_workflow123", "succeeded", paymentID, product, now.Unix(), amount), now)
	refundJob := jobs.Job{Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID))}
	refundDone := make(chan error, 1)
	go func() {
		refundDone <- service.HandlePaymentEventJob(ctx, refundJob)
	}()
	waitForProductBlockingTx(t, ctx, pool, deletionPID)
	if err := gate.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	refundErr, deletionErr := <-refundDone, <-deletionDone
	if deletionErr != nil {
		t.Fatal(deletionErr)
	}
	// The serializable refund snapshot predates deletion; the worker must replay
	// the rolled-back event against the committed account state.
	var serializationErr *pgconn.PgError
	if !errors.As(refundErr, &serializationErr) || serializationErr.Code != "40001" || !jobs.ShouldRetry(refundErr) {
		t.Fatalf("expected retryable serialization failure, got %v", refundErr)
	}
	var entitlementStatus string
	var queuedCleanups int
	if err := pool.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM jobs WHERE kind=$2)
		FROM entitlements WHERE order_id=$1`, fixture.order, datarights.MediaCleanupJobKind).Scan(&entitlementStatus, &queuedCleanups); err != nil {
		t.Fatal(err)
	}
	if entitlementStatus != "active" || queuedCleanups != 0 {
		t.Fatalf("rolled-back refund changed entitlement or queued cleanup: %s %d", entitlementStatus, queuedCleanups)
	}
	for range 2 {
		if err := service.HandlePaymentEventJob(ctx, refundJob); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM jobs WHERE kind=$2)
		FROM entitlements WHERE order_id=$1`, fixture.order, datarights.MediaCleanupJobKind).Scan(&entitlementStatus, &queuedCleanups); err != nil {
		t.Fatal(err)
	}
	if entitlementStatus != "refunded" || queuedCleanups != 1 {
		t.Fatalf("refund replay did not revoke and enqueue exactly once: %s %d", entitlementStatus, queuedCleanups)
	}
	var cleanupJob jobs.Job
	if err := pool.QueryRow(ctx, `SELECT id,payload FROM jobs WHERE kind=$1`, datarights.MediaCleanupJobKind).Scan(&cleanupJob.ID, &cleanupJob.Payload); err != nil {
		t.Fatalf("concurrent refund lost cleanup obligation: %v", err)
	}
	if err := datarights.NewService(pool, fixture.root).HandleMediaCleanupJob(ctx, cleanupJob); err != nil {
		t.Fatal(err)
	}
	if _, err := media.NewLocalStore(fixture.root).Stat(ctx, fixture.source.String()+".jpg"); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("concurrent refund retained unneeded file: %v", err)
	}
}

func TestAccountDeletionResumesWithoutRestoringErasedMediaAccess(t *testing.T) {
	for _, scenario := range []string{"prepare_rollback", "storage_failure", "receipt_rollback"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			_, seller, source, _ := newProductCheckoutFixture(t, pool)
			root := paymentTestRoot(t, pool)
			store := &failingDeletionStore{Store: media.NewLocalStore(root), fail: scenario == "storage_failure"}
			key := source.String() + ".jpg"
			replacePaymentFixtureBytes(t, pool, source, []byte("Private seller file"))
			requestID, job := scheduleMarketplaceDeletion(t, pool, root, seller)
			service := datarights.NewServiceWithMedia(pool, root, media.NewCatalog(store))
			if scenario != "storage_failure" {
				if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_deletion_test() RETURNS trigger AS $$
					BEGIN RAISE EXCEPTION 'injected deletion failure'; END;
					$$ LANGUAGE plpgsql`); err != nil {
					t.Fatal(err)
				}
				trigger := `CREATE TRIGGER reject_deletion_test BEFORE UPDATE OF status ON users
					FOR EACH ROW WHEN (NEW.status='deleted') EXECUTE FUNCTION reject_deletion_test()`
				if scenario == "receipt_rollback" {
					trigger = `CREATE TRIGGER reject_deletion_test BEFORE INSERT ON data_rights_deletion_receipts
						FOR EACH ROW EXECUTE FUNCTION reject_deletion_test()`
				}
				if _, err := pool.Exec(ctx, trigger); err != nil {
					t.Fatal(err)
				}
			}
			if err := service.HandleDeletionJob(ctx, job); err == nil {
				t.Fatal("injected failure was ignored")
			}
			var userStatus, requestStatus string
			var receipts int
			if err := pool.QueryRow(ctx, `SELECT u.status,r.status,(SELECT count(*) FROM data_rights_deletion_receipts WHERE request_id=r.id)
				FROM users u JOIN data_rights_requests r ON r.user_id=u.id WHERE r.id=$1`, requestID).Scan(&userStatus, &requestStatus, &receipts); err != nil {
				t.Fatal(err)
			}
			if receipts != 0 {
				t.Fatal("failed cleanup claimed completed deletion")
			}
			_, statErr := store.Stat(ctx, key)
			if scenario == "prepare_rollback" {
				if userStatus != "active" || requestStatus != "scheduled" || statErr != nil {
					t.Fatalf("preparation rollback lost original state or bytes: %s %s %v", userStatus, requestStatus, statErr)
				}
			} else {
				if userStatus != "deleted" || requestStatus != "processing" {
					t.Fatalf("cleanup rollback restored account access: %s %s", userStatus, requestStatus)
				}
				if scenario == "storage_failure" && statErr != nil || scenario == "receipt_rollback" && !errors.Is(statErr, media.ErrNotFound) {
					t.Fatalf("unexpected physical cleanup result: %v", statErr)
				}
				if _, err := assets.NewService(pool, root).Content(ctx, seller, source); err == nil {
					t.Fatal("failed finalization reopened source content")
				}
			}
			store.fail = false
			if scenario != "storage_failure" {
				if _, err := pool.Exec(ctx, `DROP FUNCTION reject_deletion_test() CASCADE`); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := service.HandleDeletionJob(ctx, job); err != nil {
					t.Fatal(err)
				}
			}
			if err := pool.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM data_rights_deletion_receipts WHERE request_id=$1)
				FROM data_rights_requests WHERE id=$1`, requestID).Scan(&requestStatus, &receipts); err != nil || requestStatus != "completed" || receipts != 1 {
				t.Fatalf("retry did not complete exactly once: %s %d %v", requestStatus, receipts, err)
			}
			if _, err := store.Stat(ctx, key); !errors.Is(err, media.ErrNotFound) {
				t.Fatalf("retry did not erase file: %v", err)
			}
		})
	}
}

func TestRecoveredSellerDeletionRetainsPurchasedDelivery(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	fixture := newPurchasedReferenceFixture(t, pool, "video")
	var seller uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT owner_id FROM assets WHERE id=$1`, fixture.source).Scan(&seller); err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Recovery operator','admin')`, actor, actor.String()+"@test.local", "recovery_"+actor.String()[:8]); err != nil {
		t.Fatal(err)
	}
	request, _ := scheduleMarketplaceDeletion(t, pool, fixture.root, seller)
	var originalID uuid.UUID
	if err := pool.QueryRow(ctx, `UPDATE jobs SET available_at=now()-interval '1 day',max_attempts=1
 WHERE kind=$1 AND payload->>'requestId'=$2 RETURNING id`, datarights.DeletionJobKind, request.String()).Scan(&originalID); err != nil {
		t.Fatal(err)
	}
	repository := jobs.NewRepository(pool)
	original, err := repository.Claim(ctx, "deletion-retention-test", time.Minute)
	if err != nil || original.ID != originalID {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_retention_receipt_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected receipt failure'; END $$;
 CREATE TRIGGER fail_retention_receipt_test BEFORE INSERT ON data_rights_deletion_receipts FOR EACH ROW EXECUTE FUNCTION fail_retention_receipt_test()`); err != nil {
		t.Fatal(err)
	}
	rights := datarights.NewService(pool, fixture.root)
	failure := rights.HandleDeletionJob(ctx, original)
	if failure == nil {
		t.Fatal("expected receipt failure")
	}
	if err := repository.Fail(ctx, original, "deletion-retention-test", failure); err != nil {
		t.Fatal(err)
	}
	assertPurchasedBytes(t, pool, fixture.root, fixture.buyer, fixture.purchased, fixture.content)
	if _, err := pool.Exec(ctx, `DROP FUNCTION fail_retention_receipt_test() CASCADE`); err != nil {
		t.Fatal(err)
	}
	attempts := 1
	replacement, err := rights.RetryDeletionJob(ctx, actor, original.ID, datarights.MediaCleanupRetryInput{ExpectedAttempts: &attempts, Confirmed: true, Reason: "Receipt storage was inspected and repaired."}, "retention-recovery")
	if err != nil || replacement.Kind != "cleanup" {
		t.Fatal(replacement, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET available_at=now()-interval '1 day' WHERE id=$1`, replacement.ID); err != nil {
		t.Fatal(err)
	}
	retry, err := repository.Claim(ctx, "deletion-retention-retry", time.Minute)
	if err != nil || retry.ID != replacement.ID {
		t.Fatal("replacement lease", retry, err)
	}
	if err := rights.HandleDeletionJob(ctx, retry); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(ctx, retry, "deletion-retention-retry"); err != nil {
		t.Fatal(err)
	}
	assertPurchasedBytes(t, pool, fixture.root, fixture.buyer, fixture.purchased, fixture.content)
	if _, err := assets.NewService(pool, fixture.root).Content(ctx, uuid.Nil, fixture.purchased); !errors.Is(err, assets.ErrForbidden) {
		t.Fatal("recovery exposed purchased media", err)
	}
	var status, userStatus string
	var receipts int
	if err := pool.QueryRow(ctx, `SELECT r.status,u.status,(SELECT count(*) FROM data_rights_deletion_receipts WHERE request_id=r.id)
 FROM data_rights_requests r JOIN users u ON u.id=r.user_id WHERE r.id=$1`, request).Scan(&status, &userStatus, &receipts); err != nil || status != "completed" || userStatus != "deleted" || receipts != 1 {
		t.Fatal("recovery failed retention contract", status, userStatus, receipts, err)
	}
}

func TestRefundCleansDeletedSellerMediaOnlyAfterConfirmation(t *testing.T) {
	for _, deletedSeller := range []bool{false, true} {
		t.Run(fmt.Sprintf("deleted_seller_%t", deletedSeller), func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			fixture := newPurchasedReferenceFixture(t, pool, "video")
			var seller, product, paymentID uuid.UUID
			var amount int
			if err := pool.QueryRow(ctx, `SELECT pi.payee_id,o.product_id,pi.id,pi.amount_cents
				FROM payment_intents pi JOIN orders o ON o.id=pi.order_id WHERE o.id=$1`, fixture.order).Scan(&seller, &product, &paymentID, &amount); err != nil {
				t.Fatal(err)
			}
			service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(&productCheckoutRuntime{}))
			if _, err := service.BeginProductRefund(ctx, fixture.buyer, fixture.order, "retention-refund-key", "test", "The purchased resource does not match my requirements."); err != nil {
				t.Fatal(err)
			}
			if err := service.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, paymentID)); err != nil {
				t.Fatal(err)
			}
			if deletedSeller {
				deleteMarketplaceAccount(t, pool, fixture.root, seller)
			}
			// A pending refund retains the active entitlement and original bytes.
			assertPurchasedBytes(t, pool, fixture.root, fixture.buyer, fixture.purchased, fixture.content)
			now := time.Now().UTC().Truncate(time.Second)
			service.verifier.now = func() time.Time { return now }
			receipt := receivePaymentWorkflowEvent(t, service, productRefundEvent("evt_retention_refunded", "re_workflow123", "succeeded", paymentID, product, now.Unix(), amount), now)
			paymentJob := jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID))}
			for range 2 {
				if err := service.HandlePaymentEventJob(ctx, paymentJob); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := assets.NewService(pool, fixture.root).Content(ctx, fixture.buyer, fixture.purchased); err == nil {
				t.Fatal("refund retained buyer access")
			}
			rows, err := pool.Query(ctx, `SELECT id,payload FROM jobs WHERE kind=$1`, datarights.MediaCleanupJobKind)
			if err != nil {
				t.Fatal(err)
			}
			var cleanupJobs []jobs.Job
			for rows.Next() {
				var job jobs.Job
				if err := rows.Scan(&job.ID, &job.Payload); err != nil {
					t.Fatal(err)
				}
				cleanupJobs = append(cleanupJobs, job)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if deletedSeller && len(cleanupJobs) != 1 || !deletedSeller && len(cleanupJobs) != 0 {
				t.Fatalf("incorrect durable cleanup count: %d", len(cleanupJobs))
			}
			for _, job := range cleanupJobs {
				for range 2 {
					if err := datarights.NewService(pool, fixture.root).HandleMediaCleanupJob(ctx, job); err != nil {
						t.Fatal(err)
					}
				}
			}
			_, err = media.NewLocalStore(fixture.root).Stat(ctx, fixture.source.String()+".jpg")
			if deletedSeller && !errors.Is(err, media.ErrNotFound) || !deletedSeller && err != nil {
				t.Fatalf("incorrect final file state: %v", err)
			}
		})
	}
}

func TestProductDeletionWaitsForUncommittedCheckout(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	buyer, seller, source, product := newProductCheckoutFixture(t, pool)
	root := paymentTestRoot(t, pool)
	replacePaymentFixtureBytes(t, pool, source, []byte("Pending contract bytes"))
	_, deletion := scheduleMarketplaceDeletion(t, pool, root, seller)
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Rollback(context.Background()) }()
	lockID := time.Now().UnixNano()
	if _, err := gate.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE FUNCTION pause_product_contract_test() RETURNS trigger AS $$
	BEGIN PERFORM pg_advisory_xact_lock(%d::bigint); RETURN NEW; END;
	$$ LANGUAGE plpgsql;
	CREATE TRIGGER pause_product_contract_test BEFORE INSERT ON product_order_contracts
	FOR EACH ROW EXECUTE FUNCTION pause_product_contract_test()`, lockID)); err != nil {
		t.Fatal(err)
	}
	waitForBlocked := func(blocker int32) int32 {
		t.Helper()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			var pid int32
			err := pool.QueryRow(ctx, `SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) LIMIT 1`, blocker).Scan(&pid)
			if err == nil {
				return pid
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				t.Fatal(err)
			}
			select {
			case <-ctx.Done():
				t.Fatal("expected transaction lock was not observed")
			case <-ticker.C:
			}
		}
	}
	version := productOfferVersion(t, pool, product)
	checkoutDone := make(chan error, 1)
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(&guardedProductRuntime{}))
	go func() {
		_, _, err := service.BeginProductCheckout(ctx, buyer, product, "checkout-deletion-race", "test", "https://example.test/success", "https://example.test/cancel", true, version)
		checkoutDone <- err
	}()
	checkoutPID := waitForBlocked(int32(gate.Conn().PgConn().PID()))
	deletionDone := make(chan error, 1)
	go func() { deletionDone <- datarights.NewService(pool, root).HandleDeletionJob(ctx, deletion) }()
	waitForBlocked(checkoutPID)
	if err := gate.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// Withdrawal can win before the external checkout call. In either ordering,
	// the committed pending contract must protect the accepted file.
	if err := <-checkoutDone; err != nil && !errors.Is(err, ErrInvalidCheckout) {
		t.Fatalf("checkout failed unexpectedly: %v", err)
	}
	if err := <-deletionDone; err != nil {
		t.Fatal(err)
	}
	var contracts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_order_contracts WHERE root_asset_id=$1`, source).Scan(&contracts); err != nil || contracts != 1 {
		t.Fatalf("contract was not committed: %d %v", contracts, err)
	}
	var orderID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT order_id FROM product_delivery_snapshots WHERE source_key=$1`, source.String()+".jpg").Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := productdelivery.Load(ctx, pool, orderID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State == "ready" {
		object, err := snapshot.Open(ctx, media.NewCatalog(media.NewLocalStore(root)), nil)
		if err != nil {
			t.Fatalf("concurrent deletion erased ready copy: %v", err)
		}
		body, err := io.ReadAll(object.Body)
		_ = object.Body.Close()
		if err != nil || string(body) != "Pending contract bytes" {
			t.Fatalf("wrong retained bytes: %q %v", body, err)
		}
	} else if _, err := media.NewLocalStore(root).Stat(ctx, source.String()+".jpg"); err != nil {
		t.Fatalf("concurrent deletion erased source before copy was ready: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT scan_status FROM assets WHERE id=$1`, source).Scan(&status); err != nil || status != "clean" {
		t.Fatalf("concurrent deletion invalidated contracted source: %s %v", status, err)
	}
}

func assertPurchasedBytes(t *testing.T, pool *pgxpool.Pool, root string, buyer, asset uuid.UUID, expected []byte) {
	t.Helper()
	ctx := context.Background()
	content, err := assets.NewService(pool, root).Content(ctx, buyer, asset)
	if err != nil {
		t.Fatal(err)
	}
	object, err := content.Open(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	actual, readErr := io.ReadAll(object.Body)
	closeErr := object.Body.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(actual, expected) {
		t.Fatalf("contract media changed: %q read=%v close=%v", actual, readErr, closeErr)
	}
}

func TestProductMediaRetainedAfterSellerDeletion(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	fixture := newPurchasedReferenceFixture(t, pool, "video")
	var seller, product uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT a.owner_id,o.product_id FROM assets a JOIN orders o ON o.id=$2 WHERE a.id=$1`, fixture.source, fixture.order).Scan(&seller, &product); err != nil {
		t.Fatal(err)
	}
	secondBuyer := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name) VALUES($1,$2,$3,'Second buyer')`, secondBuyer, secondBuyer.String()+"@test.local", "b_"+secondBuyer.String()[:8]); err != nil {
		t.Fatal(err)
	}
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(&guardedProductRuntime{}))
	checkout, _, err := service.BeginProductCheckout(ctx, secondBuyer, product, "second-retention-buyer", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	service.verifier.now = func() time.Time { return now }
	providerID := strings.ReplaceAll(checkout.PaymentID.String(), "-", "")
	receipt := receivePaymentWorkflowEvent(t, service, billingStripeCheckoutEvent(t, pool, "evt_"+providerID, checkout.PaymentID, product, "product", checkout.AmountCents, "pi_"+providerID, now.Unix()), now)
	if err := service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID))}); err != nil {
		t.Fatal(err)
	}
	var secondAsset uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT asset_id FROM entitlements WHERE order_id=$1`, checkout.OrderID).Scan(&secondAsset); err != nil {
		t.Fatal(err)
	}
	store := media.NewLocalStore(fixture.root)
	if err := store.Put(ctx, "seller-replacement.jpg", []byte("Unpurchased replacement"), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE assets SET storage_key='seller-replacement.jpg' WHERE id=$1`, fixture.source); err != nil {
		t.Fatal(err)
	}
	deleteMarketplaceAccount(t, pool, fixture.root, seller)
	assertPurchasedBytes(t, pool, fixture.root, fixture.buyer, fixture.purchased, fixture.content)
	assertPurchasedBytes(t, pool, fixture.root, secondBuyer, secondAsset, fixture.content)
	if _, err := store.Stat(ctx, "seller-replacement.jpg"); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("uncontracted current media was retained: %v", err)
	}
	if _, err := assets.NewService(pool, fixture.root).Content(ctx, uuid.Nil, fixture.purchased); !errors.Is(err, assets.ErrForbidden) {
		t.Fatalf("retained purchase became public: %v", err)
	}
	runtime := &referenceWorkerRuntime{}
	creator := creation.NewServiceWithRuntimes(pool, fixture.root, creation.NewRuntimeCatalog(runtime))
	generation, err := creator.SubmitCommand(ctx, fixture.buyer, creation.SubmitInput{Mode: "video", Prompt: "Use my licensed purchase after the seller leaves", SourceAssetIDs: []uuid.UUID{fixture.purchased}}, "retained-purchase-generation", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.HandleJob(ctx, generationWorkerJob(t, pool, generation.ID)); err != nil || runtime.calls != 1 || len(runtime.request.ReferenceAssets) != 1 || !bytes.Equal(runtime.request.ReferenceAssets[0].Content, fixture.content) {
		t.Fatalf("retained licensed reuse failed: %v calls=%d", err, runtime.calls)
	}
	if _, err := store.Stat(ctx, fixture.source.String()+".jpg"); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("redundant deleted seller original retained: %v", err)
	}
	deleteMarketplaceAccount(t, pool, fixture.root, fixture.buyer)
	firstCopy, err := productdelivery.Load(ctx, pool, fixture.order)
	if err != nil || firstCopy.State != "removed" {
		t.Fatalf("deleted buyer copy retained: %+v %v", firstCopy, err)
	}
	assertPurchasedBytes(t, pool, fixture.root, secondBuyer, secondAsset, fixture.content)
	deleteMarketplaceAccount(t, pool, fixture.root, secondBuyer)
	secondCopy, err := productdelivery.Load(ctx, pool, checkout.OrderID)
	if err != nil || secondCopy.State != "removed" {
		t.Fatalf("last buyer copy retained: %+v %v", secondCopy, err)
	}
	if _, err := store.Stat(ctx, fixture.source.String()+".jpg"); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("orphaned contract media survived final buyer deletion: %v", err)
	}
}

func TestPendingProductContractSurvivesSellerDeletion(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	buyer, seller, source, product := newProductCheckoutFixture(t, pool)
	root := paymentTestRoot(t, pool)
	content := []byte("Purchased before seller deletion")
	replacePaymentFixtureBytes(t, pool, source, content)
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(&productCheckoutRuntime{}))
	checkout, _, err := service.BeginProductCheckout(ctx, buyer, product, "pending-retention-buyer", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product))
	if err != nil {
		t.Fatal(err)
	}
	deleteMarketplaceAccount(t, pool, root, seller)
	now := time.Now().UTC().Truncate(time.Second)
	service.verifier.now = func() time.Time { return now }
	receipt := receivePaymentWorkflowEvent(t, service, productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents), now)
	if err := service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID))}); err != nil {
		t.Fatal(err)
	}
	var purchased uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT asset_id FROM entitlements WHERE order_id=$1`, checkout.OrderID).Scan(&purchased); err != nil {
		t.Fatal(err)
	}
	assertPurchasedBytes(t, pool, root, buyer, purchased, content)
	if _, _, err := service.BeginProductCheckout(ctx, buyer, product, "after-deletion-checkout", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product)); err == nil {
		t.Fatal("deleted seller accepted new checkout")
	}
	deleteMarketplaceAccount(t, pool, root, buyer)
	if _, err := media.NewLocalStore(root).Stat(ctx, source.String()+".jpg"); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("unused source retained: %v", err)
	}
}

func TestProductDeletionDoesNotRestoreRejectedOrRefundedMedia(t *testing.T) {
	for _, scenario := range []string{"rejected", "refunded", "buyer_deleted_first"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			fixture := newPurchasedReferenceFixture(t, pool, "video")
			var seller uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT owner_id FROM assets WHERE id=$1`, fixture.source).Scan(&seller); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "rejected":
				if _, err := pool.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, fixture.source); err != nil {
					t.Fatal(err)
				}
			case "refunded":
				if _, err := pool.Exec(ctx, `UPDATE entitlements SET status='refunded',revoked_at=now() WHERE order_id=$1`, fixture.order); err != nil {
					t.Fatal(err)
				}
			case "buyer_deleted_first":
				deleteMarketplaceAccount(t, pool, fixture.root, fixture.buyer)
			}
			deleteMarketplaceAccount(t, pool, fixture.root, seller)
			if _, err := assets.NewService(pool, fixture.root).Content(ctx, fixture.buyer, fixture.purchased); !errors.Is(err, assets.ErrNotFound) {
				t.Fatalf("deletion granted access to rejected/revoked media: %v", err)
			}
			if scenario != "rejected" {
				if _, err := media.NewLocalStore(fixture.root).Stat(ctx, fixture.source.String()+".jpg"); !errors.Is(err, media.ErrNotFound) {
					t.Fatalf("no remaining buyer but source retained: %v", err)
				}
			}
		})
	}
}

func TestRecoveredMediaCleanupRechecksProductRights(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	fixture := newPurchasedReferenceFixture(t, pool, "video")
	var seller uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT owner_id FROM assets WHERE id=$1`, fixture.source).Scan(&seller); err != nil {
		t.Fatal(err)
	}
	deleteMarketplaceAccount(t, pool, fixture.root, seller)
	// Independent uncontracted files become removable, but the existing purchase
	// must remain readable through both failed and successful cleanup attempts.
	extra := uuid.New()
	key := extra.String() + ".txt"
	store := media.NewLocalStore(fixture.root)
	if err := store.Put(ctx, key, []byte("unneeded private file"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,storage_backend,storage_key)
 VALUES($1,$2,'document','Unneeded file','/private-fixture','text/plain','rejected','upload','local_file',$3)`, extra, seller, key); err != nil {
		t.Fatal(err)
	}
	// Isolate the cleanup worker's queue from unrelated fixture notifications.
	if _, err := pool.Exec(ctx, `UPDATE jobs SET available_at=now()+interval '1 day' WHERE status='queued'`); err != nil {
		t.Fatal(err)
	}
	var original uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('userId',$2::text),1) RETURNING id`, datarights.MediaCleanupJobKind, seller).Scan(&original); err != nil {
		t.Fatal(err)
	}
	repository := jobs.NewRepository(pool)
	job, err := repository.Claim(ctx, "cleanup-test", time.Minute)
	if err != nil || job.ID != original {
		t.Fatal("wrong claimed cleanup", err)
	}
	failing := &failingDeletionStore{Store: store, fail: true}
	cleanupService := datarights.NewServiceWithMedia(pool, fixture.root, media.NewCatalog(failing))
	failure := cleanupService.HandleMediaCleanupJob(ctx, job)
	if failure == nil {
		t.Fatal("injected deletion unexpectedly succeeded")
	}
	if err := repository.Fail(ctx, job, "cleanup-test", failure); err != nil {
		t.Fatal(err)
	}
	assertPurchasedBytes(t, pool, fixture.root, fixture.buyer, fixture.purchased, fixture.content)
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, fixture.buyer); err != nil {
		t.Fatal(err)
	}
	attempts := 1
	recovered, err := cleanupService.RetryMediaCleanup(ctx, fixture.buyer, original, datarights.MediaCleanupRetryInput{ExpectedAttempts: &attempts, Confirmed: true, Reason: "Storage connectivity restored; retain all active purchase deliveries."}, "cleanup-retention-recovery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stat(ctx, key); err != nil {
		t.Fatal("recovery HTTP/service call deleted bytes before worker", err)
	}
	failing.fail = false
	job, err = repository.Claim(ctx, "cleanup-test", time.Minute)
	if err != nil || job.ID != recovered.ID {
		t.Fatal("replacement not claimable", err)
	}
	if err := cleanupService.HandleMediaCleanupJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(ctx, job, "cleanup-test"); err != nil {
		t.Fatal(err)
	}
	assertPurchasedBytes(t, pool, fixture.root, fixture.buyer, fixture.purchased, fixture.content)
	if _, err := store.Stat(ctx, key); !errors.Is(err, media.ErrNotFound) {
		t.Fatal("unneeded file remains", err)
	}
	// A later refund can remove the last retention requirement; an earlier
	// successful retry does not make its former retention decision permanent.
	if _, err := pool.Exec(ctx, `UPDATE entitlements SET status='refunded',revoked_at=now() WHERE order_id=$1`, fixture.order); err != nil {
		t.Fatal(err)
	}
	var later jobs.Job
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('userId',$2::text),20) RETURNING id,payload`, datarights.MediaCleanupJobKind, seller).Scan(&later.ID, &later.Payload); err != nil {
		t.Fatal(err)
	}
	if err := cleanupService.HandleMediaCleanupJob(ctx, later); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stat(ctx, fixture.source.String()+".jpg"); !errors.Is(err, media.ErrNotFound) {
		t.Fatal("last revoked delivery remains", err)
	}
	var originalStatus, attemptStatus string
	if err := pool.QueryRow(ctx, `SELECT j.status,a.status FROM jobs j JOIN job_attempts a ON a.job_id=j.id WHERE j.id=$1`, original).Scan(&originalStatus, &attemptStatus); err != nil || originalStatus != "failed" || attemptStatus != "failed" {
		t.Fatal("failure history overwritten", err)
	}
}

func TestExpiredSellerHoldResumesDeletionAndRetainsPurchasedBytes(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	fixture := newPurchasedReferenceFixture(t, pool, "video")
	var seller uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT owner_id FROM assets WHERE id=$1`, fixture.source).Scan(&seller); err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Hold operator','admin')`, actor, actor.String()+"@test.local", "expiry_"+actor.String()[:8]); err != nil {
		t.Fatal(err)
	}
	request, _ := scheduleMarketplaceDeletion(t, pool, fixture.root, seller)
	rights := datarights.NewService(pool, fixture.root)
	hold, err := rights.CreateHold(ctx, actor, datarights.HoldInput{UserID: seller, AuthorityReference: "SELLER-HOLD-EXPIRY-TEST"}, "hold-retention")
	if err != nil {
		t.Fatal(err)
	}
	var originalID uuid.UUID
	if err := pool.QueryRow(ctx, `UPDATE jobs SET available_at=now()-interval '1 day' WHERE kind=$1 AND payload->>'requestId'=$2 RETURNING id`, datarights.DeletionJobKind, request.String()).Scan(&originalID); err != nil {
		t.Fatal(err)
	}
	repository := jobs.NewRepository(pool)
	original, err := repository.Claim(ctx, "expiry-original", time.Minute)
	if err != nil || original.ID != originalID {
		t.Fatal(original, err)
	}
	if err := rights.HandleDeletionJob(ctx, original); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM users WHERE id=$1`, seller).Scan(&status); err != nil || status != "active" {
		t.Fatal("hold failed", status, err)
	}
	// Expiry races with acknowledgement of an old handler that already returned
	// blocked. The still-running old job must not suppress a successor.
	if _, err := pool.Exec(ctx, `UPDATE data_rights_legal_holds SET review_at=now()-interval '2 days',expires_at=now()-interval '1 day' WHERE id=$1`, hold.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := rights.ExpireLegalHolds(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	var successorID uuid.UUID
	if err := pool.QueryRow(ctx, `UPDATE jobs SET available_at=now()-interval '1 day' WHERE kind=$1 AND payload->>'requestId'=$2 AND status='queued' RETURNING id`, datarights.DeletionJobKind, request.String()).Scan(&successorID); err != nil || successorID == originalID {
		t.Fatal("lost expiry wakeup", err)
	}
	if err := repository.Complete(ctx, original, "expiry-original"); err != nil {
		t.Fatal(err)
	}
	successor, err := repository.Claim(ctx, "expiry-successor", time.Minute)
	if err != nil || successor.ID != successorID {
		t.Fatal(successor, err)
	}
	if err := rights.HandleDeletionJob(ctx, successor); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(ctx, successor, "expiry-successor"); err != nil {
		t.Fatal(err)
	}
	assertPurchasedBytes(t, pool, fixture.root, fixture.buyer, fixture.purchased, fixture.content)
	if _, err := assets.NewService(pool, fixture.root).Content(ctx, uuid.Nil, fixture.purchased); !errors.Is(err, assets.ErrForbidden) {
		t.Fatal("expiry exposed purchased media", err)
	}
	var requestStatus string
	var receipt []byte
	if err := pool.QueryRow(ctx, `SELECT u.status,r.status,d.receipt FROM users u JOIN data_rights_requests r ON r.user_id=u.id JOIN data_rights_deletion_receipts d ON d.request_id=r.id WHERE r.id=$1`, request).Scan(&status, &requestStatus, &receipt); err != nil || status != "deleted" || requestStatus != "completed" || !strings.Contains(string(receipt), "erased_except_required_task_and_product_contracts") {
		t.Fatal("expiry failed retention contract", status, requestStatus, string(receipt), err)
	}
}

func TestAccountCleanupLocksPaymentsBeforeSubjectsAndSources(t *testing.T) {
	for _, actor := range []string{"buyer", "seller"} {
		for _, operation := range []string{"media_worker", "deletion_finalizer"} {
			t.Run(actor+"/"+operation, func(t *testing.T) {
				pool, cleanup := paymentTestPool(t)
				defer cleanup()
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				f := newPurchasedReferenceFixture(t, pool, "video")
				subject := f.buyer
				if actor == "seller" {
					if err := pool.QueryRow(ctx, `SELECT owner_id FROM assets WHERE id=$1`, f.source).Scan(&subject); err != nil {
						t.Fatal(err)
					}
				}
				store := &failingDeletionStore{Store: media.NewLocalStore(f.root), fail: true}
				service := datarights.NewServiceWithMedia(pool, f.root, media.NewCatalog(store))
				requestID, deletionJob := scheduleMarketplaceDeletion(t, pool, f.root, subject)
				if err := service.HandleDeletionJob(ctx, deletionJob); err == nil {
					t.Fatal("expected physical cleanup failure after committed preparation")
				}
				store.fail = false
				run := func() error { return service.HandleDeletionJob(ctx, deletionJob) }
				if operation == "media_worker" {
					var job jobs.Job
					if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts)
 VALUES($1,jsonb_build_object('userId',$2::text),20) RETURNING id,payload`, datarights.MediaCleanupJobKind, subject).Scan(&job.ID, &job.Payload); err != nil {
						t.Fatal(err)
					}
					run = func() error { return service.HandleMediaCleanupJob(ctx, job) }
				}
				gate, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer gate.Rollback(context.Background())
				if _, err = gate.Exec(ctx, `SELECT id FROM payment_intents WHERE order_id=$1 FOR UPDATE`, f.order); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- run() }()
				waitForProductBlockingTx(t, ctx, pool, int32(gate.Conn().PgConn().PID()))
				// A payment transaction must still be able to acquire subjects and source
				// assets. NOWAIT detects a reverse edge without choosing a deadlock victim.
				_, lockErr := gate.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE NOWAIT`, subject)
				if lockErr == nil {
					_, lockErr = gate.Exec(ctx, `SELECT id FROM assets WHERE id IN ($1,$2) ORDER BY id FOR UPDATE NOWAIT`, f.source, f.purchased)
				}
				if err := gate.Rollback(ctx); err != nil {
					t.Fatal(err)
				}
				if err := <-done; err != nil {
					t.Fatal("account cleanup failed", err)
				}
				if lockErr != nil {
					t.Fatal("account cleanup held subjects or sources while waiting for payment", lockErr)
				}
				if err := service.HandleDeletionJob(ctx, deletionJob); err != nil {
					t.Fatal(err)
				}
				var status string
				if err := pool.QueryRow(ctx, `SELECT status FROM data_rights_requests WHERE id=$1`, requestID).Scan(&status); err != nil || status != "completed" {
					t.Fatal("deletion did not complete", status, err)
				}
				snapshot, err := productdelivery.Load(ctx, pool, f.order)
				if err != nil {
					t.Fatal(err)
				}
				if actor == "buyer" {
					if snapshot.State != "removed" {
						t.Fatal("delivery copy not removed", snapshot.State)
					}
					if _, err := store.Stat(ctx, f.source.String()+".jpg"); err != nil {
						t.Fatal("active seller source removed", err)
					}
				} else {
					if snapshot.State != "ready" {
						t.Fatal("active buyer copy removed", snapshot.State)
					}
					assertPurchasedBytes(t, pool, f.root, f.buyer, f.purchased, f.content)
					if _, err := store.Stat(ctx, f.source.String()+".jpg"); !errors.Is(err, media.ErrNotFound) {
						t.Fatal("unneeded deleted seller source remains", err)
					}
				}
			})
		}
	}
}

func TestReconciledSellerDeletionRetainsPurchasedDelivery(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	f := newPurchasedReferenceFixture(t, pool, "video")
	var seller uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT owner_id FROM assets WHERE id=$1`, f.source).Scan(&seller); err != nil {
		t.Fatal(err)
	}
	request, _ := scheduleMarketplaceDeletion(t, pool, f.root, seller)
	if _, err := pool.Exec(ctx, `DELETE FROM jobs WHERE kind=$1 AND payload->>'requestId'=$2 AND status='queued'`, datarights.DeletionJobKind, request.String()); err != nil {
		t.Fatal(err)
	}
	service := datarights.NewService(pool, f.root)
	if n, err := service.ReconcileDeletions(ctx, 100); err != nil || n != 1 {
		t.Fatal("seller deletion not reconciled", n, err)
	}
	var job jobs.Job
	if err := pool.QueryRow(ctx, `SELECT j.id,j.payload FROM jobs j JOIN account_deletion_reconciliations r ON r.job_id=j.id WHERE r.request_id=$1`, request).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleDeletionJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	current, err := service.Get(ctx, seller, request)
	if err != nil || current.Status != "completed" || current.Receipt == nil {
		t.Fatal("seller deletion incomplete", err)
	}
	assertPurchasedBytes(t, pool, f.root, f.buyer, f.purchased, f.content)
	if _, err := media.NewLocalStore(f.root).Stat(ctx, f.source.String()+".jpg"); !errors.Is(err, media.ErrNotFound) {
		t.Fatal("unneeded original retained", err)
	}
}

func TestBuyerDeletionRetainsHeldLegacySellerSource(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	buyer, seller, source, product := newProductCheckoutFixture(t, pool)
	root := paymentTestRoot(t, pool)
	content := []byte("Legacy seller evidence under a later hold")
	replacePaymentFixtureBytes(t, pool, source, content)
	purchase := testutil.SeedLegacyProductOrder(t, pool, buyer, product)
	deleteMarketplaceAccount(t, pool, root, seller)
	assertPurchasedBytes(t, pool, root, buyer, purchase.AssetID, content)
	service := datarights.NewService(pool, root)
	hold, err := service.CreateHold(ctx, buyer, datarights.HoldInput{UserID: seller, AuthorityReference: "LEGACY-SELLER-SOURCE-PRESERVATION"}, "hold")
	if err != nil {
		t.Fatal(err)
	}
	var scannedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT scanned_at FROM assets WHERE id=$1`, source).Scan(&scannedAt); err != nil {
		t.Fatal(err)
	}
	deleteMarketplaceAccount(t, pool, root, buyer)
	if _, err := media.NewLocalStore(root).Stat(ctx, source.String()+".jpg"); err != nil {
		t.Fatal("buyer deletion removed another account's held source", err)
	}
	var scan string
	var after time.Time
	if err := pool.QueryRow(ctx, `SELECT scan_status,scanned_at FROM assets WHERE id=$1`, source).Scan(&scan, &after); err != nil || scan != "clean" || !after.Equal(scannedAt) {
		t.Fatal("buyer deletion rewrote held seller metadata", scan, err)
	}
	if _, err := service.ReleaseHold(ctx, buyer, hold.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResumeLegalHoldCleanups(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var job jobs.Job
	if err := pool.QueryRow(ctx, `SELECT j.id,j.payload FROM jobs j JOIN legal_hold_cleanup_dispatches d ON d.job_id=j.id WHERE d.hold_id=$1 AND d.kind='account' AND d.user_id=$2`, hold.ID, seller).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleMediaCleanupJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err := media.NewLocalStore(root).Stat(ctx, source.String()+".jpg"); !errors.Is(err, media.ErrNotFound) {
		t.Fatal("released source was never removed", err)
	}
}
