package payments

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

type sellerBankExecutionRuntime struct {
	*settlementReadRuntime
	payouts, bankReads atomic.Int32
	status             string
	create             func(context.Context, PayoutRequest) (Payout, error)
	lookupBank         func(context.Context, PayoutRequest) (PayoutLookupResult, error)
}

func bankObservation(input PayoutRequest, status string) Payout {
	return Payout{ProviderID: "po_" + strings.ReplaceAll(input.PayoutRequestID.String(), "-", ""), Destination: input.DestinationID,
		BankDestinationID: input.BankDestinationID, AmountCents: input.AmountCents, Currency: input.Currency, Status: status, CreatedAt: input.ReservedAt}
}

func (r *sellerBankExecutionRuntime) CreatePayout(ctx context.Context, input PayoutRequest) (Payout, error) {
	r.payouts.Add(1)
	if r.create != nil {
		return r.create(ctx, input)
	}
	return bankObservation(input, r.status), nil
}

func (r *sellerBankExecutionRuntime) ReadPayout(ctx context.Context, input PayoutRequest, _ string) (Payout, error) {
	r.bankReads.Add(1)
	return bankObservation(input, r.status), nil
}

func (r *sellerBankExecutionRuntime) LookupPayout(ctx context.Context, input PayoutRequest) (PayoutLookupResult, error) {
	r.bankReads.Add(1)
	if r.lookupBank != nil {
		return r.lookupBank(ctx, input)
	}
	return PayoutLookupResult{Outcome: "found", Pages: 1, Observations: []Payout{bankObservation(input, r.status)}}, nil
}

