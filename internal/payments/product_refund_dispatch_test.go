package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

type durableProductRefundRuntime struct {
	productCheckoutRuntime
	mu            sync.Mutex
	failNext      bool
	operations    []uuid.UUID
	metadataFlags []bool
}

func (r *durableProductRefundRuntime) CreateRefund(_ context.Context, input RefundRequest) (Refund, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.operations = append(r.operations, input.OperationID)
	r.metadataFlags = append(r.metadataFlags, input.IncludeOperationMetadata)
	if r.failNext {
		r.failNext = false
		return Refund{}, errors.New("refund response lost")
	}
	return Refund{ProviderID: "re_" + strings.ReplaceAll(input.OperationID.String(), "-", ""),
		ProviderPaymentID: input.ProviderPaymentID, AmountCents: input.AmountCents, Currency: input.Currency, Status: "pending"}, nil
}

func fulfilledRefundFixture(t *testing.T, pool *pgxpool.Pool, runtime ProviderRuntime) (*Service, Checkout, uuid.UUID, uuid.UUID, time.Time) {
	t.Helper()
	ctx := context.Background()
	buyer, _, _, product := newProductCheckoutFixture(t, pool)
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(runtime))
	checkout, _, err := service.BeginProductCheckout(ctx, buyer, product, "durable-refund-checkout", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	service.verifier.now = func() time.Time { return now }
	receivePaymentWorkflowEvent(t, service, productPaidEvent(checkout.PaymentID, product, now.Unix(), 1900), now)
	repository := jobs.NewRepository(pool)
	job, err := repository.Claim(ctx, "refund-fixture", time.Minute)
	if err != nil || job.Kind != PaymentEventJobKind {
		t.Fatalf("claim payment: job=%#v err=%v", job, err)
	}
	if err := service.HandlePaymentEventJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(ctx, job, "refund-fixture"); err != nil {
		t.Fatal(err)
	}
	// Leave notification delivery queued but outside this worker test's window.
	if _, err := pool.Exec(ctx, `UPDATE jobs SET available_at=now()+interval '1 hour' WHERE kind='notification.deliver'`); err != nil {
		t.Fatal(err)
	}
	return service, checkout, buyer, product, now
}

func currentProductRefundJob(t *testing.T, pool *pgxpool.Pool, paymentID uuid.UUID) jobs.Job {
	t.Helper()
	job := jobs.Job{Kind: ProductRefundJobKind}
	if err := pool.QueryRow(context.Background(), `SELECT j.id,j.payload FROM jobs j
		JOIN payment_intents pi ON j.payload->>'paymentId'=pi.id::text
		JOIN orders o ON o.id=pi.order_id AND j.payload->>'operationId'=o.refund_operation_id::text
		WHERE j.kind=$1 AND pi.id=$2`, ProductRefundJobKind, paymentID).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	return job
}

