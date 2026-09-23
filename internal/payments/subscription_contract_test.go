package payments

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestSubscriptionContractPlanUpdateCannotSplitSnapshot(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	t.Cleanup(cleanup)
	f := newBillingDispatchFixture(t, pool, "stripe", "subscription")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	traced, entered, release := testutil.GateQuery(t, pool, "INSERT INTO subscription_checkout_contracts")
	f.service = newPaymentTestService(t, traced, f.service.config, NewRuntimeCatalog(f.runtime))
	var wg sync.WaitGroup
	t.Cleanup(func() { release(); wg.Wait() })
	done := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _, err := f.begin(ctx, "concurrent-plan")
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout='100ms'`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `UPDATE subscription_plans SET name='Concurrent replacement',included_points=1 WHERE id=$1`, f.plan)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("plan update bypassed checkout lock: %v", err)
	}
	_ = tx.Rollback(ctx)
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE subscription_plans SET name='Concurrent replacement',included_points=1 WHERE id=$1`, f.plan); err != nil {
		t.Fatal(err)
	}
	var acceptedName string
	var acceptedPoints int64
	if err := pool.QueryRow(ctx, `SELECT plan_name,included_points FROM subscription_checkout_contracts WHERE buyer_id=$1`, f.buyer).Scan(&acceptedName, &acceptedPoints); err != nil || acceptedName == "Concurrent replacement" || acceptedPoints == 1 {
		t.Fatalf("accepted snapshot split across plan update: %q %d err=%v", acceptedName, acceptedPoints, err)
	}
}

func TestSubscriptionAcceptedLongCheckoutKeysFulfill(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, length := range []int{121, 128} {
		f := newBillingDispatchFixture(t, pool, "stripe", "subscription")
		f.key = strings.Repeat("k", length)
		checkout, _, err := f.begin(t.Context(), "long-key")
		if err != nil {
			t.Fatal(err)
		}
		f.confirmSubscription(t, checkout, false)
		var key string
		if err := pool.QueryRow(t.Context(), `SELECT idempotency_key FROM user_subscriptions WHERE purchase_operation_id=$1`, checkout.PaymentID).Scan(&key); err != nil || key != f.key {
			t.Fatalf("accepted %d-byte key not fulfilled: key=%q err=%v", length, key, err)
		}
	}
}

func TestSubscriptionAcceptedModelScope(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	f := newBillingDispatchFixture(t, pool, "stripe", "subscription")
	ctx := t.Context()
	provider, original, added := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO provider_configs(id,name,protocol,endpoint,runtime_provider,admin_enabled,created_by,updated_by)
		VALUES($1,'Contract Provider','openai_responses','https://provider.test/v1','openai',true,$2,$2)`, provider, f.buyer); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO provider_config_models(id,provider_id,mode,model_name,display_name,description,estimated_cost_cents,admin_enabled,created_by,updated_by)
		VALUES($3,$1,'chat','contract-original','Original model','Accepted model.',1,true,$2,$2),
		($4,$1,'chat','contract-new','New model','Added later.',1,true,$2,$2)`, provider, f.buyer, original, added); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO provider_profiles(id,mode,provider,model_name,display_name,description,estimated_cost_cents,currency,local_test,admin_enabled)
		VALUES('contract-profile','chat','openai','profile-model','Profile model','Unbound legacy model.',1,'USD',false,true)`); err != nil {
		t.Fatal(err)
	}
	models := []uuid.UUID{original}
	if _, err := billing.NewService(pool).UpdateSubscriptionPlan(ctx, f.operator, f.plan, billing.SubscriptionPlanUpdate{ExpectedVersion: 1, ModelIDs: &models}, "contract-models-original"); err != nil {
		t.Fatal(err)
	}
	checkout, _, err := f.begin(ctx, "models")
	if err != nil {
		t.Fatal(err)
	}
	models = []uuid.UUID{added}
	if _, err := billing.NewService(pool).UpdateSubscriptionPlan(ctx, f.operator, f.plan, billing.SubscriptionPlanUpdate{ExpectedVersion: 2, ModelIDs: &models}, "contract-models-added"); err != nil {
		t.Fatal(err)
	}
	f.confirmSubscription(t, checkout, false)
	for _, test := range []struct {
		name   string
		model  *uuid.UUID
		legacy bool
		want   error
	}{
		{"original remains included", &original, false, nil},
		{"new model not granted", &added, false, billing.ErrModelNotIncluded},
		{"unbound profile denied", nil, false, billing.ErrModelNotIncluded},
		{"missing contract model denied", &added, true, billing.ErrModelNotIncluded},
		{"missing contract profile denied", nil, true, billing.ErrModelNotIncluded},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if test.legacy {
				// Simulate a historical subscription without inventing accepted terms.
				if _, err := tx.Exec(ctx, `ALTER TABLE subscription_checkout_contracts DISABLE TRIGGER subscription_checkout_contracts_immutable`); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(ctx, `DELETE FROM subscription_checkout_contracts WHERE payment_id=$1`, checkout.PaymentID); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(ctx, `ALTER TABLE subscription_checkout_contracts ENABLE TRIGGER subscription_checkout_contracts_immutable`); err != nil {
					t.Fatal(err)
				}
			}
			_, _, err = billing.ReserveGenerationPointsTx(ctx, tx, f.buyer, uuid.New(), test.model, "contract-profile", "chat", billing.MeterInput{PromptCharacters: 10})
			if !errors.Is(err, test.want) {
				t.Fatalf("model scope: got %v want %v", err, test.want)
			}
		})
	}
	// Later buyers accept the current plan, without changing the first contract.
	newBuyer := newBillingDispatchFixture(t, pool, "stripe", "subscription")
	newCheckout, _, err := newBuyer.begin(ctx, "new-models")
	if err != nil {
		t.Fatal(err)
	}
	var modelIDs []uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT model_ids FROM subscription_checkout_contracts WHERE payment_id=$1`, newCheckout.PaymentID).Scan(&modelIDs); err != nil || len(modelIDs) != 1 || modelIDs[0] != added {
		t.Fatalf("new checkout did not capture current models: %v err=%v", modelIDs, err)
	}
}

