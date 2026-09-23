package payments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func TestWaffoProductCompensation(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	buyer, _, source, product := newProductCheckoutFixture(t, pool)
	connector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-connector-token" {
			http.Error(w, "missing connector credentials", http.StatusUnauthorized)
			return
		}
		var payload map[string]any
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &payload); err != nil {
			http.Error(w, "invalid payload", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/checkout/identity":
			_ = json.NewEncoder(w).Encode(ProductCheckoutIdentity{Provider: "waffo_pancake", MerchantID: "MER_compensation", StoreID: "STO_compensation", APIVersion: waffoProductCheckoutAPI, RequestVersion: waffoProductCheckoutVersion})
		case "/checkout":
			_ = json.NewEncoder(w).Encode(map[string]any{"providerId": "CHK_compensation", "checkoutUrl": "https://checkout.waffo.ai/session/CHK_compensation", "status": "open", "paymentStatus": "pending", "expiresAt": time.Now().Add(time.Hour), "liveMode": false, "checkoutIdentity": payload["checkoutIdentity"]})
		case "/refund":
			if payload["storeId"] != "STO_compensation" || payload["buyerIdentity"] != buyer.String() || payload["buyerEmail"] != buyer.String()+"@test.local" || payload["providerPaymentId"] != "PAY_compensation" || payload["amountCents"] != float64(1900) || payload["currency"] != "USD" {
				t.Errorf("refund dispatch contract: %#v", payload)
				http.Error(w, "invalid refund", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"providerId": "RFD_compensation", "providerPaymentId": "PAY_compensation", "amountCents": 1900, "currency": "USD", "status": "pending", "paymentIdentity": payload["paymentIdentity"], "operationId": payload["operationId"], "refundContractVersion": payload["refundContractVersion"]})
		case "/webhook/verify":
			// The connector's cryptographic verification is mocked at this boundary.
			if r.Header.Get("x-waffo-signature") != "fixture-signature" {
				http.Error(w, "invalid fixture signature", http.StatusUnauthorized)
				return
			}
			hash := sha256.Sum256(raw)
			_ = json.NewEncoder(w).Encode(map[string]any{"verification": map[string]any{"contractVersion": waffoWebhookContractVersion, "environment": "test", "payloadSHA256": hex.EncodeToString(hash[:])}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer connector.Close()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_provider_configs(provider,enabled,environment,merchant_id,store_id,product_id_onetime)
		VALUES('waffo_pancake',true,'test','MER_compensation','STO_compensation','PROD_compensation')`); err != nil {
		t.Fatal(err)
	}
	runtime := NewWaffoRuntime(WaffoRuntimeConfig{ConnectorURL: connector.URL, ConnectorToken: "test-connector-token", Environment: "test"})
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, Provider: "waffo_pancake", WaffoEnvironment: "test", WaffoMerchantID: "MER_compensation", WaffoWebhookURL: connector.URL, WaffoConnectorToken: "test-connector-token"}, NewRuntimeCatalog(runtime))
	checkout, _, err := service.BeginProductCheckout(ctx, buyer, product, "waffo-compensation", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, source); err != nil {
		t.Fatal(err)
	}
	process := func(deliveryID, eventType, operation string) error {
		t.Helper()
		refundStatus := "succeeded"
		if eventType == "refund.failed" {
			refundStatus = "failed"
		}
		body, err := json.Marshal(map[string]any{"id": deliveryID, "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "eventType": eventType, "eventId": "PAY_compensation", "storeId": "STO_compensation", "mode": "test", "data": map[string]any{
			"orderId": "ORD_compensation", "orderMerchantExternalId": checkout.OrderID.String(), "merchantProvidedBuyerIdentity": buyer.String(), "currency": "USD", "amount": "19.00", "paymentId": "PAY_compensation", "paymentStatus": "succeeded", "refundStatus": refundStatus, "refundTicketMerchantExternalId": operation,
			"orderMetadata": map[string]string{"hcaiPaymentId": checkout.PaymentID.String(), "hcaiResourceId": product.String(), "hcaiPurpose": "product"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := service.ReceiveWaffoWebhook(ctx, body, "fixture-signature")
		if err != nil {
			t.Fatal(err)
		}
		return service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))})
	}
	if err := process("delivery-compensation-paid", "order.completed", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_provider_configs SET store_id='STO_new_sales' WHERE provider='waffo_pancake'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET email=$2 WHERE id=$1`, buyer, buyer.String()+"@changed.test"); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, checkout.PaymentID)); err != nil {
		t.Fatal(err)
	}
	// Original-store callbacks remain valid after the new-sales store changes.
	var operation uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT refund_operation_id FROM orders WHERE id=$1`, checkout.OrderID).Scan(&operation); err != nil {
		t.Fatal(err)
	}
	if err := process("delivery-compensation-mismatch", "refund.succeeded", uuid.NewString()); err == nil {
		t.Fatal("unrelated external refund operation accepted")
	}
	if err := process("delivery-compensation-failed", "refund.failed", operation.String()); err != nil {
		t.Fatal(err)
	}
	// A later administrative retry must not erase the signed identity of the
	// failed attempt. The admin service's snapshot path is tested separately.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	laterOperation := uuid.New()
	if _, err := tx.Exec(ctx, `UPDATE orders SET refund_operation_id=$2,refund_requested_at=now() WHERE id=$1`, checkout.OrderID, laterOperation); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_intents SET status='refund_pending',provider_refund_id=NULL WHERE id=$1`, checkout.PaymentID); err != nil {
		t.Fatal(err)
	}
	if err := RecordProductRefundAttemptTx(ctx, tx, checkout.PaymentID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"delivery-compensation-refund", "delivery-compensation-replay"} {
		if err := process(id, "refund.succeeded", operation.String()); err != nil {
			t.Fatal(err)
		}
	}
	if err := process("delivery-compensation-latefailure", "refund.failed", operation.String()); err != nil {
		t.Fatalf("matching late failure did not preserve confirmed refund: %v", err)
	}
	if err := process("delivery-compensation-wronglate", "refund.failed", uuid.NewString()); err == nil {
		t.Fatal("unrelated late failure was accepted")
	}
	var status, orderStatus, providerRefundID string
	var rights int
	if err := pool.QueryRow(ctx, `SELECT pi.status,o.status,pi.provider_refund_id,(SELECT count(*) FROM entitlements WHERE order_id=o.id)
		FROM payment_intents pi JOIN orders o ON o.id=pi.order_id WHERE pi.id=$1`, checkout.PaymentID).Scan(&status, &orderStatus, &providerRefundID, &rights); err != nil {
		t.Fatal(err)
	}
	if status != "refunded" || orderStatus != "refunded" || providerRefundID != "RFD_compensation" || rights != 0 {
		t.Fatalf("compensation result: %s/%s providerRefund=%s rights=%d", status, orderStatus, providerRefundID, rights)
	}
}
