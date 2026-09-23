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

func automaticRefundFixture(t *testing.T, lost bool) (*pgxpool.Pool, *Service, *refundReadRuntime, Checkout, uuid.UUID) {
	t.Helper()
	pool, cleanup := paymentTestPool(t)
	t.Cleanup(cleanup)
	runtime := &refundReadRuntime{}
	service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, runtime)
	ctx := context.Background()
	if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "automatic-refund-test", "test", "The delivered resource does not match its description."); err != nil {
		t.Fatal(err)
	}
	if lost {
		runtime.failNext = true
		if _, err := pool.Exec(ctx, `UPDATE jobs SET max_attempts=1 WHERE kind=$1`, ProductRefundJobKind); err != nil {
			t.Fatal(err)
		}
	}
	repo := jobs.NewRepository(pool)
	job, err := repo.Claim(ctx, "auto-dispatch-fixture", time.Minute)
	if err != nil || job.Kind != ProductRefundJobKind {
		t.Fatalf("claim: %+v %v", job, err)
	}
	err = service.HandleProductRefundJob(ctx, job)
	if lost {
		if err == nil {
			t.Fatal("expected response loss")
		}
		err = repo.Fail(ctx, job, "auto-dispatch-fixture", err)
	} else {
		if err != nil {
			t.Fatal(err)
		}
		err = repo.Complete(ctx, job, "auto-dispatch-fixture")
	}
	if err != nil {
		t.Fatal(err)
	}
	operation := runtime.operations[0]
	var payment string
	if err := pool.QueryRow(ctx, `SELECT provider_payment_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&payment); err != nil {
		t.Fatal(err)
	}
	runtime.observations = []RefundObservation{{ProviderID: "re_" + strings.ReplaceAll(operation.String(), "-", ""), ProviderPaymentID: payment, AmountCents: 1900, Currency: "USD", Status: "succeeded", OperationID: &operation}}
	return pool, service, runtime, checkout, buyer
}

func runAutomaticRefundCheck(t *testing.T, service *Service) jobs.Job {
	t.Helper()
	ctx := context.Background()
	repo := jobs.NewRepository(service.pool)
	job, err := repo.Claim(ctx, "automatic-check", time.Minute)
	if err != nil || job.Kind != ProductRefundCheckJobKind {
		t.Fatalf("claim query: %+v %v", job, err)
	}
	if err := service.HandleProductRefundCheckJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := repo.Complete(ctx, job, "automatic-check"); err != nil {
		t.Fatal(err)
	}
	return job
}

func TestProductRefundAutomaticLostCallbackAndResponse(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "callback", true: "response"}[lost], func(t *testing.T) {
			pool, service, runtime, checkout, buyer := automaticRefundFixture(t, lost)
			ctx := context.Background()
			if n, err := service.ReconcileProductRefunds(ctx, 100); err != nil || n != 0 {
				t.Fatalf("premature: %d %v", n, err)
			}
			if n, err := service.reconcileProductRefunds(ctx, 100, time.Now().Add(16*time.Minute)); err != nil || n != 1 {
				t.Fatalf("dispatch: %d %v", n, err)
			}
			var actor *uuid.UUID
			var origin string
			if err := pool.QueryRow(ctx, `SELECT requested_by,origin FROM product_refund_checks WHERE payment_id=$1`, checkout.PaymentID).Scan(&actor, &origin); err != nil || actor != nil || origin != "automatic" {
				t.Fatalf("origin: %v %s %v", actor, origin, err)
			}
			// Restart before the queued read. No second financial operation.
			restarted := newPaymentTestService(t, pool, service.config, NewRuntimeCatalog(runtime))
			job := runAutomaticRefundCheck(t, restarted)
			if err := restarted.HandleProductRefundCheckJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if runtime.reads != 1 || len(runtime.operations) != 1 {
				t.Fatalf("replayed read/refund: %d/%d", runtime.reads, len(runtime.operations))
			}
			assertProductRefundState(t, pool, checkout, "refunded", "refunded", "refunded", 0, 0)
			if n, err := restarted.reconcileProductRefunds(ctx, 100, time.Now().Add(48*time.Hour)); err != nil || n != 0 {
				t.Fatalf("settled requeued: %d %v", n, err)
			}
			history, err := restarted.RefundHistory(ctx, checkout.PaymentID, "", 20)
			if err != nil || history.LatestCheck == nil || history.LatestCheck.Origin != "automatic" || history.LatestCheck.UnresolvedCount != 0 {
				t.Fatalf("history: %+v %v", history, err)
			}
			pkg, body := runProductExport(t, pool, buyer)
			if rows := pkg.Data.Marketplace.Data["refundChecks"]; len(rows) != 1 || rows[0]["origin"] != "automatic" {
				t.Fatalf("missing export provenance: %+v", rows)
			}
			if strings.Contains(string(body), "requested_by") {
				t.Fatal("private operator attribution exported")
			}
			var events int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='payment.refund_check_scheduled' AND resource_id=$1 AND actor_id IS NULL`, checkout.PaymentID).Scan(&events); err != nil || events != 1 {
				t.Fatalf("audit: %d %v", events, err)
			}
		})
	}
}

