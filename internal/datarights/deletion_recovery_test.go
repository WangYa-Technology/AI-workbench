package datarights_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

func failedDeletionFixture(t *testing.T, pool *pgxpool.Pool, stage string) (*datarights.Service, uuid.UUID, uuid.UUID, jobs.Job) {
	t.Helper()
	ctx := context.Background()
	owner, token := deletionOwner(t, pool)
	service := datarights.NewService(pool, t.TempDir())
	request, err := service.Create(ctx, owner.ID, token, datarights.CreateInput{RequestType: "account_deletion", IdentityConfirmation: owner.Handle}, "delete-recovery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET execute_after=now()-interval '1 second',cancel_until=now()-interval '1 second' WHERE id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET available_at=now(),max_attempts=1 WHERE kind=$1 AND payload->>'requestId'=$2`, datarights.DeletionJobKind, request.ID.String()); err != nil {
		t.Fatal(err)
	}
	trigger := `CREATE TRIGGER reject_deletion_recovery BEFORE UPDATE OF status ON users FOR EACH ROW WHEN(NEW.status='deleted') EXECUTE FUNCTION reject_deletion_recovery()`
	if stage == "cleanup" {
		trigger = `CREATE TRIGGER reject_deletion_recovery BEFORE INSERT ON data_rights_deletion_receipts FOR EACH ROW EXECUTE FUNCTION reject_deletion_recovery()`
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_deletion_recovery() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private failure evidence'; END $$;`+trigger); err != nil {
		t.Fatal(err)
	}
	job := claimDataRightsJobKind(t, ctx, pool, "deletion-recovery", datarights.DeletionJobKind)
	failure := service.HandleDeletionJob(ctx, job)
	if failure == nil {
		t.Fatal("failure injection did not run")
	}
	if err := jobs.NewRepository(pool).Fail(ctx, job, "deletion-recovery", failure); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DROP FUNCTION reject_deletion_recovery() CASCADE`); err != nil {
		t.Fatal(err)
	}
	return service, owner.ID, request.ID, job
}

func TestDeletionRecoveryPreservesStageAndConcurrentReplay(t *testing.T) {
	for _, stage := range []string{"prepare", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			pool, cleanup := dataRightsTestPool(t)
			defer cleanup()
			ctx := context.Background()
			service, owner, request, original := failedDeletionFixture(t, pool, stage)
			actor := cleanupUser(t, pool, "admin", "active")
			if _, err := service.RetryDeletionJob(ctx, owner, original.ID, exportRetryInput(), "forbidden"); !errors.Is(err, datarights.ErrCleanupForbidden) {
				t.Fatal("ordinary/deleted owner authorized", err)
			}
			before, err := service.Get(ctx, owner, request)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus := "scheduled"
			if stage == "cleanup" {
				wantStatus = "processing"
			}
			if before.Status != wantStatus || before.Receipt != nil {
				t.Fatal("lost original stage", before)
			}
			var wg sync.WaitGroup
			out := make(chan datarights.DeletionJob, 8)
			errs := make(chan error, 8)
			for range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					item, err := service.RetryDeletionJob(ctx, actor, original.ID, exportRetryInput(), "same-recovery")
					out <- item
					errs <- err
				}()
			}
			wg.Wait()
			close(out)
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			var replacement uuid.UUID
			for item := range out {
				if replacement == uuid.Nil {
					replacement = item.ID
				}
				if item.ID != replacement || item.Kind != stage || item.RetryOf == nil || *item.RetryOf != original.ID || item.Status != "queued" {
					t.Fatal("duplicate or incorrect stage", item)
				}
			}
			after, err := service.Get(ctx, owner, request)
			if err != nil || after.Status != before.Status || !after.ExecuteAfter.Equal(before.ExecuteAfter) || !after.CancelUntil.Equal(*before.CancelUntil) {
				t.Fatal("recovery rewrote request", after, err)
			}
			if _, err := service.Cancel(ctx, owner, request, "cancel-late"); !errors.Is(err, datarights.ErrNotCancelable) {
				t.Fatal("late cancellation allowed", err)
			}
			changed := exportRetryInput()
			changed.Reason = "This differs from the original recovery command."
			if _, err := service.RetryDeletionJob(ctx, actor, original.ID, changed, "changed"); !errors.Is(err, datarights.ErrConflict) {
				t.Fatal("changed replay accepted", err)
			}
			retry := claimDataRightsJobKind(t, ctx, pool, "deletion-retry", datarights.DeletionJobKind)
			if retry.ID != replacement || retry.MaxAttempts != 5 {
				t.Fatal(retry)
			}
			if err := service.HandleDeletionJob(ctx, retry); err != nil {
				t.Fatal(err)
			}
			if err := jobs.NewRepository(pool).Complete(ctx, retry, "deletion-retry"); err != nil {
				t.Fatal(err)
			}
			final, err := service.Get(ctx, owner, request)
			if err != nil || final.Status != "completed" || final.Receipt == nil {
				t.Fatal("recovered deletion not completed", final, err)
			}
			again, err := service.RetryDeletionJob(ctx, actor, original.ID, exportRetryInput(), "lost-response")
			if err != nil || again.ID != replacement || again.Status != "succeeded" {
				t.Fatal("replay did not return original replacement", again, err)
			}
			var recoveries, audit, receipts int
			if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM account_deletion_recoveries WHERE request_id=$1),(SELECT count(*) FROM audit_events WHERE action='data_rights.deletion_job_retried'),(SELECT count(*) FROM data_rights_deletion_receipts WHERE request_id=$1)`, request).Scan(&recoveries, &audit, &receipts); err != nil || recoveries != 1 || audit != 1 || receipts != 1 {
				t.Fatal("duplicate evidence", recoveries, audit, receipts, err)
			}
			if _, err := pool.Exec(ctx, `DELETE FROM account_deletion_recoveries WHERE request_id=$1`, request); err == nil {
				t.Fatal("recovery evidence mutable")
			}
			down, err := os.ReadFile("../platform/database/migrations/0100_account_deletion_recovery.down.sql")
			if err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, string(down)); err == nil {
				t.Fatal("migration discarded evidence")
			}
			tx.Rollback(ctx)
		})
	}
}

