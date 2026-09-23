package payments

import (
	"context"
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

func applySourceReversalExecutionMigration(t *testing.T, pool *pgxpool.Pool, direction string) {
	t.Helper()
	if direction == "down" {
		applySourceReversalClosureMigration(t, pool, direction)
	}
	body, err := os.ReadFile("../platform/database/migrations/0171_seller_source_reversal_execution." + direction + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), string(body)); err != nil {
		t.Fatal(err)
	}
	if direction == "up" {
		applySourceReversalClosureMigration(t, pool, direction)
	}
}

type sourceReversalExecutionRuntime struct {
	*sourceReversalCommandRuntime
	creates, queries atomic.Int32
	createReversal   func(context.Context, TransferReversalRequest) (TransferReversalResult, error)
	lookupReversal   func(context.Context, TransferReversalRequest) (TransferReversalResult, error)
	beforeIdentity   func(context.Context) error
}

func reversalObservation(input TransferReversalRequest) TransferReversalResult {
	return TransferReversalResult{Outcome: "found", Pages: 1,
		Transfer: TransferObservation{Transfer: Transfer{ProviderID: input.ProviderTransferID, DestinationID: input.DestinationID,
			AmountCents: input.AmountCents, Currency: input.Currency, TransferGroup: transferGroup(input.PaymentID)},
			PaymentID: input.PaymentID, ProviderChargeID: input.ProviderChargeID, LiveMode: input.LiveMode,
			CreatedAt: input.ReservedAt.Add(-time.Hour), AmountReversed: input.AmountCents},
		Observations: []TransferReversal{{ProviderID: "trr_" + strings.ReplaceAll(input.CommandID.String(), "-", ""),
			ProviderTransferID: input.ProviderTransferID, CommandID: input.CommandID, AmountCents: input.ReverseAmountCents,
			Currency: input.Currency, CreatedAt: input.ReservedAt}},
	}
}

func (r *sourceReversalExecutionRuntime) CreateTransferReversal(ctx context.Context, input TransferReversalRequest) (TransferReversalResult, error) {
	r.creates.Add(1)
	if r.createReversal != nil {
		return r.createReversal(ctx, input)
	}
	result := reversalObservation(input)
	result.Pages = 0
	return result, nil
}

func (r *sourceReversalExecutionRuntime) LookupTransferReversal(ctx context.Context, input TransferReversalRequest) (TransferReversalResult, error) {
	r.queries.Add(1)
	if r.lookupReversal != nil {
		return r.lookupReversal(ctx, input)
	}
	return reversalObservation(input), nil
}

func (r *sourceReversalExecutionRuntime) ProductCheckoutIdentity(ctx context.Context) (ProductCheckoutIdentity, error) {
	if r.beforeIdentity != nil {
		if err := r.beforeIdentity(ctx); err != nil {
			return ProductCheckoutIdentity{}, err
		}
	}
	return r.sourceReversalCommandRuntime.ProductCheckoutIdentity(ctx)
}

