package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

type checkoutReadRuntime struct {
	guardedProductRuntime
	read func(context.Context, CheckoutReadRequest) (CheckoutObservation, error)
}

func (r *checkoutReadRuntime) ReadProductCheckout(ctx context.Context, input CheckoutReadRequest) (CheckoutObservation, error) {
	if r.read != nil {
		return r.read(ctx, input)
	}
	return CheckoutObservation{ProviderCheckoutID: input.ProviderCheckoutID, AmountCents: input.AmountCents, Currency: input.Currency, LiveMode: input.LiveMode, Status: "expired", PaymentStatus: "unpaid", ExpiresAt: time.Now().Add(-time.Minute)}, nil
}

func checkoutCheckFixture(t *testing.T) (*pgxpool.Pool, *Service, *checkoutReadRuntime, Checkout, uuid.UUID, jobs.Job) {
	t.Helper()
	pool, cleanup := paymentTestPool(t)
	t.Cleanup(cleanup)
	buyer, _, _, product := newProductCheckoutFixture(t, pool)
	runtime := &checkoutReadRuntime{}
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(runtime))
	checkout, _, err := service.BeginProductCheckout(context.Background(), buyer, product, "checkout-query-original", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product))
	if err != nil {
		t.Fatal(err)
	}
	var job jobs.Job
	var scheduled time.Time
	if err := pool.QueryRow(context.Background(), `SELECT id,kind,payload,available_at,max_attempts FROM jobs WHERE kind=$1 AND payload->>'paymentId'=$2`, ProductCheckoutCheckJobKind, checkout.PaymentID.String()).Scan(&job.ID, &job.Kind, &job.Payload, &scheduled, &job.MaxAttempts); err != nil {
		t.Fatal(err)
	}
	if scheduled.Before(checkout.ExpiresAt) || job.MaxAttempts != 20 {
		t.Fatal("checkout check is not durably scheduled after expiry")
	}
	if _, err := pool.Exec(context.Background(), `UPDATE payment_intents SET checkout_expires_at=now()-interval '1 minute' WHERE id=$1`, checkout.PaymentID); err != nil {
		t.Fatal(err)
	}
	return pool, service, runtime, checkout, buyer, job
}

func assertCheckoutState(t *testing.T, pool *pgxpool.Pool, checkout Checkout, paymentState, orderState string, rights int) {
	t.Helper()
	var actualPayment, actualOrder string
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT p.status,o.status,(SELECT count(*) FROM entitlements WHERE order_id=o.id AND status='active') FROM payment_intents p JOIN orders o ON o.id=p.order_id WHERE p.id=$1`, checkout.PaymentID).Scan(&actualPayment, &actualOrder, &count); err != nil {
		t.Fatal(err)
	}
	if actualPayment != paymentState || actualOrder != orderState || count != rights {
		t.Fatalf("state %s/%s rights=%d want %s/%s rights=%d", actualPayment, actualOrder, count, paymentState, orderState, rights)
	}
}

func processCheckoutSignedPayment(t *testing.T, service *Service, checkout Checkout, eventName string, remoteSession ...string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	service.verifier.now = func() time.Time { return now }
	body := strings.ReplaceAll(string(productPaidEvent(checkout.PaymentID, checkout.ResourceID, now.Unix(), checkout.AmountCents)), "evt_productpaid", eventName)
	var session string
	if err := service.pool.QueryRow(context.Background(), `SELECT COALESCE(provider_checkout_id,'') FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&session); err != nil {
		t.Fatal(err)
	}
	if len(remoteSession) == 1 {
		if session != "" && session != remoteSession[0] {
			t.Fatal("fixture remote session does not match the recorded checkout")
		}
		session = remoteSession[0]
	}
	if !validStripeID(session, "cs_") {
		t.Fatal("a lost-response fixture must supply the original remote session")
	}
	// This fixture creates per-payment sessions, unlike productCheckoutRuntime.
	body = strings.ReplaceAll(body, "cs_workflow123", session)
	receipt := receivePaymentWorkflowEvent(t, service, []byte(body), now)
	if err := service.HandlePaymentEventJob(context.Background(), jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID))}); err != nil {
		t.Fatal(err)
	}
}

