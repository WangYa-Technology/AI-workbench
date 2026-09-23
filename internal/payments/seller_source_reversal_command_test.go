package payments

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type sourceReversalCommandRuntime struct {
	*sellerBankExecutionRuntime
	reversals atomic.Int32
}

func (r *sourceReversalCommandRuntime) CreateTransferReversal(context.Context, TransferReversalRequest) (TransferReversalResult, error) {
	r.reversals.Add(1)
	return TransferReversalResult{}, errors.New("command reservation must not send money")
}

func (r *sourceReversalCommandRuntime) LookupTransferReversal(context.Context, TransferReversalRequest) (TransferReversalResult, error) {
	r.reversals.Add(1)
	return TransferReversalResult{}, errors.New("command reservation must not contact provider")
}

func reversalCommandInput(t *testing.T, pool *pgxpool.Pool, request SellerPayoutRequest) SellerSourceReversalInput {
	t.Helper()
	input := SellerSourceReversalInput{Confirmed: true, Reason: "Return the original funded source after reviewing the bank obligation."}
	if err := pool.QueryRow(t.Context(), `SELECT t.id,r.updated_at,bank.command_id,bank.result_id
 FROM seller_payout_requests r JOIN seller_payout_transfers t ON t.payout_request_id=r.id
 LEFT JOIN LATERAL seller_source_reversal_bank_state(r.id) bank ON true
 WHERE r.id=$1`, request.ID).Scan(&input.SourceTransferID, &input.ExpectedUpdatedAt, &input.BankCommandID, &input.BankResultID); err != nil {
		t.Fatal(err)
	}
	return input
}

func sourceReversalCommandFixture(t *testing.T) (*pgxpool.Pool, *Service, *sourceReversalCommandRuntime, SellerPayoutRequest, uuid.UUID) {
	t.Helper()
	pool, service, source, _, request, job := sellerFundingFixture(t)
	if err := service.HandleSellerPayoutFundingJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	actor, _ := bankCommandInput(t, pool, request)
	runtime := &sourceReversalCommandRuntime{sellerBankExecutionRuntime: &sellerBankExecutionRuntime{settlementReadRuntime: source, status: "paid"}}
	service.runtimes = NewRuntimeCatalog(runtime)
	return pool, service, runtime, request, actor
}

