package payments

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestProductRefundHistoricalSuccess(t *testing.T) {
	for _, scenario := range []string{"no_retry", "queued_retry", "dispatched_retry", "lost_response_retry", "legacy_callback"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			runtime := &durableProductRefundRuntime{}
			service, checkout, buyer, product, now := fulfilledRefundFixture(t, pool, runtime)
			begin := func(key string) jobs.Job {
				t.Helper()
				if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, key, "history-test", "The delivered content does not match its description."); err != nil {
					t.Fatal(err)
				}
				return currentProductRefundJob(t, pool, checkout.PaymentID)
			}
			first := begin("history-first-refund")
			if err := service.HandleProductRefundJob(ctx, first); err != nil {
				t.Fatal(err)
			}
			firstOperation := runtime.operations[0]
			refundID := func(id uuid.UUID) string { return "re_" + strings.ReplaceAll(id.String(), "-", "") }
			process := func(name, status string, operation uuid.UUID) error {
				var body []byte
				if scenario == "legacy_callback" {
					body = productRefundEvent(name, refundID(operation), status, checkout.PaymentID, product, now.Unix(), 1900)
				} else {
					body = refundOperationEvent(t, name, refundID(operation), status, checkout.PaymentID, product, now, operation.String())
				}
				header := "t=" + fmt.Sprint(now.Unix()) + ",v1=" + stripeSignature(testStripeWebhookSecret, now.Unix(), body)
				receipt, err := service.ReceiveStripeWebhook(ctx, body, header)
				if err != nil {
					return err
				}
				return service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))})
			}
			if err := process("evt_historyfailed", "failed", firstOperation); err != nil {
				t.Fatal(err)
			}
			assertProductRefundState(t, pool, checkout, "paid", "fulfilled", "active", 0, 0)
			// A duplicate failure and an out-of-order pending event are consumed without
			// reopening the failed operation or duplicating its notification.
			for _, state := range []string{"failed", "pending"} {
				if err := process("evt_historylate"+state, state, firstOperation); err != nil {
					t.Fatal(err)
				}
			}
			var second jobs.Job
			var secondOperation uuid.UUID
			if scenario != "no_retry" {
				second = begin("history-second-refund")
				if err := pool.QueryRow(ctx, `SELECT refund_operation_id FROM orders WHERE id=$1`, checkout.OrderID).Scan(&secondOperation); err != nil {
					t.Fatal(err)
				}
				if scenario == "dispatched_retry" || scenario == "lost_response_retry" {
					runtime.failNext = scenario == "lost_response_retry"
					err := service.HandleProductRefundJob(ctx, second)
					if (scenario == "lost_response_retry") != (err != nil) {
						t.Fatalf("retry dispatch error: %v", err)
					}
				}
				if created, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "history-first-refund", "replay-old", "The delivered content does not match its description."); err != nil || created {
					t.Fatalf("historical command started new refund: %t %v", created, err)
				}
				// Failure for A cannot fail B or restore rights after B's later success.
				if err := process("evt_historyoldfailed", "failed", firstOperation); err != nil {
					t.Fatal(err)
				}
				assertProductRefundState(t, pool, checkout, "refund_pending", "refund_requested", "active", 0, 0)
			}
			if err := process("evt_historysuccess", "succeeded", firstOperation); err != nil {
				t.Fatal(err)
			}
			assertProductRefundState(t, pool, checkout, "refunded", "refunded", "refunded", 0, 0)
			calls := len(runtime.operations)
			if scenario != "no_retry" {
				if err := service.HandleProductRefundJob(ctx, second); err != nil {
					t.Fatal(err)
				}
			}
			if err := service.HandleProductRefundJob(ctx, first); err != nil {
				t.Fatal(err)
			}
			if len(runtime.operations) != calls {
				t.Fatal("late success dispatched an additional refund")
			}
			var savedRefund, operationStatus string
			if err := pool.QueryRow(ctx, `SELECT provider_refund_id,status FROM product_refund_attempts WHERE operation_id=$1`, firstOperation).Scan(&savedRefund, &operationStatus); err != nil || savedRefund != refundID(firstOperation) || operationStatus != "succeeded" {
				t.Fatalf("historical attempt lost: %s %s %v", savedRefund, operationStatus, err)
			}
			if scenario != "no_retry" {
				var review bool
				if err := pool.QueryRow(ctx, `SELECT reconciliation_required FROM product_refund_attempts WHERE operation_id=$1`, secondOperation).Scan(&review); err != nil || !review {
					t.Fatalf("unresolved retry hidden: %t %v", review, err)
				}
			}
			for _, state := range []string{"pending", "failed", "succeeded"} {
				if err := process("evt_historyafter"+state, state, firstOperation); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "dispatched_retry" || scenario == "lost_response_retry" {
				if err := process("evt_historysecondsuccess", "succeeded", secondOperation); err != nil {
					t.Fatal(err)
				}
				for _, state := range []string{"succeeded", "failed", "pending"} {
					if err := process("evt_historysecondlate"+state, state, secondOperation); err != nil {
						t.Fatal(err)
					}
				}
				var successes, reviews, exceptions int
				if err := pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='succeeded'),count(*) FILTER(WHERE reconciliation_required),
      (SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='refund.multiple_successes')
      FROM product_refund_attempts WHERE payment_id=$1`, checkout.PaymentID).Scan(&successes, &reviews, &exceptions); err != nil || successes != 2 || reviews != 2 || exceptions != 1 {
					t.Fatalf("duplicate success evidence: %d %d %d %v", successes, reviews, exceptions, err)
				}
			}
			var notifications, confirmations int
			if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM notifications WHERE user_id=$1 AND kind='marketplace.order_refunded'),
      (SELECT count(*) FROM payment_intent_events WHERE payment_id=$2 AND event_type='refund.confirmed')`, buyer, checkout.PaymentID).Scan(&notifications, &confirmations); err != nil || notifications != 1 || confirmations != 1 {
				t.Fatalf("duplicate confirmation: %d %d %v", notifications, confirmations, err)
			}
			assertProductRefundState(t, pool, checkout, "refunded", "refunded", "refunded", 0, 0)
			// Even after success, unrelated signed metadata cannot manufacture a prior operation.
			if scenario != "no_retry" {
				assertUnsettledProductDeliveryRetained(t, pool, service, checkout.OrderID)
			}
			if err := process("evt_historyunknown", "succeeded", uuid.New()); err == nil {
				t.Fatal("unknown historical refund accepted")
			}
			for _, query := range []string{
				`UPDATE product_refund_attempts SET status='failed' WHERE operation_id=$1`,
				`UPDATE product_refund_attempts SET amount_cents=1 WHERE operation_id=$1`,
				`UPDATE product_refund_attempts SET provider_refund_id='re_replacement' WHERE operation_id=$1`,
				`DELETE FROM product_refund_attempts WHERE operation_id=$1`,
			} {
				if _, err := pool.Exec(ctx, query, firstOperation); err == nil {
					t.Fatalf("mutable evidence: %s", query)
				}
			}
		})
	}
}