func TestProductRefundDurableDispatch(t *testing.T) {
	for _, lostResponse := range []bool{false, true} {
		t.Run(fmt.Sprintf("lost_response_%t", lostResponse), func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			runtime := &durableProductRefundRuntime{failNext: lostResponse}
			service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, runtime)
			created, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "durable-refund-command", "test", "The delivered content does not meet the license description.")
			if err != nil || !created || len(runtime.operations) != 0 {
				t.Fatalf("refund must be accepted before dispatch: created=%t operations=%v err=%v", created, runtime.operations, err)
			}
			if created, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "durable-refund-command", "replay-before-dispatch", "The delivered content does not meet the license description."); err != nil || created || len(runtime.operations) != 0 {
				t.Fatalf("replay dispatched or recreated refund: created=%t operations=%v err=%v", created, runtime.operations, err)
			}
			assertProductRefundState(t, pool, checkout, "refund_pending", "refund_requested", "active", 0, 0)

			// A fresh service can recover the committed obligation without another
			// browser request, including when the first provider response was lost.
			restarted := newPaymentTestService(t, pool, service.config, NewRuntimeCatalog(runtime))
			repository := jobs.NewRepository(pool)
			job, err := repository.Claim(ctx, "refund-restart", time.Minute)
			if err != nil || job.Kind != ProductRefundJobKind {
				t.Fatalf("claim durable refund: job=%#v err=%v", job, err)
			}
			if lostResponse {
				cause := restarted.HandleProductRefundJob(ctx, job)
				if cause == nil || !jobs.ShouldRetry(cause) {
					t.Fatalf("lost response must be retryable: %v", cause)
				}
				if err := repository.Fail(ctx, job, "refund-restart", cause); err != nil {
					t.Fatal(err)
				}
				var status string
				if err := pool.QueryRow(ctx, `SELECT status FROM jobs WHERE id=$1`, job.ID).Scan(&status); err != nil || status != "queued" {
					t.Fatalf("failed dispatch was not requeued: status=%s err=%v", status, err)
				}
				// Advance only this isolated job's backoff to exercise the next lease.
				if _, err := pool.Exec(ctx, `UPDATE jobs SET available_at=now() WHERE id=$1`, job.ID); err != nil {
					t.Fatal(err)
				}
				previous := job
				job, err = repository.Claim(ctx, "refund-restart", time.Minute)
				if err != nil || job.ID != previous.ID || job.LeaseToken == previous.LeaseToken || job.Attempts != previous.Attempts+1 {
					t.Fatalf("retry did not acquire a new lease for the same job: job=%#v err=%v", job, err)
				}
			}
			results := make(chan error, 8)
			for i := 0; i < cap(results); i++ {
				go func() { results <- restarted.HandleProductRefundJob(ctx, job) }()
			}
			for i := 0; i < cap(results); i++ {
				if err := <-results; err != nil {
					t.Error(err)
				}
			}
			if t.Failed() {
				t.FailNow()
			}
			if err := repository.Complete(ctx, job, "refund-restart"); err != nil {
				t.Fatal(err)
			}
			if _, err := restarted.BeginProductRefund(ctx, buyer, checkout.OrderID, "durable-refund-command", "replay", "The delivered content does not meet the license description."); err != nil {
				t.Fatal(err)
			}
			var queued, events int
			var operation uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT o.refund_operation_id,
				(SELECT count(*) FROM jobs WHERE kind=$2 AND payload->>'paymentId'=$3),
				(SELECT count(*) FROM payment_intent_events WHERE payment_id=$4 AND event_type='refund.provider_requested')
				FROM orders o WHERE o.id=$1`, checkout.OrderID, ProductRefundJobKind, checkout.PaymentID.String(), checkout.PaymentID).Scan(&operation, &queued, &events); err != nil {
				t.Fatal(err)
			}
			wantCalls := 1
			if lostResponse {
				wantCalls = 2
			}
			if queued != 1 || events != 1 || len(runtime.operations) != wantCalls {
				t.Fatalf("duplicate refund: jobs=%d events=%d operations=%v", queued, events, runtime.operations)
			}
			for _, dispatched := range runtime.operations {
				if dispatched != operation {
					t.Fatal("recovery changed provider idempotency key")
				}
			}
			assertProductRefundState(t, pool, checkout, "refund_pending", "refund_requested", "active", 0, 0)
		})
	}
}

func TestProductRefundQueueFailureRollsBackRequest(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	runtime := &durableProductRefundRuntime{}
	service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, runtime)
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_refund_job() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN IF NEW.kind='payment.refund_product' THEN RAISE EXCEPTION 'refund queue unavailable'; END IF; RETURN NEW; END $$;
	CREATE TRIGGER reject_refund_job BEFORE INSERT ON jobs FOR EACH ROW EXECUTE FUNCTION reject_refund_job()`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "queue-failure-refund", "test", "The delivered content does not meet the license description."); err == nil {
		t.Fatal("queue failure accepted refund")
	}
	assertProductRefundState(t, pool, checkout, "paid", "fulfilled", "active", 0, 0)
	var events int
	var operation *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT refund_operation_id,(SELECT count(*) FROM payment_intent_events WHERE payment_id=$2 AND event_type='refund.requested') FROM orders WHERE id=$1`, checkout.OrderID, checkout.PaymentID).Scan(&operation, &events); err != nil {
		t.Fatal(err)
	}
	if operation != nil || events != 0 || len(runtime.operations) != 0 {
		t.Fatalf("queue failure left side effects: operation=%v events=%d requests=%d", operation, events, len(runtime.operations))
	}
}

func TestProductRefundSupersededJobDoesNotDispatch(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	runtime := &durableProductRefundRuntime{}
	service, checkout, buyer, product, now := fulfilledRefundFixture(t, pool, runtime)
	if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "first-refund-command", "test", "The delivered content does not meet the license description."); err != nil {
		t.Fatal(err)
	}
	oldJob := currentProductRefundJob(t, pool, checkout.PaymentID)
	if err := service.HandleProductRefundJob(ctx, oldJob); err != nil {
		t.Fatal(err)
	}
	var refundID string
	if err := pool.QueryRow(ctx, `SELECT provider_refund_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&refundID); err != nil {
		t.Fatal(err)
	}
	receipt := receivePaymentWorkflowEvent(t, service, productRefundEvent("evt_durablefailed", refundID, "failed", checkout.PaymentID, product, now.Unix(), checkout.AmountCents), now)
	if err := service.HandlePaymentEventJob(ctx, jobs.Job{Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductRefundJob(ctx, oldJob); err != nil {
		t.Fatalf("finished attempt should drain its job: %v", err)
	}
	order, err := marketplace.NewService(pool).GetOrder(ctx, buyer, checkout.OrderID)
	if err != nil || !order.CanRequestRefund || order.AssetID == nil {
		t.Fatalf("failed refund did not restore request capability: %+v %v", order, err)
	}
	owned, err := assets.NewService(pool, t.TempDir()).GetOwned(ctx, buyer, *order.AssetID)
	if err != nil || owned.Provenance == nil || owned.Provenance.Purchase == nil || !owned.Provenance.Purchase.CanDownload || !owned.Provenance.Purchase.CanReuse {
		t.Fatalf("failed refund hid active purchase rights: %+v %v", owned.Provenance, err)
	}
	runtime.failNext = true
	if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "second-refund-command", "test", "Retry the refund after the provider confirmed failure."); err != nil {
		t.Fatal(err)
	}
	newJob := currentProductRefundJob(t, pool, checkout.PaymentID)
	if err := service.HandleProductRefundJob(ctx, newJob); err == nil {
		t.Fatal("expected lost response")
	}
	if err := service.HandleProductRefundJob(ctx, oldJob); err != nil {
		t.Fatal(err)
	}
	if len(runtime.operations) != 2 {
		t.Fatal("stale job dispatched the new operation")
	}
	if err := service.HandleProductRefundJob(ctx, newJob); err != nil {
		t.Fatal(err)
	}
	if len(runtime.operations) != 3 || runtime.operations[0] == runtime.operations[1] || runtime.operations[1] != runtime.operations[2] {
		t.Fatalf("wrong recovery operations: %v", runtime.operations)
	}
}

