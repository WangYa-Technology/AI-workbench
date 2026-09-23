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

func exportRecoveryFixture(t *testing.T, pool *pgxpool.Pool, owner uuid.UUID) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	request := uuid.New()
	var job uuid.UUID
	if _, err := pool.Exec(ctx, `INSERT INTO data_rights_requests(id,user_id,request_type,status,subject_ref,execute_after) VALUES($1,$2,'data_export','queued',$3,now())`, request, owner, "subject_"+strings.Repeat("a", 24)); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('requestId',$2::text),1) RETURNING id`, datarights.ExportJobKind, request).Scan(&job); err != nil {
		t.Fatal(err)
	}
	return request, job
}
func exportRetryInput() datarights.MediaCleanupRetryInput {
	attempts := 1
	return datarights.MediaCleanupRetryInput{ExpectedAttempts: &attempts, Reason: "The export failure was investigated and repaired.", Confirmed: true}
}

func TestExportRecoveryFailureProjectionAndConcurrentReplay(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	owner := cleanupUser(t, pool, "member", "active")
	actor := cleanupUser(t, pool, "admin", "active")
	request, original := exportRecoveryFixture(t, pool, owner)
	service := datarights.NewService(pool, t.TempDir())
	repository := jobs.NewRepository(pool)
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_export_recovery_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'secret database path'; END $$;
 CREATE TRIGGER fail_export_recovery_test BEFORE INSERT ON data_rights_export_parts FOR EACH ROW EXECUTE FUNCTION fail_export_recovery_test()`); err != nil {
		t.Fatal(err)
	}
	job, err := repository.Claim(ctx, "export-test", time.Minute)
	if err != nil || job.ID != original {
		t.Fatal(job, err)
	}
	failure := service.HandleExportJob(ctx, job)
	if failure == nil {
		t.Fatal("failure injection did not execute")
	}
	if err = repository.Fail(ctx, job, "export-test", failure); err != nil {
		t.Fatal(err)
	}
	projected, err := service.Get(ctx, owner, request)
	if err != nil || projected.Status != "failed" || projected.FailureCode == nil || *projected.FailureCode != "export_job_failed" {
		t.Fatalf("stuck request: %#v %v", projected, err)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER fail_export_recovery_test ON data_rights_export_parts`); err != nil {
		t.Fatal(err)
	}
	if _, err = service.RetryExportJob(ctx, owner, original, exportRetryInput(), "forbidden"); !errors.Is(err, datarights.ErrCleanupForbidden) {
		t.Fatal("owner gained operator recovery", err)
	}
	var wg sync.WaitGroup
	results := make(chan datarights.ExportJob, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			item, err := service.RetryExportJob(ctx, actor, original, exportRetryInput(), "concurrent-retry")
			results <- item
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var replacement uuid.UUID
	for item := range results {
		if replacement == uuid.Nil {
			replacement = item.ID
		}
		if item.ID != replacement || item.RetryOf == nil || *item.RetryOf != original {
			t.Fatal("duplicate replacement", item)
		}
	}
	var counts int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM data_export_recoveries WHERE request_id=$1`, request).Scan(&counts); err != nil || counts != 1 {
		t.Fatal("recovery evidence", counts, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='data_rights.export_job_retried' AND resource_id=$1`, original).Scan(&counts); err != nil || counts != 1 {
		t.Fatal("audit duplicated", counts, err)
	}
	input := exportRetryInput()
	input.Reason = "A different command must not change the first recovery."
	if _, err = service.RetryExportJob(ctx, actor, original, input, "changed"); !errors.Is(err, datarights.ErrConflict) {
		t.Fatal("changed replay accepted", err)
	}
	projected, err = service.Get(ctx, owner, request)
	if err != nil || projected.Status != "queued" || projected.FailureCode != nil {
		t.Fatal("active replacement hidden", projected, err)
	}
	retry, err := repository.Claim(ctx, "retry-test", time.Minute)
	if err != nil || retry.ID != replacement {
		t.Fatal(retry, err)
	}
	if err = service.HandleExportJob(ctx, retry); err != nil {
		t.Fatal(err)
	}
	if err = repository.Complete(ctx, retry, "retry-test"); err != nil {
		t.Fatal(err)
	}
	body, checksum, err := service.Download(ctx, owner, request)
	if err != nil || !json.Valid(body) {
		t.Fatal("recovered export unavailable", err)
	}
	again, err := service.RetryExportJob(ctx, actor, original, exportRetryInput(), "lost-response")
	if err != nil || again.ID != replacement || again.Status != "succeeded" {
		t.Fatal("lost response replay", again, err)
	}
	after, afterSum, err := service.Download(ctx, owner, request)
	if err != nil || checksum != afterSum || string(body) != string(after) {
		t.Fatal("replay changed file", err)
	}
	page, err := service.ListExportJobs(ctx, datarights.MediaCleanupListInput{Status: "all", Kind: "all", ListInput: datarights.ListInput{Limit: 1}})
	if err != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatal(page, err)
	}
	second, err := service.ListExportJobs(ctx, datarights.MediaCleanupListInput{Status: "all", Kind: "all", ListInput: datarights.ListInput{Limit: 1, Cursor: *page.NextCursor}})
	if err != nil || len(second.Items) != 1 || second.Items[0].ID == page.Items[0].ID {
		t.Fatal("pagination", second, err)
	}
	if _, err = service.ListExportJobs(ctx, datarights.MediaCleanupListInput{Status: "failed", Kind: "all", ListInput: datarights.ListInput{Cursor: *page.NextCursor}}); !errors.Is(err, datarights.ErrInvalidList) {
		t.Fatal("cross-filter cursor", err)
	}
	encoded, _ := json.Marshal(second)
	if strings.Contains(string(encoded), "secret database path") {
		t.Fatal("raw error disclosed")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM data_export_recoveries WHERE original_job_id=$1`, original); err == nil {
		t.Fatal("recovery evidence mutable")
	}
	down, err := os.ReadFile("../platform/database/migrations/0099_data_export_recovery.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(down)); err == nil {
		t.Fatal("migration discarded evidence")
	}
	tx.Rollback(ctx)
}

