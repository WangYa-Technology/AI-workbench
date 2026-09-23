package jobs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func TestExpiredOwnerCannotRenewCompleteOrFailBeforeRecovery(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	repository := jobs.NewRepository(pool)
	for _, action := range []string{"renew", "complete", "fail"} {
		t.Run(action, func(t *testing.T) {
			id, err := repository.Enqueue(t.Context(), "test.expired", nil)
			if err != nil {
				t.Fatal(err)
			}
			job, err := repository.Claim(t.Context(), "expired-owner", time.Minute)
			if err != nil || job.ID != id {
				t.Fatal(job, err)
			}
			if _, err := pool.Exec(t.Context(), `UPDATE jobs SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
			switch action {
			case "renew":
				err = repository.Renew(t.Context(), job, "expired-owner", time.Minute)
			case "complete":
				err = repository.Complete(t.Context(), job, "expired-owner")
			case "fail":
				err = repository.Fail(t.Context(), job, "expired-owner", errors.New("stale failure"))
			}
			if !errors.Is(err, jobs.ErrLeaseLost) {
				t.Errorf("expired owner %s accepted: %v", action, err)
			}
			// End this fixture before the next claim; the real recovery closes the
			// original attempt, without giving the stale owner a new lease.
			if _, err := pool.Exec(t.Context(), `UPDATE jobs SET max_attempts=1,lease_expires_at=now()-interval '1 second' WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
			if err := repository.RecoverExpired(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLeaseExpiryIsRecheckedAfterRowLockWait(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	repository := jobs.NewRepository(pool)
	for _, action := range []string{"renew", "complete", "fail"} {
		t.Run(action, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			id, err := repository.Enqueue(ctx, "test.locked_expiry", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE jobs SET max_attempts=1 WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
			job, err := repository.Claim(ctx, "waiting-owner", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if _, err := tx.Exec(ctx, `SELECT 1 FROM jobs WHERE id=$1 FOR UPDATE`, id); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				switch action {
				case "renew":
					done <- repository.Renew(ctx, job, "waiting-owner", time.Minute)
				case "complete":
					done <- repository.Complete(ctx, job, "waiting-owner")
				case "fail":
					done <- repository.Fail(ctx, job, "waiting-owner", errors.New("expired after wait"))
				}
			}()
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			for {
				var waiting, expired bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity a JOIN pg_locks l ON l.pid=a.pid WHERE a.wait_event_type='Lock' AND l.relation='jobs'::regclass),lease_expires_at<=clock_timestamp() FROM jobs WHERE id=$1`, id).Scan(&waiting, &expired); err != nil {
					t.Fatal(err)
				}
				if waiting && expired {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("no expired blocked writer")
				case <-ticker.C:
				}
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, jobs.ErrLeaseLost) {
				t.Fatal("expired waiter retained lease", action, err)
			}
			if err := repository.RecoverExpired(ctx); err != nil {
				t.Fatal(err)
			}
			assertStatus(t, pool, id, "failed")
			assertAttempt(t, pool, id, 1, "lease_expired", "worker_lease_expired", 0)
		})
	}
}

func TestExpiredRecoveryIsBoundedAndPreservesInvalidEvidence(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `WITH claimed AS (
 INSERT INTO jobs(kind,status,attempts,max_attempts,lease_owner,lease_token,lease_expires_at)
 SELECT 'test.batch','running',1,2,'departed',gen_random_uuid(),now()-interval '1 minute' FROM generate_series(1,103)
 RETURNING id,kind,lease_token,lease_expires_at)
 INSERT INTO job_attempts(job_id,attempt_number,job_kind,worker_ref_hash,lease_token,lease_expires_at)
 SELECT id,1,kind,repeat('a',64),lease_token,lease_expires_at FROM claimed`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO jobs(kind,status,lease_expires_at) VALUES('test.broken','running',now()-interval '2 minutes')`); err != nil {
		t.Fatal(err)
	}
	repository := jobs.NewRepository(pool)
	for pass, want := range []int{3, 0} {
		if err := repository.RecoverExpired(ctx); err != nil {
			t.Fatal(err)
		}
		var running, closed, broken int
		if err := pool.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM jobs WHERE kind='test.batch' AND status='running'),
 (SELECT count(*) FROM job_attempts WHERE status='lease_expired'),
 (SELECT count(*) FROM jobs WHERE kind='test.broken' AND status='running' AND lease_token IS NULL)`).Scan(&running, &closed, &broken); err != nil || running != want || closed != 103-want || broken != 1 {
			t.Fatal(pass, running, closed, broken, err)
		}
	}
}