func TestProductRefundAutomaticConcurrentScansAndRollback(t *testing.T) {
	pool, service, runtime, checkout, _ := automaticRefundFixture(t, false)
	ctx := context.Background()
	due := time.Now().Add(16 * time.Minute)
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_auto_refund_audit() RETURNS trigger AS $$ BEGIN IF NEW.action='payment.refund_check_scheduled' THEN RAISE EXCEPTION 'injected'; END IF; RETURN NEW; END $$ LANGUAGE plpgsql;
 CREATE TRIGGER reject_auto_refund_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_auto_refund_audit()`); err != nil {
		t.Fatal(err)
	}
	if n, err := service.reconcileProductRefunds(ctx, 100, due); err == nil || n != 0 {
		t.Fatalf("audit rollback: %d %v", n, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind=$1`, ProductRefundCheckJobKind).Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphan job: %d %v", count, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_auto_refund_audit ON audit_events`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other := newPaymentTestService(t, pool, service.config, NewRuntimeCatalog(runtime))
			if _, err := other.reconcileProductRefunds(ctx, 100, due); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_refund_checks WHERE payment_id=$1`, checkout.PaymentID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate: %d %v", count, err)
	}
	var origin string
	if err := pool.QueryRow(ctx, `SELECT origin FROM product_refund_checks WHERE payment_id=$1`, checkout.PaymentID).Scan(&origin); err != nil || origin != "automatic" {
		t.Fatal(origin, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE product_refund_checks SET origin='operator',requested_by=(SELECT payer_id FROM payment_intents WHERE id=$1) WHERE payment_id=$1`, checkout.PaymentID); err == nil {
		t.Fatal("origin rewritten")
	}
	if runtime.reads != 0 || len(runtime.operations) != 1 {
		t.Fatal("scanner performed remote operation")
	}
}

func TestProductRefundAutomaticPendingBackoffAndManualCheck(t *testing.T) {
	pool, service, runtime, checkout, _ := automaticRefundFixture(t, false)
	ctx := context.Background()
	runtime.observations[0].Status = "pending"
	for round := 0; round < 8; round++ {
		var due time.Time
		if err := pool.QueryRow(ctx, `SELECT due_at FROM product_refund_reconciliation_candidates WHERE payment_id=$1`, checkout.PaymentID).Scan(&due); err != nil {
			t.Fatal(err)
		}
		if n, err := service.reconcileProductRefunds(ctx, 100, due.Add(-time.Microsecond)); err != nil || n != 0 {
			t.Fatalf("early round %d: %d %v", round, n, err)
		}
		if n, err := service.reconcileProductRefunds(ctx, 100, due); err != nil || n != 1 {
			t.Fatalf("due round %d: %d %v", round, n, err)
		}
		runAutomaticRefundCheck(t, service)
		var delay float64
		if err := pool.QueryRow(ctx, `SELECT extract(epoch FROM (v.due_at-c.completed_at)) FROM product_refund_reconciliation_candidates v JOIN LATERAL
 (SELECT completed_at FROM product_refund_checks WHERE payment_id=v.payment_id ORDER BY created_at DESC,id DESC LIMIT 1) c ON true WHERE v.payment_id=$1`, checkout.PaymentID).Scan(&delay); err != nil {
			t.Fatal(err)
		}
		want := min(1440, 15*(1<<min(round+1, 7))) * 60
		if delay != float64(want) {
			t.Fatalf("round %d delay=%v want=%d", round, delay, want)
		}
	}
	assertProductRefundState(t, pool, checkout, "refund_pending", "refund_requested", "active", 0, 0)
	history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil || !history.CanCheck {
		t.Fatalf("manual unavailable: %+v %v", history, err)
	}
	history, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion)
	if err != nil || history.LatestCheck.Origin != "operator" {
		t.Fatalf("manual provenance: %+v %v", history, err)
	}
	if n, err := service.reconcileProductRefunds(ctx, 100, time.Now().Add(48*time.Hour)); err != nil || n != 0 {
		t.Fatalf("manual overlap: %d %v", n, err)
	}
}

