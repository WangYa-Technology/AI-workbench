package payments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func waffoProductWebhookBody(t *testing.T, f *waffoRefundFixture, kind string, operation uuid.UUID) map[string]any {
	t.Helper()
	refundStatus := "succeeded"
	if kind == "refund.failed" {
		refundStatus = "failed"
	}
	return map[string]any{"id": "delivery_" + uuid.NewString(), "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "eventType": kind, "eventId": "PAY_dispatch", "storeId": "STO_dispatch", "mode": "test", "data": map[string]any{
		"orderId": "ORD_dispatch", "orderMerchantExternalId": f.checkout.OrderID.String(), "merchantProvidedBuyerIdentity": f.buyer.String(),
		"currency": "USD", "amount": "19.00", "paymentId": "PAY_dispatch", "paymentStatus": "succeeded", "refundStatus": refundStatus, "refundTicketMerchantExternalId": operation.String(),
		"orderMetadata": map[string]any{"hcaiPaymentId": f.checkout.PaymentID.String(), "hcaiResourceId": f.product.String(), "hcaiPurpose": "product"},
	}}
}

func processWaffoReceipt(t *testing.T, f *waffoRefundFixture, receipt Receipt) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"eventId": receipt.EventID})
	if err := f.service.HandlePaymentEventJob(context.Background(), jobs.Job{Kind: PaymentEventJobKind, Payload: payload}); err != nil {
		t.Fatal(err)
	}
}

func TestWaffoProductWebhookUsesOriginalIdentity(t *testing.T) {
	f := newWaffoRefundFixture(t, "await_payment")
	ctx := context.Background()
	// Disable new sales and change every mutable routing/profile field. A
	// retained verifier for the old environment still receives original results.
	if _, err := f.pool.Exec(ctx, `UPDATE payment_provider_configs SET enabled=false,merchant_id='MER_new',store_id='STO_new',environment='prod' WHERE provider='waffo_pancake'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE users SET email=$2,display_name='Renamed buyer' WHERE id=$1`, f.buyer, f.buyer.String()+"@changed.test"); err != nil {
		t.Fatal(err)
	}
	if _, enabled, _ := f.service.ProductProviderStatus(ctx); enabled {
		t.Fatal("new sales re-enabled by historical callbacks")
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO payment_provider_configs(provider,enabled,environment) VALUES('stripe',true,'test')`); err != nil {
		t.Fatal(err)
	}
	f.service.config.Provider = "stripe"
	body, _ := json.Marshal(waffoProductWebhookBody(t, f, "order.completed", uuid.Nil))
	receipt, err := f.service.ReceiveWaffoWebhook(ctx, body, "fixture-signature")
	if err != nil {
		t.Fatal(err)
	}
	processWaffoReceipt(t, f, receipt)
	assertCheckoutState(t, f.pool, f.checkout, "paid", "fulfilled", 1)
	replay, err := f.service.ReceiveWaffoWebhook(ctx, body, "fixture-signature")
	if err != nil || !replay.Duplicate || replay.EventID != receipt.EventID {
		t.Fatalf("replay lost original receipt: %#v %v", replay, err)
	}

	var count int
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM product_waffo_webhook_bindings WHERE event_id=$1 AND original_merchant_id='MER_dispatch' AND store_id='STO_dispatch' AND live_mode=false AND order_id=$2 AND buyer_id=$3`, receipt.EventID, f.checkout.OrderID, f.buyer).Scan(&count); err != nil || count != 1 {
		t.Fatalf("incorrect frozen provenance: %d %v", count, err)
	}
	if _, err = f.service.BeginProductRefund(ctx, f.buyer, f.checkout.OrderID, "old-provider-refund", "test", "The delivered content does not match the agreement."); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(ctx, `SELECT refund_operation_id FROM orders WHERE id=$1`, f.checkout.OrderID).Scan(&f.operation); err != nil {
		t.Fatal(err)
	}
	if err = f.service.HandleProductRefundJob(ctx, currentProductRefundJob(t, f.pool, f.checkout.PaymentID)); err != nil {
		t.Fatal(err)
	}
	refundBody, _ := json.Marshal(waffoProductWebhookBody(t, f, "refund.succeeded", f.operation))
	refund, err := f.service.ReceiveWaffoWebhook(ctx, refundBody, "fixture-signature")
	if err != nil {
		t.Fatal(err)
	}
	processWaffoReceipt(t, f, refund)
	assertCheckoutState(t, f.pool, f.checkout, "refunded", "refunded", 0)
	pkg, bodyExport := runProductExport(t, f.pool, f.buyer)
	events := pkg.Data.Marketplace.Data["providerEvents"]
	if len(events) != 2 {
		t.Fatalf("missing exported signed events: %d", len(events))
	}
	for _, event := range events {
		binding, ok := event["waffoBinding"].(map[string]any)
		if !ok || binding["storeId"] != "STO_dispatch" || binding["contractVersion"] != waffoWebhookContractVersion || binding["orderId"] != f.checkout.OrderID.String() {
			t.Fatalf("export lost binding: %#v", binding)
		}
	}
	if strings.Contains(string(bodyExport), "dispatch-test-token") {
		t.Fatal("verification credential leaked")
	}
	for _, sql := range []string{`UPDATE product_waffo_webhook_bindings SET store_id='STO_changed' WHERE event_id=$1`, `DELETE FROM product_waffo_webhook_bindings WHERE event_id=$1`} {
		if _, err = f.pool.Exec(ctx, sql, receipt.EventID); err == nil {
			t.Fatal("mutable webhook evidence")
		}
	}
	down, err := os.ReadFile("../platform/database/migrations/0108_product_waffo_webhook_bindings.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard verified product Waffo webhook bindings") {
		t.Fatalf("rollback discarded bindings: %v", err)
	}
	// A conflicting signed resend now preserves a funds hold; exercise it after the valid refund path.
	if _, err = f.service.ReceiveWaffoWebhook(ctx, append(append([]byte{}, body...), ' '), "fixture-signature"); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("same delivery id with changed raw body accepted: %v", err)
	}
}

