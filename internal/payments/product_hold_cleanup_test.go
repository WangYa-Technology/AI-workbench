package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
)

func TestEndedHoldDispatchDeletesOnlyUnneededProductCopy(t *testing.T) {
	for _, scenario := range []string{"released", "expired", "pending_payment", "active_entitlement", "other_hold", "new_hold_after_dispatch", "already_removed"} {
		t.Run(scenario, func(t *testing.T) {
			f, snapshot, original, actor := newFailedProductCleanup(t)
			ctx := context.Background()
			service := datarights.NewService(f.pool, paymentTestRoot(t, f.pool))
			hold, err := service.CreateHold(ctx, actor, datarights.HoldInput{UserID: f.buyer, AuthorityReference: "BUYER-COPY-RETENTION-HOLD"}, "copy-hold")
			if err != nil {
				t.Fatal(err)
			}
			var seller uuid.UUID
			if err := f.pool.QueryRow(ctx, `SELECT seller_id FROM products WHERE id=$1`, f.product).Scan(&seller); err != nil {
				t.Fatal(err)
			}
			shouldDispatch := true
			switch scenario {
			case "pending_payment":
				_, err = f.pool.Exec(ctx, `UPDATE payment_intents SET status='checkout_pending' WHERE id=$1`, f.payment)
				shouldDispatch = false
			case "active_entitlement":
				_, err = f.pool.Exec(ctx, `INSERT INTO entitlements(user_id,product_id,order_id,asset_id,status,license_code) SELECT $1,$2,$3,asset_id,'active',license_code FROM products WHERE id=$2`, f.buyer, f.product, f.order)
				shouldDispatch = false
			case "other_hold":
				_, err = service.CreateHold(ctx, actor, datarights.HoldInput{UserID: seller, AuthorityReference: "SELLER-COPY-RETENTION-HOLD"}, "seller-hold")
				shouldDispatch = false
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "expired" {
				if _, err := f.pool.Exec(ctx, `UPDATE data_rights_legal_holds SET review_at=now()-interval '2 days',expires_at=now()-interval '1 day' WHERE id=$1`, hold.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := service.ExpireLegalHolds(ctx, 100); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := service.ReleaseHold(ctx, actor, hold.ID); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "already_removed" {
				if err := productdelivery.CleanupHandler(f.pool, f.service.config.MediaStores)(ctx, original); err != nil {
					t.Fatal(err)
				}
				shouldDispatch = false
			}
			if _, err := service.ResumeLegalHoldCleanups(ctx, 100); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM legal_hold_cleanup_dispatches WHERE hold_id=$1 AND kind='product'`, hold.ID).Scan(&count); err != nil || (count == 1) != shouldDispatch {
				t.Fatal("eligibility", count, shouldDispatch, err)
			}
			if !shouldDispatch {
				if scenario != "already_removed" {
					if _, err := f.store.Stat(ctx, snapshot.Key); err != nil {
						t.Fatal("protected copy lost", err)
					}
				}
				return
			}
			var job jobs.Job
			if err := f.pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM jobs j JOIN legal_hold_cleanup_dispatches d ON d.job_id=j.id WHERE d.hold_id=$1 AND d.kind='product'`, hold.ID).Scan(&job.ID, &job.Kind, &job.Payload); err != nil {
				t.Fatal(err)
			}
			if scenario == "new_hold_after_dispatch" {
				if _, err := service.CreateHold(ctx, actor, datarights.HoldInput{UserID: seller, AuthorityReference: "NEW-HOLD-AFTER-DISPATCH"}, "fresh-hold"); err != nil {
					t.Fatal(err)
				}
				if err := productdelivery.CleanupHandler(f.pool, f.service.config.MediaStores)(ctx, job); !errors.Is(err, productdelivery.ErrLegalHold) {
					t.Fatal("worker bypassed new hold", err)
				}
				if _, err := f.store.Stat(ctx, snapshot.Key); err != nil {
					t.Fatal(err)
				}
				return
			}
			// Claim and acknowledge the actual child; unrelated fixture jobs retain their schedules.
			if _, err := f.pool.Exec(ctx, `UPDATE jobs SET available_at=now()-interval '1 day' WHERE id=$1`, job.ID); err != nil {
				t.Fatal(err)
			}
			repo := jobs.NewRepository(f.pool)
			claimed, err := repo.Claim(ctx, "hold-copy-cleanup", time.Minute)
			if err != nil || claimed.ID != job.ID {
				t.Fatal(claimed, err)
			}
			if err := productdelivery.CleanupHandler(f.pool, f.service.config.MediaStores)(ctx, claimed); err != nil {
				t.Fatal(err)
			}
			if err := repo.Complete(ctx, claimed, "hold-copy-cleanup"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.Stat(ctx, snapshot.Key); !errors.Is(err, media.ErrNotFound) {
				t.Fatal("copy not removed", err)
			}
			if _, err := f.store.Stat(ctx, snapshot.SourceKey); err != nil {
				t.Fatal("seller original lost", err)
			}
			var originalStatus string
			var attempts int
			if err := f.pool.QueryRow(ctx, `SELECT status,attempts FROM jobs WHERE id=$1`, original.ID).Scan(&originalStatus, &attempts); err != nil || originalStatus != "failed" || attempts != 20 {
				t.Fatal("original evidence changed", originalStatus, attempts, err)
			}
			// Buyer export includes their dispatch, but no seller/hold/operator identifiers.
			_, body := runProductExport(t, f.pool, f.buyer)
			var pkg struct {
				Data struct {
					Jobs []map[string]any `json:"retentionCleanupJobs"`
				}
			}
			if err := json.Unmarshal(body, &pkg); err != nil || len(pkg.Data.Jobs) != 1 {
				t.Fatal("dispatch missing from export", len(pkg.Data.Jobs), err)
			}
			row, _ := json.Marshal(pkg.Data.Jobs[0])
			if strings.Contains(string(row), hold.ID.String()) || strings.Contains(string(row), seller.String()) || strings.Contains(string(row), snapshot.Key) {
				t.Fatal("dispatch exposed private evidence", string(row))
			}
			_, sellerBody := runProductExport(t, f.pool, seller)
			if err := json.Unmarshal(sellerBody, &pkg); err != nil || len(pkg.Data.Jobs) != 0 {
				t.Fatal("buyer dispatch leaked to seller", err)
			}
		})
	}
}

func TestEndedHoldDispatchResumesPaginatedOrdersAfterRestart(t *testing.T) {
	f := newPendingProductClosure(t)
	ctx := context.Background()
	// Build more than one batch through real reservation and safe local closure.
	for n := 0; n < 23; n++ {
		if n > 0 {
			f.store.fail = true
			_, _, err := f.service.BeginProductCheckout(ctx, f.buyer, f.product, fmt.Sprintf("hold-batch-checkout-%d", n), "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, f.pool, f.product))
			if !errors.Is(err, ErrCheckoutPreparation) {
				t.Fatal(err)
			}
			if err := f.pool.QueryRow(ctx, `SELECT id,order_id,version FROM payment_intents WHERE payer_id=$1 AND status='checkout_pending'`, f.buyer).Scan(&f.payment, &f.order, &f.version); err != nil {
				t.Fatal(err)
			}
		}
		f.store.fail = false
		if err := productdelivery.Ensure(ctx, f.pool, f.service.config.MediaStores, f.order); err != nil {
			t.Fatal(err)
		}
		if err := f.close(ctx, fmt.Sprintf("hold-batch-closure-%d", n)); err != nil {
			t.Fatal(err)
		}
	}
	// Existing failures remain evidence; the ended hold supplies a new trigger.
	if _, err := f.pool.Exec(ctx, `UPDATE jobs SET status='failed',attempts=20 WHERE kind=$1`, productdelivery.CleanupJobKind); err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	if _, err := f.pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Batch operator','admin')`, actor, actor.String()+"@test.local", "batch_"+actor.String()[:8]); err != nil {
		t.Fatal(err)
	}
	root := paymentTestRoot(t, f.pool)
	service := datarights.NewService(f.pool, root)
	hold, err := service.CreateHold(ctx, actor, datarights.HoldInput{UserID: f.buyer, AuthorityReference: "BATCH-ORDER-RETENTION-HOLD"}, "hold-batch")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReleaseHold(ctx, actor, hold.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := service.ResumeLegalHoldCleanups(ctx, 1); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	var count int
	var completed bool
	if err := f.pool.QueryRow(ctx, `SELECT completed_at IS NOT NULL,(SELECT count(*) FROM legal_hold_cleanup_dispatches WHERE hold_id=$1) FROM legal_hold_cleanup_checks WHERE hold_id=$1`, hold.ID).Scan(&completed, &count); err != nil || completed || count != 20 {
		t.Fatal("unbounded batch", completed, count, err)
	}
	// A fresh process reads the durable order cursor, not its empty scan cursor.
	if n, err := datarights.NewService(f.pool, root).ResumeLegalHoldCleanups(ctx, 1); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT completed_at IS NOT NULL,(SELECT count(*) FROM legal_hold_cleanup_dispatches WHERE hold_id=$1) FROM legal_hold_cleanup_checks WHERE hold_id=$1`, hold.ID).Scan(&completed, &count); err != nil || !completed || count != 23 {
		t.Fatal("lost or repeated batch", completed, count, err)
	}
	if n, err := service.ResumeLegalHoldCleanups(ctx, 100); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	var queued, failed int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='queued'),count(*) FILTER(WHERE status='failed') FROM jobs WHERE kind=$1`, productdelivery.CleanupJobKind).Scan(&queued, &failed); err != nil || queued != 23 || failed != 23 {
		t.Fatal("failure history or queue lost", queued, failed, err)
	}
}

func TestEndedHoldCleanupDoesNotRetainPaymentLockWhileWaitingForAccount(t *testing.T) {
	f, _, _, actor := newFailedProductCleanup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rights := datarights.NewService(f.pool, paymentTestRoot(t, f.pool))
	hold, err := rights.CreateHold(ctx, actor, datarights.HoldInput{UserID: f.buyer, AuthorityReference: "CLEANUP-LOCK-ORDER-CHECK"}, "hold-locks")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rights.ReleaseHold(ctx, actor, hold.ID); err != nil {
		t.Fatal(err)
	}
	gate, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err := gate.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, f.buyer); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := rights.ResumeLegalHoldCleanups(ctx, 100); done <- err }()
	waitForProductBlockingTx(t, ctx, f.pool, int32(gate.Conn().PgConn().PID()))
	// Keep dispatch phases independent: waiting on an account must not retain
	// the previous batch's payment locks, even after cleanup lock-order changes.
	if _, err := gate.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE NOWAIT`, f.payment); err != nil {
		t.Fatal("scheduler retained payment lock while waiting for account", err)
	}
	if err := gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEndedHoldCleanupRemovesDeletedSellerOriginalAndCopy(t *testing.T) {
	f, snapshot, _, actor := newFailedProductCleanup(t)
	ctx := context.Background()
	root := paymentTestRoot(t, f.pool)
	rights := datarights.NewService(f.pool, root)
	var seller uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT seller_id FROM products WHERE id=$1`, f.product).Scan(&seller); err != nil {
		t.Fatal(err)
	}
	hold, err := rights.CreateHold(ctx, actor, datarights.HoldInput{UserID: seller, AuthorityReference: "DELETED-SELLER-MEDIA-HOLD"}, "seller-hold")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE users SET status='deleted' WHERE id=$1`, seller); err != nil {
		t.Fatal(err)
	}
	if _, err := rights.ReleaseHold(ctx, actor, hold.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := rights.ResumeLegalHoldCleanups(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var accountJob jobs.Job
	if err := f.pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM jobs j JOIN legal_hold_cleanup_dispatches d ON d.job_id=j.id WHERE d.hold_id=$1 AND d.kind='account' AND d.user_id=$2`, hold.ID, seller).Scan(&accountJob.ID, &accountJob.Kind, &accountJob.Payload); err != nil {
		t.Fatal(err)
	}
	if err := rights.HandleMediaCleanupJob(ctx, accountJob); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{snapshot.Key, snapshot.SourceKey} {
		if _, err := f.store.Stat(ctx, key); !errors.Is(err, media.ErrNotFound) {
			t.Fatal("unneeded deleted-seller bytes retained", key, err)
		}
	}
	saved, err := productdelivery.Load(ctx, f.pool, f.order)
	if err != nil || saved.State != "removed" || saved.SHA256 != snapshot.SHA256 {
		t.Fatal("lost evidence", saved, err)
	}
}

