package payments

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

func missingCheckout(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := operationalPaymentFixture(t, pool, false, time.Now())
	if _, err := pool.Exec(t.Context(), `UPDATE payment_intents SET status='checkout_open',provider_checkout_id=$2,checkout_url='https://checkout.stripe.com/c/pay/missing',checkout_expires_at=now()-interval '1 minute' WHERE id=$1`, id, "cs_"+strings.ReplaceAll(id.String(), "-", "")); err != nil {
		t.Fatal(err)
	}
	return id
}

func dispatchedCheckoutJob(t *testing.T, pool *pgxpool.Pool, payment uuid.UUID) jobs.Job {
	t.Helper()
	var job jobs.Job
	if err := pool.QueryRow(t.Context(), `SELECT j.id,j.kind,j.payload,j.max_attempts FROM product_checkout_check_dispatches d JOIN jobs j ON j.id=d.job_id WHERE d.payment_id=$1`, payment).Scan(&job.ID, &job.Kind, &job.Payload, &job.MaxAttempts); err != nil {
		t.Fatal(err)
	}
	if job.Kind != ProductCheckoutCheckJobKind || job.MaxAttempts != 20 {
		t.Fatalf("invalid read job: %+v", job)
	}
	return job
}

func TestProductCheckoutReconciliationToProviderResult(t *testing.T) {
	for _, outcome := range []string{"expired", "paid"} {
		t.Run(outcome, func(t *testing.T) {
			pool, service, runtime, checkout, buyer, original := checkoutCheckFixture(t)
			ctx := t.Context()
			// Reproduce a historical missing job, before any query evidence exists.
			if _, err := pool.Exec(ctx, `DELETE FROM jobs WHERE id=$1`, original.ID); err != nil {
				t.Fatal(err)
			}
			reads := 0
			runtime.read = func(_ context.Context, r CheckoutReadRequest) (CheckoutObservation, error) {
				reads++
				o := CheckoutObservation{ProviderCheckoutID: r.ProviderCheckoutID, AmountCents: r.AmountCents, Currency: r.Currency, LiveMode: r.LiveMode, Status: "expired", PaymentStatus: "unpaid", ExpiresAt: time.Now().Add(-time.Minute)}
				if outcome == "paid" {
					o.Status, o.PaymentStatus, o.IntentStatus = "complete", "paid", "succeeded"
					o.ProviderPaymentID, o.ProviderChargeID, o.AmountReceived = "pi_reconciled", "ch_reconciled", r.AmountCents
				}
				return o, nil
			}
			assertOperationalMetric(t, pool, "problem", "checkout_check_missing", "test", 1, 0, 0)
			if n, err := service.ReconcileProductCheckouts(ctx, 100); err != nil || n != 1 {
				t.Fatalf("schedule %d %v", n, err)
			}
			if reads != 0 {
				t.Fatal("scheduler called provider")
			}
			assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
			assertOperationalMetric(t, pool, "problem", "checkout_check_missing", "test", 0, 0, 0)
			restarted := newPaymentTestService(t, pool, service.config, NewRuntimeCatalog(runtime))
			if n, err := restarted.ReconcileProductCheckouts(ctx, 100); err != nil || n != 0 {
				t.Fatal(n, err)
			}
			job := dispatchedCheckoutJob(t, pool, checkout.PaymentID)
			repo := jobs.NewRepository(pool)
			claimed, err := repo.Claim(ctx, "checkout-recovery-test", time.Minute)
			if err != nil || claimed.ID != job.ID {
				t.Fatal("read job not claimable", claimed.ID, err)
			}
			job = claimed
			if err := restarted.HandleProductCheckoutCheckJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if err := repo.Complete(ctx, job, "checkout-recovery-test"); err != nil {
				t.Fatal(err)
			}
			if outcome == "expired" {
				assertCheckoutState(t, pool, checkout, "cancelled", "cancelled", 0)
			} else {
				var event uuid.UUID
				if err := pool.QueryRow(ctx, `SELECT id FROM payment_provider_events WHERE checkout_job_id=$1`, job.ID).Scan(&event); err != nil {
					t.Fatal(err)
				}
				body, _ := json.Marshal(map[string]uuid.UUID{"eventId": event})
				if err := restarted.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: body}); err != nil {
					t.Fatal(err)
				}
				assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
			}
			if reads != 1 || runtime.calls.Load() != 1 {
				t.Fatal("unexpected provider reads or checkout recreation", reads, runtime.calls.Load())
			}
			pkg, body := runProductExport(t, pool, buyer)
			rows := pkg.Data.Marketplace.Data["checkoutCheckDispatches"]
			if len(rows) != 1 || rows[0]["jobId"] != job.ID.String() {
				t.Fatalf("dispatch export: %+v", rows)
			}
			if strings.Contains(string(body), "https://checkout.stripe.com") {
				t.Fatal("private checkout URL exported")
			}
			var seller uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT payee_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&seller); err != nil {
				t.Fatal(err)
			}
			other, _ := runProductExport(t, pool, seller)
			if len(other.Data.Marketplace.Data["checkoutCheckDispatches"]) != 0 {
				t.Fatal("buyer query evidence leaked to seller")
			}
		})
	}
}

