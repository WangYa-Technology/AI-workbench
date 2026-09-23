package payments

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
)

func productCleanupRetryInput() datarights.MediaCleanupRetryInput {
	attempts := 20
	return datarights.MediaCleanupRetryInput{ExpectedAttempts: &attempts, Confirmed: true, Reason: "Storage access was repaired and checked."}
}

// Exercise the actual reservation, copy and closure before exhausting cleanup.
func newFailedProductCleanup(t *testing.T) (pendingProductClosure, productdelivery.Snapshot, jobs.Job, uuid.UUID) {
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
	if err = f.close(ctx, "cleanup-recovery-closure"); err != nil {
		t.Fatal(err)
	}
	var job jobs.Job
	if err = f.pool.QueryRow(ctx, `SELECT id,payload FROM jobs WHERE kind=$1 AND payload->>'orderId'=$2`, productdelivery.CleanupJobKind, f.order.String()).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	failed := &failingDeletionStore{Store: f.store, fail: true}
	if err = productdelivery.CleanupHandler(f.pool, media.NewCatalog(failed))(ctx, job); err == nil {
		t.Fatal("expected physical deletion failure")
	}
	if _, err = f.pool.Exec(ctx, `UPDATE jobs SET status='failed',attempts=20,last_error_code='handler_failed',last_error='private/storage/path' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	if _, err = f.pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Cleanup operator','admin')`, actor, actor.String()+"@test.local", "op_"+actor.String()[:8]); err != nil {
		t.Fatal(err)
	}
	return f, snapshot, job, actor
}

func TestProductCleanupRecoveryPreservesEvidenceAndDeletesOnlyCopy(t *testing.T) {
	f, snapshot, original, actor := newFailedProductCleanup(t)
	ctx := context.Background()
	service := datarights.NewService(f.pool, paymentTestRoot(t, f.pool))
	page, err := service.ListMediaCleanups(ctx, datarights.MediaCleanupListInput{Kind: "product"})
	if err != nil || len(page.Items) != 1 || !page.Items[0].CanRetry || page.Items[0].OrderID == nil || *page.Items[0].OrderID != f.order || page.Items[0].UserID == nil || *page.Items[0].UserID != f.buyer {
		t.Fatalf("product projection: %+v %v", page, err)
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), "private/storage") || strings.Contains(string(encoded), snapshot.Key) {
		t.Fatal("private object details leaked")
	}
	if _, err = service.RetryMediaCleanup(ctx, f.buyer, original.ID, productCleanupRetryInput(), "forbidden"); !errors.Is(err, datarights.ErrCleanupForbidden) {
		t.Fatalf("buyer recovered cleanup: %v", err)
	}
	replacement, err := service.RetryMediaCleanup(ctx, actor, original.ID, productCleanupRetryInput(), "recover-copy")
	if err != nil || replacement.Kind != "product" || replacement.RetryOf == nil || *replacement.RetryOf != original.ID || replacement.OrderID == nil || *replacement.OrderID != f.order {
		t.Fatalf("replacement: %+v %v", replacement, err)
	}
	if _, err = f.store.Stat(ctx, snapshot.Key); err != nil {
		t.Fatal("API deleted copy before worker", err)
	}
	var retry jobs.Job
	if err = f.pool.QueryRow(ctx, `SELECT id,kind,payload FROM jobs WHERE id=$1`, replacement.ID).Scan(&retry.ID, &retry.Kind, &retry.Payload); err != nil {
		t.Fatal(err)
	}
	if retry.Kind != productdelivery.CleanupJobKind || strings.Contains(string(retry.Payload), "userId") {
		t.Fatalf("wrong worker selected: %+v", retry)
	}
	if err = productdelivery.CleanupHandler(f.pool, f.service.config.MediaStores)(ctx, retry); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Stat(ctx, snapshot.Key); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("copy retained: %v", err)
	}
	if _, err = f.store.Stat(ctx, snapshot.SourceKey); err != nil {
		t.Fatal("seller original deleted", err)
	}
	saved, err := productdelivery.Load(ctx, f.pool, f.order)
	if err != nil || saved.State != "removed" || saved.SHA256 != snapshot.SHA256 || saved.Key != snapshot.Key {
		t.Fatalf("delivery evidence lost: %+v %v", saved, err)
	}
	var status string
	var attempts, rights, audit int
	if err = f.pool.QueryRow(ctx, `SELECT status,attempts,(SELECT count(*) FROM entitlements WHERE order_id=$2),
	 (SELECT count(*) FROM audit_events WHERE action='data_rights.media_cleanup_retried' AND metadata->>'kind'=$3 AND metadata->>'subjectId'=$2::text)
	 FROM jobs WHERE id=$1`, original.ID, f.order, productdelivery.CleanupJobKind).Scan(&status, &attempts, &rights, &audit); err != nil || status != "failed" || attempts != 20 || rights != 0 || audit != 1 {
		t.Fatalf("history changed: %s %d %d %d %v", status, attempts, rights, audit, err)
	}
	if _, err = service.RetryMediaCleanup(ctx, actor, original.ID, productCleanupRetryInput(), "duplicate"); !errors.Is(err, datarights.ErrConflict) {
		t.Fatalf("duplicate recovery accepted: %v", err)
	}
}

