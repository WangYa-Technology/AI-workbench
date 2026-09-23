package payments

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
)

func TestClosedCheckoutPendingRefundAutomaticRecovery(t *testing.T) {
	for _, status := range []string{"cancelled", "payment_failed"} {
		t.Run(status, func(t *testing.T) {
			pool, service, checkout, _, check := closedCheckoutFixture(t, status)
			ctx := t.Context()
			operation := uuid.New()
			quarantineExec(t, pool, `INSERT INTO product_refund_attempts(operation_id,payment_id,provider,provider_payment_id,amount_cents,currency,correlation_enabled,provider_refund_id,status,requested_at) VALUES($1,$2,'stripe','pi_closed_saved',1900,'USD',true,'re_automatic_historical','pending',now()-interval '1 day')`, operation, checkout.PaymentID)
			quarantineExec(t, pool, `UPDATE orders SET refund_operation_id=$2,refund_correlation_enabled=true,refund_requested_at=now()-interval '1 day' WHERE id=$1`, checkout.OrderID, operation)
			if err := service.HandleProductCheckoutCheckJob(ctx, check); err != nil {
				t.Fatal(err)
			}
			// The lookup and checkout handlers already ran in the fixture. Finish only
			// those jobs; all subsequent refund reads use real queue claims/completions.
			quarantineExec(t, pool, `UPDATE jobs SET status='succeeded' WHERE kind IN ($1,$2)`, ProductCheckoutLookupJobKind, ProductCheckoutCheckJobKind)
			runtime := &refundReadRuntime{observations: []RefundObservation{{ProviderID: "re_automatic_historical", ProviderPaymentID: "pi_closed_saved", AmountCents: 1900, Currency: "USD", Status: "pending", OperationID: &operation}}}
			service.runtimes = NewRuntimeCatalog(runtime)
			first := runAutomaticRefundCheck(t, service)
			assertCheckoutState(t, pool, checkout, status, status, 0)
			assertOperationalMetric(t, pool, "problem", "closed_checkout_paid", "test", 1, 0, 0)
			var due time.Time
			var delay float64
			if err := pool.QueryRow(ctx, `SELECT v.due_at,extract(epoch FROM (v.due_at-c.completed_at)) FROM product_refund_reconciliation_candidates v JOIN product_refund_checks c ON c.job_id=$2 WHERE v.payment_id=$1`, checkout.PaymentID, first.ID).Scan(&due, &delay); err != nil {
				t.Fatal("closed pending refund lost automatic follow-up", err)
			}
			if delay != 30*60 {
				t.Fatal("wrong historical refund backoff", delay)
			}
			if n, err := service.reconcileProductRefunds(ctx, 100, due.Add(-time.Microsecond)); err != nil || n != 0 {
				t.Fatal("premature read", n, err)
			}
			var wg sync.WaitGroup
			for range 2 {
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
			quarantineCount(t, pool, `SELECT count(*) FROM product_refund_checks WHERE payment_id=$1`, 2, checkout.PaymentID)
			if runtime.reads != 1 || len(runtime.operations) != 0 {
				t.Fatal("scheduling called provider", runtime.reads, runtime.operations)
			}
			second := runAutomaticRefundCheck(t, service)
			if err := pool.QueryRow(ctx, `SELECT v.due_at,extract(epoch FROM (v.due_at-c.completed_at)) FROM product_refund_reconciliation_candidates v JOIN product_refund_checks c ON c.job_id=$2 WHERE v.payment_id=$1`, checkout.PaymentID, second.ID).Scan(&due, &delay); err != nil || delay != 60*60 {
				t.Fatal("pending follow-up lost backoff", due, delay, err)
			}
			// A new service resumes the same obligation when the original remote
			// refund eventually succeeds; it must not issue a new refund operation.
			runtime.observations[0].Status = "succeeded"
			restarted := newPaymentTestService(t, pool, service.config, NewRuntimeCatalog(runtime))
			if n, err := restarted.reconcileProductRefunds(ctx, 100, due); err != nil || n != 1 {
				t.Fatal("restart did not resume read", n, err)
			}
			third := runAutomaticRefundCheck(t, restarted)
			assertCheckoutState(t, pool, checkout, "refunded", "refunded", 0)
			quarantineCount(t, pool, `SELECT count(*) FROM product_closed_checkout_refund_confirmations WHERE payment_id=$1`, 1, checkout.PaymentID)
			var original jobs.Job
			if err := pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM product_closed_checkout_recoveries r JOIN jobs j ON j.kind=$2 AND j.payload->>'eventId'=r.event_id::text WHERE r.payment_id=$1`, checkout.PaymentID, PaymentEventJobKind).Scan(&original.ID, &original.Kind, &original.Payload); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := restarted.HandlePaymentEventJob(ctx, original); err != nil {
					t.Fatal(err)
				}
			}
			if err := restarted.HandleProductRefundCheckJob(ctx, third); err != nil {
				t.Fatal(err)
			}
			assertOperationalMetric(t, pool, "problem", "closed_checkout_paid", "test", 0, 0, 0)
			quarantineCount(t, pool, `SELECT count(*) FROM product_refund_reconciliation_candidates WHERE payment_id=$1`, 0, checkout.PaymentID)
			quarantineCount(t, pool, `SELECT count(*) FROM product_refund_attempts WHERE payment_id=$1`, 1, checkout.PaymentID)
			quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE kind=$2 AND payload->>'paymentId'=$1`, 0, checkout.PaymentID.String(), ProductRefundJobKind)
			if runtime.reads != 3 || len(runtime.operations) != 0 {
				t.Fatal("recovery repeated financial dispatch", runtime.reads, runtime.operations)
			}
			var cleanup jobs.Job
			if err := pool.QueryRow(ctx, `SELECT id,kind,payload FROM jobs WHERE kind=$1 AND payload->>'orderId'=$2 ORDER BY created_at DESC,id DESC LIMIT 1`, productdelivery.CleanupJobKind, checkout.OrderID.String()).Scan(&cleanup.ID, &cleanup.Kind, &cleanup.Payload); err != nil {
				t.Fatal(err)
			}
			if err := productdelivery.CleanupHandler(pool, service.config.MediaStores)(ctx, cleanup); err != nil {
				t.Fatal(err)
			}
			snapshot, err := productdelivery.Load(ctx, pool, checkout.OrderID)
			if err != nil || snapshot.State != "removed" {
				t.Fatal("resolved historical refund retained copy", snapshot.State, err)
			}
			var observations []byte
			if err := pool.QueryRow(ctx, `SELECT observations FROM product_refund_checks WHERE job_id=$1`, first.ID).Scan(&observations); err != nil {
				t.Fatal(err)
			}
			var saved []RefundObservation
			if err := json.Unmarshal(observations, &saved); err != nil || len(saved) != 1 || saved[0].Status != "pending" {
				t.Fatal("original pending evidence overwritten", string(observations), err)
			}
		})
	}
}