func bankExecutionFixture(t *testing.T) (*pgxpool.Pool, *Service, *sellerBankExecutionRuntime, Checkout, SellerPayoutRequest, uuid.UUID, jobs.Job) {
	t.Helper()
	pool, service, sourceRuntime, checkout, request, sourceJob := sellerFundingFixture(t)
	if err := service.HandleSellerPayoutFundingJob(t.Context(), sourceJob); err != nil {
		t.Fatal(err)
	}
	actor, input := bankCommandInput(t, pool, request)
	command, err := service.ReserveSellerBankPayout(t.Context(), actor, request.ID, input, "bank-execution-fixture", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	jobID, err := service.QueueSellerBankPayout(t.Context(), actor, command.Command.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := service.QueueSellerBankPayout(t.Context(), actor, command.Command.ID); err != nil || again != jobID {
		t.Fatal("bank queue replay", again, err)
	}
	job := jobs.Job{ID: jobID, Kind: SellerBankPayoutJobKind}
	if err := pool.QueryRow(t.Context(), `SELECT payload FROM jobs WHERE id=$1`, jobID).Scan(&job.Payload); err != nil {
		t.Fatal(err)
	}
	runtime := &sellerBankExecutionRuntime{settlementReadRuntime: sourceRuntime, status: "paid"}
	service.runtimes = NewRuntimeCatalog(runtime)
	return pool, service, runtime, checkout, request, command.Command.ID, job
}

func assertBankLedger(t *testing.T, pool *pgxpool.Pool, request SellerPayoutRequest, debit, returned int, status string) {
	t.Helper()
	var debits, returns int
	var parent string
	if err := pool.QueryRow(t.Context(), `SELECT
 (SELECT count(*) FROM seller_ledger_entries WHERE payout_request_id=$1 AND entry_type='payout_debit'),
 (SELECT count(*) FROM seller_ledger_entries WHERE payout_request_id=$1 AND entry_type='payout_return'),
 (SELECT status FROM seller_payout_requests WHERE id=$1)`, request.ID).Scan(&debits, &returns, &parent); err != nil || debits != debit || returns != returned || parent != status {
		t.Fatalf("debit=%d return=%d parent=%s; want %d/%d/%s: %v", debits, returns, parent, debit, returned, status, err)
	}
}

func TestSellerBankExecutionPaidAndReturnedLedger(t *testing.T) {
	pool, service, runtime, _, request, command, job := bankExecutionFixture(t)
	ctx := t.Context()
	for range 2 {
		if err := service.HandleSellerBankPayoutJob(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	assertBankLedger(t, pool, request, 1, 0, "succeeded")
	balance, err := singleSellerFunds(t, service, ctx, request.SellerID)
	if err != nil || balance.AvailableCents != 0 || balance.ReservedCents != 0 || balance.WithdrawableCents != 0 {
		t.Fatalf("paid bank money became reusable %+v: %v", balance, err)
	}
	if runtime.payouts.Load() != 1 || runtime.bankReads.Load() != 1 || runtime.transfers.Load() != 1 {
		t.Fatal("duplicate external money movement")
	}
	// Authenticated observation must remain possible after finance permission and
	// the new-write switch are removed. Neither may erase an actual bank return.
	service.config.Enabled = false
	if _, err := pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=(SELECT actor_id FROM seller_bank_payout_commands WHERE id=$1)`, command); err != nil {
		t.Fatal(err)
	}
	runtime.status = "failed"
	for range 2 {
		if err := service.HandleSellerBankPayoutJob(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	assertBankLedger(t, pool, request, 1, 1, "reconciliation_required")
	balance, err = singleSellerFunds(t, service, ctx, request.SellerID)
	if err != nil || balance.AvailableCents != request.AmountCents || balance.ReservedCents != request.AmountCents || balance.WithdrawableCents != 0 {
		t.Fatalf("returned funds escaped reconciliation %+v: %v", balance, err)
	}
	runtime.status = "paid"
	if err := service.HandleSellerBankPayoutJob(ctx, job); err == nil {
		t.Fatal("stale paid result erased the confirmed return")
	}
	assertBankLedger(t, pool, request, 1, 1, "reconciliation_required")
	for _, query := range []string{`DELETE FROM seller_bank_payout_dispatches`, `UPDATE seller_bank_payout_dispatches SET started_at=NULL,deadline_at=NULL`,
		`DELETE FROM seller_bank_payout_reads`, `UPDATE seller_bank_payout_reads SET evidence='{}'`, `DELETE FROM seller_bank_payout_results`,
		`UPDATE seller_bank_payout_results SET status='paid'`, `UPDATE seller_payout_requests SET status='succeeded'`} {
		_, err := pool.Exec(ctx, query)
		requirePayoutConstraint(t, err)
	}
	down, err := os.ReadFile("../platform/database/migrations/0166_seller_bank_payout_execution.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, string(down))
	requirePayoutConstraint(t, err)
}

func TestSellerBankExecutionLostResponseAndAtomicRecovery(t *testing.T) {
	for _, scenario := range []string{"response_lost", "audit_failure", "crash_before_post"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, _, request, command, job := bankExecutionFixture(t)
			ctx := t.Context()
			switch scenario {
			case "response_lost":
				runtime.create = func(context.Context, PayoutRequest) (Payout, error) {
					return Payout{}, context.DeadlineExceeded
				}
			case "audit_failure":
				if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_bank_result_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.action='seller_payout.bank_observed' THEN RAISE EXCEPTION 'injected bank audit failure'; END IF; RETURN NEW; END; $$;
 CREATE TRIGGER fail_bank_result_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_bank_result_audit()`); err != nil {
					t.Fatal(err)
				}
			case "crash_before_post":
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(ctx)
				if _, err := tx.Exec(ctx, `SELECT set_config('app.seller_bank_execution_job',$1,true)`, job.ID.String()); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(ctx, `UPDATE seller_bank_payout_dispatches SET started_at=t,deadline_at=t+interval '20 seconds' FROM clock_timestamp() t WHERE command_id=$1`, command); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				runtime.lookupBank = func(context.Context, PayoutRequest) (PayoutLookupResult, error) {
					return PayoutLookupResult{Outcome: "not_found", Pages: 1, Observations: []Payout{}}, nil
				}
			}
			if err := service.HandleSellerBankPayoutJob(ctx, job); err == nil {
				t.Fatal("unconfirmed bank result succeeded")
			}
			parent := "reconciliation_required"
			if scenario == "audit_failure" {
				parent = "processing"
				if _, err := pool.Exec(ctx, `DROP TRIGGER fail_bank_result_audit ON audit_events; DROP FUNCTION fail_bank_result_audit()`); err != nil {
					t.Fatal(err)
				}
			}
			assertBankLedger(t, pool, request, 0, 0, parent)
			service.config.Enabled = false
			runtime.lookupBank = nil
			if err := service.HandleSellerBankPayoutJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			assertBankLedger(t, pool, request, 1, 0, "succeeded")
			if scenario == "audit_failure" {
				var unfinished, superseded int
				if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE finished_at IS NULL),
 count(*) FILTER (WHERE outcome='superseded' AND error_code='bank_read_superseded'
 AND evidence->'observations'='[]'::jsonb AND evidence ? 'supersededBy')
 FROM seller_bank_payout_reads WHERE command_id=$1`, command).Scan(&unfinished, &superseded); err != nil || unfinished != 0 || superseded != 1 {
					t.Fatalf("lost read was not retained as superseded: unfinished=%d superseded=%d err=%v", unfinished, superseded, err)
				}
			}
			wantPosts := int32(1)
			if scenario == "crash_before_post" {
				wantPosts = 0
			}
			if runtime.payouts.Load() != wantPosts || runtime.bankReads.Load() < 1 {
				t.Fatal("recovery resent bank payout")
			}
		})
	}
}

func TestSellerBankExecutionConflictingEvidenceIsSticky(t *testing.T) {
	for _, scenario := range []string{"wrong_bank", "wrong_amount", "ambiguous", "partial_other", "old_timestamp"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, _, request, _, job := bankExecutionFixture(t)
			ctx := t.Context()
			runtime.create = func(context.Context, PayoutRequest) (Payout, error) { return Payout{}, context.DeadlineExceeded }
			if err := service.HandleSellerBankPayoutJob(ctx, job); err == nil {
				t.Fatal("lost response accepted")
			}
			runtime.lookupBank = func(_ context.Context, input PayoutRequest) (PayoutLookupResult, error) {
				item := bankObservation(input, "paid")
				result := PayoutLookupResult{Outcome: "found", Pages: 1}
				switch scenario {
				case "wrong_bank":
					item.BankDestinationID = "ba_another"
				case "wrong_amount":
					item.AmountCents++
				case "old_timestamp":
					item.CreatedAt = input.ReservedAt.Add(-24 * time.Hour)
				case "partial_other":
					item.ProviderID = "po_different"
					result.Observations = []Payout{item}
					return result, errors.New("second page failed")
				case "ambiguous":
					other := item
					other.ProviderID = "po_different"
					result.Outcome, result.Observations = "ambiguous", []Payout{item, other}
					return result, nil
				}
				result.Observations = []Payout{item}
				return result, nil
			}
			if err := service.HandleSellerBankPayoutJob(ctx, job); err == nil {
				t.Fatal("conflict credited bank payout")
			}
			runtime.lookupBank = nil
			if err := service.HandleSellerBankPayoutJob(ctx, job); err == nil {
				t.Fatal("later result concealed earlier conflicting evidence")
			}
			assertBankLedger(t, pool, request, 0, 0, "reconciliation_required")
			if runtime.payouts.Load() != 1 {
				t.Fatal("conflict caused another bank payout")
			}
		})
	}
}

func TestSellerBankExecutionConcurrentHandlers(t *testing.T) {
	pool, service, runtime, _, request, _, job := bankExecutionFixture(t)
	ctx := t.Context()
	// Another handler can register a lookup before the first sender regains
	// the payment lock. The fake must not invent a remote payout before POST.
	runtime.lookupBank = func(_ context.Context, input PayoutRequest) (PayoutLookupResult, error) {
		if runtime.payouts.Load() == 0 {
			return PayoutLookupResult{Outcome: "not_found", Pages: 1, Observations: []Payout{}}, nil
		}
		return PayoutLookupResult{Outcome: "found", Pages: 1, Observations: []Payout{bankObservation(input, "paid")}}, nil
	}
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- service.HandleSellerBankPayoutJob(ctx, job) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && fundingErrorCode(err) != "payment_settlement_pending" {
			t.Fatal(err)
		}
	}
	if err := service.HandleSellerBankPayoutJob(ctx, job); err != nil {
		t.Fatal("final authenticated recovery", err)
	}
	assertBankLedger(t, pool, request, 1, 0, "succeeded")
	if runtime.payouts.Load() != 1 {
		t.Fatal("concurrent handlers sent multiple payouts", runtime.payouts.Load())
	}
}

func TestSellerBankExecutionEligibilityBeforeFirstSend(t *testing.T) {
	for _, scenario := range []string{"revoked", "refund", "disabled"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, _, request, command, job := bankExecutionFixture(t)
			ctx := t.Context()
			var err error
			switch scenario {
			case "revoked":
				_, err = pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=(SELECT actor_id FROM seller_bank_payout_commands WHERE id=$1)`, command)
			case "refund":
				tx, txErr := pool.Begin(ctx)
				if txErr != nil {
					t.Fatal(txErr)
				}
				defer tx.Rollback(ctx)
				var payment, settlement uuid.UUID
				if err = tx.QueryRow(ctx, `SELECT payment_id,settlement_id FROM seller_bank_payout_commands WHERE id=$1`, command).Scan(&payment, &settlement); err != nil {
					t.Fatal(err)
				}
				if err = lockProductSettlementPaymentTx(ctx, tx, settlement); err == nil {
					err = markProductSettlementRefundTx(ctx, tx, payment)
				}
				if err == nil {
					err = tx.Commit(ctx)
				}
			case "disabled":
				service.config.Enabled = false
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := service.HandleSellerBankPayoutJob(ctx, job); err == nil {
				t.Fatal("ineligible bank payout sent")
			}
			if runtime.payouts.Load() != 0 || runtime.bankReads.Load() != 0 {
				t.Fatal("ineligible payout contacted bank")
			}
			assertBankLedger(t, pool, request, 0, 0, "processing")
		})
	}
}

