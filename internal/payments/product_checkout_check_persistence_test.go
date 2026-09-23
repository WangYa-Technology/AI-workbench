package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

// Cancel only after the real runtime has authenticated and validated both GETs.
type cancelAfterCheckoutRead struct {
	*StripeRuntime
	cancel context.CancelFunc
}

func (r *cancelAfterCheckoutRead) ReadProductCheckout(ctx context.Context, request CheckoutReadRequest) (CheckoutObservation, error) {
	observation, err := r.StripeRuntime.ReadProductCheckout(ctx, request)
	r.cancel()
	return observation, err
}

func TestProductCheckoutCheckPaidEvidenceSurvivesCancellation(t *testing.T) {
	pool, service, _, checkout, _, queued := checkoutCheckFixture(t)
	ctx := t.Context()
	var session string
	if err := pool.QueryRow(ctx, `SELECT provider_checkout_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&session); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer local-checkout-cancellation" {
			t.Error("unexpected financial dispatch or unauthenticated read")
			http.Error(w, "rejected", http.StatusForbidden)
			return
		}
		metadata := map[string]string{"hcai_payment_id": checkout.PaymentID.String(), "hcai_resource_id": checkout.ResourceID.String(), "hcai_purpose": "product"}
		switch r.URL.Path {
		case "/account":
			fmt.Fprint(w, `{"id":"acct_workflow","object":"account"}`)
		case "/balance":
			fmt.Fprint(w, `{"object":"balance","livemode":false}`)
		case "/checkout/sessions/" + session:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": session, "object": "checkout.session", "mode": "payment", "status": "complete", "payment_status": "paid", "payment_intent": "pi_workflow123", "amount_total": checkout.AmountCents, "currency": "usd", "livemode": false, "expires_at": time.Now().Add(-time.Minute).Unix(), "client_reference_id": checkout.PaymentID.String(), "metadata": metadata})
		case "/payment_intents/pi_workflow123":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "pi_workflow123", "object": "payment_intent", "status": "succeeded", "amount": checkout.AmountCents, "amount_received": checkout.AmountCents, "currency": "usd", "livemode": false, "latest_charge": "ch_workflow123", "metadata": metadata})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	workerCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	service.runtimes = NewRuntimeCatalog(&cancelAfterCheckoutRead{StripeRuntime: originalMerchantHTTPRuntime(t, upstream, "local-checkout-cancellation"), cancel: stopWorker})
	repo := jobs.NewRepository(pool)
	quarantineExec(t, pool, `UPDATE jobs SET available_at=now()-interval '1 day' WHERE id=$1`, queued.ID)
	job, err := repo.Claim(ctx, "checkout-stopping", time.Minute)
	if err != nil || job.ID != queued.ID {
		t.Fatal(job, err)
	}
	if err = service.HandleProductCheckoutCheckJob(workerCtx, job); err != nil {
		t.Fatal("validated payment lost during worker shutdown", err)
	}
	if workerCtx.Err() == nil || calls.Load() != 4 {
		t.Fatal("fixture did not cancel after authenticated reads", calls.Load())
	}
	assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
	quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE checkout_job_id=$1 AND payment_status='paid' AND evidence_source='provider_query'`, 1, job.ID)
	quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.queried'`, 1, checkout.PaymentID)
	if err = repo.Complete(workerCtx, job, "checkout-stopping"); err == nil {
		t.Fatal("stopped worker unexpectedly completed its lease")
	}
	// Reclaim the real job. The replacement must use durable evidence and its
	// existing fulfillment job, without another remote read or duplicate rights.
	quarantineExec(t, pool, `UPDATE jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID)
	if err = repo.RecoverExpired(ctx); err != nil {
		t.Fatal(err)
	}
	quarantineExec(t, pool, `UPDATE jobs SET available_at=now()-interval '1 day' WHERE id=$1`, job.ID)
	replacement, err := repo.Claim(ctx, "checkout-restarted", time.Minute)
	if err != nil || replacement.ID != job.ID || replacement.LeaseToken == job.LeaseToken {
		t.Fatal(replacement, err)
	}
	restarted := newPaymentTestService(t, pool, service.config, service.runtimes)
	if err = restarted.HandleProductCheckoutCheckJob(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	if err = repo.Complete(ctx, replacement, "checkout-restarted"); err != nil {
		t.Fatal(err)
	}
	fulfillment, err := repo.Claim(ctx, "checkout-fulfillment", time.Minute)
	if err != nil || fulfillment.Kind != PaymentEventJobKind {
		t.Fatal(fulfillment, err)
	}
	for range 2 {
		if err = restarted.HandlePaymentEventJob(ctx, fulfillment); err != nil {
			t.Fatal(err)
		}
	}
	if err = repo.Complete(ctx, fulfillment, "checkout-fulfillment"); err != nil {
		t.Fatal(err)
	}
	assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
	quarantineCount(t, pool, `SELECT count(*) FROM jobs j JOIN payment_provider_events e ON j.payload->>'eventId'=e.id::text WHERE e.checkout_job_id=$1 AND j.kind=$2`, 1, job.ID, PaymentEventJobKind)
	if calls.Load() != 4 {
		t.Fatal("restart refetched paid evidence", calls.Load())
	}
}

func TestProductCheckoutLateExpiryCannotOverrideQueuedPaidEvidence(t *testing.T) {
	pool, service, runtime, checkout, buyer, queued := checkoutCheckFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	var calls atomic.Int32
	runtime.read = func(readCtx context.Context, request CheckoutReadRequest) (CheckoutObservation, error) {
		observation := CheckoutObservation{ProviderCheckoutID: request.ProviderCheckoutID, AmountCents: request.AmountCents, Currency: request.Currency, LiveMode: request.LiveMode, Status: "expired", PaymentStatus: "unpaid", ExpiresAt: time.Now().Add(-time.Minute)}
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-readCtx.Done():
				return CheckoutObservation{}, readCtx.Err()
			}
			return observation, nil
		}
		observation.Status, observation.PaymentStatus, observation.IntentStatus = "complete", "paid", "succeeded"
		observation.ProviderPaymentID, observation.ProviderChargeID, observation.AmountReceived = "pi_workflow123", "ch_workflow123", request.AmountCents
		return observation, nil
	}
	repo := jobs.NewRepository(pool)
	quarantineExec(t, pool, `UPDATE jobs SET available_at=now()-interval '1 day' WHERE id=$1`, queued.ID)
	old, err := repo.Claim(ctx, "checkout-old-query", time.Minute)
	if err != nil || old.ID != queued.ID {
		t.Fatal(old, err)
	}
	done := make(chan error, 1)
	go func() { done <- service.HandleProductCheckoutCheckJob(ctx, old) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	quarantineExec(t, pool, `UPDATE jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, old.ID)
	if err = repo.RecoverExpired(ctx); err != nil {
		t.Fatal(err)
	}
	replacement, err := repo.Claim(ctx, "checkout-paid-query", time.Minute)
	if err != nil || replacement.ID != old.ID || replacement.LeaseToken == old.LeaseToken {
		t.Fatal(replacement, err)
	}
	if err = service.HandleProductCheckoutCheckJob(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	if err = repo.Complete(ctx, replacement, "checkout-paid-query"); err != nil {
		t.Fatal(err)
	}
	// Deliberately leave the paid event queued, as on a busy fulfillment worker.
	unblock()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
	if _, _, err = service.BeginProductCheckout(ctx, buyer, checkout.ResourceID, "late-expiry-new-checkout", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, checkout.ResourceID)); !errors.Is(err, ErrCheckoutConflict) {
		t.Fatal("late expiry reopened a paid checkout slot", err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.queried'`, 2, checkout.PaymentID)
	quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.expired_verified'`, 0, checkout.PaymentID)
	var eventID uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT id FROM payment_provider_events WHERE checkout_job_id=$1 AND payment_status='paid'`, old.ID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	fulfillment, err := repo.Claim(ctx, "checkout-late-expiry-fulfillment", time.Minute)
	if err != nil || fulfillment.Kind != PaymentEventJobKind {
		t.Fatal(fulfillment, err)
	}
	if err = service.HandlePaymentEventJob(ctx, fulfillment); err != nil {
		t.Fatal(err)
	}
	if err = repo.Complete(ctx, fulfillment, "checkout-late-expiry-fulfillment"); err != nil {
		t.Fatal(err)
	}
	assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
	quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE kind=$1 AND payload->>'paymentId'=$2`, 0, ProductRefundJobKind, checkout.PaymentID.String())
}

func TestProductCheckoutExpiryCannotOverrideQueuedSignedPayment(t *testing.T) {
	for _, kind := range []string{"checkout.session.completed", "checkout.session.async_payment_succeeded", "payment_intent.succeeded"} {
		t.Run(kind, func(t *testing.T) { testCheckoutExpiryWithQueuedSignedPayment(t, kind) })
	}
}

func testCheckoutExpiryWithQueuedSignedPayment(t *testing.T, kind string) {
	pool, service, runtime, checkout, _, job := checkoutCheckFixture(t)
	ctx := t.Context()
	var eventID uuid.UUID
	runtime.read = func(_ context.Context, request CheckoutReadRequest) (CheckoutObservation, error) {
		// Receive a real verified webhook while the query is in flight; its
		// fulfillment job remains queued when the old unpaid response arrives.
		now := time.Now().UTC().Truncate(time.Second)
		service.verifier.now = func() time.Time { return now }
		body := strings.ReplaceAll(string(productPaidEvent(checkout.PaymentID, checkout.ResourceID, now.Unix(), checkout.AmountCents)), "cs_workflow123", request.ProviderCheckoutID)
		var event map[string]any
		if err := json.Unmarshal([]byte(body), &event); err != nil {
			t.Fatal(err)
		}
		event["type"] = kind
		if kind == "payment_intent.succeeded" {
			data := event["data"].(map[string]any)
			metadata := data["object"].(map[string]any)["metadata"]
			data["object"] = map[string]any{"id": "pi_workflow123", "object": "payment_intent", "status": "succeeded", "amount": checkout.AmountCents, "amount_received": checkout.AmountCents, "currency": "usd", "latest_charge": "ch_workflow123", "metadata": metadata}
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		eventID = receivePaymentWorkflowEvent(t, service, encoded, now).EventID
		return CheckoutObservation{ProviderCheckoutID: request.ProviderCheckoutID, AmountCents: request.AmountCents, Currency: request.Currency, LiveMode: request.LiveMode, Status: "expired", PaymentStatus: "unpaid", ExpiresAt: time.Now().Add(-time.Minute)}, nil
	}
	if err := service.HandleProductCheckoutCheckJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
	quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.expired_verified'`, 0, checkout.PaymentID)
	if err := service.HandlePaymentEventJob(ctx, jobs.Job{Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, eventID))}); err != nil {
		t.Fatal(err)
	}
	assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
}

