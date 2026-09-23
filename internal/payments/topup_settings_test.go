package payments

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestWalletTopupConfiguredMinimumAndOriginalFulfillment(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := t.Context()
	userID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Topup buyer','member')`, userID, userID.String()+"@test.local", "topup_"+userID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	runtime := &productCheckoutRuntime{}
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, Provider: "stripe", APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(runtime))
	checkout := func(amount int, key string) (BillingCheckout, bool, error) {
		return service.BeginWalletTopupCheckout(ctx, userID, amount, key, "settings-test", "https://app.example.test/workspace/billing?payment=success", "https://app.example.test/workspace/billing?payment=cancelled")
	}
	var before int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM billing_accounts WHERE user_id=$1 AND currency='USD'`, userID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	old, created, err := checkout(500, "topup-settings-original")
	if err != nil || !created || runtime.calls != 1 {
		t.Fatalf("original=%+v created=%v calls=%d err=%v", old, created, runtime.calls, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE wallet_topup_settings SET minimum_amount_cents=1000,preset_amounts_cents=ARRAY[1000,2000],version=version+1`); err != nil {
		t.Fatal(err)
	}
	replay, created, err := checkout(500, "topup-settings-original")
	if err != nil || created || !replay.AlreadyCreated || replay.PaymentID != old.PaymentID || runtime.calls != 1 {
		t.Fatalf("replay=%+v created=%v calls=%d err=%v", replay, created, runtime.calls, err)
	}
	if _, _, err := checkout(500, "topup-settings-too-small"); !errors.Is(err, billing.ErrTopupAmountOutOfRange) {
		t.Fatal(err)
	}
	if runtime.calls != 1 {
		t.Fatal("below-minimum request reached provider")
	}
	custom, created, err := checkout(1529, "topup-settings-custom")
	if err != nil || !created || custom.AmountCents != 1529 || runtime.calls != 2 {
		t.Fatalf("custom=%+v created=%v calls=%d err=%v", custom, created, runtime.calls, err)
	}
	for _, item := range []struct {
		id               uuid.UUID
		version, minimum int
	}{{old.PaymentID, 1, 50}, {custom.PaymentID, 2, 1000}} {
		var version, minimum int
		if err := pool.QueryRow(ctx, `SELECT (evidence->>'topupSettingsVersion')::int,(evidence->>'minimumTopupCents')::int FROM payment_intent_events WHERE payment_id=$1 AND event_type='checkout.requested'`, item.id).Scan(&version, &minimum); err != nil || version != item.version || minimum != item.minimum {
			t.Fatalf("snapshot=%d/%d err=%v", version, minimum, err)
		}
	}
	// Raising the minimum never changes an already accepted payment's credit.
	now := time.Now().UTC().Truncate(time.Second)
	service.verifier.now = func() time.Time { return now }
	body := billingStripeCheckoutEvent(t, pool, "evt_topup_old_settings", old.PaymentID, userID, "wallet_topup", 500, "pi_topup_old_settings", now.Unix())
	header := "t=" + fmt.Sprint(now.Unix()) + ",v1=" + stripeSignature(testStripeWebhookSecret, now.Unix(), body)
	if _, err := service.ReceiveStripeWebhook(ctx, body, header); err != nil {
		t.Fatal(err)
	}
	processPaymentEventJob(t, pool, service, "topup-settings-worker")
	if receipt, err := service.ReceiveStripeWebhook(ctx, body, header); err != nil || !receipt.Duplicate {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	var after int64
	var entries int
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM billing_accounts WHERE user_id=$1 AND currency='USD'`, userID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM billing_entries WHERE operation_id=$1 AND amount_cents=500`, old.PaymentID).Scan(&entries); err != nil || entries != 1 || after != before+500 {
		t.Fatalf("entries=%d wallet=%d/%d err=%v", entries, before, after, err)
	}
}

func TestWalletTopupConcurrentSettingsChangeBeforeDispatch(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	t.Cleanup(cleanup)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	userID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Topup buyer','member')`, userID, userID.String()+"@test.local", "topup_"+userID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	traced, entered, release := testutil.GateQuery(t, pool, "SELECT id,version,minimum_amount_cents,preset_amounts_cents,updated_at FROM wallet_topup_settings")
	runtime := &productCheckoutRuntime{}
	service := newPaymentTestService(t, traced, ServiceConfig{Enabled: true, Provider: "stripe"}, NewRuntimeCatalog(runtime))
	done := make(chan error, 1)
	var wg sync.WaitGroup
	t.Cleanup(func() { release(); wg.Wait() })
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _, err := service.BeginWalletTopupCheckout(ctx, userID, 500, "topup-settings-concurrent", "concurrent", "https://app.example.test/success", "https://app.example.test/cancel")
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("missing settings read: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := pool.Exec(ctx, `UPDATE wallet_topup_settings SET minimum_amount_cents=1000,version=version+1`); err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-done; !errors.Is(err, ErrCheckoutBusy) {
		t.Fatalf("expected retryable conflict: %v", err)
	}
	if runtime.calls != 0 {
		t.Fatal("conflicting checkout reached provider")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_intents WHERE payer_id=$1`, userID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("intents=%d err=%v", count, err)
	}
	if _, _, err := service.BeginWalletTopupCheckout(ctx, userID, 500, "topup-settings-concurrent", "retry", "https://app.example.test/success", "https://app.example.test/cancel"); !errors.Is(err, billing.ErrTopupAmountOutOfRange) {
		t.Fatalf("retry ignored new rule: %v", err)
	}
}

func TestWalletTopupSQLGuardRejectsOldWriters(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := t.Context()
	userID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'SQL buyer','member')`, userID, userID.String()+"@test.local", "topup_"+userID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE wallet_topup_settings SET minimum_amount_cents=1000,version=version+1`); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		amount   int
		currency string
	}{{999, "USD"}, {1000, "EUR"}, {100000000, "USD"}} {
		_, err := pool.Exec(ctx, `INSERT INTO payment_intents(id,provider,purpose,payer_id,resource_id,amount_cents,currency,status,live_mode,idempotency_key) VALUES($1,'stripe','wallet_topup',$2,$2,$3,$4,'checkout_pending',false,$5)`, uuid.New(), userID, item.amount, item.currency, uuid.NewString())
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "wallet_topup_minimum" {
			t.Fatalf("amount=%d currency=%s err=%v", item.amount, item.currency, err)
		}
	}
	for _, amounts := range []string{"ARRAY[1000,1000]", "ARRAY[999]", "ARRAY[NULL]::integer[]", "ARRAY[[1000,2000]]"} {
		if _, err := pool.Exec(ctx, `UPDATE wallet_topup_settings SET preset_amounts_cents=`+amounts); err == nil {
			t.Fatalf("accepted invalid presets %s", amounts)
		}
	}
}