func TestProductCleanupRecoveryConcurrentJobsAndAutomaticEnqueue(t *testing.T) {
	f, _, job, actor := newFailedProductCleanup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var other uuid.UUID
	if err := f.pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,status,attempts,max_attempts) SELECT kind,payload,'failed',20,20 FROM jobs WHERE id=$1 RETURNING id`, job.ID).Scan(&other); err != nil {
		t.Fatal(err)
	}
	service := datarights.NewService(f.pool, paymentTestRoot(t, f.pool))
	results := make(chan error, 4)
	for _, id := range []uuid.UUID{job.ID, other, job.ID, other} {
		go func(id uuid.UUID) {
			_, err := service.RetryMediaCleanup(ctx, actor, id, productCleanupRetryInput(), "parallel-retry")
			results <- err
		}(id)
	}
	succeeded := 0
	for range 4 {
		err := <-results
		if err == nil {
			succeeded++
		} else if !errors.Is(err, datarights.ErrConflict) {
			t.Fatal(err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("parallel recoveries=%d", succeeded)
	}
	// Automatic follow-up must notice the already queued replacement.
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = productdelivery.EnqueueCleanupTx(ctx, tx, f.order); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind=$1 AND payload->>'orderId'=$2 AND status='queued'`, productdelivery.CleanupJobKind, f.order.String()).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate queued copies: %d %v", count, err)
	}
	// A later failure may be recovered without resetting the original record.
	var next uuid.UUID
	if err = f.pool.QueryRow(ctx, `UPDATE jobs SET status='failed',attempts=20 WHERE kind=$1 AND status='queued' RETURNING id`, productdelivery.CleanupJobKind).Scan(&next); err != nil {
		t.Fatal(err)
	}
	result, err := service.RetryMediaCleanup(ctx, actor, next, productCleanupRetryInput(), "next-attempt")
	if err != nil || result.RetryOf == nil || *result.RetryOf != next {
		t.Fatalf("retry lineage: %+v %v", result, err)
	}
}

func TestProductCleanupRecoveryRejectsRevokedOperator(t *testing.T) {
	for _, phase := range []string{"payment_lock", "enqueue"} {
		for _, change := range []string{"role", "suspended", "permission"} {
			t.Run(phase+"/"+change, func(t *testing.T) {
				f, snapshot, original, actor := newFailedProductCleanup(t)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				t.Cleanup(cancel)
				service := datarights.NewService(f.pool, paymentTestRoot(t, f.pool))
				var wait, release func()
				if phase == "enqueue" {
					traced, entered, resume := testutil.GateQuery(t, f.pool, "INSERT INTO jobs(kind,payload,max_attempts)")
					service = datarights.NewService(traced, paymentTestRoot(t, f.pool))
					wait = func() {
						select {
						case <-entered:
						case <-ctx.Done():
							t.Fatal("recovery did not reach enqueue", ctx.Err())
						}
					}
					release = resume
				} else {
					gate, err := f.pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = gate.Rollback(context.Background()) })
					if _, err := gate.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, f.payment); err != nil {
						t.Fatal(err)
					}
					wait = func() { waitForProductBlockingTx(t, ctx, f.pool, int32(gate.Conn().PgConn().PID())) }
					release = func() {
						if err := gate.Commit(ctx); err != nil {
							t.Fatal(err)
						}
					}
				}
				done := make(chan error, 1)
				go func() {
					_, err := service.RetryMediaCleanup(ctx, actor, original.ID, productCleanupRetryInput(), "revoked-product-cleanup")
					done <- err
				}()
				wait()
				var err error
				switch change {
				case "role":
					_, err = f.pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor)
				case "suspended":
					_, err = f.pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, actor)
				case "permission":
					_, err = f.pool.Exec(ctx, `DELETE FROM role_permissions WHERE role='admin' AND permission_id='admin:data-rights'`)
				}
				if err != nil {
					t.Fatal(err)
				}
				release()
				if err := <-done; !errors.Is(err, datarights.ErrCleanupForbidden) {
					t.Fatalf("revoked operator enqueued product cleanup: %v", err)
				}
				var status string
				var attempts, queued, evidence, audits int
				if err := f.pool.QueryRow(ctx, `SELECT status,attempts,
 (SELECT count(*) FROM jobs WHERE kind=$2 AND status='queued'),
 (SELECT count(*) FROM media_cleanup_recoveries),
 (SELECT count(*) FROM audit_events WHERE action='data_rights.media_cleanup_retried')
 FROM jobs WHERE id=$1`, original.ID, productdelivery.CleanupJobKind).Scan(&status, &attempts, &queued, &evidence, &audits); err != nil || status != "failed" || attempts != 20 || queued != 0 || evidence != 0 || audits != 0 {
					t.Fatalf("revoked recovery changed state: %s %d %d %d %d %v", status, attempts, queued, evidence, audits, err)
				}
				if _, err := f.store.Stat(ctx, snapshot.Key); err != nil {
					t.Fatal("denied recovery changed physical delivery", err)
				}
			})
		}
	}
}

