package payments

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func billingWaffoVerifier(t *testing.T, f *billingDispatchFixture) {
	t.Helper()
	connector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/webhook/verify" || r.Header.Get("x-waffo-signature") != "fixture-signature" || r.Header.Get("Authorization") != "Bearer billing-verifier-test" {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		digest := sha256.Sum256(raw)
		_ = json.NewEncoder(w).Encode(map[string]any{"verification": map[string]any{
			"contractVersion": waffoWebhookContractVersion, "environment": "test", "payloadSHA256": hex.EncodeToString(digest[:]),
		}})
	}))
	t.Cleanup(connector.Close)
	f.service.config.WaffoWebhookURL = connector.URL
	f.service.config.WaffoConnectorToken = "billing-verifier-test"
}

func billingWaffoBody(f *billingDispatchFixture, checkout BillingCheckout) map[string]any {
	kind := "order.completed"
	if f.purpose == "subscription" {
		kind = "subscription.activated"
	}
	capture := "PAY_" + checkout.PaymentID.String()
	return map[string]any{"id": "delivery_" + uuid.NewString(), "eventId": capture, "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"eventType": kind, "storeId": "STO_test", "mode": "test", "data": map[string]any{
			"orderId": "ORD_billing", "orderMerchantExternalId": checkout.PaymentID.String(), "merchantProvidedBuyerIdentity": f.buyer.String(),
			"amount": fmt.Sprintf("%d.%02d", checkout.AmountCents/100, checkout.AmountCents%100), "currency": "USD", "paymentId": capture, "paymentStatus": "succeeded",
			"orderMetadata": map[string]any{"hcaiPaymentId": checkout.PaymentID.String(), "hcaiResourceId": checkout.ResourceID.String(), "hcaiPurpose": f.purpose},
		}}
}

func billingReceiptJob(t *testing.T, receipt Receipt) jobs.Job {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"eventId": receipt.EventID})
	if err != nil {
		t.Fatal(err)
	}
	return jobs.Job{Kind: PaymentEventJobKind, Payload: payload}
}

func TestWaffoBillingWebhookUsesOriginalIdentity(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, purpose := range []string{"wallet_topup", "subscription"} {
		t.Run(purpose, func(t *testing.T) {
			f := newBillingDispatchFixture(t, pool, "waffo_pancake", purpose)
			billingWaffoVerifier(t, f)
			checkout, _, err := f.begin(t.Context(), "success")
			if err != nil {
				t.Fatal(err)
			}
			f.service.config.Provider = "stripe"
			f.service.config.WaffoStoreID = "STO_changed"
			f.service.config.WaffoMerchantID = "MER_changed"
			body, _ := json.Marshal(billingWaffoBody(f, checkout))
			receipt, err := f.service.ReceiveWaffoWebhook(t.Context(), body, "fixture-signature")
			if err != nil {
				t.Fatalf("original callback rejected after sales configuration change: %v", err)
			}
			if err := f.service.HandlePaymentEventJob(t.Context(), billingReceiptJob(t, receipt)); err != nil {
				t.Fatal(err)
			}
			var status string
			if err := pool.QueryRow(t.Context(), `SELECT status FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&status); err != nil || status != "paid" {
				t.Fatalf("original payment not fulfilled: status=%s err=%v", status, err)
			}
			replay, err := f.service.ReceiveWaffoWebhook(t.Context(), body, "fixture-signature")
			if err != nil || !replay.Duplicate || replay.EventID != receipt.EventID {
				t.Fatalf("lost receipt on replay: %+v %v", replay, err)
			}
			if _, err := f.service.ReceiveWaffoWebhook(t.Context(), append(append([]byte{}, body...), ' '), "fixture-signature"); !errors.Is(err, ErrEventConflict) {
				t.Fatalf("changed raw delivery accepted: %v", err)
			}
			var bindings, work int
			if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM billing_waffo_webhook_bindings WHERE event_id=$1 AND original_merchant_id='MER_test' AND store_id='STO_test' AND buyer_id=$2),
 (SELECT count(*) FROM jobs WHERE kind=$3 AND payload->>'eventId'=$1::text)`, receipt.EventID, f.buyer, PaymentEventJobKind).Scan(&bindings, &work); err != nil || bindings != 1 || work != 1 {
				t.Fatalf("binding or job not idempotent: %d %d %v", bindings, work, err)
			}
			for _, query := range []string{`UPDATE billing_waffo_webhook_bindings SET store_id='STO_changed' WHERE event_id=$1`, `DELETE FROM billing_waffo_webhook_bindings WHERE event_id=$1`} {
				if _, err := pool.Exec(t.Context(), query, receipt.EventID); err == nil {
					t.Fatal("binding evidence was mutable")
				}
			}
			down, err := os.ReadFile("../platform/database/migrations/0141_billing_waffo_webhook_bindings.down.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), string(down)); err == nil || !strings.Contains(err.Error(), "cannot discard verified billing Waffo webhook bindings") {
				t.Fatalf("rollback discarded evidence: %v", err)
			}
		})
	}
}

