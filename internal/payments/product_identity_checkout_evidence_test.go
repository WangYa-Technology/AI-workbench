package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func TestRecoveredPaidCheckoutUsesSavedEvidence(t *testing.T) {
	for _, scenario := range []string{"expired", "offline", "replacement_offline", "missing_check_offline", "due_now"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, checkout, buyer, check := checkoutCheckFixture(t)
			ctx := t.Context()
			quarantineExec(t, pool, `TRUNCATE product_checkout_dispatches,product_checkout_requests`)
			reads := 0
			runtime.read = func(_ context.Context, request CheckoutReadRequest) (CheckoutObservation, error) {
				reads++
				return CheckoutObservation{ProviderCheckoutID: request.ProviderCheckoutID, AmountCents: request.AmountCents, Currency: request.Currency, LiveMode: request.LiveMode, Status: "complete", PaymentStatus: "paid", ProviderPaymentID: "pi_recovered_paid", ProviderChargeID: "ch_recovered_paid", IntentStatus: "succeeded", AmountReceived: request.AmountCents, ExpiresAt: time.Now().Add(time.Hour)}, nil
			}
			service.runtimes = NewRuntimeCatalog(&recoveryCheckoutRuntime{checkoutReadRuntime: runtime})
			recovery := identityRecoveryJob(t, pool, checkout.PaymentID, buyer)
			if err := service.HandleProductIdentityRecoveryJob(ctx, recovery); err != nil {
				t.Fatal(err)
			}
			assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
			if scenario == "due_now" {
				var ready bool
				if err := pool.QueryRow(ctx, `SELECT available_at<=clock_timestamp() FROM jobs WHERE id=$1`, check.ID).Scan(&ready); err != nil {
					t.Fatal(err)
				}
				if !ready {
					t.Fatal("verified paid recovery left its existing check scheduled for future expiry")
				}
				quarantineExec(t, pool, `UPDATE payment_intents SET checkout_expires_at=now()+interval '1 hour' WHERE id=$1`, checkout.PaymentID)
			}
			if scenario == "replacement_offline" {
				quarantineExec(t, pool, `UPDATE jobs SET status='failed' WHERE id=$1`, check.ID)
				if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,$2,20) RETURNING id`, check.Kind, check.Payload).Scan(&check.ID); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "expired" {
				runtime.read = func(_ context.Context, request CheckoutReadRequest) (CheckoutObservation, error) {
					reads++
					return CheckoutObservation{ProviderCheckoutID: request.ProviderCheckoutID, AmountCents: request.AmountCents, Currency: request.Currency, LiveMode: request.LiveMode, Status: "expired", PaymentStatus: "unpaid", ExpiresAt: time.Now().Add(-time.Minute)}, nil
				}
			} else {
				service.runtimes = NewRuntimeCatalog()
			}
			if scenario == "missing_check_offline" {
				// Model historical lost scheduling, without erasing a failed task.
				quarantineExec(t, pool, `DELETE FROM jobs WHERE id=$1`, check.ID)
				quarantineExec(t, pool, `UPDATE payment_intents SET checkout_expires_at=now()+interval '1 hour',checkout_url=NULL WHERE id=$1`, checkout.PaymentID)
				assertOperationalMetric(t, pool, "problem", "checkout_check_missing", "test", 1, 0, 0)
				assertOperationalMetric(t, pool, "problem", "checkout_evidence_missing", "test", 0, 0, 0)
				if n, err := service.ReconcileProductCheckouts(ctx, 100); err != nil || n != 1 {
					t.Fatal("saved payment could not restore missing check without runtime", n, err)
				}
				check = dispatchedCheckoutJob(t, pool, checkout.PaymentID)
				if n, err := service.ReconcileProductCheckouts(ctx, 100); err != nil || n != 0 {
					t.Fatal("missing check was dispatched twice", n, err)
				}
				assertOperationalMetric(t, pool, "problem", "checkout_check_missing", "test", 0, 0, 0)
			}
			if err := service.HandleProductCheckoutCheckJob(ctx, check); err != nil {
				t.Fatal("saved recovered payment did not advance", err)
			}
			assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
			var fulfillment jobs.Job
			if err := pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM jobs j JOIN payment_provider_events e ON j.payload->>'eventId'=e.id::text
 WHERE e.payment_id=$1 AND e.checkout_job_id=$2 AND j.kind=$3`, checkout.PaymentID, check.ID, PaymentEventJobKind).Scan(&fulfillment.ID, &fulfillment.Kind, &fulfillment.Payload); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := service.HandlePaymentEventJob(ctx, fulfillment); err != nil {
					t.Fatal(err)
				}
				if err := service.HandleProductCheckoutCheckJob(ctx, check); err != nil {
					t.Fatal(err)
				}
			}
			assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
			quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.queried' AND evidence->>'identityRecoveryJobId'=$2`, 1, checkout.PaymentID, recovery.ID.String())
			quarantineCount(t, pool, `SELECT count(*) FROM product_refund_checks WHERE payment_id=$1`, 1, checkout.PaymentID)
			quarantineCount(t, pool, `SELECT count(*) FROM product_checkout_requests WHERE payment_id=$1`, 0, checkout.PaymentID)
			order, err := marketplace.NewService(pool).GetOrder(ctx, buyer, checkout.OrderID)
			if err != nil || order.CanRequestRefund || order.RefundUnavailableReason != "reconciliation_required" {
				t.Fatal("payment proof bypassed historical refund reconciliation", order, err)
			}
			if reads != 1 {
				t.Fatal("payment proof was re-fetched", reads)
			}
			if scenario == "replacement_offline" {
				pkg, body := runProductExport(t, pool, buyer)
				found := false
				for _, event := range pkg.Data.Marketplace.Data["paymentEvents"] {
					if event["identityRecoveryJobId"] == recovery.ID.String() && event["checkoutJobId"] == check.ID.String() {
						found = true
					}
				}
				if !found || len(pkg.Data.Marketplace.Data["checkoutRequests"]) != 0 || strings.Contains(string(body), "https://api.stripe.com") {
					t.Fatal("export omitted recovery lineage or fabricated an original request")
				}
				var seller uuid.UUID
				if err := pool.QueryRow(ctx, `SELECT seller_id FROM products WHERE id=$1`, checkout.ResourceID).Scan(&seller); err != nil {
					t.Fatal(err)
				}
				_, sellerBody := runProductExport(t, pool, seller)
				if strings.Contains(string(sellerBody), recovery.ID.String()) {
					t.Fatal("seller export exposed buyer recovery evidence")
				}
			}
		})
	}
}

