package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Real reservation, verified independent copy and guarded unpaid closure.
func newCleanupReconciliationFixture(t *testing.T) (pendingProductClosure, productdelivery.Snapshot, jobs.Job, uuid.UUID) {
	t.Helper()
	f := newPendingProductClosure(t)
	ctx := context.Background()
	f.store.fail = false
	if err := productdelivery.Ensure(ctx, f.pool, f.service.config.MediaStores, f.order); err != nil {
		t.Fatal(err)
	}
	snapshot, err := productdelivery.Load(ctx, f.pool, f.order)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.close(ctx, "reconcile-close-checkout"); err != nil {
		t.Fatal(err)
	}
	var job jobs.Job
	if err = f.pool.QueryRow(ctx, `SELECT id,kind,payload FROM jobs WHERE kind=$1 AND payload->>'orderId'=$2`, productdelivery.CleanupJobKind, f.order.String()).Scan(&job.ID, &job.Kind, &job.Payload); err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	if _, err = f.pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Cleanup auditor','admin')`, actor, actor.String()+"@example.test", "recon_"+actor.String()[:8]); err != nil {
		t.Fatal(err)
	}
	return f, snapshot, job, actor
}
func loseOriginalCleanup(t *testing.T, f pendingProductClosure, job jobs.Job) {
	t.Helper()
	// Reproduce a historical missing dispatch, never a production repair action.
	if _, err := f.pool.Exec(context.Background(), `DELETE FROM jobs WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
}
func reconciliationJob(t *testing.T, f pendingProductClosure) jobs.Job {
	t.Helper()
	var j jobs.Job
	if err := f.pool.QueryRow(context.Background(), `SELECT j.id,j.kind,j.payload FROM jobs j JOIN product_cleanup_reconciliations r ON r.job_id=j.id WHERE r.order_id=$1 ORDER BY r.created_at DESC,r.job_id DESC LIMIT 1`, f.order).Scan(&j.ID, &j.Kind, &j.Payload); err != nil {
		t.Fatal(err)
	}
	return j
}
func claimReconciliationJob(t *testing.T, f pendingProductClosure, j jobs.Job) jobs.Job {
	t.Helper()
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `UPDATE jobs SET available_at=now()-interval '1 day' WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := jobs.NewRepository(f.pool).Claim(ctx, "cleanup-reconcile", time.Minute)
	if err != nil || claimed.ID != j.ID {
		t.Fatal(claimed, err)
	}
	return claimed
}

func TestCleanupReconciliationRepairsMissingDispatchAtomically(t *testing.T) {
	f, snapshot, original, _ := newCleanupReconciliationFixture(t)
	ctx := context.Background()
	loseOriginalCleanup(t, f, original)
	svc := datarights.NewService(f.pool, paymentTestRoot(t, f.pool))
	// Evidence failure must roll back the new job as well as the linkage.
	if _, err := f.pool.Exec(ctx, `CREATE FUNCTION fail_cleanup_reconcile_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='product.cleanup_reconciled' THEN RAISE EXCEPTION 'private/storage/secret'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER fail_cleanup_reconcile_test BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_cleanup_reconcile_test()`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReconcileProductCleanups(ctx, 100); err == nil {
		t.Fatal("missing injected failure")
	}
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM product_cleanup_reconciliations)+(SELECT count(*) FROM jobs WHERE kind=$1)`, productdelivery.CleanupJobKind).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial dispatch", count, err)
	}
	if _, err := f.pool.Exec(ctx, `DROP TRIGGER fail_cleanup_reconcile_test ON audit_events`); err != nil {
		t.Fatal(err)
	}
	// Concurrent independent process cursors must still dispatch once.
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := datarights.NewService(f.pool, "").ReconcileProductCleanups(ctx, 100)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM product_cleanup_reconciliations WHERE order_id=$1`, f.order).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate reconciliation", count, err)
	}
	job := reconciliationJob(t, f)
	if n, err := svc.ReconcileProductCleanups(ctx, 100); err != nil || n != 0 {
		t.Fatal("queued duplicated", n, err)
	}
	claimed := claimReconciliationJob(t, f, job)
	if err := productdelivery.CleanupHandler(f.pool, f.service.config.MediaStores)(ctx, claimed); err != nil {
		t.Fatal(err)
	}
	if err := jobs.NewRepository(f.pool).Complete(ctx, claimed, "cleanup-reconcile"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Stat(ctx, snapshot.Key); !errors.Is(err, media.ErrNotFound) {
		t.Fatal("copy not removed", err)
	}
	if _, err := f.store.Stat(ctx, snapshot.SourceKey); err != nil {
		t.Fatal("active seller original deleted", err)
	}
	if n, err := svc.ReconcileProductCleanups(ctx, 100); err != nil || n != 0 {
		t.Fatal("removed copy rescanned", n, err)
	}
	for _, statement := range []string{`DELETE FROM product_cleanup_reconciliations WHERE order_id=$1`, `UPDATE product_cleanup_reconciliations SET created_at=now() WHERE order_id=$1`} {
		if _, err := f.pool.Exec(ctx, statement, f.order); err == nil {
			t.Fatal("dispatch evidence mutable")
		}
	}
	down, err := os.ReadFile("../platform/database/migrations/0103_product_cleanup_reconciliation.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(down)); err == nil {
		t.Fatal("down discarded reconciliation evidence")
	}
	tx.Rollback(ctx)
	// Export only the buyer's safe job linkage; no seller, storage or admin data.
	var seller uuid.UUID
	if err = f.pool.QueryRow(ctx, `SELECT seller_id FROM products WHERE id=$1`, f.product).Scan(&seller); err != nil {
		t.Fatal(err)
	}
	for _, subject := range []uuid.UUID{f.buyer, seller} {
		_, body := runProductExport(t, f.pool, subject)
		var pkg struct {
			Data struct {
				Rows []map[string]any `json:"productCleanupReconciliations"`
			}
		}
		if err = json.Unmarshal(body, &pkg); err != nil {
			t.Fatal(err)
		}
		if subject == f.buyer {
			if len(pkg.Data.Rows) != 1 || pkg.Data.Rows[0]["jobId"] != job.ID.String() || pkg.Data.Rows[0]["previousJobId"] != nil {
				t.Fatal("buyer evidence missing", pkg.Data.Rows)
			}
			raw, _ := json.Marshal(pkg.Data.Rows)
			if strings.Contains(string(raw), snapshot.Key) || strings.Contains(string(raw), seller.String()) {
				t.Fatal("private cleanup evidence leaked")
			}
		} else if len(pkg.Data.Rows) != 0 {
			t.Fatal("seller received buyer linkage")
		}
	}
}