func TestSubscriptionContractImmutableAndMigrationGuard(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	f := newBillingDispatchFixture(t, pool, "stripe", "subscription")
	ctx := t.Context()
	down, err := os.ReadFile("../platform/database/migrations/0138_subscription_checkout_contracts.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../platform/database/migrations/0138_subscription_checkout_contracts.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payment_intents(id,provider,purpose,payer_id,resource_id,amount_cents,currency,status,live_mode,idempotency_key)
		VALUES($1,'stripe','subscription',$2,$3,4200,'USD','checkout_pending',false,'missing-contract')`, uuid.New(), f.buyer, f.plan); err == nil {
		t.Fatal("new writer committed subscription without original contract")
	}
	checkout, _, err := f.begin(ctx, "guard")
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE subscription_checkout_contracts SET included_points=1 WHERE payment_id=$1`,
		`DELETE FROM subscription_checkout_contracts WHERE payment_id=$1`,
		`UPDATE payment_intents SET amount_cents=amount_cents+1 WHERE id=$1`,
		`UPDATE payment_intents SET resource_id=payer_id WHERE id=$1`,
		`UPDATE payment_intents SET purpose='wallet_topup' WHERE id=$1`,
		`UPDATE payment_intents SET idempotency_key='changed-command' WHERE id=$1`,
	} {
		if _, err := pool.Exec(ctx, query, checkout.PaymentID); err == nil {
			t.Fatalf("contract changed: %s", query)
		}
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, string(down)); err == nil {
		t.Fatal("downgrade discarded accepted terms")
	}
}

