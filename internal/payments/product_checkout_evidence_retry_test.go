package payments

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestProductCheckoutCheckRetainsResponseOnTransactionAbort(t *testing.T) {
	for _, code := range []string{"40001", "40P01", "23514"} {
		t.Run(code, func(t *testing.T) {
			pool, service, runtime, checkout, _, job := checkoutCheckFixture(t)
			ctx := t.Context()
			reads := 0
			runtime.read = func(_ context.Context, r CheckoutReadRequest) (CheckoutObservation, error) {
				reads++
				if reads > 1 {
					t.Error("database retry discarded the original authenticated response")
					return CheckoutObservation{}, errors.New("subsequent provider read unavailable")
				}
				return CheckoutObservation{ProviderCheckoutID: r.ProviderCheckoutID, AmountCents: r.AmountCents, Currency: r.Currency, LiveMode: r.LiveMode, Status: "complete", PaymentStatus: "paid", ProviderPaymentID: "pi_workflow123", ProviderChargeID: "ch_workflow123", IntentStatus: "succeeded", AmountReceived: r.AmountCents, ExpiresAt: time.Now().Add(-time.Minute)}, nil
			}
			// Abort after the query event and payment event have been inserted but
			// before the fulfillment job can commit. Sequences survive rollbacks.
			quarantineExec(t, pool, `CREATE SEQUENCE checkout_evidence_write_attempt`)
			quarantineExec(t, pool, fmt.Sprintf(`CREATE FUNCTION abort_checkout_evidence_test() RETURNS trigger AS $$ BEGIN
 IF nextval('checkout_evidence_write_attempt')<=2 THEN RAISE EXCEPTION 'injected transaction abort' USING ERRCODE='%s'; END IF;
 RETURN NEW; END; $$ LANGUAGE plpgsql;
 CREATE TRIGGER abort_checkout_evidence_test BEFORE INSERT ON jobs FOR EACH ROW WHEN(NEW.kind='payment.process_event') EXECUTE FUNCTION abort_checkout_evidence_test()`, code))
			err := service.HandleProductCheckoutCheckJob(ctx, job)
			if code == "23514" {
				var pgerr *pgconn.PgError
				if !errors.As(err, &pgerr) || pgerr.Code != code {
					t.Fatal("constraint error was hidden", err)
				}
				quarantineCount(t, pool, `SELECT last_value FROM checkout_evidence_write_attempt`, 1)
				quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE checkout_job_id=$1`, 0, job.ID)
				quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.queried'`, 0, checkout.PaymentID)
				assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
				return
			}
			if err != nil {
				t.Fatal("verified payment lost after a proven transaction abort", err)
			}
			quarantineCount(t, pool, `SELECT last_value FROM checkout_evidence_write_attempt`, 3)
			quarantineCount(t, pool, `SELECT count(*) FROM payment_provider_events WHERE checkout_job_id=$1 AND payment_status='paid'`, 1, job.ID)
			quarantineCount(t, pool, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.queried'`, 1, checkout.PaymentID)
			var fulfillment jobs.Job
			if err = pool.QueryRow(ctx, `SELECT j.id,j.kind,j.payload FROM jobs j JOIN payment_provider_events e ON j.payload->>'eventId'=e.id::text WHERE e.checkout_job_id=$1 AND j.kind=$2`, job.ID, PaymentEventJobKind).Scan(&fulfillment.ID, &fulfillment.Kind, &fulfillment.Payload); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err = service.HandlePaymentEventJob(ctx, fulfillment); err != nil {
					t.Fatal(err)
				}
			}
			assertCheckoutState(t, pool, checkout, "paid", "fulfilled", 1)
			if reads != 1 {
				t.Fatal("unexpected provider reads", reads)
			}
		})
	}
}

