package payments

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func applyTransferExecutionMigration(t *testing.T, pool *pgxpool.Pool, direction string) {
	t.Helper()
	if direction == "down" {
		applyPaymentExecutionLockMigration(t, pool, direction)
	}
	body, err := os.ReadFile("../platform/database/migrations/0168_transfer_execution_jobs." + direction + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), string(body)); err != nil {
		t.Fatal(err)
	}
	if direction == "up" {
		applyPaymentExecutionLockMigration(t, pool, direction)
	}
}

type transferExecutionFixture struct {
	pool       *pgxpool.Pool
	service    *Service
	runtime    *settlementReadRuntime
	job        jobs.Job
	settlement uuid.UUID
	request    SellerPayoutRequest
	source     bool
}

func newTransferExecutionFixture(t *testing.T, source bool) transferExecutionFixture {
	t.Helper()
	f := transferExecutionFixture{source: source}
	if source {
		f.pool, f.service, f.runtime, _, f.request, f.job = sellerFundingFixture(t)
	} else {
		pool, cleanup := paymentTestPool(t)
		t.Cleanup(cleanup)
		f.pool = pool
		f.runtime = &settlementReadRuntime{productSettlementRuntime: &productSettlementRuntime{}}
		f.service, _, f.job, f.settlement = productSettlementFixture(t, pool, f.runtime.productSettlementRuntime)
		f.service.runtimes = NewRuntimeCatalog(f.runtime)
	}
	return f
}

func (f transferExecutionFixture) handle(ctx context.Context) error {
	if f.source {
		return f.service.HandleSellerPayoutFundingJob(ctx, f.job)
	}
	return f.service.HandleProductSettlementJob(ctx, f.job)
}

func (f transferExecutionFixture) assertStarted(t *testing.T, want bool) {
	t.Helper()
	query := `SELECT reserved_at IS NOT NULL FROM product_settlement_dispatches WHERE job_id=$1`
	if f.source {
		query = `SELECT started_at IS NOT NULL FROM seller_payout_funding_dispatches WHERE job_id=$1`
	}
	var started bool
	if err := f.pool.QueryRow(t.Context(), query, f.job.ID).Scan(&started); err != nil || started != want {
		t.Fatal("unexpected dispatch evidence", started, want, err)
	}
}

func TestTransferExecutionStoppedAndMismatchedJobs(t *testing.T) {
	for _, source := range []bool{false, true} {
		for _, state := range []string{"failed", "cancelled", "succeeded", "wrong_kind", "wrong_payload"} {
			t.Run(map[bool]string{false: "settlement/", true: "funding/"}[source]+state, func(t *testing.T) {
				f := newTransferExecutionFixture(t, source)
				query := `UPDATE jobs SET status=$2 WHERE id=$1`
				if state == "wrong_kind" {
					query = `UPDATE jobs SET kind=$2 WHERE id=$1`
				}
				if state == "wrong_payload" {
					query = `UPDATE jobs SET payload=jsonb_build_object('wrong',$2::text) WHERE id=$1`
				}
				if _, err := f.pool.Exec(t.Context(), query, f.job.ID, state); err != nil {
					t.Fatal(err)
				}
				if err := f.handle(t.Context()); err == nil {
					t.Fatal("invalid execution accepted")
				}
				if f.runtime.transfers.Load() != 0 {
					t.Fatal("invalid execution moved money")
				}
				f.assertStarted(t, false)
			})
		}
	}
}

type cancelTransferRuntime struct {
	*settlementReadRuntime
	calls          atomic.Int32
	beforeIdentity func(context.Context, int32) error
}

func (r *cancelTransferRuntime) ProductCheckoutIdentity(ctx context.Context) (ProductCheckoutIdentity, error) {
	if err := r.beforeIdentity(ctx, r.calls.Add(1)); err != nil {
		return ProductCheckoutIdentity{}, err
	}
	return r.settlementReadRuntime.ProductCheckoutIdentity(ctx)
}

