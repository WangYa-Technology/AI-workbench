package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgconn"
)

func refundOperationEvent(t *testing.T, eventID, refundID, status string, paymentID, productID uuid.UUID, now time.Time, operation string) []byte {
	t.Helper()
	var event map[string]any
	if err := json.Unmarshal(productRefundEvent(eventID, refundID, status, paymentID, productID, now.Unix(), 1900), &event); err != nil {
		t.Fatal(err)
	}
	object := event["data"].(map[string]any)["object"].(map[string]any)
	object["metadata"].(map[string]any)["hcai_refund_operation_id"] = operation
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestProductRefundLostResponseSignedCorrelation(t *testing.T) {
	for _, terminal := range []string{"succeeded", "failed", "canceled"} {
		t.Run(terminal, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			runtime := &durableProductRefundRuntime{failNext: true}
			service, checkout, buyer, product, now := fulfilledRefundFixture(t, pool, runtime)
			if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "lost-response-refund", "correlation-test", "The delivered content does not match its description."); err != nil {
				t.Fatal(err)
			}
			job := currentProductRefundJob(t, pool, checkout.PaymentID)
			if err := service.HandleProductRefundJob(ctx, job); err == nil || !jobs.ShouldRetry(err) {
				t.Fatalf("expected retryable lost response: %v", err)
			}
			operation := runtime.operations[0]
			const refundID = "re_signedrecovery"
			process := func(body []byte) error {
				header := "t=" + fmt.Sprint(now.Unix()) + ",v1=" + stripeSignature(testStripeWebhookSecret, now.Unix(), body)
				receipt, err := service.ReceiveStripeWebhook(ctx, body, header)
				if err != nil {
					return err
				}
				return service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))})
			}
			// Signed identity alone does not waive operation, payment, amount or currency checks.
			for i, mutation := range []func(map[string]any){
				func(object map[string]any) { delete(object["metadata"].(map[string]any), "hcai_refund_operation_id") },
				func(object map[string]any) {
					object["metadata"].(map[string]any)["hcai_refund_operation_id"] = uuid.New().String()
				},
				func(object map[string]any) { object["amount"] = 1899 },
				func(object map[string]any) { object["payment_intent"] = "pi_unrelated" },
				func(object map[string]any) { object["currency"] = "eur" },
			} {
				var event map[string]any
				if err := json.Unmarshal(refundOperationEvent(t, fmt.Sprintf("evt_badcorrelation%d", i), refundID, "succeeded", checkout.PaymentID, product, now, operation.String()), &event); err != nil {
					t.Fatal(err)
				}
				mutation(event["data"].(map[string]any)["object"].(map[string]any))
				body, err := json.Marshal(event)
				if err != nil {
					t.Fatal(err)
				}
				if err := process(body); err == nil {
					t.Fatalf("invalid correlation %d accepted", i)
				}
				var recorded *string
				if err := pool.QueryRow(ctx, `SELECT provider_refund_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&recorded); err != nil || recorded != nil {
					t.Fatalf("invalid evidence changed refund binding: %v %v", recorded, err)
				}
				assertProductRefundState(t, pool, checkout, "refund_pending", "refund_requested", "active", 0, 0)
			}
			if err := process(refundOperationEvent(t, "evt_correlatedpending", refundID, "pending", checkout.PaymentID, product, now, operation.String())); err != nil {
				t.Fatal(err)
			}
			assertProductRefundState(t, pool, checkout, "refund_pending", "refund_requested", "active", 0, 0)
			// Evidence of the accepted request prevents another remote dispatch.
			if err := service.HandleProductRefundJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if len(runtime.operations) != 1 {
				t.Fatal("callback recovery sent another refund request")
			}
			if err := process(refundOperationEvent(t, "evt_differentrefund", "re_wrongrefund", "succeeded", checkout.PaymentID, product, now, operation.String())); err == nil {
				t.Fatal("mismatched provider refund overwrote binding")
			}
			if err := process(refundOperationEvent(t, "evt_correlatedterminal", refundID, terminal, checkout.PaymentID, product, now, operation.String())); err != nil {
				t.Fatal(err)
			}
			if terminal == "succeeded" {
				assertProductRefundState(t, pool, checkout, "refunded", "refunded", "refunded", 0, 0)
				if err := process(refundOperationEvent(t, "evt_terminalduplicate", refundID, terminal, checkout.PaymentID, product, now, operation.String())); err != nil {
					t.Fatal(err)
				}
				for _, late := range []string{"pending", "requires_action", "failed", "canceled"} {
					if err := process(refundOperationEvent(t, "evt_late"+late, refundID, late, checkout.PaymentID, product, now, operation.String())); err != nil {
						t.Fatal(err)
					}
					assertProductRefundState(t, pool, checkout, "refunded", "refunded", "refunded", 0, 0)
				}
				var confirmations int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='refund.confirmed'`, checkout.PaymentID).Scan(&confirmations); err != nil || confirmations != 1 {
					t.Fatalf("duplicate confirmation: %d %v", confirmations, err)
				}
			} else {
				assertProductRefundState(t, pool, checkout, "paid", "fulfilled", "active", 0, 0)
			}
		})
	}
}