func TestRecoveredPaidCheckoutAuthenticatedRestart(t *testing.T) {
	pool, service, _, checkout, buyer, check := checkoutCheckFixture(t)
	ctx := t.Context()
	quarantineExec(t, pool, `TRUNCATE product_checkout_dispatches,product_checkout_requests`)
	binding, err := readProductPaymentBinding(ctx, pool, checkout.PaymentID, false)
	if err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int32
	metadata := map[string]string{"hcai_payment_id": checkout.PaymentID.String(), "hcai_resource_id": checkout.ResourceID.String(), "hcai_purpose": "product"}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer recovered-checkout-fixture" || r.Header.Get("Stripe-Version") != testStripeAPIVersion {
			t.Error("recovery sent an unauthenticated read or a financial command")
			http.Error(w, "rejected", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/account":
			fmt.Fprint(w, `{"id":"acct_workflow","object":"account"}`)
		case "/balance":
			fmt.Fprint(w, `{"object":"balance","livemode":false}`)
		case "/checkout/sessions/" + binding.ProviderCheckoutID:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": binding.ProviderCheckoutID, "object": "checkout.session", "mode": "payment", "status": "complete", "payment_status": "paid", "payment_intent": "pi_recovery_verified", "amount_total": checkout.AmountCents, "currency": "usd", "livemode": false, "expires_at": time.Now().Add(time.Hour).Unix(), "client_reference_id": checkout.PaymentID.String(), "metadata": metadata})
		case "/payment_intents/pi_recovery_verified":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "pi_recovery_verified", "object": "payment_intent", "status": "succeeded", "amount": checkout.AmountCents, "amount_received": checkout.AmountCents, "currency": "usd", "livemode": false, "latest_charge": "ch_recovery_verified", "metadata": metadata})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	service.runtimes = NewRuntimeCatalog(originalMerchantHTTPRuntime(t, upstream, "recovered-checkout-fixture"))
	recovery := identityRecoveryJob(t, pool, checkout.PaymentID, buyer)
	if err := service.HandleProductIdentityRecoveryJob(ctx, recovery); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 4 {
		t.Fatal("fixture missed authenticated account/session/payment reads", reads.Load())
	}
	for _, migrationName := range []string{"0134_product_closed_refund_reconciliation.down.sql", "0133_product_closed_checkout_refund_confirmation.down.sql", "0132_product_closed_checkout_recovery.down.sql", "0131_product_checkout_evidence_conflicts.down.sql", "0130_product_checkout_session_evidence.down.sql", "0130_product_checkout_session_evidence.up.sql", "0131_product_checkout_evidence_conflicts.up.sql", "0132_product_closed_checkout_recovery.up.sql", "0133_product_closed_checkout_refund_confirmation.up.sql", "0134_product_closed_refund_reconciliation.up.sql"} {
		body, err := os.ReadFile("../platform/database/migrations/" + migrationName)
		if err != nil {
			t.Fatal(err)
		}
		quarantineExec(t, pool, string(body))
		quarantineCount(t, pool, `SELECT count(*) FROM product_payment_identity_recoveries WHERE payment_id=$1`, 1, checkout.PaymentID)
	}
	upstream.Close()
	restarted := newPaymentTestService(t, pool, service.config, NewRuntimeCatalog())
	if err := restarted.HandleProductCheckoutCheckJob(ctx, check); err != nil {
		t.Fatal(err)
	}
	var fulfillment jobs.Job
	if err := pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM jobs j JOIN payment_provider_events e ON j.payload->>'eventId'=e.id::text WHERE e.checkout_job_id=$1 AND j.kind=$2`, check.ID, PaymentEventJobKind).Scan(&fulfillment.ID, &fulfillment.Kind, &fulfillment.Payload); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := restarted.HandlePaymentEventJob(ctx, fulfillment); err != nil {
			t.Fatal(err)
		}
	}
	assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
	quarantineCount(t, pool, `SELECT count(*) FROM product_refund_checks WHERE payment_id=$1`, 1, checkout.PaymentID)
	quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND evidence->>'identityRecoveryJobId'=$2`, 1, checkout.PaymentID, recovery.ID.String())
}

