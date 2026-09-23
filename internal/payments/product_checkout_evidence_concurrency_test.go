package payments

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
)

func TestProductCheckoutPaidQueryWithConcurrentLookup(t *testing.T) {
	for _, name := range []string{"matching", "conflicting_charge", "closed_with_matching_recovery"} {
		conflict := name == "conflicting_charge"
		t.Run(name, func(t *testing.T) {
			pool, service, runtime, checkout, _, first := pendingCheckoutLookupFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			open := runtime.observation
			paid := open
			paid.Status, paid.PaymentStatus, paid.IntentStatus = "complete", "paid", "succeeded"
			paid.ProviderPaymentID, paid.ProviderChargeID, paid.AmountReceived = "pi_concurrent_paid", "ch_concurrent_paid", checkout.AmountCents
			runtime.lookup = func(ctx context.Context, _ CheckoutLookupRequest) (CheckoutLookupResult, error) {
				observation := open
				if runtime.reads.Load() == 1 {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return CheckoutLookupResult{}, ctx.Err()
					}
					observation = paid
				}
				return CheckoutLookupResult{Outcome: "found", Pages: 1, Scanned: 1, Matches: []string{observation.ProviderCheckoutID}, Observation: &observation}, nil
			}
			lookupDone := make(chan error, 1)
			go func() { lookupDone <- service.HandleProductCheckoutLookupJob(ctx, first) }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			quarantineExec(t, pool, `UPDATE jobs SET status='failed' WHERE id=$1`, first.ID)
			second := first
			if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,$2,20) RETURNING id`, second.Kind, second.Payload).Scan(&second.ID); err != nil {
				t.Fatal(err)
			}
			if err := service.HandleProductCheckoutLookupJob(ctx, second); err != nil {
				t.Fatal(err)
			}
			check := locatedCheckJob(t, pool, second.ID)
			quarantineExec(t, pool, `UPDATE payment_intents SET checkout_expires_at=now()-interval '1 minute' WHERE id=$1`, checkout.PaymentID)
			service.runtimes = NewRuntimeCatalog(&delayedPaidLookupCheckRuntime{lookupRuntime: runtime, read: func(ctx context.Context, _ CheckoutReadRequest) (CheckoutObservation, error) {
				// The query began with only an open-session receipt. A still-running
				// authenticated locator commits its paid observation before this read ends.
				unblock()
				select {
				case err := <-lookupDone:
					if err != nil {
						return CheckoutObservation{}, err
					}
				case <-ctx.Done():
					return CheckoutObservation{}, ctx.Err()
				}
				if name == "closed_with_matching_recovery" {
					quarantineExec(t, pool, `UPDATE payment_intents SET status='cancelled' WHERE id=$1`, checkout.PaymentID)
					quarantineExec(t, pool, `UPDATE orders SET status='cancelled' WHERE id=$1`, checkout.OrderID)
				}
				result := paid
				result.ExpiresAt = result.ExpiresAt.Add(time.Second)
				if conflict {
					result.ProviderChargeID = "ch_concurrent_other"
				}
				return result, nil
			}})
			err := service.HandleProductCheckoutCheckJob(ctx, check)
			if conflict {
				if !errors.Is(err, ErrCheckoutReconciliation) {
					t.Fatal("conflicting paid observations did not stop fulfillment", err)
				}
				quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE payment_id=$1`, 0, checkout.PaymentID)
				quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.queried' AND evidence->'observation'->>'providerChargeId'='ch_concurrent_other'`, 1, checkout.PaymentID)
				service.runtimes = NewRuntimeCatalog()
				for range 2 {
					if err := service.HandleProductCheckoutCheckJob(ctx, check); !errors.Is(err, ErrCheckoutReconciliation) {
						t.Fatal("retry forgot a recorded payment conflict", err)
					}
				}
				quarantineCount(t, pool, `SELECT count(*) FROM product_checkout_evidence_conflicts WHERE payment_id=$1`, 1, checkout.PaymentID)
				quarantineCount(t, pool, `SELECT count(*) FROM product_refund_review WHERE payment_id=$1`, 1, checkout.PaymentID)
				assertOperationalMetric(t, pool, "problem", "checkout_evidence_conflict", "test", 1, 0, 0)
				assertOperationalMetric(t, pool, "problem", "checkout_evidence_conflict", "live", 0, 0, 0)
				quarantineCount(t, pool, `SELECT count(*) FROM product_order_funds_retention WHERE order_id=$1`, 1, checkout.OrderID)
				assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND evidence->>'lookupJobId'=$2`, 1, checkout.PaymentID, first.ID.String())
				if name == "closed_with_matching_recovery" {
					assertCheckoutState(t, pool, checkout, "cancelled", "cancelled", 0)
					quarantineCount(t, pool, `SELECT count(*) FROM product_closed_checkout_recoveries WHERE payment_id=$1`, 1, checkout.PaymentID)
					quarantineCount(t, pool, `SELECT count(*) FROM product_refund_checks WHERE payment_id=$1`, 1, checkout.PaymentID)
					quarantineCount(t, pool, `SELECT count(*) FROM product_refund_attempts WHERE payment_id=$1`, 0, checkout.PaymentID)
					return
				}
				var fulfillment jobs.Job
				if err := pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM jobs j JOIN payment_provider_events e ON j.payload->>'eventId'=e.id::text WHERE e.checkout_job_id=$1 AND j.kind=$2`, check.ID, PaymentEventJobKind).Scan(&fulfillment.ID, &fulfillment.Kind, &fulfillment.Payload); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					if err := service.HandlePaymentEventJob(ctx, fulfillment); err != nil {
						t.Fatal(err)
					}
				}
				assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
			}
			if runtime.checkoutReads.Load() != 1 || runtime.reads.Load() != 2 {
				t.Fatal("unexpected repeated reads")
			}
		})
	}
}

