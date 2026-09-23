package payments

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSellerSourceReversalClosureAuthorityAndVersion(t *testing.T) {
	pool, service, _, request, command, job := sourceReversalExecutionFixture(t)
	ctx := t.Context()
	if err := service.HandleSellerSourceReversalJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	actor, input := sourceClosureInput(t, pool, command.ID)
	for _, scenario := range []string{"unconfirmed", "stale", "read", "reason", "key", "self", "revoked"} {
		t.Run(scenario, func(t *testing.T) {
			changed, who, key := input, actor, "rejected-source-close"
			switch scenario {
			case "unconfirmed":
				changed.Confirmed = false
			case "stale":
				changed.ExpectedUpdatedAt = changed.ExpectedUpdatedAt.Add(-time.Microsecond)
			case "read":
				changed.ReadID = uuid.New()
			case "reason":
				changed.Reason = "short"
			case "key":
				key = "short"
			case "self":
				who = request.SellerID
				if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, who); err != nil {
					t.Fatal(err)
				}
			case "revoked":
				if _, err := pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := service.CloseSellerSourceReversal(ctx, who, command.ID, changed, key, "reject"); err == nil {
				t.Fatal("invalid closure accepted")
			}
			assertSourceClosure(t, pool, request, 0)
			assertReversalFundsRetained(t, pool, service, request, 1)
		})
	}
	// Funds already returned must not become orphaned when the first actor loses authority.
	replacement := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Replacement finance','admin')`, replacement, replacement.String()+"@test.local", "close_"+replacement.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CloseSellerSourceReversal(ctx, replacement, command.ID, input, "replacement-finance-close", "trace"); err != nil {
		t.Fatal("current finance could not consume return", err)
	}
	assertSourceClosure(t, pool, request, 1)
	if _, err := pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CloseSellerSourceReversal(ctx, replacement, command.ID, input, "replacement-finance-close", "retry"); err == nil {
		t.Fatal("revoked actor read private replay")
	}
}

func TestSellerSourceReversalClosureConcurrentReplay(t *testing.T) {
	pool, service, _, request, command, job := sourceReversalExecutionFixture(t)
	ctx := t.Context()
	if err := service.HandleSellerSourceReversalJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	actor, input := sourceClosureInput(t, pool, command.ID)
	type result struct {
		out SellerSourceClosureSubmission
		err error
	}
	results := make(chan result, 6)
	var wg sync.WaitGroup
	for range cap(results) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := service.CloseSellerSourceReversal(ctx, actor, command.ID, input, "concurrent-close-key", "trace")
			results <- result{out, err}
		}()
	}
	wg.Wait()
	close(results)
	created := 0
	var id uuid.UUID
	for r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		if !r.out.Replayed {
			created++
		}
		if id != uuid.Nil && id != r.out.Closure.ID {
			t.Fatal("different closures")
		}
		id = r.out.Closure.ID
	}
	if created != 1 {
		t.Fatal("new closures", created)
	}
	assertSourceClosure(t, pool, request, 1)
}