func TestWaffoBillingWebhookRejectsForeignIdentity(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, purpose := range []string{"wallet_topup", "subscription"} {
		f := newBillingDispatchFixture(t, pool, "waffo_pancake", purpose)
		billingWaffoVerifier(t, f)
		checkout, _, err := f.begin(t.Context(), "success")
		if err != nil {
			t.Fatal(err)
		}
		mutations := map[string]func(map[string]any){
			"wrong buyer":   func(b map[string]any) { b["data"].(map[string]any)["merchantProvidedBuyerIdentity"] = uuid.NewString() },
			"wrong order":   func(b map[string]any) { b["data"].(map[string]any)["orderMerchantExternalId"] = uuid.NewString() },
			"missing buyer": func(b map[string]any) { delete(b["data"].(map[string]any), "merchantProvidedBuyerIdentity") },
			"missing order": func(b map[string]any) { delete(b["data"].(map[string]any), "orderMerchantExternalId") },
			"wrong store":   func(b map[string]any) { b["storeId"] = "STO_other" },
			"wrong resource": func(b map[string]any) {
				b["data"].(map[string]any)["orderMetadata"].(map[string]any)["hcaiResourceId"] = uuid.NewString()
			},
			"missing purpose": func(b map[string]any) {
				delete(b["data"].(map[string]any)["orderMetadata"].(map[string]any), "hcaiPurpose")
			},
			"wrong amount":   func(b map[string]any) { b["data"].(map[string]any)["amount"] = "0.01" },
			"wrong currency": func(b map[string]any) { b["data"].(map[string]any)["currency"] = "EUR" },
			"refund": func(b map[string]any) {
				b["eventType"] = "refund.succeeded"
				b["data"].(map[string]any)["refundStatus"] = "succeeded"
			},
		}
		if purpose == "wallet_topup" {
			mutations["subscription event"] = func(b map[string]any) { b["eventType"] = "subscription.activated" }
		}
		for name, mutate := range mutations {
			t.Run(purpose+"/"+name, func(t *testing.T) {
				body := billingWaffoBody(f, checkout)
				mutate(body)
				raw, _ := json.Marshal(body)
				if _, err := f.service.ReceiveWaffoWebhook(t.Context(), raw, "fixture-signature"); !errors.Is(err, ErrInvalidEvent) {
					t.Fatalf("foreign identity admitted: %v", err)
				}
			})
		}
		var events int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM payment_provider_events WHERE payment_id=$1`, checkout.PaymentID).Scan(&events); err != nil || events != 0 {
			t.Fatalf("rejected event queued for processing: %d %v", events, err)
		}
		raw, _ := json.Marshal(billingWaffoBody(f, checkout))
		if _, err := f.service.ReceiveWaffoWebhook(t.Context(), raw, "invalid-signature"); !errors.Is(err, ErrInvalidSignature) {
			t.Fatalf("invalid signature accepted: %v", err)
		}
		f.service.config.Enabled = false
		if _, err := f.service.ReceiveWaffoWebhook(t.Context(), raw, "fixture-signature"); !errors.Is(err, ErrDisabled) {
			t.Fatalf("deployment shutdown bypassed: %v", err)
		}
	}
}

func TestWaffoBillingWebhookWorkerRequiresVerifiedEvidence(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, direction := range []string{"down", "up"} {
		migration, err := os.ReadFile("../platform/database/migrations/0141_billing_waffo_webhook_bindings." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), string(migration)); err != nil {
			t.Fatalf("empty-schema migration %s: %v", direction, err)
		}
	}
	f := newBillingDispatchFixture(t, pool, "waffo_pancake", "wallet_topup")
	billingWaffoVerifier(t, f)
	checkout, _, err := f.begin(t.Context(), "success")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(billingWaffoBody(f, checkout))
	receipt, err := f.service.ReceiveWaffoWebhook(t.Context(), body, "fixture-signature")
	if err != nil {
		t.Fatal(err)
	}
	// Model a pre-migration minimized event in this isolated schema only.
	if _, err := pool.Exec(t.Context(), `TRUNCATE billing_waffo_webhook_bindings`); err != nil {
		t.Fatal(err)
	}
	if err := f.service.HandlePaymentEventJob(t.Context(), billingReceiptJob(t, receipt)); !errors.Is(err, ErrCheckoutReconciliation) {
		t.Fatalf("unbound historical event changed funds: %v", err)
	}
	var status string
	if err := pool.QueryRow(t.Context(), `SELECT status FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&status); err != nil || status != "checkout_open" {
		t.Fatalf("unbound payment changed: %s %v", status, err)
	}
	down, err := os.ReadFile("../platform/database/migrations/0141_billing_waffo_webhook_bindings.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), string(down)); err == nil {
		t.Fatal("rollback discarded protection for an unprocessed legacy event")
	}
	replay, err := f.service.ReceiveWaffoWebhook(t.Context(), body, "fixture-signature")
	if err != nil || !replay.Duplicate {
		t.Fatalf("verified re-delivery could not bind old event: %+v %v", replay, err)
	}
	if err := f.service.HandlePaymentEventJob(t.Context(), billingReceiptJob(t, replay)); err != nil {
		t.Fatal(err)
	}
}

