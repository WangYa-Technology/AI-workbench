package payments

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Emulate when an operation was originally requested, before its immutable
// attempt is inserted. Never rewrite retained operation evidence to age it.
func refundRequestTimeFixture(t *testing.T, pool *pgxpool.Pool, expression string) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `CREATE FUNCTION refund_request_time_fixture() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN
 IF NEW.refund_operation_id IS DISTINCT FROM OLD.refund_operation_id THEN
 NEW.refund_requested_at := `+expression+`;
 END IF;
 RETURN NEW;
 END $$;
 CREATE TRIGGER refund_request_time_fixture BEFORE UPDATE ON orders
 FOR EACH ROW EXECUTE FUNCTION refund_request_time_fixture()`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestProductStripeRefundRetryWindow(t *testing.T) {
	cases := []struct {
		name, requested string
		allowed         bool
	}{
		{"within_window", "now()-interval '22 hours'", true},
		{"at_deadline", "now()-interval '23 hours'", false},
		{"expired", "now()-interval '48 hours'", false},
		{"future", "now()+interval '1 hour'", false},
		{"missing", "NULL", false},
		{"mutable_order_refreshed", "now()-interval '48 hours'", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			runtime := &durableProductRefundRuntime{}
			service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, runtime)
			refundRequestTimeFixture(t, pool, tc.requested)
			if _, err := service.BeginProductRefund(t.Context(), buyer, checkout.OrderID, "timed-refund", "test", "The delivered resource does not meet the agreed description."); err != nil {
				t.Fatal(err)
			}
			if tc.name == "mutable_order_refreshed" {
				if _, err := pool.Exec(t.Context(), `UPDATE orders SET refund_requested_at=now() WHERE id=$1`, checkout.OrderID); err != nil {
					t.Fatal(err)
				}
			}
			err := service.HandleProductRefundJob(t.Context(), currentProductRefundJob(t, pool, checkout.PaymentID))
			if tc.allowed {
				if err != nil || len(runtime.operations) != 1 {
					t.Fatalf("valid refund failed: calls=%d err=%v", len(runtime.operations), err)
				}
			} else {
				if !errors.Is(err, ErrCheckoutReconciliation) || jobs.ShouldRetry(err) || len(runtime.operations) != 0 {
					t.Fatalf("uncertain aged refund dispatched: calls=%d err=%v", len(runtime.operations), err)
				}
				var held bool
				if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM product_refund_review WHERE payment_id=$1)`, checkout.PaymentID).Scan(&held); err != nil || !held {
					t.Fatalf("expired operation absent from shared review gate: %t %v", held, err)
				}
				history, err := service.RefundHistory(t.Context(), checkout.PaymentID, "", 20)
				if err != nil || len(history.Items) != 1 || !history.Items[0].ReconciliationRequired {
					t.Fatalf("refund history omitted the review requirement: %+v %v", history, err)
				}
			}
			assertProductRefundState(t, pool, checkout, "refund_pending", "refund_requested", "active", 0, 0)
		})
	}
}

func TestProductStripeExpiredRefundQueryRecovery(t *testing.T) {
	for _, outcome := range []string{"succeeded", "failed", "missing"} {
		t.Run(outcome, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			runtime := &refundReadRuntime{}
			service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, runtime)
			refundRequestTimeFixture(t, pool, "now()-interval '48 hours'")
			if _, err := service.BeginProductRefund(t.Context(), buyer, checkout.OrderID, "expired-refund", "test", "The delivered resource does not meet the agreed description."); err != nil {
				t.Fatal(err)
			}
			dispatch := currentProductRefundJob(t, pool, checkout.PaymentID)
			if err := service.HandleProductRefundJob(t.Context(), dispatch); !errors.Is(err, ErrCheckoutReconciliation) {
				t.Fatalf("unsafe dispatch: %v", err)
			}
			// A worker records the non-retryable dispatch failure; the periodic scanner
			// can then query the original merchant instead of retrying the money move.
			if _, err := pool.Exec(t.Context(), `UPDATE jobs SET status='failed',last_error_code='payment_reconciliation_required' WHERE id=$1`, dispatch.ID); err != nil {
				t.Fatal(err)
			}
			var operation uuid.UUID
			var payment string
			if err := pool.QueryRow(t.Context(), `SELECT o.refund_operation_id,pi.provider_payment_id FROM orders o JOIN payment_intents pi ON pi.order_id=o.id WHERE o.id=$1`, checkout.OrderID).Scan(&operation, &payment); err != nil {
				t.Fatal(err)
			}
			if outcome != "missing" {
				runtime.observations = []RefundObservation{{ProviderID: "re_expiredoriginal", ProviderPaymentID: payment, AmountCents: checkout.AmountCents, Currency: "USD", Status: outcome, OperationID: &operation}}
			}
			if n, err := service.ReconcileProductRefunds(t.Context(), 100); err != nil || n != 1 {
				t.Fatalf("automatic recovery scheduling: %d %v", n, err)
			}
			var job jobs.Job
			if err := pool.QueryRow(t.Context(), `SELECT j.id,j.kind,j.payload FROM product_refund_checks c JOIN jobs j ON j.id=c.job_id WHERE c.payment_id=$1`, checkout.PaymentID).Scan(&job.ID, &job.Kind, &job.Payload); err != nil {
				t.Fatal(err)
			}
			if err := service.HandleProductRefundCheckJob(t.Context(), job); err != nil {
				t.Fatal(err)
			}
			history, err := service.RefundHistory(t.Context(), checkout.PaymentID, "", 20)
			if err != nil || history.LatestCheck == nil || history.LatestCheck.Status != "completed" || runtime.reads != 1 || len(runtime.operations) != 0 {
				t.Fatalf("query did not recover without dispatch: %+v reads=%d calls=%d err=%v", history, runtime.reads, len(runtime.operations), err)
			}
			var held bool
			if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM product_refund_review WHERE payment_id=$1)`, checkout.PaymentID).Scan(&held); err != nil {
				t.Fatal(err)
			}
			switch outcome {
			case "succeeded":
				if held || history.LatestCheck.UnresolvedCount != 0 {
					t.Fatal("verified success still held")
				}
				assertProductRefundState(t, pool, checkout, "refunded", "refunded", "refunded", 0, 0)
			case "failed":
				if held || history.LatestCheck.UnresolvedCount != 0 {
					t.Fatal("verified failure still held")
				}
				assertProductRefundState(t, pool, checkout, "paid", "fulfilled", "active", 0, 0)
			default:
				if !held || history.LatestCheck.UnresolvedCount == 0 {
					t.Fatal("missing provider result released uncertain refund")
				}
				assertProductRefundState(t, pool, checkout, "refund_pending", "refund_requested", "active", 0, 0)
			}
			if err := service.HandleProductRefundJob(context.Background(), dispatch); outcome != "missing" && err != nil {
				t.Fatal(fmt.Errorf("drain terminal refund: %w", err))
			}
			if len(runtime.operations) != 0 {
				t.Fatal("query recovery caused another refund")
			}
			if outcome == "failed" {
				if _, err := pool.Exec(t.Context(), `DROP TRIGGER refund_request_time_fixture ON orders`); err != nil {
					t.Fatal(err)
				}
				if _, err := service.BeginProductRefund(t.Context(), buyer, checkout.OrderID, "verified-failure-retry", "test", "Retry only after the original refund has verifiably failed."); err != nil {
					t.Fatal(err)
				}
				if err := service.HandleProductRefundJob(t.Context(), dispatch); err != nil || len(runtime.operations) != 0 {
					t.Fatalf("old job dispatched new operation: %v", err)
				}
				if err := service.HandleProductRefundJob(t.Context(), currentProductRefundJob(t, pool, checkout.PaymentID)); err != nil || len(runtime.operations) != 1 || runtime.operations[0] == operation {
					t.Fatalf("verified failure did not permit a fresh operation: %v", err)
				}
			}
		})
	}
}

