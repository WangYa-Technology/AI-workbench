package httpapi_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestStripeProductWebhookAfterSalesSwitchHTTP(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	buyer, seller, asset, product, order, payment := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO users(id,email,handle,display_name,role) VALUES($1,'stripebuyer@test.local','stripebuyer','Buyer','member'),($2,'stripeseller@test.local','stripeseller','Seller','creator')`, buyer, seller)
	exec(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 VALUES($1,$2,'image','Private source','/private.jpg','image/jpeg','clean','upload','hcai-commercial-standard-v1','local_file','fixture.jpg')`, asset, seller)
	exec(`INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status,ai_disclosure,included_files,compatibility)
 VALUES($1,$2,$3,'Stripe product','Webhook routing fixture','workflow',1900,'USD','hcai-commercial-standard-v1','active','AI-assisted','[]','HCAI CHAT')`, product, seller, asset)
	exec(`INSERT INTO orders(id,buyer_id,product_id,status,amount_cents,currency,idempotency_key,product_title_snapshot,license_name_snapshot,license_version,license_terms_snapshot,refund_window_days_snapshot)
 SELECT $1,$2,$3,'payment_pending',1900,'USD','stripe-switch-order','Stripe product',name,version,terms,refund_window_days
 FROM licenses WHERE code='hcai-commercial-standard-v1'`, order, buyer, product)
	exec(`INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key,provider_checkout_id)
 VALUES($1,'stripe','product',$2,$3,$4,$5,1900,'USD','checkout_pending',false,'stripe-switch-webhook','cs_original_order')`, payment, buyer, seller, product, order)
	exec(`INSERT INTO payment_provider_configs(provider,enabled,environment) VALUES('stripe',false,'test'),('waffo_pancake',true,'test')`)
	const secret = "whsec_stripe_original_callback"
	// The original verifier remains while new sales and runtimes select Waffo.
	cfg := config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", PaymentProvider: "waffo_pancake",
		WaffoEnabled: true, WaffoEnvironment: "test", StripeWebhookSecret: secret, StripeAPIVersion: "2026-02-25.clover", StripeWebhookToleranceSeconds: 300}
	server := httptest.NewServer(httpapi.New(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	now := time.Now().Unix()
	body := []byte(fmt.Sprintf(`{"id":"evt_stripe_original_order","object":"event","api_version":"2026-02-25.clover","created":%d,"livemode":false,"type":"checkout.session.completed","data":{"object":{"id":"cs_original_order","object":"checkout.session","payment_status":"paid","amount_total":1900,"currency":"usd","payment_intent":"pi_original_order","metadata":{"hcai_payment_id":%q,"hcai_resource_id":%q,"hcai_purpose":"product"}}}}`, now, payment.String(), product.String()))
	post := func(t *testing.T, candidate []byte, receipt *payments.Receipt) int {
		t.Helper()
		header := "t=" + fmt.Sprint(now) + ",v1=" + paymentStripeSignature(secret, now, candidate)
		var result any
		if receipt != nil {
			result = receipt
		}
		return postStripeWebhook(t, server.URL, candidate, header, "application/json", result).StatusCode
	}
	var first, duplicate payments.Receipt
	if status := post(t, body, &first); status != http.StatusAccepted {
		t.Fatalf("original callback after switch=%d", status)
	}
	if status := post(t, body, &duplicate); status != http.StatusOK || !duplicate.Duplicate || duplicate.EventID != first.EventID {
		t.Fatalf("duplicate callback=%d receipt=%+v", status, duplicate)
	}
	for _, tc := range []struct {
		name, old, replacement string
		status                 int
	}{
		{"unknown_payment", payment.String(), uuid.NewString(), http.StatusServiceUnavailable},
		{"wrong_product", product.String(), uuid.NewString(), http.StatusUnprocessableEntity},
		{"wrong_amount", `"amount_total":1900`, `"amount_total":1901`, http.StatusUnprocessableEntity},
		{"wrong_session", "cs_original_order", "cs_other_order", http.StatusUnprocessableEntity},
		{"connected_funds", `"data":`, `"account":"acct_another_merchant","data":`, http.StatusUnprocessableEntity},
		{"organization_funds", `"data":`, `"context":"acct_another_merchant","data":`, http.StatusUnprocessableEntity},
		{"wrong_mode", `"livemode":false`, `"livemode":true`, http.StatusConflict},
		{"wrong_version", "2026-02-25.clover", "2025-01-01.acacia", http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if status := post(t, []byte(strings.Replace(string(body), tc.old, tc.replacement, 1)), nil); status != tc.status {
				t.Fatalf("status=%d want=%d", status, tc.status)
			}
		})
	}
	if status := post(t, append(append([]byte{}, body...), ' '), nil); status != http.StatusConflict {
		t.Fatalf("changed delivery body=%d", status)
	}
	if response := postStripeWebhook(t, server.URL, body, "invalid", "application/json", nil); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid signature=%d", response.StatusCode)
	}
	var events, queued, rights int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM payment_provider_events),
 (SELECT count(*) FROM jobs WHERE kind=$1),(SELECT count(*) FROM entitlements WHERE order_id=$2)`, payments.PaymentEventJobKind, order).Scan(&events, &queued, &rights); err != nil || events != 1 || queued != 1 || rights != 0 {
		t.Fatalf("receipt must only queue one event: events=%d jobs=%d rights=%d err=%v", events, queued, rights, err)
	}
}