func TestProductCheckoutReconciliationEligibility(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(&checkoutReadRuntime{}))
	for _, scenario := range []string{"future", "infinite", "failed", "cancelled", "succeeded", "queued", "identity_missing", "waffo", "order_changed", "pending_event", "event_processing_missing"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			id := missingCheckout(t, pool)
			exec := func(sql string) {
				t.Helper()
				if _, err := pool.Exec(ctx, sql, id); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "future":
				exec(`UPDATE payment_intents SET checkout_expires_at=now()+interval '1 hour' WHERE id=$1`)
			case "infinite":
				exec(`UPDATE payment_intents SET checkout_expires_at='infinity' WHERE id=$1`)
			case "failed", "cancelled", "succeeded", "queued":
				if _, err := pool.Exec(ctx, `INSERT INTO jobs(kind,payload,status) VALUES($1,jsonb_build_object('paymentId',$2::text),$3)`, ProductCheckoutCheckJobKind, id, scenario); err != nil {
					t.Fatal(err)
				}
			case "identity_missing":
				// The checkout fixture's request is immutable. Simulate a pre-snapshot
				// row only inside this isolated schema, then restore its guard.
				if _, err := pool.Exec(ctx, `ALTER TABLE product_checkout_requests DISABLE TRIGGER USER`); err != nil {
					t.Fatal(err)
				}
				exec(`DELETE FROM product_checkout_requests WHERE payment_id=$1`)
				if _, err := pool.Exec(ctx, `ALTER TABLE product_checkout_requests ENABLE TRIGGER USER`); err != nil {
					t.Fatal(err)
				}
			case "waffo":
				exec(`UPDATE payment_intents SET provider='waffo_pancake' WHERE id=$1`)
			case "order_changed":
				exec(`UPDATE orders SET status='cancelled' WHERE id=(SELECT order_id FROM payment_intents WHERE id=$1)`)
			case "pending_event", "event_processing_missing":
				var event uuid.UUID
				if err := pool.QueryRow(ctx, `INSERT INTO payment_provider_events(provider,provider_event_id,event_type,api_version,live_mode,occurred_at,payload_sha256,object_id,object_type,payment_id,purpose) VALUES('stripe',$2,'checkout.session.completed','2026-02-25.clover',false,now(),repeat('a',64),'cs_pending','checkout.session',$1,'product') RETURNING id`, id, "evt_"+id.String()).Scan(&event); err != nil {
					t.Fatal(err)
				}
				if scenario == "pending_event" {
					if _, err := pool.Exec(ctx, `INSERT INTO payment_provider_event_processing(event_id,status) VALUES($1,'received')`, event); err != nil {
						t.Fatal(err)
					}
				}
			}
			if changed, err := service.reconcileProductCheckout(ctx, id); err != nil || changed {
				t.Fatal("ineligible checkout scheduled", changed, err)
			}
		})
	}
	if n, err := service.ReconcileProductCheckouts(t.Context(), 0); err == nil || n != 0 {
		t.Fatal("invalid limit accepted", n, err)
	}
}

func TestProductCheckoutReconciliationConcurrencyAndLocks(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := t.Context()
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(&checkoutReadRuntime{}))
	id := missingCheckout(t, pool)
	locked, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Rollback(ctx)
	if _, err = locked.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	other := missingCheckout(t, pool)
	totalScheduled := 0
	for range 2 {
		n, err := service.ReconcileProductCheckouts(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		totalScheduled += n
	}
	if totalScheduled != 1 {
		t.Fatal("locked head blocked bounded scan", totalScheduled)
	}
	dispatchedCheckoutJob(t, pool, other)
	if err = locked.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	counts := make(chan int, 2)
	errs := make(chan error, 2)
	for range 2 {
		restarted := newPaymentTestService(t, pool, service.config, NewRuntimeCatalog(&checkoutReadRuntime{}))
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, e := restarted.ReconcileProductCheckouts(ctx, 100)
			counts <- n
			errs <- e
		}()
	}
	wg.Wait()
	close(counts)
	close(errs)
	total := 0
	for n := range counts {
		total += n
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if total != 1 {
		t.Fatal("duplicate or lost scheduling", total)
	}
	dispatchedCheckoutJob(t, pool, id)
	var events, audits int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.check_scheduled'),(SELECT count(*) FROM audit_events WHERE resource_id=$1 AND action='payment.checkout_check_scheduled')`, id).Scan(&events, &audits); err != nil || events != 1 || audits != 1 {
		t.Fatal(events, audits, err)
	}
}

