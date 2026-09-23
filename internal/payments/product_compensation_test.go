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
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func TestProductPaymentCompensation(t *testing.T) {
	for _, reason := range []string{"source_unavailable", "buyer_unavailable", "contract_unavailable", "checkout_closed", "already_owned"} {
		t.Run(reason, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			buyer, _, source, product := newProductCheckoutFixture(t, pool)
			runtime := &productCheckoutRuntime{}
			service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(runtime))
			checkout, _, err := service.BeginProductCheckout(ctx, buyer, product, "compensation-checkout", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product))
			if err != nil {
				t.Fatal(err)
			}
			exec := func(sql string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(ctx, sql, args...); err != nil {
					t.Fatal(err)
				}
			}
			switch reason {
			case "source_unavailable":
				exec(`UPDATE assets SET scan_status='rejected' WHERE id=$1`, source)
			case "buyer_unavailable":
				exec(`UPDATE users SET status='deleted' WHERE id=$1`, buyer)
			case "contract_unavailable":
				// Simulate a pre-contract historical order in this isolated schema only.
				exec(`ALTER TABLE product_delivery_snapshots DISABLE TRIGGER USER`)
				exec(`DELETE FROM product_delivery_snapshots WHERE order_id=$1`, checkout.OrderID)
				exec(`ALTER TABLE product_delivery_snapshots ENABLE TRIGGER USER`)
				exec(`ALTER TABLE orders DISABLE TRIGGER orders_delivery_requirement`)
				exec(`UPDATE orders SET delivery_snapshot_required=false WHERE id=$1`, checkout.OrderID)
				exec(`ALTER TABLE orders ENABLE TRIGGER orders_delivery_requirement`)
				exec(`ALTER TABLE product_order_contracts DISABLE TRIGGER USER`)
				exec(`DELETE FROM product_order_contracts WHERE order_id=$1`, checkout.OrderID)
				exec(`ALTER TABLE product_order_contracts ENABLE TRIGGER USER`)
			case "checkout_closed":
				exec(`UPDATE payment_intents SET status='cancelled' WHERE id=$1`, checkout.PaymentID)
				exec(`UPDATE orders SET status='cancelled' WHERE id=$1`, checkout.OrderID)
				// A late payment must not collide with a newer open purchase intent.
				if _, _, err := service.BeginProductCheckout(ctx, buyer, product, "newer-checkout-command", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product)); err != nil {
					t.Fatal(err)
				}
			case "already_owned":
				otherOrder := uuid.New()
				exec(`INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,idempotency_key,product_title_snapshot,license_name_snapshot,license_version,license_terms_snapshot,refund_window_days_snapshot)
					SELECT $1,buyer_id,product_id,amount_cents,currency,'fulfilled','legacy-purchase',product_title_snapshot,license_name_snapshot,license_version,license_terms_snapshot,refund_window_days_snapshot FROM orders WHERE id=$2`, otherOrder, checkout.OrderID)
				exec(`INSERT INTO entitlements(user_id,product_id,order_id,asset_id,license_code) VALUES($1,$2,$3,$4,'hcai-commercial-standard-v1')`, buyer, product, otherOrder, source)
			}
			now := time.Now().UTC().Truncate(time.Second)
			service.verifier.now = func() time.Time { return now }
			process := func(body []byte) error {
				t.Helper()
				header := "t=" + fmt.Sprint(now.Unix()) + ",v1=" + stripeSignature(testStripeWebhookSecret, now.Unix(), body)
				r, err := service.ReceiveStripeWebhook(ctx, body, header)
				if err != nil {
					return err
				}
				return service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, r.EventID.String()))})
			}
			paid := productPaidEvent(checkout.PaymentID, product, now.Unix(), 1900)
			if err := process(paid); err != nil {
				t.Fatal(err)
			}
			if err := process([]byte(strings.ReplaceAll(string(paid), "evt_productpaid", "evt_paidduplicate"))); err != nil {
				t.Fatal(err)
			}
			assertState := func(intent, order string, wantJobs int) {
				t.Helper()
				var actualIntent, actualOrder, actualReason string
				var assets, rights, queued int
				err := pool.QueryRow(ctx, `SELECT pi.status,o.status,pi.compensation_reason,
				  (SELECT count(*) FROM assets WHERE source_id=o.id AND source_type='purchase'),
				  (SELECT count(*) FROM entitlements WHERE order_id=o.id),
				  (SELECT count(*) FROM jobs WHERE kind=$2 AND payload->>'paymentId'=pi.id::text)
				  FROM payment_intents pi JOIN orders o ON o.id=pi.order_id WHERE pi.id=$1`, checkout.PaymentID, ProductRefundJobKind).Scan(&actualIntent, &actualOrder, &actualReason, &assets, &rights, &queued)
				if err != nil {
					t.Fatal(err)
				}
				if actualIntent != intent || actualOrder != order || actualReason != reason || assets != 0 || rights != 0 || queued != wantJobs {
					t.Fatalf("compensation state: %s/%s reason=%s assets=%d rights=%d jobs=%d", actualIntent, actualOrder, actualReason, assets, rights, queued)
				}
				assertOperationalMetric(t, pool, "problem", "settlement_missing", "test", 0, 0, 0)
				assertOperationalMetric(t, pool, "backlog", "settlement_due", "test", 0, 0, 0)
				assertOperationalMetric(t, pool, "backlog", "settlement_unresolved", "test", 0, 0, 0)
			}
			assertState("refund_pending", "refund_requested", 1)
			refundJob := currentProductRefundJob(t, pool, checkout.PaymentID)
			if err := service.HandleProductRefundJob(ctx, refundJob); err != nil {
				t.Fatal(err)
			}
			if err := service.HandleProductRefundJob(ctx, refundJob); err != nil {
				t.Fatal(err)
			}
			if runtime.refundCalls != 1 {
				t.Fatalf("duplicate refund calls: %d", runtime.refundCalls)
			}
			if err := process(productRefundEvent("evt_badrefund", "re_unrelated999", "succeeded", checkout.PaymentID, product, now.Unix(), 1900)); err == nil {
				t.Fatal("mismatched refund accepted")
			}
			assertState("refund_pending", "refund_requested", 1)
			if err := process(productRefundEvent("evt_goodrefund", "re_workflow123", "succeeded", checkout.PaymentID, product, now.Unix(), 1900)); err != nil {
				t.Fatal(err)
			}
			assertState("refunded", "refunded", 1)
			if err := process(productRefundEvent("evt_refundreplay", "re_workflow123", "succeeded", checkout.PaymentID, product, now.Unix(), 1900)); err != nil {
				t.Fatal(err)
			}
			if err := process(productRefundEvent("evt_terminalmismatch", "re_unrelated999", "succeeded", checkout.PaymentID, product, now.Unix(), 1900)); err == nil {
				t.Fatal("terminal state bypassed refund identity validation")
			}
			if err := process([]byte(strings.ReplaceAll(string(paid), "evt_productpaid", "evt_paidafterrefund"))); err != nil {
				t.Fatal(err)
			}
			assertState("refunded", "refunded", 1)
			// A signed financial conflict now holds further outbound money movement.
			// Verify rejection after the ordinary compensation lifecycle; the
			// quarantine suite separately verifies blocking before dispatch.
			wrongAmount := strings.ReplaceAll(string(productPaidEvent(checkout.PaymentID, product, now.Unix(), 1800)), "evt_productpaid", "evt_wrongamount")
			if err := process([]byte(wrongAmount)); err == nil {
				t.Fatal("mismatched payment amount accepted")
			}
			wrongPayment := strings.ReplaceAll(strings.ReplaceAll(string(paid), "evt_productpaid", "evt_wrongpayment"), "pi_workflow123", "pi_unrelated999")
			if err := process([]byte(wrongPayment)); err == nil {
				t.Fatal("different payment reused as compensation confirmation")
			}
			if reason == "already_owned" {
				var count int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM entitlements WHERE user_id=$1 AND status='active'`, buyer).Scan(&count); err != nil || count != 1 {
					t.Fatalf("other purchase revoked: %d %v", count, err)
				}
			}
		})
	}
}

type retryProductRefundRuntime struct {
	productCheckoutRuntime
	mu         sync.Mutex
	operations []uuid.UUID
}

func (r *retryProductRefundRuntime) CreateRefund(_ context.Context, request RefundRequest) (Refund, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.operations = append(r.operations, request.OperationID)
	// Model a provider that accepted the operation but lost its first response.
	if len(r.operations) == 1 {
		return Refund{}, errors.New("provider response unavailable")
	}
	return Refund{ProviderID: "re_recovered123", ProviderPaymentID: request.ProviderPaymentID, AmountCents: request.AmountCents, Currency: request.Currency, Status: "pending"}, nil
}

func TestProductCompensationDispatchRetryAndConcurrency(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	buyer, _, source, product := newProductCheckoutFixture(t, pool)
	runtime := &retryProductRefundRuntime{}
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(runtime))
	checkout, _, err := service.BeginProductCheckout(ctx, buyer, product, "dispatch-recovery", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, source); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	service.verifier.now = func() time.Time { return now }
	receipt := receivePaymentWorkflowEvent(t, service, productPaidEvent(checkout.PaymentID, product, now.Unix(), 1900), now)
	if err := service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}); err != nil {
		t.Fatal(err)
	}
	job := currentProductRefundJob(t, pool, checkout.PaymentID)
	if err := service.HandleProductRefundJob(ctx, job); err == nil {
		t.Fatal("lost response was reported as successful")
	}
	results := make(chan error, 8)
	start := make(chan struct{})
	for i := 0; i < cap(results); i++ {
		go func() {
			<-start
			results <- service.HandleProductRefundJob(ctx, job)
		}()
	}
	close(start)
	for i := 0; i < cap(results); i++ {
		if err := <-results; err != nil {
			t.Errorf("concurrent refund dispatch: %v", err)
		}
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.operations) != 2 || runtime.operations[0] == uuid.Nil || runtime.operations[0] != runtime.operations[1] {
		t.Fatalf("retry changed operation or dispatched duplicates: %v", runtime.operations)
	}
	var status, refundID string
	var requests, rights int
	if err := pool.QueryRow(ctx, `SELECT pi.status,pi.provider_refund_id,
		(SELECT count(*) FROM payment_intent_events WHERE payment_id=pi.id AND event_type='refund.provider_requested'),
		(SELECT count(*) FROM entitlements WHERE order_id=pi.order_id) FROM payment_intents pi WHERE id=$1`, checkout.PaymentID).Scan(&status, &refundID, &requests, &rights); err != nil {
		t.Fatal(err)
	}
	if status != "refund_pending" || refundID != "re_recovered123" || requests != 1 || rights != 0 {
		t.Fatalf("unexpected dispatch evidence: status=%s refund=%s requests=%d rights=%d", status, refundID, requests, rights)
	}
}

func TestProductCompensationFailurePreservesRefundObligation(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	buyer, _, source, product := newProductCheckoutFixture(t, pool)
	runtime := &productCheckoutRuntime{}
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(runtime))
	checkout, _, err := service.BeginProductCheckout(ctx, buyer, product, "failed-compensation", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, source); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	service.verifier.now = func() time.Time { return now }
	process := func(body []byte) {
		t.Helper()
		r := receivePaymentWorkflowEvent(t, service, body, now)
		if err := service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, r.EventID.String()))}); err != nil {
			t.Fatal(err)
		}
	}
	paid := productPaidEvent(checkout.PaymentID, product, now.Unix(), 1900)
	process(paid)
	if err := service.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, checkout.PaymentID)); err != nil {
		t.Fatal(err)
	}
	process(productRefundEvent("evt_compfailed", "re_workflow123", "failed", checkout.PaymentID, product, now.Unix(), 1900))
	process(productRefundEvent("evt_compfailedagain", "re_workflow123", "failed", checkout.PaymentID, product, now.Unix(), 1900))
	process([]byte(strings.ReplaceAll(string(paid), "evt_productpaid", "evt_paidafterfailedrefund")))
	var intent, order string
	var rights, failures int
	if err := pool.QueryRow(ctx, `SELECT pi.status,o.status,(SELECT count(*) FROM entitlements WHERE order_id=o.id),
		(SELECT count(*) FROM payment_intent_events WHERE payment_id=pi.id AND event_type='refund.failed')
		FROM payment_intents pi JOIN orders o ON o.id=pi.order_id WHERE pi.id=$1`, checkout.PaymentID).Scan(&intent, &order, &rights, &failures); err != nil {
		t.Fatal(err)
	}
	if intent != "refund_failed" || order != "refund_requested" || rights != 0 || failures != 1 {
		t.Fatalf("failed refund lost obligation: %s/%s rights=%d failures=%d", intent, order, rights, failures)
	}
	assertUnsettledProductDeliveryRetained(t, pool, service, checkout.OrderID)
	// Compensation must not prevent a new, now-deliverable checkout.
	if _, err := pool.Exec(ctx, `UPDATE assets SET scan_status='clean' WHERE id=$1`, source); err != nil {
		t.Fatal(err)
	}
	if next, _, err := service.BeginProductCheckout(ctx, buyer, product, "fresh-after-compensation", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product)); err != nil || next.PaymentID == checkout.PaymentID {
		t.Fatalf("compensation reused as checkout: %v", err)
	}
}
