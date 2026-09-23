package payments

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

type lookupRuntime struct {
	returnURLRuntime
	lookup           func(context.Context, CheckoutLookupRequest) (CheckoutLookupResult, error)
	observation      CheckoutObservation
	reads            atomic.Int32
	checkoutReads    atomic.Int32
	identityOverride *ProductCheckoutIdentity
}

func (r *lookupRuntime) LookupProductCheckout(ctx context.Context, input CheckoutLookupRequest) (CheckoutLookupResult, error) {
	r.reads.Add(1)
	return r.lookup(ctx, input)
}
func (r *lookupRuntime) ReadProductCheckout(_ context.Context, _ CheckoutReadRequest) (CheckoutObservation, error) {
	r.checkoutReads.Add(1)
	return r.observation, nil
}
func (r *lookupRuntime) ProductCheckoutIdentity(ctx context.Context) (ProductCheckoutIdentity, error) {
	if r.identityOverride != nil {
		return *r.identityOverride, nil
	}
	return r.productCheckoutRuntime.ProductCheckoutIdentity(ctx)
}

func pendingCheckoutLookupFixture(t *testing.T) (*pgxpool.Pool, *Service, *lookupRuntime, Checkout, uuid.UUID, jobs.Job) {
	t.Helper()
	ctx := context.Background()
	pool, cleanup := paymentTestPool(t)
	t.Cleanup(cleanup)
	buyer, _, _, product := newProductCheckoutFixture(t, pool)
	runtime := &lookupRuntime{}
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(runtime))
	if _, _, err := service.BeginProductCheckout(ctx, buyer, product, "lost-response-command", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product)); err == nil || len(runtime.requests) != 1 {
		t.Fatalf("expected lost checkout response: %v", err)
	}
	checkout, err := scanProductCheckout(pool.QueryRow(ctx, productCheckoutSelect+` WHERE p.payer_id=$1`, buyer))
	if err != nil {
		t.Fatal(err)
	}
	var job jobs.Job
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('paymentId',$2::text,'actorId',$3::text),20) RETURNING id,kind,payload`, ProductCheckoutLookupJobKind, checkout.PaymentID, buyer).Scan(&job.ID, &job.Kind, &job.Payload); err != nil {
		t.Fatal(err)
	}
	runtime.observation = CheckoutObservation{ProviderCheckoutID: "cs_lookup_existing", CheckoutURL: "https://checkout.stripe.com/c/pay/recovered", Status: "open", PaymentStatus: "unpaid", AmountCents: checkout.AmountCents, Currency: checkout.Currency, ExpiresAt: time.Now().Add(time.Hour)}
	runtime.lookup = func(_ context.Context, input CheckoutLookupRequest) (CheckoutLookupResult, error) {
		if input.PaymentID != checkout.PaymentID || input.ResourceID != product || input.CreatedAfter.After(time.Now()) || input.CreatedBefore.Before(time.Now()) {
			t.Error("wrong lookup scope")
		}
		return CheckoutLookupResult{Outcome: "found", Pages: 1, Scanned: 1, Matches: []string{runtime.observation.ProviderCheckoutID}, Observation: &runtime.observation}, nil
	}
	return pool, service, runtime, checkout, buyer, job
}
func locatedCheckJob(t *testing.T, pool *pgxpool.Pool, lookupID uuid.UUID) jobs.Job {
	t.Helper()
	var job jobs.Job
	if err := pool.QueryRow(context.Background(), `SELECT j.id,j.kind,j.payload FROM product_checkout_lookups l JOIN jobs j ON j.id=l.check_job_id WHERE l.job_id=$1`, lookupID).Scan(&job.ID, &job.Kind, &job.Payload); err != nil {
		t.Fatal(err)
	}
	return job
}

func TestProductCheckoutLookupRecovery(t *testing.T) {
	for _, scenario := range []string{"open", "paid", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, checkout, buyer, job := pendingCheckoutLookupFixture(t)
			ctx := context.Background()
			if scenario == "expired" {
				runtime.observation.Status = "expired"
				runtime.observation.CheckoutURL = ""
				runtime.observation.ExpiresAt = time.Now().Add(-time.Minute)
			}
			if scenario == "paid" {
				runtime.observation.Status = "complete"
				runtime.observation.PaymentStatus = "paid"
				runtime.observation.ProviderPaymentID = "pi_lookup_existing"
				runtime.observation.ProviderChargeID = "ch_lookup_existing"
				runtime.observation.IntentStatus = "succeeded"
				runtime.observation.AmountReceived = checkout.AmountCents
				runtime.observation.CheckoutURL = "https://malicious.test/never-use"
			}
			if err := service.HandleProductCheckoutLookupJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
			// Terminal sessions intentionally have no reusable payment link.
			// The immutable lookup is evidence, not a missing-checkout alarm.
			assertOperationalMetric(t, pool, "problem", "checkout_evidence_missing", "test", 0, 0, 0)
			if err := service.HandleProductCheckoutLookupJob(ctx, job); err != nil || runtime.reads.Load() != 1 {
				t.Fatal("lookup replay re-read", err)
			}
			if len(runtime.requests) != 1 {
				t.Fatal("lookup created another checkout")
			}
			var stored, resultURL string
			if err := pool.QueryRow(ctx, `SELECT l.result::text,COALESCE(pi.checkout_url,'') FROM product_checkout_lookups l JOIN payment_intents pi ON pi.id=l.payment_id WHERE l.job_id=$1`, job.ID).Scan(&stored, &resultURL); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stored, "http") || (scenario != "open" && resultURL != "") {
				t.Fatal("terminal checkout exposed a URL")
			}
			if scenario == "open" {
				if _, err := pool.Exec(ctx, `UPDATE payment_intents SET checkout_url=NULL WHERE id=$1`, checkout.PaymentID); err != nil {
					t.Fatal(err)
				}
				assertOperationalMetric(t, pool, "problem", "checkout_evidence_missing", "test", 1, 0, 0)
				if _, err := pool.Exec(ctx, `UPDATE payment_intents SET checkout_url=$2 WHERE id=$1`, checkout.PaymentID, resultURL); err != nil {
					t.Fatal(err)
				}
			} else {
				// Proof for another session cannot excuse a missing URL.
				if _, err := pool.Exec(ctx, `UPDATE payment_intents SET provider_checkout_id='cs_unrelated' WHERE id=$1`, checkout.PaymentID); err != nil {
					t.Fatal(err)
				}
				assertOperationalMetric(t, pool, "problem", "checkout_evidence_missing", "test", 1, 0, 0)
				if _, err := pool.Exec(ctx, `UPDATE payment_intents SET provider_checkout_id=$2 WHERE id=$1`, checkout.PaymentID, runtime.observation.ProviderCheckoutID); err != nil {
					t.Fatal(err)
				}
			}
			checkJob := locatedCheckJob(t, pool, job.ID)
			if scenario == "paid" {
				// Finance can replace an exhausted query job; its read-only
				// verification still uses the original immutable lookup proof.
				if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed' WHERE id=$1`, checkJob.ID); err != nil {
					t.Fatal(err)
				}
				assertOperationalMetric(t, pool, "problem", "checkout_evidence_missing", "test", 0, 0, 0)
				assertOperationalMetric(t, pool, "problem", "checkout_check_stopped", "test", 1, 0, 0)
				if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,$2,20) RETURNING id`, checkJob.Kind, checkJob.Payload).Scan(&checkJob.ID); err != nil {
					t.Fatal(err)
				}
				assertOperationalMetric(t, pool, "problem", "checkout_check_stopped", "test", 0, 0, 0)
			}
			checkErr := service.HandleProductCheckoutCheckJob(ctx, checkJob)
			switch scenario {
			case "open":
				if checkErr == nil || !jobs.ShouldRetry(checkErr) {
					t.Fatal("open recovered session ignored expiry schedule", checkErr)
				}
				resumed, _, err := service.BeginProductCheckout(ctx, buyer, checkout.ResourceID, "lost-response-command", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, checkout.ResourceID))
				if err != nil || resumed.PaymentID != checkout.PaymentID || resumed.CheckoutURL != runtime.observation.CheckoutURL || len(runtime.requests) != 1 {
					t.Fatalf("original buyer cannot resume without duplicate create: %#v %v", resumed, err)
				}
			case "expired":
				if checkErr != nil {
					t.Fatal(checkErr)
				}
				assertCheckoutState(t, pool, checkout, "cancelled", "cancelled", 0)
			case "paid":
				if checkErr != nil {
					t.Fatal("paid lookup waited for future expiry", checkErr)
				}
				var eventID uuid.UUID
				if err := pool.QueryRow(ctx, `SELECT id FROM payment_provider_events WHERE checkout_job_id=$1`, checkJob.ID).Scan(&eventID); err != nil {
					t.Fatal(err)
				}
				body, _ := json.Marshal(map[string]string{"eventId": eventID.String()})
				if err := service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: body}); err != nil {
					t.Fatal(err)
				}
				assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
			}
			for _, sql := range []string{`UPDATE product_checkout_lookups SET result='{}' WHERE job_id=$1`, `DELETE FROM product_checkout_lookups WHERE job_id=$1`} {
				if _, err := pool.Exec(ctx, sql, job.ID); err == nil {
					t.Fatal("lookup evidence mutable")
				}
			}
			down, err := os.ReadFile("../platform/database/migrations/0087_product_checkout_lookup.down.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard checkout lookup") {
				t.Fatal("rollback discarded evidence or failed for wrong reason", err)
			}
		})
	}
}

func TestProductCheckoutLookupUncertainty(t *testing.T) {
	for _, scenario := range []string{"not_found", "ambiguous", "incomplete", "merchant_changed", "missing_snapshot", "forged_job", "callback_wins", "different_payment_wins", "binding_changed"} {
		t.Run(scenario, func(t *testing.T) {
			pool, service, runtime, checkout, buyer, job := pendingCheckoutLookupFixture(t)
			ctx := context.Background()
			originalLookup := runtime.lookup
			runtime.lookup = func(ctx context.Context, input CheckoutLookupRequest) (CheckoutLookupResult, error) {
				result, err := originalLookup(ctx, input)
				switch scenario {
				case "not_found":
					result.Outcome = "not_found"
					result.Observation = nil
					result.Matches = []string{}
				case "ambiguous":
					result.Outcome = "ambiguous"
					result.Observation = nil
					result.Scanned = 2
					result.Matches = append(result.Matches, "cs_lookup_duplicate")
				case "incomplete":
					result.Outcome = "incomplete"
					result.Observation = nil
					result.Pages = 10
					result.Scanned = 1000
				case "callback_wins", "different_payment_wins":
					processCheckoutSignedPayment(t, service, checkout, "evt_lookup_callback_wins", runtime.observation.ProviderCheckoutID)
					runtime.observation.Status = "complete"
					runtime.observation.PaymentStatus = "paid"
					runtime.observation.IntentStatus = "succeeded"
					runtime.observation.ProviderPaymentID = "pi_workflow123"
					runtime.observation.ProviderChargeID = "ch_lookup_existing"
					runtime.observation.AmountReceived = checkout.AmountCents
					if scenario == "different_payment_wins" {
						runtime.observation.ProviderPaymentID = "pi_lookup_other"
					}
				case "binding_changed":
					if _, err := pool.Exec(ctx, `UPDATE payment_intents SET amount_cents=amount_cents+100 WHERE id=$1`, checkout.PaymentID); err != nil {
						t.Fatal(err)
					}
				}
				return result, err
			}
			if scenario == "merchant_changed" {
				identity, _ := runtime.ProductCheckoutIdentity(ctx)
				identity.MerchantID = "acct_othermerchant"
				runtime.identityOverride = &identity
			}
			if scenario == "missing_snapshot" {
				if _, err := pool.Exec(ctx, `TRUNCATE product_checkout_dispatches, product_checkout_requests`); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "forged_job" {
				job.ID = uuid.New()
			}
			err := service.HandleProductCheckoutLookupJob(ctx, job)
			if scenario == "callback_wins" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("uncertain lookup accepted")
			}
			earlyFailure := oneOf(scenario, "merchant_changed", "missing_snapshot", "forged_job")
			if earlyFailure && runtime.reads.Load() != 0 {
				t.Fatal("lookup bypassed identity/job guard")
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_checkout_lookups WHERE payment_id=$1`, checkout.PaymentID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if (count == 0) != earlyFailure {
				t.Fatal("lookup evidence count", count)
			}
			if !earlyFailure {
				before := runtime.reads.Load()
				repeatErr := service.HandleProductCheckoutLookupJob(ctx, job)
				if (repeatErr == nil) != (scenario == "callback_wins") || runtime.reads.Load() != before {
					t.Fatal("uncertain result changed on job replay", repeatErr)
				}
			}
			if oneOf(scenario, "callback_wins", "different_payment_wins") {
				assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
			} else {
				assertCheckoutState(t, pool, checkout, "checkout_pending", "payment_pending", 0)
			}
			if len(runtime.requests) != 1 {
				t.Fatal("lookup caused another money request")
			}
			if scenario == "ambiguous" {
				if _, _, err := service.BeginProductCheckout(ctx, buyer, checkout.ResourceID, "lost-response-command", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, checkout.ResourceID)); !errors.Is(err, ErrCheckoutReconciliation) {
					t.Fatal("ambiguous match reopened checkout", err)
				}
				processCheckoutSignedPayment(t, service, checkout, "evt_ambiguous_later_payment", runtime.observation.ProviderCheckoutID)
			}
			if oneOf(scenario, "ambiguous", "different_payment_wins") {
				order, err := marketplace.NewService(pool).GetOrder(ctx, buyer, checkout.OrderID)
				if err != nil || order.CanRequestRefund || order.RefundUnavailableReason != "reconciliation_required" {
					t.Fatal("ambiguous funds unlocked refunds", err)
				}
			}
		})
	}
}