func TestProductCheckoutVerifiedExpiry(t *testing.T) {
	pool, service, _, checkout, buyer, job := checkoutCheckFixture(t)
	ctx := context.Background()
	begin := func(key string) (Checkout, error) {
		c, _, err := service.BeginProductCheckout(ctx, buyer, checkout.ResourceID, key, "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, checkout.ResourceID))
		return c, err
	}
	if _, err := begin("new-before-verification"); !errors.Is(err, ErrCheckoutConflict) {
		t.Fatalf("local clock unlocked checkout: %v", err)
	}
	if err := service.HandleProductCheckoutCheckJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	assertCheckoutState(t, pool, checkout, "cancelled", "cancelled", 0)
	if _, err := begin("checkout-query-original"); !errors.Is(err, ErrCheckoutExpired) {
		t.Fatalf("old command silently reopened: %v", err)
	}
	replacement, err := begin("explicit-new-checkout")
	if err != nil {
		t.Fatal(err)
	}
	if replacement.PaymentID == checkout.PaymentID {
		t.Fatal("closed command was repurposed")
	}
	if err := service.HandleProductCheckoutCheckJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	processCheckoutSignedPayment(t, service, checkout, "evt_closedlatepayment")
	assertCheckoutState(t, pool, checkout, "refund_pending", "refund_requested", 0)
	assertCheckoutState(t, pool, replacement, "checkout_open", "payment_pending", 0)
	var reason string
	if err := pool.QueryRow(ctx, `SELECT compensation_reason FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&reason); err != nil || reason != "checkout_closed" {
		t.Fatalf("late payment lost: %s %v", reason, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_intent_events SET evidence='{}' WHERE payment_id=$1 AND event_type='checkout.expired_verified'`, checkout.PaymentID); err == nil {
		t.Fatal("expiry proof mutable")
	}
	down, err := os.ReadFile("../platform/database/migrations/0084_product_checkout_checks.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard checkout query evidence") {
		t.Fatalf("rollback discarded checks: %v", err)
	}
}

func TestProductCheckoutCheckRetainsUncertainPayments(t *testing.T) {
	for _, scenario := range []string{"open", "async", "uncancelled_intent", "invalid", "network", "early", "unknown_job"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, checkout, _, job := checkoutCheckFixture(t)
			ctx := context.Background()
			runtime.read = func(_ context.Context, r CheckoutReadRequest) (CheckoutObservation, error) {
				if scenario == "network" {
					return CheckoutObservation{}, errors.New("network unavailable")
				}
				o := CheckoutObservation{ProviderCheckoutID: r.ProviderCheckoutID, AmountCents: r.AmountCents, Currency: r.Currency, Status: "expired", PaymentStatus: "unpaid", ExpiresAt: time.Now().Add(-time.Minute)}
				switch scenario {
				case "open":
					o.Status = "open"
				case "async":
					o.Status, o.ProviderPaymentID, o.IntentStatus = "complete", "pi_asyncwait", "processing"
				case "uncancelled_intent":
					o.ProviderPaymentID, o.IntentStatus = "pi_uncancelled", "requires_payment_method"
				case "invalid":
					o.AmountCents = 1
				case "early", "unknown_job":
					t.Error("invalid/early job contacted provider")
				}
				return o, nil
			}
			if scenario == "early" {
				if _, err := pool.Exec(ctx, `UPDATE payment_intents SET checkout_expires_at=now()+interval '1 hour' WHERE id=$1`, checkout.PaymentID); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "unknown_job" {
				job.ID = uuid.New()
			}
			if err := service.HandleProductCheckoutCheckJob(ctx, job); err == nil {
				t.Fatal("uncertain payment treated as settled")
			}
			assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
		})
	}
}