func TestProductRefundAutomaticEligibilityAndRecovery(t *testing.T) {
	for _, scenario := range []string{"dispatch_queued", "dispatch_running", "check_failed", "check_cancelled", "disabled", "unsupported", "identity_gap", "busy"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, _, checkout, _ := automaticRefundFixture(t, false)
			ctx := context.Background()
			switch scenario {
			case "dispatch_queued", "dispatch_running":
				state := strings.TrimPrefix(scenario, "dispatch_")
				if _, err := pool.Exec(ctx, `UPDATE jobs SET status=$2 WHERE kind=$1`, ProductRefundJobKind, state); err != nil {
					t.Fatal(err)
				}
			case "check_failed", "check_cancelled":
				h, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
				if err != nil {
					t.Fatal(err)
				}
				h, err = service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, h.PaymentVersion)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `UPDATE jobs SET status=$2 WHERE id=(SELECT job_id FROM product_refund_checks WHERE id=$1)`, h.LatestCheck.ID, strings.TrimPrefix(scenario, "check_")); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				service.config.Enabled = false
			case "unsupported":
				service.runtimes = NewRuntimeCatalog(&productCheckoutRuntime{})
			case "identity_gap":
				// Model a legacy gap with a view override in this isolated schema;
				// never erase immutable checkout evidence to construct a fixture.
				if _, err := pool.Exec(ctx, `CREATE OR REPLACE VIEW product_payment_identity_gaps AS SELECT id AS payment_id FROM payment_intents WHERE purpose='product'`); err != nil {
					t.Fatal(err)
				}
			case "busy":
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(ctx)
				if _, err := tx.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, checkout.PaymentID); err != nil {
					t.Fatal(err)
				}
			}
			pass, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			if n, err := service.reconcileProductRefunds(pass, 100, time.Now().Add(48*time.Hour)); err != nil || n != 0 {
				t.Fatalf("ineligible scheduled: %d %v", n, err)
			}
			if strings.HasPrefix(scenario, "check_") {
				h, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
				if err != nil || h.LatestCheck.Status != "failed" || !h.CanCheck {
					t.Fatalf("terminal query not recoverable: %+v %v", h, err)
				}
				if _, err := service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, h.PaymentVersion); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestProductRefundAutomaticMigrationRoundtripAndEvidenceGuard(t *testing.T) {
	pool, service, _, _, _ := automaticRefundFixture(t, false)
	ctx := context.Background()
	applyConfirmation := func(direction string) {
		body, err := os.ReadFile("../platform/database/migrations/0133_product_closed_checkout_refund_confirmation." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	down, err := os.ReadFile("../platform/database/migrations/0106_product_refund_reconciliation.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../platform/database/migrations/0106_product_refund_reconciliation.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	// The confirmation candidate references the refund-check row, including
	// its provenance column. Roll back that empty dependent migration first.
	applyConfirmation("down")
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	applyConfirmation("up")
	if n, err := service.reconcileProductRefunds(ctx, 100, time.Now().Add(16*time.Minute)); err != nil || n != 1 {
		t.Fatalf("dispatch after roundtrip: %d %v", n, err)
	}
	if _, err := pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard automatic refund query provenance") {
		t.Fatalf("expected automatic evidence guard, got %v", err)
	}
	if _, err := service.ReconcileProductRefunds(ctx, 0); !errors.Is(err, ErrInvalidRefund) {
		t.Fatal(err)
	}
}

func TestProductRefundAutomaticFairnessAndLockedRecheck(t *testing.T) {
	pool, service, _, first, _ := automaticRefundFixture(t, false)
	ctx := context.Background()
	// A separate historical pending operation, with distinct remote identities.
	// This fixture exercises scheduling only, not fulfillment of the second order.
	buyer, seller, _, product := newProductCheckoutFixture(t, pool)
	order, payment := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,idempotency_key,license_version,license_terms_snapshot,product_title_snapshot,license_name_snapshot,refund_window_days_snapshot)
 VALUES($1,$2,$3,1900,'USD','refund_requested','fairness-history','1','Accepted historical terms','Historical resource','Commercial',14)`, order, buyer, product); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key,provider_payment_id)
 VALUES($1,'stripe','product',$2,$3,$4,$5,1900,'USD','refund_pending',false,'fairness-history','pi_fairnesshistory')`, payment, buyer, seller, product, order); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO product_checkout_requests(payment_id,identity,request)
 SELECT $1,identity,jsonb_build_object('PaymentID',$1::uuid,'Purpose','product','ResourceID',$3::uuid,'OrderExternalID',$4::uuid,'BuyerIdentity',$5::uuid,'AmountCents',1900,'Currency','USD')
 FROM product_checkout_requests WHERE payment_id=$2`, payment, first.PaymentID, product, order, buyer); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO product_refund_attempts(operation_id,payment_id,provider,provider_payment_id,amount_cents,currency,correlation_enabled,status,requested_at)
 VALUES($1,$2,'stripe','pi_fairnesshistory',1900,'USD',true,'pending',now())`, uuid.New(), payment); err != nil {
		t.Fatal(err)
	}
	ids := []uuid.UUID{first.PaymentID, payment}
	if strings.Compare(ids[0].String(), ids[1].String()) > 0 {
		ids[0], ids[1] = ids[1], ids[0]
	}
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(ctx)
	if _, err := gate.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, ids[0]); err != nil {
		t.Fatal(err)
	}
	asOf := time.Now().Add(16 * time.Minute)
	if n, err := service.reconcileProductRefunds(ctx, 1, asOf); err != nil || n != 0 {
		t.Fatalf("busy first %d %v", n, err)
	}
	if n, err := service.reconcileProductRefunds(ctx, 1, asOf); err != nil || n != 1 {
		t.Fatalf("starved second %d %v", n, err)
	}
	if err := gate.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// The direct per-item operation must recheck, even if a stale scan selected it.
	if _, err := pool.Exec(ctx, `UPDATE product_refund_attempts SET status='failed',reconciliation_required=false WHERE payment_id=$1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	if changed, err := service.reconcileProductRefund(ctx, ids[0], asOf); err != nil || changed {
		t.Fatalf("stale candidate accepted %t %v", changed, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE product_refund_attempts SET status='pending' WHERE payment_id=$1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	if n, err := service.reconcileProductRefunds(ctx, 1, asOf); err != nil || n != 1 {
		t.Fatalf("did not wrap %d %v", n, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_refund_checks`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("checks %d %v", count, err)
	}
}

