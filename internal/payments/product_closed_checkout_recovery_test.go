package payments

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestClosedProductCheckoutSavedPayment(t *testing.T) {
	for _, scenario := range []string{"new_checkout", "cleanup", "recover_cancelled", "recover_failed"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, checkout, buyer, lookup := pendingCheckoutLookupFixture(t)
			ctx := t.Context()
			runtime.observation.Status, runtime.observation.PaymentStatus, runtime.observation.IntentStatus = "complete", "paid", "succeeded"
			runtime.observation.ProviderPaymentID, runtime.observation.ProviderChargeID, runtime.observation.AmountReceived = "pi_closed_saved", "ch_closed_saved", checkout.AmountCents
			if err := service.HandleProductCheckoutLookupJob(ctx, lookup); err != nil {
				t.Fatal(err)
			}
			check := locatedCheckJob(t, pool, lookup.ID)
			status := "cancelled"
			if scenario == "recover_failed" {
				status = "payment_failed"
			}
			// Historical order state from the old expiry/failure path; immutable paid
			// observations remain intact. This fixture runs only in its private schema.
			quarantineExec(t, pool, `UPDATE payment_intents SET status=$2 WHERE id=$1`, checkout.PaymentID, status)
			quarantineExec(t, pool, `UPDATE orders SET status=$2 WHERE id=$1`, checkout.OrderID, status)
			switch scenario {
			case "new_checkout":
				_, _, err := service.BeginProductCheckout(ctx, buyer, checkout.ResourceID, "closed-payment-retry", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, checkout.ResourceID))
				if !errors.Is(err, ErrCheckoutReconciliation) || len(runtime.requests) != 1 {
					t.Fatal("known closed-order payment allowed a new checkout dispatch", err, len(runtime.requests))
				}
			case "cleanup":
				var cleanup jobs.Job
				if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('orderId',$2::text),20) RETURNING id,kind,payload`, productdelivery.CleanupJobKind, checkout.OrderID).Scan(&cleanup.ID, &cleanup.Kind, &cleanup.Payload); err != nil {
					t.Fatal(err)
				}
				if err := productdelivery.CleanupHandler(pool, service.config.MediaStores)(ctx, cleanup); err != nil {
					t.Fatal(err)
				}
				snapshot, err := productdelivery.Load(ctx, pool, checkout.OrderID)
				if err != nil || snapshot.State != "ready" {
					t.Fatal("closed-order payment lost its delivery evidence", snapshot, err)
				}
			default:
				service.runtimes = NewRuntimeCatalog()
				if err := service.HandleProductCheckoutCheckJob(context.Background(), check); err != nil {
					t.Fatal(err)
				}
				assertCheckoutState(t, pool, checkout, status, status, 0)
				quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE payment_id=$1 AND payment_status='paid'`, 1, checkout.PaymentID)
				quarantineCount(t, pool, `SELECT count(*) FROM product_refund_checks WHERE payment_id=$1`, 1, checkout.PaymentID)
				quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE kind='payment.refund_product' AND payload->>'paymentId'=$1`, 0, checkout.PaymentID.String())
			}
		})
	}
}

func closedCheckoutFixture(t *testing.T, status string) (*pgxpool.Pool, *Service, Checkout, uuid.UUID, jobs.Job) {
	t.Helper()
	pool, service, runtime, checkout, buyer, lookup := pendingCheckoutLookupFixture(t)
	runtime.observation.Status, runtime.observation.PaymentStatus, runtime.observation.IntentStatus = "complete", "paid", "succeeded"
	runtime.observation.ProviderPaymentID, runtime.observation.ProviderChargeID, runtime.observation.AmountReceived = "pi_closed_saved", "ch_closed_saved", checkout.AmountCents
	if err := service.HandleProductCheckoutLookupJob(t.Context(), lookup); err != nil {
		t.Fatal(err)
	}
	quarantineExec(t, pool, `UPDATE payment_intents SET status=$2 WHERE id=$1`, checkout.PaymentID, status)
	quarantineExec(t, pool, `UPDATE orders SET status=$2 WHERE id=$1`, checkout.OrderID, status)
	return pool, service, checkout, buyer, locatedCheckJob(t, pool, lookup.ID)
}

func closedCheckoutHistoryJob(t *testing.T, pool *pgxpool.Pool, payment uuid.UUID) jobs.Job {
	t.Helper()
	var job jobs.Job
	if err := pool.QueryRow(t.Context(), `SELECT j.id,j.kind,j.payload FROM product_refund_checks c JOIN jobs j ON j.id=c.job_id WHERE c.payment_id=$1 ORDER BY c.created_at DESC,c.id DESC LIMIT 1`, payment).Scan(&job.ID, &job.Kind, &job.Payload); err != nil {
		t.Fatal(err)
	}
	return job
}

func TestClosedProductCheckoutRefundLifecycle(t *testing.T) {
	for _, scenario := range []string{"cancelled", "payment_failed", "offline_retry", "unknown_refund", "partial_read", "historical_attempt", "prior_history"} {
		t.Run(scenario, func(t *testing.T) {
			status := "cancelled"
			if scenario == "payment_failed" {
				status = scenario
			}
			pool, service, checkout, buyer, check := closedCheckoutFixture(t, status)
			ctx := t.Context()
			if scenario == "prior_history" {
				// A query requested before the recovery marker cannot satisfy its
				// preflight even if its result is saved afterward.
				quarantineExec(t, pool, `UPDATE payment_intents SET provider_payment_id='pi_closed_saved' WHERE id=$1`, checkout.PaymentID)
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := insertProductRefundCheckWithOriginTx(ctx, tx, nil, checkout.PaymentID, "automatic"); err != nil {
					_ = tx.Rollback(ctx)
					t.Fatal(err)
				}
				if err := tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "historical_attempt" {
				quarantineExec(t, pool, `INSERT INTO product_refund_attempts(operation_id,payment_id,provider,provider_payment_id,amount_cents,currency,correlation_enabled,status,requested_at) VALUES(gen_random_uuid(),$1,'stripe','pi_closed_saved',1900,'USD',true,'requested',now())`, checkout.PaymentID)
			}
			service.runtimes = NewRuntimeCatalog()
			for range 2 {
				if err := service.HandleProductCheckoutCheckJob(ctx, check); err != nil {
					t.Fatal(err)
				}
			}
			assertCheckoutState(t, pool, checkout, status, status, 0)
			assertOperationalMetric(t, pool, "problem", "closed_checkout_paid", "test", 1, 0, 0)
			quarantineCount(t, pool, `SELECT count(*) FROM product_closed_checkout_recoveries WHERE payment_id=$1`, 1, checkout.PaymentID)
			quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE kind=$2 AND payload->>'eventId'=(SELECT event_id::text FROM product_closed_checkout_recoveries WHERE payment_id=$1)`, 0, checkout.PaymentID, PaymentEventJobKind)
			historyJob := closedCheckoutHistoryJob(t, pool, checkout.PaymentID)
			if scenario == "offline_retry" {
				if err := service.HandleProductRefundCheckJob(ctx, historyJob); err == nil {
					t.Fatal("offline read reported success")
				}
				quarantineExec(t, pool, `UPDATE jobs SET status='failed',last_error_code='payment_provider_unavailable' WHERE id=$1`, historyJob.ID)
			}
			runtime := &refundReadRuntime{}
			service.runtimes = NewRuntimeCatalog(runtime)
			if scenario == "offline_retry" {
				history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
				if err != nil || !history.CanCheck {
					t.Fatal("closed recovery cannot retry history", history, err)
				}
				if _, err := service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion); err != nil {
					t.Fatal(err)
				}
				historyJob = closedCheckoutHistoryJob(t, pool, checkout.PaymentID)
			}
			if scenario == "unknown_refund" || scenario == "partial_read" {
				runtime.observations = []RefundObservation{{ProviderID: "re_historical_manual", ProviderPaymentID: "pi_closed_saved", AmountCents: 1900, Currency: "USD", Status: "succeeded"}}
				if scenario == "partial_read" {
					runtime.readError = newProviderFailure("payment_authentication", 0)
				}
			}
			err := service.HandleProductRefundCheckJob(ctx, historyJob)
			if scenario == "partial_read" {
				if err == nil {
					t.Fatal("partial history reported success")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if len(runtime.operations) != 0 {
				t.Fatal("history read dispatched a refund")
			}
			if scenario == "prior_history" {
				quarantineCount(t, pool, `SELECT count(*) FROM product_closed_checkout_funds_ready WHERE payment_id=$1`, 0, checkout.PaymentID)
				next := closedCheckoutHistoryJob(t, pool, checkout.PaymentID)
				if next.ID == historyJob.ID {
					t.Fatal("pre-marker history did not schedule a fresh read")
				}
				if err := service.HandleProductRefundCheckJob(ctx, next); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "unknown_refund" || scenario == "partial_read" || scenario == "historical_attempt" {
				assertCheckoutState(t, pool, checkout, status, status, 0)
				quarantineCount(t, pool, `SELECT count(*) FROM product_closed_checkout_funds_ready WHERE payment_id=$1`, 0, checkout.PaymentID)
				quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE kind=$2 AND payload->>'paymentId'=$1`, 0, checkout.PaymentID.String(), ProductRefundJobKind)
				assertOperationalMetric(t, pool, "problem", "closed_checkout_paid", "test", 1, 0, 0)
				if scenario == "unknown_refund" {
					history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion); err != nil {
						t.Fatal(err)
					}
					runtime.observations = nil
					if err := service.HandleProductRefundCheckJob(ctx, closedCheckoutHistoryJob(t, pool, checkout.PaymentID)); err != nil {
						t.Fatal(err)
					}
					quarantineCount(t, pool, `SELECT count(*) FROM product_closed_checkout_funds_ready WHERE payment_id=$1`, 0, checkout.PaymentID)
					assertCheckoutState(t, pool, checkout, status, status, 0)
				}
				return
			}
			var eventJob jobs.Job
			if err := pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM product_closed_checkout_recoveries r JOIN jobs j ON j.payload->>'eventId'=r.event_id::text AND j.kind=$2 WHERE r.payment_id=$1`, checkout.PaymentID, PaymentEventJobKind).Scan(&eventJob.ID, &eventJob.Kind, &eventJob.Payload); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := service.HandlePaymentEventJob(ctx, eventJob); err != nil {
					t.Fatal(err)
				}
			}
			assertCheckoutState(t, pool, checkout, "refund_pending", "refund_requested", 0)
			assertOperationalMetric(t, pool, "problem", "closed_checkout_paid", "test", 0, 0, 0)
			refund := currentProductRefundJob(t, pool, checkout.PaymentID)
			for range 2 {
				if err := service.HandleProductRefundJob(ctx, refund); err != nil {
					t.Fatal(err)
				}
			}
			if len(runtime.operations) != 1 {
				t.Fatal("refund did not dispatch exactly once", runtime.operations)
			}
			operation := runtime.operations[0]
			runtime.observations = []RefundObservation{{ProviderID: "re_" + strings.ReplaceAll(operation.String(), "-", ""), ProviderPaymentID: "pi_closed_saved", AmountCents: 1900, Currency: "USD", Status: "succeeded", OperationID: &operation}}
			history, err := service.RefundHistory(ctx, checkout.PaymentID, "", 20)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.RequestRefundCheck(ctx, refundCheckOperator(t, pool), checkout.PaymentID, history.PaymentVersion); err != nil {
				t.Fatal(err)
			}
			if err := service.HandleProductRefundCheckJob(ctx, closedCheckoutHistoryJob(t, pool, checkout.PaymentID)); err != nil {
				t.Fatal(err)
			}
			assertCheckoutState(t, pool, checkout, "refunded", "refunded", 0)
			quarantineCount(t, pool, `SELECT count(*) FROM product_refund_attempts WHERE payment_id=$1 AND status='succeeded'`, 1, checkout.PaymentID)
			var cleanup jobs.Job
			if err := pool.QueryRow(ctx, `SELECT id,kind,payload FROM jobs WHERE kind=$1 AND payload->>'orderId'=$2 ORDER BY created_at DESC LIMIT 1`, productdelivery.CleanupJobKind, checkout.OrderID.String()).Scan(&cleanup.ID, &cleanup.Kind, &cleanup.Payload); err != nil {
				t.Fatal(err)
			}
			if err := productdelivery.CleanupHandler(pool, service.config.MediaStores)(ctx, cleanup); err != nil {
				t.Fatal(err)
			}
			snapshot, err := productdelivery.Load(ctx, pool, checkout.OrderID)
			if err != nil || snapshot.State != "removed" {
				t.Fatal("verified refund did not release retained snapshot", snapshot.State, err)
			}
			if scenario == "cancelled" {
				pkg, _ := runProductExport(t, pool, buyer)
				if len(pkg.Data.Marketplace.Data["closedCheckoutRecoveries"]) != 1 {
					t.Fatal("buyer export lost original recovery")
				}
				var seller uuid.UUID
				if err := pool.QueryRow(ctx, `SELECT payee_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&seller); err != nil {
					t.Fatal(err)
				}
				pkg, _ = runProductExport(t, pool, seller)
				if len(pkg.Data.Marketplace.Data["closedCheckoutRecoveries"]) != 0 {
					t.Fatal("seller received buyer recovery records")
				}
			}
		})
	}
}

func TestClosedProductCheckoutConcurrentRecovery(t *testing.T) {
	pool, service, checkout, _, check := closedCheckoutFixture(t, "cancelled")
	service.runtimes = NewRuntimeCatalog()
	var wg sync.WaitGroup
	errors := make(chan error, 6)
	for range 6 {
		wg.Go(func() { errors <- service.HandleProductCheckoutCheckJob(t.Context(), check) })
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	quarantineCount(t, pool, `SELECT count(*) FROM product_closed_checkout_recoveries WHERE payment_id=$1`, 1, checkout.PaymentID)
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_checks WHERE payment_id=$1`, 1, checkout.PaymentID)
	quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE payment_id=$1`, 1, checkout.PaymentID)
}

func TestClosedProductCheckoutCallbackBeforeRecovery(t *testing.T) {
	pool, service, checkout, _, check := closedCheckoutFixture(t, "cancelled")
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	service.verifier.now = func() time.Time { return now }
	body := strings.NewReplacer("cs_workflow123", "cs_lookup_existing", "pi_workflow123", "pi_closed_saved").Replace(string(productPaidEvent(checkout.PaymentID, checkout.ResourceID, now.Unix(), checkout.AmountCents)))
	receipt := receivePaymentWorkflowEvent(t, service, []byte(body), now)
	payload, _ := json.Marshal(map[string]string{"eventId": receipt.EventID.String()})
	callback := jobs.Job{Kind: PaymentEventJobKind, Payload: payload}
	if err := service.HandlePaymentEventJob(ctx, callback); !errors.Is(err, ErrCheckoutReconciliation) {
		t.Fatal("callback bypassed historical preflight", err)
	}
	assertCheckoutState(t, pool, checkout, "cancelled", "cancelled", 0)
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_attempts WHERE payment_id=$1`, 0, checkout.PaymentID)
	if err := service.HandleProductCheckoutCheckJob(ctx, check); err != nil {
		t.Fatal(err)
	}
	service.runtimes = NewRuntimeCatalog(&refundReadRuntime{})
	if err := service.HandleProductRefundCheckJob(ctx, closedCheckoutHistoryJob(t, pool, checkout.PaymentID)); err != nil {
		t.Fatal(err)
	}
	if err := service.HandlePaymentEventJob(ctx, callback); err != nil {
		t.Fatal("verified original callback cannot resume", err)
	}
	assertCheckoutState(t, pool, checkout, "refund_pending", "refund_requested", 0)
	var original jobs.Job
	if err := pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM product_closed_checkout_recoveries r JOIN jobs j ON j.payload->>'eventId'=r.event_id::text AND j.kind=$2 WHERE r.payment_id=$1`, checkout.PaymentID, PaymentEventJobKind).Scan(&original.ID, &original.Kind, &original.Payload); err != nil {
		t.Fatal(err)
	}
	if err := service.HandlePaymentEventJob(ctx, original); err != nil {
		t.Fatal(err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_attempts WHERE payment_id=$1`, 1, checkout.PaymentID)
	quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE kind=$2 AND payload->>'paymentId'=$1`, 1, checkout.PaymentID.String(), ProductRefundJobKind)
}

func TestClosedProductCheckoutMerchantRecoveryScheduling(t *testing.T) {
	for _, scenario := range []string{"existing", "missing", "failed"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, checkout, buyer, check := checkoutCheckFixture(t)
			ctx := t.Context()
			quarantineExec(t, pool, `TRUNCATE product_checkout_dispatches,product_checkout_requests`)
			quarantineExec(t, pool, `UPDATE payment_intents SET status='cancelled' WHERE id=$1`, checkout.PaymentID)
			quarantineExec(t, pool, `UPDATE orders SET status='cancelled' WHERE id=$1`, checkout.OrderID)
			runtime.read = func(_ context.Context, r CheckoutReadRequest) (CheckoutObservation, error) {
				return CheckoutObservation{ProviderCheckoutID: r.ProviderCheckoutID, AmountCents: r.AmountCents, Currency: r.Currency, LiveMode: r.LiveMode, Status: "complete", PaymentStatus: "paid", ProviderPaymentID: "pi_closed_merchant", ProviderChargeID: "ch_closed_merchant", IntentStatus: "succeeded", AmountReceived: r.AmountCents, ExpiresAt: time.Now().Add(time.Hour)}, nil
			}
			service.runtimes = NewRuntimeCatalog(&recoveryCheckoutRuntime{checkoutReadRuntime: runtime})
			if err := service.HandleProductIdentityRecoveryJob(ctx, identityRecoveryJob(t, pool, checkout.PaymentID, buyer)); err != nil {
				t.Fatal(err)
			}
			service.runtimes = NewRuntimeCatalog()
			if scenario == "missing" {
				quarantineExec(t, pool, `DELETE FROM jobs WHERE id=$1`, check.ID)
				if n, err := service.ReconcileProductCheckouts(ctx, 100); err != nil || n != 1 {
					t.Fatal("missing historical check not restored", n, err)
				}
				check = dispatchedCheckoutJob(t, pool, checkout.PaymentID)
				quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.check_scheduled' AND from_status='cancelled' AND to_status='cancelled'`, 1, checkout.PaymentID)
			} else if scenario == "failed" {
				quarantineExec(t, pool, `UPDATE jobs SET status='failed' WHERE id=$1`, check.ID)
				if n, err := service.ReconcileProductCheckouts(ctx, 100); err != nil || n != 0 {
					t.Fatal("scheduler bypassed failed-job recovery", n, err)
				}
				if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,$2,20) RETURNING id`, check.Kind, check.Payload).Scan(&check.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := service.HandleProductCheckoutCheckJob(ctx, check); err != nil {
				t.Fatal(err)
			}
			assertCheckoutState(t, pool, checkout, "cancelled", "cancelled", 0)
			quarantineCount(t, pool, `SELECT count(*) FROM product_refund_checks WHERE payment_id=$1`, 1, checkout.PaymentID)
			quarantineCount(t, pool, `SELECT count(*) FROM product_checkout_requests WHERE payment_id=$1`, 0, checkout.PaymentID)
		})
	}
}

func TestClosedProductCheckoutEvidenceRetentionAndMigration(t *testing.T) {
	pool, service, checkout, _, check := closedCheckoutFixture(t, "cancelled")
	ctx := t.Context()
	down, err := os.ReadFile("../platform/database/migrations/0132_product_closed_checkout_recovery.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err == nil {
		t.Fatal("rollback removed unresolved closed payment protection")
	}
	if err := service.HandleProductCheckoutCheckJob(ctx, check); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err == nil {
		t.Fatal("rollback erased preserved recovery evidence")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM product_closed_checkout_recoveries WHERE payment_id=$1`, checkout.PaymentID); err == nil {
		t.Fatal("recovery evidence was mutable")
	}
	var cleanup jobs.Job
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('orderId',$2::text),20) RETURNING id,kind,payload`, productdelivery.CleanupJobKind, checkout.OrderID).Scan(&cleanup.ID, &cleanup.Kind, &cleanup.Payload); err != nil {
		t.Fatal(err)
	}
	if err := productdelivery.CleanupHandler(pool, service.config.MediaStores)(ctx, cleanup); err != nil {
		t.Fatal(err)
	}
	snapshot, err := productdelivery.Load(ctx, pool, checkout.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	object, err := snapshot.Open(ctx, service.config.MediaStores, nil)
	if err != nil {
		t.Fatal("retained bytes unavailable", err)
	}
	body, err := io.ReadAll(object.Body)
	_ = object.Body.Close()
	if err != nil || int64(len(body)) != snapshot.Size {
		t.Fatal("retained bytes incomplete", err)
	}
	var eventID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT event_id FROM product_closed_checkout_recoveries WHERE payment_id=$1`, checkout.PaymentID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"eventId": eventID.String()})
	if err := service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: payload}); err == nil {
		t.Fatal("manual event processing bypassed refund history")
	}
	assertCheckoutState(t, pool, checkout, "cancelled", "cancelled", 0)
}