func TestExportRecoveryLeaseExpiryCancellationAndPurge(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	owner := cleanupUser(t, pool, "member", "active")
	actor := cleanupUser(t, pool, "admin", "active")
	request, original := exportRecoveryFixture(t, pool, owner)
	service := datarights.NewService(pool, t.TempDir())
	repository := jobs.NewRepository(pool)
	job, err := repository.Claim(ctx, "crashed-worker", time.Minute)
	if err != nil || job.ID != original {
		t.Fatal(job, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE jobs SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, original); err != nil {
		t.Fatal(err)
	}
	if err = repository.RecoverExpired(ctx); err != nil {
		t.Fatal(err)
	}
	projected, err := service.Get(ctx, owner, request)
	if err != nil || projected.Status != "failed" {
		t.Fatal("worker crash hidden", projected, err)
	}
	if _, err = service.Cancel(ctx, owner, request, "owner-cancel"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.RetryExportJob(ctx, actor, original, exportRetryInput(), "cancelled-retry"); !errors.Is(err, datarights.ErrConflict) {
		t.Fatal("cancelled consent resurrected", err)
	}
	fresh, _ := exportRecoveryFixture(t, pool, owner)
	running, err := repository.Claim(ctx, "normal-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.HandleExportJob(ctx, running); err != nil {
		t.Fatal(err)
	}
	// The file committed, but the worker died before acknowledging its job.
	if _, err = pool.Exec(ctx, `UPDATE jobs SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, running.ID); err != nil {
		t.Fatal(err)
	}
	if err = repository.RecoverExpired(ctx); err != nil {
		t.Fatal(err)
	}
	projected, err = service.Get(ctx, owner, fresh)
	if err != nil || projected.Status != "ready" {
		t.Fatal("successful file hidden by job acknowledgement loss", projected, err)
	}
	if _, err = service.RetryExportJob(ctx, actor, running.ID, exportRetryInput(), "already-ready"); !errors.Is(err, datarights.ErrConflict) {
		t.Fatal("ready file regenerated", err)
	}
	var expiryJob uuid.UUID
	if err = pool.QueryRow(ctx, `UPDATE jobs SET status='failed',attempts=1,max_attempts=1,last_error_code='handler_failed' WHERE kind=$1 AND payload->>'requestId'=$2 RETURNING id`, datarights.ExportExpiryJobKind, fresh.String()).Scan(&expiryJob); err != nil {
		t.Fatal(err)
	}
	if _, err = service.RetryExportJob(ctx, actor, expiryJob, exportRetryInput(), "early-cleanup"); !errors.Is(err, datarights.ErrConflict) {
		t.Fatal("purge before expiry", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('app.data_rights_maintenance','on',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE data_rights_export_artifacts SET expires_at=now()-interval '1 second' WHERE request_id=$1`, fresh); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	replacement, err := service.RetryExportJob(ctx, actor, expiryJob, exportRetryInput(), "expired-cleanup")
	if err != nil || replacement.Kind != "expiry" {
		t.Fatal(replacement, err)
	}
	retry := claimDataRightsJobKind(t, ctx, pool, "cleanup-worker", datarights.ExportExpiryJobKind)
	if retry.ID != replacement.ID {
		t.Fatal(retry, err)
	}
	if err = service.HandleExportExpiryJob(ctx, retry); err != nil {
		t.Fatal(err)
	}
	if err = repository.Complete(ctx, retry, "cleanup-worker"); err != nil {
		t.Fatal(err)
	}
	var retained int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM data_rights_export_parts WHERE request_id=$1 AND body IS NOT NULL`, fresh).Scan(&retained); err != nil || retained != 0 {
		t.Fatal("expired PII retained", retained, err)
	}
	if _, err = service.RetryExportJob(ctx, actor, running.ID, exportRetryInput(), "regenerate"); !errors.Is(err, datarights.ErrConflict) {
		t.Fatal("closed export regenerated", err)
	}
}

func TestExportRecoveryRechecksAfterRequestLock(t *testing.T) {
	for _, scenario := range []string{"cancel", "permission", "owner"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := dataRightsTestPool(t)
			defer cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			owner := cleanupUser(t, pool, "member", "active")
			actor := cleanupUser(t, pool, "admin", "active")
			request, job := exportRecoveryFixture(t, pool, owner)
			if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed',attempts=1 WHERE id=$1`, job); err != nil {
				t.Fatal(err)
			}
			service := datarights.NewService(pool, t.TempDir())
			blocker, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(context.Background())
			var blockerPID int
			if err = blocker.QueryRow(ctx, `SELECT pg_backend_pid() FROM data_rights_requests WHERE id=$1 FOR UPDATE`, request).Scan(&blockerPID); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := service.RetryExportJob(ctx, actor, job, exportRetryInput(), "wait-and-recheck")
				done <- err
			}()
			for {
				var waiting bool
				if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, blockerPID).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case err = <-done:
					t.Fatalf("recovery did not wait: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			expected := datarights.ErrConflict
			switch scenario {
			case "cancel":
				_, err = blocker.Exec(ctx, `UPDATE data_rights_requests SET status='cancelled' WHERE id=$1`, request)
			case "permission":
				_, err = pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor)
				expected = datarights.ErrCleanupForbidden
			case "owner":
				_, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, owner)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = blocker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-done; !errors.Is(err, expected) {
				t.Fatalf("stale eligibility accepted: %v", err)
			}
			var count int
			if err = pool.QueryRow(ctx, `SELECT count(*) FROM data_export_recoveries WHERE request_id=$1`, request).Scan(&count); err != nil || count != 0 {
				t.Fatal("rejected recovery left evidence", count, err)
			}
		})
	}
}