func TestProductCheckoutPaidSourceAddedWhileWaiting(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		name := "matching"
		if conflict {
			name = "conflicting_charge"
		}
		t.Run(name, func(t *testing.T) {
			pool, service, runtime, checkout, buyer, lookup := pendingCheckoutLookupFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			runtime.observation.Status, runtime.observation.PaymentStatus, runtime.observation.IntentStatus = "complete", "paid", "succeeded"
			runtime.observation.ProviderPaymentID, runtime.observation.ProviderChargeID, runtime.observation.AmountReceived = "pi_waiting_paid", "ch_waiting_paid", checkout.AmountCents
			if err := service.HandleProductCheckoutLookupJob(ctx, lookup); err != nil {
				t.Fatal(err)
			}
			check := locatedCheckJob(t, pool, lookup.ID)
			recovery := identityRecoveryJob(t, pool, checkout.PaymentID, buyer)
			identity, err := runtime.ProductCheckoutIdentity(ctx)
			if err != nil {
				t.Fatal(err)
			}
			binding, err := readProductPaymentBinding(ctx, pool, checkout.PaymentID, false)
			if err != nil {
				t.Fatal(err)
			}
			observation := runtime.observation
			observation.ExpiresAt = observation.ExpiresAt.Add(time.Second)
			if conflict {
				observation.ProviderChargeID = "ch_waiting_other"
			}
			identityBody, _ := json.Marshal(identity)
			bindingBody, _ := json.Marshal(binding)
			observationBody, _ := json.Marshal(ProductPaymentIdentityObservation{Checkout: &observation})
			gate, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Rollback(context.Background())
			if _, err := gate.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, checkout.PaymentID); err != nil {
				t.Fatal(err)
			}
			service.runtimes = NewRuntimeCatalog()
			done := make(chan error, 1)
			go func() { done <- service.HandleProductCheckoutCheckJob(ctx, check) }()
			waitForProductBlockingTx(t, ctx, pool, int32(gate.Conn().PgConn().PID()))
			// Coexisting historical recovery evidence becomes visible after the first
			// local read. The production immutable receipt is neither edited nor erased.
			if _, err := gate.Exec(ctx, `INSERT INTO product_payment_identity_recoveries(payment_id,job_id,requested_by,identity,binding,observation) VALUES($1,$2,$3,$4,$5,$6)`, checkout.PaymentID, recovery.ID, buyer, identityBody, bindingBody, observationBody); err != nil {
				t.Fatal(err)
			}
			if err := gate.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			err = <-done
			if conflict {
				if !errors.Is(err, ErrCheckoutReconciliation) {
					t.Fatal("conflicting source was accepted", err)
				}
				quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE payment_id=$1`, 0, checkout.PaymentID)
			} else {
				if err != nil {
					t.Fatal("consistent additional evidence stalled the current check", err)
				}
				quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND evidence->>'lookupJobId'=$2 AND evidence->>'identityRecoveryJobId'=$3`, 1, checkout.PaymentID, lookup.ID.String(), recovery.ID.String())
				quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE payment_id=$1`, 1, checkout.PaymentID)
				for _, migrationName := range []string{"0134_product_closed_refund_reconciliation.down.sql", "0133_product_closed_checkout_refund_confirmation.down.sql", "0132_product_closed_checkout_recovery.down.sql", "0131_product_checkout_evidence_conflicts.down.sql", "0131_product_checkout_evidence_conflicts.up.sql", "0132_product_closed_checkout_recovery.up.sql", "0133_product_closed_checkout_refund_confirmation.up.sql", "0134_product_closed_refund_reconciliation.up.sql"} {
					migration, err := os.ReadFile("../platform/database/migrations/" + migrationName)
					if err != nil {
						t.Fatal(err)
					}
					quarantineExec(t, pool, string(migration))
					quarantineCount(t, pool, `SELECT count(*) FROM product_payment_identity_recoveries WHERE payment_id=$1`, 1, checkout.PaymentID)
					quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.queried'`, 1, checkout.PaymentID)
				}
				quarantineCount(t, pool, `SELECT count(*) FROM product_checkout_evidence_conflicts WHERE payment_id=$1`, 0, checkout.PaymentID)
			}
			assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
		})
	}
}