func TestProductCheckoutLookupPersistentReview(t *testing.T) {
	for _, outcome := range []string{"ambiguous", "state_changed"} {
		t.Run(outcome, func(t *testing.T) {
			pool, service, runtime, checkout, buyer, job := pendingCheckoutLookupFixture(t)
			ctx := context.Background()
			original := runtime.lookup
			runtime.lookup = func(ctx context.Context, input CheckoutLookupRequest) (CheckoutLookupResult, error) {
				result, err := original(ctx, input)
				if outcome == "ambiguous" {
					result.Outcome = "ambiguous"
					result.Observation = nil
					result.Scanned = 2
					result.Matches = append(result.Matches, "cs_another_candidate")
				} else {
					if _, err := pool.Exec(ctx, `UPDATE payment_intents SET provider_checkout_id='cs_other_won' WHERE id=$1`, checkout.PaymentID); err != nil {
						return result, err
					}
				}
				return result, err
			}
			if err := service.HandleProductCheckoutLookupJob(ctx, job); !errors.Is(err, ErrCheckoutReconciliation) {
				t.Fatal("expected persistent uncertainty", err)
			}
			// A subsequent bounded lookup can find only one candidate. Earlier evidence
			// must still block both old-key and new-key URL reuse.
			if _, err := pool.Exec(ctx, `UPDATE jobs SET status='failed' WHERE id=$1`, job.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE payment_intents SET provider_checkout_id=NULL WHERE id=$1`, checkout.PaymentID); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,$2,20) RETURNING id`, job.Kind, job.Payload).Scan(&job.ID); err != nil {
				t.Fatal(err)
			}
			runtime.lookup = original
			if err := service.HandleProductCheckoutLookupJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"lost-response-command", "new-command-after-lookup"} {
				got, _, err := service.BeginProductCheckout(ctx, buyer, checkout.ResourceID, key, "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, checkout.ResourceID))
				if !errors.Is(err, ErrCheckoutReconciliation) || got.CheckoutURL != "" {
					t.Fatalf("unsafe link returned: %#v %v", got, err)
				}
			}
			// Expiring just the selected candidate cannot prove that the other session
			// never collected funds; keep the original active purchase slot occupied.
			runtime.observation.Status = "expired"
			runtime.observation.ExpiresAt = time.Now().Add(-time.Minute)
			if _, err := pool.Exec(ctx, `UPDATE payment_intents SET checkout_expires_at=now()-interval '1 minute' WHERE id=$1`, checkout.PaymentID); err != nil {
				t.Fatal(err)
			}
			if err := service.HandleProductCheckoutCheckJob(ctx, locatedCheckJob(t, pool, job.ID)); !errors.Is(err, ErrCheckoutReconciliation) {
				t.Fatal("ambiguity cancelled via a single expired session", err)
			}
			assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
			if len(runtime.requests) != 1 {
				t.Fatal("created another checkout")
			}
		})
	}
}