func TestProductCheckoutLookupRetainsResponseOnTransactionAbort(t *testing.T) {
	for _, code := range []string{"40001", "40P01", "23514"} {
		for _, outcome := range []string{"found", "not_found"} {
			t.Run(code+"/"+outcome, func(t *testing.T) {
				pool, service, runtime, checkout, _, job := pendingCheckoutLookupFixture(t)
				ctx := t.Context()
				runtime.lookup = func(_ context.Context, _ CheckoutLookupRequest) (CheckoutLookupResult, error) {
					if runtime.reads.Load() > 1 {
						t.Error("lookup response was discarded during database retry")
						return CheckoutLookupResult{}, errors.New("subsequent lookup unavailable")
					}
					if outcome == "not_found" {
						return CheckoutLookupResult{Outcome: outcome, Pages: 1}, nil
					}
					return CheckoutLookupResult{Outcome: outcome, Pages: 1, Scanned: 1, Matches: []string{runtime.observation.ProviderCheckoutID}, Observation: &runtime.observation}, nil
				}
				// The found branch has already updated the intent and queued the
				// follow-up check when this transaction is deliberately rolled back.
				quarantineExec(t, pool, `CREATE SEQUENCE checkout_lookup_write_attempt`)
				quarantineExec(t, pool, fmt.Sprintf(`CREATE FUNCTION abort_checkout_lookup_test() RETURNS trigger AS $$ BEGIN
 IF nextval('checkout_lookup_write_attempt')<=2 THEN RAISE EXCEPTION 'injected transaction abort' USING ERRCODE='%s'; END IF;
 RETURN NEW; END; $$ LANGUAGE plpgsql;
 CREATE TRIGGER abort_checkout_lookup_test BEFORE INSERT ON product_checkout_lookups FOR EACH ROW EXECUTE FUNCTION abort_checkout_lookup_test()`, code))
				err := service.HandleProductCheckoutLookupJob(ctx, job)
				if code == "23514" {
					var pgerr *pgconn.PgError
					if !errors.As(err, &pgerr) || pgerr.Code != code {
						t.Fatal("constraint error hidden", err)
					}
					quarantineCount(t, pool, `SELECT last_value FROM checkout_lookup_write_attempt`, 1)
					quarantineCount(t, pool, `SELECT count(*) FROM product_checkout_lookups WHERE job_id=$1`, 0, job.ID)
					quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE kind=$1 AND payload->>'paymentId'=$2`, 0, ProductCheckoutCheckJobKind, checkout.PaymentID.String())
					assertCheckoutState(t, pool, checkout, "checkout_pending", "payment_pending", 0)
					return
				}
				if outcome == "found" && err != nil || outcome == "not_found" && !errors.Is(err, ErrCheckoutReconciliation) {
					t.Fatal("lookup evidence lost after transaction abort", err)
				}
				quarantineCount(t, pool, `SELECT last_value FROM checkout_lookup_write_attempt`, 3)
				quarantineCount(t, pool, `SELECT count(*) FROM product_checkout_lookups WHERE job_id=$1 AND outcome=$2`, 1, job.ID, outcome)
				quarantineCount(t, pool, `SELECT count(*) FROM audit_events WHERE action='payment.checkout_lookup_completed' AND resource_id=$1`, 1, checkout.PaymentID)
				checks, status := 0, "checkout_pending"
				if outcome == "found" {
					checks, status = 1, "checkout_open"
				}
				quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE kind=$1 AND payload->>'paymentId'=$2`, checks, ProductCheckoutCheckJobKind, checkout.PaymentID.String())
				assertCheckoutState(t, pool, checkout, status, "payment_pending", 0)
				err = service.HandleProductCheckoutLookupJob(ctx, job)
				if outcome == "found" && err != nil || outcome == "not_found" && !errors.Is(err, ErrCheckoutReconciliation) || runtime.reads.Load() != 1 || len(runtime.requests) != 1 {
					t.Fatal("lookup replay changed its evidence or repeated checkout", err, runtime.reads.Load(), len(runtime.requests))
				}
			})
		}
	}
}

func TestProductCheckoutLookupPreservesVerifiedResultOnCancellation(t *testing.T) {
	for _, outcome := range []string{"found", "not_found", "invalid", "read_error"} {
		t.Run(outcome, func(t *testing.T) {
			pool, service, runtime, checkout, _, job := pendingCheckoutLookupFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			runtime.lookup = func(context.Context, CheckoutLookupRequest) (CheckoutLookupResult, error) {
				cancel()
				if outcome == "read_error" {
					return CheckoutLookupResult{}, context.Canceled
				}
				if outcome == "found" {
					return CheckoutLookupResult{Outcome: outcome, Pages: 1, Scanned: 1, Matches: []string{runtime.observation.ProviderCheckoutID}, Observation: &runtime.observation}, nil
				}
				return CheckoutLookupResult{Outcome: outcome, Pages: 1}, nil
			}
			err := service.HandleProductCheckoutLookupJob(ctx, job)
			switch outcome {
			case "found":
				if err != nil {
					t.Fatal("verified checkout location lost during shutdown", err)
				}
				assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
			case "not_found":
				if !errors.Is(err, ErrCheckoutReconciliation) {
					t.Fatal("verified unresolved outcome lost during shutdown", err)
				}
				assertCheckoutState(t, pool, checkout, "checkout_pending", "payment_pending", 0)
			default:
				if err == nil {
					t.Fatal("unverified lookup result accepted")
				}
				quarantineCount(t, pool, `SELECT count(*) FROM product_checkout_lookups WHERE job_id=$1`, 0, job.ID)
				return
			}
			quarantineCount(t, pool, `SELECT count(*) FROM product_checkout_lookups WHERE job_id=$1 AND outcome=$2`, 1, job.ID, outcome)
			if runtime.reads.Load() != 1 || len(runtime.requests) != 1 {
				t.Fatal("shutdown repeated remote operation")
			}
		})
	}
}

