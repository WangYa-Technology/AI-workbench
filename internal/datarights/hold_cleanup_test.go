package datarights_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

func cleanupHold(t *testing.T, pool *pgxpool.Pool, service *datarights.Service, user uuid.UUID) (uuid.UUID, datarights.Hold) {
	t.Helper()
	actor := cleanupUser(t, pool, "admin", "active")
	hold, err := service.CreateHold(context.Background(), actor, datarights.HoldInput{UserID: user, AuthorityReference: "PRIVATE-CLEANUP-HOLD-REFERENCE"}, "cleanup-hold")
	if err != nil {
		t.Fatal(err)
	}
	return actor, hold
}

func TestEndedHoldCleanupReevaluatesAccountAndPreservesOriginalFailure(t *testing.T) {
	for _, mode := range []string{"release", "expiry", "legacy_expired"} {
		t.Run(mode, func(t *testing.T) {
			pool, cleanup := dataRightsTestPool(t)
			defer cleanup()
			ctx := context.Background()
			root := t.TempDir()
			service := datarights.NewService(pool, root)
			owner := cleanupUser(t, pool, "member", "deleted")
			original := cleanupFailedJob(t, pool, owner)
			actor, hold := cleanupHold(t, pool, service, owner)
			if n, err := service.ResumeLegalHoldCleanups(ctx, 100); err != nil || n != 0 {
				t.Fatal("active hold processed", n, err)
			}
			switch mode {
			case "release":
				if _, err := service.ReleaseHold(ctx, actor, hold.ID); err != nil {
					t.Fatal(err)
				}
			case "expiry":
				makeHoldDue(t, pool, hold.ID)
				if _, err := service.ExpireLegalHolds(ctx, 100); err != nil {
					t.Fatal(err)
				}
			case "legacy_expired":
				makeHoldDue(t, pool, hold.ID)
				if _, err := pool.Exec(ctx, `UPDATE data_rights_legal_holds SET status='expired' WHERE id=$1`, hold.ID); err != nil {
					t.Fatal(err)
				}
			}
			var wg sync.WaitGroup
			results := make(chan error, 4)
			for range 4 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := datarights.NewService(pool, root).ResumeLegalHoldCleanups(ctx, 100)
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
			var originalStatus string
			var attempts, count, audits int
			var complete bool
			if err := pool.QueryRow(ctx, `SELECT j.status,j.attempts,c.completed_at IS NOT NULL,
 (SELECT count(*) FROM legal_hold_cleanup_dispatches WHERE hold_id=c.hold_id),
 (SELECT count(*) FROM audit_events WHERE resource_id=c.hold_id AND action='data_rights.hold_cleanup_scheduled')
 FROM jobs j JOIN legal_hold_cleanup_checks c ON c.hold_id=$2 WHERE j.id=$1`, original, hold.ID).Scan(&originalStatus, &attempts, &complete, &count, &audits); err != nil || originalStatus != "failed" || attempts != 20 || !complete || count != 1 || audits != 1 {
				t.Fatal(originalStatus, attempts, complete, count, audits, err)
			}
			job := claimDataRightsJobKind(t, ctx, pool, "hold-cleanup", datarights.MediaCleanupJobKind)
			if err := service.HandleMediaCleanupJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if err := jobs.NewRepository(pool).Complete(ctx, job, "hold-cleanup"); err != nil {
				t.Fatal(err)
			}
			if n, err := service.ResumeLegalHoldCleanups(ctx, 100); err != nil || n != 0 {
				t.Fatal("repeat dispatch after completion", n, err)
			}
			for _, sql := range []string{`DELETE FROM legal_hold_cleanup_checks`, `UPDATE legal_hold_cleanup_checks SET completed_at=NULL`, `DELETE FROM legal_hold_cleanup_dispatches`, `UPDATE legal_hold_cleanup_dispatches SET job_id=gen_random_uuid()`} {
				if _, err := pool.Exec(ctx, sql); err == nil {
					t.Fatal("evidence mutable", sql)
				}
			}
			down, err := os.ReadFile("../platform/database/migrations/0102_legal_hold_cleanup.down.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard") {
				t.Fatal("down discarded evidence", err)
			}
		})
	}
}

