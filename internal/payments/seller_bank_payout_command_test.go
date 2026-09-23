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
	"github.com/jackc/pgx/v5/pgxpool"
)

func bankCommandInput(t *testing.T, pool *pgxpool.Pool, request SellerPayoutRequest) (uuid.UUID, SellerBankPayoutInput) {
	t.Helper()
	var actor uuid.UUID
	input := SellerBankPayoutInput{AmountCents: request.AmountCents, BankDestinationID: "ba_original", Confirmed: true, Reason: "Authorize the frozen bank payout after source funding."}
	if err := pool.QueryRow(t.Context(), `SELECT f.actor_id,f.transfer_id,f.review_id,v.revision
 FROM seller_payout_funding_admissions f JOIN seller_payout_reviews v ON v.id=f.review_id
 WHERE f.payout_request_id=$1`, request.ID).Scan(&actor, &input.SourceTransferID, &input.ReviewID, &input.ExpectedRevision); err != nil {
		t.Fatal(err)
	}
	return actor, input
}

func assertBankCommandCount(t *testing.T, pool *pgxpool.Pool, want int) {
	t.Helper()
	var commands, events, audits int
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM seller_bank_payout_commands),
 (SELECT count(*) FROM seller_payout_request_events WHERE event_type='bank.reserved'),
 (SELECT count(*) FROM audit_events WHERE action='seller_payout.bank_reserved')`).Scan(&commands, &events, &audits); err != nil || commands != want || events != want || audits != want {
		t.Fatalf("bank evidence commands/events/audits=%d/%d/%d, want %d: %v", commands, events, audits, want, err)
	}
}

func TestSellerBankCommandReservationReplayAndEvidence(t *testing.T) {
	pool, service, runtime, _, request, job := sellerFundingFixture(t)
	ctx := t.Context()
	actor, input := bankCommandInput(t, pool, request)
	if _, err := service.ReserveSellerBankPayout(ctx, actor, request.ID, input, "before-funding-key", "test"); !errors.Is(err, ErrSellerBankPayoutConflict) {
		t.Fatal("unfunded source accepted", err)
	}
	assertBankCommandCount(t, pool, 0)
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	var jobsBefore int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs`).Scan(&jobsBefore); err != nil {
		t.Fatal(err)
	}
	first, err := service.ReserveSellerBankPayout(ctx, actor, request.ID, input, "bank-command-key", "original-bank-trace")
	if err != nil || first.Replayed || first.Command.ID == uuid.Nil || first.Command.PayoutRequestID != request.ID || first.Command.SourceTransferID != input.SourceTransferID || first.Command.ReviewID != input.ReviewID || first.Command.ReviewRevision != input.ExpectedRevision || first.Command.AmountCents != request.AmountCents || first.Command.BankDestinationID != input.BankDestinationID || first.Command.Currency != "USD" || first.Command.CreatedAt.IsZero() {
		t.Fatalf("bank reservation %+v: %v", first, err)
	}
	service.config.Enabled = false
	again, err := service.ReserveSellerBankPayout(ctx, actor, request.ID, input, "bank-command-key", "retry-bank-trace")
	if err != nil || !again.Replayed || again.Command != first.Command {
		t.Fatalf("bank replay %+v: %v", again, err)
	}
	for _, field := range []string{"source", "review", "revision", "amount", "bank", "reason", "request"} {
		changed, id := input, request.ID
		switch field {
		case "source":
			changed.SourceTransferID = uuid.New()
		case "review":
			changed.ReviewID = uuid.New()
		case "revision":
			changed.ExpectedRevision++
		case "amount":
			changed.AmountCents++
		case "bank":
			changed.BankDestinationID = "ba_changed"
		case "reason":
			changed.Reason = "A different explanation for the bank payout."
		case "request":
			id = uuid.New()
		}
		if _, err := service.ReserveSellerBankPayout(ctx, actor, id, changed, "bank-command-key", "changed"); !errors.Is(err, ErrSellerBankPayoutConflict) {
			t.Fatalf("changed %s replay: %v", field, err)
		}
	}
	if _, err := service.ReserveSellerBankPayout(ctx, actor, request.ID, input, "new-disabled-key", "test"); !errors.Is(err, ErrDisabled) {
		t.Fatal("disabled new command", err)
	}
	var jobsAfter int
	var trace, dispatch string
	var matched bool
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM jobs),c.request_id,c.dispatch_key,
 c.provider_identity=t.provider_identity AND c.destination_id=t.destination_id AND c.source_provider_transfer_id=t.provider_transfer_id
 FROM seller_bank_payout_commands c JOIN seller_payout_transfers t ON t.id=c.source_transfer_id WHERE c.id=$1`, first.Command.ID).Scan(&jobsAfter, &trace, &dispatch, &matched); err != nil || jobsAfter != jobsBefore || trace != "original-bank-trace" || dispatch != "seller-bank-payout-"+request.ID.String() || !matched {
		t.Fatalf("frozen evidence jobs=%d/%d trace=%s dispatch=%s match=%v: %v", jobsAfter, jobsBefore, trace, dispatch, matched, err)
	}
	assertBankCommandCount(t, pool, 1)
	assertSellerFunding(t, pool, request, "succeeded", "processing")
	if runtime.transfers.Load() != 1 || runtime.reads.Load() != 0 {
		t.Fatal("bank reservation contacted source provider again")
	}
	balance, err := singleSellerFunds(t, service, ctx, request.SellerID)
	if err != nil || balance.ReservedCents != request.AmountCents || balance.WithdrawableCents != 0 {
		t.Fatalf("bank reservation released money %+v: %v", balance, err)
	}
	for _, query := range []string{`DELETE FROM seller_bank_payout_commands`, `UPDATE seller_bank_payout_commands SET reason='Replace the original command'`, `UPDATE seller_payout_requests SET status='succeeded'`} {
		_, err := pool.Exec(ctx, query)
		requirePayoutConstraint(t, err)
	}
	for _, protocol := range []string{"", "old-v0"} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.seller_bank_payout_protocol',$1,true)`, protocol); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, `INSERT INTO seller_bank_payout_commands SELECT (jsonb_populate_record(NULL::seller_bank_payout_commands,to_jsonb(c)||jsonb_build_object('id',gen_random_uuid()))).* FROM seller_bank_payout_commands c`)
		requirePayoutConstraint(t, err)
		_ = tx.Rollback(ctx)
	}
	body, err := os.ReadFile("../platform/database/migrations/0165_seller_bank_payout_commands.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, string(body))
	requirePayoutConstraint(t, err)
	_ = tx.Rollback(ctx)
	var buyer uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT p.payer_id FROM seller_bank_payout_commands c JOIN payment_intents p ON p.id=c.payment_id WHERE c.id=$1`, first.Command.ID).Scan(&buyer); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []uuid.UUID{request.SellerID, actor, buyer} {
		exporter, exportID, exportJob := sellerExportFixture(t, pool, owner)
		data, exported := sellerExportData(t, exporter, owner, exportID, exportJob)
		rows := data["sellerBankPayoutCommands"]
		if owner == request.SellerID {
			if len(rows) != 1 || len(rows[0]) != 8 || rows[0]["id"] != first.Command.ID.String() || rows[0]["payoutRequestId"] != request.ID.String() || rows[0]["sourceTransferId"] != input.SourceTransferID.String() || rows[0]["reviewRevision"] != float64(input.ExpectedRevision) || rows[0]["amountCents"] != float64(request.AmountCents) || rows[0]["currency"] != "USD" || rows[0]["bankDestinationId"] != input.BankDestinationID || rows[0]["createdAt"] == nil {
				t.Fatal("bank command export differs from the owner-safe snapshot", rows)
			}
			for _, private := range []string{input.Reason, actor.String(), "bank-command-key", "original-bank-trace", "seller-bank-payout-" + request.ID.String()} {
				if strings.Contains(string(exported), private) {
					t.Fatal("seller export exposed internal bank command evidence")
				}
			}
		} else if len(rows) != 0 {
			t.Fatal("non-owner exported bank command", rows)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReserveSellerBankPayout(ctx, actor, request.ID, input, "bank-command-key", "revoked"); !errors.Is(err, ErrFinanceForbidden) {
		t.Fatal("revoked replay allowed", err)
	}
}

func TestSellerBankCommandAtomicRollbackAndConcurrentReservation(t *testing.T) {
	pool, service, _, _, request, job := sellerFundingFixture(t)
	ctx := t.Context()
	actor, input := bankCommandInput(t, pool, request)
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_bank_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.action='seller_payout.bank_reserved' THEN RAISE EXCEPTION 'injected audit failure'; END IF; RETURN NEW; END; $$;
 CREATE TRIGGER fail_bank_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_bank_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReserveSellerBankPayout(ctx, actor, request.ID, input, "atomic-bank-command", "fail"); err == nil {
		t.Fatal("audit failure committed command")
	}
	assertBankCommandCount(t, pool, 0)
	if _, err := pool.Exec(ctx, `DROP TRIGGER fail_bank_audit ON audit_events; DROP FUNCTION fail_bank_audit()`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan SellerBankPayoutReservation, 6)
	errs := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := service.ReserveSellerBankPayout(ctx, actor, request.ID, input, "atomic-bank-command", "concurrent")
			results <- out
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
	var id uuid.UUID
	created := 0
	for out := range results {
		if id == uuid.Nil {
			id = out.Command.ID
		}
		if out.Command.ID != id {
			t.Fatal("concurrent retries changed bank command")
		}
		if !out.Replayed {
			created++
		}
	}
	if created != 1 {
		t.Fatal("creation count", created)
	}
	if _, err := service.ReserveSellerBankPayout(ctx, actor, request.ID, input, "another-bank-command", "duplicate"); !errors.Is(err, ErrSellerBankPayoutConflict) {
		t.Fatal("different key created second command", err)
	}
	assertBankCommandCount(t, pool, 1)
}

func TestSellerBankCommandCurrentEligibility(t *testing.T) {
	for _, scenario := range []string{"self", "revoked", "suspended", "source", "approval", "revision", "bank", "amount", "destination", "refund", "debt", "mode", "unconfirmed"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, _, _, request, job := sellerFundingFixture(t)
			ctx := t.Context()
			actor, input := bankCommandInput(t, pool, request)
			if err := service.HandleSellerPayoutFundingJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			var err error
			switch scenario {
			case "self":
				actor = request.SellerID
				_, err = pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, actor)
			case "revoked":
				_, err = pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor)
			case "suspended":
				_, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, actor)
			case "source":
				input.SourceTransferID = uuid.New()
			case "approval":
				input.ReviewID = uuid.New()
			case "revision":
				input.ExpectedRevision++
			case "bank":
				input.BankDestinationID = "ba_changed"
			case "amount":
				input.AmountCents++
			case "destination":
				_, err = pool.Exec(ctx, `UPDATE payment_destinations SET destination_id='acct_replaced' WHERE user_id=$1`, request.SellerID)
			case "refund":
				_, err = pool.Exec(ctx, `UPDATE product_settlements SET status='refund_hold' WHERE id=(SELECT settlement_id FROM seller_payout_transfers WHERE id=$1)`, input.SourceTransferID)
			case "debt":
				_, err = pool.Exec(ctx, `INSERT INTO seller_recovery_obligations(seller_id,settlement_id,amount_cents,remaining_cents,currency,status)
 SELECT r.seller_id,t.settlement_id,1,1,'USD','open' FROM seller_payout_transfers t
 JOIN seller_payout_requests r ON r.id=t.payout_request_id WHERE t.id=$1`, input.SourceTransferID)
			case "mode":
				_, err = pool.Exec(ctx, `UPDATE product_settlement_settings SET payout_mode='automatic'`)
			case "unconfirmed":
				input.Confirmed = false
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.ReserveSellerBankPayout(ctx, actor, request.ID, input, "ineligible-bank-key", "test"); err == nil {
				t.Fatal("ineligible bank reservation accepted")
			}
			assertBankCommandCount(t, pool, 0)
		})
	}
}

func TestSellerBankCommandEligibilityAfterLockWait(t *testing.T) {
	for _, scenario := range []string{"revocation", "refund"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, _, _, request, job := sellerFundingFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			actor, input := bankCommandInput(t, pool, request)
			if err := service.HandleSellerPayoutFundingJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			blocker, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(ctx)
			var settlement, payment uuid.UUID
			if err = blocker.QueryRow(ctx, `SELECT s.id,p.id FROM product_settlements s JOIN payment_intents p ON p.id=s.payment_id
 JOIN seller_payout_transfers t ON t.settlement_id=s.id WHERE t.id=$1 FOR UPDATE OF p`, input.SourceTransferID).Scan(&settlement, &payment); err != nil {
				t.Fatal(err)
			}
			var pid int
			if err = blocker.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := service.ReserveSellerBankPayout(ctx, actor, request.ID, input, "waiting-bank-command", "test")
				done <- err
			}()
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid)) AND query LIKE '%product_settlements%')`, pid).Scan(&waiting); err != nil {
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
			if scenario == "revocation" {
				_, err = pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor)
			} else {
				err = lockProductSettlementPaymentTx(ctx, blocker, settlement)
				if err == nil {
					err = markProductSettlementRefundTx(ctx, blocker, payment)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = blocker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-done; err == nil || (scenario == "revocation" && !errors.Is(err, ErrFinanceForbidden)) {
				t.Fatal("committed eligibility change ignored", err)
			}
			assertBankCommandCount(t, pool, 0)
		})
	}
}