func TestWaffoProductWebhookRejectsMismatchedIdentity(t *testing.T) {
	f := newWaffoRefundFixture(t, "await_payment")
	ctx := context.Background()
	mutations := map[string]func(map[string]any){
		"foreign store":     func(b map[string]any) { b["storeId"] = "STO_foreign" },
		"wrong environment": func(b map[string]any) { b["mode"] = "prod" },
		"wrong buyer":       func(b map[string]any) { b["data"].(map[string]any)["merchantProvidedBuyerIdentity"] = uuid.NewString() },
		"missing buyer":     func(b map[string]any) { delete(b["data"].(map[string]any), "merchantProvidedBuyerIdentity") },
		"wrong order":       func(b map[string]any) { b["data"].(map[string]any)["orderMerchantExternalId"] = uuid.NewString() },
		"missing order":     func(b map[string]any) { delete(b["data"].(map[string]any), "orderMerchantExternalId") },
		"wrong product": func(b map[string]any) {
			b["data"].(map[string]any)["orderMetadata"].(map[string]any)["hcaiResourceId"] = uuid.NewString()
		},
		"unknown payment": func(b map[string]any) {
			b["data"].(map[string]any)["orderMetadata"].(map[string]any)["hcaiPaymentId"] = uuid.NewString()
		},
		"false purpose": func(b map[string]any) {
			b["data"].(map[string]any)["orderMetadata"].(map[string]any)["hcaiPurpose"] = "wallet_topup"
		},
		"wrong amount":   func(b map[string]any) { b["data"].(map[string]any)["amount"] = "19.01" },
		"wrong currency": func(b map[string]any) { b["data"].(map[string]any)["currency"] = "EUR" },
		"wrong event":    func(b map[string]any) { b["eventType"] = "subscription.activated" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			b := waffoProductWebhookBody(t, f, "order.completed", uuid.Nil)
			mutate(b)
			raw, _ := json.Marshal(b)
			if _, err := f.service.ReceiveWaffoWebhook(ctx, raw, "fixture-signature"); !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("mismatched signed delivery accepted: %v", err)
			}
		})
	}
	raw, _ := json.Marshal(waffoProductWebhookBody(t, f, "order.completed", uuid.Nil))
	if _, err := f.service.ReceiveWaffoWebhook(ctx, raw, "wrong-signature"); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("bad signature accepted: %v", err)
	}
	f.service.config.Enabled = false
	if _, err := f.service.ReceiveWaffoWebhook(ctx, raw, "fixture-signature"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("global processing shutdown bypassed: %v", err)
	}
	f.service.config.Enabled = true
	if _, err := f.pool.Exec(ctx, `TRUNCATE product_checkout_dispatches,product_checkout_requests`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ReceiveWaffoWebhook(ctx, raw, "fixture-signature"); !errors.Is(err, ErrCheckoutReconciliation) {
		t.Fatalf("legacy identity inferred from current settings: %v", err)
	}
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM payment_provider_events WHERE payment_id=$1`, f.checkout.PaymentID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unbound events entered inbox: %d %v", count, err)
	}
	assertCheckoutState(t, f.pool, f.checkout, "checkout_open", "payment_pending", 0)
}

func TestWaffoProductWebhookVerifierContract(t *testing.T) {
	f := newWaffoRefundFixture(t, "await_payment")
	ctx := context.Background()
	body, _ := json.Marshal(waffoProductWebhookBody(t, f, "order.completed", uuid.Nil))
	hash := sha256.Sum256(body)
	for _, mode := range []string{"old_connector", "wrong_hash", "wrong_environment", "wrong_version", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			var redirected atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/redirected" {
					redirected.Add(1)
				}
				if mode == "redirect" && r.URL.Path == "/webhook/verify" {
					http.Redirect(w, r, "/redirected", 307)
					return
				}
				proof := map[string]any{"contractVersion": waffoWebhookContractVersion, "environment": "test", "payloadSHA256": hex.EncodeToString(hash[:])}
				switch mode {
				case "wrong_hash":
					proof["payloadSHA256"] = strings.Repeat("0", 64)
				case "wrong_environment":
					proof["environment"] = "prod"
				case "wrong_version":
					proof["contractVersion"] = "old"
				case "old_connector":
					_ = json.NewEncoder(w).Encode(map[string]any{"event": map[string]any{"id": "replacement-event"}})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"verification": proof})
			}))
			defer server.Close()
			f.service.config.WaffoWebhookURL = server.URL
			if _, err := f.service.ReceiveWaffoWebhook(ctx, body, "fixture-signature"); err == nil {
				t.Fatal("unverified original body accepted")
			}
			if redirected.Load() != 0 {
				t.Fatal("signature verification credentials followed redirect")
			}
		})
	}
}

func TestWaffoProductWebhookLegacyEventNeedsReverification(t *testing.T) {
	f := newWaffoRefundFixture(t, "await_payment")
	ctx := context.Background()
	body, _ := json.Marshal(waffoProductWebhookBody(t, f, "order.completed", uuid.Nil))
	receipt, err := f.service.ReceiveWaffoWebhook(ctx, body, "fixture-signature")
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce an old minimized inbox row: only this isolated fixture removes
	// the new binding table's data. Never infer the signed fields from settings.
	if _, err = f.pool.Exec(ctx, `TRUNCATE product_waffo_webhook_bindings`); err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../platform/database/migrations/0108_product_waffo_webhook_bindings.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard verified product Waffo webhook bindings") {
		t.Fatalf("rollback removed the unresolved legacy event guard: %v", err)
	}
	var envelope waffoWebhookEnvelope
	if err = json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	event, err := minimizeWaffoEvent(envelope, body, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	event.WaffoVerificationVersion = waffoWebhookContractVersion
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := bindWaffoProductWebhookTx(ctx, tx, event)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	event.ObjectID = "ORD_different_minimized_value"
	if err = saveWaffoProductWebhookBindingTx(ctx, tx, receipt.EventID, *binding, event); !errors.Is(err, ErrEventConflict) {
		_ = tx.Rollback(ctx)
		t.Fatalf("reverification blessed different old financial fields: %v", err)
	}
	_ = tx.Rollback(ctx)
	payload, _ := json.Marshal(map[string]any{"eventId": receipt.EventID})
	if err = f.service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: payload}); !errors.Is(err, ErrCheckoutReconciliation) {
		t.Fatalf("old inbox bypassed original-store proof: %v", err)
	}
	assertCheckoutState(t, f.pool, f.checkout, "checkout_open", "payment_pending", 0)
	replay, err := f.service.ReceiveWaffoWebhook(ctx, body, "fixture-signature")
	if err != nil || !replay.Duplicate {
		t.Fatalf("signed identical replay could not add evidence: %#v %v", replay, err)
	}
	processWaffoReceipt(t, f, replay)
	assertCheckoutState(t, f.pool, f.checkout, "paid", "fulfilled", 1)
}