func TestProductCheckoutCheckPaidRecovery(t *testing.T) {
	pool, service, runtime, checkout, _, job := checkoutCheckFixture(t)
	ctx := context.Background()
	runtime.read = func(_ context.Context, r CheckoutReadRequest) (CheckoutObservation, error) {
		return CheckoutObservation{ProviderCheckoutID: r.ProviderCheckoutID, AmountCents: r.AmountCents, Currency: r.Currency, Status: "complete", PaymentStatus: "paid", ProviderPaymentID: "pi_workflow123", ProviderChargeID: "ch_workflow123", IntentStatus: "succeeded", AmountReceived: r.AmountCents, ExpiresAt: time.Now().Add(-time.Minute)}, nil
	}
	if err := service.HandleProductCheckoutCheckJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
	var fulfillment jobs.Job
	var eventID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload,e.id FROM payment_provider_events e JOIN jobs j ON j.payload->>'eventId'=e.id::text AND j.kind=$2 WHERE e.checkout_job_id=$1 AND e.evidence_source='provider_query'`, job.ID, PaymentEventJobKind).Scan(&fulfillment.ID, &fulfillment.Kind, &fulfillment.Payload, &eventID); err != nil {
		t.Fatal(err)
	}
	// Crash after committing evidence: restart must not read a newer snapshot.
	runtime.read = func(context.Context, CheckoutReadRequest) (CheckoutObservation, error) {
		t.Error("persisted paid evidence refetched")
		return CheckoutObservation{}, errors.New("offline")
	}
	if err := service.HandleProductCheckoutCheckJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := service.HandlePaymentEventJob(ctx, fulfillment); err != nil {
		t.Fatal(err)
	}
	if err := service.HandlePaymentEventJob(ctx, fulfillment); err != nil {
		t.Fatal(err)
	}
	assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
	processCheckoutSignedPayment(t, service, checkout, "evt_querythenwebhook")
	assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
	if _, err := pool.Exec(ctx, `UPDATE payment_provider_events SET payment_status='unpaid' WHERE id=$1`, eventID); err == nil {
		t.Fatal("query proof mutable")
	}
}

func TestProductCheckoutExpiryRacingSignedSuccess(t *testing.T) {
	pool, service, runtime, checkout, _, job := checkoutCheckFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	runtime.read = func(ctx context.Context, r CheckoutReadRequest) (CheckoutObservation, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return CheckoutObservation{}, ctx.Err()
		}
		return CheckoutObservation{ProviderCheckoutID: r.ProviderCheckoutID, AmountCents: r.AmountCents, Currency: r.Currency, Status: "expired", PaymentStatus: "unpaid", ExpiresAt: time.Now().Add(-time.Minute)}, nil
	}
	result := make(chan error, 1)
	go func() { result <- service.HandleProductCheckoutCheckJob(context.Background(), job) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("query did not start")
	}
	processCheckoutSignedPayment(t, service, checkout, "evt_queryracepaid")
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
}

func TestProductCheckoutPaidQueryRacingClosure(t *testing.T) {
	pool, service, runtime, checkout, buyer, job := checkoutCheckFixture(t)
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	var reads atomic.Int32
	runtime.read = func(ctx context.Context, r CheckoutReadRequest) (CheckoutObservation, error) {
		o := CheckoutObservation{ProviderCheckoutID: r.ProviderCheckoutID, AmountCents: r.AmountCents, Currency: r.Currency, Status: "expired", PaymentStatus: "unpaid", ExpiresAt: time.Now().Add(-time.Minute)}
		if reads.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return CheckoutObservation{}, ctx.Err()
			}
			o.Status, o.PaymentStatus, o.IntentStatus = "complete", "paid", "succeeded"
			o.ProviderPaymentID, o.ProviderChargeID, o.AmountReceived = "pi_workflow123", "ch_workflow123", r.AmountCents
		}
		return o, nil
	}
	result := make(chan error, 1)
	go func() { result <- service.HandleProductCheckoutCheckJob(ctx, job) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("query did not start")
	}
	// Simulate a lease handoff receiving a contradictory terminal snapshot. Any
	// valid paid evidence arriving later must still be reconciled and compensated.
	if err := service.HandleProductCheckoutCheckJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	replacement, _, err := service.BeginProductCheckout(ctx, buyer, checkout.ResourceID, "query-race-replacement", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, checkout.ResourceID))
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	var eventID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM payment_provider_events WHERE checkout_job_id=$1`, job.ID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	if err := service.HandlePaymentEventJob(ctx, jobs.Job{Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, eventID.String()))}); err != nil {
		t.Fatal(err)
	}
	assertCheckoutState(t, pool, checkout, "refund_pending", "refund_requested", 0)
	assertCheckoutState(t, pool, replacement, "checkout_open", "payment_pending", 0)
}