func assertSourceReversalCommandCount(t *testing.T, pool *pgxpool.Pool, want int) {
	t.Helper()
	var commands, jobs, events, audits int
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM seller_source_reversal_commands),
 (SELECT count(*) FROM jobs WHERE kind='payment.reverse_seller_source'),
 (SELECT count(*) FROM seller_payout_request_events WHERE event_type='source_reversal.queued'),
 (SELECT count(*) FROM audit_events WHERE action='seller_payout.source_reversal_queued')`).Scan(&commands, &jobs, &events, &audits); err != nil || commands != want || jobs != want || events != want || audits != want {
		t.Fatalf("reversal command/job/event/audit=%d/%d/%d/%d want %d: %v", commands, jobs, events, audits, want, err)
	}
}

func TestSellerSourceReversalCommandAtomicReplayAndBankExclusion(t *testing.T) {
	pool, service, runtime, request, actor := sourceReversalCommandFixture(t)
	input := reversalCommandInput(t, pool, request)
	ctx := t.Context()
	out, err := service.SubmitSellerSourceReversal(ctx, actor, request.ID, input, "source-reversal-command", "trace")
	if err != nil || out.Replayed || out.Command.ID == uuid.Nil || out.Command.BankDisposition != "not_reserved" || out.Command.AmountCents != request.AmountCents {
		t.Fatalf("reserve %+v %v", out, err)
	}
	assertSourceReversalCommandCount(t, pool, 1)
	_, bankInput := bankCommandInput(t, pool, request)
	if _, err := service.SubmitSellerBankPayout(ctx, actor, request.ID, bankInput, "bank-after-reversal", "trace"); !errors.Is(err, ErrSellerBankPayoutConflict) {
		t.Fatal("bank command bypassed reversal", err)
	}
	projection, err := service.GetSellerBankPayoutOperation(ctx, actor, request.ID)
	if err != nil || projection.CanSubmit || projection.CanResume {
		t.Fatal("bank operation still advertised", projection, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='cancelled' WHERE id=$1`, out.Command.JobID); err != nil {
		t.Fatal(err)
	}
	service.config.Enabled = false
	again, err := service.SubmitSellerSourceReversal(ctx, actor, request.ID, input, "source-reversal-command", "retry")
	if err != nil || !again.Replayed || again.Command.ID != out.Command.ID || again.Command.JobID != out.Command.JobID {
		t.Fatal("same-key recovery changed evidence", again, err)
	}
	var jobStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM jobs WHERE id=$1`, out.Command.JobID).Scan(&jobStatus); err != nil || jobStatus != "cancelled" {
		t.Fatal("replay revived stopped job", jobStatus, err)
	}
	for _, change := range []string{"source", "version", "bank", "reason", "request"} {
		t.Run(change, func(t *testing.T) {
			changed, req := input, request.ID
			switch change {
			case "source":
				changed.SourceTransferID = uuid.New()
			case "version":
				changed.ExpectedUpdatedAt = input.ExpectedUpdatedAt.Add(time.Microsecond)
			case "bank":
				changed.BankCommandID = new(uuid.UUID)
				*changed.BankCommandID = uuid.New()
			case "reason":
				changed.Reason += " Changed."
			case "request":
				req = uuid.New()
			}
			if _, err := service.SubmitSellerSourceReversal(ctx, actor, req, changed, "source-reversal-command", "conflict"); !errors.Is(err, ErrSellerSourceReversalConflict) {
				t.Fatal("changed replay accepted", err)
			}
		})
	}
	for _, query := range []string{`UPDATE seller_source_reversal_commands SET reason='replace the original evidence'`, `DELETE FROM seller_source_reversal_commands`, `UPDATE seller_payout_requests SET status='cancelled' WHERE id=$1`} {
		args := []any{}
		if query[len(query)-2:] == "$1" {
			args = append(args, request.ID)
		}
		if _, err := pool.Exec(ctx, query, args...); err == nil {
			t.Fatal("immutable funds evidence modified", query)
		}
	}
	balance, err := singleSellerFunds(t, service, ctx, request.SellerID)
	if err != nil || balance.ReservedCents != request.AmountCents || balance.WithdrawableCents != 0 {
		t.Fatal("reservation released before actual reversal", balance, err)
	}
	if runtime.reversals.Load() != 0 || runtime.payouts.Load() != 0 || runtime.transfers.Load() != 1 {
		t.Fatal("command reservation contacted provider")
	}
	assertSourceReversalCommandCount(t, pool, 1)
}

func TestSellerSourceReversalCommandBankStates(t *testing.T) {
	for _, scenario := range []string{"unstarted", "stopped", "failed", "returned", "paid", "pending", "unknown", "conflicting", "open_read", "later_pending", "later_unknown"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, bankRuntime, _, request, command, job := bankExecutionFixture(t)
			actor, _ := bankCommandInput(t, pool, request)
			runtime := &sourceReversalCommandRuntime{sellerBankExecutionRuntime: bankRuntime}
			service.runtimes = NewRuntimeCatalog(runtime)
			ctx := t.Context()
			switch scenario {
			case "stopped":
				if _, err := pool.Exec(ctx, `UPDATE jobs SET status='cancelled' WHERE id=$1`, job.ID); err != nil {
					t.Fatal(err)
				}
			case "failed", "returned", "paid", "pending", "conflicting", "open_read", "later_pending", "later_unknown":
				if scenario == "failed" || scenario == "open_read" || scenario == "later_pending" || scenario == "later_unknown" {
					runtime.status = "failed"
				}
				if scenario == "pending" {
					runtime.status = "pending"
				}
				if err := service.HandleSellerBankPayoutJob(ctx, job); err != nil && scenario != "pending" {
					t.Fatal(err)
				}
				if scenario == "returned" {
					runtime.status = "failed"
					if err := service.HandleSellerBankPayoutJob(ctx, job); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "conflicting" {
					runtime.lookupBank = func(_ context.Context, input PayoutRequest) (PayoutLookupResult, error) {
						a, b := bankObservation(input, "failed"), bankObservation(input, "failed")
						b.ProviderID = "po_another123"
						return PayoutLookupResult{Outcome: "ambiguous", Pages: 1, Observations: []Payout{a, b}}, nil
					}
					if err := service.HandleSellerBankPayoutJob(ctx, job); err == nil {
						t.Fatal("contradiction accepted")
					}
				}
				if scenario == "open_read" {
					if _, err := pool.Exec(ctx, `INSERT INTO seller_bank_payout_reads(command_id,kind,started_at,deadline_at)
 SELECT $1,'query',t,t+interval '20 seconds' FROM clock_timestamp() t`, command); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "later_pending" || scenario == "later_unknown" {
					runtime.status = "pending"
					if scenario == "later_unknown" {
						runtime.lookupBank = func(context.Context, PayoutRequest) (PayoutLookupResult, error) {
							return PayoutLookupResult{Outcome: "not_found", Observations: []Payout{}}, nil
						}
					}
					if err := service.HandleSellerBankPayoutJob(ctx, job); err == nil {
						t.Fatal("later unresolved result accepted")
					}
				}
			case "unknown":
				runtime.create = func(context.Context, PayoutRequest) (Payout, error) { return Payout{}, errors.New("lost response") }
				if err := service.HandleSellerBankPayoutJob(ctx, job); err == nil {
					t.Fatal("lost response accepted")
				}
			}
			allowed := scenario == "unstarted" || scenario == "stopped" || scenario == "failed" || scenario == "returned"
			view, readErr := service.GetSellerSourceReversalOperation(ctx, actor, request.ID)
			if readErr != nil || view.CanSubmit != allowed || view.CanClose {
				t.Fatal("bank obligation projection", scenario, view, readErr)
			}
			input := reversalCommandInput(t, pool, request)
			out, err := service.SubmitSellerSourceReversal(ctx, actor, request.ID, input, "bank-disposition-reversal", "test")
			if !allowed {
				if !errors.Is(err, ErrSellerSourceReversalConflict) {
					t.Fatal("unresolved bank obligation accepted", out, err)
				}
				assertSourceReversalCommandCount(t, pool, 0)
				return
			}
			want := scenario
			if scenario == "unstarted" || scenario == "stopped" {
				want = "not_started"
			}
			if err != nil || out.Command.BankDisposition != want || out.Command.BankCommandID == nil || *out.Command.BankCommandID != command {
				t.Fatal("resolved bank disposition rejected", out, err)
			}
			if want == "not_started" {
				if err := service.HandleSellerBankPayoutJob(ctx, job); err == nil {
					t.Fatal("bank sent after reversal decision")
				}
				if runtime.payouts.Load() != 0 {
					t.Fatal("bank provider called after reversal")
				}
				if scenario == "stopped" {
					if _, err := service.ResumeSellerBankPayout(ctx, actor, request.ID, bankResumeInput(command, job.ID), "resume-after-reversal", "test"); !errors.Is(err, ErrSellerBankPayoutConflict) {
						t.Fatal("resume bypassed reversal", err)
					}
				}
			}
			assertSourceReversalCommandCount(t, pool, 1)
			if runtime.reversals.Load() != 0 {
				t.Fatal("reservation sent reversal")
			}
		})
	}
}

func TestSellerSourceReversalCommandRollbackAndConcurrentReplay(t *testing.T) {
	pool, service, _, request, actor := sourceReversalCommandFixture(t)
	input := reversalCommandInput(t, pool, request)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_source_reversal_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.action='seller_payout.source_reversal_queued' THEN RAISE EXCEPTION 'injected audit failure'; END IF; RETURN NEW; END; $$;
 CREATE TRIGGER fail_source_reversal_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_source_reversal_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SubmitSellerSourceReversal(ctx, actor, request.ID, input, "atomic-source-reversal", "test"); err == nil {
		t.Fatal("committed without audit")
	}
	assertSourceReversalCommandCount(t, pool, 0)
	if _, err := pool.Exec(ctx, `DROP TRIGGER fail_source_reversal_audit ON audit_events; DROP FUNCTION fail_source_reversal_audit()`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	results := make(chan SellerSourceReversalSubmission, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := service.SubmitSellerSourceReversal(ctx, actor, request.ID, input, "atomic-source-reversal", "concurrent")
			errs <- err
			results <- out
		}()
	}
	wg.Wait()
	close(errs)
	close(results)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	created := 0
	var id uuid.UUID
	for out := range results {
		if id == uuid.Nil {
			id = out.Command.ID
		}
		if out.Command.ID != id {
			t.Fatal("replay changed command")
		}
		if !out.Replayed {
			created++
		}
	}
	if created != 1 {
		t.Fatal("creation count", created)
	}
	if _, err := service.SubmitSellerSourceReversal(ctx, actor, request.ID, input, "competing-source-key", "test"); !errors.Is(err, ErrSellerSourceReversalConflict) {
		t.Fatal("second key bypassed source uniqueness", err)
	}
	assertSourceReversalCommandCount(t, pool, 1)
}

func applySourceReversalCommandMigration(t *testing.T, pool *pgxpool.Pool, direction string) {
	t.Helper()
	if direction == "down" {
		applySourceReversalExecutionMigration(t, pool, direction)
	}
	body, err := os.ReadFile("../platform/database/migrations/0170_seller_source_reversal_commands." + direction + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), string(body)); err != nil {
		t.Fatal(err)
	}
	if direction == "up" {
		applySourceReversalExecutionMigration(t, pool, direction)
	}
}

func TestSellerSourceReversalCommandMigrationRetention(t *testing.T) {
	pool, service, _, request, actor := sourceReversalCommandFixture(t)
	applySourceReversalCommandMigration(t, pool, "down")
	applySourceReversalCommandMigration(t, pool, "up")
	input := reversalCommandInput(t, pool, request)
	if _, err := service.SubmitSellerSourceReversal(t.Context(), actor, request.ID, input, "retained-source-command", "test"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile("../platform/database/migrations/0170_seller_source_reversal_commands.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), string(body))
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "55000" {
		t.Fatal("migration discarded command", err)
	}
	assertSourceReversalCommandCount(t, pool, 1)
}

func TestSellerSourceReversalCommandStaleBankSnapshot(t *testing.T) {
	for _, level := range []pgx.TxIsoLevel{pgx.ReadCommitted, pgx.RepeatableRead, pgx.Serializable} {
		t.Run(string(level), func(t *testing.T) {
			pool, service, bank, _, request, command, _ := bankExecutionFixture(t)
			service.runtimes = NewRuntimeCatalog(&sourceReversalCommandRuntime{sellerBankExecutionRuntime: bank})
			actor, _ := bankCommandInput(t, pool, request)
			input := reversalCommandInput(t, pool, request)
			tx, err := pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: level})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			if _, err = tx.Exec(t.Context(), `SELECT count(*) FROM seller_source_reversal_commands`); err != nil {
				t.Fatal(err)
			}
			if _, err = service.SubmitSellerSourceReversal(t.Context(), actor, request.ID, input, "snapshot-reversal-command", "test"); err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(t.Context(), `SELECT assert_seller_bank_payout_command(c) FROM seller_bank_payout_commands c WHERE id=$1`, command)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || (pgerr.Code != "23514" && pgerr.Code != "40001") {
				t.Fatal("old snapshot authorized bank send", err)
			}
		})
	}
}

func TestSellerSourceReversalCommandEligibilityAndAuthority(t *testing.T) {
	for _, scenario := range []string{"unconfirmed", "stale_version", "wrong_source", "wrong_bank", "wrong_result", "invalid_key", "short_reason", "self", "revoked", "disabled", "unsupported", "legacy_protocol"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, request, actor := sourceReversalCommandFixture(t)
			input := reversalCommandInput(t, pool, request)
			key := "rejected-source-reversal"
			want := ErrSellerSourceReversalInvalid
			switch scenario {
			case "unconfirmed":
				input.Confirmed = false
			case "stale_version":
				input.ExpectedUpdatedAt = input.ExpectedUpdatedAt.Add(-time.Second)
				want = ErrSellerSourceReversalConflict
			case "wrong_source":
				input.SourceTransferID = uuid.New()
				want = ErrSellerSourceReversalConflict
			case "wrong_bank":
				id := uuid.New()
				input.BankCommandID = &id
				want = ErrSellerSourceReversalConflict
			case "wrong_result":
				id := uuid.New()
				input.BankResultID = &id
			case "invalid_key":
				key = "bad key"
			case "short_reason":
				input.Reason = "short"
			case "self":
				actor = request.SellerID
				want = ErrFinanceForbidden
				if _, err := pool.Exec(t.Context(), `UPDATE users SET role='admin' WHERE id=$1`, actor); err != nil {
					t.Fatal(err)
				}
			case "revoked":
				want = ErrFinanceForbidden
				if _, err := pool.Exec(t.Context(), `UPDATE users SET role='member' WHERE id=$1`, actor); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				service.config.Enabled = false
				want = ErrDisabled
			case "unsupported":
				service.runtimes = NewRuntimeCatalog(runtime.sellerBankExecutionRuntime)
				want = ErrProviderUnavailable
			case "legacy_protocol":
				tx, err := pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(t.Context())
				if _, err = tx.Exec(t.Context(), `SELECT set_config('app.seller_reversal_protocol','',true)`); err != nil {
					t.Fatal(err)
				}
				_, err = tx.Exec(t.Context(), `SELECT lock_seller_payout_disposition($1)`, request.ID)
				var pgerr *pgconn.PgError
				if !errors.As(err, &pgerr) || pgerr.Code != "23514" {
					t.Fatal("old protocol accepted", err)
				}
				assertSourceReversalCommandCount(t, pool, 0)
				return
			}
			if _, err := service.SubmitSellerSourceReversal(t.Context(), actor, request.ID, input, key, "test"); !errors.Is(err, want) {
				t.Fatal("incorrect rejection", err, want)
			}
			assertSourceReversalCommandCount(t, pool, 0)
		})
	}
}

func TestSellerSourceReversalCommandAuthorizationAfterWait(t *testing.T) {
	pool, service, _, request, actor := sourceReversalCommandFixture(t)
	input := reversalCommandInput(t, pool, request)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var pid int
	if err = tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT lock_seller_payout_disposition($1)`, request.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := service.SubmitSellerSourceReversal(ctx, actor, request.ID, input, "wait-revoke-source", "test")
		done <- err
	}()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database()
 AND $1=ANY(pg_blocking_pids(pid)) AND query LIKE '%lock_seller_payout_disposition%')`, pid).Scan(&waiting); err != nil {
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
	if _, err = pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, ErrFinanceForbidden) {
		t.Fatal("authority revocation during wait ignored", err)
	}
	assertSourceReversalCommandCount(t, pool, 0)
}

func TestSellerSourceReversalCommandBankSendWinsRace(t *testing.T) {
	pool, service, bank, _, request, _, job := bankExecutionFixture(t)
	actor, _ := bankCommandInput(t, pool, request)
	service.runtimes = NewRuntimeCatalog(&sourceReversalCommandRuntime{sellerBankExecutionRuntime: bank})
	input := reversalCommandInput(t, pool, request)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	bank.create = func(ctx context.Context, input PayoutRequest) (Payout, error) {
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
	done := make(chan error, 1)
	go func() {
		_, err := service.SubmitSellerSourceReversal(ctx, actor, request.ID, input, "race-source-reversal", "test")
		done <- err
	}()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database()
 AND cardinality(pg_blocking_pids(pid))>0 AND query LIKE '%SELECT lock_seller_payout_disposition%')`).Scan(&waiting); err != nil {
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
	if err := <-done; !errors.Is(err, ErrSellerSourceReversalConflict) {
		t.Fatal("reversal overtook bank send", err)
	}
	assertSourceReversalCommandCount(t, pool, 0)
	assertBankLedger(t, pool, request, 1, 0, "succeeded")
}
