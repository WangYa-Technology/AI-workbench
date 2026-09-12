package jobs_test

import (
	"context"
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