func TestDeletionRecoveryEligibilityAndPagination(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service, owner, request, original := failedDeletionFixture(t, pool, "prepare")
	actor := cleanupUser(t, pool, "admin", "active")
	check := func(reason string) {
		t.Helper()
		page, err := service.ListDeletionJobs(ctx, datarights.MediaCleanupListInput{})
		if err != nil || len(page.Items) != 1 || page.Items[0].UnavailableReason != reason || page.Items[0].CanRetry != (reason == "") {
			t.Fatal("wrong eligibility", reason, page, err)
		}
		if reason != "" {
			if _, err := service.RetryDeletionJob(ctx, actor, original.ID, exportRetryInput(), "ineligible"); !errors.Is(err, datarights.ErrConflict) {
				t.Fatal("ineligible recovery accepted", reason, err)
			}
		}
	}
	check("")
	for _, state := range []string{"cancelled", "completed", "failed"} {
		if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET status=$2 WHERE id=$1`, request, state); err != nil {
			t.Fatal(err)
		}
		check("request_closed")
	}
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET status='scheduled',cancel_until=now()+interval '1 day' WHERE id=$1`, request); err != nil {
		t.Fatal(err)
	}
	check("grace_period")
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET cancel_until=now()-interval '1 second',execute_after=now()+interval '1 day' WHERE id=$1`, request); err != nil {
		t.Fatal(err)
	}
	check("grace_period")
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET execute_after=now()-interval '1 second' WHERE id=$1`, request); err != nil {
		t.Fatal(err)
	}
	hold, err := service.CreateHold(ctx, actor, datarights.HoldInput{UserID: owner, AuthorityReference: "LEGAL-RECOVERY-CHECK"}, "hold")
	if err != nil {
		t.Fatal(err)
	}
	check("legal_hold")
	if _, err := service.ReleaseHold(ctx, actor, hold.ID); err != nil {
		t.Fatal(err)
	}
	check("active_job")
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='cancelled' WHERE status='queued' AND kind=$1`, datarights.DeletionJobKind); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET status='processing' WHERE id=$1`, request); err != nil {
		t.Fatal(err)
	}
	check("inconsistent_stage")
	if _, err := pool.Exec(ctx, `UPDATE users SET status='deleted' WHERE id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	check("inconsistent_stage") // Missing preparation evidence remains a blocker.
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET status='scheduled' WHERE id=$1`, request); err != nil {
		t.Fatal(err)
	}
	check("inconsistent_stage")
	page, err := service.ListDeletionJobs(ctx, datarights.MediaCleanupListInput{ListInput: datarights.ListInput{Limit: 1}, Status: "all"})
	if err != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatal(page, err)
	}
	next, err := service.ListDeletionJobs(ctx, datarights.MediaCleanupListInput{ListInput: datarights.ListInput{Limit: 1, Cursor: *page.NextCursor}, Status: "all"})
	if err != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID {
		t.Fatal(next, err)
	}
	if _, err := service.ListExportJobs(ctx, datarights.MediaCleanupListInput{ListInput: datarights.ListInput{Cursor: *page.NextCursor}, Status: "all"}); !errors.Is(err, datarights.ErrInvalidList) {
		t.Fatal("cross-queue cursor", err)
	}
	encoded, _ := json.Marshal(next)
	if strings.Contains(string(encoded), "private failure evidence") {
		t.Fatal("raw error disclosed")
	}
}

