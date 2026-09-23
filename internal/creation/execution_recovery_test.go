package creation_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

func expireGenerationJob(t *testing.T, pool *pgxpool.Pool, job jobs.Job) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := jobs.NewRepository(pool).RecoverExpired(ctx); err != nil {
		t.Fatal(err)
	}
}

func assertExecutionOutcome(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, status, reservation string, charges, assets, evidence int) {
	t.Helper()
	var gotStatus, gotReservation string
	var gotCharges, gotAssets, gotEvidence int
	err := pool.QueryRow(context.Background(), `SELECT g.status,p.status,
 (SELECT count(*) FROM point_entries WHERE operation_id=g.id AND entry_type='generation_charge'),
 (SELECT count(*) FROM assets WHERE source_id=g.id AND source_type='generation'),
 (SELECT count(*) FROM jobs WHERE kind=$2 AND payload->>'generationId'=g.id::text)
 FROM generations g JOIN point_reservations p ON p.generation_id=g.id WHERE g.id=$1`, id, creation.FailureEvidenceJobKind).
		Scan(&gotStatus, &gotReservation, &gotCharges, &gotAssets, &gotEvidence)
	if err != nil || gotStatus != status || gotReservation != reservation || gotCharges != charges || gotAssets != assets || gotEvidence != evidence {
		t.Fatalf("outcome %s/%s charges=%d assets=%d evidence=%d error=%v", gotStatus, gotReservation, gotCharges, gotAssets, gotEvidence, err)
	}
}

func TestGenerationRecoveryCannotDeferAnInFlightRecovery(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	owner := lifecycleOwner(t, pool)
	runtime := newLifecycleRuntime(t)
	stores := media.NewCatalog(media.NewLocalStore(t.TempDir()))
	catalog := creation.NewRuntimeCatalog(runtime)
	svc := creation.NewServiceWithMedia(pool, stores, catalog)
	g, err := svc.SubmitCommand(ctx, owner, creation.SubmitInput{Mode: "image", Prompt: "Recover without competing deferral"}, "concurrent-recovery-gate", "test")
	if err != nil {
		t.Fatal(err)
	}
	job := testutil.FinalGenerationAttempt(t, pool, lifecycleJob(t, pool, g.ID))
	expireGenerationJob(t, pool, job)
	traced, entered, release := testutil.GateQuery(t, pool, "SELECT status FROM users WHERE id=$1 FOR SHARE SKIP LOCKED")
	first := creation.NewServiceWithMedia(traced, stores, catalog)
	type outcome struct {
		count int
		err   error
	}
	done := make(chan outcome, 1)
	go func() { n, err := first.ReconcileExecutions(ctx, 1); done <- outcome{n, err} }()
	select {
	case <-entered:
	case <-ctx.Done():
		release()
		<-done
		t.Fatal("first recovery did not reach the account-locked boundary")
	}
	n, secondErr := svc.ReconcileExecutions(ctx, 1)
	var due bool
	readErr := pool.QueryRow(ctx, `SELECT recovery_after<=clock_timestamp() FROM generation_executions WHERE generation_id=$1`, g.ID).Scan(&due)
	release()
	result := <-done
	if secondErr != nil || n != 0 || readErr != nil || !due || result.err != nil || result.count != 1 {
		t.Fatalf("in-flight recovery was deferred: second=%d/%v due=%t/%v first=%d/%v", n, secondErr, due, readErr, result.count, result.err)
	}
	assertExecutionOutcome(t, pool, g.ID, "failed", "released", 0, 0, 1)
	if runtime.calls.Load() != 0 {
		t.Fatal("recovery called generation provider")
	}
}