func TestProductRefundAutomaticFailurePreservesEvidence(t *testing.T) {
	pool, service, runtime, checkout, _ := automaticRefundFixture(t, false)
	ctx := context.Background()
	if n, err := service.reconcileProductRefunds(ctx, 100, time.Now().Add(16*time.Minute)); err != nil || n != 1 {
		t.Fatalf("schedule %d %v", n, err)
	}
	runtime.readError = newProviderFailure("payment_authentication", 0)
	repo := jobs.NewRepository(pool)
	job, err := repo.Claim(ctx, "failed-automatic-query", time.Minute)
	if err != nil || job.Kind != ProductRefundCheckJobKind {
		t.Fatalf("job %+v %v", job, err)
	}
	cause := service.HandleProductRefundCheckJob(ctx, job)
	if cause == nil || jobs.ShouldRetry(cause) {
		t.Fatalf("authentication error: %v", cause)
	}
	if err := repo.Fail(ctx, job, "failed-automatic-query", cause); err != nil {
		t.Fatal(err)
	}
	if n, err := service.reconcileProductRefunds(ctx, 100, time.Now().Add(30*24*time.Hour)); err != nil || n != 0 {
		t.Fatalf("failed check reset %d %v", n, err)
	}
	assertProductRefundState(t, pool, checkout, "refund_pending", "refund_requested", "active", 0, 0)
	h, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
	if err != nil || h.LatestCheck.Status != "failed" || h.LatestCheck.Origin != "automatic" || !h.CanCheck {
		t.Fatalf("history %+v %v", h, err)
	}
	previous := h.LatestCheck.ID
	if _, err := service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, h.PaymentVersion); err != nil {
		t.Fatal(err)
	}
	runtime.readError = nil
	runAutomaticRefundCheck(t, service)
	var origin, state string
	if err := pool.QueryRow(ctx, `SELECT origin,status FROM product_refund_checks WHERE id=$1`, previous).Scan(&origin, &state); err != nil || origin != "automatic" || state != "failed" {
		t.Fatalf("previous overwritten %s %s %v", origin, state, err)
	}
	assertProductRefundState(t, pool, checkout, "refunded", "refunded", "refunded", 0, 0)
	if len(runtime.operations) != 1 {
		t.Fatal("query repeated money movement")
	}
}