func TestRecoveredPaidCheckoutCrossSourceAgreement(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprint(conflict), func(t *testing.T) {
			pool, service, runtime, checkout, buyer, lookup := pendingCheckoutLookupFixture(t)
			ctx := t.Context()
			runtime.observation.Status, runtime.observation.PaymentStatus, runtime.observation.IntentStatus = "complete", "paid", "succeeded"
			runtime.observation.ProviderPaymentID, runtime.observation.ProviderChargeID, runtime.observation.AmountReceived = "pi_cross_source", "ch_cross_source", checkout.AmountCents
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
				observation.ProviderChargeID = "ch_different_source"
			}
			identityBody, _ := json.Marshal(identity)
			bindingBody, _ := json.Marshal(binding)
			observationBody, _ := json.Marshal(ProductPaymentIdentityObservation{Checkout: &observation})
			// Exercise coexisting restored historical evidence. Runtime recovery
			// does not manufacture a new request when its original is missing.
			quarantineExec(t, pool, `INSERT INTO product_payment_identity_recoveries(payment_id,job_id,requested_by,identity,binding,observation) VALUES($1,$2,$3,$4,$5,$6)`, checkout.PaymentID, recovery.ID, buyer, identityBody, bindingBody, observationBody)
			service.runtimes = NewRuntimeCatalog()
			err = service.HandleProductCheckoutCheckJob(ctx, check)
			if conflict {
				if err == nil || err.Error() != "payment_reconciliation_required" {
					t.Fatal("conflicting receipt sources were accepted", err)
				}
				quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE payment_id=$1`, 0, checkout.PaymentID)
			} else if err != nil {
				t.Fatal(err)
			} else {
				quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND evidence->>'lookupJobId'=$2 AND evidence->>'identityRecoveryJobId'=$3`, 1, checkout.PaymentID, lookup.ID.String(), recovery.ID.String())
				quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE payment_id=$1`, 1, checkout.PaymentID)
			}
			assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
			if runtime.checkoutReads.Load() != 0 {
				t.Fatal("historical receipts re-fetched")
			}
		})
	}
}

func TestRecoveredPaidCheckoutRejectsChangedEvidence(t *testing.T) {
	for _, scenario := range []string{"amount", "mode", "buyer", "session", "payment", "charge", "binding", "identity_mode", "invalid_observation", "mixed_observation"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, checkout, buyer, check := checkoutCheckFixture(t)
			ctx := t.Context()
			quarantineExec(t, pool, `TRUNCATE product_checkout_dispatches,product_checkout_requests`)
			runtime.read = func(_ context.Context, request CheckoutReadRequest) (CheckoutObservation, error) {
				return CheckoutObservation{ProviderCheckoutID: request.ProviderCheckoutID, AmountCents: request.AmountCents, Currency: request.Currency, LiveMode: request.LiveMode, Status: "complete", PaymentStatus: "paid", ProviderPaymentID: "pi_recovered_paid", ProviderChargeID: "ch_recovered_paid", IntentStatus: "succeeded", AmountReceived: request.AmountCents, ExpiresAt: time.Now().Add(time.Hour)}, nil
			}
			service.runtimes = NewRuntimeCatalog(&recoveryCheckoutRuntime{checkoutReadRuntime: runtime})
			if err := service.HandleProductIdentityRecoveryJob(ctx, identityRecoveryJob(t, pool, checkout.PaymentID, buyer)); err != nil {
				t.Fatal(err)
			}
			runtime.read = func(_ context.Context, _ CheckoutReadRequest) (CheckoutObservation, error) {
				t.Error("invalid positive evidence fell back to another provider read")
				return CheckoutObservation{}, nil
			}
			switch scenario {
			case "amount":
				quarantineExec(t, pool, `UPDATE payment_intents SET amount_cents=amount_cents+100 WHERE id=$1`, checkout.PaymentID)
			case "mode":
				quarantineExec(t, pool, `UPDATE payment_intents SET live_mode=NOT live_mode WHERE id=$1`, checkout.PaymentID)
			case "buyer":
				quarantineExec(t, pool, `UPDATE payment_intents SET payer_id=(SELECT seller_id FROM products WHERE id=$2) WHERE id=$1`, checkout.PaymentID, checkout.ResourceID)
			case "session":
				quarantineExec(t, pool, `UPDATE payment_intents SET provider_checkout_id='cs_other' WHERE id=$1`, checkout.PaymentID)
			case "payment":
				quarantineExec(t, pool, `UPDATE payment_intents SET provider_payment_id='pi_other' WHERE id=$1`, checkout.PaymentID)
			case "charge":
				quarantineExec(t, pool, `UPDATE payment_intents SET provider_charge_id='ch_other' WHERE id=$1`, checkout.PaymentID)
			default:
				// Seed malformed historical evidence only in this disposable schema.
				// Production recovery records remain append-only.
				identity, binding, observation := "identity", "binding", "observation"
				switch scenario {
				case "binding":
					binding = `jsonb_set(binding,'{resourceId}',to_jsonb(requested_by::text))`
				case "identity_mode":
					identity = `jsonb_set(identity,'{liveMode}','true'::jsonb)`
				case "invalid_observation":
					observation = `jsonb_set(observation,'{checkout,providerChargeId}','""'::jsonb)`
				case "mixed_observation":
					observation = `observation || '{"payment":{}}'::jsonb`
				}
				quarantineExec(t, pool, `CREATE TEMP TABLE recovery_fixture_copy AS SELECT * FROM product_payment_identity_recoveries;
 TRUNCATE product_payment_identity_recoveries;
 INSERT INTO product_payment_identity_recoveries(payment_id,job_id,requested_by,identity,binding,observation,created_at)
 SELECT payment_id,job_id,requested_by,`+identity+`,`+binding+`,`+observation+`,created_at FROM recovery_fixture_copy;
 DROP TABLE recovery_fixture_copy`)
			}
			if err := service.HandleProductCheckoutCheckJob(ctx, check); err == nil || err.Error() != "payment_reconciliation_required" {
				t.Fatal("expected reconciliation rejection", err)
			}
			assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
			quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE payment_id=$1`, 0, checkout.PaymentID)
			quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.expired_verified'`, 0, checkout.PaymentID)
		})
	}
}

