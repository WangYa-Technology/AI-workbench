package payments

import (
	"context"
	"testing"
	"time"
)

func TestSellerSourceReversalExecutionExpiredUnsupportedOrChangedIdentity(t *testing.T) {
	for _, scenario := range []string{"expired", "unsupported", "merchant", "cancelled_transport"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, request, command, job := sourceReversalExecutionFixture(t)
			ctx := t.Context()
			switch scenario {
			case "expired":
				// Age immutable test evidence only inside this disposable fixture.
				if _, err := pool.Exec(ctx, `ALTER TABLE seller_source_reversal_commands DISABLE TRIGGER seller_source_reversal_command_guard`); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `UPDATE seller_source_reversal_commands SET created_at=clock_timestamp()-interval '24 hours' WHERE id=$1`, command.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `ALTER TABLE seller_source_reversal_commands ENABLE TRIGGER seller_source_reversal_command_guard`); err != nil {
					t.Fatal(err)
				}
			case "unsupported":
				service.runtimes = NewRuntimeCatalog(runtime.sellerBankExecutionRuntime)
			case "merchant":
				runtime.merchant = "acct_substituted123"
			case "cancelled_transport":
				runtime.createReversal = func(context.Context, TransferReversalRequest) (TransferReversalResult, error) {
					return TransferReversalResult{}, context.Canceled
				}
			}
			if err := service.HandleSellerSourceReversalJob(ctx, job); err == nil {
				t.Fatal("invalid first send completed")
			}
			assertReversalFundsRetained(t, pool, service, request, 0)
			wantPosts, wantMarkers := int32(0), 0
			if scenario == "merchant" {
				wantMarkers = 1
			}
			if scenario == "cancelled_transport" {
				wantPosts, wantMarkers = 1, 1
			}
			var markers int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM seller_source_reversal_dispatches WHERE command_id=$1`, command.ID).Scan(&markers); err != nil || markers != wantMarkers || runtime.creates.Load() != wantPosts {
				t.Fatal("invalid execution evidence", markers, runtime.creates.Load(), err)
			}
			if scenario == "merchant" {
				runtime.merchant = ""
				if err := service.HandleSellerSourceReversalJob(ctx, job); err == nil {
					t.Fatal("later identity reset erased conflict")
				}
				assertReversalFundsRetained(t, pool, service, request, 0)
			}
			if scenario == "cancelled_transport" {
				if err := service.HandleSellerSourceReversalJob(ctx, job); err != nil {
					t.Fatal("transport cancellation blocked authenticated recovery", err)
				}
				assertReversalFundsRetained(t, pool, service, request, 1)
				if runtime.creates.Load() != 1 || runtime.queries.Load() != 1 {
					t.Fatal("transport cancellation resent money")
				}
			}
		})
	}
}

func TestSellerSourceReversalExecutionLateObservationNeedsFreshQuery(t *testing.T) {
	pool, service, runtime, request, command, job := sourceReversalExecutionFixture(t)
	runtime.createReversal = func(ctx context.Context, input TransferReversalRequest) (TransferReversalResult, error) {
		// Simulate a provider returning a successful response only after the
		// registered deadline. The caller must not accept this as timely proof.
		<-ctx.Done()
		return reversalObservation(input), nil
	}
	if err := service.HandleSellerSourceReversalJob(t.Context(), job); err == nil {
		t.Fatal("late result accepted")
	}
	assertReversalFundsRetained(t, pool, service, request, 0)
	var outcome, code string
	var review bool
	if err := pool.QueryRow(t.Context(), `SELECT outcome,error_code,requires_review FROM seller_source_reversal_reads WHERE command_id=$1 AND kind='create'`, command.ID).Scan(&outcome, &code, &review); err != nil || outcome != "error" || code != "payment_provider_timeout" || review {
		t.Fatal("late observation misclassified", outcome, code, review, err)
	}
	if err := service.HandleSellerSourceReversalJob(t.Context(), job); err != nil {
		t.Fatal("fresh authenticated query failed", err)
	}
	assertReversalFundsRetained(t, pool, service, request, 1)
	if runtime.creates.Load() != 1 || runtime.queries.Load() != 1 {
		t.Fatal("late response resent reversal")
	}
}

func TestSellerSourceReversalRecoveryAuditAtomicity(t *testing.T) {
	pool, service, runtime, _, command, job := sourceReversalExecutionFixture(t)
	ctx := t.Context()
	runtime.createReversal = func(context.Context, TransferReversalRequest) (TransferReversalResult, error) {
		return TransferReversalResult{}, context.DeadlineExceeded
	}
	if err := service.HandleSellerSourceReversalJob(ctx, job); err == nil {
		t.Fatal("lost response completed")
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_reversal_check_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.action='marketplace.seller_reversal_check_scheduled' THEN RAISE EXCEPTION 'injected recovery audit failure'; END IF; RETURN NEW; END; $$;
 CREATE TRIGGER fail_reversal_check_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_reversal_check_audit()`); err != nil {
		t.Fatal(err)
	}
	var due time.Time
	if err := pool.QueryRow(ctx, `SELECT due_at FROM seller_source_reversal_check_candidates WHERE command_id=$1`, command.ID).Scan(&due); err != nil {
		t.Fatal(err)
	}
	if changed, err := service.scheduleSellerSourceReversalCheck(ctx, command.ID, due); err == nil || changed {
		t.Fatal("audit failure committed recovery", changed, err)
	}
	var checks, queued int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM seller_source_reversal_checks),
 (SELECT count(*) FROM jobs WHERE kind='payment.check_seller_source_reversal')`).Scan(&checks, &queued); err != nil || checks != 0 || queued != 0 {
		t.Fatal("partial recovery persisted", checks, queued, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER fail_reversal_check_audit ON audit_events; DROP FUNCTION fail_reversal_check_audit()`); err != nil {
		t.Fatal(err)
	}
	if changed, err := service.scheduleSellerSourceReversalCheck(ctx, command.ID, due); err != nil || !changed {
		t.Fatal("atomic retry failed", changed, err)
	}
}
