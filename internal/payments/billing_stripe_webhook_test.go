package payments

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func billingStripeBody(t *testing.T, f *billingDispatchFixture, checkout BillingCheckout) map[string]any {
	t.Helper()
	var session string
	if err := f.pool.QueryRow(t.Context(), `SELECT provider_checkout_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&session); err != nil {
		t.Fatal(err)
	}
	return map[string]any{"id": "evt_" + strings.ReplaceAll(uuid.NewString(), "-", ""), "object": "event", "api_version": testStripeAPIVersion, "created": time.Now().Unix(), "livemode": false, "type": "checkout.session.completed",
		"data": map[string]any{"object": map[string]any{"id": session, "object": "checkout.session", "status": "complete", "payment_status": "paid",
			"amount_total": checkout.AmountCents, "currency": "usd", "payment_intent": "pi_" + strings.ReplaceAll(checkout.PaymentID.String(), "-", ""),
			"metadata": map[string]any{"hcai_payment_id": checkout.PaymentID.String(), "hcai_resource_id": checkout.ResourceID.String(), "hcai_purpose": checkout.Purpose}}}}
}

func receiveBillingStripe(t *testing.T, f *billingDispatchFixture, body map[string]any) (Receipt, error) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	f.service.config.APIVersion = testStripeAPIVersion
	f.service.verifier = NewStripeWebhookVerifier(testStripeWebhookSecret, 5*time.Minute)
	now := time.Now().Unix()
	return f.service.ReceiveStripeWebhook(t.Context(), raw, fmt.Sprintf("t=%d,v1=%s", now, stripeSignature(testStripeWebhookSecret, now, raw)))
}

func TestStripeBillingWebhookOriginalSession(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, purpose := range []string{"wallet_topup", "subscription"} {
		f := newBillingDispatchFixture(t, pool, "stripe", purpose)
		checkout, _, err := f.begin(t.Context(), "success")
		if err != nil {
			t.Fatal(err)
		}
		for _, wrong := range []string{"session", "resource"} {
			t.Run(purpose+"/"+wrong, func(t *testing.T) {
				body := billingStripeBody(t, f, checkout)
				object := body["data"].(map[string]any)["object"].(map[string]any)
				if wrong == "session" {
					object["id"] = "cs_otherpayment"
				} else {
					object["metadata"].(map[string]any)["hcai_resource_id"] = uuid.NewString()
				}
				if _, err := receiveBillingStripe(t, f, body); !errors.Is(err, ErrInvalidEvent) {
					t.Fatalf("foreign %s admitted: %v", wrong, err)
				}
			})
		}
	}
}

func TestStripeBillingWebhookAfterSalesSwitch(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, purpose := range []string{"wallet_topup", "subscription"} {
		t.Run(purpose, func(t *testing.T) {
			f := newBillingDispatchFixture(t, pool, "stripe", purpose)
			checkout, _, err := f.begin(t.Context(), "success")
			if err != nil {
				t.Fatal(err)
			}
			f.service.config.Provider = "waffo_pancake"
			receipt, err := receiveBillingStripe(t, f, billingStripeBody(t, f, checkout))
			if err != nil {
				t.Fatalf("old obligation rejected: %v", err)
			}
			if err := f.service.HandlePaymentEventJob(t.Context(), billingReceiptJob(t, receipt)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStripeBillingWebhookFailedAttemptThenSuccess(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, purpose := range []string{"wallet_topup", "subscription"} {
		t.Run(purpose, func(t *testing.T) {
			f := newBillingDispatchFixture(t, pool, "stripe", purpose)
			checkout, _, err := f.begin(t.Context(), "success")
			if err != nil {
				t.Fatal(err)
			}
			body := billingStripeBody(t, f, checkout)
			body["type"] = "checkout.session.async_payment_failed"
			body["data"].(map[string]any)["object"].(map[string]any)["payment_status"] = "unpaid"
			receipt, err := receiveBillingStripe(t, f, body)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.service.HandlePaymentEventJob(t.Context(), billingReceiptJob(t, receipt)); err != nil {
				t.Fatal(err)
			}
			receipt, err = receiveBillingStripe(t, f, billingStripeBody(t, f, checkout))
			if err != nil {
				t.Fatal(err)
			}
			if err := f.service.HandlePaymentEventJob(t.Context(), billingReceiptJob(t, receipt)); err != nil {
				t.Fatalf("verified success blocked by earlier attempt: %v", err)
			}
		})
	}
}