func TestWaffoBillingWebhookRequiresOriginalDispatch(t *testing.T) {
	for _, defect := range []string{"missing dispatch", "wrong digest", "changed event"} {
		t.Run(defect, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			f := newBillingDispatchFixture(t, pool, "waffo_pancake", "subscription")
			billingWaffoVerifier(t, f)
			checkout, _, err := f.begin(t.Context(), "success")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(billingWaffoBody(f, checkout))
			expected := ErrCheckoutReconciliation
			if defect == "changed event" {
				receipt, err := f.service.ReceiveWaffoWebhook(t.Context(), body, "fixture-signature")
				if err != nil {
					t.Fatal(err)
				}
				// Keep the raw digest, but simulate a legacy minimized tuple with a different amount.
				tx, err := pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(t.Context())
				if _, err := tx.Exec(t.Context(), `ALTER TABLE payment_provider_events DISABLE TRIGGER USER`); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(t.Context(), `UPDATE payment_provider_events SET amount_cents=amount_cents+1 WHERE id=$1`, receipt.EventID); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(t.Context(), `ALTER TABLE payment_provider_events ENABLE TRIGGER USER`); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(t.Context()); err != nil {
					t.Fatal(err)
				}
				if err := f.service.HandlePaymentEventJob(t.Context(), billingReceiptJob(t, receipt)); !errors.Is(err, ErrInvalidEvent) {
					t.Fatalf("worker trusted changed financial tuple: %v", err)
				}
				expected = ErrEventConflict
			} else {
				tx, err := pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(t.Context())
				if _, err := tx.Exec(t.Context(), `ALTER TABLE billing_checkout_dispatches DISABLE TRIGGER USER`); err != nil {
					t.Fatal(err)
				}
				query := `UPDATE billing_checkout_dispatches SET request_sha256=repeat('0',64) WHERE payment_id=$1`
				if defect == "missing dispatch" {
					query = `DELETE FROM billing_checkout_dispatches WHERE payment_id=$1`
				}
				if _, err := tx.Exec(t.Context(), query, checkout.PaymentID); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(t.Context(), `ALTER TABLE billing_checkout_dispatches ENABLE TRIGGER USER`); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.service.ReceiveWaffoWebhook(t.Context(), body, "fixture-signature"); !errors.Is(err, expected) {
				t.Fatalf("invalid evidence accepted: %v", err)
			}
		})
	}
}

func TestWaffoBillingWebhookExportIsolation(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	var fixtures []*billingDispatchFixture
	var receipts []Receipt
	for _, purpose := range []string{"wallet_topup", "subscription"} {
		f := newBillingDispatchFixture(t, pool, "waffo_pancake", purpose)
		billingWaffoVerifier(t, f)
		checkout, _, err := f.begin(t.Context(), "private-return-path")
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(billingWaffoBody(f, checkout))
		receipt, err := f.service.ReceiveWaffoWebhook(t.Context(), raw, "fixture-signature")
		if err != nil {
			t.Fatal(err)
		}
		fixtures, receipts = append(fixtures, f), append(receipts, receipt)
	}
	for i, f := range fixtures {
		marketplace, raw := runProductExport(t, pool, f.buyer)
		for name, rows := range marketplace.Data.Marketplace.Data {
			if name == "sellerFundsReconciliation" {
				if len(rows) != 1 || len(rows[0]) != 3 || rows[0]["version"] != float64(1) || rows[0]["unresolvedRecords"] != float64(0) {
					t.Fatal("billing-only export has unexpected financial metadata", rows)
				}
				if _, err := time.Parse(time.RFC3339Nano, rows[0]["asOf"].(string)); err != nil {
					t.Fatal("financial metadata timestamp missing", err)
				}
				continue
			}
			if len(rows) != 0 {
				t.Fatalf("billing-only user received marketplace records in %s", name)
			}
		}
		var decoded struct {
			Data struct {
				Bindings []map[string]any `json:"billingWebhookBindings"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		if len(decoded.Data.Bindings) != 1 || decoded.Data.Bindings[0]["eventId"] != receipts[i].EventID.String() || decoded.Data.Bindings[0]["storeId"] != "STO_test" {
			t.Fatalf("missing owned binding or leaked foreign evidence: %+v", decoded.Data.Bindings)
		}
		for _, secret := range []string{"billing-verifier-test", "private-return-path", "MER_test", "request_sha256", "orderMetadata", receipts[1-i].EventID.String()} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("export disclosed forbidden field %q", secret)
			}
		}
	}
}
