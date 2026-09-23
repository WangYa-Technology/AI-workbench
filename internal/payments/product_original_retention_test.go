package payments

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

func originalCleanupJob(t *testing.T, pool *pgxpool.Pool, owner uuid.UUID) jobs.Job {
	t.Helper()
	var job jobs.Job
	if err := pool.QueryRow(context.Background(), `INSERT INTO jobs(kind,payload,max_attempts)
 VALUES($1,jsonb_build_object('userId',$2::text),20) RETURNING id,payload`, datarights.MediaCleanupJobKind, owner).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	return job
}

func TestBuyerCleanupWaitsForLegacySourceHold(t *testing.T) {
	for _, operation := range []string{"deletion", "media_worker"} {
		t.Run(operation, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			buyer, seller, source, product := newProductCheckoutFixture(t, pool)
			root := paymentTestRoot(t, pool)
			testutil.SeedLegacyProductOrder(t, pool, buyer, product)
			deleteMarketplaceAccount(t, pool, root, seller)
			_, deletion := scheduleMarketplaceDeletion(t, pool, root, buyer)
			service := datarights.NewService(pool, root)
			run := func() error { return service.HandleDeletionJob(ctx, deletion) }
			if operation == "media_worker" {
				failure := &failingDeletionStore{Store: media.NewLocalStore(root), fail: true}
				if err := datarights.NewServiceWithMedia(pool, root, media.NewCatalog(failure)).HandleDeletionJob(ctx, deletion); err == nil {
					t.Fatal("expected cleanup failure after preparing buyer deletion")
				}
				job := originalCleanupJob(t, pool, buyer)
				run = func() error { return service.HandleMediaCleanupJob(ctx, job) }
			}
			var before time.Time
			if err := pool.QueryRow(ctx, `SELECT scanned_at FROM assets WHERE id=$1`, source).Scan(&before); err != nil {
				t.Fatal(err)
			}
			gate, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Rollback(context.Background())
			if _, err := gate.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, seller); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- run() }()
			waitForProductBlockingTx(t, ctx, pool, int32(gate.Conn().PgConn().PID()))
			// Commit a hold only after observing physical cleanup blocked on the
			// seller row. This exercises the fresh post-lock retention statement.
			if _, err := gate.Exec(ctx, `INSERT INTO data_rights_legal_holds(user_id,created_by,reason,authority_reference_hash,review_at,expires_at)
 VALUES($1,$1,'Preserve original evidence',repeat('a',64),now()+interval '1 day',now()+interval '2 days')`, seller); err != nil {
				t.Fatal(err)
			}
			if err := gate.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if _, err := media.NewLocalStore(root).Stat(ctx, source.String()+".jpg"); err != nil {
				t.Fatal("concurrent source hold lost", err)
			}
			var after time.Time
			if err := pool.QueryRow(ctx, `SELECT scanned_at FROM assets WHERE id=$1`, source).Scan(&after); err != nil || !before.Equal(after) {
				t.Fatal("other account metadata changed", err)
			}
		})
	}
}