func TestStripeRefundOperationMetadataValidation(t *testing.T) {
	for _, value := range []string{"", "invalid-operation", uuid.Nil.String()} {
		body := refundOperationEvent(t, "evt_invalidoperation", "re_invalidoperation", "succeeded", uuid.New(), uuid.New(), time.Now(), value)
		if _, err := minimizeStripeEvent(body, testStripeAPIVersion, false); err == nil {
			t.Fatalf("accepted invalid operation metadata %q", value)
		}
	}
}

func TestProductRefundCorrelationPreservesLegacyDispatch(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	runtime := &durableProductRefundRuntime{failNext: true}
	service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, runtime)
	// Model a legacy request before its immutable attempt is recorded. Editing
	// the order after a modern request is frozen is a protocol conflict, not a
	// legacy operation, and must never change a replay's Stripe parameters.
	if _, err := pool.Exec(ctx, `CREATE FUNCTION legacy_refund_protocol_fixture() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF NEW.refund_operation_id IS DISTINCT FROM OLD.refund_operation_id THEN
 NEW.refund_correlation_enabled:=false; END IF; RETURN NEW; END $$;
 CREATE TRIGGER legacy_refund_protocol_fixture BEFORE UPDATE ON orders
 FOR EACH ROW EXECUTE FUNCTION legacy_refund_protocol_fixture()`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "legacy-correlation-command", "test", "The delivered content does not match its description."); err != nil {
		t.Fatal(err)
	}
	job := currentProductRefundJob(t, pool, checkout.PaymentID)
	if err := service.HandleProductRefundJob(ctx, job); err == nil {
		t.Fatal("expected lost response")
	}
	if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "legacy-correlation-command", "replay", "The delivered content does not match its description."); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductRefundJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if len(runtime.operations) != 2 || runtime.operations[0] != runtime.operations[1] || runtime.metadataFlags[0] || runtime.metadataFlags[1] {
		t.Fatal("legacy replay changed operation or request metadata")
	}
}

