package payments

import (
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func applySourceReversalClosureMigration(t *testing.T, pool *pgxpool.Pool, direction string) {
	t.Helper()
	body, err := os.ReadFile("../platform/database/migrations/0172_seller_source_reversal_closure." + direction + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), string(body)); err != nil {
		t.Fatal(err)
	}
}

func sourceClosureInput(t *testing.T, pool *pgxpool.Pool, commandID uuid.UUID) (uuid.UUID, SellerSourceClosureInput) {
	t.Helper()
	var actor uuid.UUID
	input := SellerSourceClosureInput{Confirmed: true, Reason: "Close the fully returned source after checking the original bank obligation."}
	if err := pool.QueryRow(t.Context(), `SELECT c.actor_id,x.read_id,r.updated_at
 FROM seller_source_reversal_commands c JOIN seller_source_reversal_results x ON x.command_id=c.id
 JOIN seller_payout_requests r ON r.id=c.payout_request_id WHERE c.id=$1`, commandID).Scan(&actor, &input.ReadID, &input.ExpectedUpdatedAt); err != nil {
		t.Fatal(err)
	}
	return actor, input
}

func assertSourceClosure(t *testing.T, pool *pgxpool.Pool, request SellerPayoutRequest, want int) {
	t.Helper()
	var closures, releases, allocations, events, audits int
	if err := pool.QueryRow(t.Context(), `SELECT
 (SELECT count(*) FROM seller_source_reversal_closures WHERE payout_request_id=$1),
 (SELECT count(*) FROM seller_ledger_entries WHERE payout_request_id=$1 AND entry_type='payout_release'),
 (SELECT count(*) FROM seller_payout_request_allocations WHERE payout_request_id=$1 AND released_at IS NOT NULL),
 (SELECT count(*) FROM seller_payout_request_events WHERE payout_request_id=$1 AND event_type='source_reversal.closed'),
 (SELECT count(*) FROM audit_events WHERE resource_id=$1 AND action='seller_payout.source_reversal_closed')`, request.ID).Scan(&closures, &releases, &allocations, &events, &audits); err != nil {
		t.Fatal(err)
	}
	if closures != want || releases != want || allocations != want || events != want || audits != want {
		t.Fatalf("closure/release/allocation/event/audit=%d/%d/%d/%d/%d want %d", closures, releases, allocations, events, audits, want)
	}
}

func TestSellerSourceReversalClosureReleaseAndReplay(t *testing.T) {
	pool, service, runtime, request, command, job := sourceReversalExecutionFixture(t)
	ctx := t.Context()
	if err := service.HandleSellerSourceReversalJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	actor, input := sourceClosureInput(t, pool, command.ID)
	service.config.Enabled = false // Closing verified evidence must survive the new-write switch.
	out, err := service.CloseSellerSourceReversal(ctx, actor, command.ID, input, "source-close-release", "trace")
	if err != nil || out.Replayed || out.Closure.Resolution != "released" {
		t.Fatalf("close %+v: %v", out, err)
	}
	assertSourceClosure(t, pool, request, 1)
	assertSellerFunding(t, pool, request, "succeeded", "cancelled")
	balance, err := singleSellerFunds(t, service, ctx, request.SellerID)
	if err != nil || balance.ReservedCents != 0 || balance.WithdrawableCents != request.AmountCents {
		t.Fatal("returned source not released exactly once", balance, err)
	}
	again, err := service.CloseSellerSourceReversal(ctx, actor, command.ID, input, "source-close-release", "retry")
	if err != nil || !again.Replayed || again.Closure.ID != out.Closure.ID {
		t.Fatal("replay changed closure", again, err)
	}
	changed := input
	changed.Reason += " Changed."
	if _, err := service.CloseSellerSourceReversal(ctx, actor, command.ID, changed, "source-close-release", "conflict"); !errors.Is(err, ErrSellerSourceClosureConflict) {
		t.Fatal("changed replay accepted", err)
	}
	if _, err := service.CloseSellerSourceReversal(ctx, actor, command.ID, input, "source-close-new-key", "conflict"); !errors.Is(err, ErrSellerSourceClosureConflict) {
		t.Fatal("second closure accepted", err)
	}
	if err := service.HandleSellerSourceReversalJob(ctx, job); err != nil {
		t.Fatal("old worker replay", err)
	}
	if runtime.creates.Load() != 1 || runtime.queries.Load() != 0 {
		t.Fatal("closure contacted provider", runtime.creates.Load(), runtime.queries.Load())
	}
	assertSellerFunding(t, pool, request, "succeeded", "cancelled")
	assertSourceClosure(t, pool, request, 1)
}

func TestSellerSourceReversalClosureMigrationRoundTrip(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	applySourceReversalClosureMigration(t, pool, "down")
	applySourceReversalClosureMigration(t, pool, "up")
}

func TestSellerSourceReversalClosureRollback(t *testing.T) {
	for _, target := range []string{"audit", "event", "ledger"} {
		t.Run(target, func(t *testing.T) {
			pool, service, _, request, command, job := sourceReversalExecutionFixture(t)
			ctx := t.Context()
			if err := service.HandleSellerSourceReversalJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			actor, input := sourceClosureInput(t, pool, command.ID)
			table, condition := "audit_events", "NEW.action='seller_payout.source_reversal_closed'"
			if target == "event" {
				table, condition = "seller_payout_request_events", "NEW.event_type='source_reversal.closed'"
			}
			if target == "ledger" {
				table, condition = "seller_ledger_entries", "NEW.entry_type='payout_release'"
			}
			if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_source_closure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF `+condition+` THEN RAISE EXCEPTION 'injected closure failure'; END IF; RETURN NEW; END; $$; CREATE TRIGGER fail_source_closure BEFORE INSERT ON `+table+` FOR EACH ROW EXECUTE FUNCTION fail_source_closure()`); err != nil {
				t.Fatal(err)
			}
			if _, err := service.CloseSellerSourceReversal(ctx, actor, command.ID, input, "rollback-source-closure", "trace"); err == nil {
				t.Fatal("failure committed")
			}
			assertSourceClosure(t, pool, request, 0)
			assertReversalFundsRetained(t, pool, service, request, 1)
			if _, err := pool.Exec(ctx, `DROP TRIGGER fail_source_closure ON `+table+`; DROP FUNCTION fail_source_closure()`); err != nil {
				t.Fatal(err)
			}
			if _, err := service.CloseSellerSourceReversal(ctx, actor, command.ID, input, "rollback-source-closure", "retry"); err != nil {
				t.Fatal("rollback lost retry", err)
			}
			assertSourceClosure(t, pool, request, 1)
		})
	}
}
