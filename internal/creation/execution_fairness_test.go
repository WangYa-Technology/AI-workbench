package creation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/accountlifecycle"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
)

func TestGenerationRecoveryLockedHeadDoesNotStarveLaterExecutions(t *testing.T) {
	for _, lockKind := range []string{"account", "account_row", "generation", "job", "binding"} {
		t.Run(lockKind, func(t *testing.T) {
			pool, cleanup := testPool(t)
			defer cleanup()
			ctx := t.Context()
			runtime := newLifecycleRuntime(t)
			service := creation.NewServiceWithMedia(pool, media.NewCatalog(media.NewLocalStore(t.TempDir())), creation.NewRuntimeCatalog(runtime))
			repo := jobs.NewRepository(pool)
			var owners, ids [2]uuid.UUID
			var claimed [2]jobs.Job
			for i := range ids {
				owners[i] = lifecycleOwner(t, pool)
				g, err := service.SubmitCommand(ctx, owners[i], creation.SubmitInput{Mode: "image", Prompt: "Execution recovery fairness"}, "fairness-submit", "test")
				if err != nil {
					t.Fatal(err)
				}
				ids[i] = g.ID
				claimed[i] = testutil.FinalGenerationAttempt(t, pool, lifecycleJob(t, pool, g.ID))
				if err = repo.Fail(ctx, claimed[i], "generation-test", errors.New("fixture crash")); err != nil {
					t.Fatal(err)
				}
				if _, err = pool.Exec(ctx, `UPDATE generation_executions SET recovery_after=now()-interval '10 minutes'+$2::integer*interval '1 minute' WHERE generation_id=$1`, g.ID, i); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := pool.Exec(ctx, `UPDATE generation_executions SET recovery_checks=7,last_error_code='generation_recovery_failed' WHERE generation_id=$1`, ids[0]); err != nil {
				t.Fatal(err)
			}
			locked, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer locked.Rollback(ctx)
			switch lockKind {
			case "account":
				err = accountlifecycle.Lock(ctx, locked, owners[0])
			case "account_row":
				_, err = locked.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, owners[0])
			case "generation":
				_, err = locked.Exec(ctx, `SELECT id FROM generations WHERE id=$1 FOR UPDATE`, ids[0])
			case "job":
				_, err = locked.Exec(ctx, `SELECT id FROM jobs WHERE id=$1 FOR UPDATE`, claimed[0].ID)
			case "binding":
				_, err = locked.Exec(ctx, `SELECT generation_id FROM generation_executions WHERE generation_id=$1 FOR UPDATE`, ids[0])
			}
			if err != nil {
				t.Fatal(err)
			}
			total := 0
			for range 2 {
				pass, cancel := context.WithTimeout(ctx, time.Second)
				n, recoverErr := service.ReconcileExecutions(pass, 1)
				cancel()
				if recoverErr != nil {
					t.Fatalf("busy %s consumed pass: %v", lockKind, recoverErr)
				}
				total += n
			}
			if total != 1 {
				t.Errorf("locked head starved later generation: %d", total)
			}
			assertExecutionOutcome(t, pool, ids[0], "queued", "held", 0, 0, 0)
			assertExecutionOutcome(t, pool, ids[1], "failed", "released", 0, 0, 1)
			if err = locked.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			var checks int
			var code string
			var deferred bool
			if err = pool.QueryRow(ctx, `SELECT recovery_checks,last_error_code,recovery_after>now() FROM generation_executions WHERE generation_id=$1`, ids[0]).Scan(&checks, &code, &deferred); err != nil {
				t.Fatal(err)
			}
			if checks != 7 || code != "generation_recovery_failed" {
				t.Fatalf("busy skip rewrote evidence: %d %s", checks, code)
			}
			if lockKind != "binding" && !deferred {
				t.Error("busy head was not deferred")
			}
			if _, err = pool.Exec(ctx, `UPDATE generation_executions SET recovery_after=now()-interval '1 second' WHERE generation_id=$1`, ids[0]); err != nil {
				t.Fatal(err)
			}
			if n, err := service.ReconcileExecutions(ctx, 1); err != nil || n != 1 {
				t.Fatalf("unlocked recovery: %d %v", n, err)
			}
			assertExecutionOutcome(t, pool, ids[0], "failed", "released", 0, 0, 1)
			if runtime.calls.Load() != 0 {
				t.Fatal("recovery dispatched model")
			}
		})
	}
}
