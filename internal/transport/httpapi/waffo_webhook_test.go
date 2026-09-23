package httpapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestWaffoWebhookHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := context.Background()
	connector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/webhook/verify" || r.Header.Get("Authorization") != "Bearer fixture-connector-token" {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.Header.Get("x-waffo-signature") != "verified-fixture" {
			http.Error(w, "bad signature", 401)
			return
		}
		body, _ := io.ReadAll(r.Body)
		hash := sha256.Sum256(body)
		_ = json.NewEncoder(w).Encode(map[string]any{"verification": map[string]any{"contractVersion": "waffo-webhook-v1", "environment": "test", "payloadSHA256": hex.EncodeToString(hash[:])}})
	}))
	defer connector.Close()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true, PaymentProvider: "waffo_pancake", WaffoEnabled: true, WaffoEnvironment: "test", WaffoMerchantID: "MER_current", WaffoStoreID: "STO_current", WaffoConnectorURL: connector.URL, WaffoConnectorToken: "fixture-connector-token"}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	buyer, seller, asset, product, order, payment := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []uuid.UUID{buyer, seller} {
		exec(`INSERT INTO users(id,email,handle,display_name) VALUES($1,$2,$3,'Webhook fixture')`, user, user.String()+"@test.local", "webhook_"+user.String()[:8])
	}
	exec(`INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key) VALUES($1,$2,'image','Webhook source','/media/fixture.jpg','image/jpeg','clean','generation','hcai-commercial-standard-v1','local_file','fixture.jpg')`, asset, seller)
	exec(`INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status) VALUES($1,$2,$3,'Webhook item','Fixture','asset',1900,'USD','hcai-commercial-standard-v1','active')`, product, seller, asset)
	exec(`INSERT INTO orders(id,buyer_id,product_id,amount_cents,currency,status,idempotency_key,product_title_snapshot,license_name_snapshot,license_version,license_terms_snapshot,refund_window_days_snapshot) SELECT $1,$2,$3,1900,'USD','payment_pending','webhook-order','Webhook item',name,version,terms,refund_window_days FROM licenses WHERE code='hcai-commercial-standard-v1'`, order, buyer, product)
	exec(`INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,order_id,amount_cents,currency,status,live_mode,idempotency_key) VALUES($1,'waffo_pancake','product',$2,$3,$4,$5,1900,'USD','checkout_pending',false,'webhook-payment')`, payment, buyer, seller, product, order)
	exec(`INSERT INTO product_checkout_requests(payment_id,identity,request) SELECT pi.id,jsonb_build_object('provider',pi.provider,'merchantId','MER_original','storeId','STO_original','liveMode',false,'endpoint',$2::text,'apiVersion','pancake-ts-0.19.1','requestVersion','waffo-product-checkout-v1'),jsonb_build_object('PaymentID',pi.id,'Purpose','product','ResourceID',pi.resource_id,'OrderExternalID',pi.order_id,'BuyerIdentity',pi.payer_id,'AmountCents',pi.amount_cents,'Currency',pi.currency) FROM payment_intents pi WHERE pi.id=$1`, payment, connector.URL)
	exec(`INSERT INTO payment_provider_configs(provider,enabled,environment,merchant_id,store_id) VALUES('waffo_pancake',false,'test','MER_new','STO_new')`)
	body := map[string]any{"id": "delivery_http_original", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "eventType": "order.completed", "eventId": "PAY_http_original", "storeId": "STO_original", "mode": "test", "data": map[string]any{"orderId": "ORD_http_original", "orderMerchantExternalId": order.String(), "merchantProvidedBuyerIdentity": buyer.String(), "currency": "USD", "amount": "19.00", "paymentId": "PAY_http_original", "paymentStatus": "succeeded", "orderMetadata": map[string]string{"hcaiPaymentId": payment.String(), "hcaiResourceId": product.String(), "hcaiPurpose": "product"}}}
	post := func(raw []byte, signature, contentType string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/payments/webhooks/waffo", bytes.NewReader(raw))
		req.Header.Set("x-waffo-signature", signature)
		req.Header.Set("Origin", "https://payment-provider.example.test")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Content-Type", contentType)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var decoded map[string]any
		if err = json.NewDecoder(res.Body).Decode(&decoded); err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, decoded
	}
	raw, _ := json.Marshal(body)
	gate, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(t.Context())
	if _, err = gate.Exec(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "webhook-admission:waffo_pancake:delivery_http_original"); err != nil {
		t.Fatal(err)
	}
	if code, result := post(raw, "verified-fixture", "application/json"); code != 503 {
		t.Fatalf("concurrent admission was not retryable: %d %#v", code, result)
	} else if failure, ok := result["error"].(map[string]any); !ok || failure["code"] != "payment_event_busy" || failure["retryable"] != true {
		t.Fatal("missing retryable admission error", result)
	}
	if err = gate.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if code, _ := post(raw, "verified-fixture", "application/json"); code != 202 {
		t.Fatalf("original store rejected after sales disabled: %d", code)
	}
	if code, receipt := post(raw, "verified-fixture", "application/json"); code != 200 || receipt["duplicate"] != true {
		t.Fatalf("duplicate contract: %d %#v", code, receipt)
	}
	if code, _ := post(raw, "invalid", "application/json"); code != 401 {
		t.Fatalf("signature error: %d", code)
	}
	if code, _ := post(raw, strings.Repeat("x", 4097), "application/json"); code != 401 {
		t.Fatalf("signature header limit: %d", code)
	}
	if code, _ := post(raw, "verified-fixture", "text/plain"); code != 415 {
		t.Fatalf("content type: %d", code)
	}
	if code, _ := post(bytes.Repeat([]byte("x"), 1024*1024+1), "verified-fixture", "application/json"); code != 413 {
		t.Fatalf("body limit: %d", code)
	}
	body["id"] = "delivery_http_wrong_store"
	body["storeId"] = "STO_current"
	raw, _ = json.Marshal(body)
	if code, _ := post(raw, "verified-fixture", "application/json"); code != 422 {
		t.Fatalf("current store substituted for original: %d", code)
	}
	exec(`TRUNCATE product_checkout_dispatches,product_checkout_requests`)
	body["storeId"] = "STO_original"
	raw, _ = json.Marshal(body)
	code, result := post(raw, "verified-fixture", "application/json")
	if code != 409 {
		t.Fatalf("missing original evidence: %d %#v", code, result)
	}
	var receipts, jobs int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM product_waffo_webhook_bindings WHERE payment_id=$1),(SELECT count(*) FROM jobs j JOIN payment_provider_events e ON j.payload->>'eventId'=e.id::text WHERE e.payment_id=$1 AND j.kind='payment.process_event')`, payment).Scan(&receipts, &jobs); err != nil || receipts != 1 || jobs != 1 {
		t.Fatalf("invalid event persisted or replay duplicated work: %d %d %v", receipts, jobs, err)
	}
	for _, purpose := range []string{"wallet_topup", "subscription"} {
		t.Run(purpose, func(t *testing.T) {
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			billingPayment, resource := uuid.New(), buyer
			productType, eventType := "onetime", "order.completed"
			if purpose == "subscription" {
				productType, eventType = "subscription", "subscription.activated"
				if err := pool.QueryRow(ctx, `SELECT id FROM subscription_plans WHERE tier_code='creator'`).Scan(&resource); err != nil {
					t.Fatal(err)
				}
			}
			exec(`WITH intent AS (
 INSERT INTO payment_intents(id,provider,purpose,payer_id,resource_id,amount_cents,currency,status,live_mode,idempotency_key)
 VALUES($1::uuid,'waffo_pancake',$2,$3,$4,1900,'USD','checkout_pending',false,($1::uuid)::text) RETURNING *)
 INSERT INTO subscription_checkout_contracts(payment_id,buyer_id,plan_id,plan_name,tier_code,description,price_cents,currency,included_points,billing_period_days,model_ids)
 SELECT i.id,i.payer_id,p.id,p.name,p.tier_code,p.description,i.amount_cents,i.currency,p.included_points,p.billing_period_days,
 ARRAY(SELECT m.provider_model_id FROM subscription_plan_models m WHERE m.plan_id=p.id ORDER BY m.provider_model_id)
 FROM intent i JOIN subscription_plans p ON p.id=i.resource_id WHERE i.purpose='subscription'`, billingPayment, purpose, buyer, resource)
			exec(`INSERT INTO billing_checkout_requests(payment_id,identity,request)
 SELECT id,jsonb_build_object('provider',provider,'merchantId','MER_original','storeId','STO_original','liveMode',false,'endpoint',$2::text,'apiVersion','pancake-ts-0.19.1','requestVersion','waffo-product-checkout-v1'),
 jsonb_build_object('PaymentID',id,'Purpose',purpose,'ResourceID',resource_id,'OrderExternalID',id,'BuyerIdentity',payer_id,'AmountCents',amount_cents,'Currency',currency,'ProductID','PROD_billing','ProductType',$3::text)
 FROM payment_intents WHERE id=$1`, billingPayment, connector.URL, productType)
			exec(`INSERT INTO billing_checkout_dispatches(payment_id,request_sha256,payment_version)
 SELECT r.payment_id,encode(public.digest(r.request::text,'sha256'),'hex'),p.version
 FROM billing_checkout_requests r JOIN payment_intents p ON p.id=r.payment_id WHERE r.payment_id=$1`, billingPayment)
			billingBody := map[string]any{"id": "delivery_" + billingPayment.String(), "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
				"eventType": eventType, "eventId": "PAY_" + billingPayment.String(), "storeId": "STO_original", "mode": "test", "data": map[string]any{
					"orderId": "ORD_" + billingPayment.String(), "orderMerchantExternalId": billingPayment.String(), "merchantProvidedBuyerIdentity": buyer.String(),
					"amount": "19.00", "currency": "USD", "paymentId": "PAY_" + billingPayment.String(), "paymentStatus": "succeeded",
					"orderMetadata": map[string]string{"hcaiPaymentId": billingPayment.String(), "hcaiResourceId": resource.String(), "hcaiPurpose": purpose}}}
			billingRaw, _ := json.Marshal(billingBody)
			if code, result := post(billingRaw, "verified-fixture", "application/json"); code != 202 {
				t.Fatalf("original billing callback rejected with sales disabled: %d %+v", code, result)
			}
			if code, result := post(billingRaw, "verified-fixture", "application/json"); code != 200 || result["duplicate"] != true {
				t.Fatalf("billing duplicate receipt: %d %+v", code, result)
			}
			billingBody["id"] = "delivery_wrong_" + billingPayment.String()
			billingBody["data"].(map[string]any)["merchantProvidedBuyerIdentity"] = seller.String()
			billingRaw, _ = json.Marshal(billingBody)
			if code, _ := post(billingRaw, "verified-fixture", "application/json"); code != 422 {
				t.Fatalf("foreign billing buyer accepted: %d", code)
			}
			billingBody["data"].(map[string]any)["merchantProvidedBuyerIdentity"] = buyer.String()
			// Keep fixture cleanup aligned with the production FK graph: webhook
			// evidence rows reference checkout dispatches, which reference requests.
			exec(`TRUNCATE billing_stripe_webhook_bindings,billing_stripe_checkout_bindings,
 billing_waffo_webhook_bindings,billing_checkout_dispatches,billing_checkout_requests`)
			billingRaw, _ = json.Marshal(billingBody)
			if code, result := post(billingRaw, "verified-fixture", "application/json"); code != 409 {
				t.Fatalf("missing billing request inferred: %d %+v", code, result)
			}
		})
	}
}