func TestProductCheckoutReconciliationTerminalLookupAndOrderLock(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := t.Context()
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(&checkoutReadRuntime{}))
	id := missingCheckout(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET checkout_expires_at=now()+interval '1 hour',checkout_url=NULL WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if n, err := service.ReconcileProductCheckouts(ctx, 100); err != nil || n != 0 {
		t.Fatal("unproven early query", n, err)
	}
	var lookup uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,status) VALUES($1,'succeeded') RETURNING id`, ProductCheckoutLookupJobKind).Scan(&lookup); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO product_checkout_lookups(job_id,payment_id,requested_by,outcome,searched_after,searched_before,result)
 SELECT $2,id,payer_id,'found',now()-interval '1 hour',now(),jsonb_build_object('outcome','found','observation',jsonb_build_object('providerCheckoutId',provider_checkout_id,'status','complete','paymentStatus','paid')) FROM payment_intents WHERE id=$1`, id, lookup); err != nil {
		t.Fatal(err)
	}
	locked, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Rollback(ctx)
	if _, err = locked.Exec(ctx, `SELECT o.id FROM orders o JOIN payment_intents p ON p.order_id=o.id WHERE p.id=$1 FOR UPDATE OF o`, id); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if n, err := service.ReconcileProductCheckouts(bounded, 100); err != nil || n != 0 {
		t.Fatal("busy order blocked scan", n, err)
	}
	if err := locked.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := service.ReconcileProductCheckouts(ctx, 100); err != nil || n != 1 {
		t.Fatal("proven terminal lookup not resumed", n, err)
	}
	dispatchedCheckoutJob(t, pool, id)
}

func TestProductCheckoutReconciliationAtomicEvidenceAndMigration(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := t.Context()
	apply := func(suffix string) error {
		body, err := os.ReadFile("../platform/database/migrations/0124_product_checkout_check_reconciliation." + suffix + ".sql")
		if err != nil {
			return err
		}
		_, err = pool.Exec(ctx, string(body))
		return err
	}
	if err := apply("down"); err != nil {
		t.Fatal(err)
	}
	if err := apply("up"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO maintenance_health(kind,passes,failures,last_failed,completed_at,last_success_at) VALUES('product_checkout_reconciliation',1,0,false,now(),now())`); err != nil {
		t.Fatal(err)
	}
	id := missingCheckout(t, pool)
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(&checkoutReadRuntime{}))
	if _, err := pool.Exec(ctx, `CREATE FUNCTION refuse_checkout_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='payment.checkout_check_scheduled' THEN RAISE EXCEPTION 'test audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER refuse_checkout_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION refuse_checkout_audit()`); err != nil {
		t.Fatal(err)
	}
	if n, err := service.ReconcileProductCheckouts(ctx, 100); err == nil || n != 0 {
		t.Fatal("failed audit committed", n, err)
	}
	var count, version int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM jobs WHERE kind='payment.check_product_checkout'),version FROM payment_intents WHERE id=$1`, id).Scan(&count, &version); err != nil || count != 0 || version != 1 {
		t.Fatal(count, version, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER refuse_checkout_audit ON audit_events; DROP FUNCTION refuse_checkout_audit()`); err != nil {
		t.Fatal(err)
	}
	if n, err := service.ReconcileProductCheckouts(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	for _, sql := range []string{`DELETE FROM product_checkout_check_dispatches WHERE payment_id=$1`, `UPDATE product_checkout_check_dispatches SET due_at=now() WHERE payment_id=$1`} {
		if _, err := pool.Exec(ctx, sql, id); err == nil {
			t.Fatal("dispatch evidence mutable")
		}
	}
	if err := apply("down"); err == nil || !strings.Contains(err.Error(), "durable evidence") {
		t.Fatal("rollback discarded evidence", err)
	}
	// A dispatch must not point to another kind of job, even when the original
	// payment and version exist. The deferred guard rejects the whole statement.
	other := missingCheckout(t, pool)
	var wrongJob uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind) VALUES('test.unrelated') RETURNING id`).Scan(&wrongJob); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET version=2 WHERE id=$1`, other); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO product_checkout_check_dispatches(payment_id,job_id,payment_version,due_at) VALUES($1,$2,1,now())`, other, wrongJob); err == nil || !strings.Contains(err.Error(), "matching product query job") {
		t.Fatal("unbound dispatch accepted", err)
	}
}
