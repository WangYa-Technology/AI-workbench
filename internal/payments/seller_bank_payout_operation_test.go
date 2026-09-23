package payments

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

func bankOperationFixture(t *testing.T) (*pgxpool.Pool, *Service, *sellerBankExecutionRuntime, SellerPayoutRequest, uuid.UUID, SellerBankPayoutInput) {
	t.Helper()
	pool, service, source, _, request, job := sellerFundingFixture(t)
	if err := service.HandleSellerPayoutFundingJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	actor, input := bankCommandInput(t, pool, request)
	runtime := &sellerBankExecutionRuntime{settlementReadRuntime: source, status: "paid"}
	service.runtimes = NewRuntimeCatalog(runtime)
	return pool, service, runtime, request, actor, input
}

func TestSellerBankOperationAtomicSubmissionAndReplay(t *testing.T) {
	pool, service, runtime, request, actor, input := bankOperationFixture(t)
	ctx := t.Context()
	before, err := service.GetSellerBankPayoutOperation(ctx, actor, request.ID)
	if err != nil || !before.CanSubmit || before.Bank != nil {
		t.Fatalf("bank candidate %+v: %v", before, err)
	}
	first, err := service.SubmitSellerBankPayout(ctx, actor, request.ID, input, "bank-operation-once", "operation")
	if err != nil || first.Replayed || first.JobID == uuid.Nil || first.Operation.CanSubmit || first.Operation.Bank == nil || first.Operation.Bank.JobStatus == nil || *first.Operation.Bank.JobStatus != "queued" {
		t.Fatalf("submission %+v: %v", first, err)
	}
	service.config.Enabled = false
	again, err := service.SubmitSellerBankPayout(ctx, actor, request.ID, input, "bank-operation-once", "retry")
	if err != nil || !again.Replayed || again.JobID != first.JobID || again.Command.ID != first.Command.ID {
		t.Fatalf("replay %+v: %v", again, err)
	}
	changed := input
	changed.AmountCents++
	if _, err := service.SubmitSellerBankPayout(ctx, actor, request.ID, changed, "bank-operation-once", "conflict"); !errors.Is(err, ErrSellerBankPayoutConflict) {
		t.Fatal("changed input reused key", err)
	}
	if runtime.payouts.Load() != 0 || runtime.bankReads.Load() != 0 {
		t.Fatal("HTTP-style submission contacted the bank")
	}
	assertBankCommandCount(t, pool, 1)
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed' WHERE id=$1`, first.JobID); err != nil {
		t.Fatal(err)
	}
	again, err = service.SubmitSellerBankPayout(ctx, actor, request.ID, input, "bank-operation-once", "stopped")
	if err != nil || again.Operation.Bank == nil || again.Operation.Bank.JobStatus == nil || *again.Operation.Bank.JobStatus != "failed" {
		t.Fatal("replay revived stopped bank job", again, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SubmitSellerBankPayout(ctx, actor, request.ID, input, "bank-operation-once", "revoked"); !errors.Is(err, ErrFinanceForbidden) {
		t.Fatal("revoked actor replayed", err)
	}
	if _, err := service.GetSellerBankPayoutOperation(ctx, actor, request.ID); !errors.Is(err, ErrFinanceForbidden) {
		t.Fatal("revoked actor read bank operation", err)
	}
}

func TestSellerBankOperationQueueAuditFailureRollsBackCommand(t *testing.T) {
	pool, service, runtime, request, actor, input := bankOperationFixture(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_bank_queue_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.action='seller_payout.bank_queued' THEN RAISE EXCEPTION 'injected queue audit failure'; END IF; RETURN NEW; END; $$;
 CREATE TRIGGER reject_bank_queue_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_bank_queue_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SubmitSellerBankPayout(ctx, actor, request.ID, input, "bank-operation-atomic", "test"); err == nil {
		t.Fatal("failed audit accepted")
	}
	assertBankCommandCount(t, pool, 0)
	var jobs, dispatches, events int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM jobs WHERE kind='payment.execute_seller_bank_payout'),
 (SELECT count(*) FROM seller_bank_payout_dispatches),(SELECT count(*) FROM seller_payout_request_events WHERE event_type='bank.queued')`).Scan(&jobs, &dispatches, &events); err != nil || jobs+dispatches+events != 0 {
		t.Fatal("partial bank command committed", jobs, dispatches, events, err)
	}
	if runtime.payouts.Load() != 0 {
		t.Fatal("rollback moved bank funds")
	}
}

func TestSellerBankOperationConcurrentConfirmation(t *testing.T) {
	pool, service, _, request, actor, input := bankOperationFixture(t)
	var wg sync.WaitGroup
	results := make(chan SellerBankPayoutSubmission, 3)
	errs := make(chan error, 3)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := service.SubmitSellerBankPayout(t.Context(), actor, request.ID, input, "bank-operation-parallel", "test")
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first uuid.UUID
	for result := range results {
		if first == uuid.Nil {
			first = result.JobID
		}
		if result.JobID != first {
			t.Fatal("multiple jobs for one confirmation")
		}
	}
	assertBankCommandCount(t, pool, 1)
}

func TestSellerBankOperationCapabilityAndEligibility(t *testing.T) {
	pool, service, runtime, request, actor, input := bankOperationFixture(t)
	ctx := t.Context()
	service.runtimes = NewRuntimeCatalog(runtime.settlementReadRuntime)
	view, err := service.GetSellerBankPayoutOperation(ctx, actor, request.ID)
	if err != nil || view.CanSubmit {
		t.Fatal("missing bank runtime is actionable", view, err)
	}
	if _, err := service.SubmitSellerBankPayout(ctx, actor, request.ID, input, "bank-operation-capability", "test"); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatal("missing capability submitted", err)
	}
	assertBankCommandCount(t, pool, 0)
	service.runtimes = NewRuntimeCatalog(runtime)
	changed := input
	changed.Confirmed = false
	if _, err := service.SubmitSellerBankPayout(ctx, actor, request.ID, changed, "bank-operation-confirm", "test"); !errors.Is(err, ErrSellerBankPayoutInvalid) {
		t.Fatal("unconfirmed submitted", err)
	}
	changed = input
	changed.ExpectedRevision++
	if _, err := service.SubmitSellerBankPayout(ctx, actor, request.ID, changed, "bank-operation-revision", "test"); !errors.Is(err, ErrSellerBankPayoutConflict) {
		t.Fatal("wrong approval submitted", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE product_settlement_settings SET payout_mode='automatic' WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	view, err = service.GetSellerBankPayoutOperation(ctx, actor, request.ID)
	if err != nil || view.CanSubmit {
		t.Fatal("disabled payout mode actionable", view, err)
	}
	if _, err := service.SubmitSellerBankPayout(ctx, actor, request.ID, input, "bank-operation-mode", "test"); !errors.Is(err, ErrSellerBankPayoutConflict) {
		t.Fatal("changed payout mode submitted", err)
	}
	assertBankCommandCount(t, pool, 0)
}

// The finance projection must track irreversible financial evidence, rather
// than treating the newest queue status or a stale pending read as bank state.
func TestSellerBankOperationProjectionAcrossBankResults(t *testing.T) {
	pool, service, runtime, request, actor, input := bankOperationFixture(t)
	ctx := t.Context()
	submitted, err := service.SubmitSellerBankPayout(ctx, actor, request.ID, input, "bank-projection-sequence", "test")
	if err != nil {
		t.Fatal(err)
	}
	job := jobs.Job{ID: submitted.JobID, Kind: SellerBankPayoutJobKind}
	if err := pool.QueryRow(ctx, `SELECT payload FROM jobs WHERE id=$1`, job.ID).Scan(&job.Payload); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		observed, displayed, parent string
		debit, returned             int
		requiresReview              bool
	}{
		{"pending", "pending", "processing", 0, 0, false},
		{"paid", "paid", "succeeded", 1, 0, false},
		{"pending", "paid", "succeeded", 1, 0, false},
		{"failed", "failed", "reconciliation_required", 1, 1, true},
		{"paid", "failed", "reconciliation_required", 1, 1, true},
	} {
		runtime.status = step.observed
		// Nonterminal/conflicting observations deliberately return retry errors;
		// the persisted read and projection below determine what may be shown.
		_ = service.HandleSellerBankPayoutJob(ctx, job)
		current, err := service.GetSellerBankPayoutOperation(ctx, actor, request.ID)
		if err != nil || current.Bank == nil || current.Bank.ProviderStatus == nil || *current.Bank.ProviderStatus != step.displayed ||
			current.Request.Status != step.parent || current.Bank.RequiresReview != step.requiresReview || current.CanSubmit ||
			current.Bank.StartedAt == nil || current.Bank.CheckedAt == nil {
			t.Fatalf("observed=%s expected=%s/%s review=%t; got %+v bank=%+v: %v", step.observed, step.displayed, step.parent, step.requiresReview, current, current.Bank, err)
		}
		assertBankLedger(t, pool, request, step.debit, step.returned, step.parent)
	}
	if runtime.payouts.Load() != 1 || runtime.bankReads.Load() != 4 {
		t.Fatalf("projection sequence sent money again: creates=%d reads=%d", runtime.payouts.Load(), runtime.bankReads.Load())
	}
}