func TestProductCheckoutClosureSchedulesDeletedSourceCleanup(t *testing.T) {
	pool, service, _, checkout, _, job := checkoutCheckFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE users SET status='deleted' WHERE id=(SELECT payee_id FROM payment_intents WHERE id=$1)`, checkout.PaymentID); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductCheckoutCheckJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind='data_rights.media_cleanup' AND payload->>'userId'=(SELECT payee_id::text FROM payment_intents WHERE id=$1)`, checkout.PaymentID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("released contract left deleted owner's media retained: %d %v", count, err)
	}
	if err := service.HandleProductCheckoutCheckJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	var again int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind='data_rights.media_cleanup'`).Scan(&again); err != nil || again != count {
		t.Fatalf("cleanup duplicated: %d %v", again, err)
	}
}

func TestProductPaymentFailureKeepsCheckoutLocked(t *testing.T) {
	for _, eventType := range []string{"payment_intent.payment_failed", "checkout.session.async_payment_failed"} {
		t.Run(eventType, func(t *testing.T) {
			pool, service, _, checkout, buyer, job := checkoutCheckFixture(t)
			ctx := context.Background()
			var session string
			if err := pool.QueryRow(ctx, `SELECT provider_checkout_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&session); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			service.verifier.now = func() time.Time { return now }
			objectType, id, status, amountField := "payment_intent", "pi_workflow123", "requires_payment_method", "amount"
			if eventType == "checkout.session.async_payment_failed" {
				objectType, id, status, amountField = "checkout.session", session, "unpaid", "amount_total"
			}
			body := []byte(fmt.Sprintf(`{"id":"evt_attemptfailed","object":"event","api_version":%q,"created":%d,"livemode":false,"type":%q,"data":{"object":{"id":%q,"object":%q,"status":%q,"payment_status":%q,%q:1900,"currency":"usd","payment_intent":"pi_workflow123","metadata":{"hcai_payment_id":%q,"hcai_resource_id":%q,"hcai_purpose":"product"}}}}`, testStripeAPIVersion, now.Unix(), eventType, id, objectType, status, status, amountField, checkout.PaymentID.String(), checkout.ResourceID.String()))
			receipt := receivePaymentWorkflowEvent(t, service, body, now)
			if err := service.HandlePaymentEventJob(ctx, jobs.Job{Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}); err != nil {
				t.Fatal(err)
			}
			assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
			if _, _, err := service.BeginProductCheckout(ctx, buyer, checkout.ResourceID, "attempt-failed-new-key", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, checkout.ResourceID)); !errors.Is(err, ErrCheckoutConflict) {
				t.Fatalf("failed attempt allowed another payment: %v", err)
			}
			processCheckoutSignedPayment(t, service, checkout, "evt_retryafterfailed")
			assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
			late := receivePaymentWorkflowEvent(t, service, []byte(strings.ReplaceAll(string(body), "evt_attemptfailed", "evt_failedlate")), now)
			if err := service.HandlePaymentEventJob(ctx, jobs.Job{Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, late.EventID.String()))}); err != nil {
				t.Fatal(err)
			}
			assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
			if err := service.HandleProductCheckoutCheckJob(ctx, job); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProductCheckoutCheckAuthenticatedHTTP(t *testing.T) {
	pool, service, _, checkout, _, job := checkoutCheckFixture(t)
	ctx := context.Background()
	var session string
	if err := pool.QueryRow(ctx, `SELECT provider_checkout_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&session); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer sk_test_checkout_query" {
			t.Error("query made a money command or unauthenticated request")
			w.WriteHeader(403)
			return
		}
		metadata := map[string]string{"hcai_payment_id": checkout.PaymentID.String(), "hcai_resource_id": checkout.ResourceID.String(), "hcai_purpose": "product"}
		switch r.URL.Path {
		case "/account":
			fmt.Fprint(w, `{"id":"acct_workflow","object":"account"}`)
		case "/balance":
			fmt.Fprint(w, `{"object":"balance","livemode":false}`)
		case "/checkout/sessions/" + session:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": session, "object": "checkout.session", "mode": "payment", "status": "complete", "payment_status": "paid", "payment_intent": "pi_workflow123", "amount_total": 1900, "currency": "usd", "livemode": false, "expires_at": time.Now().Add(-time.Minute).Unix(), "client_reference_id": checkout.PaymentID.String(), "metadata": metadata})
		case "/payment_intents/pi_workflow123":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "pi_workflow123", "object": "payment_intent", "status": "succeeded", "amount": 1900, "amount_received": 1900, "currency": "usd", "livemode": false, "latest_charge": "ch_workflow123", "metadata": metadata})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	service.runtimes = NewRuntimeCatalog(originalMerchantHTTPRuntime(t, server, "sk_test_checkout_query"))
	if err := service.HandleProductCheckoutCheckJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	var eventID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM payment_provider_events WHERE checkout_job_id=$1 AND evidence_source='provider_query' AND event_type='checkout.observed'`, job.ID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	if err := service.HandlePaymentEventJob(ctx, jobs.Job{Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, eventID.String()))}); err != nil {
		t.Fatal(err)
	}
	assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
	if calls.Load() != 4 {
		t.Fatalf("unexpected provider requests: %d", calls.Load())
	}
	for _, nullableField := range []string{"purpose", "payment_status"} {
		purpose, paymentStatus := "'product'", "'paid'"
		if nullableField == "purpose" {
			purpose = "NULL"
		} else {
			paymentStatus = "NULL"
		}
		_, err := pool.Exec(ctx, `INSERT INTO payment_provider_events(provider,provider_event_id,event_type,api_version,live_mode,occurred_at,payload_sha256,object_id,object_type,payment_id,resource_id,purpose,amount_cents,currency,payment_status,provider_payment_id,provider_charge_id,evidence_source,checkout_job_id)
 SELECT provider,$2,event_type,api_version,live_mode,occurred_at,payload_sha256,object_id,object_type,payment_id,resource_id,`+purpose+`,amount_cents,currency,`+paymentStatus+`,provider_payment_id,provider_charge_id,evidence_source,checkout_job_id FROM payment_provider_events WHERE id=$1`, eventID, "invalidquery_"+nullableField)
		if err == nil || !strings.Contains(err.Error(), "payment_query_evidence") {
			t.Fatalf("nullable %s bypassed query provenance constraint: %v", nullableField, err)
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	service.verifier.now = func() time.Time { return now }
	body := []byte(strings.ReplaceAll(string(productPaidEvent(checkout.PaymentID, checkout.ResourceID, now.Unix(), 1900)), "checkout.session.completed", "checkout.observed"))
	header := "t=" + fmt.Sprint(now.Unix()) + ",v1=" + stripeSignature(testStripeWebhookSecret, now.Unix(), body)
	receipt, err := service.ReceiveStripeWebhook(ctx, body, header)
	if err != nil || receipt.Status != "ignored" {
		t.Fatalf("external caller forged a query: %#v %v", receipt, err)
	}
}

func TestProductCheckoutCheckMigrationBackfill(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	apply := func(name string) {
		t.Helper()
		body, err := os.ReadFile("../platform/database/migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	buyer, _, _, product := newProductCheckoutFixture(t, pool)
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true}, NewRuntimeCatalog(&guardedProductRuntime{}))
	checkout, _, err := service.BeginProductCheckout(ctx, buyer, product, "before-checkout-checks", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product))
	if err != nil {
		t.Fatal(err)
	}
	// Build a real checkout with current code before removing newer views.
	restoreFundsViews := suspendSellerFundsViewsForMigrationTest(t, pool)
	apply("0134_product_closed_refund_reconciliation.down.sql")
	apply("0133_product_closed_checkout_refund_confirmation.down.sql")
	apply("0132_product_closed_checkout_recovery.down.sql")
	apply("0131_product_checkout_evidence_conflicts.down.sql")
	apply("0130_product_checkout_session_evidence.down.sql")
	apply("0129_product_checkout_lookup_check_sharing.down.sql")
	apply("0124_product_checkout_check_reconciliation.down.sql")
	apply("0107_product_refund_dispatch.down.sql")
	apply("0106_product_refund_reconciliation.down.sql")
	apply("0087_product_checkout_lookup.down.sql")
	apply("0086_product_payment_identity_recovery.down.sql")
	apply("0084_product_checkout_checks.down.sql")
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET checkout_expires_at=now()-interval '1 day' WHERE id=$1`, checkout.PaymentID); err != nil {
		t.Fatal(err)
	}
	apply("0084_product_checkout_checks.up.sql")
	apply("0086_product_payment_identity_recovery.up.sql")
	apply("0087_product_checkout_lookup.up.sql")
	apply("0106_product_refund_reconciliation.up.sql")
	apply("0107_product_refund_dispatch.up.sql")
	apply("0124_product_checkout_check_reconciliation.up.sql")
	apply("0129_product_checkout_lookup_check_sharing.up.sql")
	apply("0130_product_checkout_session_evidence.up.sql")
	apply("0131_product_checkout_evidence_conflicts.up.sql")
	apply("0132_product_closed_checkout_recovery.up.sql")
	apply("0133_product_closed_checkout_refund_confirmation.up.sql")
	apply("0134_product_closed_refund_reconciliation.up.sql")
	restoreFundsViews()
	assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
	var due bool
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*),bool_and(available_at<=now() AND max_attempts=20) FROM jobs WHERE kind=$1 AND payload->>'paymentId'=$2`, ProductCheckoutCheckJobKind, checkout.PaymentID.String()).Scan(&count, &due); err != nil || count != 1 || !due {
		t.Fatalf("historical session not safely scheduled: %d %t %v", count, due, err)
	}
}
