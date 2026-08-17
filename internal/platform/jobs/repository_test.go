package jobs_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRepositoryDurableLeaseLifecycle(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	repository := jobs.NewRepository(pool)

	id, err := repository.Enqueue(ctx, "test.complete", map[string]string{"value": "durable"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := repository.Claim(ctx, "worker-a", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != id || claimed.Attempts != 1 {
		t.Fatalf("unexpected claim: %#v", claimed)
	}
	if err := repository.Complete(ctx, claimed, "worker-a"); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, pool, id, "succeeded")
	assertAttempt(t, pool, id, 1, "succeeded", "", 0)

	expiredID, err := repository.Enqueue(ctx, "test.recover", map[string]string{"value": "recover"})
	if err != nil {
		t.Fatal(err)
	}
	expiredClaim, err := repository.Claim(ctx, "worker-b", 60*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, expiredID); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecoverExpired(ctx); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, pool, expiredID, "queued")
	assertAttempt(t, pool, expiredID, 1, "lease_expired", "worker_lease_expired", 0)
	if err := repository.Complete(ctx, expiredClaim, "worker-b"); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatalf("stale worker completed recovered job: %v", err)
	}

	restarted, err := repository.Claim(ctx, "worker-c", 90*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.ID != expiredID || restarted.Attempts != 2 || restarted.LeaseToken == expiredClaim.LeaseToken {
		t.Fatalf("restart claim evidence mismatch: %#v", restarted)
	}
	if err := repository.Renew(ctx, restarted, "worker-c", 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecoverExpired(ctx); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, pool, expiredID, "running")
	assertAttempt(t, pool, expiredID, 2, "running", "", 1)
	if err := repository.Complete(ctx, restarted, "worker-c"); err != nil {
		t.Fatal(err)
	}
	assertAttempt(t, pool, expiredID, 2, "succeeded", "", 1)

	failedID, err := repository.Enqueue(ctx, "test.failure", map[string]string{"value": "private"})
	if err != nil {
		t.Fatal(err)
	}
	failedClaim, err := repository.Claim(ctx, "worker-private", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Fail(ctx, failedClaim, "worker-private", errors.New("provider response contains secret-token-value")); err != nil {
		t.Fatal(err)
	}
	var lastError, lastErrorCode string
	if err := pool.QueryRow(ctx, `SELECT last_error,last_error_code FROM jobs WHERE id=$1`, failedID).Scan(&lastError, &lastErrorCode); err != nil {
		t.Fatal(err)
	}
	if lastError != "handler_failed" || lastErrorCode != "handler_failed" {
		t.Fatalf("unsafe failure detail persisted: last_error=%q code=%q", lastError, lastErrorCode)
	}
	assertAttempt(t, pool, failedID, 1, "retry_scheduled", "handler_failed", 0)
	if _, err := pool.Exec(ctx, `UPDATE job_attempts SET error_code='tampered' WHERE job_id=$1`, failedID); err == nil {
		t.Fatal("terminal job attempt evidence was mutable")
	}

	cancelledID, err := repository.Enqueue(ctx, "test.cancel", map[string]string{"value": "cancel"})
	if err != nil {
		t.Fatal(err)
	}
	cancelledClaim, err := repository.Claim(ctx, "worker-cancelled", time.Second)
	if err != nil || cancelledClaim.ID != cancelledID {
		t.Fatalf("claim cancellable job: job=%#v err=%v", cancelledClaim, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='cancelled',updated_at=now() WHERE id=$1`, cancelledID); err != nil {
		t.Fatal(err)
	}
	assertAttempt(t, pool, cancelledID, 1, "cancelled", "job_cancelled", 0)
	var leaseCleared bool
	if err := pool.QueryRow(ctx, `SELECT lease_owner IS NULL AND lease_token IS NULL AND lease_expires_at IS NULL AND last_error_code='job_cancelled' FROM jobs WHERE id=$1`, cancelledID).Scan(&leaseCleared); err != nil || !leaseCleared {
		t.Fatalf("cancelled job retained lease evidence: cleared=%v err=%v", leaseCleared, err)
	}
	if err := repository.Complete(ctx, cancelledClaim, "worker-cancelled"); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatalf("cancelled worker completed job: %v", err)
	}
}

func assertStatus(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, expected string) {
	t.Helper()
	var actual string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM jobs WHERE id=$1`, id).Scan(&actual); err != nil {
		t.Fatal(err)
	}
	if actual != expected {
		t.Fatalf("expected status %q, got %q", expected, actual)
	}
}

func assertAttempt(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, attempt int, expectedStatus, expectedError string, expectedRenewals int) {
	t.Helper()
	var status string
	var errorCode *string
	var renewals int
	var workerHash string
	if err := pool.QueryRow(context.Background(), `SELECT status,error_code,lease_renewals,worker_ref_hash FROM job_attempts WHERE job_id=$1 AND attempt_number=$2`, id, attempt).Scan(&status, &errorCode, &renewals, &workerHash); err != nil {
		t.Fatal(err)
	}
	actualError := ""
	if errorCode != nil {
		actualError = *errorCode
	}
	if status != expectedStatus || actualError != expectedError || renewals != expectedRenewals || len(workerHash) != 64 {
		t.Fatalf("attempt evidence mismatch: status=%q error=%q renewals=%d workerHash=%q", status, actualError, renewals, workerHash)
	}
}

func testPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		baseURL = "postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable"
	}
	admin, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		t.Skipf("PostgreSQL integration database unavailable: %v", err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Skipf("PostgreSQL integration database unavailable: %v", err)
	}
	schema := "test_jobs_" + uuid.NewString()[:8]
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(baseURL)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	pool, err := database.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}
