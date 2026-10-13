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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func applySourceDispositionMigration(t *testing.T, pool *pgxpool.Pool, direction string) {
	t.Helper()
	b, e := os.ReadFile("../platform/database/migrations/0179_seller_source_dispositions." + direction + ".sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(t.Context(), string(b)); e != nil {
		t.Fatal(e)
	}
}
func dispositionActor(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(t.Context(), `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Finance reviewer','admin')`, id, id.String()+"@test.local", "disp_"+id.String()[:8]); err != nil {
		t.Fatal(err)
	}
	return id
}
func dispositionInput(t *testing.T, pool *pgxpool.Pool, request uuid.UUID) SellerSourceDispositionInput {
	t.Helper()
	in := SellerSourceDispositionInput{Phase: "propose", Confirmed: true, Reason: "Preserve all reservations and reconcile original Provider evidence read-only."}
	if err := pool.QueryRow(t.Context(), `SELECT updated_at FROM seller_payout_requests WHERE id=$1`, request).Scan(&in.ExpectedUpdatedAt); err != nil {
		t.Fatal(err)
	}
	return in
}
func approvedDisposition(t *testing.T, pool *pgxpool.Pool, s *Service, request, command uuid.UUID) (SellerSourceDisposition, jobs.Job) {
	t.Helper()
	maker, checker := dispositionActor(t, pool), dispositionActor(t, pool)
	in := dispositionInput(t, pool, request)
	out, err := s.DisposeSellerSourceReversal(t.Context(), maker, command, in, "disposition-propose", "test")
	if err != nil {
		t.Fatal(err)
	}
	in.Phase = "approve"
	in.ProposalID = &out.ID
	out, err = s.DisposeSellerSourceReversal(t.Context(), checker, command, in, "disposition-approve", "test")
	if err != nil {
		t.Fatal(err)
	}
	job := jobs.Job{Kind: SellerSourceReversalCheckJobKind}
	if err := pool.QueryRow(t.Context(), `SELECT j.id,j.payload FROM seller_source_reversal_checks c JOIN jobs j ON j.id=c.job_id WHERE c.command_id=$1 ORDER BY c.created_at DESC LIMIT 1`, command).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	return out, job
}

