package payments

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func applyPaymentExecutionLockMigration(t *testing.T, pool *pgxpool.Pool, direction string) {
	t.Helper()
	if direction == "down" {
		applySourceReversalCommandMigration(t, pool, direction)
	}
	body, err := os.ReadFile("../platform/database/migrations/0169_payment_execution_lease_locks." + direction + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), string(body)); err != nil {
		t.Fatal(err)
	}
	if direction == "up" {
		applySourceReversalCommandMigration(t, pool, direction)
	}
}

type executionLeaseFixture struct {
	pool    *pgxpool.Pool
	job     jobs.Job
	handle  func(context.Context, jobs.Job) error
	install func(func(context.Context))
}

func newExecutionLeaseFixture(t *testing.T, kind string) executionLeaseFixture {
	t.Helper()
	var pool *pgxpool.Pool
	var job jobs.Job
	var handle func(context.Context, jobs.Job) error
	var install func(func(context.Context))
	if kind == "reversal" {
		p, service, runtime, _, _, j := sourceReversalExecutionFixture(t)
		pool, job, handle = p, j, service.HandleSellerSourceReversalJob
		install = func(renew func(context.Context)) {
			runtime.createReversal = func(ctx context.Context, input TransferReversalRequest) (TransferReversalResult, error) {
				renew(ctx)
				return reversalObservation(input), nil
			}
		}
	} else if kind == "bank" {
		p, service, runtime, _, _, _, j := bankExecutionFixture(t)
		pool, job = p, j
		handle = service.HandleSellerBankPayoutJob
		install = func(renew func(context.Context)) {
			runtime.create = func(ctx context.Context, input PayoutRequest) (Payout, error) {
				renew(ctx)
				return bankObservation(input, "paid"), nil
			}
		}
	} else {
		f := newTransferExecutionFixture(t, kind == "funding")
		pool, job = f.pool, f.job
		handle = f.service.HandleProductSettlementJob
		if f.source {
			handle = f.service.HandleSellerPayoutFundingJob
		}
		install = func(renew func(context.Context)) {
			f.runtime.onTransfer = func(ctx context.Context, _ TransferRequest) { renew(ctx) }
		}
	}
	return executionLeaseFixture{pool, job, handle, install}
}

func TestPaymentExecutionAllowsLeaseRenewal(t *testing.T) {
	for _, kind := range []string{"settlement", "funding", "bank", "reversal"} {
		t.Run(kind, func(t *testing.T) {
			f := newExecutionLeaseFixture(t, kind)
			pool, job, handle, install := f.pool, f.job, f.handle, f.install
			if _, err := pool.Exec(t.Context(), `UPDATE jobs SET available_at=CASE WHEN id=$1 THEN now() ELSE now()+interval '1 day' END`, job.ID); err != nil {
				t.Fatal(err)
			}
			repository := jobs.NewRepository(pool)
			claimed, err := repository.Claim(t.Context(), "payment-execution-lease", 30*time.Second)
			if err != nil || claimed.ID != job.ID {
				t.Fatal("claim original execution", claimed.ID, err)
			}
			var renewalErr error
			var attempted bool
			install(func(ctx context.Context) {
				attempted = true
				renewCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
				defer cancel()
				renewalErr = repository.Renew(renewCtx, claimed, "payment-execution-lease", 30*time.Second)
			})
			if err := handle(t.Context(), claimed); err != nil {
				t.Fatal(err)
			}
			if !attempted || renewalErr != nil {
				t.Fatal("active money movement blocked its own heartbeat", attempted, renewalErr)
			}
			if err := repository.Complete(t.Context(), claimed, "payment-execution-lease"); err != nil {
				t.Fatal("complete original lease", err)
			}
		})
	}
}

func TestPaymentExecutionControlChangesSerialize(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, kind := range []string{ProductSettlementJobKind, SellerPayoutFundingJobKind, SellerBankPayoutJobKind, SellerSourceReversalJobKind} {
		for _, change := range []string{"status", "kind", "payload", "delete"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				id, err := jobs.NewRepository(pool).Enqueue(t.Context(), kind, map[string]string{"id": "guard-test"})
				if err != nil {
					t.Fatal(err)
				}
				send, err := pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer send.Rollback(t.Context())
				var valid bool
				if err := send.QueryRow(t.Context(), `SELECT payment_execution_job_matches($1,$2,'{"id":"guard-test"}')`, id, kind).Scan(&valid); err != nil || !valid {
					t.Fatal("lock execution", valid, err)
				}
				query := map[string]string{
					"status":  `UPDATE jobs SET status='cancelled' WHERE id=$1`,
					"kind":    `UPDATE jobs SET kind='not-a-payment' WHERE id=$1`,
					"payload": `UPDATE jobs SET payload='{}' WHERE id=$1`,
					"delete":  `DELETE FROM jobs WHERE id=$1`,
				}[change]
				stop, err := pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if _, err := stop.Exec(t.Context(), `SET LOCAL lock_timeout='100ms'`); err != nil {
					_ = stop.Rollback(t.Context())
					t.Fatal(err)
				}
				_, changeErr := stop.Exec(t.Context(), query, id)
				_ = stop.Rollback(t.Context())
				var pgerr *pgconn.PgError
				if !errors.As(changeErr, &pgerr) || pgerr.Code != "55P03" {
					t.Fatal("execution control mutation bypassed active send", changeErr)
				}
				if err := send.Commit(t.Context()); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(t.Context(), query, id); err != nil {
					t.Fatal("control change after send", err)
				}
				if err := pool.QueryRow(t.Context(), `SELECT payment_execution_job_matches($1,$2,'{"id":"guard-test"}')`, id, kind).Scan(&valid); err != nil || valid {
					t.Fatal("changed job remained executable", valid, err)
				}
			})
		}
	}
}