func TestExportRecoverySerializesDifferentFailuresAndPreservesLineage(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	owner := cleanupUser(t, pool, "member", "active")
	actor := cleanupUser(t, pool, "admin", "active")
	request, first := exportRecoveryFixture(t, pool, owner)
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed',attempts=1 WHERE id=$1`, first); err != nil {
		t.Fatal(err)
	}
	var other uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,status,attempts,max_attempts) VALUES($1,jsonb_build_object('requestId',$2::text),'failed',1,1) RETURNING id`, datarights.ExportJobKind, request).Scan(&other); err != nil {
		t.Fatal(err)
	}
	// The view-only backout with no recovery records preserves original failures.
	for _, direction := range []string{"down", "up"} {
		body, err := os.ReadFile("../platform/database/migrations/0099_data_export_recovery." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, string(body)); err != nil {
			tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	service := datarights.NewService(pool, t.TempDir())
	type result struct {
		item datarights.ExportJob
		err  error
	}
	results := make(chan result, 2)
	for _, id := range []uuid.UUID{first, other} {
		go func(id uuid.UUID) {
			item, err := service.RetryExportJob(ctx, actor, id, exportRetryInput(), "different-originals")
			results <- result{item, err}
		}(id)
	}
	var replacement datarights.ExportJob
	success := 0
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err == nil {
			success++
			replacement = r.item
		} else if !errors.Is(r.err, datarights.ErrConflict) {
			t.Fatal(r.err)
		}
	}
	if success != 1 || replacement.RetryOf == nil {
		t.Fatal("parallel original failures created multiple jobs", success)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed',attempts=1 WHERE id=$1`, replacement.ID); err != nil {
		t.Fatal(err)
	}
	replay, err := service.RetryExportJob(ctx, actor, *replacement.RetryOf, exportRetryInput(), "old-command")
	if err != nil || replay.ID != replacement.ID || replay.Status != "failed" {
		t.Fatal("old command silently created a new retry", replay, err)
	}
	tail, err := service.RetryExportJob(ctx, actor, replacement.ID, exportRetryInput(), "retry-replacement")
	if err != nil || tail.RetryOf == nil || *tail.RetryOf != replacement.ID || tail.ID == replacement.ID {
		t.Fatal("lineage lost", tail, err)
	}
	var links, active int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM data_export_recoveries WHERE request_id=$1),
 (SELECT count(*) FROM jobs WHERE kind='data_rights.export' AND payload->>'requestId'=$1::text AND status IN ('queued','running'))`, request).Scan(&links, &active); err != nil || links != 2 || active != 1 {
		t.Fatal("invalid retry chain", links, active, err)
	}
}