type delayedRefundIdentityRuntime struct {
	durableProductRefundRuntime
	delay time.Duration
}

func (r *delayedRefundIdentityRuntime) ProductCheckoutIdentity(ctx context.Context) (ProductCheckoutIdentity, error) {
	if r.delay > 0 {
		timer := time.NewTimer(r.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ProductCheckoutIdentity{}, ctx.Err()
		case <-timer.C:
		}
	}
	return r.productCheckoutRuntime.ProductCheckoutIdentity(ctx)
}

func TestProductStripeRefundWindowRecheckedAfterIdentityWait(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	runtime := &delayedRefundIdentityRuntime{}
	service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, runtime)
	refundRequestTimeFixture(t, pool, "now()-interval '23 hours'+interval '2 seconds'")
	if _, err := service.BeginProductRefund(t.Context(), buyer, checkout.OrderID, "wait-window", "test", "The delivered resource does not meet the agreed description."); err != nil {
		t.Fatal(err)
	}
	runtime.delay = 2200 * time.Millisecond
	started := time.Now()
	err := service.HandleProductRefundJob(t.Context(), currentProductRefundJob(t, pool, checkout.PaymentID))
	if !errors.Is(err, ErrCheckoutReconciliation) || len(runtime.operations) != 0 || time.Since(started) < runtime.delay {
		t.Fatalf("window was not rechecked after waiting: calls=%d elapsed=%v err=%v", len(runtime.operations), time.Since(started), err)
	}
}

func TestProductStripeRefundWindowMigration(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	runtime := &durableProductRefundRuntime{}
	service, checkout, buyer, _, _ := fulfilledRefundFixture(t, pool, runtime)
	down, err := os.ReadFile("../platform/database/migrations/0119_stripe_refund_retry_window.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../platform/database/migrations/0119_stripe_refund_retry_window.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), string(down)); err != nil {
		t.Fatal(err)
	}
	refundRequestTimeFixture(t, pool, "now()-interval '48 hours'")
	if _, err := service.BeginProductRefund(t.Context(), buyer, checkout.OrderID, "historical-window", "test", "The delivered resource does not meet the agreed description."); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), string(up)); err != nil {
		t.Fatal(err)
	}
	var held bool
	if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM product_refund_review WHERE payment_id=$1)`, checkout.PaymentID).Scan(&held); err != nil || !held {
		t.Fatalf("migration missed existing uncertainty: %t %v", held, err)
	}
	if _, err := pool.Exec(t.Context(), string(down)); err == nil {
		t.Fatal("rollback removed an unresolved refund guard")
	}
	if _, err := pool.Exec(t.Context(), `UPDATE product_refund_attempts SET requested_at=now() WHERE payment_id=$1`, checkout.PaymentID); err == nil {
		t.Fatal("original operation timestamp could be refreshed")
	}
	if err := service.HandleProductRefundJob(t.Context(), currentProductRefundJob(t, pool, checkout.PaymentID)); !errors.Is(err, ErrCheckoutReconciliation) || len(runtime.operations) != 0 {
		t.Fatalf("migrated uncertainty dispatched: %v", err)
	}
}