func TestProductCheckoutEvidenceRetriesCommitAbort(t *testing.T) {
	pool, service, runtime, checkout, _, job := pendingCheckoutLookupFixture(t)
	ctx := t.Context()
	// A deferred trigger aborts COMMIT itself, after all lookup mutations. The
	// transaction is known rolled back, unlike a lost connection during COMMIT.
	quarantineExec(t, pool, `CREATE SEQUENCE checkout_commit_abort_attempt;
 CREATE FUNCTION abort_checkout_commit_test() RETURNS trigger AS $$ BEGIN
 IF nextval('checkout_commit_abort_attempt')<=2 THEN RAISE EXCEPTION 'injected commit abort' USING ERRCODE='40001'; END IF;
 RETURN NEW; END; $$ LANGUAGE plpgsql;
 CREATE CONSTRAINT TRIGGER abort_checkout_commit_test AFTER INSERT ON product_checkout_lookups DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION abort_checkout_commit_test()`)
	if err := service.HandleProductCheckoutLookupJob(ctx, job); err != nil {
		t.Fatal("confirmed commit abort lost original lookup", err)
	}
	quarantineCount(t, pool, `SELECT last_value FROM checkout_commit_abort_attempt`, 3)
	quarantineCount(t, pool, `SELECT count(*) FROM product_checkout_lookups WHERE job_id=$1`, 1, job.ID)
	quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE kind=$1 AND payload->>'paymentId'=$2`, 1, ProductCheckoutCheckJobKind, checkout.PaymentID.String())
	quarantineCount(t, pool, `SELECT count(*) FROM audit_events WHERE action='payment.checkout_lookup_completed' AND resource_id=$1`, 1, checkout.PaymentID)
	assertCheckoutState(t, pool, checkout, "checkout_open", "payment_pending", 0)
	if runtime.reads.Load() != 1 || len(runtime.requests) != 1 {
		t.Fatal("commit retry repeated a provider operation")
	}
}

func TestProductCheckoutLookupEvidenceRetryBudget(t *testing.T) {
	pool, service, runtime, checkout, _, job := pendingCheckoutLookupFixture(t)
	quarantineExec(t, pool, `CREATE SEQUENCE checkout_persistent_abort_attempt;
 CREATE FUNCTION abort_checkout_persistently_test() RETURNS trigger AS $$ BEGIN
 PERFORM nextval('checkout_persistent_abort_attempt'); RAISE EXCEPTION 'persistent serialization failure' USING ERRCODE='40001';
 END; $$ LANGUAGE plpgsql;
 CREATE TRIGGER abort_checkout_persistently_test BEFORE INSERT ON product_checkout_lookups FOR EACH ROW EXECUTE FUNCTION abort_checkout_persistently_test()`)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	started := time.Now()
	err := service.HandleProductCheckoutLookupJob(ctx, job)
	elapsed := time.Since(started)
	if !errors.Is(err, context.DeadlineExceeded) || elapsed < 4500*time.Millisecond || elapsed > 8*time.Second || runtime.reads.Load() != 1 || len(runtime.requests) != 1 {
		t.Fatal("lookup write retries did not share one bounded budget", elapsed, runtime.reads.Load(), err)
	}
	var attempts int
	if err = pool.QueryRow(t.Context(), `SELECT last_value FROM checkout_persistent_abort_attempt`).Scan(&attempts); err != nil || attempts < 3 || attempts > 50 {
		t.Fatal("write retry backoff was not bounded", attempts, err)
	}
	quarantineCount(t, pool, `SELECT count(*) FROM product_checkout_lookups WHERE job_id=$1`, 0, job.ID)
	quarantineCount(t, pool, `SELECT count(*) FROM jobs WHERE kind=$1 AND payload->>'paymentId'=$2`, 0, ProductCheckoutCheckJobKind, checkout.PaymentID.String())
	quarantineCount(t, pool, `SELECT count(*) FROM audit_events WHERE action='payment.checkout_lookup_completed' AND resource_id=$1`, 0, checkout.PaymentID)
	assertCheckoutState(t, pool, checkout, "checkout_pending", "payment_pending", 0)
}

func TestPaymentEvidenceWriteDoesNotRetryUncertainCommit(t *testing.T) {
	for _, code := range []string{"40003", "08006", "23505"} {
		t.Run(code, func(t *testing.T) {
			calls := 0
			cause := &pgconn.PgError{Code: code, Message: "injected uncertain or permanent failure"}
			stored, err := retryPaymentEvidenceWrite(t.Context(), func(context.Context) (bool, error) {
				calls++
				return true, cause
			})
			if !stored || !errors.Is(err, cause) || calls != 1 {
				t.Fatal("uncertain commit was replayed or hidden", stored, calls, err)
			}
		})
	}
}