func TestOriginalMediaSharedWithActiveOwnerSurvivesDeletion(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	f := newPurchasedReferenceFixture(t, pool, "video")
	other, source, root := f.buyer, f.source, f.root
	var seller uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT owner_id FROM assets WHERE id=$1`, source).Scan(&seller); err != nil {
		t.Fatal(err)
	}
	// Asset locations are unique. Move the original first, then register its
	// frozen contract location under another owner; do not weaken that index.
	store := media.NewLocalStore(root)
	movedKey := uuid.NewString() + ".jpg"
	if err := store.Put(ctx, movedKey, f.content, "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE assets SET storage_key=$2 WHERE id=$1`, source, movedKey); err != nil {
		t.Fatal(err)
	}
	alias := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 SELECT $1,$2,kind,'Shared original','/private-alias',mime_type,'clean','upload',license_code,storage_backend,$4 FROM assets WHERE id=$3`, alias, other, source, source.String()+".jpg"); err != nil {
		t.Fatal(err)
	}
	deleteMarketplaceAccount(t, pool, root, seller)
	if _, err := store.Stat(ctx, source.String()+".jpg"); err != nil {
		t.Fatal("active alias lost its original", err)
	}
	if _, err := store.Stat(ctx, movedKey); !errors.Is(err, media.ErrNotFound) {
		t.Fatal("unneeded moved original retained", err)
	}
	var scan string
	if err := pool.QueryRow(ctx, `SELECT scan_status FROM assets WHERE id=$1`, alias).Scan(&scan); err != nil || scan != "clean" {
		t.Fatal("active alias was changed", scan, err)
	}
	deleteMarketplaceAccount(t, pool, root, other)
	if _, err := store.Stat(ctx, source.String()+".jpg"); !errors.Is(err, media.ErrNotFound) {
		t.Fatal("last owner deletion did not remove shared original", err)
	}
}

func TestEndedLegacyBuyerHoldSchedulesOriginalOwnerCleanup(t *testing.T) {
	for _, ending := range []string{"release", "expiry"} {
		t.Run(ending, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			buyer, seller, source, product := newProductCheckoutFixture(t, pool)
			root := paymentTestRoot(t, pool)
			purchase := testutil.SeedLegacyProductOrder(t, pool, buyer, product)
			service := datarights.NewService(pool, root)
			hold, err := service.CreateHold(ctx, buyer, datarights.HoldInput{UserID: buyer, AuthorityReference: "LEGACY-BUYER-ORIGINAL"}, "test")
			if err != nil {
				t.Fatal(err)
			}
			// The entitlement ended, but the buyer's legal evidence still needs
			// the original. Legacy orders have no snapshot cleanup subject view.
			if _, err := pool.Exec(ctx, `UPDATE entitlements SET status='refunded',revoked_at=now() WHERE order_id=$1`, purchase.OrderID); err != nil {
				t.Fatal(err)
			}
			deleteMarketplaceAccount(t, pool, root, seller)
			store := media.NewLocalStore(root)
			if _, err := store.Stat(ctx, source.String()+".jpg"); err != nil {
				t.Fatal("buyer hold failed to retain legacy evidence", err)
			}
			if ending == "release" {
				if _, err := service.ReleaseHold(ctx, buyer, hold.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := pool.Exec(ctx, `UPDATE data_rights_legal_holds SET review_at=now()-interval '2 days',expires_at=now()-interval '1 day' WHERE id=$1`, hold.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := service.ExpireLegalHolds(ctx, 100); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := service.ResumeLegalHoldCleanups(ctx, 100); err != nil {
				t.Fatal(err)
			}
			var job jobs.Job
			if err := pool.QueryRow(ctx, `SELECT j.id,j.payload FROM jobs j JOIN legal_hold_cleanup_dispatches d ON d.job_id=j.id
 WHERE d.hold_id=$1 AND d.kind='account' AND d.user_id=$2`, hold.ID, seller).Scan(&job.ID, &job.Payload); err != nil {
				t.Fatal("ended buyer hold failed to schedule original owner", err)
			}
			if err := service.HandleMediaCleanupJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Stat(ctx, source.String()+".jpg"); !errors.Is(err, media.ErrNotFound) {
				t.Fatal("original remained after hold ended", err)
			}
		})
	}
}

func TestBuyerHoldRetainsFrozenOriginalAfterSourceMoves(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	f := newPurchasedReferenceFixture(t, pool, "video")
	var seller uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT owner_id FROM assets WHERE id=$1`, f.source).Scan(&seller); err != nil {
		t.Fatal(err)
	}
	service := datarights.NewService(pool, f.root)
	hold, err := service.CreateHold(ctx, f.buyer, datarights.HoldInput{UserID: f.buyer, AuthorityReference: "FROZEN-CONTRACT-ORIGINAL"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	store := media.NewLocalStore(f.root)
	movedKey := uuid.NewString() + ".jpg"
	if err := store.Put(ctx, movedKey, []byte("uncontracted replacement"), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE assets SET storage_key=$2 WHERE id=$1`, f.source, movedKey); err != nil {
		t.Fatal(err)
	}
	deleteMarketplaceAccount(t, pool, f.root, seller)
	if _, err := store.Stat(ctx, f.source.String()+".jpg"); err != nil {
		t.Fatal("hold lost frozen location after source moved", err)
	}
	if _, err := store.Stat(ctx, movedKey); !errors.Is(err, media.ErrNotFound) {
		t.Fatal("unrelated replacement retained", err)
	}
	assertPurchasedBytes(t, pool, f.root, f.buyer, f.purchased, f.content)
	if _, err := service.ReleaseHold(ctx, f.buyer, hold.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResumeLegalHoldCleanups(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var job jobs.Job
	if err := pool.QueryRow(ctx, `SELECT j.id,j.payload FROM jobs j JOIN legal_hold_cleanup_dispatches d ON d.job_id=j.id
 WHERE d.hold_id=$1 AND d.kind='account' AND d.user_id=$2`, hold.ID, seller).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleMediaCleanupJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stat(ctx, f.source.String()+".jpg"); !errors.Is(err, media.ErrNotFound) {
		t.Fatal("unneeded frozen original remained", err)
	}
	assertPurchasedBytes(t, pool, f.root, f.buyer, f.purchased, f.content)
}

func TestDeletedTaskClientHoldRetainsCreatorOriginal(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	client, creator, source, _ := newProductCheckoutFixture(t, pool)
	root := paymentTestRoot(t, pool)
	demand, delivery, granted := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO demands(id,client_id,assignee_id,title,brief,deliverable_type,budget_cents,deadline,status)
 VALUES($1,$2,$3,'Retained task','Accepted task evidence','image',100,now()+interval '1 day','accepted')`, demand, client, creator); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO deliveries(id,demand_id,creator_id,asset_id,status) VALUES($1,$2,$3,$4,'accepted')`, delivery, demand, creator, source); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,origin_asset_id)
 SELECT $1,$2,kind,'Granted task result',media_url,mime_type,'clean','delivery',id FROM assets WHERE id=$3`, granted, client, source); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO task_delivery_grants(demand_id,delivery_id,source_asset_id,asset_id,client_id,creator_id,rights_terms,rights_evidence,ai_disclosure,allow_derivative_reuse)
 VALUES($1,$2,$3,$4,$5,$6,'Agreed terms','Accepted evidence','AI assisted',false)`, demand, delivery, source, granted, client, creator); err != nil {
		t.Fatal(err)
	}
	deleteMarketplaceAccount(t, pool, root, client)
	service := datarights.NewService(pool, root)
	hold, err := service.CreateHold(ctx, creator, datarights.HoldInput{UserID: client, AuthorityReference: "DELETED-TASK-CLIENT-EVIDENCE"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	deleteMarketplaceAccount(t, pool, root, creator)
	store := media.NewLocalStore(root)
	if _, err := store.Stat(ctx, source.String()+".jpg"); err != nil {
		t.Fatal("task client hold lost original", err)
	}
	if _, err := service.ReleaseHold(ctx, creator, hold.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResumeLegalHoldCleanups(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var job jobs.Job
	if err := pool.QueryRow(ctx, `SELECT j.id,j.payload FROM jobs j JOIN legal_hold_cleanup_dispatches d ON d.job_id=j.id
 WHERE d.hold_id=$1 AND d.kind='account' AND d.user_id=$2`, hold.ID, creator).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal("task creator cleanup not dispatched", err)
	}
	if err := service.HandleMediaCleanupJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stat(ctx, source.String()+".jpg"); !errors.Is(err, media.ErrNotFound) {
		t.Fatal("task original remained after hold release", err)
	}
}

func TestOriginalCleanupDoesNotBlockHoldActorForeignKey(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newPurchasedReferenceFixture(t, pool, "video")
	var seller uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT owner_id FROM assets WHERE id=$1`, f.source).Scan(&seller); err != nil {
		t.Fatal(err)
	}
	// A media alias owner may also be the operator creating the legal hold.
	// Put that owner first in the sorted lock set to expose the reverse FK edge.
	actor := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,'retention-operator@test.local','retention_operator','Operator','admin')`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE assets SET storage_key=$2 WHERE id=$1`, f.source, uuid.NewString()+".jpg"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 SELECT $1,$2,kind,'Frozen location alias','/private-alias',mime_type,'clean','upload',license_code,storage_backend,$4 FROM assets WHERE id=$3`, uuid.New(), actor, f.source, f.source.String()+".jpg"); err != nil {
		t.Fatal(err)
	}
	deleteMarketplaceAccount(t, pool, f.root, seller)
	job := originalCleanupJob(t, pool, seller)
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err := gate.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, seller); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- datarights.NewService(pool, f.root).HandleMediaCleanupJob(ctx, job) }()
	waitForProductBlockingTx(t, ctx, pool, int32(gate.Conn().PgConn().PID()))
	if _, err := gate.Exec(ctx, `SET LOCAL lock_timeout='500ms'`); err != nil {
		t.Fatal(err)
	}
	_, holdErr := gate.Exec(ctx, `INSERT INTO data_rights_legal_holds(user_id,created_by,reason,authority_reference_hash,review_at,expires_at)
 VALUES($1,$2,'Preserve original evidence',repeat('c',64),now()+interval '1 day',now()+interval '2 days')`, seller, actor)
	if holdErr != nil {
		_ = gate.Rollback(ctx)
		<-done
		t.Fatal("cleanup subject lock blocked hold creator FK", holdErr)
	}
	if err := gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, datarights.ErrHoldCutoff) {
		t.Fatal("cleanup missed newly committed hold", err)
	}
}

func TestDeliveryCleanupDoesNotBlockHoldActorForeignKey(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newPurchasedReferenceFixture(t, pool, "video")
	var seller uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT owner_id FROM assets WHERE id=$1`, f.source).Scan(&seller); err != nil {
		t.Fatal(err)
	}
	actor, subject := seller, f.buyer
	if actor.String() > subject.String() {
		actor, subject = subject, actor
	}
	var job jobs.Job
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('orderId',$2::text),20) RETURNING id,payload`, productdelivery.CleanupJobKind, f.order).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err := gate.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, subject); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- productdelivery.CleanupHandler(pool, media.NewCatalog(media.NewLocalStore(f.root)))(ctx, job)
	}()
	waitForProductBlockingTx(t, ctx, pool, int32(gate.Conn().PgConn().PID()))
	if _, err := gate.Exec(ctx, `SET LOCAL lock_timeout='500ms'`); err != nil {
		t.Fatal(err)
	}
	_, holdErr := gate.Exec(ctx, `INSERT INTO data_rights_legal_holds(user_id,created_by,reason,authority_reference_hash,review_at,expires_at)
 VALUES($1,$2,'Preserve delivery evidence',repeat('d',64),now()+interval '1 day',now()+interval '2 days')`, subject, actor)
	if holdErr != nil {
		_ = gate.Rollback(ctx)
		<-done
		t.Fatal("delivery cleanup blocked hold creator FK", holdErr)
	}
	if err := gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	assertPurchasedBytes(t, pool, f.root, f.buyer, f.purchased, f.content)
}

func TestOriginalMediaReconciliationResumesAfterRetainedBuyerRightsEnd(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	buyer, seller, source, product := newProductCheckoutFixture(t, pool)
	root := paymentTestRoot(t, pool)
	purchase := testutil.SeedLegacyProductOrder(t, pool, buyer, product)
	deleteMarketplaceAccount(t, pool, root, seller)
	service := datarights.NewService(pool, root)
	if n, err := service.ReconcileOriginalMediaCleanups(ctx, 100); err != nil || n != 0 {
		t.Fatal("active buyer caused cleanup dispatch", n, err)
	}
	assertPurchasedBytes(t, pool, root, buyer, purchase.AssetID, []byte("The accepted purchased reference"))
	// Simulate historical loss of cleanup dispatch when the final entitlement
	// ended, without inventing a new deletion request or erasing job evidence.
	if _, err := pool.Exec(ctx, `UPDATE entitlements SET status='refunded',revoked_at=now() WHERE order_id=$1`, purchase.OrderID); err != nil {
		t.Fatal(err)
	}
	if n, err := service.ReconcileOriginalMediaCleanups(ctx, 100); err != nil || n != 1 {
		t.Fatal("missing original cleanup not reconciled", n, err)
	}
	var job jobs.Job
	if err := pool.QueryRow(ctx, `SELECT j.id,j.payload FROM jobs j JOIN original_media_cleanup_reconciliations r ON r.job_id=j.id WHERE r.user_id=$1`, seller).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleMediaCleanupJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err := media.NewLocalStore(root).Stat(ctx, source.String()+".jpg"); !errors.Is(err, media.ErrNotFound) {
		t.Fatal("unneeded original remains", err)
	}
	var receipts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM original_media_cleanup_receipts WHERE owner_id=$1 AND job_id=$2`, seller, job.ID).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatal("physical result not recorded", receipts, err)
	}
}

