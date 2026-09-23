package datarights_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

func heldDeletion(t *testing.T, pool *pgxpool.Pool, service *datarights.Service) (uuid.UUID, datarights.Request, datarights.Hold) {
	t.Helper()
	ctx := context.Background()
	owner, token := deletionOwner(t, pool)
	actor := cleanupUser(t, pool, "admin", "active")
	hold, err := service.CreateHold(ctx, actor, datarights.HoldInput{UserID: owner.ID, AuthorityReference: "LEGAL-EXPIRY-TEST"}, "hold-expiry")
	if err != nil {
		t.Fatal(err)
	}
	request, err := service.Create(ctx, owner.ID, token, datarights.CreateInput{RequestType: "account_deletion", IdentityConfirmation: owner.Handle}, "delete-expiry")
	if err != nil || request.Status != "blocked" {
		t.Fatal(request, err)
	}
	// Model the historical case where the initial deletion job acknowledged
	// the hold and completed, so natural expiry must provide a successor.
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='succeeded' WHERE kind=$1 AND payload->>'requestId'=$2`, datarights.DeletionJobKind, request.ID.String()); err != nil {
		t.Fatal(err)
	}
	return owner.ID, request, hold
}

func makeHoldDue(t *testing.T, pool *pgxpool.Pool, holdID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE data_rights_legal_holds SET review_at=now()-interval '2 days',expires_at=now()-interval '1 day' WHERE id=$1`, holdID); err != nil {
		t.Fatal(err)
	}
}

func assertExpiry(t *testing.T, pool *pgxpool.Pool, requestID, holdID uuid.UUID, wantRequest, wantHold string, wantJobs, wantAudit int) {
	t.Helper()
	var rs, hs string
	var queued, audits int
	err := pool.QueryRow(context.Background(), `SELECT r.status,h.status,
 (SELECT count(*) FROM jobs WHERE kind=$3 AND payload->>'requestId'=r.id::text AND status='queued'),
 (SELECT count(*) FROM audit_events WHERE action='data_rights.legal_hold_expired' AND resource_id=h.id)
 FROM data_rights_requests r JOIN data_rights_legal_holds h ON h.id=$2 WHERE r.id=$1`, requestID, holdID, datarights.DeletionJobKind).Scan(&rs, &hs, &queued, &audits)
	if err != nil {
		t.Fatal(err)
	}
	if rs != wantRequest || hs != wantHold || queued != wantJobs || audits != wantAudit {
		t.Fatalf("request=%s hold=%s queued=%d audits=%d; want %s %s %d %d", rs, hs, queued, audits, wantRequest, wantHold, wantJobs, wantAudit)
	}
}

