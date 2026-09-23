package datarights_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

func missingDeletionFixture(t *testing.T, pool *pgxpool.Pool, stage string) (*datarights.Service, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	owner, token := deletionOwner(t, pool)
	service := datarights.NewService(pool, t.TempDir())
	request, err := service.Create(ctx, owner.ID, token, datarights.CreateInput{RequestType: "account_deletion", IdentityConfirmation: owner.Handle}, "missing-dispatch")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE data_rights_requests SET execute_after=now()-interval '1 second',cancel_until=now()-interval '1 second' WHERE id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	if stage == "cleanup" {
		// Run the real two-stage handler without creating a job-attempt record to
		// represent a historical worker whose dispatch/lease evidence was absent.
		if _, err = pool.Exec(ctx, `CREATE FUNCTION interrupt_deletion_reconciliation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'simulated historical interruption'; END $$;
 CREATE TRIGGER interrupt_deletion_reconciliation BEFORE INSERT ON data_rights_deletion_receipts FOR EACH ROW EXECUTE FUNCTION interrupt_deletion_reconciliation()`); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]any{"requestId": request.ID})
		if err = service.HandleDeletionJob(ctx, jobs.Job{Payload: raw}); err == nil {
			t.Fatal("preparation interruption did not run")
		}
		if _, err = pool.Exec(ctx, `DROP FUNCTION interrupt_deletion_reconciliation() CASCADE`); err != nil {
			t.Fatal(err)
		}
	}
	// Only an unclaimed fixture job is removed. Immutable job-attempt evidence
	// and production failures are never erased to manufacture eligibility.
	if _, err = pool.Exec(ctx, `DELETE FROM jobs WHERE kind=$1 AND payload->>'requestId'=$2 AND status='queued'`, datarights.DeletionJobKind, request.ID.String()); err != nil {
		t.Fatal(err)
	}
	return service, owner.ID, request.ID
}

func TestDeletionReconciliationAtomicDispatchAndCompletion(t *testing.T) {
	for _, stage := range []string{"prepare", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			pool, cleanup := dataRightsTestPool(t)
			defer cleanup()
			ctx := context.Background()
			service, owner, request := missingDeletionFixture(t, pool, stage)
			before, err := service.Get(ctx, owner, request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_deletion_reconciliation_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='data_rights.deletion_reconciled' THEN RAISE EXCEPTION 'private audit outage'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER fail_deletion_reconciliation_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_deletion_reconciliation_audit()`); err != nil {
				t.Fatal(err)
			}
			if _, err := service.ReconcileDeletions(ctx, 100); err == nil {
				t.Fatal("expected audit failure")
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM account_deletion_reconciliations)+(SELECT count(*) FROM jobs WHERE kind=$1)`, datarights.DeletionJobKind).Scan(&count); err != nil || count != 0 {
				t.Fatal("non-atomic dispatch", count, err)
			}
			if _, err := pool.Exec(ctx, `DROP FUNCTION fail_deletion_reconciliation_audit() CASCADE`); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			results := make(chan error, 8)
			for range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := datarights.NewService(pool, t.TempDir()).ReconcileDeletions(ctx, 100)
					results <- err
				}()
			}
			wg.Wait()
			close(results)
			for err := range results {
				if err != nil {
					t.Fatal(err)
				}
			}
			var jobID uuid.UUID
			var recordedStage string
			var previous *uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT job_id,stage,previous_job_id FROM account_deletion_reconciliations WHERE request_id=$1`, request).Scan(&jobID, &recordedStage, &previous); err != nil || recordedStage != stage || previous != nil {
				t.Fatal("wrong dispatch proof", recordedStage, previous, err)
			}
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind=$1`, datarights.DeletionJobKind).Scan(&count); err != nil || count != 1 {
				t.Fatal("duplicate dispatch", count, err)
			}
			after, err := service.Get(ctx, owner, request)
			if err != nil || after.Status != before.Status || !after.ExecuteAfter.Equal(before.ExecuteAfter) || !after.CancelUntil.Equal(*before.CancelUntil) || after.Version != before.Version {
				t.Fatal("reconciliation rewrote original request", err)
			}
			for _, sql := range []string{`UPDATE account_deletion_reconciliations SET stage='prepare'`, `DELETE FROM account_deletion_reconciliations`} {
				if _, err := pool.Exec(ctx, sql); err == nil {
					t.Fatal("mutable evidence")
				}
			}
			down, err := os.ReadFile("../platform/database/migrations/0104_account_deletion_reconciliation.down.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard account deletion reconciliation evidence") {
				t.Fatal("unsafe rollback", err)
			}
			// Private exports expose only this request owner's dispatch linkage.
			foreign := cleanupUser(t, pool, "member", "active")
			for _, subject := range []uuid.UUID{owner, foreign} {
				exportID, exportJobID := exportRecoveryFixture(t, pool, subject)
				raw, _ := json.Marshal(map[string]any{"requestId": exportID})
				if stage == "cleanup" && subject == owner {
					if err := service.HandleExportJob(ctx, jobs.Job{ID: exportJobID, Payload: raw}); !errors.Is(err, datarights.ErrNotReady) {
						t.Fatal("deleted owner gained a readable export", err)
					}
					continue
				}
				if err := service.HandleExportJob(ctx, jobs.Job{ID: exportJobID, Payload: raw}); err != nil {
					t.Fatal(err)
				}
				file, _, err := service.OpenExport(ctx, subject, exportID)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(file)
				file.Close()
				if err != nil {
					t.Fatal(err)
				}
				var pkg struct {
					Data struct {
						Rows []map[string]any `json:"accountDeletionReconciliations"`
					}
				}
				if err := json.Unmarshal(body, &pkg); err != nil {
					t.Fatal(err)
				}
				if subject == owner {
					if len(pkg.Data.Rows) != 1 || pkg.Data.Rows[0]["jobId"] != jobID.String() || pkg.Data.Rows[0]["stage"] != stage {
						t.Fatal("missing owner evidence", pkg.Data.Rows)
					}
				} else if len(pkg.Data.Rows) != 0 {
					t.Fatal("foreign deletion evidence leaked")
				}
			}
			job := claimDataRightsJobKind(t, ctx, pool, "reconciliation", datarights.DeletionJobKind)
			if job.ID != jobID || job.MaxAttempts != 5 {
				t.Fatal("wrong claimed successor", job.ID)
			}
			if err := service.HandleDeletionJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if err := jobs.NewRepository(pool).Complete(ctx, job, "reconciliation"); err != nil {
				t.Fatal(err)
			}
			after, err = service.Get(ctx, owner, request)
			if err != nil || after.Status != "completed" || after.Receipt == nil {
				t.Fatal("deletion incomplete", err)
			}
			if n, err := service.ReconcileDeletions(ctx, 100); err != nil || n != 0 {
				t.Fatal("completed request redispatched", n, err)
			}
		})
	}
}