func TestDeletionRecoveryWaitsForHoldAndPermissionChanges(t *testing.T) {
	for _, scenario := range []string{"hold", "permission", "completed"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := dataRightsTestPool(t)
			defer cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			service, owner, request, original := failedDeletionFixture(t, pool, "prepare")
			actor := cleanupUser(t, pool, "admin", "active")
			gate, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Rollback(context.Background())
			if _, err := gate.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "data-rights-deletion:"+owner.String()); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := service.RetryDeletionJob(ctx, actor, original.ID, exportRetryInput(), "wait")
				done <- err
			}()
			waitDeletionBlocker(t, ctx, pool, gate.Conn().PgConn().PID())
			want := datarights.ErrConflict
			switch scenario {
			case "hold":
				_, err = gate.Exec(ctx, `INSERT INTO data_rights_legal_holds(user_id,created_by,reason,authority_reference_hash,review_at,expires_at) VALUES($1,$2,'Preserve account records.',repeat('a',64),now()+interval '1 day',now()+interval '2 days')`, owner, actor)
			case "permission":
				_, err = gate.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor)
				want = datarights.ErrCleanupForbidden
			case "completed":
				_, err = gate.Exec(ctx, `UPDATE data_rights_requests SET status='completed' WHERE id=$1`, request)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = gate.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-done; !errors.Is(err, want) {
				t.Fatal("stale recovery permitted", err)
			}
			var count int
			if err = pool.QueryRow(ctx, `SELECT count(*) FROM account_deletion_recoveries`).Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
		})
	}
}