func TestHoldExpiryResumesOncePreservesGraceAndIgnoresReviewDate(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	root := t.TempDir()
	service := datarights.NewService(pool, root)
	_, request, hold := heldDeletion(t, pool, service)
	if _, err := pool.Exec(ctx, `UPDATE data_rights_legal_holds SET review_at=now()-interval '1 day' WHERE id=$1`, hold.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := service.ExpireLegalHolds(ctx, 100); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	assertExpiry(t, pool, request.ID, hold.ID, "blocked", "active", 0, 0)
	makeHoldDue(t, pool, hold.ID)
	// Distinct services represent separate worker processes with independent cursors.
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := datarights.NewService(pool, root).ExpireLegalHolds(ctx, 100)
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
	assertExpiry(t, pool, request.ID, hold.ID, "scheduled", "expired", 1, 1)
	var premature, events int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM jobs WHERE kind=$2 AND payload->>'requestId'=r.id::text AND available_at<greatest(r.execute_after,r.cancel_until)),
 (SELECT count(*) FROM data_rights_events WHERE request_id=r.id AND event_type='legal_hold_expired' AND actor_id IS NULL)
 FROM data_rights_requests r WHERE r.id=$1`, request.ID, datarights.DeletionJobKind).Scan(&premature, &events); err != nil || premature != 0 || events != 1 {
		t.Fatal(premature, events, err)
	}
	if n, err := service.ExpireLegalHolds(ctx, 100); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestHoldExpiryLegacyLinksAndClosedStages(t *testing.T) {
	for _, scenario := range []string{"unlinked", "legacy_expired", "cancelled", "processing", "completed", "queued", "running", "succeeded"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := dataRightsTestPool(t)
			defer cleanup()
			ctx := context.Background()
			service := datarights.NewService(pool, t.TempDir())
			_, request, hold := heldDeletion(t, pool, service)
			makeHoldDue(t, pool, hold.ID)
			wantRequest, wantJobs, wantAudit := "scheduled", 1, 1
			var err error
			switch scenario {
			case "unlinked":
				_, err = pool.Exec(ctx, `UPDATE data_rights_legal_holds SET request_id=NULL WHERE id=$1`, hold.ID)
			case "legacy_expired":
				_, err = pool.Exec(ctx, `UPDATE data_rights_legal_holds SET status='expired' WHERE id=$1`, hold.ID)
				wantAudit = 0
			case "cancelled", "processing", "completed":
				_, err = pool.Exec(ctx, `UPDATE data_rights_requests SET status=$2 WHERE id=$1`, request.ID, scenario)
				wantRequest = scenario
				wantJobs = 0
			case "queued", "running", "succeeded":
				_, err = pool.Exec(ctx, `INSERT INTO jobs(kind,payload,status,available_at) VALUES($1,jsonb_build_object('requestId',$2::text),$3,$4)`, datarights.DeletionJobKind, request.ID, scenario, request.ExecuteAfter)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.ExpireLegalHolds(ctx, 100); err != nil {
				t.Fatal(err)
			}
			assertExpiry(t, pool, request.ID, hold.ID, wantRequest, "expired", wantJobs, wantAudit)
			// A fresh process must not resurrect closed stages or duplicate the successor.
			if n, err := datarights.NewService(pool, t.TempDir()).ExpireLegalHolds(ctx, 100); err != nil || n != 0 {
				t.Fatal(n, err)
			}
		})
	}
}

func TestHoldExpiryReplacementKeepsDeletionBlocked(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service := datarights.NewService(pool, t.TempDir())
	owner, request, old := heldDeletion(t, pool, service)
	makeHoldDue(t, pool, old.ID)
	actor := cleanupUser(t, pool, "admin", "active")
	fresh, err := service.CreateHold(ctx, actor, datarights.HoldInput{UserID: owner, AuthorityReference: "LEGAL-REPLACEMENT"}, "replace")
	if err != nil {
		t.Fatal(err)
	}
	assertExpiry(t, pool, request.ID, old.ID, "blocked", "expired", 0, 1)
	if n, err := service.ExpireLegalHolds(ctx, 100); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	assertExpiry(t, pool, request.ID, fresh.ID, "blocked", "active", 0, 0)
	if _, err := service.ReleaseHold(ctx, actor, fresh.ID); err != nil {
		t.Fatal(err)
	}
	assertExpiry(t, pool, request.ID, fresh.ID, "scheduled", "released", 1, 0)
}

func TestHoldExpirySkipsBusySubjectsAndRotatesBoundedBatches(t *testing.T) {
	for _, lock := range []string{"subject", "user"} {
		t.Run(lock, func(t *testing.T) {
			pool, cleanup := dataRightsTestPool(t)
			defer cleanup()
			ctx := context.Background()
			service := datarights.NewService(pool, t.TempDir())
			owner, first, hold1 := heldDeletion(t, pool, service)
			_, second, hold2 := heldDeletion(t, pool, service)
			makeHoldDue(t, pool, hold1.ID)
			makeHoldDue(t, pool, hold2.ID)
			gate, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Rollback(ctx)
			if lock == "subject" {
				_, err = gate.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "data-rights-deletion:"+owner.String())
			} else {
				_, err = gate.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, owner)
			}
			if err != nil {
				t.Fatal(err)
			}
			passCtx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			if n, err := service.ExpireLegalHolds(passCtx, 1); err != nil || n != 0 {
				t.Fatal(n, err)
			}
			if n, err := service.ExpireLegalHolds(passCtx, 1); err != nil || n != 1 {
				t.Fatal("oldest busy subject starved next batch", n, err)
			}
			assertExpiry(t, pool, first.ID, hold1.ID, "blocked", "active", 0, 0)
			assertExpiry(t, pool, second.ID, hold2.ID, "scheduled", "expired", 1, 1)
			if err := gate.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if n, err := service.ExpireLegalHolds(ctx, 1); err != nil || n != 1 {
				t.Fatal("cursor did not wrap", n, err)
			}
			assertExpiry(t, pool, first.ID, hold1.ID, "scheduled", "expired", 1, 1)
		})
	}
}

func TestHoldExpiryRollsBackAndRetriesWithoutBlockingOtherSubjects(t *testing.T) {
	for _, failure := range []string{"audit", "job"} {
		t.Run(failure, func(t *testing.T) {
			pool, cleanup := dataRightsTestPool(t)
			defer cleanup()
			ctx := context.Background()
			root := t.TempDir()
			service := datarights.NewService(pool, root)
			_, request, hold := heldDeletion(t, pool, service)
			_, other, otherHold := heldDeletion(t, pool, service)
			makeHoldDue(t, pool, hold.ID)
			makeHoldDue(t, pool, otherHold.ID)
			sql := `CREATE FUNCTION fail_expiry() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='data_rights.legal_hold_expired' AND NEW.resource_id='` + hold.ID.String() + `'::uuid THEN RAISE EXCEPTION 'injected expiry audit failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER fail_expiry BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_expiry()`
			table := "audit_events"
			if failure == "job" {
				table = "jobs"
				sql = `CREATE FUNCTION fail_expiry() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='data_rights.delete' AND NEW.payload->>'requestId'='` + request.ID.String() + `' THEN RAISE EXCEPTION 'injected expiry job failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER fail_expiry BEFORE INSERT ON jobs FOR EACH ROW EXECUTE FUNCTION fail_expiry()`
			}
			if _, err := pool.Exec(ctx, sql); err != nil {
				t.Fatal(err)
			}
			if n, err := service.ExpireLegalHolds(ctx, 100); err == nil || n != 1 {
				t.Fatal(n, err)
			}
			assertExpiry(t, pool, request.ID, hold.ID, "blocked", "active", 0, 0)
			assertExpiry(t, pool, other.ID, otherHold.ID, "scheduled", "expired", 1, 1)
			if _, err := pool.Exec(ctx, "DROP TRIGGER fail_expiry ON "+table); err != nil {
				t.Fatal(err)
			}
			// A restart rediscovers the still-due hold, without an exhausted retry job.
			if n, err := datarights.NewService(pool, root).ExpireLegalHolds(ctx, 100); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			assertExpiry(t, pool, request.ID, hold.ID, "scheduled", "expired", 1, 1)
		})
	}
}