func TestGenerationExecutionExpiryRecoveryAndManualRetry(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	owner := lifecycleOwner(t, pool)
	runtime := newLifecycleRuntime(t)
	svc := creation.NewServiceWithMedia(pool, media.NewCatalog(media.NewLocalStore(t.TempDir())), creation.NewRuntimeCatalog(runtime))
	g, err := svc.SubmitCommand(ctx, owner, creation.SubmitInput{Mode: "image", Prompt: "Lease recovery"}, "lease-recovery", "test")
	if err != nil {
		t.Fatal(err)
	}
	job := lifecycleJob(t, pool, g.ID)
	// Keep one retry available: neither recovery nor a stale attempt may fail or release it.
	expireGenerationJob(t, pool, job)
	if n, err := svc.ReconcileExecutions(ctx, 100); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err := svc.HandleJob(ctx, job); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatal("stale dispatch", err)
	}
	assertExecutionOutcome(t, pool, g.ID, "queued", "held", 0, 0, 0)
	current := testutil.FinalGenerationAttempt(t, pool, lifecycleJob(t, pool, g.ID))
	if current.LeaseToken == job.LeaseToken {
		t.Fatal("retry did not rotate token")
	}
	if _, err = pool.Exec(ctx, `UPDATE generations SET status='running',progress=35 WHERE id=$1`, g.ID); err != nil {
		t.Fatal(err)
	}
	expireGenerationJob(t, pool, current)
	// Competing maintenance passes must produce one release and one evidence job.
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := svc.ReconcileExecutions(ctx, 100); results <- err }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertExecutionOutcome(t, pool, g.ID, "failed", "released", 0, 0, 1)
	if n, err := svc.ReconcileExecutions(ctx, 100); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	evidence := jobs.Job{Kind: creation.FailureEvidenceJobKind, Payload: current.Payload}
	for i := 0; i < 2; i++ {
		if err := svc.HandleJob(ctx, evidence); err != nil {
			t.Fatal(err)
		}
	}
	var notices, audits, reserved int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM notifications WHERE resource_id=$1 AND kind='generation.failed'),
 (SELECT count(*) FROM audit_events WHERE resource_id=$1 AND action='generation.failed'),
 (SELECT reserved_points FROM point_accounts WHERE user_id=$2)`, g.ID, owner).Scan(&notices, &audits, &reserved); err != nil || notices != 1 || audits != 1 || reserved != 0 {
		t.Fatal(notices, audits, reserved, err)
	}
	if runtime.calls.Load() != 0 {
		t.Fatal("expired worker dispatched")
	}
	retry, err := svc.Retry(ctx, owner, g.ID, "manual-after-worker-crash", "test")
	if err != nil || retry.ID == g.ID {
		t.Fatal(retry, err)
	}
	retryJob := lifecycleJob(t, pool, retry.ID)
	if err = svc.HandleJob(ctx, retryJob); err != nil {
		t.Fatal(err)
	}
	assertExecutionOutcome(t, pool, retry.ID, "succeeded", "captured", 1, 1, 0)
	assertExecutionOutcome(t, pool, g.ID, "failed", "released", 0, 0, 1)
	// A result may commit just before a worker exits without acknowledging the
	// queue job. Recovering that queue failure must preserve the successful charge.
	retryJob = testutil.FinalGenerationAttempt(t, pool, retryJob)
	expireGenerationJob(t, pool, retryJob)
	if n, err := svc.ReconcileExecutions(ctx, 100); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	assertExecutionOutcome(t, pool, retry.ID, "succeeded", "captured", 1, 1, 0)
}

func TestGenerationExecutionLateProviderAndStorageResultsAreFenced(t *testing.T) {
	for _, phase := range []string{"image_success", "chat_success", "provider_failure", "storage_success", "new_attempt"} {
		t.Run(phase, func(t *testing.T) {
			pool, cleanup := testPool(t)
			defer cleanup()
			ctx := context.Background()
			owner := lifecycleOwner(t, pool)
			runtime := newLifecycleRuntime(t)
			store := &outputFaultStore{Store: media.NewLocalStore(t.TempDir())}
			svc := creation.NewServiceWithMedia(pool, media.NewCatalog(store), creation.NewRuntimeCatalog(runtime))
			mode := "image"
			if phase == "chat_success" {
				mode = "chat"
			}
			g, err := svc.SubmitCommand(ctx, owner, creation.SubmitInput{Mode: mode, Prompt: "Late execution"}, "late-execution", "test")
			if err != nil {
				t.Fatal(err)
			}
			job := lifecycleJob(t, pool, g.ID)
			if phase != "new_attempt" {
				job = testutil.FinalGenerationAttempt(t, pool, job)
			}
			var replacement jobs.Job
			interrupt := func(context.Context) error {
				expireGenerationJob(t, pool, job)
				if phase == "new_attempt" {
					replacement = lifecycleJob(t, pool, g.ID)
				}
				return nil
			}
			if phase == "storage_success" {
				store.afterPut = func() error { return interrupt(ctx) }
			} else {
				runtime.change = interrupt
			}
			if phase == "provider_failure" {
				runtime.failure = creation.NewProviderFailure("provider_rate_limited", 0)
			}
			if err = svc.HandleJob(ctx, job); !errors.Is(err, jobs.ErrLeaseLost) {
				t.Fatal("late result not fenced", err)
			}
			assertExecutionOutcome(t, pool, g.ID, "running", "held", 0, 0, 0)
			if phase == "new_attempt" {
				if n, err := svc.ReconcileExecutions(ctx, 100); err != nil || n != 0 {
					t.Fatal(n, err)
				}
				runtime.change = nil
				if err := svc.HandleJob(ctx, replacement); err != nil {
					t.Fatal(err)
				}
				assertExecutionOutcome(t, pool, g.ID, "succeeded", "captured", 1, 1, 0)
			} else {
				if n, err := svc.ReconcileExecutions(ctx, 100); err != nil || n != 1 {
					t.Fatal(n, err)
				}
				assertExecutionOutcome(t, pool, g.ID, "failed", "released", 0, 0, 1)
				if phase == "storage_success" {
					_, _, state := outputIntentFor(t, pool, g.ID)
					if state != "pending" {
						t.Fatal("lost file cleanup evidence", state)
					}
				}
			}
		})
	}
}

func TestGenerationExecutionRequiresDurableIdentityAndAttempt(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	owner := lifecycleOwner(t, pool)
	runtime := newLifecycleRuntime(t)
	svc := creation.NewServiceWithMedia(pool, media.NewCatalog(media.NewLocalStore(t.TempDir())), creation.NewRuntimeCatalog(runtime))
	g, err := svc.SubmitCommand(ctx, owner, creation.SubmitInput{Mode: "chat", Prompt: "Identity"}, "execution-identity", "test")
	if err != nil {
		t.Fatal(err)
	}
	job := lifecycleJob(t, pool, g.ID)
	for _, variant := range []string{"missing_id", "missing_token", "wrong_id", "wrong_token", "wrong_kind"} {
		bad := job
		switch variant {
		case "missing_id":
			bad.ID = uuid.Nil
		case "missing_token":
			bad.LeaseToken = uuid.Nil
		case "wrong_id":
			bad.ID = uuid.New()
		case "wrong_token":
			bad.LeaseToken = uuid.New()
		case "wrong_kind":
			bad.Kind = "unknown"
		}
		if err := svc.HandleJob(ctx, bad); !errors.Is(err, jobs.ErrLeaseLost) {
			t.Fatal(variant, err)
		}
	}
	// An unrelated failed duplicate cannot finalize this generation.
	var duplicate uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,status,last_error_code) VALUES($1,$2,'failed','worker_lease_expired') RETURNING id`, job.Kind, job.Payload).Scan(&duplicate); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.ReconcileExecutions(ctx, 100); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET payload=jsonb_build_object('generationId',$2::text) WHERE id=$1`, job.ID, uuid.New()); err == nil {
		t.Fatal("binding changed")
	}
	if _, err := pool.Exec(ctx, `UPDATE generation_executions SET job_id=$2 WHERE generation_id=$1`, g.ID, duplicate); err == nil {
		t.Fatal("execution reassigned")
	}
	// Caller counters cannot turn a retryable Provider failure into terminal failure.
	job.Attempts = 100
	job.MaxAttempts = 1
	runtime.failure = creation.NewProviderFailure("provider_rate_limited", 0)
	if err := svc.HandleJob(ctx, job); err == nil || !jobs.ShouldRetry(err) {
		t.Fatal(err)
	}
	assertExecutionOutcome(t, pool, g.ID, "queued", "held", 0, 0, 0)
	if runtime.calls.Load() != 1 {
		t.Fatal(runtime.calls.Load())
	}
}

func TestGenerationExecutionRecoveryRollsBackInconsistentReservationsAndBacksOff(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	owner := lifecycleOwner(t, pool)
	runtime := newLifecycleRuntime(t)
	svc := creation.NewServiceWithMedia(pool, media.NewCatalog(media.NewLocalStore(t.TempDir())), creation.NewRuntimeCatalog(runtime))
	g, err := svc.SubmitCommand(ctx, owner, creation.SubmitInput{Mode: "image", Prompt: "Rollback"}, "execution-rollback", "test")
	if err != nil {
		t.Fatal(err)
	}
	job := testutil.FinalGenerationAttempt(t, pool, lifecycleJob(t, pool, g.ID))
	expireGenerationJob(t, pool, job)
	if _, err = pool.Exec(ctx, `UPDATE point_accounts SET reserved_points=0 WHERE user_id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.ReconcileExecutions(ctx, 100); err == nil || n != 0 {
		t.Fatal(n, err)
	}
	assertExecutionOutcome(t, pool, g.ID, "queued", "held", 0, 0, 0)
	m, err := creation.ExecutionMetrics(ctx, pool)
	if err != nil || m.Failed != 1 || m.Due != 0 {
		t.Fatal(m, err)
	}
	if n, err := svc.ReconcileExecutions(ctx, 100); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE point_accounts SET reserved_points=(SELECT held_points FROM point_reservations WHERE generation_id=$2) WHERE user_id=$1`, owner, g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE generation_executions SET recovery_after=now()-interval '1 second' WHERE generation_id=$1`, g.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.ReconcileExecutions(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	assertExecutionOutcome(t, pool, g.ID, "failed", "released", 0, 0, 1)
	m, err = creation.ExecutionMetrics(ctx, pool)
	if err != nil || m.Failed != 0 || m.Unresolved != 0 {
		t.Fatal(m, err)
	}
}