func TestEndedHoldCleanupCoalescesQueuedButNotRunningJobs(t *testing.T) {
	for _, status := range []string{"queued", "running", "failed", "succeeded"} {
		t.Run(status, func(t *testing.T) {
			pool, cleanup := dataRightsTestPool(t)
			defer cleanup()
			ctx := context.Background()
			service := datarights.NewService(pool, t.TempDir())
			owner := cleanupUser(t, pool, "member", "deleted")
			actor, hold := cleanupHold(t, pool, service, owner)
			old := cleanupFailedJob(t, pool, owner)
			if _, err := pool.Exec(ctx, `UPDATE jobs SET status=$2 WHERE id=$1`, old, status); err != nil {
				t.Fatal(err)
			}
			if _, err := service.ReleaseHold(ctx, actor, hold.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := service.ResumeLegalHoldCleanups(ctx, 100); err != nil {
				t.Fatal(err)
			}
			var dispatched uuid.UUID
			var queued int
			if err := pool.QueryRow(ctx, `SELECT d.job_id,(SELECT count(*) FROM jobs WHERE kind=$2 AND payload->>'userId'=$3 AND status='queued') FROM legal_hold_cleanup_dispatches d WHERE d.hold_id=$1`, hold.ID, datarights.MediaCleanupJobKind, owner.String()).Scan(&dispatched, &queued); err != nil || queued != 1 || (dispatched == old) != (status == "queued") {
				t.Fatal(dispatched, old, queued, err)
			}
		})
	}
}

func TestEndedHoldCleanupRechecksReplacementAndAccountStatus(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service := datarights.NewService(pool, t.TempDir())
	active := cleanupUser(t, pool, "member", "active")
	actor, hold := cleanupHold(t, pool, service, active)
	if _, err := service.ReleaseHold(ctx, actor, hold.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResumeLegalHoldCleanups(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var dispatches int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM legal_hold_cleanup_dispatches`).Scan(&dispatches); err != nil || dispatches != 0 {
		t.Fatal("active user's originals queued", dispatches, err)
	}
	owner := cleanupUser(t, pool, "member", "deleted")
	actor, old := cleanupHold(t, pool, service, owner)
	if _, err := service.ReleaseHold(ctx, actor, old.ID); err != nil {
		t.Fatal(err)
	}
	_, fresh := cleanupHold(t, pool, service, owner)
	if n, err := service.ResumeLegalHoldCleanups(ctx, 100); err != nil || n != 0 {
		t.Fatal("replacement ignored", n, err)
	}
	if _, err := service.ReleaseHold(ctx, actor, fresh.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResumeLegalHoldCleanups(ctx, 100); err != nil {
		t.Fatal(err)
	}
	// A hold added after dispatch remains authoritative at execution.
	_, _ = cleanupHold(t, pool, service, owner)
	job := claimDataRightsJobKind(t, ctx, pool, "cleanup-reheld", datarights.MediaCleanupJobKind)
	if err := service.HandleMediaCleanupJob(ctx, job); !errors.Is(err, datarights.ErrHoldCutoff) {
		t.Fatal("worker ignored fresh hold", err)
	}
}

func TestEndedHoldCleanupCompletionAuditFailurePreservesCommittedProgress(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	root := t.TempDir()
	service := datarights.NewService(pool, root)
	owner := cleanupUser(t, pool, "member", "deleted")
	actor, hold := cleanupHold(t, pool, service, owner)
	if _, err := service.ReleaseHold(ctx, actor, hold.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_hold_cleanup_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='data_rights.hold_cleanup_scheduled' THEN RAISE EXCEPTION 'private audit failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER fail_hold_cleanup_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_hold_cleanup_audit()`); err != nil {
		t.Fatal(err)
	}
	if n, err := service.ResumeLegalHoldCleanups(ctx, 100); err == nil || n != 0 {
		t.Fatal(n, err)
	}
	var checks, dispatches, queued int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM legal_hold_cleanup_checks),(SELECT count(*) FROM legal_hold_cleanup_dispatches),(SELECT count(*) FROM jobs WHERE kind=$1)`, datarights.MediaCleanupJobKind).Scan(&checks, &dispatches, &queued); err != nil || checks != 1 || dispatches != 1 || queued != 1 {
		t.Fatal("committed dispatch was lost", checks, dispatches, queued, err)
	}
	if _, err := pool.Exec(ctx, `DROP FUNCTION fail_hold_cleanup_audit() CASCADE`); err != nil {
		t.Fatal(err)
	}
	if n, err := datarights.NewService(pool, root).ResumeLegalHoldCleanups(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

func TestEndedHoldCleanupEmptyMigrationRoundTripAndInvalidLimit(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, direction := range []string{"down", "up"} {
		sql, err := os.ReadFile("../platform/database/migrations/0102_legal_hold_cleanup." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	service := datarights.NewService(pool, t.TempDir())
	for _, limit := range []int{-1, 0, 101} {
		if _, err := service.ResumeLegalHoldCleanups(ctx, limit); !errors.Is(err, datarights.ErrInvalid) {
			t.Fatal(limit, err)
		}
	}
}

func TestEndedHoldCleanupDispatchFailureRollsBackCursorAndJob(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service := datarights.NewService(pool, t.TempDir())
	owner := cleanupUser(t, pool, "member", "deleted")
	actor, hold := cleanupHold(t, pool, service, owner)
	if _, err := service.ReleaseHold(ctx, actor, hold.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_hold_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected dispatch evidence failure'; END $$;
 CREATE TRIGGER fail_hold_dispatch BEFORE INSERT ON legal_hold_cleanup_dispatches FOR EACH ROW EXECUTE FUNCTION fail_hold_dispatch()`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResumeLegalHoldCleanups(ctx, 100); err == nil {
		t.Fatal("injected dispatch did not fail")
	}
	var jobsCount, dispatches int
	var cursorEmpty, complete bool
	if err := pool.QueryRow(ctx, `SELECT after_user_id IS NULL,completed_at IS NOT NULL,(SELECT count(*) FROM jobs WHERE kind=$2),(SELECT count(*) FROM legal_hold_cleanup_dispatches) FROM legal_hold_cleanup_checks WHERE hold_id=$1`, hold.ID, datarights.MediaCleanupJobKind).Scan(&cursorEmpty, &complete, &jobsCount, &dispatches); err != nil || !cursorEmpty || complete || jobsCount != 0 || dispatches != 0 {
		t.Fatal(cursorEmpty, complete, jobsCount, dispatches, err)
	}
	if _, err := pool.Exec(ctx, `DROP FUNCTION fail_hold_dispatch() CASCADE`); err != nil {
		t.Fatal(err)
	}
	if n, err := service.ResumeLegalHoldCleanups(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}