func TestProductCheckoutLookupConcurrentWorkers(t *testing.T) {
	pool, service, runtime, checkout, _, job := pendingCheckoutLookupFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var startingVersion int
	if err := pool.QueryRow(ctx, `SELECT version FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&startingVersion); err != nil {
		t.Fatal(err)
	}
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	original := runtime.lookup
	runtime.lookup = func(ctx context.Context, input CheckoutLookupRequest) (CheckoutLookupResult, error) {
		arrived <- struct{}{}
		select {
		case <-release:
			return original(ctx, input)
		case <-ctx.Done():
			return CheckoutLookupResult{}, ctx.Err()
		}
	}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- service.HandleProductCheckoutLookupJob(ctx, job) }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-arrived:
		case <-ctx.Done():
			t.Fatal("concurrent readers did not start")
		}
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var evidence, checks, events, version int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM product_checkout_lookups WHERE payment_id=$1),
 (SELECT count(*) FROM jobs WHERE kind=$2 AND payload->>'paymentId'=$1::text),
 (SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.lookup_completed'),version
 FROM payment_intents WHERE id=$1`, checkout.PaymentID, ProductCheckoutCheckJobKind).Scan(&evidence, &checks, &events, &version); err != nil {
		t.Fatal(err)
	}
	if evidence != 1 || checks != 1 || events != 1 || version != startingVersion+1 {
		t.Fatalf("duplicate recovery: evidence=%d jobs=%d events=%d version=%d", evidence, checks, events, version)
	}
	before := runtime.reads.Load()
	if err := service.HandleProductCheckoutLookupJob(ctx, job); err != nil || runtime.reads.Load() != before {
		t.Fatal("durable result replay queried again", err)
	}
}
