package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

func pendingStripeWebhookFixture(t *testing.T) (*Service, *pgxpool.Pool, Checkout, uuid.UUID, uuid.UUID) {
	t.Helper()
	pool, cleanup := paymentTestPool(t)
	t.Cleanup(cleanup)
	buyer, _, _, product := newProductCheckoutFixture(t, pool)
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion,
		WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(&productCheckoutRuntime{}))
	checkout, _, err := service.BeginProductCheckout(context.Background(), buyer, product, "stripe-webhook-routing", "test",
		"https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product))
	if err != nil {
		t.Fatal(err)
	}
	return service, pool, checkout, buyer, product
}

func processStripeReceipt(t *testing.T, service *Service, pool *pgxpool.Pool, receipt Receipt) {
	t.Helper()
	job := jobs.Job{Kind: PaymentEventJobKind}
	if err := pool.QueryRow(context.Background(), `SELECT id,payload FROM jobs WHERE kind=$1 AND payload->>'eventId'=$2`,
		PaymentEventJobKind, receipt.EventID.String()).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	if err := service.HandlePaymentEventJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
}

func TestStripeProductWebhookSurvivesSalesSwitch(t *testing.T) {
	for _, mode := range []string{"disabled_row", "waffo_selection", "no_stripe_runtime"} {
		t.Run(mode, func(t *testing.T) {
			original, pool, checkout, buyer, product := pendingStripeWebhookFixture(t)
			ctx := context.Background()
			if _, err := pool.Exec(ctx, `INSERT INTO payment_provider_configs(provider,enabled,environment)
 VALUES('stripe',false,'test') ON CONFLICT(provider) DO UPDATE SET enabled=false`); err != nil {
				t.Fatal(err)
			}
			cfg := original.config
			if mode != "disabled_row" {
				cfg.Provider = "waffo_pancake"
				if _, err := pool.Exec(ctx, `INSERT INTO payment_provider_configs(provider,enabled,environment)
 VALUES('waffo_pancake',true,'test')`); err != nil {
					t.Fatal(err)
				}
			}
			runtimes := original.runtimes
			if mode == "no_stripe_runtime" {
				runtimes = NewRuntimeCatalog()
			}
			service := NewServiceWithRuntimes(pool, cfg, runtimes)
			now := time.Now().UTC().Truncate(time.Second)
			body := productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents)
			receipt := receivePaymentWorkflowEvent(t, service, body, now)
			processStripeReceipt(t, service, pool, receipt)
			assertProductRefundState(t, pool, checkout, "paid", "fulfilled", "active", 0, 0)
			if service.webhookProviderEnabled(ctx, "stripe") {
				t.Fatal("reception re-enabled new Stripe sales")
			}
			header := "t=" + fmt.Sprint(now.Unix()) + ",v1=" + stripeSignature(testStripeWebhookSecret, now.Unix(), body)
			if again, err := service.ReceiveStripeWebhook(ctx, body, header); err != nil || !again.Duplicate || again.EventID != receipt.EventID || again.Status != "processed" {
				t.Fatalf("duplicate changed event identity: %#v err=%v", again, err)
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind=$1 AND payload->>'eventId'=$2`, PaymentEventJobKind, receipt.EventID.String()).Scan(&count); err != nil || count != 1 {
				t.Fatalf("duplicate jobs=%d err=%v", count, err)
			}
			// Existing refund dispatch retains the original runtime. Reception
			// itself must not need API credentials or enable another sale.
			if _, err := original.BeginProductRefund(ctx, buyer, checkout.OrderID, "stripe-routing-refund", "test", "The delivered content does not meet the purchase description."); err != nil {
				t.Fatal(err)
			}
			if err := original.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, checkout.PaymentID)); err != nil {
				t.Fatal(err)
			}
			refund := productRefundEvent("evt_routing_refund", "re_workflow123", "succeeded", checkout.PaymentID, product, now.Unix(), checkout.AmountCents)
			// Production CreateRefund sends only payment/operation metadata.
			refund = []byte(strings.Replace(string(refund), fmt.Sprintf(`,"hcai_resource_id":%q,"hcai_purpose":"product"`, product.String()), "", 1))
			refundReceipt := receivePaymentWorkflowEvent(t, service, refund, now)
			processStripeReceipt(t, service, pool, refundReceipt)
			assertProductRefundState(t, pool, checkout, "refunded", "refunded", "refunded", 0, 0)
		})
	}
}

func TestStripeProductWebhookRejectsWrongTransaction(t *testing.T) {
	service, pool, checkout, _, product := pendingStripeWebhookFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	for _, salesEnabled := range []bool{true, false} {
		if _, err := pool.Exec(ctx, `INSERT INTO payment_provider_configs(provider,enabled,environment)
 VALUES('stripe',$1,'test') ON CONFLICT(provider) DO UPDATE SET enabled=excluded.enabled`, salesEnabled); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"product", "purpose", "amount", "session", "object_type", "missing_product", "missing_purpose", "mode", "connected_account", "organization"} {
			t.Run(fmt.Sprintf("sales_%t_%s", salesEnabled, field), func(t *testing.T) {
				var event map[string]any
				if err := json.Unmarshal(productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents), &event); err != nil {
					t.Fatal(err)
				}
				object := event["data"].(map[string]any)["object"].(map[string]any)
				metadata := object["metadata"].(map[string]any)
				switch field {
				case "product":
					metadata["hcai_resource_id"] = uuid.NewString()
				case "purpose":
					metadata["hcai_purpose"] = "task"
				case "amount":
					object["amount_total"] = checkout.AmountCents + 1
				case "session":
					object["id"] = "cs_another_order"
				case "object_type":
					object["object"], object["id"] = "payment_intent", "pi_wrong_object"
				case "missing_product":
					delete(metadata, "hcai_resource_id")
				case "missing_purpose":
					delete(metadata, "hcai_purpose")
				case "mode":
					event["livemode"] = true
					service.config.LiveMode = true // verifier environment alone is insufficient
					defer func() { service.config.LiveMode = false }()
				case "connected_account":
					event["account"] = "acct_other_merchant"
				case "organization":
					event["context"] = "acct_other_merchant"
				}
				body, _ := json.Marshal(event)
				header := "t=" + fmt.Sprint(now.Unix()) + ",v1=" + stripeSignature(testStripeWebhookSecret, now.Unix(), body)
				if _, err := service.ReceiveStripeWebhook(ctx, body, header); !errors.Is(err, ErrInvalidEvent) {
					t.Fatalf("mismatched transaction accepted: %v", err)
				}
			})
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_provider_events`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected events persisted=%d err=%v", count, err)
	}
	// A signed event claiming product metadata is insufficient to bypass the
	// sales gate when the local transaction is unknown or belongs to billing.
	billingID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_intents(id,provider,purpose,payer_id,resource_id,amount_cents,currency,status,live_mode,idempotency_key)
 SELECT $2,'stripe','wallet_topup',payer_id,payer_id,1900,'USD','checkout_pending',false,'stripe-billing-routing' FROM payment_intents WHERE id=$1`, checkout.PaymentID, billingID); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		paymentID uuid.UUID
		want      error
	}{
		{"unknown_payment", uuid.New(), ErrDisabled},
		// Known billing obligations are authenticated before the new-sales gate.
		// Spoofed product metadata must fail that binding, even with sales off.
		{"billing_as_product", billingID, ErrInvalidEvent},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := productPaidEvent(test.paymentID, product, now.Unix(), checkout.AmountCents)
			header := "t=" + fmt.Sprint(now.Unix()) + ",v1=" + stripeSignature(testStripeWebhookSecret, now.Unix(), body)
			if _, err := service.ReceiveStripeWebhook(ctx, body, header); !errors.Is(err, test.want) {
				t.Fatalf("non-product transaction: got %v, want %v", err, test.want)
			}
		})
	}
	var eventJobs int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM payment_provider_events),
 (SELECT count(*) FROM jobs WHERE kind=$1)`, PaymentEventJobKind).Scan(&count, &eventJobs); err != nil || count != 0 || eventJobs != 0 {
		t.Fatalf("rejected transactions produced events=%d jobs=%d err=%v", count, eventJobs, err)
	}
	body := productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents)
	if _, err := service.ReceiveStripeWebhook(ctx, body, "invalid"); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("sales bypass skipped signature: %v", err)
	}
	service.config.Enabled = false
	if _, err := service.ReceiveStripeWebhook(ctx, body, "invalid"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("global stop bypassed: %v", err)
	}
}

