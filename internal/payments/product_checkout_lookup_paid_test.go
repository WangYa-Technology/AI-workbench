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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func TestProductCheckoutPaidLookupSurvivesLaterProviderState(t *testing.T) {
	for _, scenario := range []string{"expired", "offline", "replacement_offline"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			pool, service, runtime, checkout, buyer, lookupJob := pendingCheckoutLookupFixture(t)
			runtime.observation.Status = "complete"
			runtime.observation.PaymentStatus = "paid"
			runtime.observation.ProviderPaymentID = "pi_lookup_saved_paid"
			runtime.observation.ProviderChargeID = "ch_lookup_saved_paid"
			runtime.observation.IntentStatus = "succeeded"
			runtime.observation.AmountReceived = checkout.AmountCents
			if err := service.HandleProductCheckoutLookupJob(ctx, lookupJob); err != nil {
				t.Fatal(err)
			}
			checkJob := locatedCheckJob(t, pool, lookupJob.ID)
			if scenario == "replacement_offline" {
				if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed' WHERE id=$1`, checkJob.ID); err != nil {
					t.Fatal(err)
				}
				if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,$2,20) RETURNING id`, checkJob.Kind, checkJob.Payload).Scan(&checkJob.ID); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "expired" {
				runtime.observation = CheckoutObservation{ProviderCheckoutID: runtime.observation.ProviderCheckoutID, Status: "expired", PaymentStatus: "unpaid", AmountCents: checkout.AmountCents, Currency: checkout.Currency, ExpiresAt: time.Now().Add(-time.Minute)}
			} else {
				service.runtimes = NewRuntimeCatalog()
			}
			if err := service.HandleProductCheckoutCheckJob(ctx, checkJob); err != nil {
				t.Fatal("saved authenticated payment could not advance", err)
			}
			assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
			var eventID uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT id FROM payment_provider_events WHERE checkout_job_id=$1 AND provider_payment_id='pi_lookup_saved_paid' AND provider_charge_id='ch_lookup_saved_paid'`, checkJob.ID).Scan(&eventID); err != nil {
				t.Fatal("original payment proof not queued for fulfillment", err)
			}
			if err := service.HandleProductCheckoutCheckJob(ctx, checkJob); err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(map[string]string{"eventId": eventID.String()})
			for range 2 {
				if err := service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: body}); err != nil {
					t.Fatal(err)
				}
			}
			assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
			var evidenceCount, fulfillmentCount int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.queried' AND evidence->>'lookupJobId'=$2`, checkout.PaymentID, lookupJob.ID.String()).Scan(&evidenceCount); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind=$1 AND payload->>'eventId'=$2`, PaymentEventJobKind, eventID.String()).Scan(&fulfillmentCount); err != nil {
				t.Fatal(err)
			}
			if evidenceCount != 1 || fulfillmentCount != 1 || runtime.checkoutReads.Load() != 0 || runtime.reads.Load() != 1 || len(runtime.requests) != 1 {
				t.Fatalf("lost provenance or repeated operation: evidence=%d fulfillment=%d queries=%d lookups=%d creates=%d", evidenceCount, fulfillmentCount, runtime.checkoutReads.Load(), runtime.reads.Load(), len(runtime.requests))
			}
			if scenario == "replacement_offline" {
				pkg, body := runProductExport(t, pool, buyer)
				found := false
				for _, event := range pkg.Data.Marketplace.Data["paymentEvents"] {
					if event["lookupJobId"] == lookupJob.ID.String() && event["checkoutJobId"] == checkJob.ID.String() {
						found = true
					}
				}
				if !found || strings.Contains(string(body), "checkout.stripe.com") {
					t.Fatal("owner export missing safe replacement evidence lineage")
				}
				var seller uuid.UUID
				if err := pool.QueryRow(ctx, `SELECT seller_id FROM products WHERE id=$1`, checkout.ResourceID).Scan(&seller); err != nil {
					t.Fatal(err)
				}
				_, sellerBody := runProductExport(t, pool, seller)
				if strings.Contains(string(sellerBody), lookupJob.ID.String()) {
					t.Fatal("seller export exposed buyer lookup evidence")
				}
			}
		})
	}
}