func TestCleanupReconciliationGuardsFundsRightsHoldsAndFailedJobs(t *testing.T) {
	for _, scenario := range []string{"queued", "running", "failed", "cancelled_job", "pending_payment", "inconsistent_order", "active_right", "active_hold", "refund_review", "already_removed"} {
		t.Run(scenario, func(t *testing.T) {
			f, snapshot, original, actor := newCleanupReconciliationFixture(t)
			ctx := context.Background()
			svc := datarights.NewService(f.pool, paymentTestRoot(t, f.pool))
			if scenario != "queued" && scenario != "running" && scenario != "failed" && scenario != "cancelled_job" && scenario != "already_removed" {
				loseOriginalCleanup(t, f, original)
			}
			var err error
			switch scenario {
			case "running":
				_, err = f.pool.Exec(ctx, `UPDATE jobs SET status='running' WHERE id=$1`, original.ID)
			case "failed":
				_, err = f.pool.Exec(ctx, `UPDATE jobs SET status='failed',attempts=20,last_error_code='handler_failed' WHERE id=$1`, original.ID)
			case "cancelled_job":
				_, err = f.pool.Exec(ctx, `UPDATE jobs SET status='cancelled' WHERE id=$1`, original.ID)
			case "pending_payment":
				_, err = f.pool.Exec(ctx, `UPDATE payment_intents SET status='checkout_pending' WHERE id=$1`, f.payment)
			case "inconsistent_order":
				_, err = f.pool.Exec(ctx, `UPDATE payment_intents SET status='paid' WHERE id=$1`, f.payment)
			case "active_right":
				_, err = f.pool.Exec(ctx, `INSERT INTO entitlements(user_id,product_id,order_id,asset_id,status,license_code) SELECT $1,$2,$3,asset_id,'active',license_code FROM products WHERE id=$2`, f.buyer, f.product, f.order)
			case "active_hold":
				_, err = svc.CreateHold(ctx, actor, datarights.HoldInput{UserID: f.buyer, AuthorityReference: "COPY-RECONCILIATION-HOLD"}, "guard-hold")
			case "refund_review":
				_, err = f.pool.Exec(ctx, `INSERT INTO product_refund_attempts(operation_id,payment_id,provider,provider_payment_id,amount_cents,currency,correlation_enabled,status,reconciliation_required,requested_at)
  SELECT gen_random_uuid(),id,provider,'pi_unresolved',amount_cents,currency,true,'succeeded',true,now() FROM payment_intents WHERE id=$1`, f.payment)
			case "already_removed":
				err = productdelivery.CleanupHandler(f.pool, f.service.config.MediaStores)(ctx, original)
			}
			if err != nil {
				t.Fatal(err)
			}
			if n, err := svc.ReconcileProductCleanups(ctx, 100); err != nil || n != 0 {
				t.Fatal("unsafe dispatch", scenario, n, err)
			}
			if scenario != "already_removed" {
				if _, err := f.store.Stat(ctx, snapshot.Key); err != nil {
					t.Fatal("protected copy lost", err)
				}
			}
			if scenario == "failed" {
				var attempts int
				var state string
				if err = f.pool.QueryRow(ctx, `SELECT attempts,status FROM jobs WHERE id=$1`, original.ID).Scan(&attempts, &state); err != nil || attempts != 20 || state != "failed" {
					t.Fatal("exhausted evidence reset", attempts, state, err)
				}
			}
		})
	}
}

