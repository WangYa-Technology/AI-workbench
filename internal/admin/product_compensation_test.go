package admin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/payments"
)

func TestAdminProductCompensationRecovery(t *testing.T) {
	for _, provider := range []string{"stripe", "waffo_pancake"} {
		t.Run(provider, func(t *testing.T) {
			pool, cleanup := testPool(t)
			defer cleanup()
			ctx := context.Background()
			actor, buyer, seller, source, product, order, payment, previousOperation := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			for _, user := range []uuid.UUID{actor, buyer, seller} {
				exec(`INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Recovery test','admin')`, user, user.String()+"@test.local", "recovery_"+user.String()[:8])
			}
			exec(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
		VALUES($1,$2,'image','Unavailable source','/media/source.jpg','image/jpeg','rejected','delivery','hcai-commercial-standard-v1')`, source, seller)
			exec(`INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status)
		VALUES($1,$2,$3,'Compensation product','Recovery fixture','asset',1900,'USD','hcai-commercial-standard-v1','active')`, product, seller, source)
			exec(`INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,idempotency_key,product_title_snapshot,license_name_snapshot,license_version,license_terms_snapshot,refund_window_days_snapshot,refund_operation_id,refund_reason)
		SELECT $1,$2,$3,1900,'USD','refund_requested','recovery-order','Compensation product',name,version,terms,refund_window_days,$4,'Automatic refund: source_unavailable'
		FROM licenses WHERE code='hcai-commercial-standard-v1'`, order, buyer, product, previousOperation)
			exec(`INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key,provider_payment_id,provider_refund_id,compensation_reason)
		VALUES($1,$6,'product',$2,$3,$4,$5,1900,'USD','refund_failed',false,'recovery-payment','pi_recovery123','re_failed123','source_unavailable')`, payment, buyer, seller, product, order, provider)
			// Modern fixture with original merchant evidence.
			exec(`INSERT INTO product_checkout_requests(payment_id,identity,request)
 SELECT pi.id,jsonb_build_object('provider',pi.provider,'merchantId','acct_fixture','liveMode',pi.live_mode,
 'endpoint',CASE WHEN pi.provider='stripe' THEN 'https://api.stripe.com/v1' ELSE 'http://127.0.0.1:8091' END, 'storeId','STO_fixture', 'apiVersion',CASE WHEN pi.provider='stripe' THEN '2026-02-25.clover' ELSE 'pancake-ts-0.19.1' END,'requestVersion',CASE WHEN pi.provider='stripe' THEN 'stripe-product-checkout-v1' ELSE 'waffo-product-checkout-v1' END),
 jsonb_build_object('PaymentID',pi.id,'Purpose','product','ResourceID',pi.resource_id,'OrderExternalID',pi.order_id,
 'BuyerIdentity',pi.payer_id,'BuyerEmail',u.email,'AmountCents',pi.amount_cents,'Currency',pi.currency)
 FROM payment_intents pi JOIN users u ON u.id=pi.payer_id WHERE pi.id=$1`, payment)
			service := admin.NewService(pool, true)
			if _, err := service.RecoverPayment(ctx, actor, payment, admin.PaymentRecovery{Action: "retry_refund", ExpectedVersion: 2}, "stale-recovery"); !errors.Is(err, admin.ErrConflict) {
				t.Fatalf("wrong version accepted: %v", err)
			}
			recovered, err := service.RecoverPayment(ctx, actor, payment, admin.PaymentRecovery{Action: "retry_refund", ExpectedVersion: 1}, "compensation-recovery")
			if err != nil || recovered.Status != "refund_pending" || recovered.Version != 2 || recovered.Job == nil || recovered.Job.Kind != payments.ProductRefundJobKind {
				t.Fatalf("recovery result: %#v %v", recovered, err)
			}
			if _, err := service.RecoverPayment(ctx, actor, payment, admin.PaymentRecovery{Action: "retry_refund", ExpectedVersion: 2}, "duplicate-recovery"); !errors.Is(err, admin.ErrConflict) {
				t.Fatalf("active job duplicated: %v", err)
			}
			var status, reason string
			var operation uuid.UUID
			var refundID *string
			var version, rights, jobs, events int
			err = pool.QueryRow(ctx, `SELECT o.status,o.refund_operation_id,pi.provider_refund_id,pi.compensation_reason,pi.version,
		(SELECT count(*) FROM entitlements WHERE order_id=o.id),
		(SELECT count(*) FROM jobs WHERE kind=$2 AND payload->>'paymentId'=pi.id::text),
		(SELECT count(*) FROM payment_intent_events WHERE payment_id=pi.id AND event_type='admin.retry_refund')
		FROM payment_intents pi JOIN orders o ON o.id=pi.order_id WHERE pi.id=$1`, payment, payments.ProductRefundJobKind).Scan(&status, &operation, &refundID, &reason, &version, &rights, &jobs, &events)
			if err != nil {
				t.Fatal(err)
			}
			if status != "refund_requested" || operation == previousOperation || operation == uuid.Nil || refundID != nil || reason != "source_unavailable" || version != 2 || rights != 0 || jobs != 1 || events != 1 {
				t.Fatalf("recovery changed obligation or duplicated evidence: status=%s operation=%s refund=%v reason=%s version=%d rights=%d jobs=%d events=%d", status, operation, refundID, reason, version, rights, jobs, events)
			}
			var queuedOperation string
			if err := pool.QueryRow(ctx, `SELECT payload->>'operationId' FROM jobs WHERE kind=$1 AND payload->>'paymentId'=$2`, payments.ProductRefundJobKind, payment.String()).Scan(&queuedOperation); err != nil {
				t.Fatal(err)
			}
			if queuedOperation != operation.String() {
				t.Fatalf("recovery job bound to wrong operation: %s", queuedOperation)
			}
			var attempts, failed, queued int
			if err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE operation_id=$2 AND provider_refund_id='re_failed123' AND status='failed'),
      count(*) FILTER(WHERE operation_id=$3 AND provider_refund_id IS NULL AND status='requested')
      FROM product_refund_attempts WHERE payment_id=$1`, payment, previousOperation, operation).Scan(&attempts, &failed, &queued); err != nil || attempts != 2 || failed != 1 || queued != 1 {
				t.Fatalf("recovery discarded prior attempt: %d %d %d %v", attempts, failed, queued, err)
			}
			if provider == "waffo_pancake" {
				var permit bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_refund_dispatches WHERE operation_id=$1 AND reserved_at IS NULL)`, operation).Scan(&permit); err != nil || !permit {
					t.Fatalf("fresh administrative retry lacks permit: %t %v", permit, err)
				}
				exec(`UPDATE product_refund_dispatches SET reserved_at=now() WHERE operation_id=$1`, operation)
				exec(`UPDATE jobs SET status='succeeded' WHERE kind=$1 AND payload->>'paymentId'=$2`, payments.ProductRefundJobKind, payment.String())
				if _, err := service.RecoverPayment(ctx, actor, payment, admin.PaymentRecovery{Action: "retry_refund", ExpectedVersion: 2}, "uncertain-waffo-dispatch"); !errors.Is(err, admin.ErrConflict) {
					t.Fatalf("operator could resend uncertain ticket: %v", err)
				}
				attention, err := service.ListPaymentOperations(ctx, admin.PaymentOperationListInput{Query: payment.String(), Attention: "needs_attention"})
				if err != nil || len(attention.Items) != 1 || attention.Items[0].AttentionCode != "refund_reconciliation_required" {
					t.Fatalf("uncertain Waffo dispatch missing from operator queue: %#v %v", attention, err)
				}
			}
			exec(`UPDATE product_refund_attempts SET reconciliation_required=true WHERE operation_id=$1`, previousOperation)
			exec(`UPDATE jobs SET status='succeeded' WHERE kind=$1 AND payload->>'paymentId'=$2`, payments.ProductRefundJobKind, payment.String())
			if _, err := service.RecoverPayment(ctx, actor, payment, admin.PaymentRecovery{Action: "retry_refund", ExpectedVersion: 2}, "unresolved-attempt"); !errors.Is(err, admin.ErrConflict) {
				t.Fatalf("recovery ignored unresolved funds: %v", err)
			}
			attention, err := service.ListPaymentOperations(ctx, admin.PaymentOperationListInput{Query: payment.String(), Attention: "needs_attention"})
			if err != nil || len(attention.Items) != 1 || attention.Items[0].AttentionCode != "refund_reconciliation_required" {
				t.Fatalf("refund reconciliation missing from operations queue: %#v %v", attention, err)
			}

		})
	}
}

func TestAdminProductRefundRecoveryUsesAttemptEvidence(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	actor, buyer, seller, source, product, order, payment, operation := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []uuid.UUID{actor, buyer, seller} {
		exec(`INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Refund history','admin')`, user, user.String()+"@test.local", "refund_"+user.String()[:8])
	}
	exec(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
   VALUES($1,$2,'image','Refund source','/media/refund.jpg','image/jpeg','clean','delivery','hcai-commercial-standard-v1')`, source, seller)
	exec(`INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status)
   VALUES($1,$2,$3,'Refund history product','History fixture','asset',1900,'USD','hcai-commercial-standard-v1','active')`, product, seller, source)
	exec(`INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,idempotency_key,product_title_snapshot,license_name_snapshot,license_version,license_terms_snapshot,refund_window_days_snapshot,refund_operation_id,refund_reason)
   SELECT $1,$2,$3,1900,'USD','fulfilled','refund-history-order','Refund history product',name,version,terms,refund_window_days,$4,'Refund did not complete'
   FROM licenses WHERE code='hcai-commercial-standard-v1'`, order, buyer, product, operation)
	exec(`INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key,provider_payment_id)
   VALUES($1,'stripe','product',$2,$3,$4,$5,1900,'USD','paid',false,'refund-history-payment','pi_historyadmin')`, payment, buyer, seller, product, order)
	exec(`INSERT INTO product_refund_attempts(operation_id,payment_id,provider,provider_payment_id,amount_cents,currency,correlation_enabled,provider_refund_id,status,requested_at)
   VALUES($1,$2,'stripe','pi_historyadmin',1900,'USD',true,'re_historyadmin','failed',now())`, operation, payment)
	exec(`INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,created_at)
   VALUES($1,'refund.failed','refund_pending','paid',now()-interval '1 minute'),($1,'refund.operation_observed','paid','paid',now())`, payment)
	// Modern fixture with original merchant evidence.
	exec(`INSERT INTO product_checkout_requests(payment_id,identity,request)
 SELECT pi.id,jsonb_build_object('provider',pi.provider,'merchantId','acct_fixture','liveMode',pi.live_mode,
 'endpoint','https://api.stripe.com/v1','apiVersion','2026-02-25.clover','requestVersion','stripe-product-checkout-v1'),
 jsonb_build_object('PaymentID',pi.id,'Purpose','product','ResourceID',pi.resource_id,'OrderExternalID',pi.order_id,
 'BuyerIdentity',pi.payer_id,'BuyerEmail',u.email,'AmountCents',pi.amount_cents,'Currency',pi.currency)
 FROM payment_intents pi JOIN users u ON u.id=pi.payer_id WHERE pi.id=$1`, payment)
	service := admin.NewService(pool, true)
	result, err := service.RecoverPayment(ctx, actor, payment, admin.PaymentRecovery{Action: "retry_refund", ExpectedVersion: 1}, "observed-after-failure")
	if err != nil || result.Status != "refund_pending" {
		t.Fatalf("later observation prevented failed-operation recovery: %#v %v", result, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_refund_attempts WHERE payment_id=$1`, payment).Scan(&count); err != nil || count != 2 {
		t.Fatalf("recovery history: %d %v", count, err)
	}
	// A newer processed event must not hide an older event that still needs
	// recovery. The metric and operations entry must refer to the same backlog.
	failedEvent, healthyEvent := uuid.New(), uuid.New()
	for _, event := range []uuid.UUID{failedEvent, healthyEvent} {
		exec(`INSERT INTO payment_provider_events(id,provider,provider_event_id,event_type,api_version,live_mode,occurred_at,payload_sha256,object_id,object_type,payment_id,purpose,received_at)
 VALUES($1,'stripe',$2,'checkout.session.completed','2026-02-25.clover',false,now(),repeat('a',64),'cs_metrics_event','checkout.session',$3,'product',CASE WHEN $4 THEN now()-interval '2 days' ELSE now() END)`, event, "evt_"+event.String()[:8], payment, event == failedEvent)
	}
	exec(`INSERT INTO payment_provider_event_processing(event_id,status,error_code,processed_at,updated_at)
 VALUES($1,'failed','handler_failed',now()-interval '2 days',now()-interval '2 days'),($2,'processed',NULL,now(),now())`, failedEvent, healthyEvent)
	metrics, err := payments.ProductOperationalMetrics(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	failures := int64(0)
	for _, item := range metrics.Problems {
		if item.Kind == "event_failed" && item.Mode == "test" {
			failures = item.Count
		}
	}
	if failures != 1 {
		t.Fatalf("older failed event missing from metrics: %+v", metrics.Problems)
	}
	page, err := service.ListPaymentOperations(ctx, admin.PaymentOperationListInput{Query: payment.String(), Attention: "needs_attention"})
	if err != nil || len(page.Items) != 1 || page.Items[0].AttentionCode != "event_processing_failed" || page.Items[0].ProviderEvent == nil || page.Items[0].ProviderEvent.ID != failedEvent {
		t.Fatalf("older failure hidden in operations: %+v %v", page, err)
	}
	if _, err := service.ReplayPaymentEvent(ctx, actor, failedEvent, admin.PaymentEventReplay{ExpectedVersion: 1}, "metrics-event-recovery"); err != nil {
		t.Fatal(err)
	}
	metrics, err = payments.ProductOperationalMetrics(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range metrics.Problems {
		if item.Kind == "event_failed" && item.Count != 0 {
			t.Fatalf("replay did not clear failed-state metric: %+v", item)
		}
	}
}