func TestHoldExpiryRunsResumedDeletionThroughRealLease(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service := datarights.NewService(pool, t.TempDir())
	owner, request, hold := heldDeletion(t, pool, service)
	makeHoldDue(t, pool, hold.ID)
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET execute_after=now()-interval '1 second',cancel_until=now()-interval '1 second' WHERE id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := service.ExpireLegalHolds(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	repo := jobs.NewRepository(pool)
	for range 1 {
		job := claimDataRightsJobKind(t, ctx, pool, "expiry-worker", datarights.DeletionJobKind)
		if err := service.HandleDeletionJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		if err := repo.Complete(ctx, job, "expiry-worker"); err != nil {
			t.Fatal(err)
		}
	}
	var status string
	var receipts int
	if err := pool.QueryRow(ctx, `SELECT u.status,(SELECT count(*) FROM data_rights_deletion_receipts WHERE request_id=$2) FROM users u WHERE id=$1`, owner, request.ID).Scan(&status, &receipts); err != nil || status != "deleted" || receipts != 1 {
		t.Fatal(status, receipts, err)
	}
}

func TestHoldExpiryRejectsInvalidBatch(t *testing.T) {
	service := datarights.NewService(nil, t.TempDir())
	for _, limit := range []int{-1, 0, 101} {
		if _, err := service.ExpireLegalHolds(context.Background(), limit); !errors.Is(err, datarights.ErrInvalid) {
			t.Fatal(limit, err)
		}
	}
}

func TestHoldExpiryIndexMigrationPreservesEvidence(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service := datarights.NewService(pool, t.TempDir())
	_, request, hold := heldDeletion(t, pool, service)
	makeHoldDue(t, pool, hold.ID)
	if _, err := service.ExpireLegalHolds(ctx, 100); err != nil {
		t.Fatal(err)
	}
	for _, direction := range []string{"down", "up"} {
		sql, err := os.ReadFile("../platform/database/migrations/0101_legal_hold_expiry." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
		assertExpiry(t, pool, request.ID, hold.ID, "scheduled", "expired", 1, 1)
	}
}

func TestHoldExpiryConcurrentReplacementReblocksBeforeDeletion(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	service := datarights.NewService(pool, t.TempDir())
	owner, request, old := heldDeletion(t, pool, service)
	makeHoldDue(t, pool, old.ID)
	actor := cleanupUser(t, pool, "admin", "active")
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET execute_after=now()-interval '1 second',cancel_until=now()-interval '1 second' WHERE id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err := gate.Exec(ctx, `SELECT id FROM data_rights_legal_holds WHERE id=$1 FOR UPDATE`, old.ID); err != nil {
		t.Fatal(err)
	}
	expiryDone := make(chan error, 1)
	go func() { _, err := service.ExpireLegalHolds(ctx, 100); expiryDone <- err }()
	expiryPID := waitDeletionBlocker(t, ctx, pool, gate.Conn().PgConn().PID())
	replacementDone := make(chan error, 1)
	go func() {
		_, err := service.CreateHold(ctx, actor, datarights.HoldInput{UserID: owner, AuthorityReference: "LEGAL-CONCURRENT-REPLACEMENT"}, "replacement")
		replacementDone <- err
	}()
	waitDeletionBlocker(t, ctx, pool, expiryPID)
	if err := gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-expiryDone; err != nil {
		t.Fatal(err)
	}
	if err := <-replacementDone; err != nil {
		t.Fatal(err)
	}
	assertExpiry(t, pool, request.ID, old.ID, "blocked", "expired", 1, 1)
	job := claimDataRightsJobKind(t, ctx, pool, "replacement-worker", datarights.DeletionJobKind)
	if err := service.HandleDeletionJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := jobs.NewRepository(pool).Complete(ctx, job, "replacement-worker"); err != nil {
		t.Fatal(err)
	}
	var status string
	var receipts int
	if err := pool.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM data_rights_deletion_receipts WHERE request_id=$2) FROM users WHERE id=$1`, owner, request.ID).Scan(&status, &receipts); err != nil || status != "active" || receipts != 0 {
		t.Fatal("fresh hold bypassed", status, receipts, err)
	}
}