func sourceReversalExecutionFixture(t *testing.T) (*pgxpool.Pool, *Service, *sourceReversalExecutionRuntime, SellerPayoutRequest, SellerSourceReversalCommand, jobs.Job) {
	t.Helper()
	pool, service, original, request, actor := sourceReversalCommandFixture(t)
	out, err := service.SubmitSellerSourceReversal(t.Context(), actor, request.ID, reversalCommandInput(t, pool, request), "reversal-execution", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	runtime := &sourceReversalExecutionRuntime{sourceReversalCommandRuntime: original}
	service.runtimes = NewRuntimeCatalog(runtime)
	job := jobs.Job{ID: out.Command.JobID, Kind: SellerSourceReversalJobKind}
	if err := pool.QueryRow(t.Context(), `SELECT payload FROM jobs WHERE id=$1`, job.ID).Scan(&job.Payload); err != nil {
		t.Fatal(err)
	}
	return pool, service, runtime, request, out.Command, job
}

func assertReversalFundsRetained(t *testing.T, pool *pgxpool.Pool, service *Service, request SellerPayoutRequest, results int) {
	t.Helper()
	var source string
	var observed, released int
	if err := pool.QueryRow(t.Context(), `SELECT t.status,
 (SELECT count(*) FROM seller_source_reversal_results x JOIN seller_source_reversal_commands c ON c.id=x.command_id WHERE c.payout_request_id=$1),
 (SELECT count(*) FROM seller_ledger_entries WHERE payout_request_id=$1 AND entry_type IN ('payout_release','payout_debit','payout_return'))
 FROM seller_payout_transfers t WHERE t.payout_request_id=$1`, request.ID).Scan(&source, &observed, &released); err != nil || source != "succeeded" || observed != results || released != 0 {
		t.Fatalf("source=%s results=%d released=%d want succeeded/%d/0: %v", source, observed, released, results, err)
	}
	balance, err := singleSellerFunds(t, service, t.Context(), request.SellerID)
	if err != nil || balance.ReservedCents != request.AmountCents || balance.WithdrawableCents != 0 {
		t.Fatal("reversal observation released funds", balance, err)
	}
}

func TestSellerSourceReversalExecutionObservedAndReadOnlyReplay(t *testing.T) {
	pool, service, runtime, request, command, job := sourceReversalExecutionFixture(t)
	ctx := t.Context()
	if err := service.HandleSellerSourceReversalJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	assertReversalFundsRetained(t, pool, service, request, 1)
	service.config.Enabled = false
	if _, err := pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=(SELECT actor_id FROM seller_source_reversal_commands WHERE id=$1)`, command.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleSellerSourceReversalJob(ctx, job); err != nil {
		t.Fatal("readonly recovery required write permission", err)
	}
	assertReversalFundsRetained(t, pool, service, request, 1)
	var status, failure string
	if err := pool.QueryRow(ctx, `SELECT status,failure_code FROM seller_payout_requests WHERE id=$1`, request.ID).Scan(&status, &failure); err != nil || status != "reconciliation_required" || failure != "source_reversal_observed" {
		t.Fatal("observation disguised as ledger closure", status, failure, err)
	}
	if runtime.creates.Load() != 1 || runtime.queries.Load() != 1 || runtime.transfers.Load() != 1 || runtime.payouts.Load() != 0 {
		t.Fatal("unexpected external funds execution")
	}
	for _, sql := range []string{`DELETE FROM seller_source_reversal_dispatches`, `UPDATE seller_source_reversal_dispatches SET started_at=clock_timestamp()`,
		`DELETE FROM seller_source_reversal_reads`, `UPDATE seller_source_reversal_reads SET evidence='{}'`,
		`DELETE FROM seller_source_reversal_results`, `UPDATE seller_source_reversal_results SET provider_reversal_id='trr_changed123'`} {
		_, err := pool.Exec(ctx, sql)
		requirePayoutConstraint(t, err)
	}
}

func TestSellerSourceReversalExecutionLostResponseAndAtomicRecovery(t *testing.T) {
	for _, scenario := range []string{"response_lost", "final_read_lost", "audit_failure", "crash_before_post"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, request, command, job := sourceReversalExecutionFixture(t)
			ctx := t.Context()
			switch scenario {
			case "response_lost":
				runtime.createReversal = func(context.Context, TransferReversalRequest) (TransferReversalResult, error) {
					return TransferReversalResult{}, context.DeadlineExceeded
				}
			case "final_read_lost":
				runtime.createReversal = func(_ context.Context, input TransferReversalRequest) (TransferReversalResult, error) {
					result := reversalObservation(input)
					result.Transfer.AmountReversed = 0
					return result, context.DeadlineExceeded
				}
			case "audit_failure":
				if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_reversal_result_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.action='seller_payout.source_reversal_checked' THEN RAISE EXCEPTION 'injected reversal audit failure'; END IF; RETURN NEW; END; $$;
 CREATE TRIGGER fail_reversal_result_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_reversal_result_audit()`); err != nil {
					t.Fatal(err)
				}
			case "crash_before_post":
				if _, err := pool.Exec(ctx, `INSERT INTO seller_source_reversal_dispatches(command_id,job_id,started_at,deadline_at)
 SELECT $1,$2,t,t+interval '20 seconds' FROM clock_timestamp() t`, command.ID, job.ID); err != nil {
					t.Fatal(err)
				}
				runtime.lookupReversal = func(_ context.Context, input TransferReversalRequest) (TransferReversalResult, error) {
					result := reversalObservation(input)
					result.Outcome, result.Observations, result.Transfer.AmountReversed = "not_found", nil, 0
					return result, nil
				}
			}
			if err := service.HandleSellerSourceReversalJob(ctx, job); err == nil {
				t.Fatal("uncertain result completed")
			}
			assertReversalFundsRetained(t, pool, service, request, 0)
			if scenario == "audit_failure" {
				if _, err := pool.Exec(ctx, `DROP TRIGGER fail_reversal_result_audit ON audit_events; DROP FUNCTION fail_reversal_result_audit()`); err != nil {
					t.Fatal(err)
				}
			}
			runtime.lookupReversal = nil
			if err := service.HandleSellerSourceReversalJob(ctx, job); err != nil {
				t.Fatal("query failed to recover original result", err)
			}
			assertReversalFundsRetained(t, pool, service, request, 1)
			wantPosts, wantQueries := int32(1), int32(1)
			if scenario == "crash_before_post" {
				wantPosts, wantQueries = 0, 2
			}
			if runtime.creates.Load() != wantPosts || runtime.queries.Load() != wantQueries {
				t.Fatal("recovery resent reversal")
			}
			var open int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM seller_source_reversal_reads WHERE command_id=$1 AND finished_at IS NULL`, command.ID).Scan(&open); err != nil || open != 0 {
				t.Fatal("recovered result left orphan read", open, err)
			}
		})
	}
}

func TestSellerSourceReversalExecutionRejectsContradictoryEvidence(t *testing.T) {
	for _, scenario := range []string{"amount", "parent", "mode", "partial", "duplicate", "other_command", "changed_id", "invalid_then_valid"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, request, _, job := sourceReversalExecutionFixture(t)
			runtime.createReversal = func(_ context.Context, input TransferReversalRequest) (TransferReversalResult, error) {
				result := reversalObservation(input)
				switch scenario {
				case "amount", "invalid_then_valid":
					result.Observations[0].AmountCents++
				case "parent":
					result.Transfer.ProviderID = "tr_wrong123"
				case "mode":
					result.Transfer.LiveMode = !input.LiveMode
				case "partial":
					result.Transfer.AmountReversed--
				case "duplicate":
					result.Observations = append(result.Observations, result.Observations[0])
					result.Outcome = "ambiguous"
				case "other_command":
					result.Observations[0].CommandID = uuid.New()
				case "changed_id":
					result.Observations[0].ProviderID = "trr_other123"
					return result, context.DeadlineExceeded
				}
				return result, nil
			}
			if err := service.HandleSellerSourceReversalJob(t.Context(), job); err == nil {
				t.Fatal("contradictory evidence accepted")
			}
			assertReversalFundsRetained(t, pool, service, request, 0)
			if err := service.HandleSellerSourceReversalJob(t.Context(), job); err == nil {
				t.Fatal("later valid read erased contradiction")
			}
			assertReversalFundsRetained(t, pool, service, request, 0)
		})
	}
}

func TestSellerSourceReversalExecutionConcurrentSend(t *testing.T) {
	pool, service, runtime, request, _, job := sourceReversalExecutionFixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- service.HandleSellerSourceReversalJob(t.Context(), job) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if runtime.creates.Load() > 1 || runtime.creates.Load()+runtime.queries.Load() < 1 {
		t.Fatal("concurrent execution violated first-send boundary")
	}
	assertReversalFundsRetained(t, pool, service, request, 1)
}

func TestSellerSourceReversalExecutionStoppedAndRevoked(t *testing.T) {
	for _, scenario := range []string{"stopped", "revoked", "disabled", "wrong_job", "stop_before_post"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, request, command, job := sourceReversalExecutionFixture(t)
			var sql string
			switch scenario {
			case "stopped":
				sql = `UPDATE jobs SET status='cancelled' WHERE id=(SELECT job_id FROM seller_source_reversal_commands WHERE id=$1)`
			case "revoked":
				sql = `UPDATE users SET role='member' WHERE id=(SELECT actor_id FROM seller_source_reversal_commands WHERE id=$1)`
			case "disabled":
				service.config.Enabled = false
			case "wrong_job":
				job.ID = uuid.New()
			case "stop_before_post":
				runtime.beforeIdentity = func(ctx context.Context) error {
					_, err := pool.Exec(ctx, `UPDATE jobs SET status='cancelled' WHERE id=$1`, job.ID)
					return err
				}
			}
			if sql != "" {
				if _, err := pool.Exec(t.Context(), sql, command.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := service.HandleSellerSourceReversalJob(t.Context(), job); err == nil {
				t.Fatal("ineligible first send accepted")
			}
			if runtime.creates.Load() != 0 {
				t.Fatal("ineligible command sent money")
			}
			assertReversalFundsRetained(t, pool, service, request, 0)
			var markers int
			if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM seller_source_reversal_dispatches WHERE command_id=$1`, command.ID).Scan(&markers); err != nil {
				t.Fatal(err)
			}
			want := 0
			if scenario == "stop_before_post" {
				want = 1
			}
			if markers != want {
				t.Fatal("first-send evidence lost or consumed too early", markers, want)
			}
		})
	}
}

func TestSellerSourceReversalExecutionMigrationRetention(t *testing.T) {
	pool, service, _, request, _, job := sourceReversalExecutionFixture(t)
	applySourceReversalExecutionMigration(t, pool, "down")
	applySourceReversalExecutionMigration(t, pool, "up")
	if err := service.HandleSellerSourceReversalJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../platform/database/migrations/0171_seller_source_reversal_execution.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	_, err = tx.Exec(t.Context(), string(down))
	requirePayoutConstraint(t, err)
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertReversalFundsRetained(t, pool, service, request, 1)
}