func TestRecoveredDeletionRechecksHoldAtExecutionAndSurvivesLeaseLoss(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service, owner, request, original := failedDeletionFixture(t, pool, "prepare")
	actor := cleanupUser(t, pool, "admin", "active")
	replacement, err := service.RetryDeletionJob(ctx, actor, original.ID, exportRetryInput(), "recover")
	if err != nil {
		t.Fatal(err)
	}
	repository := jobs.NewRepository(pool)
	job := claimDataRightsJobKind(t, ctx, pool, "crashed-deletion", datarights.DeletionJobKind)
	if job.ID != replacement.ID {
		t.Fatal(job)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecoverExpired(ctx); err != nil {
		t.Fatal(err)
	}
	job = claimDataRightsJobKind(t, ctx, pool, "resumed-deletion", datarights.DeletionJobKind)
	hold, err := service.CreateHold(ctx, actor, datarights.HoldInput{UserID: owner, AuthorityReference: "LEGAL-AFTER-RECOVERY"}, "hold-after-recovery")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.HandleDeletionJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(ctx, job, "resumed-deletion"); err != nil {
		t.Fatal(err)
	}
	var userStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM users WHERE id=$1`, owner).Scan(&userStatus); err != nil || userStatus != "active" {
		t.Fatal("worker bypassed new legal hold", userStatus, err)
	}
	current, err := service.Get(ctx, owner, request)
	if err != nil || current.Status != "blocked" || current.Receipt != nil {
		t.Fatal("held deletion completed", current, err)
	}
	if _, err := service.ReleaseHold(ctx, actor, hold.ID); err != nil {
		t.Fatal(err)
	}
	job = claimDataRightsJobKind(t, ctx, pool, "released-deletion", datarights.DeletionJobKind)
	if err := service.HandleDeletionJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(ctx, job, "released-deletion"); err != nil {
		t.Fatal(err)
	}
	current, err = service.Get(ctx, owner, request)
	if err != nil || current.Status != "completed" || current.Receipt == nil {
		t.Fatal("released request did not complete", current, err)
	}
}

func TestDeletionRecoverySerializesDifferentFailuresAndPreservesLineage(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service, _, request, original := failedDeletionFixture(t, pool, "prepare")
	actor := cleanupUser(t, pool, "admin", "active")
	var other uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,status,attempts,max_attempts) VALUES($1,jsonb_build_object('requestId',$2::text),'failed',1,1) RETURNING id`, datarights.DeletionJobKind, request).Scan(&other); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan datarights.DeletionJob, 2)
	errs := make(chan error, 2)
	for _, id := range []uuid.UUID{original.ID, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			item, err := service.RetryDeletionJob(ctx, actor, id, exportRetryInput(), "different-original")
			results <- item
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, datarights.ErrConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatal("different failures produced concurrent deletion jobs", success)
	}
	var first datarights.DeletionJob
	for item := range results {
		if item.ID != uuid.Nil {
			first = item
		}
	}
	// Exhaust this replacement through actual worker leases, leaving the
	// original failure and its immutable lineage intact.
	repository := jobs.NewRepository(pool)
	for range 5 {
		if _, err := pool.Exec(ctx, `UPDATE jobs SET available_at=now() WHERE id=$1`, first.ID); err != nil {
			t.Fatal(err)
		}
		job := claimDataRightsJobKind(t, ctx, pool, "repeat-failure", datarights.DeletionJobKind)
		if job.ID != first.ID {
			t.Fatal(job)
		}
		if err := repository.Fail(ctx, job, "repeat-failure", errors.New("storage still unavailable")); err != nil {
			t.Fatal(err)
		}
	}
	input := exportRetryInput()
	attempts := 5
	input.ExpectedAttempts = &attempts
	second, err := service.RetryDeletionJob(ctx, actor, first.ID, input, "second-level")
	if err != nil || second.RetryOf == nil || *second.RetryOf != first.ID {
		t.Fatal(second, err)
	}
	again, err := service.RetryDeletionJob(ctx, actor, *first.RetryOf, exportRetryInput(), "old-replay")
	if err != nil || again.ID != first.ID || again.Status != "failed" || again.RetryJobID == nil || *again.RetryJobID != second.ID {
		t.Fatal("old command redirected or created a new retry", again, err)
	}
}

func TestDeletionRecoveryMigrationPreservesJobsWithoutRecoveryEvidence(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service, _, _, original := failedDeletionFixture(t, pool, "prepare")
	down, err := os.ReadFile("../platform/database/migrations/0100_account_deletion_recovery.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../platform/database/migrations/0100_account_deletion_recovery.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := service.ListDeletionJobs(ctx, datarights.MediaCleanupListInput{})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != original.ID || !page.Items[0].CanRetry {
		t.Fatal("migration lost original failure", page, err)
	}
}