func TestSubscriptionAcceptedTermsSurvivePlanChanges(t *testing.T) {
	for _, provider := range []string{"stripe", "waffo_pancake"} {
		for _, change := range []string{"price", "benefits", "inactive"} {
			t.Run(provider+"/"+change, func(t *testing.T) {
				pool, cleanup := paymentTestPool(t)
				defer cleanup()
				f := newBillingDispatchFixture(t, pool, provider, "subscription")
				var name string
				var points int64
				var days int
				if err := pool.QueryRow(t.Context(), `SELECT name,included_points,billing_period_days FROM subscription_plans WHERE id=$1`, f.plan).Scan(&name, &points, &days); err != nil {
					t.Fatal(err)
				}
				checkout, _, err := f.begin(t.Context(), "accepted")
				if err != nil {
					t.Fatal(err)
				}
				query := `UPDATE subscription_plans SET price_cents=price_cents+100 WHERE id=$1`
				if change == "benefits" {
					query = `UPDATE subscription_plans SET name='Changed terms',included_points=1,billing_period_days=1 WHERE id=$1`
				} else if change == "inactive" {
					query = `UPDATE subscription_plans SET active=false WHERE id=$1`
				}
				if _, err := pool.Exec(t.Context(), query, f.plan); err != nil {
					t.Fatal(err)
				}
				replay, created, err := f.begin(t.Context(), "after-plan-change")
				if err != nil || created || replay.PaymentID != checkout.PaymentID || replay.AmountCents != checkout.AmountCents || len(f.runtime.requests) != 1 {
					t.Fatalf("plan change replaced original checkout: %+v created=%v err=%v", replay, created, err)
				}
				f.confirmSubscription(t, checkout, false)
				overview, err := billing.NewService(pool).PointOverview(t.Context(), f.buyer)
				if err != nil || overview.CurrentSubscription == nil {
					t.Fatalf("subscription overview: %+v err=%v", overview, err)
				}
				sub := overview.CurrentSubscription
				if sub.PlanName != name || sub.GrantedPoints != points || sub.PriceCents != checkout.AmountCents || sub.CurrentPeriodEnd.Sub(sub.StartedAt) != time.Duration(days)*24*time.Hour {
					t.Fatalf("accepted terms changed: %+v; want name=%s points=%d days=%d", sub, name, points, days)
				}
				if provider == "waffo_pancake" {
					before := overview.Account.BalancePoints
					f.confirmSubscription(t, checkout, true)
					after, err := billing.NewService(pool).PointOverview(t.Context(), f.buyer)
					if err != nil || after.Account.BalancePoints != before+points || after.CurrentSubscription.CurrentPeriodEnd.Sub(sub.CurrentPeriodEnd) != time.Duration(days)*24*time.Hour {
						t.Fatalf("renewal changed accepted benefits: %+v err=%v", after, err)
					}
				}
			})
		}
	}
}

func (f *billingDispatchFixture) confirmSubscription(t *testing.T, checkout BillingCheckout, renewal bool) {
	t.Helper()
	eventType, paid := "checkout.session.completed", "paid"
	apiVersion, objectType := testStripeAPIVersion, "checkout.session"
	if f.runtime.Provider() == "waffo_pancake" {
		apiVersion, objectType = "waffo-pancake-v1", "order"
		eventType, paid = "subscription.activated", "succeeded"
		if renewal {
			eventType = "subscription.payment_succeeded"
		}
	}
	purpose, currency := "subscription", "USD"
	amount := int64(checkout.AmountCents)
	providerPayment := "pay_" + uuid.NewString()
	if f.runtime.Provider() == "stripe" {
		// Billing Stripe webhook binding requires the provider's authenticated
		// PaymentIntent identity. The fixture runtime returns a checkout session
		// whose ID is the durable local binding, so use a valid Stripe PaymentIntent
		// identifier here instead of the generic Waffo-style payment token.
		providerPayment = "pi_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	var providerCheckoutID string
	if err := f.pool.QueryRow(t.Context(), `SELECT provider_checkout_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&providerCheckoutID); err != nil {
		t.Fatal(err)
	}
	event := minimizedProviderEvent{ProviderEventID: "evt_" + uuid.NewString(), EventType: eventType,
		APIVersion: apiVersion, OccurredAt: time.Now().UTC(), PayloadSHA256: strings.Repeat("a", 64),
		ObjectID: providerCheckoutID, ObjectType: objectType,
		PaymentID: &checkout.PaymentID, ResourceID: &f.plan, Purpose: &purpose,
		AmountCents: &amount, Currency: &currency, PaymentStatus: &paid, ProviderPaymentID: &providerPayment, Supported: true}
	if f.runtime.Provider() == "waffo_pancake" {
		event.WaffoVerificationVersion, event.WaffoStoreID = waffoWebhookContractVersion, f.runtime.identity.StoreID
		event.WaffoOrderExternalID, event.WaffoBuyerIdentity = checkout.PaymentID.String(), f.buyer.String()
	} else {
		event.StripeVerificationVersion = stripeBillingWebhookVersion
	}
	if _, err := f.service.receiveProviderEvent(t.Context(), f.runtime.Provider(), event); err != nil {
		t.Fatal(err)
	}
	processPaymentEventJob(t, f.pool, f.service, "contract-worker")
}
