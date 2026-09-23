package payments

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

func stopBankJob(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, status string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET status=$2 WHERE id=$1`, id, status); err != nil {
		t.Fatal(err)
	}
}

func bankResumeInput(command, predecessor uuid.UUID) SellerBankPayoutResumeInput {
	return SellerBankPayoutResumeInput{CommandID: command, ExpectedJobID: predecessor, Confirmed: true,
		Reason: "Continue the stopped bank job without changing the original authorization."}
}

func assertBankResumeCount(t *testing.T, pool *pgxpool.Pool, count int) {
	t.Helper()
	var resumes, events, audits, executions int
	err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM seller_bank_payout_resumes),
 (SELECT count(*) FROM seller_payout_request_events WHERE event_type='bank.resumed'),
 (SELECT count(*) FROM audit_events WHERE action='seller_payout.bank_resumed'),
 (SELECT count(*) FROM jobs WHERE kind='payment.execute_seller_bank_payout')`).Scan(&resumes, &events, &audits, &executions)
	if err != nil || resumes != count || events != count || audits != count || executions != count+1 {
		t.Fatalf("resume history=%d/%d/%d jobs=%d want=%d: %v", resumes, events, audits, executions, count, err)
	}
}

func TestSellerBankResumeImmutableChainAndSingleSend(t *testing.T) {
	pool, service, runtime, _, request, command, original := bankExecutionFixture(t)
	actor, _ := bankCommandInput(t, pool, request)
	ctx := t.Context()
	current := original
	var first SellerBankPayoutResumeResult
	for revision, status := range []string{"cancelled", "failed", "succeeded"} {
		stopBankJob(t, pool, current.ID, status)
		assertOperationalMetric(t, pool, "problem", "seller_bank_dispatch_stopped", "test", 1, 0, 0)
		view, err := service.GetSellerBankPayoutOperation(ctx, actor, request.ID)
		if err != nil || !view.CanResume || view.CanSubmit {
			t.Fatalf("stopped candidate %+v: %v", view, err)
		}
		if err := service.HandleSellerBankPayoutJob(ctx, current); err == nil || runtime.payouts.Load() != 0 {
			t.Fatal("stopped predecessor sent money", err)
		}
		result, err := service.ResumeSellerBankPayout(ctx, actor, request.ID, bankResumeInput(command, current.ID), "bank-resume-"+status, "test")
		if err != nil || result.Replayed || result.Resume.Revision != revision+1 || result.Resume.PredecessorJobID != current.ID || result.Resume.JobID == current.ID {
			t.Fatalf("resume %+v: %v", result, err)
		}
		if result.Operation.CanResume || result.Operation.CanSubmit || result.Operation.Bank == nil || result.Operation.Bank.Resume == nil ||
			result.Operation.Bank.Resume.JobStatus != "queued" || *result.Operation.Bank.JobID != original.ID ||
			result.Operation.Bank.Resume.JobID != result.Resume.JobID {
			t.Fatal("original and effective execution projections were mixed", result.Operation)
		}
		if revision == 0 {
			first = result
		}
		if err := service.HandleSellerBankPayoutJob(ctx, current); !errors.Is(err, ErrSellerBankPayoutInvalid) {
			t.Fatal("superseded execution was accepted", err)
		}
		current = jobs.Job{ID: result.Resume.JobID, Kind: SellerBankPayoutJobKind, Payload: original.Payload}
		assertBankResumeCount(t, pool, revision+1)
		assertOperationalMetric(t, pool, "problem", "seller_bank_dispatch_stopped", "test", 0, 0, 0)
		assertBankLedger(t, pool, request, 0, 0, "processing")
	}
	// Replay recovers the original continuation, never the latest task or a new one.
	service.config.Enabled = false
	replay, err := service.ResumeSellerBankPayout(ctx, actor, request.ID, bankResumeInput(command, original.ID), "bank-resume-cancelled", "retry")
	if err != nil || !replay.Replayed || replay.Resume != first.Resume {
		t.Fatal("replay rewrote continuation history", replay, err)
	}
	service.config.Enabled = true
	if err := service.HandleSellerBankPayoutJob(ctx, current); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleSellerBankPayoutJob(ctx, current); err != nil {
		t.Fatal(err)
	}
	if runtime.payouts.Load() != 1 || runtime.bankReads.Load() != 1 {
		t.Fatal("continuation duplicated bank sends")
	}
	assertBankLedger(t, pool, request, 1, 0, "succeeded")
	assertBankResumeCount(t, pool, 3)
	view, err := service.GetSellerBankPayoutOperation(ctx, actor, request.ID)
	if err != nil || view.CanResume || view.CanSubmit {
		t.Fatal("paid bank command is actionable", view, err)
	}
	stopBankJob(t, pool, current.ID, "succeeded")
	var due time.Time
	if err := pool.QueryRow(ctx, `SELECT due_at FROM seller_bank_payout_check_candidates WHERE command_id=$1`, command).Scan(&due); err != nil {
		t.Fatal("continuation lost post-paid return monitoring", err)
	}
	if n, err := service.reconcileSellerBankPayouts(ctx, 10, due); err != nil || n != 1 {
		t.Fatal("continuation could not schedule readonly reconciliation", n, err)
	}
	check := jobs.Job{Kind: SellerBankPayoutCheckJobKind}
	if err := pool.QueryRow(ctx, `SELECT j.id,j.payload FROM seller_bank_payout_checks c JOIN jobs j ON j.id=c.job_id WHERE command_id=$1`, command).Scan(&check.ID, &check.Payload); err != nil {
		t.Fatal(err)
	}
	runtime.status = "failed"
	if err := service.HandleSellerBankPayoutCheckJob(ctx, check); err != nil {
		t.Fatal(err)
	}
	assertBankLedger(t, pool, request, 1, 1, "reconciliation_required")
	if runtime.payouts.Load() != 1 {
		t.Fatal("continued payout return triggered another send")
	}
	var buyer uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT p.payer_id FROM seller_bank_payout_commands c JOIN payment_intents p ON p.id=c.payment_id WHERE c.id=$1`, command).Scan(&buyer); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []uuid.UUID{request.SellerID, buyer, actor} {
		exporter, id, exportJob := sellerExportFixture(t, pool, owner)
		data, body := sellerExportData(t, exporter, owner, id, exportJob)
		rows := data["sellerBankPayoutResumes"]
		if owner == request.SellerID {
			if len(rows) != 3 {
				t.Fatal("seller continuation export incomplete", rows)
			}
			for _, row := range rows {
				if len(row) != 4 || row["commandId"] != command.String() || row["revision"] == nil || row["createdAt"] == nil || row["id"] == nil {
					t.Fatal("continuation export escaped allowlist", row)
				}
			}
			for _, secret := range []string{original.ID.String(), current.ID.String(), bankResumeInput(command, original.ID).Reason, "bank-resume-cancelled"} {
				if strings.Contains(string(body), secret) {
					t.Fatal("continuation export disclosed internal evidence", secret)
				}
			}
		} else if len(rows) != 0 {
			t.Fatal("foreign account received seller continuation records")
		}
	}
	for _, query := range []string{`DELETE FROM seller_bank_payout_resumes`, `UPDATE seller_bank_payout_resumes SET reason='changed after authorization'`} {
		_, err := pool.Exec(ctx, query)
		requirePayoutConstraint(t, err)
	}
	body, err := os.ReadFile("../platform/database/migrations/0167_seller_bank_payout_resumes.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, string(body))
	requirePayoutConstraint(t, err)
}

func TestSellerBankResumeConcurrentKeys(t *testing.T) {
	for _, same := range []bool{true, false} {
		t.Run(map[bool]string{true: "same_key", false: "different_keys"}[same], func(t *testing.T) {
			pool, service, _, _, request, command, job := bankExecutionFixture(t)
			actor, _ := bankCommandInput(t, pool, request)
			stopBankJob(t, pool, job.ID, "failed")
			var wg sync.WaitGroup
			results := make(chan SellerBankPayoutResumeResult, 2)
			errs := make(chan error, 2)
			for _, key := range []string{"resume-parallel-1", "resume-parallel-2"} {
				if same {
					key = "resume-parallel-1"
				}
				wg.Add(1)
				go func(key string) {
					defer wg.Done()
					r, err := service.ResumeSellerBankPayout(t.Context(), actor, request.ID, bankResumeInput(command, job.ID), key, "test")
					results <- r
					errs <- err
				}(key)
			}
			wg.Wait()
			close(results)
			close(errs)
			conflicts := 0
			for err := range errs {
				if errors.Is(err, ErrSellerBankPayoutConflict) {
					conflicts++
				} else if err != nil {
					t.Fatal(err)
				}
			}
			wantConflicts := 1
			if same {
				wantConflicts = 0
			}
			if conflicts != wantConflicts {
				t.Fatal("unexpected concurrent outcomes", conflicts)
			}
			if same {
				one, two := <-results, <-results
				if one.Resume != two.Resume || one.Replayed == two.Replayed {
					t.Fatal("same key did not recover one continuation")
				}
			}
			assertBankResumeCount(t, pool, 1)
		})
	}
}

func TestSellerBankResumeEligibilityAndAtomicity(t *testing.T) {
	for _, scenario := range []string{"active_job", "disabled", "runtime", "expired", "changed_bank", "mode", "self", "revoked", "original_revoked", "unconfirmed", "wrong_job", "started", "audit_failure"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, _, request, command, job := bankExecutionFixture(t)
			actor, _ := bankCommandInput(t, pool, request)
			ctx := t.Context()
			input := bankResumeInput(command, job.ID)
			stopBankJob(t, pool, job.ID, "failed")
			sql := ""
			switch scenario {
			case "active_job":
				stopBankJob(t, pool, job.ID, "queued")
			case "disabled":
				service.config.Enabled = false
			case "runtime":
				service.runtimes = NewRuntimeCatalog(runtime.settlementReadRuntime)
			case "expired":
				// Fixture-only time travel; production code cannot mutate commands.
				sql = `ALTER TABLE seller_bank_payout_commands DISABLE TRIGGER seller_bank_payout_command_guard;
 UPDATE seller_bank_payout_commands SET created_at=clock_timestamp()-interval '24 hours';
 ALTER TABLE seller_bank_payout_commands ENABLE TRIGGER seller_bank_payout_command_guard;`
			case "changed_bank":
				if _, err := pool.Exec(ctx, `UPDATE payment_destinations SET destination_id='acct_changed' WHERE user_id=$1`, request.SellerID); err != nil {
					t.Fatal(err)
				}
			case "mode":
				sql = `UPDATE product_settlement_settings SET payout_mode='automatic' WHERE singleton=true`
			case "self":
				actor = request.SellerID
				if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, actor); err != nil {
					t.Fatal(err)
				}
			case "revoked", "original_revoked":
				if _, err := pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor); err != nil {
					t.Fatal(err)
				}
				if scenario == "original_revoked" {
					actor = uuid.New()
					if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Other finance','admin')`, actor, actor.String()+"@test.local", "review_"+actor.String()[:8]); err != nil {
						t.Fatal(err)
					}
				}
			case "unconfirmed":
				input.Confirmed = false
			case "wrong_job":
				input.ExpectedJobID = uuid.New()
			case "started":
				stopBankJob(t, pool, job.ID, "queued")
				runtime.status = "pending"
				if err := service.HandleSellerBankPayoutJob(ctx, job); err == nil {
					t.Fatal("pending bank payment settled")
				}
				stopBankJob(t, pool, job.ID, "failed")
			case "audit_failure":
				sql = `CREATE FUNCTION fail_resume_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.action='seller_payout.bank_resumed' THEN RAISE EXCEPTION 'injected resume audit failure'; END IF; RETURN NEW; END; $$;
 CREATE TRIGGER fail_resume_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_resume_audit()`
			}
			if sql != "" {
				if _, err := pool.Exec(ctx, sql); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := service.ResumeSellerBankPayout(ctx, actor, request.ID, input, "resume-rejected-test", "test"); err == nil {
				t.Fatal("ineligible or non-atomic resume accepted")
			}
			assertBankResumeCount(t, pool, 0)
			if oneOf(scenario, "active_job", "disabled", "runtime", "expired", "changed_bank", "mode", "original_revoked", "started", "self") {
				view, err := service.GetSellerBankPayoutOperation(ctx, actor, request.ID)
				if err != nil || view.CanResume {
					t.Fatal("ineligible continuation hint", view, err)
				}
			}
			if scenario != "started" && runtime.payouts.Load() != 0 {
				t.Fatal("resume contacted bank")
			}
		})
	}
}