func TestSourceDispositionUnsentReadOnly(t *testing.T) {
	for _, state := range []string{"queued", "cancelled", "failed", "expired"} {
		t.Run(state, func(t *testing.T) {
			pool, s, r, request, command, original := sourceReversalExecutionFixture(t)
			if state == "expired" { // Only historical fixture clocks; immutable production evidence is never edited.
				if _, err := pool.Exec(t.Context(), `ALTER TABLE seller_source_reversal_commands DISABLE TRIGGER USER; UPDATE seller_source_reversal_commands SET created_at=created_at-interval '24 hours'; ALTER TABLE seller_source_reversal_commands ENABLE TRIGGER USER`); err != nil {
					t.Fatal(err)
				}
			} else if _, err := pool.Exec(t.Context(), `UPDATE jobs SET status=$2 WHERE id=$1`, original.ID, state); err != nil {
				t.Fatal(err)
			}
			_, job := approvedDisposition(t, pool, s, request.ID, command.ID)
			r.lookupReversal = func(_ context.Context, in TransferReversalRequest) (TransferReversalResult, error) {
				out := reversalObservation(in)
				out.Outcome = "not_found"
				out.Observations = nil
				out.Transfer.AmountReversed = 0
				return out, nil
			}
			s.config.Enabled = false
			if err := s.HandleSellerSourceReversalCheckJob(t.Context(), job); err == nil {
				t.Fatal("not-found accepted as return")
			}
			if err := s.HandleSellerSourceReversalJob(t.Context(), original); err == nil {
				t.Fatal("original write revived")
			}
			if r.creates.Load() != 0 || r.queries.Load() != 1 {
				t.Fatalf("write/read %d/%d", r.creates.Load(), r.queries.Load())
			}
			var n int
			if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM seller_source_reversal_dispatches`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("fabricated dispatch %d %v", n, err)
			}
			assertReversalFundsRetained(t, pool, s, request, 0)
			if _, err := pool.Exec(t.Context(), `UPDATE seller_source_reversal_dispositions SET reason='altered evidence'`); err == nil {
				t.Fatal("mutable disposition")
			}
			down, _ := os.ReadFile("../platform/database/migrations/0179_seller_source_dispositions.down.sql")
			if _, err := pool.Exec(t.Context(), string(down)); err == nil {
				t.Fatal("downgrade erased audit")
			}
		})
	}
}

func TestSourceDispositionAuthoritySnapshotAndReplay(t *testing.T) {
	pool, s, _, request, command, _ := sourceReversalExecutionFixture(t)
	maker, checker := dispositionActor(t, pool), dispositionActor(t, pool)
	in := dispositionInput(t, pool, request.ID)
	if _, err := s.DisposeSellerSourceReversal(t.Context(), request.SellerID, command.ID, in, "seller-denied", "test"); err == nil {
		t.Fatal("seller proposed")
	}
	proposal, err := s.DisposeSellerSourceReversal(t.Context(), maker, command.ID, in, "propose-replay", "test")
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.DisposeSellerSourceReversal(t.Context(), maker, command.ID, in, "propose-replay", "retry")
	if err != nil || !again.Replayed || again.ID != proposal.ID {
		t.Fatal(again, err)
	}
	changed := in
	changed.Reason += " Changed."
	if _, err := s.DisposeSellerSourceReversal(t.Context(), maker, command.ID, changed, "propose-replay", "test"); !errors.Is(err, ErrSellerSourceReversalConflict) {
		t.Fatal("key mismatch", err)
	}
	in.Phase = "approve"
	in.ProposalID = &proposal.ID
	if _, err := s.DisposeSellerSourceReversal(t.Context(), maker, command.ID, in, "self-approve", "test"); !errors.Is(err, ErrSellerSourceReversalConflict) {
		t.Fatal("self approval", err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET status='cancelled' WHERE id=$1`, command.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DisposeSellerSourceReversal(t.Context(), checker, command.ID, in, "stale-approve", "test"); !errors.Is(err, ErrSellerSourceReversalConflict) {
		t.Fatal("changed evidence approved", err)
	}
	in = dispositionInput(t, pool, request.ID)
	proposal, err = s.DisposeSellerSourceReversal(t.Context(), maker, command.ID, in, "fresh-propose", "test")
	if err != nil {
		t.Fatal(err)
	}
	in.Phase = "approve"
	in.ProposalID = &proposal.ID
	if _, err := pool.Exec(t.Context(), `UPDATE users SET role='member' WHERE id=$1`, maker); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DisposeSellerSourceReversal(t.Context(), checker, command.ID, in, "revoked-maker", "test"); !errors.Is(err, ErrSellerSourceReversalConflict) {
		t.Fatal("revoked maker", err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE users SET role='admin' WHERE id=$1`, maker); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.DisposeSellerSourceReversal(t.Context(), checker, command.ID, in, "approve-replay", "test")
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
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM seller_source_reversal_checks WHERE command_id=$1`, command.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("duplicate recovery jobs %d %v", n, err)
	}
	assertReversalFundsRetained(t, pool, s, request, 0)
}

func TestSourceDispositionPartialConflictAndAlerts(t *testing.T) {
	for _, kind := range []string{"partial", "incomplete", "wrong_merchant", "conflict"} {
		t.Run(kind, func(t *testing.T) {
			pool, s, r, request, command, job := sourceReversalExecutionFixture(t)
			r.createReversal = func(_ context.Context, in TransferReversalRequest) (TransferReversalResult, error) {
				out := reversalObservation(in)
				switch kind {
				case "partial":
					out.Transfer.AmountReversed = in.AmountCents / 2
					out.Observations = nil
					out.Outcome = "not_found"
				case "incomplete":
					out.Outcome = "incomplete"
					out.Observations = nil
				case "conflict":
					out.Transfer.Currency = "EUR"
				case "wrong_merchant":
					return out, ErrCheckoutReconciliation
				}
				return out, nil
			}
			if err := s.HandleSellerSourceReversalJob(t.Context(), job); err == nil {
				t.Fatal("unsafe result accepted")
			}
			_, check := approvedDisposition(t, pool, s, request.ID, command.ID)
			if kind == "wrong_merchant" {
				r.beforeIdentity = func(context.Context) error { return ErrCheckoutReconciliation }
			}
			err := s.HandleSellerSourceReversalCheckJob(t.Context(), check)
			if kind != "incomplete" && err == nil {
				t.Fatal("conflicting history erased")
			}
			want := 0
			if kind == "incomplete" {
				want = 1
			}
			assertReversalFundsRetained(t, pool, s, request, want)
			metrics, err := ProductOperationalMetrics(t.Context(), pool)
			if err != nil {
				t.Fatal(err)
			}
			seen := false
			for _, m := range metrics.Problems {
				if m.Kind == "seller_reversal_review" && m.Mode == "test" && m.Count > 0 {
					seen = true
				}
			}
			if !seen {
				t.Fatal("missing review alert")
			}
			if r.creates.Load() != 1 {
				t.Fatal("recovery resent")
			}
		})
	}
}

func TestSourceDispositionAuditRollbackAndMigration(t *testing.T) {
	pool, s, _, request, command, _ := sourceReversalExecutionFixture(t)
	applySourceDispositionMigration(t, pool, "down")
	applySourceDispositionMigration(t, pool, "up")
	if _, err := pool.Exec(t.Context(), `CREATE FUNCTION reject_source_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action LIKE 'seller_payout.source_disposition_%' THEN RAISE EXCEPTION 'injected'; END IF; RETURN NEW; END; $$; CREATE TRIGGER reject_source_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_source_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DisposeSellerSourceReversal(t.Context(), dispositionActor(t, pool), command.ID, dispositionInput(t, pool, request.ID), "rollback-proposal", "test"); err == nil {
		t.Fatal("audit failure ignored")
	}
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM seller_source_reversal_dispositions`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial decision %d %v", n, err)
	}
	assertReversalFundsRetained(t, pool, s, request, 0)
}

func TestSourceDispositionExpiredProposal(t *testing.T) {
	pool, s, _, request, command, _ := sourceReversalExecutionFixture(t)
	maker := dispositionActor(t, pool)
	in := dispositionInput(t, pool, request.ID)
	proposal, err := s.DisposeSellerSourceReversal(t.Context(), maker, command.ID, in, "expired-propose", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE seller_source_reversal_dispositions DISABLE TRIGGER USER; UPDATE seller_source_reversal_dispositions SET created_at=created_at-interval '25 hours'; ALTER TABLE seller_source_reversal_dispositions ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	in.Phase = "approve"
	in.ProposalID = &proposal.ID
	if _, err := s.DisposeSellerSourceReversal(t.Context(), dispositionActor(t, pool), command.ID, in, "expired-approve", "test"); !errors.Is(err, ErrSellerSourceReversalConflict) {
		t.Fatal(err)
	}
	assertReversalFundsRetained(t, pool, s, request, 0)
}

func TestSourceDispositionApprovalAuditAndAutomaticRecovery(t *testing.T) {
	pool, s, r, request, command, original := sourceReversalExecutionFixture(t)
	maker, checker := dispositionActor(t, pool), dispositionActor(t, pool)
	in := dispositionInput(t, pool, request.ID)
	p, err := s.DisposeSellerSourceReversal(t.Context(), maker, command.ID, in, "proposal-recover", "test")
	if err != nil {
		t.Fatal(err)
	}
	in.Phase = "approve"
	in.ProposalID = &p.ID
	if _, err := pool.Exec(t.Context(), `CREATE FUNCTION fail_disposition_approval() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='seller_payout.source_disposition_approve' THEN RAISE EXCEPTION 'injected'; END IF; RETURN NEW; END; $$; CREATE TRIGGER fail_disposition_approval BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_disposition_approval()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DisposeSellerSourceReversal(t.Context(), checker, command.ID, in, "approval-recover", "test"); err == nil {
		t.Fatal("approval ignored audit failure")
	}
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM seller_source_reversal_checks`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("approval leaked job %d %v", n, err)
	}
	if _, err := pool.Exec(t.Context(), `DROP TRIGGER fail_disposition_approval ON audit_events`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DisposeSellerSourceReversal(t.Context(), checker, command.ID, in, "approval-recover", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET status='cancelled' WHERE id IN (SELECT job_id FROM seller_source_reversal_checks)`); err != nil {
		t.Fatal(err)
	}
	// A stopped read gets another read, even while the original unsent job is queued.
	count, err := s.reconcileSellerSourceReversals(t.Context(), 10, time.Now().Add(time.Hour))
	if err != nil || count != 1 {
		t.Fatalf("reconcile %d %v", count, err)
	}
	again, err := s.reconcileSellerSourceReversals(t.Context(), 10, time.Now().Add(time.Hour))
	if err != nil || again != 0 {
		t.Fatalf("duplicate scheduled %d %v", again, err)
	}
	var job jobs.Job
	if err := pool.QueryRow(t.Context(), `SELECT j.id,j.kind,j.payload FROM seller_source_reversal_checks c JOIN jobs j ON j.id=c.job_id WHERE c.command_id=$1 AND j.status='queued'`, command.ID).Scan(&job.ID, &job.Kind, &job.Payload); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleSellerSourceReversalCheckJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if r.creates.Load() != 0 || r.queries.Load() != 1 {
		t.Fatal("recovery wrote funds")
	}
	if err := s.HandleSellerSourceReversalJob(t.Context(), original); err == nil {
		t.Fatal("original was revived")
	}
	actor, input := sourceClosureInput(t, pool, command.ID)
	// A proposer cannot consume the returned funds, even with valid proof.
	if _, err := s.CloseSellerSourceReversal(t.Context(), maker, command.ID, input, "maker-cannot-close", "test"); !errors.Is(err, ErrSellerSourceClosureConflict) {
		t.Fatal("proposer self closed", err)
	}
	if _, err := s.CloseSellerSourceReversal(t.Context(), actor, command.ID, input, "independent-close", "test"); err != nil {
		t.Fatal(err)
	}
	assertSourceClosure(t, pool, request, 1)
}

func TestSourceDispositionEvidenceChangeInvalidatesApproval(t *testing.T) {
	for _, change := range []string{"payment", "identity", "job", "debt", "allocation", "refund"} {
		t.Run(change, func(t *testing.T) {
			pool, s, _, request, command, _ := sourceReversalExecutionFixture(t)
			maker, checker := dispositionActor(t, pool), dispositionActor(t, pool)
			in := dispositionInput(t, pool, request.ID)
			p, err := s.DisposeSellerSourceReversal(t.Context(), maker, command.ID, in, "snapshot-propose", "test")
			if err != nil {
				t.Fatal(err)
			}
			// Isolated corrupt/historical evidence only, preserving production guards.
			mutations := map[string]string{
				"payment":    `UPDATE payment_intents SET version=version+1 WHERE id=(SELECT payment_id FROM seller_source_reversal_commands WHERE id=$1)`,
				"identity":   `ALTER TABLE product_checkout_requests DISABLE TRIGGER USER; UPDATE product_checkout_requests SET identity=jsonb_set(identity,'{merchantId}','"acct_different"') WHERE payment_id=(SELECT payment_id FROM seller_source_reversal_commands LIMIT 1); ALTER TABLE product_checkout_requests ENABLE TRIGGER USER`,
				"job":        `UPDATE jobs SET status='cancelled' WHERE id=(SELECT job_id FROM seller_source_reversal_commands WHERE id=$1)`,
				"debt":       `INSERT INTO seller_recovery_obligations(seller_id,settlement_id,amount_cents,remaining_cents,currency,status) SELECT seller_id,settlement_id,amount_cents,amount_cents,currency,'open' FROM seller_source_reversal_commands WHERE id=$1`,
				"allocation": `ALTER TABLE seller_payout_request_allocations DISABLE TRIGGER USER; UPDATE seller_payout_request_allocations SET amount_cents=amount_cents-1; ALTER TABLE seller_payout_request_allocations ENABLE TRIGGER USER`,
				"refund":     `UPDATE orders SET status='refund_requested',updated_at=clock_timestamp() WHERE id=(SELECT order_id FROM payment_intents WHERE id=(SELECT payment_id FROM seller_source_reversal_commands WHERE id=$1))`,
			}
			if change == "identity" || change == "allocation" {
				_, err = pool.Exec(t.Context(), mutations[change])
			} else {
				_, err = pool.Exec(t.Context(), mutations[change], command.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			in.Phase = "approve"
			in.ProposalID = &p.ID
			if _, err := s.DisposeSellerSourceReversal(t.Context(), checker, command.ID, in, "snapshot-approve", "test"); !errors.Is(err, ErrSellerSourceReversalConflict) {
				t.Fatal("changed evidence approved", change, err)
			}
			var n int
			if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM seller_ledger_entries WHERE payout_request_id=$1 AND entry_type='payout_release'`, request.ID).Scan(&n); err != nil || n != 0 {
				t.Fatal("released changed graph", n, err)
			}
		})
	}
}

func TestSourceReturnConsumesProviderDisputeDebt(t *testing.T) {
	pool, s, _, request, command, job := sourceReversalExecutionFixture(t)
	if err := s.HandleSellerSourceReversalJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	var payment uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT payment_id FROM seller_source_reversal_commands WHERE id=$1`, command.ID).Scan(&payment); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	s.config.WebhookSecret = testStripeWebhookSecret
	s.config.APIVersion = testStripeAPIVersion
	// The fixture service's verifier was configured by fulfilledRefundFixture.
	s.verifier.now = func() time.Time { return now }
	body := []byte(fmt.Sprintf(`{"id":"evt_source_disputeloss","object":"event","api_version":%q,"created":%d,"livemode":false,"type":"charge.dispute.closed","data":{"object":{"id":"dp_source_disputeloss","object":"dispute","payment_intent":"pi_workflow123","charge":"ch_settlement","amount":1900,"currency":"usd","status":"lost","reason":"fraudulent","network_reason_code":"visa_10_4","evidence_details":{"due_by":%d}}}}`, testStripeAPIVersion, now.Unix(), now.Add(time.Hour).Unix()))
	receipt := receivePaymentWorkflowEvent(t, s, body, now)
	if err := s.HandlePaymentEventJob(t.Context(), jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}); err != nil {
		t.Fatal(err)
	}
	actor, in := sourceClosureInput(t, pool, command.ID)
	out, err := s.CloseSellerSourceReversal(t.Context(), actor, command.ID, in, "dispute-debt-close", "test")
	if err != nil || out.Closure.Resolution != "refund_recovered" {
		t.Fatal(out, err)
	}
	balance, err := singleSellerFunds(t, s, t.Context(), request.SellerID)
	if err != nil || balance.ReservedCents != 0 || balance.WithdrawableCents != 0 || balance.RecoveryDueCents != 0 {
		t.Fatal("dispute recovery restored income", balance, err)
	}
}

func TestSourceDispositionRacesFirstSendAndRevocation(t *testing.T) {
	pool, s, r, request, command, job := sourceReversalExecutionFixture(t)
	maker := dispositionActor(t, pool)
	in := dispositionInput(t, pool, request.ID)
	start := make(chan struct{})
	done := make(chan error, 2)
	go func() { <-start; done <- s.HandleSellerSourceReversalJob(t.Context(), job) }()
	go func() {
		<-start
		_, err := s.DisposeSellerSourceReversal(t.Context(), maker, command.ID, in, "race-proposal", "test")
		done <- err
	}()
	close(start)
	for i := 0; i < 2; i++ {
		err := <-done
		if err != nil {
			var p *pgconn.PgError
			// A proposal winning before dispatch records a held observation and
			// asks the worker to retry reconciliation; it must never POST.
			if !errors.Is(err, ErrSellerSourceReversalConflict) && sourceReversalErrorCode(err) != "payment_settlement_pending" && !(errors.As(err, &p) && oneOf(p.Code, "23514", "40001", "40P01")) {
				t.Fatal(err)
			}
		}
	}
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM seller_source_reversal_dispositions WHERE command_id=$1`, command.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		in = dispositionInput(t, pool, request.ID)
		if _, err := s.DisposeSellerSourceReversal(t.Context(), maker, command.ID, in, "retry-proposal", "test"); err != nil {
			t.Fatal(err)
		}
	}
	calls := r.creates.Load()
	_ = s.HandleSellerSourceReversalJob(t.Context(), job)
	if r.creates.Load() != calls || calls > 1 {
		t.Fatal("proposal permitted another write")
	}
	// Revoke an approver while blocked on the financial graph, not before entry.
	checker := dispositionActor(t, pool)
	in = dispositionInput(t, pool, request.ID)
	p, err := s.DisposeSellerSourceReversal(t.Context(), maker, command.ID, in, "revoke-proposal", "test")
	if err != nil {
		t.Fatal(err)
	}
	in.Phase = "approve"
	in.ProposalID = &p.ID
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
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
	result := make(chan error, 1)
	go func() {
		_, err := s.DisposeSellerSourceReversal(ctx, checker, command.ID, in, "revoke-approval", "test")
		result <- err
	}()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid)) AND query LIKE '%lock_seller_payout_disposition%')`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, checker); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrFinanceForbidden) {
		t.Fatal("revoked after wait accepted", err)
	}
	var releases int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM seller_ledger_entries WHERE payout_request_id=$1 AND entry_type='payout_release'`, request.ID).Scan(&releases); err != nil || releases != 0 {
		t.Fatal("race released funds", releases, err)
	}
}