func TestTransferExecutionLateCancellationOnlyReconciles(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(map[bool]string{false: "settlement", true: "funding"}[source], func(t *testing.T) {
			f := newTransferExecutionFixture(t, source)
			runtime := &cancelTransferRuntime{settlementReadRuntime: f.runtime, beforeIdentity: func(ctx context.Context, call int32) error {
				// Settlement authenticates in both transactions; source funding only
				// authenticates after its durable first-send marker has committed.
				if source || call == 2 {
					_, err := f.pool.Exec(ctx, `UPDATE jobs SET status='cancelled' WHERE id=$1`, f.job.ID)
					return err
				}
				return nil
			}}
			f.service.runtimes = NewRuntimeCatalog(runtime)
			if err := f.handle(t.Context()); err == nil {
				t.Fatal("late cancellation was ignored")
			}
			if f.runtime.transfers.Load() != 0 {
				t.Fatal("late cancellation still sent funds")
			}
			f.assertStarted(t, true)
			if source {
				assertSellerFunding(t, f.pool, f.request, "reconciliation_required", "reconciliation_required")
			} else {
				var state string
				if err := f.pool.QueryRow(t.Context(), `SELECT status FROM product_settlements WHERE id=$1`, f.settlement).Scan(&state); err != nil || state != "recovery_required" {
					t.Fatal("late cancellation not retained", state, err)
				}
			}
			f.service.runtimes = NewRuntimeCatalog(f.runtime)
			f.runtime.lookup = func(context.Context, TransferLookupRequest) (TransferLookupResult, error) {
				return TransferLookupResult{Outcome: "not_found", Pages: 1, Observations: []TransferObservation{}}, nil
			}
			f.service.config.Enabled = false
			if source {
				check := scheduleFundingRecovery(t, f.service)
				_ = f.service.HandleSellerPayoutFundingCheckJob(t.Context(), check)
			} else {
				check := scheduleSettlementCheck(t, f.service, f.settlement)
				_ = f.service.HandleProductSettlementCheckJob(t.Context(), check)
			}
			if f.runtime.transfers.Load() != 0 || f.runtime.reads.Load() != 1 {
				t.Fatal("recovery resent transfer or failed to query")
			}
			f.assertStarted(t, true)
		})
	}
}

func TestTransferExecutionCancellationWaitsForActiveSend(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(map[bool]string{false: "settlement", true: "funding"}[source], func(t *testing.T) {
			f := newTransferExecutionFixture(t, source)
			entered, release := make(chan struct{}), make(chan struct{})
			f.runtime.onTransfer = func(ctx context.Context, _ TransferRequest) {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			done := make(chan error, 1)
			go func() { done <- f.handle(t.Context()) }()
			select {
			case <-entered:
			case err := <-done:
				t.Fatal("execution stopped before reaching the provider", err)
			}
			tx, err := f.pool.Begin(t.Context())
			if err != nil {
				close(release)
				t.Fatal(err)
			}
			_, lockErr := tx.Exec(t.Context(), `SELECT job_id FROM payment_job_execution_locks WHERE job_id=$1 FOR UPDATE NOWAIT`, f.job.ID)
			_ = tx.Rollback(t.Context())
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			var pgerr *pgconn.PgError
			if !errors.As(lockErr, &pgerr) || pgerr.Code != "55P03" {
				t.Fatal("first send did not retain its execution control lock", lockErr)
			}
			stopBankJob(t, f.pool, f.job.ID, "cancelled")
			if err := f.handle(t.Context()); err != nil {
				t.Fatal(err)
			}
			if f.runtime.transfers.Load() != 1 {
				t.Fatal("late cancellation resent completed transfer")
			}
		})
	}
}

func TestTransferExecutionProtocolAndRoundTrip(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(map[bool]string{false: "settlement", true: "funding"}[source], func(t *testing.T) {
			f := newTransferExecutionFixture(t, source)
			for _, status := range []string{"old_protocol", "cancelled"} {
				tx, err := f.pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if status == "old_protocol" {
					_, err = tx.Exec(t.Context(), `SELECT set_config('app.payment_transfer_execution_protocol','',true)`)
				} else {
					_, err = tx.Exec(t.Context(), `UPDATE jobs SET status='cancelled' WHERE id=$1`, f.job.ID)
				}
				if err != nil {
					_ = tx.Rollback(t.Context())
					t.Fatal(err)
				}
				query := `UPDATE product_settlement_dispatches SET reserved_at=clock_timestamp() WHERE job_id=$1`
				if source {
					query = `UPDATE seller_payout_funding_dispatches SET started_at=clock_timestamp() WHERE job_id=$1`
				}
				_, err = tx.Exec(t.Context(), query, f.job.ID)
				_ = tx.Rollback(t.Context())
				requirePayoutConstraint(t, err)
			}
			f.assertStarted(t, false)
			applyTransferExecutionMigration(t, f.pool, "down")
			applyTransferExecutionMigration(t, f.pool, "up")
			if err := f.handle(t.Context()); err != nil {
				t.Fatal(err)
			}
			f.assertStarted(t, true)
			if f.runtime.transfers.Load() != 1 {
				t.Fatal("migration damaged legitimate execution")
			}
		})
	}
}