func TestRecoveredUnpaidCheckoutStillReadsProvider(t *testing.T) {
	for _, initial := range []string{"open", "expired"} {
		t.Run(initial, func(t *testing.T) {
			pool, service, runtime, checkout, buyer, check := checkoutCheckFixture(t)
			ctx := t.Context()
			quarantineExec(t, pool, `TRUNCATE product_checkout_dispatches,product_checkout_requests`)
			reads := 0
			runtime.read = func(_ context.Context, request CheckoutReadRequest) (CheckoutObservation, error) {
				reads++
				return CheckoutObservation{ProviderCheckoutID: request.ProviderCheckoutID, AmountCents: request.AmountCents, Currency: request.Currency, LiveMode: request.LiveMode, Status: initial, PaymentStatus: "unpaid", ExpiresAt: time.Now().Add(-time.Minute)}, nil
			}
			service.runtimes = NewRuntimeCatalog(&recoveryCheckoutRuntime{checkoutReadRuntime: runtime})
			if err := service.HandleProductIdentityRecoveryJob(ctx, identityRecoveryJob(t, pool, checkout.PaymentID, buyer)); err != nil {
				t.Fatal(err)
			}
			if initial == "expired" {
				// Provider expiry can precede the originally planned local expiry.
				quarantineExec(t, pool, `UPDATE payment_intents SET checkout_expires_at=now()+interval '1 hour' WHERE id=$1`, checkout.PaymentID)
			}
			runtime.read = func(_ context.Context, request CheckoutReadRequest) (CheckoutObservation, error) {
				reads++
				return CheckoutObservation{ProviderCheckoutID: request.ProviderCheckoutID, AmountCents: request.AmountCents, Currency: request.Currency, LiveMode: request.LiveMode, Status: "complete", PaymentStatus: "paid", ProviderPaymentID: "pi_recovered_later_paid", ProviderChargeID: "ch_recovered_later_paid", IntentStatus: "succeeded", AmountReceived: request.AmountCents, ExpiresAt: time.Now().Add(time.Hour)}, nil
			}
			if err := service.HandleProductCheckoutCheckJob(ctx, check); err != nil {
				t.Fatal(err)
			}
			if reads != 2 {
				t.Fatal("unpaid history replaced fresh authenticated payment read", reads)
			}
			quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE checkout_job_id=$1 AND payment_status='paid'`, 1, check.ID)
			quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND evidence ? 'identityRecoveryJobId'`, 0, checkout.PaymentID)
			assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
		})
	}
}