func TestSellerBankExecutionPendingAndInitialFailure(t *testing.T) {
	pool, service, runtime, _, request, _, job := bankExecutionFixture(t)
	ctx := t.Context()
	for _, status := range []string{"pending", "in_transit", "failed"} {
		runtime.status = status
		err := service.HandleSellerBankPayoutJob(ctx, job)
		if (err == nil) != (status == "failed") {
			t.Fatalf("status %s: %v", status, err)
		}
	}
	assertBankLedger(t, pool, request, 0, 0, "reconciliation_required")
	if runtime.payouts.Load() != 1 || runtime.bankReads.Load() != 2 {
		t.Fatal("bank transit duplicated dispatch")
	}
	balance, err := singleSellerFunds(t, service, ctx, request.SellerID)
	if err != nil || balance.ReservedCents != request.AmountCents || balance.WithdrawableCents != 0 {
		t.Fatalf("failed bank payout released Connect money %+v: %v", balance, err)
	}
}

func TestSellerBankExecutionLateResultRequiresAuthenticatedRecovery(t *testing.T) {
	pool, service, runtime, _, request, _, job := bankExecutionFixture(t)
	ctx := t.Context()
	runtime.create = func(ctx context.Context, input PayoutRequest) (Payout, error) {
		<-ctx.Done()
		// A provider/client that returns a nominal success after cancellation
		// cannot turn the durable deadline into a fresh acceptance window.
		return bankObservation(input, "paid"), nil
	}
	if err := service.HandleSellerBankPayoutJob(ctx, job); err == nil {
		t.Fatal("late bank response consumed funds")
	}
	assertBankLedger(t, pool, request, 0, 0, "reconciliation_required")
	if err := service.HandleSellerBankPayoutJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	assertBankLedger(t, pool, request, 1, 0, "succeeded")
	if runtime.payouts.Load() != 1 || runtime.bankReads.Load() != 1 {
		t.Fatal("late result recovery resent bank payout")
	}
}