func TestPaymentExecutionLockProtocolAndMigration(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	repository := jobs.NewRepository(pool)
	id, err := repository.Enqueue(t.Context(), ProductSettlementJobKind, map[string]string{"id": "existing"})
	if err != nil {
		t.Fatal(err)
	}
	applyPaymentExecutionLockMigration(t, pool, "down")
	second, err := repository.Enqueue(t.Context(), SellerPayoutFundingJobKind, map[string]string{"id": "created-before-upgrade"})
	if err != nil {
		t.Fatal(err)
	}
	applyPaymentExecutionLockMigration(t, pool, "up")
	for _, jobID := range []uuid.UUID{id, second} {
		var ok bool
		if err := pool.QueryRow(t.Context(), `SELECT payment_transfer_job_executable(id,kind,payload) FROM jobs WHERE id=$1`, jobID).Scan(&ok); err != nil || !ok {
			t.Fatal("backfilled job is not executable", ok, err)
		}
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := tx.Exec(t.Context(), `SELECT set_config('app.payment_execution_lock_protocol','',true)`); err != nil {
		t.Fatal(err)
	}
	var ok bool
	if err := tx.QueryRow(t.Context(), `SELECT payment_transfer_job_executable(id,kind,payload) FROM jobs WHERE id=$1`, id).Scan(&ok); err != nil || ok {
		t.Fatal("old protocol accepted", ok, err)
	}
}

func TestPaymentExecutionRejectsStaleTransactionSnapshot(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, isolation := range []pgx.TxIsoLevel{pgx.ReadCommitted, pgx.RepeatableRead, pgx.Serializable} {
		t.Run(string(isolation), func(t *testing.T) {
			id, err := jobs.NewRepository(pool).Enqueue(t.Context(), ProductSettlementJobKind, map[string]string{"id": "snapshot"})
			if err != nil {
				t.Fatal(err)
			}
			tx, err := pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: isolation})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			var status string
			if err := tx.QueryRow(t.Context(), `SELECT status FROM jobs WHERE id=$1`, id).Scan(&status); err != nil || status != "queued" {
				t.Fatal("establish transaction snapshot", status, err)
			}
			if _, err := pool.Exec(t.Context(), `UPDATE jobs SET status='cancelled' WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
			var valid bool
			err = tx.QueryRow(t.Context(), `SELECT payment_execution_job_matches($1,$2,'{"id":"snapshot"}')`, id, ProductSettlementJobKind).Scan(&valid)
			if err == nil && valid {
				t.Fatal("stale transaction accepted cancelled execution")
			}
			if err != nil {
				var pgerr *pgconn.PgError
				if !errors.As(err, &pgerr) || pgerr.Code != "40001" {
					t.Fatal("unexpected snapshot failure", err)
				}
			}
		})
	}
}

func TestPaymentExecutionWorkerRenewsThroughProviderCall(t *testing.T) {
	for _, kind := range []string{"settlement", "funding", "bank", "reversal"} {
		t.Run(kind, func(t *testing.T) {
			f := newExecutionLeaseFixture(t, kind)
			if _, err := f.pool.Exec(t.Context(), `UPDATE jobs SET available_at=CASE WHEN id=$1 THEN now() ELSE now()+interval '1 day' END`, f.job.ID); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			var interrupted atomic.Bool
			f.install(func(ctx context.Context) {
				calls.Add(1)
				timer := time.NewTimer(2200 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-ctx.Done():
					interrupted.Store(true)
				}
			})
			repository := jobs.NewRepository(f.pool)
			worker := jobs.NewWorkerWithOptions(repository, "payment-long-call", slog.New(slog.NewJSONHandler(io.Discard, nil)), 900*time.Millisecond, 1)
			worker.Handle(f.job.Kind, f.handle)
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			done := make(chan error, 1)
			go func() { done <- worker.Run(ctx) }()
			defer func() {
				cancel()
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Error("worker did not stop")
				}
			}()
			var status string
			for ctx.Err() == nil {
				if err := f.pool.QueryRow(ctx, `SELECT status FROM jobs WHERE id=$1`, f.job.ID).Scan(&status); err != nil {
					t.Fatal(err)
				}
				if status == "succeeded" || status == "failed" {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			var attempts, renewals int
			if err := f.pool.QueryRow(t.Context(), `SELECT j.attempts,a.lease_renewals FROM jobs j JOIN job_attempts a ON a.lease_token=j.lease_token OR (a.job_id=j.id AND a.attempt_number=1) WHERE j.id=$1`, f.job.ID).Scan(&attempts, &renewals); err != nil {
				t.Fatal(err)
			}
			if status != "succeeded" || calls.Load() != 1 || interrupted.Load() || attempts != 1 || renewals < 2 {
				t.Fatalf("long payment call: status=%s sends=%d interrupted=%v attempts=%d renewals=%d", status, calls.Load(), interrupted.Load(), attempts, renewals)
			}
		})
	}
}