func TestClosedCheckoutAutomaticRefundGuards(t *testing.T) {
	for _, scenario := range []string{"no_marker", "order_mismatch", "active_dispatch", "failed_query", "locked_payment", "rollback"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, checkout, _, check := closedCheckoutFixture(t, "cancelled")
			ctx := t.Context()
			apply := func(direction string) error {
				body, err := os.ReadFile("../platform/database/migrations/0134_product_closed_refund_reconciliation." + direction + ".sql")
				if err != nil {
					t.Fatal(err)
				}
				_, err = pool.Exec(ctx, string(body))
				return err
			}
			if scenario == "rollback" {
				if err := apply("down"); err != nil {
					t.Fatal("empty rollback", err)
				}
				if err := apply("up"); err != nil {
					t.Fatal(err)
				}
			}
			operation := uuid.New()
			quarantineExec(t, pool, `INSERT INTO product_refund_attempts(operation_id,payment_id,provider,provider_payment_id,amount_cents,currency,correlation_enabled,provider_refund_id,status,requested_at) VALUES($1,$2,'stripe','pi_closed_saved',1900,'USD',true,'re_closed_guard','pending',now()-interval '1 day')`, operation, checkout.PaymentID)
			quarantineExec(t, pool, `UPDATE orders SET refund_operation_id=$2,refund_correlation_enabled=true,refund_requested_at=now()-interval '1 day' WHERE id=$1`, checkout.OrderID, operation)
			runtime := &refundReadRuntime{observations: []RefundObservation{{ProviderID: "re_closed_guard", ProviderPaymentID: "pi_closed_saved", AmountCents: 1900, Currency: "USD", Status: "pending", OperationID: &operation}}}
			service.runtimes = NewRuntimeCatalog(runtime)
			if scenario == "no_marker" {
				quarantineExec(t, pool, `UPDATE payment_intents SET provider_payment_id='pi_closed_saved' WHERE id=$1`, checkout.PaymentID)
				if n, err := service.reconcileProductRefunds(ctx, 100, time.Now().Add(48*time.Hour)); err != nil || n != 0 {
					t.Fatal("unproven closed payment auto-scheduled", n, err)
				}
				quarantineCount(t, pool, `SELECT count(*) FROM product_refund_checks WHERE payment_id=$1`, 0, checkout.PaymentID)
				return
			}
			if err := service.HandleProductCheckoutCheckJob(ctx, check); err != nil {
				t.Fatal(err)
			}
			quarantineExec(t, pool, `UPDATE jobs SET status='succeeded' WHERE kind IN ($1,$2)`, ProductCheckoutLookupJobKind, ProductCheckoutCheckJobKind)
			if scenario == "failed_query" {
				runtime.readError = newProviderFailure("payment_provider_unavailable", 0)
				repo := jobs.NewRepository(pool)
				job, err := repo.Claim(ctx, "closed-failed-query", time.Minute)
				if err != nil || job.Kind != ProductRefundCheckJobKind {
					t.Fatal(job, err)
				}
				failure := service.HandleProductRefundCheckJob(ctx, job)
				if failure == nil {
					t.Fatal("failed read reported success")
				}
				if err := repo.Fail(ctx, job, "closed-failed-query", failure); err != nil {
					t.Fatal(err)
				}
				quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE id=$1 AND status='failed'`, 1, job.ID)
			} else {
				runAutomaticRefundCheck(t, service)
			}
			if scenario == "rollback" {
				if err := apply("down"); err == nil || !strings.Contains(err.Error(), "cannot remove automatic closed refund follow-up while funds remain unresolved") {
					t.Fatal("rollback lost pending historical follow-up", err)
				}
				quarantineCount(t, pool, `SELECT count(*) FROM product_refund_reconciliation_candidates WHERE payment_id=$1`, 1, checkout.PaymentID)
				return
			}
			if scenario == "order_mismatch" {
				quarantineExec(t, pool, `UPDATE orders SET status='fulfilled' WHERE id=$1`, checkout.OrderID)
			}
			if scenario == "active_dispatch" {
				quarantineExec(t, pool, `INSERT INTO jobs(kind,payload) VALUES($1,jsonb_build_object('paymentId',$2::text,'operationId',$3::text))`, ProductRefundJobKind, checkout.PaymentID, operation)
			}
			release := func() {}
			if scenario == "locked_payment" {
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, checkout.PaymentID); err != nil {
					t.Fatal(err)
				}
				release = func() {
					if err := tx.Rollback(ctx); err != nil {
						t.Fatal(err)
					}
				}
				defer tx.Rollback(ctx)
			}
			if n, err := service.reconcileProductRefunds(ctx, 100, time.Now().Add(48*time.Hour)); err != nil || n != 0 {
				t.Fatal("guard allowed automatic dispatch", scenario, n, err)
			}
			quarantineCount(t, pool, `SELECT count(*) FROM product_refund_checks WHERE payment_id=$1`, 1, checkout.PaymentID)
			if len(runtime.operations) != 0 || runtime.reads != 1 {
				t.Fatal("guard performed remote operation", runtime.reads, runtime.operations)
			}
			release()
			if scenario == "locked_payment" {
				if n, err := service.reconcileProductRefunds(ctx, 100, time.Now().Add(48*time.Hour)); err != nil || n != 1 {
					t.Fatal("released payment not rediscovered", n, err)
				}
			}
		})
	}
}