func TestSellerBankExecutionConcurrentRefundKeepsBankDebitAndDebt(t *testing.T) {
	pool, service, runtime, _, request, command, job := bankExecutionFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	runtime.create = func(ctx context.Context, input PayoutRequest) (Payout, error) {
		close(entered)
		select {
		case <-release:
			return bankObservation(input, "paid"), nil
		case <-ctx.Done():
			return Payout{}, ctx.Err()
		}
	}
	bankDone := make(chan error, 1)
	go func() { bankDone <- service.HandleSellerBankPayoutJob(ctx, job) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	refund, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer refund.Rollback(ctx)
	var settlement, payment uuid.UUID
	var pid int
	if err := refund.QueryRow(ctx, `SELECT settlement_id,payment_id FROM seller_bank_payout_commands WHERE id=$1`, command).Scan(&settlement, &payment); err != nil {
		t.Fatal(err)
	}
	if err := refund.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	refundDone := make(chan error, 1)
	go func() {
		err := lockProductSettlementPaymentTx(ctx, refund, settlement)
		if err == nil {
			err = markProductSettlementRefundTx(ctx, refund, payment)
		}
		if err == nil {
			err = refund.Commit(ctx)
		}
		refundDone <- err
	}()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids($1))>0`, pid).Scan(&waiting); err != nil {
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
	close(release)
	if err := <-bankDone; err != nil {
		t.Fatal(err)
	}
	if err := <-refundDone; err != nil {
		t.Fatal(err)
	}
	assertBankLedger(t, pool, request, 1, 0, "succeeded")
	balance, err := singleSellerFunds(t, service, ctx, request.SellerID)
	if err != nil || balance.AvailableCents != 0 || balance.WithdrawableCents != 0 || balance.RecoveryDueCents != request.AmountCents {
		t.Fatalf("concurrent refund lost recovery debt %+v: %v", balance, err)
	}
}

func TestSellerBankExecutionMissingRuntimeDoesNotConsumeFirstSend(t *testing.T) {
	for _, scenario := range []string{"runtime", "reader", "creator"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, _, request, command, job := bankExecutionFixture(t)
			switch scenario {
			case "runtime":
				service.runtimes = NewRuntimeCatalog()
			case "reader":
				service.runtimes = NewRuntimeCatalog(runtime.settlementReadRuntime)
			case "creator":
				service.runtimes = NewRuntimeCatalog(struct {
					ProviderRuntime
					SellerPayoutReader
				}{runtime.settlementReadRuntime, runtime})
			}
			if err := service.HandleSellerBankPayoutJob(t.Context(), job); err == nil {
				t.Fatal("missing runtime capability was accepted")
			}
			var started bool
			var reads int
			if err := pool.QueryRow(t.Context(), `SELECT d.started_at IS NOT NULL,
 (SELECT count(*) FROM seller_bank_payout_reads WHERE command_id=d.command_id)
 FROM seller_bank_payout_dispatches d WHERE command_id=$1`, command).Scan(&started, &reads); err != nil || started || reads != 0 {
				t.Fatalf("missing capability consumed first send: started=%v reads=%d err=%v", started, reads, err)
			}
			assertBankLedger(t, pool, request, 0, 0, "processing")
			service.runtimes = NewRuntimeCatalog(runtime)
			if err := service.HandleSellerBankPayoutJob(t.Context(), job); err != nil {
				t.Fatal("restored capability could not send", err)
			}
			assertBankLedger(t, pool, request, 1, 0, "succeeded")
			if runtime.payouts.Load() != 1 || runtime.bankReads.Load() != 0 {
				t.Fatal("capability repair did not preserve the single first send")
			}
		})
	}
}

func TestSellerBankExecutionStoppedJobCannotStart(t *testing.T) {
	pool, service, runtime, _, request, _, job := bankExecutionFixture(t)
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET status='cancelled' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleSellerBankPayoutJob(t.Context(), job); err == nil {
		t.Fatal("stopped job started a bank payout")
	}
	if runtime.payouts.Load() != 0 {
		t.Fatal("stopped job sent money")
	}
	assertBankLedger(t, pool, request, 0, 0, "processing")
}