func TestDeletionReconciliationGuards(t *testing.T) {
	for _, scenario := range []string{"queued", "running", "failed", "cancelled_job", "future_execution", "future_cancellation", "missing_deadline", "request_cancelled", "request_completed", "hold", "missing_request_evidence", "wrong_owner_evidence", "processing_active_user", "processing_without_preparation", "prepared_but_reverted"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := dataRightsTestPool(t)
			defer cleanup()
			ctx := context.Background()
			service, owner, request := missingDeletionFixture(t, pool, "prepare")
			exec := func(sql string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(ctx, sql, args...); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "queued", "running", "failed", "cancelled_job":
				status := scenario
				if status == "cancelled_job" {
					status = "cancelled"
				}
				exec(`INSERT INTO jobs(kind,payload,status,max_attempts) VALUES($1,jsonb_build_object('requestId',$2::text),$3,5)`, datarights.DeletionJobKind, request, status)
			case "future_execution":
				exec(`UPDATE data_rights_requests SET execute_after=now()+interval '1 hour' WHERE id=$1`, request)
			case "future_cancellation":
				exec(`UPDATE data_rights_requests SET cancel_until=now()+interval '1 hour' WHERE id=$1`, request)
			case "missing_deadline":
				exec(`UPDATE data_rights_requests SET cancel_until=NULL WHERE id=$1`, request)
			case "request_cancelled":
				exec(`UPDATE data_rights_requests SET status='cancelled' WHERE id=$1`, request)
			case "request_completed":
				exec(`UPDATE data_rights_requests SET status='completed',completed_at=now() WHERE id=$1`, request)
			case "hold":
				actor := cleanupUser(t, pool, "admin", "active")
				if _, err := service.CreateHold(ctx, actor, datarights.HoldInput{UserID: owner, AuthorityReference: "PRIVATE-RECONCILIATION-HOLD"}, "hold"); err != nil {
					t.Fatal(err)
				}
			case "missing_request_evidence", "wrong_owner_evidence":
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(ctx)
				if _, err = tx.Exec(ctx, `SELECT set_config('app.data_rights_maintenance','on',true)`); err != nil {
					t.Fatal(err)
				}
				sql := `DELETE FROM data_rights_events WHERE request_id=$1 AND event_type='requested'`
				if scenario == "wrong_owner_evidence" {
					sql = `UPDATE data_rights_events SET actor_id=NULL WHERE request_id=$1 AND event_type='requested'`
				}
				if _, err = tx.Exec(ctx, sql, request); err != nil {
					t.Fatal(err)
				}
				if err = tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			case "processing_active_user":
				exec(`UPDATE data_rights_requests SET status='processing' WHERE id=$1`, request)
			case "processing_without_preparation":
				exec(`UPDATE data_rights_requests SET status='processing' WHERE id=$1`, request)
				exec(`UPDATE users SET status='deleted' WHERE id=$1`, owner)
			case "prepared_but_reverted":
				exec(`INSERT INTO data_rights_events(request_id,event_type,to_status,reason) VALUES($1,'deletion_prepared','processing','Historical preparation must not be restarted')`, request)
			}
			if n, err := service.ReconcileDeletions(ctx, 100); err != nil || n != 0 {
				t.Fatal("unsafe request scheduled", n, err)
			}
		})
	}
}