func TestGenerationExecutionFailedWriteRecoveryAndInactiveEvidence(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	owner := lifecycleOwner(t, pool)
	runtime := newLifecycleRuntime(t)
	svc := creation.NewServiceWithMedia(pool, media.NewCatalog(media.NewLocalStore(t.TempDir())), creation.NewRuntimeCatalog(runtime))
	g, err := svc.SubmitCommand(ctx, owner, creation.SubmitInput{Mode: "chat", Prompt: "Crash before finalization"}, "execution-failed-write", "test")
	if err != nil {
		t.Fatal(err)
	}
	job := testutil.FinalGenerationAttempt(t, pool, lifecycleJob(t, pool, g.ID))
	if err = jobs.NewRepository(pool).Fail(ctx, job, "generation-test", errors.New("failed to persist business result")); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.ReconcileExecutions(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err = svc.HandleJob(ctx, jobs.Job{Kind: creation.FailureEvidenceJobKind, Payload: job.Payload}); err != nil {
		t.Fatal(err)
	}
	assertExecutionOutcome(t, pool, g.ID, "failed", "released", 0, 0, 1)
	var notices int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE resource_id=$1`, g.ID).Scan(&notices); err != nil || notices != 0 {
		t.Fatal(notices, err)
	}
}

func TestGenerationExecutionUnboundHistoricalEvidenceIsNotInvented(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	owner := lifecycleOwner(t, pool)
	id := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO generations(id,owner_id,mode,provider,model_name,prompt,status) VALUES($1,$2,'chat','local_test','local-chat','Historical','running')`, id, owner); err != nil {
		t.Fatal(err)
	}
	svc := creation.NewService(pool, t.TempDir(), "", true)
	if n, err := svc.ReconcileExecutions(ctx, 100); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	m, err := creation.ExecutionMetrics(ctx, pool)
	if err != nil || m.Unresolved != 1 {
		t.Fatal(m, err)
	}
	job := jobs.Job{ID: uuid.New(), Kind: creation.JobKind, LeaseToken: uuid.New(), Payload: []byte(fmt.Sprintf(`{"generationId":%q}`, id))}
	if err = svc.HandleJob(ctx, job); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatal(err)
	}
}