func TestProductRefundAttemptMigration(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service, checkout, _, product, now := fulfilledRefundFixture(t, pool, &durableProductRefundRuntime{})
	down, err := os.ReadFile("../platform/database/migrations/0082_product_refund_attempts.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../platform/database/migrations/0082_product_refund_attempts.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	restoreReconciliation := rollbackCleanupReconciliationForMigrationTest(t, pool)
	restoreFundsViews := suspendSellerFundsViewsForMigrationTest(t, pool)
	applyRefundObservationMigration(t, pool, "down")
	dispatchDown, err := os.ReadFile("../platform/database/migrations/0107_product_refund_dispatch.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(dispatchDown)); err != nil {
		t.Fatal(err)
	}
	autoDown, err := os.ReadFile("../platform/database/migrations/0106_product_refund_reconciliation.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(autoDown)); err != nil {
		t.Fatal(err)
	}
	// This isolated historical fixture keeps newer dispatch evidence intact.
	// Rebuild only the derived closure view around the older view migration.
	closureMigration, err := os.ReadFile("../platform/database/migrations/0091_product_checkout_local_closure.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	viewOffset := strings.Index(string(closureMigration), "CREATE VIEW product_checkout_locally_closable AS")
	if viewOffset < 0 {
		t.Fatal("closure view definition missing")
	}
	if _, err = pool.Exec(ctx, `DROP VIEW product_checkout_locally_closable`); err != nil {
		t.Fatal(err)
	}
	// Remove dependent later migration first, as the deployment rollback order requires.
	missingCheckDown, err := os.ReadFile("../platform/database/migrations/0124_product_checkout_check_reconciliation.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(missingCheckDown)); err != nil {
		t.Fatal(err)
	}
	lookupDown, err := os.ReadFile("../platform/database/migrations/0087_product_checkout_lookup.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(lookupDown)); err != nil {
		t.Fatal(err)
	}
	identityDown, err := os.ReadFile("../platform/database/migrations/0086_product_payment_identity_recovery.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(identityDown)); err != nil {
		t.Fatal(err)
	}
	checkoutDown, err := os.ReadFile("../platform/database/migrations/0084_product_checkout_checks.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	checkoutUp, err := os.ReadFile("../platform/database/migrations/0084_product_checkout_checks.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(checkoutDown)); err != nil {
		t.Fatal(err)
	}
	checksDown, err := os.ReadFile("../platform/database/migrations/0083_product_refund_checks.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	checksUp, err := os.ReadFile("../platform/database/migrations/0083_product_refund_checks.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(checksDown)); err != nil {
		t.Fatal(err)
	}
	// Downgrade an empty schema, construct the actual pre-upgrade failed state,
	// then apply the new migration. This never touches the developer schema.
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	operation := uuid.New()
	if _, err := pool.Exec(ctx, `UPDATE orders SET refund_operation_id=$2,refund_requested_at=now(),refund_idempotency_key='legacy-history-command',refund_correlation_enabled=false WHERE id=$1`, checkout.OrderID, operation); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
   VALUES($1,'refund.provider_requested','refund_pending','refund_pending',jsonb_build_object('operationId',$2::text,'providerRefundId','re_legacyhistory'))`, checkout.PaymentID, operation); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(checksUp)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(checkoutUp)); err != nil {
		t.Fatal(err)
	}
	identityUp, err := os.ReadFile("../platform/database/migrations/0086_product_payment_identity_recovery.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(identityUp)); err != nil {
		t.Fatal(err)
	}
	lookupUp, err := os.ReadFile("../platform/database/migrations/0087_product_checkout_lookup.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(lookupUp)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(closureMigration)[viewOffset:]); err != nil {
		t.Fatal(err)
	}
	restoreReconciliation()
	autoUp, err := os.ReadFile("../platform/database/migrations/0106_product_refund_reconciliation.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(autoUp)); err != nil {
		t.Fatal(err)
	}
	dispatchUp, err := os.ReadFile("../platform/database/migrations/0107_product_refund_dispatch.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(dispatchUp)); err != nil {
		t.Fatal(err)
	}
	missingCheckUp, err := os.ReadFile("../platform/database/migrations/0124_product_checkout_check_reconciliation.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(missingCheckUp)); err != nil {
		t.Fatal(err)
	}
	var status, id, key string
	applyRefundObservationMigration(t, pool, "up")
	restoreFundsViews()
	var enabled bool
	if err := pool.QueryRow(ctx, `SELECT status,provider_refund_id,idempotency_key,correlation_enabled FROM product_refund_attempts WHERE operation_id=$1`, operation).Scan(&status, &id, &key, &enabled); err != nil || status != "failed" || id != "re_legacyhistory" || key != "legacy-history-command" || enabled {
		t.Fatalf("backfill mismatch: %s %s %s %t %v", status, id, key, enabled, err)
	}
	if _, err := pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard product refund operation evidence") {
		t.Fatalf("expected evidence-preserving rollback guard, got %v", err)
	}
	receipt := receivePaymentWorkflowEvent(t, service, productRefundEvent("evt_legacylatesuccess", "re_legacyhistory", "succeeded", checkout.PaymentID, product, now.Unix(), 1900), now)
	if err := service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}); err != nil {
		t.Fatal(err)
	}
	assertProductRefundState(t, pool, checkout, "refunded", "refunded", "refunded", 0, 0)
}

func TestProductRefundHistoricalSuccessDuringDispatch(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprintf("lost_%t", lost), func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			firstRuntime := &durableProductRefundRuntime{}
			service, checkout, buyer, product, now := fulfilledRefundFixture(t, pool, firstRuntime)
			if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "history-concurrent-first", "test", "The delivered content does not match its description."); err != nil {
				t.Fatal(err)
			}
			if err := service.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, checkout.PaymentID)); err != nil {
				t.Fatal(err)
			}
			operation := firstRuntime.operations[0]
			refundID := "re_" + strings.ReplaceAll(operation.String(), "-", "")
			firstEvent := receivePaymentWorkflowEvent(t, service, refundOperationEvent(t, "evt_concurrentfailed", refundID, "failed", checkout.PaymentID, product, now, operation.String()), now)
			if err := service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, firstEvent.EventID.String()))}); err != nil {
				t.Fatal(err)
			}
			if _, err := service.BeginProductRefund(ctx, buyer, checkout.OrderID, "history-concurrent-second", "test", "The delivered content does not match its description."); err != nil {
				t.Fatal(err)
			}
			runtime := &earlyRefundCallbackRuntime{entered: make(chan RefundRequest, 1), release: make(chan struct{}), lost: lost}
			service.runtimes = NewRuntimeCatalog(runtime)
			var once sync.Once
			release := func() { once.Do(func() { close(runtime.release) }) }
			defer release()
			job := currentProductRefundJob(t, pool, checkout.PaymentID)
			dispatch := make(chan error, 1)
			go func() { dispatch <- service.HandleProductRefundJob(ctx, job) }()
			select {
			case <-runtime.entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			receipt := receivePaymentWorkflowEvent(t, service, refundOperationEvent(t, "evt_concurrentoldsuccess", refundID, "succeeded", checkout.PaymentID, product, now, operation.String()), now)
			eventJob := jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}
			processed := make(chan error, 1)
			go func() { processed <- service.HandlePaymentEventJob(ctx, eventJob) }()
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock'
      AND query LIKE '%SELECT o.refund_operation_id,pi.status FROM payment_intents pi%' AND cardinality(pg_blocking_pids(pid))>0)`).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case <-ticker.C:
				case <-ctx.Done():
					t.Fatal("old success did not wait for active dispatch")
				}
			}
			release()
			select {
			case err := <-dispatch:
				if (err != nil) != lost {
					t.Fatalf("dispatch result: %v", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			select {
			case err := <-processed:
				if err != nil {
					var conflict *pgconn.PgError
					if !errors.As(err, &conflict) || conflict.Code != "40001" || !jobs.ShouldRetry(err) {
						t.Fatal(err)
					}
					if err := service.HandlePaymentEventJob(ctx, eventJob); err != nil {
						t.Fatal(err)
					}
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			assertProductRefundState(t, pool, checkout, "refunded", "refunded", "refunded", 0, 0)
			if err := service.HandleProductRefundJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if runtime.calls.Load() != 1 {
				t.Fatal("worker resent a refund after old success")
			}
			var reviews int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_refund_attempts WHERE payment_id=$1 AND reconciliation_required`, checkout.PaymentID).Scan(&reviews); err != nil || reviews != 1 {
				t.Fatalf("in-flight refund lost from review: %d %v", reviews, err)
			}
		})
	}
}