func TestProductCheckoutConflictingQueryProtectsQueuedAndFulfilledOrder(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		name := "queued"
		if delivered {
			name = "fulfilled"
		}
		t.Run(name, func(t *testing.T) {
			pool, service, runtime, checkout, buyer, first := checkoutCheckFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			entered := make(chan int, 2)
			release := []chan struct{}{make(chan struct{}), make(chan struct{})}
			var once [2]sync.Once
			unblock := func(n int) { once[n].Do(func() { close(release[n]) }) }
			defer unblock(0)
			defer unblock(1)
			var reads atomic.Int32
			runtime.read = func(ctx context.Context, r CheckoutReadRequest) (CheckoutObservation, error) {
				n := int(reads.Add(1)) - 1
				if n > 1 {
					return CheckoutObservation{}, errors.New("unexpected repeated remote read")
				}
				entered <- n
				select {
				case <-release[n]:
				case <-ctx.Done():
					return CheckoutObservation{}, ctx.Err()
				}
				charge := "ch_queued_first"
				if n == 1 {
					charge = "ch_queued_second"
				}
				return CheckoutObservation{ProviderCheckoutID: r.ProviderCheckoutID, Status: "complete", PaymentStatus: "paid", ProviderPaymentID: "pi_queued_paid", ProviderChargeID: charge, IntentStatus: "succeeded", AmountReceived: r.AmountCents, AmountCents: r.AmountCents, Currency: r.Currency, LiveMode: r.LiveMode, ExpiresAt: time.Now().Add(time.Hour)}, nil
			}
			done := []chan error{make(chan error, 1), make(chan error, 1)}
			go func() { done[0] <- service.HandleProductCheckoutCheckJob(ctx, first) }()
			select {
			case n := <-entered:
				if n != 0 {
					t.Fatal(n)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			quarantineExec(t, pool, `UPDATE jobs SET status='failed' WHERE id=$1`, first.ID)
			second := first
			if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,$2,20) RETURNING id`, first.Kind, first.Payload).Scan(&second.ID); err != nil {
				t.Fatal(err)
			}
			go func() { done[1] <- service.HandleProductCheckoutCheckJob(ctx, second) }()
			select {
			case n := <-entered:
				if n != 1 {
					t.Fatal(n)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			unblock(0)
			if err := <-done[0]; err != nil {
				t.Fatal(err)
			}
			var fulfillment jobs.Job
			if err := pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM jobs j JOIN payment_provider_events e ON j.payload->>'eventId'=e.id::text WHERE e.checkout_job_id=$1 AND j.kind=$2`, first.ID, PaymentEventJobKind).Scan(&fulfillment.ID, &fulfillment.Kind, &fulfillment.Payload); err != nil {
				t.Fatal(err)
			}
			if delivered {
				if err := service.HandlePaymentEventJob(ctx, fulfillment); err != nil {
					t.Fatal(err)
				}
				assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
			}
			unblock(1)
			if err := <-done[1]; !errors.Is(err, ErrCheckoutReconciliation) {
				t.Fatal("second paid query bypassed conflict review", err)
			}
			quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.queried'`, 2, checkout.PaymentID)
			quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE payment_id=$1`, 1, checkout.PaymentID)
			quarantineCount(t, pool, `SELECT count(*) FROM product_checkout_evidence_conflicts WHERE payment_id=$1`, 1, checkout.PaymentID)
			quarantineCount(t, pool, `SELECT count(*) FROM product_refund_review WHERE payment_id=$1`, 1, checkout.PaymentID)
			assertOperationalMetric(t, pool, "problem", "checkout_evidence_conflict", "test", 1, 0, 0)
			assertOperationalMetric(t, pool, "problem", "checkout_evidence_conflict", "live", 0, 0, 0)
			down, err := os.ReadFile("../platform/database/migrations/0131_product_checkout_evidence_conflicts.down.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot remove checkout evidence conflict protection") {
				t.Fatal("downgrade discarded the conflict gate", err)
			}
			if !delivered {
				for range 2 {
					if err := service.HandlePaymentEventJob(ctx, fulfillment); !errors.Is(err, ErrCheckoutReconciliation) {
						t.Fatal("queued fulfillment ignored new payment conflict", err)
					}
				}
				assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
			} else {
				order, err := marketplace.NewService(pool).GetOrder(ctx, buyer, checkout.OrderID)
				if err != nil || order.CanRequestRefund || order.RefundUnavailableReason != "reconciliation_required" {
					t.Fatal("conflicting payment still allowed ordinary refund", order, err)
				}
				// Remove the separate active-rights reason for retaining the copy. The
				// shared financial conflict must independently protect actual bytes.
				quarantineExec(t, pool, `UPDATE entitlements SET status='revoked',revoked_at=now() WHERE order_id=$1`, checkout.OrderID)
				snapshot, err := productdelivery.Load(ctx, pool, checkout.OrderID)
				if err != nil {
					t.Fatal(err)
				}
				var cleanup jobs.Job
				if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('orderId',$2::text),20) RETURNING id,kind,payload`, productdelivery.CleanupJobKind, checkout.OrderID).Scan(&cleanup.ID, &cleanup.Kind, &cleanup.Payload); err != nil {
					t.Fatal(err)
				}
				if err := productdelivery.CleanupHandler(pool, service.config.MediaStores)(ctx, cleanup); err != nil {
					t.Fatal(err)
				}
				kept, err := productdelivery.Load(ctx, pool, checkout.OrderID)
				if err != nil || kept.State != "ready" || kept.SHA256 != snapshot.SHA256 {
					t.Fatal("financial review lost its delivery copy", kept, err)
				}
				store, err := service.config.MediaStores.Get(kept.Backend)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.Stat(ctx, kept.Key); err != nil {
					t.Fatal("retained copy bytes were deleted", err)
				}
			}
			if reads.Load() != 2 {
				t.Fatal("unexpected query replay", reads.Load())
			}
		})
	}
}