func TestSellerBankCommandRequiresResolvedFunding(t *testing.T) {
	pool, service, runtime, _, request, job := sellerFundingFixture(t)
	ctx := t.Context()
	actor, input := bankCommandInput(t, pool, request)
	runtime.lost = true
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err == nil {
		t.Fatal("unknown funding marked complete")
	}
	if _, err := service.ReserveSellerBankPayout(ctx, actor, request.ID, input, "unknown-funding-bank", "test"); !errors.Is(err, ErrSellerBankPayoutConflict) {
		t.Fatal("unknown source authorized bank payout", err)
	}
	assertBankCommandCount(t, pool, 0)
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	assertSellerFunding(t, pool, request, "succeeded", "reconciliation_required")
	if _, err := service.ReserveSellerBankPayout(ctx, actor, request.ID, input, "unknown-funding-bank", "test"); err != nil {
		t.Fatal("resolved source cannot reserve bank command", err)
	}
	assertBankCommandCount(t, pool, 1)
	if runtime.transfers.Load() != 1 || runtime.reads.Load() != 1 {
		t.Fatal("source recovery created another transfer")
	}
}

func TestSellerBankCommandCompetingKeysCannotReserveTwice(t *testing.T) {
	pool, service, _, _, request, job := sellerFundingFixture(t)
	ctx := t.Context()
	actor, input := bankCommandInput(t, pool, request)
	if err := service.HandleSellerPayoutFundingJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, key := range []string{"first-concurrent-bank", "second-concurrent-bank"} {
		go func() {
			<-start
			_, err := service.ReserveSellerBankPayout(ctx, actor, request.ID, input, key, "test")
			errs <- err
		}()
	}
	close(start)
	success, conflicts := 0, 0
	for range 2 {
		err := <-errs
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrSellerBankPayoutConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("competing reservations success=%d conflict=%d", success, conflicts)
	}
	assertBankCommandCount(t, pool, 1)
}