func TestProductRefundLegacyJobIsolation(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	runtime := &durableProductRefundRuntime{failNext: true}
	service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, runtime)
	if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "legacy-refund-command", "test", "The delivered content does not meet the license description."); err != nil {
		t.Fatal(err)
	}
	current := currentProductRefundJob(t, pool, checkout.PaymentID)
	if err := service.HandleProductRefundJob(ctx, current); err == nil {
		t.Fatal("expected lost response")
	}
	var legacy jobs.Job
	if err := pool.QueryRow(ctx, `UPDATE jobs SET payload=payload-'operationId'
		WHERE id=$1 RETURNING id,payload`, current.ID).Scan(&legacy.ID, &legacy.Payload); err != nil {
		t.Fatal(err)
	}
	var stale jobs.Job
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,created_at)
		SELECT $1,$2,refund_requested_at-interval '1 second' FROM orders WHERE id=$3 RETURNING id,payload`,
		ProductRefundJobKind, legacy.Payload, checkout.OrderID).Scan(&stale.ID, &stale.Payload); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductRefundJob(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if len(runtime.operations) != 1 {
		t.Fatal("legacy job from an earlier attempt dispatched")
	}
	if err := service.HandleProductRefundJob(ctx, jobs.Job{Payload: legacy.Payload}); err == nil {
		t.Fatal("unpersisted legacy job dispatched")
	}
	if err := service.HandleProductRefundJob(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if len(runtime.operations) != 2 || runtime.operations[0] != runtime.operations[1] {
		t.Fatalf("legacy recovery changed operation: %v", runtime.operations)
	}
}

func TestProductRefundReplayRestoresMissingJob(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy_%t", legacy), func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			runtime := &durableProductRefundRuntime{}
			service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, runtime)
			const key = "restore-refund-command"
			const reason = "The delivered content does not meet the license description."
			if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, key, "test", reason); err != nil {
				t.Fatal(err)
			}
			original := currentProductRefundJob(t, pool, checkout.PaymentID)
			// Emulate a request accepted before initial refunds had durable jobs.
			if _, err := pool.Exec(ctx, `DELETE FROM jobs WHERE id=$1`, original.ID); err != nil {
				t.Fatal(err)
			}
			if created, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, key, "restore", reason); err != nil || created {
				t.Fatalf("restore pending request: created=%t err=%v", created, err)
			}
			restored := currentProductRefundJob(t, pool, checkout.PaymentID)
			if restored.ID == original.ID || string(restored.Payload) != string(original.Payload) {
				t.Fatalf("restoration changed the refund operation: original=%s restored=%s", original.Payload, restored.Payload)
			}
			if legacy {
				if _, err := pool.Exec(ctx, `UPDATE jobs SET payload=payload-'operationId' WHERE id=$1`, restored.ID); err != nil {
					t.Fatal(err)
				}
			}
			for _, status := range []string{"queued", "failed"} {
				if _, err := pool.Exec(ctx, `UPDATE jobs SET status=$2,attempts=CASE WHEN $2='failed' THEN max_attempts ELSE attempts END WHERE id=$1`, restored.ID, status); err != nil {
					t.Fatal(err)
				}
				if created, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, key, "repeat", reason); err != nil || created {
					t.Fatalf("replay %s request: created=%t err=%v", status, created, err)
				}
				var count int
				var actualStatus string
				if err := pool.QueryRow(ctx, `SELECT count(*),min(status) FROM jobs WHERE kind=$1 AND payload->>'paymentId'=$2`, ProductRefundJobKind, checkout.PaymentID.String()).Scan(&count, &actualStatus); err != nil {
					t.Fatal(err)
				}
				if count != 1 || actualStatus != status || len(runtime.operations) != 0 {
					t.Fatalf("replay recreated or dispatched %s job: count=%d status=%s operations=%v", status, count, actualStatus, runtime.operations)
				}
			}
			assertProductRefundState(t, pool, checkout, "refund_pending", "refund_requested", "active", 0, 0)
		})
	}
}