func TestProductRefundCorrelationRejectsChangedReplayProtocol(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	runtime := &durableProductRefundRuntime{failNext: true}
	service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, runtime)
	if _, err := service.BeginProductRefund(t.Context(), buyer, checkout.OrderID, "protocol-conflict", "test", "The delivered content does not match its description."); err != nil {
		t.Fatal(err)
	}
	job := currentProductRefundJob(t, pool, checkout.PaymentID)
	if err := service.HandleProductRefundJob(t.Context(), job); err == nil || len(runtime.operations) != 1 || !runtime.metadataFlags[0] {
		t.Fatalf("expected original dispatch with lost response: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE orders SET refund_correlation_enabled=false WHERE id=$1`, checkout.OrderID); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductRefundJob(t.Context(), job); !errors.Is(err, ErrCheckoutReconciliation) || len(runtime.operations) != 1 {
		t.Fatalf("changed Stripe request was replayed: %v", err)
	}
	var held bool
	if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM product_refund_review WHERE payment_id=$1)`, checkout.PaymentID).Scan(&held); err != nil || !held {
		t.Fatalf("protocol mismatch was absent from review: %t %v", held, err)
	}
}

type earlyRefundCallbackRuntime struct {
	productCheckoutRuntime
	entered chan RefundRequest
	release chan struct{}
	lost    bool
	calls   atomic.Int32
}

func (r *earlyRefundCallbackRuntime) CreateRefund(ctx context.Context, input RefundRequest) (Refund, error) {
	r.calls.Add(1)
	select {
	case r.entered <- input:
	case <-ctx.Done():
		return Refund{}, ctx.Err()
	}
	select {
	case <-r.release:
	case <-ctx.Done():
		return Refund{}, ctx.Err()
	}
	if r.lost {
		return Refund{}, errors.New("provider accepted but response lost")
	}
	return Refund{ProviderID: "re_earlycallback", ProviderPaymentID: input.ProviderPaymentID, AmountCents: input.AmountCents, Currency: input.Currency, Status: "pending"}, nil
}

func TestProductRefundCallbackBeforeResponse(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprintf("lost_%t", lost), func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			runtime := &earlyRefundCallbackRuntime{entered: make(chan RefundRequest, 1), release: make(chan struct{}), lost: lost}
			var once sync.Once
			release := func() { once.Do(func() { close(runtime.release) }) }
			defer release()
			service, checkout, buyer, product, now := fulfilledRefundFixture(t, pool, runtime)
			if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "early-refund-callback", "test", "The delivered content does not match the description."); err != nil {
				t.Fatal(err)
			}
			job := currentProductRefundJob(t, pool, checkout.PaymentID)
			dispatched := make(chan error, 1)
			go func() { dispatched <- service.HandleProductRefundJob(ctx, job) }()
			var input RefundRequest
			select {
			case input = <-runtime.entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if !input.IncludeOperationMetadata {
				t.Fatal("new operation omitted signed correlation")
			}
			receipt := receivePaymentWorkflowEvent(t, service, refundOperationEvent(t, "evt_earlycallback", "re_earlycallback", "succeeded", checkout.PaymentID, product, now, input.OperationID.String()), now)
			eventJob := jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}
			processed := make(chan error, 1)
			go func() { processed <- service.HandlePaymentEventJob(ctx, eventJob) }()
			// Observe the actual database lock wait, not merely goroutine scheduling.
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
				 WHERE wait_event_type='Lock' AND query LIKE '%SELECT o.refund_operation_id,pi.status FROM payment_intents pi%'
				 AND cardinality(pg_blocking_pids(pid))>0)`).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case <-ticker.C:
				case <-ctx.Done():
					t.Fatal("callback never reached refund lock")
				}
			}
			release()
			select {
			case err := <-dispatched:
				if (err != nil) != lost {
					t.Fatalf("unexpected dispatch outcome: %v", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			var eventErr error
			select {
			case eventErr = <-processed:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if eventErr != nil {
				var conflict *pgconn.PgError
				if !errors.As(eventErr, &conflict) || conflict.Code != "40001" || !jobs.ShouldRetry(eventErr) {
					t.Fatal(eventErr)
				}
				if err := service.HandlePaymentEventJob(ctx, eventJob); err != nil {
					t.Fatal(err)
				}
			}
			assertProductRefundState(t, pool, checkout, "refunded", "refunded", "refunded", 0, 0)
			if err := service.HandleProductRefundJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if runtime.calls.Load() != 1 {
				t.Fatal("terminal callback led to another refund")
			}
		})
	}
}