func TestSellerSourceReversalClosureEvidenceAndMigrationRetention(t *testing.T) {
	pool, service, _, request, command, job := sourceReversalExecutionFixture(t)
	ctx := t.Context()
	if err := service.HandleSellerSourceReversalJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	actor, input := sourceClosureInput(t, pool, command.ID)
	// A closure row alone must not bypass the ledger/event/audit transaction.
	_, err := pool.Exec(ctx, `INSERT INTO seller_source_reversal_closures(id,command_id,read_id,payout_request_id,settlement_id,seller_id,
 actor_id,expected_updated_at,resolution,release_entry_id,amount_cents,currency,reason,idempotency_key,request_id)
 SELECT $1,c.id,x.read_id,c.payout_request_id,c.settlement_id,c.seller_id,c.actor_id,r.updated_at,'released',$2,c.amount_cents,c.currency,
 'Try to commit without consuming funds proof.','incomplete-closure','trace'
 FROM seller_source_reversal_commands c JOIN seller_source_reversal_results x ON x.command_id=c.id
 JOIN seller_payout_requests r ON r.id=c.payout_request_id WHERE c.id=$3`, uuid.New(), uuid.New(), command.ID)
	if err == nil {
		t.Fatal("unconsumed closure committed")
	}
	assertSourceClosure(t, pool, request, 0)
	if _, err := service.CloseSellerSourceReversal(ctx, actor, command.ID, input, "retained-source-close", "trace"); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{`DELETE FROM seller_source_reversal_closures`, `UPDATE seller_source_reversal_closures SET reason='replace accepted closure reason'`,
		`UPDATE seller_payout_requests SET status='processing' WHERE id=$1`, `DELETE FROM seller_ledger_entries WHERE payout_request_id=$1 AND entry_type='payout_release'`} {
		args := []any{}
		if sql[len(sql)-2:] == "$1" {
			args = append(args, request.ID)
		}
		// Ledger deletion query has a predicate after its sole parameter.
		if sql == `DELETE FROM seller_ledger_entries WHERE payout_request_id=$1 AND entry_type='payout_release'` {
			args = []any{request.ID}
		}
		if _, err := pool.Exec(ctx, sql, args...); err == nil {
			t.Fatal("financial evidence changed", sql)
		}
	}
	down, err := os.ReadFile("../platform/database/migrations/0172_seller_source_reversal_closure.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, string(down))
	requirePayoutConstraint(t, err)
	assertSourceClosure(t, pool, request, 1)
}

func TestSellerSourceReversalClosureUnresolvedReadRetainsFunds(t *testing.T) {
	for _, scenario := range []string{"open", "not_found", "partial", "late", "conflicting"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, request, command, job := sourceReversalExecutionFixture(t)
			ctx := t.Context()
			if err := service.HandleSellerSourceReversalJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			actor, input := sourceClosureInput(t, pool, command.ID)
			if scenario == "open" {
				if _, err := pool.Exec(ctx, `INSERT INTO seller_source_reversal_reads(command_id,kind,started_at,deadline_at) SELECT $1,'query',t,t+interval '20 seconds' FROM clock_timestamp() t`, command.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				runtime.lookupReversal = func(_ context.Context, in TransferReversalRequest) (TransferReversalResult, error) {
					out := reversalObservation(in)
					switch scenario {
					case "not_found":
						out.Outcome = "not_found"
						out.Observations = nil
						out.Transfer.AmountReversed = 0
					case "partial":
						out.Transfer.AmountReversed--
						out.Observations[0].AmountCents--
					case "late":
						return out, context.DeadlineExceeded
					case "conflicting":
						out.Observations[0].ProviderID = "trr_different"
					}
					return out, nil
				}
				if err := service.HandleSellerSourceReversalJob(ctx, job); err == nil {
					t.Fatal("uncertain observation accepted")
				}
				_, input = sourceClosureInput(t, pool, command.ID)
			}
			if _, err := service.CloseSellerSourceReversal(ctx, actor, command.ID, input, "unresolved-source-close", "trace"); err == nil {
				t.Fatal("unresolved evidence released funds")
			}
			assertSourceClosure(t, pool, request, 0)
			assertReversalFundsRetained(t, pool, service, request, 1)
		})
	}
}

func confirmSourceClosureRefund(t *testing.T, pool *pgxpool.Pool, service *Service, commandID uuid.UUID) {
	t.Helper()
	if err := service.HandlePaymentEventJob(t.Context(), prepareSourceClosureRefund(t, pool, service, commandID)); err != nil {
		t.Fatal(err)
	}
}

func prepareSourceClosureRefund(t *testing.T, pool *pgxpool.Pool, service *Service, commandID uuid.UUID) jobs.Job {
	t.Helper()
	ctx := t.Context()
	var payment, order, buyer, product uuid.UUID
	var amount int
	if err := pool.QueryRow(ctx, `SELECT p.id,o.id,o.buyer_id,o.product_id,p.amount_cents FROM seller_source_reversal_commands c JOIN payment_intents p ON p.id=c.payment_id JOIN orders o ON o.id=p.order_id WHERE c.id=$1`, commandID).Scan(&payment, &order, &buyer, &product, &amount); err != nil {
		t.Fatal(err)
	}
	// The funding fixture has a zero-day refund policy to mature immediately.
	// Allow its buyer to initiate the normal refund workflow for this funds test;
	// do not forge refunded statuses, successful attempts or provider evidence.
	if _, err := pool.Exec(ctx, `UPDATE orders SET refund_window_days_snapshot=30 WHERE id=$1`, order); err != nil {
		t.Fatal(err)
	}
	if _, err := service.BeginProductRefund(ctx, buyer, order, "source-return-refund", "trace", "The delivered resource requires a full refund."); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, payment)); err != nil {
		t.Fatal(err)
	}
	var refund string
	if err := pool.QueryRow(ctx, `SELECT provider_refund_id FROM payment_intents WHERE id=$1`, payment).Scan(&refund); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	service.verifier.now = func() time.Time { return now }
	receipt := receivePaymentWorkflowEvent(t, service, productRefundEvent("evt_source_close_refund", refund, "succeeded", payment, product, now.Unix(), amount), now)
	return jobs.Job{Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}
}