func TestDeletionReconciliationFairnessAndSuccessfulPredecessor(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service, firstOwner, first := missingDeletionFixture(t, pool, "prepare")
	_, _, second := missingDeletionFixture(t, pool, "prepare")
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(ctx)
	if _, err = gate.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "data-rights-deletion:"+firstOwner.String()); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{0, 101} {
		if _, err := service.ReconcileDeletions(ctx, limit); !errors.Is(err, datarights.ErrInvalid) {
			t.Fatal("invalid batch", err)
		}
	}
	if n, err := service.ReconcileDeletions(ctx, 1); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if n, err := service.ReconcileDeletions(ctx, 1); err != nil || n != 1 {
		t.Fatal("busy account starved next request", n, err)
	}
	if err = gate.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := datarights.NewService(pool, t.TempDir()).ReconcileDeletions(ctx, 1); err != nil || n != 1 {
		t.Fatal("restart missed request", n, err)
	}
	var previous uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT job_id FROM account_deletion_reconciliations WHERE request_id=$1`, second).Scan(&previous); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='running' WHERE id=$1`, previous); err != nil {
		t.Fatal(err)
	}
	if n, err := service.ReconcileDeletions(ctx, 100); err != nil || n != 0 {
		t.Fatal("running job duplicated", n, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='succeeded' WHERE id=$1`, previous); err != nil {
		t.Fatal(err)
	}
	if n, err := service.ReconcileDeletions(ctx, 100); err != nil || n != 1 {
		t.Fatal("successful no-op never revisited", n, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM account_deletion_reconciliations WHERE request_id=$1 AND previous_job_id=$2`, second, previous).Scan(&count); err != nil || count != 1 {
		t.Fatal("missing successor lineage", count, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM account_deletion_reconciliations WHERE request_id=$1`, first).Scan(&count); err != nil || count != 1 {
		t.Fatal("first request duplicated", count, err)
	}
}

func TestDeletionReconciliationExecutionAndRecoveryRecheckEvidence(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service, _, request := missingDeletionFixture(t, pool, "prepare")
	actor := cleanupUser(t, pool, "admin", "active")
	if n, err := service.ReconcileDeletions(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	for generation := 0; generation < 2; generation++ {
		if _, err := pool.Exec(ctx, `UPDATE jobs SET max_attempts=1 WHERE kind=$1 AND payload->>'requestId'=$2 AND status='queued'`, datarights.DeletionJobKind, request.String()); err != nil {
			t.Fatal(err)
		}
		job := claimDataRightsJobKind(t, ctx, pool, "evidence-recheck", datarights.DeletionJobKind)
		if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET cancel_until=NULL WHERE id=$1`, request); err != nil {
			t.Fatal(err)
		}
		failure := service.HandleDeletionJob(ctx, job)
		if !errors.Is(failure, datarights.ErrInvalid) {
			t.Fatal("execution bypassed missing evidence", failure)
		}
		if err := jobs.NewRepository(pool).Fail(ctx, job, "evidence-recheck", failure); err != nil {
			t.Fatal(err)
		}
		page, err := service.ListDeletionJobs(ctx, datarights.MediaCleanupListInput{Status: "failed"})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range page.Items {
			if item.ID == job.ID {
				found = true
				if item.CanRetry || item.UnavailableReason != "inconsistent_stage" {
					t.Fatal("unsafe recovery projection", item)
				}
			}
		}
		if !found {
			t.Fatal("failed job missing")
		}
		if _, err := service.RetryDeletionJob(ctx, actor, job.ID, exportRetryInput(), "blocked"); !errors.Is(err, datarights.ErrConflict) {
			t.Fatal("recovery bypassed missing evidence", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET cancel_until=now()-interval '1 second' WHERE id=$1`, request); err != nil {
			t.Fatal(err)
		}
		if _, err := service.RetryDeletionJob(ctx, actor, job.ID, exportRetryInput(), "restored"); err != nil {
			t.Fatal(err)
		}
	}
	job := claimDataRightsJobKind(t, ctx, pool, "evidence-restored", datarights.DeletionJobKind)
	if err := service.HandleDeletionJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := jobs.NewRepository(pool).Complete(ctx, job, "evidence-restored"); err != nil {
		t.Fatal(err)
	}
}

func TestDeletionReconciliationDefersNewGraceAndHold(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service, owner, request := missingDeletionFixture(t, pool, "prepare")
	if n, err := service.ReconcileDeletions(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	job := claimDataRightsJobKind(t, ctx, pool, "grace-recheck", datarights.DeletionJobKind)
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET cancel_until=now()+interval '1 hour' WHERE id=$1`, request); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleDeletionJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := jobs.NewRepository(pool).Complete(ctx, job, "grace-recheck"); err != nil {
		t.Fatal(err)
	}
	if n, err := service.ReconcileDeletions(ctx, 100); err != nil || n != 0 {
		t.Fatal("grace ignored", n, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET cancel_until=now()-interval '1 second' WHERE id=$1`, request); err != nil {
		t.Fatal(err)
	}
	if n, err := service.ReconcileDeletions(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	job = claimDataRightsJobKind(t, ctx, pool, "hold-recheck", datarights.DeletionJobKind)
	actor := cleanupUser(t, pool, "admin", "active")
	hold, err := service.CreateHold(ctx, actor, datarights.HoldInput{UserID: owner, AuthorityReference: "LATE-RECONCILIATION-HOLD"}, "hold")
	if err != nil {
		t.Fatal(err)
	}
	if err = service.HandleDeletionJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err = jobs.NewRepository(pool).Complete(ctx, job, "hold-recheck"); err != nil {
		t.Fatal(err)
	}
	current, err := service.Get(ctx, owner, request)
	if err != nil || current.Status != "blocked" || current.Receipt != nil {
		t.Fatal("hold bypassed", err)
	}
	if _, err = service.ReleaseHold(ctx, actor, hold.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := service.ReconcileDeletions(ctx, 100); err != nil || n != 0 {
		t.Fatal("hold successor duplicated", n, err)
	}
	successor := claimDataRightsJobKind(t, ctx, pool, "hold-released", datarights.DeletionJobKind)
	if err := service.HandleDeletionJob(ctx, successor); err != nil {
		t.Fatal(err)
	}
	if err := jobs.NewRepository(pool).Complete(ctx, successor, "hold-released"); err != nil {
		t.Fatal(err)
	}
}

func TestDeletionReconciliationEmptyMigrationRoundTrip(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	for _, direction := range []string{"down", "up"} {
		sql, err := os.ReadFile("../platform/database/migrations/0104_account_deletion_reconciliation." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(context.Background(), string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if n, err := datarights.NewService(pool, t.TempDir()).ReconcileDeletions(ctx, 100); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestDeletionReconciliationPreservesActualFailedAttempts(t *testing.T) {
	for _, stage := range []string{"prepare", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			pool, cleanup := dataRightsTestPool(t)
			defer cleanup()
			ctx := context.Background()
			service, _, _, job := failedDeletionFixture(t, pool, stage)
			if n, err := service.ReconcileDeletions(ctx, 100); err != nil || n != 0 {
				t.Fatal("failed job automatically reset", n, err)
			}
			var status string
			var attempts int
			if err := pool.QueryRow(ctx, `SELECT status,attempts FROM jobs WHERE id=$1`, job.ID).Scan(&status, &attempts); err != nil || status != "failed" || attempts != 1 {
				t.Fatal("failure evidence changed", status, attempts, err)
			}
		})
	}
}

func TestDeletionReconciliationCancellationWinsAfterDispatch(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service, owner, request := missingDeletionFixture(t, pool, "prepare")
	if n, err := service.ReconcileDeletions(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	job := claimDataRightsJobKind(t, ctx, pool, "cancel-reconciliation", datarights.DeletionJobKind)
	// A restored cancellation window must protect the owner even when a worker
	// already claimed the job; cancellation does not reset running job evidence.
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET cancel_until=now()+interval '1 hour' WHERE id=$1`, request); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Cancel(ctx, owner, request, "cancel"); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleDeletionJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := jobs.NewRepository(pool).Complete(ctx, job, "cancel-reconciliation"); err != nil {
		t.Fatal(err)
	}
	if n, err := service.ReconcileDeletions(ctx, 100); err != nil || n != 0 {
		t.Fatal("cancelled request revived", n, err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM users WHERE id=$1`, owner).Scan(&status); err != nil || status != "active" {
		t.Fatal("cancelled owner erased", status, err)
	}
}