func TestProductCheckoutPaidLookupAuthenticatedRestart(t *testing.T) {
	pool, service, _, checkout, _, lookupJob := pendingCheckoutLookupFixture(t)
	ctx := t.Context()
	for _, direction := range []string{"down", "up"} {
		body, err := os.ReadFile("../platform/database/migrations/0129_product_checkout_lookup_check_sharing." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		quarantineExec(t, pool, string(body))
	}
	var reads atomic.Int32
	now := time.Now().UTC().Truncate(time.Second)
	metadata := map[string]string{"hcai_payment_id": checkout.PaymentID.String(), "hcai_resource_id": checkout.ResourceID.String(), "hcai_purpose": "product"}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer lookup-restart-fixture" || r.Header.Get("Stripe-Version") != testStripeAPIVersion {
			t.Error("unexpected mutation or unauthenticated provider read")
			http.Error(w, "rejected", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/account":
			fmt.Fprint(w, `{"id":"acct_workflow","object":"account"}`)
		case "/balance":
			fmt.Fprint(w, `{"object":"balance","livemode":false}`)
		case "/checkout/sessions":
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "has_more": false, "data": []any{map[string]any{"id": "cs_lookup_restart", "object": "checkout.session", "created": now.Unix(), "livemode": false, "client_reference_id": checkout.PaymentID.String(), "metadata": metadata}}})
		case "/checkout/sessions/cs_lookup_restart":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "cs_lookup_restart", "object": "checkout.session", "mode": "payment", "status": "complete", "payment_status": "paid", "payment_intent": "pi_lookup_restart", "amount_total": checkout.AmountCents, "currency": "usd", "livemode": false, "expires_at": now.Add(time.Hour).Unix(), "client_reference_id": checkout.PaymentID.String(), "metadata": metadata})
		case "/payment_intents/pi_lookup_restart":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "pi_lookup_restart", "object": "payment_intent", "status": "succeeded", "amount": checkout.AmountCents, "amount_received": checkout.AmountCents, "currency": "usd", "livemode": false, "latest_charge": "ch_lookup_restart", "metadata": metadata})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	service.runtimes = NewRuntimeCatalog(originalMerchantHTTPRuntime(t, upstream, "lookup-restart-fixture"))
	if err := service.HandleProductCheckoutLookupJob(ctx, lookupJob); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 5 {
		t.Fatal("missing authenticated account, list, session or payment evidence", reads.Load())
	}
	upstream.Close()
	// A fresh service with no provider runtime can consume the persisted receipt.
	// Only local DB/media fulfillment is performed after the authenticated read.
	restarted := newPaymentTestService(t, pool, service.config, NewRuntimeCatalog())
	check := locatedCheckJob(t, pool, lookupJob.ID)
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
	quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND evidence->>'lookupJobId'=$2`, 1, checkout.PaymentID, lookupJob.ID.String())
}

func TestProductCheckoutPaidLookupConflictingReceipts(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprint(conflict), func(t *testing.T) {
			pool, service, runtime, checkout, _, lookupJob := pendingCheckoutLookupFixture(t)
			ctx := t.Context()
			runtime.observation.Status, runtime.observation.PaymentStatus, runtime.observation.IntentStatus = "complete", "paid", "succeeded"
			runtime.observation.ProviderPaymentID, runtime.observation.ProviderChargeID, runtime.observation.AmountReceived = "pi_lookup_saved_paid", "ch_lookup_saved_paid", checkout.AmountCents
			if err := service.HandleProductCheckoutLookupJob(ctx, lookupJob); err != nil {
				t.Fatal(err)
			}
			check := locatedCheckJob(t, pool, lookupJob.ID)
			quarantineExec(t, pool, `UPDATE jobs SET status='succeeded' WHERE id=$1`, lookupJob.ID)
			second := lookupJob
			if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,status,max_attempts) VALUES($1,$2,'succeeded',20) RETURNING id`, lookupJob.Kind, lookupJob.Payload).Scan(&second.ID); err != nil {
				t.Fatal(err)
			}
			observation := runtime.observation
			observation.ExpiresAt = observation.ExpiresAt.Add(time.Second)
			if conflict {
				observation.ProviderChargeID = "ch_lookup_other"
			}
			body, _ := json.Marshal(CheckoutLookupResult{Outcome: "found", Pages: 1, Scanned: 1, Matches: []string{observation.ProviderCheckoutID}, Observation: &observation})
			quarantineExec(t, pool, `INSERT INTO product_checkout_lookups(job_id,payment_id,requested_by,outcome,searched_after,searched_before,result)
 SELECT $2,payment_id,requested_by,'found',searched_after,searched_before,$3 FROM product_checkout_lookups WHERE job_id=$1`, lookupJob.ID, second.ID, body)
			err := service.HandleProductCheckoutCheckJob(ctx, check)
			if conflict {
				if !errors.Is(err, ErrCheckoutReconciliation) {
					t.Fatal("conflicting paid receipts accepted", err)
				}
				quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE payment_id=$1`, 0, checkout.PaymentID)
			} else if err != nil {
				t.Fatal("same payment with a different expiry was rejected", err)
			} else {
				quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE payment_id=$1`, 1, checkout.PaymentID)
			}
			assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
			if runtime.checkoutReads.Load() != 0 {
				t.Fatal("re-fetched persisted receipt")
			}
		})
	}
}