func TestSellerSourceReversalClosureRefundDoesNotRestoreWithdrawable(t *testing.T) {
	for _, when := range []string{"before_close", "after_close", "rollback"} {
		t.Run(when, func(t *testing.T) {
			pool, service, _, request, command, job := sourceReversalExecutionFixture(t)
			settlement, _ := sourceClosureBinding(t, pool, command.ID)
			ctx := t.Context()
			if err := service.HandleSellerSourceReversalJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if when != "after_close" {
				confirmSourceClosureRefund(t, pool, service, command.ID)
			}
			actor, input := sourceClosureInput(t, pool, command.ID)
			if when == "rollback" {
				if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_refund_close() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='seller_payout.source_reversal_closed' THEN RAISE EXCEPTION 'fail refund closure'; END IF; RETURN NEW; END; $$; CREATE TRIGGER fail_refund_close BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_refund_close()`); err != nil {
					t.Fatal(err)
				}
				if _, err := service.CloseSellerSourceReversal(ctx, actor, command.ID, input, "refunded-source-close", "trace"); err == nil {
					t.Fatal("rollback succeeded")
				}
				assertSourceClosure(t, pool, request, 0)
				var due int
				if err := pool.QueryRow(ctx, `SELECT remaining_cents FROM seller_recovery_obligations WHERE settlement_id=$1`, settlement).Scan(&due); err != nil || due != request.AmountCents {
					t.Fatal("rollback lost debt", due, err)
				}
				if _, err := pool.Exec(ctx, `DROP TRIGGER fail_refund_close ON audit_events; DROP FUNCTION fail_refund_close()`); err != nil {
					t.Fatal(err)
				}
			}
			out, err := service.CloseSellerSourceReversal(ctx, actor, command.ID, input, "refunded-source-close", "trace")
			want := "refund_recovered"
			if when == "after_close" {
				want = "released"
			}
			if err != nil || out.Closure.Resolution != want {
				t.Fatal("refund closure", out, err)
			}
			if when == "after_close" {
				confirmSourceClosureRefund(t, pool, service, command.ID)
			}
			balance, err := singleSellerFunds(t, service, ctx, request.SellerID)
			if err != nil || balance.ReservedCents != 0 || balance.WithdrawableCents != 0 || balance.RecoveryDueCents != 0 {
				t.Fatal("refund restored income or debt", balance, err)
			}
			assertSourceClosure(t, pool, request, 1)
			if _, err := service.CreateSellerPayoutRequestForSettlement(ctx, request.SellerID, settlement, request.AmountCents, "refunded-new-payout"); err == nil {
				t.Fatal("refunded settlement funded again")
			}
			if when != "after_close" {
				_, err := pool.Exec(ctx, `UPDATE seller_recovery_obligations SET remaining_cents=amount_cents,status='open' WHERE settlement_id=$1`, settlement)
				requirePayoutConstraint(t, err)
			}
		})
	}
}

func TestSellerSourceReversalClosureCanFundAgain(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprintf("lost_%t", lost), func(t *testing.T) {
			pool, service, runtime, request, command, job := sourceReversalExecutionFixture(t)
			settlement, originalTransfer := sourceClosureBinding(t, pool, command.ID)
			ctx := t.Context()
			if err := service.HandleSellerSourceReversalJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			actor, input := sourceClosureInput(t, pool, command.ID)
			if _, err := service.CloseSellerSourceReversal(ctx, actor, command.ID, input, "source-close-refund-again", "trace"); err != nil {
				t.Fatal(err)
			}
			next, err := service.CreateSellerPayoutRequestForSettlement(ctx, request.SellerID, settlement, request.AmountCents, "payout-after-source-return")
			if err != nil {
				t.Fatal("could not allocate returned settlement", err)
			}
			finance, admission := approveSellerFundingFixture(t, pool, service, next)
			admitted, err := service.AdmitSellerPayoutFunding(ctx, finance, next.ID, admission, "fund-after-source-return", "trace")
			if err != nil {
				t.Fatal(err)
			}
			nextJob := jobs.Job{ID: admitted.JobID, Kind: SellerPayoutFundingJobKind}
			if err := pool.QueryRow(ctx, `SELECT payload FROM jobs WHERE id=$1`, nextJob.ID).Scan(&nextJob.Payload); err != nil {
				t.Fatal(err)
			}
			runtime.lost = lost
			runtime.transform = func(in Transfer) Transfer { in.ProviderID = "tr_new_after_return"; return in }
			runtime.onTransfer = func(_ context.Context, in TransferRequest) {
				if in.SourceRequestID != next.ID || transferDispatchKey(in) == "transfer-"+in.PaymentID.String() {
					t.Error("replacement source reused original key", in)
				}
			}
			var frozenKey string
			if err := pool.QueryRow(ctx, `SELECT dispatch_key FROM seller_payout_transfers WHERE payout_request_id=$1`, next.ID).Scan(&frozenKey); err != nil || frozenKey != "seller-source-"+next.ID.String() {
				t.Fatal("replacement source key not frozen", frozenKey, err)
			}
			if err := service.HandleSellerPayoutFundingJob(ctx, nextJob); (err != nil) != lost {
				t.Fatal("new dispatch result", err)
			}
			if lost {
				runtime.lookup = func(_ context.Context, in TransferLookupRequest) (TransferLookupResult, error) {
					if len(in.ReturnedSources) != 1 || in.ReturnedSources[0].ProviderID != originalTransfer || in.ReturnedSources[0].AmountReversed != request.AmountCents {
						t.Error("closed original proof not propagated", in.ReturnedSources)
					}
					out := observedSettlementTransfer(in)
					out.Observations[0].ProviderID = "tr_new_after_return"
					return out, nil
				}
				if err := service.HandleSellerPayoutFundingJob(ctx, nextJob); err != nil {
					t.Fatal("new source lookup failed", err)
				}
			}
			parent := "processing"
			if lost {
				parent = "reconciliation_required"
			}
			assertSellerFunding(t, pool, next, "succeeded", parent)
			assertSellerFunding(t, pool, request, "succeeded", "cancelled")
			assertSourceClosure(t, pool, request, 1)
			wantReads := int32(0)
			if lost {
				wantReads = 1
			}
			if runtime.transfers.Load() != 2 || runtime.reads.Load() != wantReads {
				t.Fatal("new transfer retried POST", runtime.transfers.Load(), runtime.reads.Load())
			}
			if err := service.HandleSellerSourceReversalJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			balance, err := singleSellerFunds(t, service, ctx, request.SellerID)
			if err != nil || balance.ReservedCents != request.AmountCents || balance.WithdrawableCents != 0 {
				t.Fatal("new allocation incorrect", balance, err)
			}
			if _, err := service.CloseSellerSourceReversal(ctx, actor, command.ID, input, "another-source-close", "trace"); !errors.Is(err, ErrSellerSourceClosureConflict) {
				t.Fatal("old closure released new allocation", err)
			}
			// The replacement source must remain usable for the actual bank leg.
			bankActor, bankInput := bankCommandInput(t, pool, next)
			bank, err := service.SubmitSellerBankPayout(ctx, bankActor, next.ID, bankInput, "bank-after-returned-source", "trace")
			if err != nil {
				t.Fatal("new source cannot reach bank leg", err)
			}
			bankJob := jobs.Job{ID: bank.JobID, Kind: SellerBankPayoutJobKind}
			if err := pool.QueryRow(ctx, `SELECT payload FROM jobs WHERE id=$1`, bankJob.ID).Scan(&bankJob.Payload); err != nil {
				t.Fatal(err)
			}
			if err := service.HandleSellerBankPayoutJob(ctx, bankJob); err != nil {
				t.Fatal(err)
			}
			assertSellerFunding(t, pool, next, "succeeded", "succeeded")
			balance, err = singleSellerFunds(t, service, ctx, request.SellerID)
			if err != nil || balance.ReservedCents != 0 || balance.WithdrawableCents != 0 {
				t.Fatal("new bank payment did not consume returned funds", balance, err)
			}
		})
	}
}

func sourceClosureBinding(t *testing.T, pool *pgxpool.Pool, commandID uuid.UUID) (uuid.UUID, string) {
	t.Helper()
	var settlement uuid.UUID
	var transfer string
	if err := pool.QueryRow(t.Context(), `SELECT settlement_id,provider_transfer_id FROM seller_source_reversal_commands WHERE id=$1`, commandID).Scan(&settlement, &transfer); err != nil {
		t.Fatal(err)
	}
	return settlement, transfer
}