func TestProductCleanupRecoveryRetentionGuards(t *testing.T) {
	for _, scenario := range []string{"pending", "entitled", "buyer_hold", "seller_hold", "source_owner_hold", "removed", "stale"} {
		t.Run(scenario, func(t *testing.T) {
			f, snapshot, job, actor := newFailedProductCleanup(t)
			ctx := context.Background()
			service := datarights.NewService(f.pool, paymentTestRoot(t, f.pool))
			input := productCleanupRetryInput()
			want := "legal_hold"
			switch scenario {
			case "pending":
				if _, err := f.pool.Exec(ctx, `UPDATE payment_intents SET status='checkout_pending' WHERE id=$1`, f.payment); err != nil {
					t.Fatal(err)
				}
				want = "order_unresolved"
			case "entitled":
				var source uuid.UUID
				if err := f.pool.QueryRow(ctx, `SELECT asset_id FROM products WHERE id=$1`, f.product).Scan(&source); err != nil {
					t.Fatal(err)
				}
				if _, err := f.pool.Exec(ctx, `INSERT INTO entitlements(user_id,product_id,order_id,asset_id,status,license_code) VALUES($1,$2,$3,$4,'active','hcai-commercial-standard-v1')`, f.buyer, f.product, f.order, source); err != nil {
					t.Fatal(err)
				}
				want = "delivery_required"
			case "removed":
				if err := productdelivery.CleanupHandler(f.pool, f.service.config.MediaStores)(ctx, job); err != nil {
					t.Fatal(err)
				}
				want = "already_removed"
			case "stale":
				n := 19
				input.ExpectedAttempts = &n
				want = ""
			default:
				subject := f.buyer
				if scenario == "seller_hold" {
					if err := f.pool.QueryRow(ctx, `SELECT seller_id FROM products WHERE id=$1`, f.product).Scan(&subject); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "source_owner_hold" {
					subject = actor
					if _, err := f.pool.Exec(ctx, `UPDATE assets SET owner_id=$1 WHERE id=(SELECT asset_id FROM products WHERE id=$2)`, actor, f.product); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := f.pool.Exec(ctx, `INSERT INTO data_rights_legal_holds(user_id,created_by,reason,authority_reference_hash,review_at,expires_at) VALUES($1,$2,'Keep delivery evidence',repeat('a',64),now()+interval '1 day',now()+interval '2 days')`, subject, actor); err != nil {
					t.Fatal(err)
				}
			}
			page, err := service.ListMediaCleanups(ctx, datarights.MediaCleanupListInput{Kind: "product"})
			if err != nil || len(page.Items) != 1 || page.Items[0].UnavailableReason != want {
				t.Fatalf("guard projection: %+v %v", page, err)
			}
			// A permission/retention guard must not hide a failed cleanup. Only
			// actual removal resolves this leaf before a replacement is linked.
			counts, err := datarights.RecoveryFailureCounts(ctx, f.pool)
			wantFailures := int64(1)
			if scenario == "removed" {
				wantFailures = 0
			}
			if err != nil || counts["media_product"] != wantFailures {
				t.Fatalf("cleanup failure metrics: %v want=%d err=%v", counts, wantFailures, err)
			}
			if _, err = service.RetryMediaCleanup(ctx, actor, job.ID, input, "protected-copy"); !errors.Is(err, datarights.ErrConflict) {
				t.Fatalf("protected recovery: %v", err)
			}
			if scenario != "removed" {
				if _, err = f.store.Stat(ctx, snapshot.Key); err != nil {
					t.Fatal("retained copy missing", err)
				}
			}
		})
	}
}

func TestProductCleanupRecoveryWaitsForLegalHold(t *testing.T) {
	f, snapshot, job, actor := newFailedProductCleanup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	gate, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(ctx)
	if _, err = gate.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, f.buyer); err != nil {
		t.Fatal(err)
	}
	service := datarights.NewService(f.pool, paymentTestRoot(t, f.pool))
	done := make(chan error, 1)
	go func() {
		_, err := service.RetryMediaCleanup(ctx, actor, job.ID, productCleanupRetryInput(), "concurrent-hold")
		done <- err
	}()
	waitForProductBlockingTx(t, ctx, f.pool, int32(gate.Conn().PgConn().PID()))
	if _, err = gate.Exec(ctx, `INSERT INTO data_rights_legal_holds(user_id,created_by,reason,authority_reference_hash,review_at,expires_at) VALUES($1,$2,'Keep delivery evidence',repeat('b',64),now()+interval '1 day',now()+interval '2 days')`, f.buyer, actor); err != nil {
		t.Fatal(err)
	}
	if err = gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, datarights.ErrConflict) {
		t.Fatalf("recovery bypassed hold: %v", err)
	}
	if _, err = f.store.Stat(ctx, snapshot.Key); err != nil {
		t.Fatal(err)
	}
}