func TestCleanupReconciliationRechecksAtExecutionAndRevisitsSuccessfulNoop(t *testing.T) {
	for _, scenario := range []string{"new_hold", "uncertain_funds", "new_right"} {
		t.Run(scenario, func(t *testing.T) {
			f, snapshot, original, actor := newCleanupReconciliationFixture(t)
			ctx := context.Background()
			loseOriginalCleanup(t, f, original)
			svc := datarights.NewService(f.pool, paymentTestRoot(t, f.pool))
			if n, err := svc.ReconcileProductCleanups(ctx, 100); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			job := claimReconciliationJob(t, f, reconciliationJob(t, f))
			var err error
			switch scenario {
			case "new_hold":
				_, err = svc.CreateHold(ctx, actor, datarights.HoldInput{UserID: f.buyer, AuthorityReference: "NEW-HOLD-BEFORE-CLEANUP"}, "new-hold")
			case "uncertain_funds":
				_, err = f.pool.Exec(ctx, `UPDATE payment_intents SET status='refund_pending' WHERE id=$1`, f.payment)
			case "new_right":
				_, err = f.pool.Exec(ctx, `INSERT INTO entitlements(user_id,product_id,order_id,asset_id,status,license_code) SELECT $1,$2,$3,asset_id,'active',license_code FROM products WHERE id=$2`, f.buyer, f.product, f.order)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = productdelivery.CleanupHandler(f.pool, f.service.config.MediaStores)(ctx, job)
			if scenario == "new_hold" {
				if !errors.Is(err, productdelivery.ErrLegalHold) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if _, err = f.store.Stat(ctx, snapshot.Key); err != nil {
				t.Fatal("changed obligation ignored", err)
			}
			if scenario != "uncertain_funds" {
				return
			}
			// A worker that already committed its no-op but has not acknowledged cannot
			// cause a lost wakeup. The next periodic scan revisits it after completion.
			if _, err = f.pool.Exec(ctx, `UPDATE payment_intents SET status='cancelled' WHERE id=$1`, f.payment); err != nil {
				t.Fatal(err)
			}
			if n, err := svc.ReconcileProductCleanups(ctx, 100); err != nil || n != 0 {
				t.Fatal("running successor duplicated", n, err)
			}
			repo := jobs.NewRepository(f.pool)
			if err = repo.Complete(ctx, job, "cleanup-reconcile"); err != nil {
				t.Fatal(err)
			}
			if n, err := svc.ReconcileProductCleanups(ctx, 100); err != nil || n != 1 {
				t.Fatal("successful no-op lost wakeup", n, err)
			}
			next := reconciliationJob(t, f)
			var predecessor uuid.UUID
			if err = f.pool.QueryRow(ctx, `SELECT previous_job_id FROM product_cleanup_reconciliations WHERE job_id=$1`, next.ID).Scan(&predecessor); err != nil || predecessor != job.ID {
				t.Fatal("predecessor lost", predecessor, err)
			}
			if err = productdelivery.CleanupHandler(f.pool, f.service.config.MediaStores)(ctx, next); err != nil {
				t.Fatal(err)
			}
			if _, err = f.store.Stat(ctx, snapshot.Key); !errors.Is(err, media.ErrNotFound) {
				t.Fatal("recovered copy retained", err)
			}
		})
	}
}

func TestCleanupReconciliationFairnessRestartAndBoundedBatch(t *testing.T) {
	f, _, original, _ := newCleanupReconciliationFixture(t)
	ctx := context.Background()
	loseOriginalCleanup(t, f, original)
	firstOrder, firstPayment := f.order, f.payment
	for n := 1; n < 3; n++ {
		f.store.fail = true
		_, _, err := f.service.BeginProductCheckout(ctx, f.buyer, f.product, fmt.Sprintf("reconcile-checkout-%d", n), "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, f.pool, f.product))
		if !errors.Is(err, ErrCheckoutPreparation) {
			t.Fatal(err)
		}
		if err = f.pool.QueryRow(ctx, `SELECT id,order_id,version FROM payment_intents WHERE payer_id=$1 AND status='checkout_pending'`, f.buyer).Scan(&f.payment, &f.order, &f.version); err != nil {
			t.Fatal(err)
		}
		f.store.fail = false
		if err = productdelivery.Ensure(ctx, f.pool, f.service.config.MediaStores, f.order); err != nil {
			t.Fatal(err)
		}
		if err = f.close(ctx, fmt.Sprintf("reconcile-close-%d", n)); err != nil {
			t.Fatal(err)
		}
		if _, err = f.pool.Exec(ctx, `DELETE FROM jobs WHERE kind=$1 AND payload->>'orderId'=$2`, productdelivery.CleanupJobKind, f.order.String()); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, firstPayment); err != nil {
		t.Fatal(err)
	}
	svc := datarights.NewService(f.pool, "")
	for _, bad := range []int{0, 101} {
		if _, err = svc.ReconcileProductCleanups(ctx, bad); !errors.Is(err, datarights.ErrInvalid) {
			t.Fatal("unbounded scan", bad, err)
		}
	}
	pass, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if n, err := svc.ReconcileProductCleanups(pass, 1); err != nil || n != 0 {
		t.Fatal("busy order blocked pass", n, err)
	}
	if n, err := svc.ReconcileProductCleanups(ctx, 1); err != nil || n != 1 {
		t.Fatal("first order starved later order", n, err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// Fresh process ignores the old hint; database evidence is authoritative.
	if n, err := datarights.NewService(f.pool, "").ReconcileProductCleanups(ctx, 1); err != nil || n != 1 {
		t.Fatal("restart lost skipped order", n, err)
	}
	var count int
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM product_cleanup_reconciliations WHERE order_id=$1`, firstOrder).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if n, err := svc.ReconcileProductCleanups(ctx, 1); err != nil || n != 1 {
		t.Fatal("last batch", n, err)
	}
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM product_cleanup_reconciliations`).Scan(&count); err != nil || count != 3 {
		t.Fatal("lost/extra jobs", count, err)
	}
}

func TestCleanupReconciliationRecoveryInheritsResolvedOrderGuard(t *testing.T) {
	f, snapshot, original, actor := newCleanupReconciliationFixture(t)
	ctx := context.Background()
	loseOriginalCleanup(t, f, original)
	svc := datarights.NewService(f.pool, paymentTestRoot(t, f.pool))
	if n, err := svc.ReconcileProductCleanups(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	job := reconciliationJob(t, f)
	for round := 0; round < 2; round++ {
		if _, err := f.pool.Exec(ctx, `UPDATE jobs SET status='failed',attempts=20,last_error_code='handler_failed' WHERE id=$1`, job.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(ctx, `UPDATE payment_intents SET status='refund_pending' WHERE id=$1`, f.payment); err != nil {
			t.Fatal(err)
		}
		page, err := svc.ListMediaCleanups(ctx, datarights.MediaCleanupListInput{Kind: "product", Status: "failed"})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range page.Items {
			if item.ID == job.ID {
				found = true
				if item.CanRetry || item.UnavailableReason != "order_unresolved" {
					t.Fatal("unsafe capability", item)
				}
			}
		}
		if !found {
			t.Fatal("failed job missing")
		}
		if _, err = svc.RetryMediaCleanup(ctx, actor, job.ID, productCleanupRetryInput(), "unresolved"); !errors.Is(err, datarights.ErrConflict) {
			t.Fatal("operator bypassed funds guard", err)
		}
		if _, err = f.pool.Exec(ctx, `UPDATE payment_intents SET status='cancelled' WHERE id=$1`, f.payment); err != nil {
			t.Fatal(err)
		}
		next, err := svc.RetryMediaCleanup(ctx, actor, job.ID, productCleanupRetryInput(), "resolved")
		if err != nil {
			t.Fatal(err)
		}
		var flag string
		if err = f.pool.QueryRow(ctx, `SELECT payload->>'retentionCheck' FROM jobs WHERE id=$1`, next.ID).Scan(&flag); err != nil || flag != "resolved_order" {
			t.Fatal("recovery dropped policy", flag, err)
		}
		// The worker obtains the guard from durable job data, not an input flag.
		job = jobs.Job{ID: next.ID, Kind: productdelivery.CleanupJobKind, Payload: []byte(fmt.Sprintf(`{"orderId":%q}`, f.order))}
		if _, err = f.pool.Exec(ctx, `UPDATE payment_intents SET status='refund_pending' WHERE id=$1`, f.payment); err != nil {
			t.Fatal(err)
		}
		if err = productdelivery.CleanupHandler(f.pool, f.service.config.MediaStores)(ctx, job); err != nil {
			t.Fatal(err)
		}
		if _, err = f.store.Stat(ctx, snapshot.Key); err != nil {
			t.Fatal("recovered worker deleted unresolved order", err)
		}
	}
}

func TestCleanupReconciliationPaidAndRefundedOrders(t *testing.T) {
	for _, scenario := range []string{"active_buyer", "deleted_buyer", "refunded"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			fixture := newPurchasedReferenceFixture(t, pool, "video")
			snapshot, err := productdelivery.Load(ctx, pool, fixture.order)
			if err != nil {
				t.Fatal(err)
			}
			svc := datarights.NewService(pool, fixture.root)
			if scenario == "deleted_buyer" {
				// Historical deletion committed, but its required physical cleanup did not.
				if _, err = pool.Exec(ctx, `UPDATE users SET status='deleted' WHERE id=$1`, fixture.buyer); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "refunded" {
				var product, payment uuid.UUID
				var amount int
				if err = pool.QueryRow(ctx, `SELECT o.product_id,p.id,p.amount_cents FROM payment_intents p JOIN orders o ON o.id=p.order_id WHERE o.id=$1`, fixture.order).Scan(&product, &payment, &amount); err != nil {
					t.Fatal(err)
				}
				service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(&productCheckoutRuntime{}))
				if _, err = service.BeginProductRefund(ctx, fixture.buyer, fixture.order, "reconcile-refund", "test", "The purchased resource does not match my requirements."); err != nil {
					t.Fatal(err)
				}
				if err = service.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, payment)); err != nil {
					t.Fatal(err)
				}
				if n, err := svc.ReconcileProductCleanups(ctx, 100); err != nil || n != 0 {
					t.Fatal("pending refund dispatched cleanup", n, err)
				}
				now := time.Now().UTC().Truncate(time.Second)
				service.verifier.now = func() time.Time { return now }
				receipt := receivePaymentWorkflowEvent(t, service, productRefundEvent("evt_reconcile_refund", "re_workflow123", "succeeded", payment, product, now.Unix(), amount), now)
				if err = service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID))}); err != nil {
					t.Fatal(err)
				}
				if _, err = pool.Exec(ctx, `DELETE FROM jobs WHERE kind=$1 AND payload->>'orderId'=$2`, productdelivery.CleanupJobKind, fixture.order.String()); err != nil {
					t.Fatal(err)
				}
			}
			count, err := svc.ReconcileProductCleanups(ctx, 100)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "active_buyer" {
				if count != 0 {
					t.Fatal("active purchase scheduled", count)
				}
				assertPurchasedBytes(t, pool, fixture.root, fixture.buyer, fixture.purchased, fixture.content)
				return
			}
			if count != 1 {
				t.Fatal("eligible settled order missed", count)
			}
			var job jobs.Job
			if err = pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM jobs j JOIN product_cleanup_reconciliations r ON r.job_id=j.id WHERE r.order_id=$1`, fixture.order).Scan(&job.ID, &job.Kind, &job.Payload); err != nil {
				t.Fatal(err)
			}
			stores := media.NewCatalog(media.NewLocalStore(fixture.root))
			if err = productdelivery.CleanupHandler(pool, stores)(ctx, job); err != nil {
				t.Fatal(err)
			}
			store := media.NewLocalStore(fixture.root)
			if _, err = store.Stat(ctx, snapshot.Key); !errors.Is(err, media.ErrNotFound) {
				t.Fatal("settled copy retained", err)
			}
			if _, err = store.Stat(ctx, snapshot.SourceKey); err != nil {
				t.Fatal("seller original removed", err)
			}
		})
	}
}

// Historical migration tests must unwind dependent views through their guarded
// migration, never DROP CASCADE or erase reconciliation evidence to proceed.
func rollbackCleanupReconciliationForMigrationTest(t *testing.T, pool *pgxpool.Pool) func() {
	t.Helper()
	apply := func(direction string) {
		t.Helper()
		body, err := os.ReadFile("../platform/database/migrations/0103_product_cleanup_reconciliation." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(context.Background(), string(body)); err != nil {
			t.Fatalf("cleanup reconciliation %s: %v", direction, err)
		}
	}
	apply("down")
	return func() { t.Helper(); apply("up") }
}