func TestBuyerCleanupCannotBypassOriginalOwnerReconciliationEvidence(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	buyer, seller, source, product := newProductCheckoutFixture(t, pool)
	root := paymentTestRoot(t, pool)
	purchase := testutil.SeedLegacyProductOrder(t, pool, buyer, product)
	deleteMarketplaceAccount(t, pool, root, seller)
	if _, err := pool.Exec(ctx, `UPDATE entitlements SET status='refunded',revoked_at=now() WHERE order_id=$1`, purchase.OrderID); err != nil {
		t.Fatal(err)
	}
	service := datarights.NewService(pool, root)
	if n, err := service.ReconcileOriginalMediaCleanups(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET completed_at=completed_at+interval '1 minute' WHERE user_id=$1 AND request_type='account_deletion'`, seller); err != nil {
		t.Fatal(err)
	}
	deleteMarketplaceAccount(t, pool, root, buyer)
	if _, err := media.NewLocalStore(root).Stat(ctx, source.String()+".jpg"); err != nil {
		t.Fatal("indirect buyer cleanup bypassed original owner evidence", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests r SET completed_at=receipt.completed_at FROM data_rights_deletion_receipts receipt WHERE receipt.request_id=r.id AND r.user_id=$1`, seller); err != nil {
		t.Fatal(err)
	}
	var job jobs.Job
	if err := pool.QueryRow(ctx, `SELECT j.id,j.payload FROM jobs j JOIN original_media_cleanup_reconciliations r ON r.job_id=j.id WHERE r.user_id=$1`, seller).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleMediaCleanupJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err := media.NewLocalStore(root).Stat(ctx, source.String()+".jpg"); !errors.Is(err, media.ErrNotFound) {
		t.Fatal("verified original was never cleaned", err)
	}
}