func TestStripeWebhookConnectedAccountStatusBoundary(t *testing.T) {
	// Connected-account status updates are legitimate; connected-account funds
	// are not platform funds. Preserve onboarding while enforcing that boundary.
	for _, account := range []string{"", "acct_onboarding", "acct_other_account"} {
		body := []byte(fmt.Sprintf(`{"id":"evt_account_source","object":"event","api_version":%q,"created":%d,"livemode":false,"type":"account.updated","account":%q,"data":{"object":{"id":"acct_onboarding","object":"account","charges_enabled":true,"payouts_enabled":true,"details_submitted":true}}}`, testStripeAPIVersion, time.Now().Unix(), account))
		_, err := minimizeStripeEvent(body, testStripeAPIVersion, false)
		if account == "acct_other_account" {
			if !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("wrong connected account accepted: %v", err)
			}
		} else if err != nil {
			t.Fatalf("legitimate onboarding update rejected: %v", err)
		}
	}
}

func TestStripeProductWebhookDelayedFailedChargePreservesPaidOrder(t *testing.T) {
	service, pool, checkout, _, product := pendingStripeWebhookFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	paid := []byte(strings.Replace(string(productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents)),
		`"payment_intent":"pi_workflow123"`, `"payment_intent":"pi_workflow123","charge":"ch_successful_attempt"`, 1))
	processStripeReceipt(t, service, pool, receivePaymentWorkflowEvent(t, service, paid, now))
	if _, err := pool.Exec(ctx, `INSERT INTO payment_provider_configs(provider,enabled,environment) VALUES('stripe',false,'test')`); err != nil {
		t.Fatal(err)
	}
	failed := []byte(fmt.Sprintf(`{"id":"evt_previous_attempt_failed","object":"event","api_version":%q,"created":%d,"livemode":false,"type":"payment_intent.payment_failed","data":{"object":{"id":"pi_workflow123","object":"payment_intent","status":"requires_payment_method","amount":%d,"currency":"usd","latest_charge":"ch_previous_failed_attempt","metadata":{"hcai_payment_id":%q,"hcai_resource_id":%q,"hcai_purpose":"product"}}}}`, testStripeAPIVersion, now.Unix(), checkout.AmountCents, checkout.PaymentID.String(), product.String()))
	processStripeReceipt(t, service, pool, receivePaymentWorkflowEvent(t, service, failed, now))
	assertProductRefundState(t, pool, checkout, "paid", "fulfilled", "active", 0, 0)
	var charge string
	if err := pool.QueryRow(ctx, `SELECT provider_charge_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&charge); err != nil || charge != "ch_successful_attempt" {
		t.Fatalf("late failure replaced successful charge: %s %v", charge, err)
	}
	// Success for a different Charge must still be rejected, even if the
	// PaymentIntent and metadata are otherwise identical.
	wrongSuccess := []byte(strings.NewReplacer("evt_previous_attempt_failed", "evt_different_charge_success", "payment_intent.payment_failed", "payment_intent.succeeded", "requires_payment_method", "succeeded").Replace(string(failed)))
	header := "t=" + fmt.Sprint(now.Unix()) + ",v1=" + stripeSignature(testStripeWebhookSecret, now.Unix(), wrongSuccess)
	if _, err := service.ReceiveStripeWebhook(ctx, wrongSuccess, header); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("different successful charge was accepted: %v", err)
	}
}

func TestStripeProductWebhookWorkerRechecksStoredEvidence(t *testing.T) {
	service, pool, checkout, _, _ := pendingStripeWebhookFixture(t)
	ctx := context.Background()
	for _, mismatch := range []string{"product", "mode", "session"} {
		t.Run(mismatch, func(t *testing.T) {
			// Model an immutable event admitted by the older API. Do not mutate
			// existing evidence or rely solely on new ingress validation.
			var eventID uuid.UUID
			if err := pool.QueryRow(ctx, `INSERT INTO payment_provider_events(provider,provider_event_id,event_type,api_version,live_mode,occurred_at,payload_sha256,
 object_id,object_type,payment_id,resource_id,purpose,amount_cents,currency,payment_status,provider_payment_id)
 SELECT 'stripe',$2,'checkout.session.completed',$3,$4='mode',now(),repeat('a',64),
 CASE WHEN $4='session' THEN 'cs_wrong_order' ELSE pi.provider_checkout_id END,'checkout.session',pi.id,
 CASE WHEN $4='product' THEN gen_random_uuid() ELSE pi.resource_id END,'product',pi.amount_cents,pi.currency,'paid','pi_workflow123'
 FROM payment_intents pi WHERE id=$1 RETURNING id`, checkout.PaymentID, "evt_old_"+mismatch, testStripeAPIVersion, mismatch).Scan(&eventID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO payment_provider_event_processing(event_id,status) VALUES($1,'received')`, eventID); err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(paymentEventJobPayload{EventID: eventID})
			if err := service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: payload}); err == nil || jobs.ShouldRetry(err) {
				t.Fatalf("worker accepted wrong %s: %v", mismatch, err)
			}
		})
	}
	var status string
	var rights int
	if err := pool.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM entitlements WHERE order_id=$1) FROM orders WHERE id=$1`, checkout.OrderID).Scan(&status, &rights); err != nil || status != "payment_pending" || rights != 0 {
		t.Fatalf("invalid worker event changed order: status=%s rights=%d err=%v", status, rights, err)
	}
}