func TestGenerationExecutionLeaseIsRecheckedAfterStorage(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	owner := lifecycleOwner(t, pool)
	runtime := newLifecycleRuntime(t)
	store := &outputFaultStore{Store: media.NewLocalStore(t.TempDir())}
	svc := creation.NewServiceWithMedia(pool, media.NewCatalog(store), creation.NewRuntimeCatalog(runtime))
	g, err := svc.SubmitCommand(ctx, owner, creation.SubmitInput{Mode: "image", Prompt: "Expired without reaper"}, "expired-no-reaper", "test")
	if err != nil {
		t.Fatal(err)
	}
	job := testutil.FinalGenerationAttempt(t, pool, lifecycleJob(t, pool, g.ID))
	store.afterPut = func() error {
		_, err := pool.Exec(ctx, `UPDATE jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID)
		return err
	}
	if err = svc.HandleJob(ctx, job); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatal(err)
	}
	assertExecutionOutcome(t, pool, g.ID, "running", "held", 0, 0, 0)
	if n, err := svc.ReconcileExecutions(ctx, 100); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err = jobs.NewRepository(pool).RecoverExpired(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.ReconcileExecutions(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

func TestGenerationExecutionProtocolRejectsOldWriters(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	owner := lifecycleOwner(t, pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL app.generation_execution_protocol=''`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO generations(owner_id,mode,provider,model_name,prompt) VALUES($1,'chat','local_test','local-chat','Old writer')`, owner)
	if err == nil || !strings.Contains(err.Error(), "generation-execution-aware") {
		t.Fatal(err)
	}
}

func TestGenerationExecutionFinalProcessCrash(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	owner := lifecycleOwner(t, pool)
	root := t.TempDir()
	source := root + "/provider.jpg"
	if err := os.WriteFile(source, []byte("final interrupted generation"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := creation.NewService(pool, root, source, true)
	g, err := svc.SubmitCommand(ctx, owner, creation.SubmitInput{Mode: "image", Prompt: "Last worker crash"}, "final-process-crash", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE jobs SET max_attempts=1 WHERE id=(SELECT job_id FROM generation_executions WHERE generation_id=$1)`, g.ID); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	childCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	child := exec.CommandContext(childCtx, executable, "-test.run=^TestGenerationOutputCrashChild$")
	child.Env = append(os.Environ(), "HCAI_OUTPUT_CRASH_CHILD=1", "HCAI_OUTPUT_CRASH_DSN="+pool.Config().ConnString(), "HCAI_OUTPUT_CRASH_ROOT="+root, "HCAI_OUTPUT_CRASH_IMAGE="+source, "HCAI_OUTPUT_CRASH_GENERATION="+g.ID.String())
	output, err := child.CombinedOutput()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 88 {
		t.Fatalf("child %v: %s", err, output)
	}
	assertExecutionOutcome(t, pool, g.ID, "running", "held", 0, 0, 0)
	job := lifecycleJob(t, pool, g.ID)
	expireGenerationJob(t, pool, job)
	if n, err := svc.ReconcileExecutions(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	assertExecutionOutcome(t, pool, g.ID, "failed", "released", 0, 0, 1)
	_, key, state := outputIntentFor(t, pool, g.ID)
	if state != "pending" {
		t.Fatal(state)
	}
	if _, err = media.NewLocalStore(root).Stat(ctx, key); err != nil {
		t.Fatal("recovery lost file evidence", err)
	}
	if err = svc.HandleJob(ctx, job); err != nil {
		t.Fatal("terminal replay", err)
	}
	assertExecutionOutcome(t, pool, g.ID, "failed", "released", 0, 0, 1)
}

func TestGenerationExecutionLegacyAndPointsReleaseIsAtomic(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	owner := lifecycleOwner(t, pool)
	svc := creation.NewServiceWithRuntimes(pool, t.TempDir(), creation.NewRuntimeCatalog(newLifecycleRuntime(t)))
	g, err := svc.SubmitCommand(ctx, owner, creation.SubmitInput{Mode: "chat", Prompt: "Both reservations"}, "both-reservations", "test")
	if err != nil {
		t.Fatal(err)
	}
	job := testutil.FinalGenerationAttempt(t, pool, lifecycleJob(t, pool, g.ID))
	expireGenerationJob(t, pool, job)
	if _, err = pool.Exec(ctx, `INSERT INTO billing_reservations(user_id,operation_type,operation_id,amount_cents,currency) VALUES($1,'generation',$2,25,'USD')`, owner, g.ID); err != nil {
		t.Fatal(err)
	}
	// Deliberately inconsistent currency account: point release must roll back too.
	if n, err := svc.ReconcileExecutions(ctx, 100); err == nil || n != 0 {
		t.Fatal(n, err)
	}
	assertExecutionOutcome(t, pool, g.ID, "queued", "held", 0, 0, 0)
	if _, err = pool.Exec(ctx, `UPDATE billing_accounts SET reserved_cents=reserved_cents+25 WHERE user_id=$1 AND currency='USD'`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE generation_executions SET recovery_after=now()-interval '1 second' WHERE generation_id=$1`, g.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.ReconcileExecutions(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	assertExecutionOutcome(t, pool, g.ID, "failed", "released", 0, 0, 1)
	var state string
	var reserved int
	if err = pool.QueryRow(ctx, `SELECT r.status,a.reserved_cents FROM billing_reservations r JOIN billing_accounts a ON a.user_id=r.user_id AND a.currency=r.currency WHERE r.operation_id=$1`, g.ID).Scan(&state, &reserved); err != nil || state != "released" || reserved != 0 {
		t.Fatal(state, reserved, err)
	}
}

func TestGenerationExecutionMigrationBackfillsOnlyUnambiguousJobs(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	down, err := os.ReadFile("../platform/database/migrations/0117_generation_execution_recovery.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../platform/database/migrations/0117_generation_execution_recovery.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	run := func(sql string) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, sql); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err = run(string(down)); err != nil {
		t.Fatal(err)
	}
	repo := jobs.NewRepository(pool)
	if _, err = repo.Enqueue(ctx, "test.execution-drain", map[string]string{"scope": "isolated"}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.Claim(ctx, "migration-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = run(string(up)); err == nil || !strings.Contains(err.Error(), "drain running jobs") {
		t.Fatal("accepted active worker", err)
	}
	if err = repo.Complete(ctx, claimed, "migration-test"); err != nil {
		t.Fatal(err)
	}
	owner := lifecycleOwner(t, pool)
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for index, id := range ids {
		if _, err = pool.Exec(ctx, `INSERT INTO generations(id,owner_id,mode,provider,model_name,prompt) VALUES($1,$2,'chat','local_test','local-chat','Historical migration')`, id, owner); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < index; j++ {
			if _, err = repo.Enqueue(ctx, creation.JobKind, map[string]string{"generationId": id.String()}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = repo.Enqueue(ctx, creation.JobKind, map[string]string{"generationId": "not-a-uuid"}); err != nil {
		t.Fatal(err)
	}
	if err = run(string(up)); err != nil {
		t.Fatal(err)
	}
	var count int
	var bound uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT count(*),(array_agg(generation_id))[1] FROM generation_executions`).Scan(&count, &bound); err != nil || count != 1 || bound != ids[1] {
		t.Fatal(count, bound, err)
	}
	if err = run(string(down)); err == nil || !strings.Contains(err.Error(), "rollback refused") {
		t.Fatal("erased execution evidence", err)
	}
	// Old worker connections cannot claim jobs even when no generation row is written.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL app.generation_execution_protocol=''`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `UPDATE jobs SET status='running' WHERE id=(SELECT job_id FROM generation_executions LIMIT 1)`)
	if err == nil || !strings.Contains(err.Error(), "generation-execution-aware") {
		t.Fatal("old worker allowed", err)
	}
}

func TestGenerationExecutionStorageAllowsWorkerHeartbeat(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner := lifecycleOwner(t, pool)
	store := &lifecycleStore{Store: media.NewLocalStore(t.TempDir())}
	svc := creation.NewServiceWithMedia(pool, media.NewCatalog(store), creation.NewRuntimeCatalog(newLifecycleRuntime(t)))
	g, err := svc.SubmitCommand(ctx, owner, creation.SubmitInput{Mode: "image", Prompt: "Heartbeat during Put"}, "put-heartbeat", "test")
	if err != nil {
		t.Fatal(err)
	}
	store.beforePut = func(ctx context.Context) error {
		var before int64
		query := `SELECT a.lease_renewals FROM generation_executions e JOIN jobs j ON j.id=e.job_id JOIN job_attempts a ON a.lease_token=j.lease_token WHERE e.generation_id=$1`
		if err := pool.QueryRow(ctx, query, g.ID).Scan(&before); err != nil {
			return err
		}
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				var renewals int64
				if err := pool.QueryRow(ctx, query, g.ID).Scan(&renewals); err != nil {
					return err
				}
				if renewals >= before+2 {
					return nil
				}
			}
		}
	}
	repo := jobs.NewRepository(pool)
	worker := jobs.NewWorkerWithLease(repo, "generation-heartbeat", slog.New(slog.NewTextHandler(io.Discard, nil)), 900*time.Millisecond)
	worker.Handle(notifications.JobKind, notifications.NewRepository(pool).HandleDeliveryJob)
	handled := make(chan error, 1)
	worker.Handle(creation.JobKind, func(ctx context.Context, job jobs.Job) error {
		err := svc.HandleJob(ctx, job)
		handled <- err
		return err
	})
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case err := <-handled:
		if err != nil {
			t.Fatal("heartbeat blocked by result persistence", err)
		}
	case <-ctx.Done():
		t.Fatal("worker did not finish", ctx.Err())
	}
	assertExecutionOutcome(t, pool, g.ID, "succeeded", "captured", 1, 1, 0)
}
