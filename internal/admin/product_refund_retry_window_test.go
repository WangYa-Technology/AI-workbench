package admin_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/payments"
)

func TestAdminExpiredStripeRefundRequiresQuery(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	actor, buyer, seller, asset, product, order, payment, operation := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []uuid.UUID{actor, buyer, seller} {
		exec(`INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Refund window','admin')`, id, id.String()+"@test.local", "window_"+id.String()[:8])
	}
	exec(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
 VALUES($1,$2,'image','Window source','/media/source.jpg','image/jpeg','clean','delivery','hcai-commercial-standard-v1')`, asset, seller)
	exec(`INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status)
 VALUES($1,$2,$3,'Window product','Fixture','asset',1900,'USD','hcai-commercial-standard-v1','active')`, product, seller, asset)
	exec(`INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,idempotency_key,product_title_snapshot,license_name_snapshot,license_version,license_terms_snapshot,refund_window_days_snapshot,refund_operation_id,refund_reason,refund_requested_at,refund_correlation_enabled)
 SELECT $1,$2,$3,1900,'USD','refund_requested','window-order','Window product',name,version,terms,refund_window_days,$4,'Response lost',now()-interval '48 hours',true FROM licenses WHERE code='hcai-commercial-standard-v1'`, order, buyer, product, operation)
	exec(`INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key,provider_payment_id)
 VALUES($1,'stripe','product',$2,$3,$4,$5,1900,'USD','refund_pending',false,'window-payment','pi_windowadmin')`, payment, buyer, seller, product, order)
	exec(`INSERT INTO product_refund_attempts(operation_id,payment_id,provider,provider_payment_id,amount_cents,currency,correlation_enabled,status,requested_at)
 SELECT $1,$2,'stripe','pi_windowadmin',1900,'USD',true,'requested',refund_requested_at FROM orders WHERE id=$3`, operation, payment, order)
	exec(`INSERT INTO product_checkout_requests(payment_id,identity,request)
 SELECT pi.id,jsonb_build_object('provider','stripe','merchantId','acct_fixture','liveMode',false,'endpoint','https://api.stripe.com/v1','apiVersion','2026-02-25.clover','requestVersion','stripe-product-checkout-v1'),
 jsonb_build_object('PaymentID',pi.id,'Purpose','product','ResourceID',pi.resource_id,'OrderExternalID',pi.order_id,'BuyerIdentity',pi.payer_id,'AmountCents',pi.amount_cents,'Currency',pi.currency)
 FROM payment_intents pi WHERE pi.id=$1`, payment)
	service := admin.NewService(pool, true)
	page, err := service.ListPaymentOperations(t.Context(), admin.PaymentOperationListInput{Query: payment.String(), Attention: "needs_attention"})
	if err != nil || len(page.Items) != 1 || page.Items[0].AttentionCode != "refund_reconciliation_required" {
		t.Fatalf("uncertain expiry missing from operations: %+v %v", page, err)
	}
	if _, err := service.RecoverPayment(t.Context(), actor, payment, admin.PaymentRecovery{Action: "retry_refund", ExpectedVersion: 1}, "expired-retry"); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("operator bypassed refund window: %v", err)
	}
	var version, jobs, attempts int
	if err := pool.QueryRow(t.Context(), `SELECT version,(SELECT count(*) FROM jobs WHERE kind=$2 AND payload->>'paymentId'=$3),(SELECT count(*) FROM product_refund_attempts WHERE payment_id=$1) FROM payment_intents WHERE id=$1`, payment, payments.ProductRefundJobKind, payment.String()).Scan(&version, &jobs, &attempts); err != nil {
		t.Fatal(err)
	}
	if version != 1 || jobs != 0 || attempts != 1 {
		t.Fatalf("rejected recovery changed evidence: version=%d jobs=%d attempts=%d", version, jobs, attempts)
	}
}
