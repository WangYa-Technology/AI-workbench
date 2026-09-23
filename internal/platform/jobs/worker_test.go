package jobs_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func TestWorkerHeartbeatsLongHandlerWithoutDuplicateClaim(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	repository := jobs.NewRepository(pool)
	jobID, err := repository.Enqueue(context.Background(), "test.long", map[string]bool{"durable": true})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	worker := jobs.NewWorkerWithLease(repository, "heartbeat-worker", logger, 900*time.Millisecond)
	var calls atomic.Int32
	worker.Handle("test.long", func(context.Context, jobs.Job) error {
		calls.Add(1)
		time.Sleep(2200 * time.Millisecond)
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		if err := pool.QueryRow(context.Background(), `SELECT status FROM jobs WHERE id=$1`, jobID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status == "succeeded" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	var status string
	var attempts, renewals int
	if err := pool.QueryRow(context.Background(), `
		SELECT j.status,j.attempts,a.lease_renewals FROM jobs j
		JOIN job_attempts a ON a.job_id=j.id AND a.attempt_number=1 WHERE j.id=$1`, jobID).Scan(&status, &attempts, &renewals); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || attempts != 1 || calls.Load() != 1 || renewals < 2 {
		t.Fatalf("heartbeat execution mismatch: status=%s attempts=%d calls=%d renewals=%d", status, attempts, calls.Load(), renewals)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after cancellation")
	}
}

func TestWorkerDrainsRunningHandlerBeforeReturning(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	repository := jobs.NewRepository(pool)
	if _, err := repository.Enqueue(context.Background(), "test.drain", map[string]bool{"durable": true}); err != nil {
		t.Fatal(err)
	}
	worker := jobs.NewWorkerWithOptions(repository, "drain-worker", slog.New(slog.NewJSONHandler(io.Discard, nil)), time.Second, 1)
	released := make(chan struct{})
	finished := make(chan struct{})
	worker.Handle("test.drain", func(ctx context.Context, _ jobs.Job) error {
		<-released
		close(finished)
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	time.Sleep(900 * time.Millisecond)
	cancel()
	select {
	case <-done:
		t.Fatal("worker returned before running handler drained")
	case <-time.After(100 * time.Millisecond):
	}
	close(released)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("handler did not finish")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not return after handler finished")
	}
}

func TestWorkerRecoversExpiredLeasesWhileAllSlotsBusy(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	repository := jobs.NewRepository(pool)
	if _, err := repository.Enqueue(t.Context(), "test.busy", nil); err != nil {
		t.Fatal(err)
	}
	worker := jobs.NewWorkerWithOptions(repository, "busy-worker", slog.New(slog.NewJSONHandler(io.Discard, nil)), 2*time.Second, 1)
	entered := make(chan struct{})
	worker.Handle("test.busy", func(ctx context.Context, _ jobs.Job) error { close(entered); <-ctx.Done(); return ctx.Err() })
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("worker shutdown blocked")
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("busy handler not claimed")
	}
	orphan, err := repository.Enqueue(t.Context(), "test.orphan", nil)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := repository.Claim(t.Context(), "departed-worker", time.Minute)
	if err != nil || claim.ID != orphan {
		t.Fatal("orphan claim", claim, err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET lease_expires_at=now()-interval '1 minute' WHERE id=$1`, orphan); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var status string
		if err := pool.QueryRow(t.Context(), `SELECT status FROM jobs WHERE id=$1`, orphan).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status == "queued" {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("saturated worker never recovered expired job", status)
		}
	}
	assertAttempt(t, pool, orphan, 1, "lease_expired", "worker_lease_expired", 0)
}

func TestWorkerDoesNotDispatchAfterDelayedClaimExpires(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	repository := jobs.NewRepository(pool)
	id, err := repository.Enqueue(t.Context(), "test.delayed_claim", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET max_attempts=1 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `CREATE FUNCTION delay_claim_for_lease_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(0.2); RETURN NEW; END $$;
 CREATE TRIGGER delay_claim_for_lease_test AFTER INSERT ON job_attempts FOR EACH ROW EXECUTE FUNCTION delay_claim_for_lease_test()`); err != nil {
		t.Fatal(err)
	}
	worker := jobs.NewWorkerWithOptions(repository, "delayed-claim-worker", slog.New(slog.NewJSONHandler(io.Discard, nil)), 150*time.Millisecond, 1)
	var calls atomic.Int32
	worker.Handle("test.delayed_claim", func(context.Context, jobs.Job) error { calls.Add(1); return nil })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	defer func() { cancel(); <-done }()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM jobs WHERE id=$1`, id).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status == "failed" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("expired claim not recovered")
		case <-ticker.C:
		}
	}
	if calls.Load() != 0 {
		t.Fatal("handler ran after claim lease expired", calls.Load())
	}
	assertAttempt(t, pool, id, 1, "lease_expired", "worker_lease_expired", 0)
}

func TestWorkerHeartbeatTimeoutCancelsBlockedHandler(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	repository := jobs.NewRepository(pool)
	id, err := repository.Enqueue(t.Context(), "test.blocked_heartbeat", nil)
	if err != nil {
		t.Fatal(err)
	}
	worker := jobs.NewWorkerWithOptions(repository, "blocked-heartbeat-worker", slog.New(slog.NewJSONHandler(io.Discard, nil)), 900*time.Millisecond, 1)
	entered, cancelled := make(chan struct{}), make(chan struct{})
	worker.Handle("test.blocked_heartbeat", func(ctx context.Context, _ jobs.Job) error {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("handler not entered")
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(t.Context(), `SELECT 1 FROM jobs WHERE id=$1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked renewal did not cancel handler")
	}
	// The cancellation must happen while the row is still locked, without
	// relying on the parent shutdown or a second worker to recover it.
	if ctx.Err() != nil {
		t.Fatal("only parent timeout cancelled handler")
	}
	cancel()
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerFinalizationTimeoutReleasesExecutionSlot(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "complete"
		if failed {
			name = "fail"
		}
		t.Run(name, func(t *testing.T) {
			pool, cleanup := testPool(t)
			defer cleanup()
			repository := jobs.NewRepository(pool)
			id, err := repository.Enqueue(t.Context(), "test.finish_blocked", nil)
			if err != nil {
				t.Fatal(err)
			}
			worker := jobs.NewWorkerWithOptions(repository, "finish-worker", slog.New(slog.NewJSONHandler(io.Discard, nil)), 900*time.Millisecond, 1)
			entered, release, next := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
			worker.Handle("test.finish_blocked", func(ctx context.Context, _ jobs.Job) error {
				close(entered)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-release:
				}
				if failed {
					return errors.New("test handler failure")
				}
				return nil
			})
			worker.Handle("test.next", func(context.Context, jobs.Job) error {
				next <- struct{}{}
				return nil
			})
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			done := make(chan error, 1)
			go func() { done <- worker.Run(ctx) }()
			defer func() { cancel(); <-done }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("handler not entered")
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				cancel()
				_ = tx.Rollback(context.Background())
			}()
			if _, err := tx.Exec(ctx, `SELECT 1 FROM jobs WHERE id=$1 FOR UPDATE`, id); err != nil {
				t.Fatal(err)
			}
			if _, err := repository.Enqueue(ctx, "test.next", nil); err != nil {
				t.Fatal(err)
			}
			close(release)
			select {
			case <-next:
			case <-time.After(3 * time.Second):
				t.Fatal("blocked finalization retained the only execution slot")
			}
			if ctx.Err() != nil {
				t.Fatal("progress depended on worker shutdown")
			}
			// The first job remains running while its lock is held. The failed
			// finalizer must not fabricate a terminal result or erase the attempt.
			assertStatus(t, pool, id, "running")
			cancel()
		})
	}
}