type stopBeforeBankSendRuntime struct {
	*sellerBankExecutionRuntime
	beforeIdentity func(context.Context) error
}

func (r *stopBeforeBankSendRuntime) ProductCheckoutIdentity(ctx context.Context) (ProductCheckoutIdentity, error) {
	if err := r.beforeIdentity(ctx); err != nil {
		return ProductCheckoutIdentity{}, err
	}
	return r.sellerBankExecutionRuntime.ProductCheckoutIdentity(ctx)
}

func TestSellerBankResumeLateCancellationRetainsRead(t *testing.T) {
	pool, service, runtime, _, request, command, job := bankExecutionFixture(t)
	service.runtimes = NewRuntimeCatalog(&stopBeforeBankSendRuntime{sellerBankExecutionRuntime: runtime, beforeIdentity: func(ctx context.Context) error {
		_, err := pool.Exec(ctx, `UPDATE jobs SET status='cancelled' WHERE id=$1`, job.ID)
		return err
	}})
	if err := service.HandleSellerBankPayoutJob(t.Context(), job); err == nil {
		t.Fatal("late cancellation was accepted")
	}
	if runtime.payouts.Load() != 0 {
		t.Fatal("cancelled job sent money after first marker")
	}
	var retained bool
	if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM seller_bank_payout_reads WHERE command_id=$1 AND kind='create' AND finished_at IS NOT NULL AND requires_review)`, command).Scan(&retained); err != nil || !retained {
		t.Fatal("late cancellation lost its durable failed observation", retained, err)
	}
	assertBankLedger(t, pool, request, 0, 0, "reconciliation_required")
	service.runtimes = NewRuntimeCatalog(runtime)
	runtime.lookupBank = func(context.Context, PayoutRequest) (PayoutLookupResult, error) {
		return PayoutLookupResult{Outcome: "not_found", Pages: 1, Observations: []Payout{}}, nil
	}
	_ = service.HandleSellerBankPayoutJob(t.Context(), job)
	if runtime.payouts.Load() != 0 || runtime.bankReads.Load() != 1 {
		t.Fatal("late cancellation retry performed a first send")
	}
}

func TestSellerBankResumeMigrationRoundTrip(t *testing.T) {
	pool, service, runtime, _, request, _, job := bankExecutionFixture(t)
	applyPaymentExecutionLockMigration(t, pool, "down")
	for _, direction := range []string{"down", "up"} {
		body, err := os.ReadFile("../platform/database/migrations/0167_seller_bank_payout_resumes." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(t.Context(), string(body)); err != nil {
			_ = tx.Rollback(t.Context())
			t.Fatal(err)
		}
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	applyPaymentExecutionLockMigration(t, pool, "up")
	if err := service.HandleSellerBankPayoutJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if runtime.payouts.Load() != 1 {
		t.Fatal("roundtrip broke ordinary dispatch")
	}
	assertBankLedger(t, pool, request, 1, 0, "succeeded")
}

func TestSellerBankResumeProtocolAndCurrentAuthority(t *testing.T) {
	pool, service, runtime, _, request, command, job := bankExecutionFixture(t)
	ctx := t.Context()
	// An older writer cannot start even the original job after this migration.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.seller_bank_resume_protocol','',true),set_config('app.seller_bank_execution_job',$1,true)`, job.ID.String()); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `UPDATE seller_bank_payout_dispatches SET started_at=t,deadline_at=t+interval '20 seconds' FROM clock_timestamp() t WHERE command_id=$1`, command)
	requirePayoutConstraint(t, err)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	stopBankJob(t, pool, job.ID, "cancelled")
	actor := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Continuation finance','admin')`, actor, actor.String()+"@test.local", "review_"+actor.String()[:8]); err != nil {
		t.Fatal(err)
	}
	result, err := service.ResumeSellerBankPayout(ctx, actor, request.ID, bankResumeInput(command, job.ID), "resume-new-finance", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResumeSellerBankPayout(ctx, actor, request.ID, bankResumeInput(command, job.ID), "resume-new-finance", "retry"); !errors.Is(err, ErrFinanceForbidden) {
		t.Fatal("revoked continuation actor replayed", err)
	}
	current := jobs.Job{ID: result.Resume.JobID, Kind: SellerBankPayoutJobKind, Payload: job.Payload}
	if err := service.HandleSellerBankPayoutJob(ctx, current); err == nil || runtime.payouts.Load() != 0 {
		t.Fatal("revoked continuation actor still dispatched", err)
	}
	assertBankLedger(t, pool, request, 0, 0, "processing")
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleSellerBankPayoutJob(ctx, current); err != nil {
		t.Fatal(err)
	}
	assertBankLedger(t, pool, request, 1, 0, "succeeded")
}
