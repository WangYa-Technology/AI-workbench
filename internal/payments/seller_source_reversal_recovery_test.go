package payments

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func TestSellerSourceReversalRecoveryOnlyQueriesStoppedAttempts(t *testing.T) {
	pool, service, runtime, request, command, original := sourceReversalExecutionFixture(t)
	ctx := t.Context()
	// A queued or stopped command with no first-send marker is not a read candidate.
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='cancelled' WHERE id=$1`, original.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := service.reconcileSellerSourceReversals(ctx, 10, time.Now().Add(time.Hour)); err != nil || n != 0 {
		t.Fatal("unstarted command recovered", n, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='queued' WHERE id=$1`, original.ID); err != nil {
		t.Fatal(err)
	}
	runtime.createReversal = func(context.Context, TransferReversalRequest) (TransferReversalResult, error) {
		return TransferReversalResult{}, context.DeadlineExceeded
	}
	if err := service.HandleSellerSourceReversalJob(ctx, original); err == nil {
		t.Fatal("lost response completed")
	}
	if n, err := service.reconcileSellerSourceReversals(ctx, 10, time.Now().Add(time.Hour)); err != nil || n != 0 {
		t.Fatal("active original job duplicated", n, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed',updated_at=clock_timestamp() WHERE id=$1`, original.ID); err != nil {
		t.Fatal(err)
	}
	var due time.Time
	if err := pool.QueryRow(ctx, `SELECT due_at FROM seller_source_reversal_check_candidates WHERE command_id=$1`, command.ID).Scan(&due); err != nil {
		t.Fatal(err)
	}
	if n, err := service.reconcileSellerSourceReversals(ctx, 10, due.Add(-time.Microsecond)); err != nil || n != 0 {
		t.Fatal("cooldown bypassed", n, err)
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err := blocker.Exec(ctx, `SELECT p.id FROM payment_intents p JOIN seller_source_reversal_commands c ON c.payment_id=p.id WHERE c.id=$1 FOR UPDATE OF p`, command.ID); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, time.Second)
	n, err := service.reconcileSellerSourceReversals(short, 10, due)
	cancel()
	if err != nil || n != 0 {
		t.Fatal("busy source blocked scan", n, err)
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.scheduleSellerSourceReversalCheck(ctx, command.ID, due)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM seller_source_reversal_checks WHERE command_id=$1`, command.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate recovery", count, err)
	}
	job := jobs.Job{Kind: SellerSourceReversalCheckJobKind}
	if err := pool.QueryRow(ctx, `SELECT j.id,j.payload FROM seller_source_reversal_checks c JOIN jobs j ON j.id=c.job_id WHERE c.command_id=$1`, command.ID).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	service.config.Enabled = false
	if _, err := pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=(SELECT actor_id FROM seller_source_reversal_commands WHERE id=$1)`, command.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleSellerSourceReversalCheckJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	assertReversalFundsRetained(t, pool, service, request, 1)
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='succeeded' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := service.reconcileSellerSourceReversals(ctx, 10, due.Add(time.Hour)); err != nil || n != 0 {
		t.Fatal("accepted reversal remained candidate", n, err)
	}
	if runtime.creates.Load() != 1 || runtime.queries.Load() != 1 {
		t.Fatal("recovery resent money")
	}
	for _, sql := range []string{`DELETE FROM seller_source_reversal_checks`, `UPDATE seller_source_reversal_checks SET created_at=clock_timestamp()`} {
		_, err := pool.Exec(ctx, sql)
		requirePayoutConstraint(t, err)
	}
	job.ID = uuid.New()
	if err := service.HandleSellerSourceReversalCheckJob(ctx, job); err == nil {
		t.Fatal("unregistered read job accepted")
	}
}

func TestSellerSourceReversalExecutionProtocol(t *testing.T) {
	pool, _, _, _, command, job := sourceReversalExecutionFixture(t)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := tx.Exec(t.Context(), `SELECT set_config('app.seller_reversal_execution_protocol','old',true)`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), `INSERT INTO seller_source_reversal_dispatches(command_id,job_id,started_at,deadline_at)
 SELECT $1,$2,t,t+interval '20 seconds' FROM clock_timestamp() t`, command.ID, job.ID)
	requirePayoutConstraint(t, err)
}
