package payments

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSellerSourceReversalClosureAuthorityAfterWait(t *testing.T) {
	pool, service, _, request, command, job := sourceReversalExecutionFixture(t)
	if err := service.HandleSellerSourceReversalJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	actor, input := sourceClosureInput(t, pool, command.ID)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var pid int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT lock_seller_payout_disposition($1)`, request.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := service.CloseSellerSourceReversal(ctx, actor, command.ID, input, "wait-revoke-source-close", "trace")
		done <- err
	}()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid)) AND query LIKE '%lock_seller_payout_disposition%')`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrFinanceForbidden) {
		t.Fatal("revoked while waiting", err)
	}
	assertSourceClosure(t, pool, request, 0)
	assertReversalFundsRetained(t, pool, service, request, 1)
}

func TestSellerSourceReversalClosureBankHistoryAndReplay(t *testing.T) {
	for _, scenario := range []string{"unstarted", "failed", "returned"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, bank, _, request, _, bankJob := bankExecutionFixture(t)
			ctx := t.Context()
			if scenario != "unstarted" {
				if scenario == "failed" {
					bank.status = "failed"
				}
				if err := service.HandleSellerBankPayoutJob(ctx, bankJob); err != nil {
					t.Fatal(err)
				}
				if scenario == "returned" {
					bank.status = "failed"
					if err := service.HandleSellerBankPayoutJob(ctx, bankJob); err != nil {
						t.Fatal(err)
					}
				}
			}
			actor, _ := bankCommandInput(t, pool, request)
			runtime := &sourceReversalExecutionRuntime{sourceReversalCommandRuntime: &sourceReversalCommandRuntime{sellerBankExecutionRuntime: bank}}
			service.runtimes = NewRuntimeCatalog(runtime)
			out, err := service.SubmitSellerSourceReversal(ctx, actor, request.ID, reversalCommandInput(t, pool, request), "return-source-with-bank", "trace")
			if err != nil {
				t.Fatal(err)
			}
			job := jobs.Job{ID: out.Command.JobID, Kind: SellerSourceReversalJobKind}
			if err := pool.QueryRow(ctx, `SELECT payload FROM jobs WHERE id=$1`, job.ID).Scan(&job.Payload); err != nil {
				t.Fatal(err)
			}
			if err := service.HandleSellerSourceReversalJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			_, input := sourceClosureInput(t, pool, out.Command.ID)
			if _, err := service.CloseSellerSourceReversal(ctx, actor, out.Command.ID, input, "close-source-with-bank", "trace"); err != nil {
				t.Fatal(err)
			}
			posts, reads := bank.payouts.Load(), bank.bankReads.Load()
			if err := service.HandleSellerBankPayoutJob(ctx, bankJob); err != nil {
				t.Fatal("old bank replay", err)
			}
			if bank.payouts.Load() != posts || bank.bankReads.Load() != reads {
				t.Fatal("closed request contacted bank")
			}
			var debits, returns int
			if err := pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE entry_type='payout_debit'),count(*) FILTER(WHERE entry_type='payout_return') FROM seller_ledger_entries WHERE payout_request_id=$1`, request.ID).Scan(&debits, &returns); err != nil {
				t.Fatal(err)
			}
			want := 0
			if scenario == "returned" {
				want = 1
			}
			if debits != want || returns != want {
				t.Fatal("bank ledger rewritten", debits, returns)
			}
			balance, err := singleSellerFunds(t, service, ctx, request.SellerID)
			if err != nil || balance.ReservedCents != 0 || balance.WithdrawableCents != request.AmountCents {
				t.Fatal("bank return closure balance", balance, err)
			}
			assertSourceClosure(t, pool, request, 1)
		})
	}
}

func TestSellerSourceReversalClosurePartialDebtAndProtocol(t *testing.T) {
	for _, scenario := range []string{"partial_debt", "old_protocol"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, _, request, command, job := sourceReversalExecutionFixture(t)
			ctx := t.Context()
			if err := service.HandleSellerSourceReversalJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if scenario == "partial_debt" {
				confirmSourceClosureRefund(t, pool, service, command.ID)
				settlement, _ := sourceClosureBinding(t, pool, command.ID)
				if _, err := pool.Exec(ctx, `UPDATE seller_recovery_obligations SET remaining_cents=remaining_cents-1 WHERE settlement_id=$1`, settlement); err != nil {
					t.Fatal(err)
				}
			} else {
				cfg := pool.Config()
				cfg.ConnConfig.RuntimeParams["app.seller_reversal_closure_protocol"] = "old"
				old, err := pgxpool.NewWithConfig(ctx, cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				service = newPaymentTestService(t, old, service.config, service.runtimes)
			}
			actor, input := sourceClosureInput(t, pool, command.ID)
			if _, err := service.CloseSellerSourceReversal(ctx, actor, command.ID, input, "reject-partial-or-old", "trace"); !errors.Is(err, ErrSellerSourceClosureConflict) {
				t.Fatal("unsafe closure", err)
			}
			assertSourceClosure(t, pool, request, 0)
		})
	}
}

func TestSellerSourceReversalClosureRefundRace(t *testing.T) {
	pool, service, _, request, command, job := sourceReversalExecutionFixture(t)
	ctx := t.Context()
	if err := service.HandleSellerSourceReversalJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	refundJob := prepareSourceClosureRefund(t, pool, service, command.ID)
	actor, input := sourceClosureInput(t, pool, command.ID)
	closed, refunded := make(chan error, 1), make(chan error, 1)
	start := make(chan struct{})
	go func() {
		<-start
		_, err := service.CloseSellerSourceReversal(ctx, actor, command.ID, input, "refund-race-source-close", "trace")
		closed <- err
	}()
	go func() { <-start; refunded <- service.HandlePaymentEventJob(ctx, refundJob) }()
	close(start)
	closeErr, refundErr := <-closed, <-refunded
	if refundErr != nil {
		t.Fatal("refund race lost verified refund", refundErr)
	}
	if closeErr != nil {
		if !errors.Is(closeErr, ErrSellerSourceClosureConflict) {
			t.Fatal(closeErr)
		}
		_, input = sourceClosureInput(t, pool, command.ID)
	}
	out, err := service.CloseSellerSourceReversal(ctx, actor, command.ID, input, "refund-race-source-close", "retry")
	if err != nil || out.Closure.Resolution != "refund_recovered" {
		t.Fatal("refund race did not settle debt", out, err)
	}
	assertSourceClosure(t, pool, request, 1)
	balance, err := singleSellerFunds(t, service, ctx, request.SellerID)
	if err != nil || balance.WithdrawableCents != 0 || balance.ReservedCents != 0 || balance.RecoveryDueCents != 0 {
		t.Fatal("refund race minted money", balance, err)
	}
}