func TestProductCleanupRecoveryWorkerRechecksAndPreservesMigrationEvidence(t *testing.T) {
	f, snapshot, job, actor := newFailedProductCleanup(t)
	ctx := context.Background()
	service := datarights.NewService(f.pool, paymentTestRoot(t, f.pool))
	replacement, err := service.RetryMediaCleanup(ctx, actor, job.ID, productCleanupRetryInput(), "before-hold")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `INSERT INTO data_rights_legal_holds(user_id,created_by,reason,authority_reference_hash,review_at,expires_at) VALUES($1,$2,'Preserve the purchased delivery',repeat('c',64),now()+interval '1 day',now()+interval '2 days')`, f.buyer, actor); err != nil {
		t.Fatal(err)
	}
	retry := jobs.Job{ID: replacement.ID, Payload: job.Payload}
	if err = productdelivery.CleanupHandler(f.pool, f.service.config.MediaStores)(ctx, retry); !errors.Is(err, productdelivery.ErrLegalHold) {
		t.Fatalf("worker bypassed new hold: %v", err)
	}
	if _, err = f.store.Stat(ctx, snapshot.Key); err != nil {
		t.Fatal(err)
	}
	restoreReconciliation := rollbackCleanupReconciliationForMigrationTest(t, f.pool)
	restoreBundleLifecycle := rollbackBundleLifecycle(t, f.pool)
	for _, direction := range []string{"down", "up"} {
		body, err := os.ReadFile("../platform/database/migrations/0092_product_delivery_cleanup_recovery." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.pool.Exec(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
		var evidence int
		if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM media_cleanup_recoveries WHERE original_job_id=$1`, job.ID).Scan(&evidence); err != nil || evidence != 1 {
			t.Fatalf("migration discarded evidence: %d %v", evidence, err)
		}
	}
	restoreReconciliation()
	restoreBundleLifecycle()
	page, err := service.ListMediaCleanups(ctx, datarights.MediaCleanupListInput{Kind: "product", Status: "all"})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("reapplied queue: %+v %v", page, err)
	}
}

func TestProductCleanupRecoveryAtomicFailure(t *testing.T) {
	f, snapshot, job, actor := newFailedProductCleanup(t)
	ctx := context.Background()
	service := datarights.NewService(f.pool, paymentTestRoot(t, f.pool))
	// Irrelevant payload fields are never propagated to the replacement worker.
	if _, err := f.pool.Exec(ctx, `UPDATE jobs SET payload=payload||'{"userId":"irrelevant","storageKey":"private/never-copy"}'::jsonb WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `CREATE FUNCTION fail_cleanup_audit_test() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'simulated audit outage'; END $$ LANGUAGE plpgsql;
	 CREATE TRIGGER fail_cleanup_audit_test BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_cleanup_audit_test()`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RetryMediaCleanup(ctx, actor, job.ID, productCleanupRetryInput(), "audit-failure"); err == nil {
		t.Fatal("expected atomic rollback")
	}
	var replacements, queued int
	if err := f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM media_cleanup_recoveries WHERE original_job_id=$1),
	 (SELECT count(*) FROM jobs WHERE kind='product.delivery_cleanup' AND status='queued')`, job.ID).Scan(&replacements, &queued); err != nil || replacements != 0 || queued != 0 {
		t.Fatalf("partial recovery: %d %d %v", replacements, queued, err)
	}
	if _, err := f.store.Stat(ctx, snapshot.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `DROP TRIGGER fail_cleanup_audit_test ON audit_events`); err != nil {
		t.Fatal(err)
	}
	next, err := service.RetryMediaCleanup(ctx, actor, job.ID, productCleanupRetryInput(), "audit-recovered")
	if err != nil {
		t.Fatal(err)
	}
	var payload []byte
	if err = f.pool.QueryRow(ctx, `SELECT payload FROM jobs WHERE id=$1`, next.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var fields map[string]string
	if json.Unmarshal(payload, &fields) != nil || len(fields) != 1 || fields["orderId"] != f.order.String() {
		t.Fatalf("reused untrusted payload: %s", payload)
	}
}