func TestProductCheckoutCheckCancellationValidationBoundary(t *testing.T) {
	for _, scenario := range []string{"before_read", "invalid_response", "read_error", "verified_expiry"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, checkout, _, job := checkoutCheckFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			runtime.read = func(_ context.Context, request CheckoutReadRequest) (CheckoutObservation, error) {
				calls++
				cancel()
				value := CheckoutObservation{ProviderCheckoutID: request.ProviderCheckoutID, AmountCents: request.AmountCents, Currency: request.Currency, LiveMode: request.LiveMode, Status: "expired", PaymentStatus: "unpaid", ExpiresAt: time.Now().Add(-time.Minute)}
				if scenario == "invalid_response" {
					value.AmountCents++
				}
				if scenario == "read_error" {
					return value, context.Canceled
				}
				return value, nil
			}
			if scenario == "before_read" {
				cancel()
			}
			err := service.HandleProductCheckoutCheckJob(ctx, job)
			if scenario == "verified_expiry" {
				if err != nil {
					t.Fatal(err)
				}
				assertCheckoutState(t, pool, checkout, "cancelled", "cancelled", 0)
				quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.queried'`, 1, checkout.PaymentID)
			} else {
				if err == nil {
					t.Fatal("cancelled or invalid read accepted")
				}
				assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
				quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.queried'`, 0, checkout.PaymentID)
			}
			quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE checkout_job_id=$1`, 0, job.ID)
			if scenario == "before_read" && calls != 0 || scenario != "before_read" && calls != 1 {
				t.Fatal("unexpected reads", calls)
			}
		})
	}
}

func TestProductCheckoutCheckEvidencePersistenceBudget(t *testing.T) {
	pool, service, runtime, checkout, buyer, job := checkoutCheckFixture(t)
	ctx := t.Context()
	lock, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	if _, err = lock.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "product-checkout:"+buyer.String()+":"+checkout.ResourceID.String()); err != nil {
		t.Fatal(err)
	}
	reads := 0
	runtime.read = func(_ context.Context, request CheckoutReadRequest) (CheckoutObservation, error) {
		reads++
		return CheckoutObservation{ProviderCheckoutID: request.ProviderCheckoutID, AmountCents: request.AmountCents, Currency: request.Currency, LiveMode: request.LiveMode, Status: "complete", PaymentStatus: "paid", ProviderPaymentID: "pi_workflow123", ProviderChargeID: "ch_workflow123", IntentStatus: "succeeded", AmountReceived: request.AmountCents, ExpiresAt: time.Now().Add(-time.Minute)}, nil
	}
	workerCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	started := time.Now()
	err = service.HandleProductCheckoutCheckJob(workerCtx, job)
	elapsed := time.Since(started)
	if !errors.Is(err, context.DeadlineExceeded) || elapsed < 4500*time.Millisecond || elapsed > 8*time.Second || reads != 1 {
		t.Fatal("persistence did not honor its independent five-second bound", elapsed, reads, err)
	}
	if !jobs.ShouldRetry(err) {
		t.Fatal("write timeout made the check permanently fail", err)
	}
	if err = lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE checkout_job_id=$1`, 0, job.ID)
	quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.queried'`, 0, checkout.PaymentID)
	assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
	// The existing job can retry after the database becomes writable. A failed
	// save must not publish a half-written event or pretend the payment settled.
	if err = service.HandleProductCheckoutCheckJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE checkout_job_id=$1`, 1, job.ID)
}