type delayedPaidLookupCheckRuntime struct {
	*lookupRuntime
	read func(context.Context, CheckoutReadRequest) (CheckoutObservation, error)
}

func (r *delayedPaidLookupCheckRuntime) ReadProductCheckout(ctx context.Context, request CheckoutReadRequest) (CheckoutObservation, error) {
	r.checkoutReads.Add(1)
	return r.read(ctx, request)
}

func TestProductCheckoutPaidLookupArrivesDuringExpiryQuery(t *testing.T) {
	pool, service, runtime, checkout, _, first := pendingCheckoutLookupFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	lookupEntered, releaseLookup := make(chan struct{}), make(chan struct{})
	queryEntered, releaseQuery := make(chan struct{}), make(chan struct{})
	var onceLookup, onceQuery sync.Once
	unblockLookup := func() { onceLookup.Do(func() { close(releaseLookup) }) }
	unblockQuery := func() { onceQuery.Do(func() { close(releaseQuery) }) }
	defer unblockLookup()
	defer unblockQuery()
	open := runtime.observation
	paid := open
	paid.Status, paid.PaymentStatus, paid.IntentStatus = "complete", "paid", "succeeded"
	paid.ProviderPaymentID, paid.ProviderChargeID, paid.AmountReceived = "pi_lookup_late", "ch_lookup_late", checkout.AmountCents
	runtime.lookup = func(ctx context.Context, _ CheckoutLookupRequest) (CheckoutLookupResult, error) {
		observation := open
		if runtime.reads.Load() == 1 {
			close(lookupEntered)
			select {
			case <-releaseLookup:
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
	case <-lookupEntered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// A replacement locator returns the earlier open session while the old
	// authenticated read is still in flight. Both observations remain durable.
	quarantineExec(t, pool, `UPDATE jobs SET status='failed' WHERE id=$1`, first.ID)
	second := first
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,$2,20) RETURNING id`, first.Kind, first.Payload).Scan(&second.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductCheckoutLookupJob(ctx, second); err != nil {
		t.Fatal(err)
	}
	check := locatedCheckJob(t, pool, second.ID)
	quarantineExec(t, pool, `UPDATE payment_intents SET checkout_expires_at=now()-interval '1 minute' WHERE id=$1`, checkout.PaymentID)
	service.runtimes = NewRuntimeCatalog(&delayedPaidLookupCheckRuntime{lookupRuntime: runtime, read: func(ctx context.Context, _ CheckoutReadRequest) (CheckoutObservation, error) {
		close(queryEntered)
		select {
		case <-releaseQuery:
		case <-ctx.Done():
			return CheckoutObservation{}, ctx.Err()
		}
		expired := open
		expired.Status, expired.ExpiresAt = "expired", time.Now().Add(-time.Minute)
		return expired, nil
	}})
	queryDone := make(chan error, 1)
	go func() { queryDone <- service.HandleProductCheckoutCheckJob(ctx, check) }()
	select {
	case <-queryEntered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	unblockLookup()
	if err := <-lookupDone; err != nil {
		t.Fatal(err)
	}
	unblockQuery()
	if err := <-queryDone; err != nil {
		t.Fatal(err)
	}
	assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
	quarantineCount(t, pool, `SELECT count(*) FROM product_checkout_lookups WHERE payment_id=$1`, 2, checkout.PaymentID)
	quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.queried' AND evidence->'observation'->>'status'='expired'`, 1, checkout.PaymentID)
	quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE payment_id=$1 AND payment_status='paid'`, 1, checkout.PaymentID)
	// The locator reused the current check, so it must already have queued
	// fulfillment before this check can be completed by the worker.
	if locatedCheckJob(t, pool, first.ID).ID != check.ID {
		t.Fatal("fixture did not exercise the shared in-flight check")
	}
	service.runtimes = NewRuntimeCatalog()
	if err := service.HandleProductCheckoutCheckJob(ctx, locatedCheckJob(t, pool, first.ID)); err != nil {
		t.Fatal(err)
	}
	var fulfillment jobs.Job
	if err := pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM jobs j JOIN payment_provider_events e ON j.payload->>'eventId'=e.id::text WHERE e.payment_id=$1 AND j.kind=$2`, checkout.PaymentID, PaymentEventJobKind).Scan(&fulfillment.ID, &fulfillment.Kind, &fulfillment.Payload); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := service.HandlePaymentEventJob(ctx, fulfillment); err != nil {
			t.Fatal(err)
		}
	}
	assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
	if runtime.checkoutReads.Load() != 1 || runtime.reads.Load() != 2 || len(runtime.requests) != 1 {
		t.Fatal("paid lookup handoff repeated provider operations")
	}
	down, err := os.ReadFile("../platform/database/migrations/0129_product_checkout_lookup_check_sharing.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard shared checkout lookup evidence") {
		t.Fatal("downgrade did not preserve shared receipts", err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM product_checkout_lookups WHERE check_job_id=$1`, 2, check.ID)
}

func TestProductCheckoutPaidLookupRejectsChangedBindings(t *testing.T) {
	for _, scenario := range []string{"amount", "mode", "buyer", "session", "payment", "charge", "provider", "missing_request", "disabled", "forged_job", "invalid_receipt"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			pool, service, runtime, checkout, _, lookupJob := pendingCheckoutLookupFixture(t)
			runtime.observation.Status, runtime.observation.PaymentStatus, runtime.observation.IntentStatus = "complete", "paid", "succeeded"
			runtime.observation.ProviderPaymentID, runtime.observation.ProviderChargeID = "pi_lookup_saved_paid", "ch_lookup_saved_paid"
			runtime.observation.AmountReceived = checkout.AmountCents
			if err := service.HandleProductCheckoutLookupJob(ctx, lookupJob); err != nil {
				t.Fatal(err)
			}
			checkJob := locatedCheckJob(t, pool, lookupJob.ID)
			quarantineExec(t, pool, `UPDATE payment_intents SET checkout_expires_at=now()-interval '1 minute' WHERE id=$1`, checkout.PaymentID)
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
			case "provider":
				quarantineExec(t, pool, `UPDATE payment_intents SET provider='waffo_pancake',checkout_url='https://checkout.example.test/session' WHERE id=$1`, checkout.PaymentID)
			case "missing_request":
				quarantineExec(t, pool, `TRUNCATE product_checkout_dispatches,product_checkout_requests`)
			case "disabled":
				service.config.Enabled = false
			case "forged_job":
				checkJob.ID = uuid.New()
			case "invalid_receipt":
				// Seed a structurally accepted but semantically invalid historical
				// receipt in this isolated schema; immutable production rows are
				// never edited by the recovery path.
				var encoded []byte
				if err := pool.QueryRow(ctx, `SELECT result FROM product_checkout_lookups WHERE job_id=$1`, lookupJob.ID).Scan(&encoded); err != nil {
					t.Fatal(err)
				}
				var result CheckoutLookupResult
				if err := json.Unmarshal(encoded, &result); err != nil {
					t.Fatal(err)
				}
				result.Observation.ProviderChargeID = ""
				encoded, _ = json.Marshal(result)
				quarantineExec(t, pool, `TRUNCATE product_checkout_lookups`)
				quarantineExec(t, pool, `INSERT INTO product_checkout_lookups(job_id,payment_id,requested_by,outcome,searched_after,searched_before,result,check_job_id)
 VALUES($1,$2,(SELECT payer_id FROM payment_intents WHERE id=$2),'found',now()-interval '1 hour',now(),$3,$4)`, lookupJob.ID, checkout.PaymentID, encoded, checkJob.ID)
			}
			err := service.HandleProductCheckoutCheckJob(ctx, checkJob)
			want := "payment_reconciliation_required"
			if scenario == "provider" {
				want = "payment_provider_unsupported"
			} else if scenario == "forged_job" {
				want = "payment_invalid_request"
			}
			if scenario == "disabled" {
				if !errors.Is(err, ErrDisabled) {
					t.Fatal("disabled service accepted proof", err)
				}
			} else if err == nil || err.Error() != want {
				t.Fatalf("want %s, got %v", want, err)
			}
			assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
			quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE payment_id=$1`, 0, checkout.PaymentID)
			quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.expired_verified'`, 0, checkout.PaymentID)
			if runtime.checkoutReads.Load() != 0 || runtime.reads.Load() != 1 || len(runtime.requests) != 1 {
				t.Fatal("invalid positive evidence fell back to remote operations")
			}
		})
	}
}