func TestEndedHoldDispatchAndOperatorRecoveryShareOneQueuedJob(t *testing.T) {
	f, _, original, actor := newFailedProductCleanup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	service := datarights.NewService(f.pool, paymentTestRoot(t, f.pool))
	hold, err := service.CreateHold(ctx, actor, datarights.HoldInput{UserID: f.buyer, AuthorityReference: "CONCURRENT-HOLD-CLEANUP"}, "hold-parallel")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReleaseHold(ctx, actor, hold.ID); err != nil {
		t.Fatal(err)
	}
	gate, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err := gate.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, f.payment); err != nil {
		t.Fatal(err)
	}
	autoDone := make(chan error, 1)
	manualDone := make(chan error, 1)
	go func() { _, err := service.ResumeLegalHoldCleanups(ctx, 100); autoDone <- err }()
	waitForProductBlockingTx(t, ctx, f.pool, int32(gate.Conn().PgConn().PID()))
	go func() {
		_, err := service.RetryMediaCleanup(ctx, actor, original.ID, productCleanupRetryInput(), "operator-race")
		manualDone <- err
	}()
	if err := gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-autoDone; err != nil {
		t.Fatal(err)
	}
	if err := <-manualDone; err != nil && !errors.Is(err, datarights.ErrConflict) {
		t.Fatal(err)
	}
	var queued, dispatches int
	if err := f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM jobs WHERE kind=$1 AND payload->>'orderId'=$2 AND status='queued'),(SELECT count(*) FROM legal_hold_cleanup_dispatches WHERE hold_id=$3 AND order_id=$2::uuid)`, productdelivery.CleanupJobKind, f.order.String(), hold.ID).Scan(&queued, &dispatches); err != nil || queued != 1 || dispatches != 1 {
		t.Fatal("concurrent dispatch duplicated queue", queued, dispatches, err)
	}
}
