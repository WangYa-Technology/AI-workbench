package payments

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgconn"
)

type renewalSnapshot struct {
	Balance, Earned, Version         int64
	End                              time.Time
	Credits, Events, Audits, Notices int
}

func readRenewalSnapshot(t *testing.T, f *billingDispatchFixture, paymentID uuid.UUID) renewalSnapshot {
	t.Helper()
	var s renewalSnapshot
	err := f.pool.QueryRow(t.Context(), `SELECT a.balance_points,a.lifetime_earned_points,a.version,s.current_period_end,
	 (SELECT count(*) FROM point_entries WHERE user_id=$1 AND entry_type='subscription_credit'),
	 (SELECT count(*) FROM payment_intent_events WHERE payment_id=$2 AND event_type='subscription.renewed'),
	 (SELECT count(*) FROM audit_events WHERE actor_id=$1 AND action='billing.subscription_renewal'),
	 (SELECT count(*) FROM notifications WHERE user_id=$1 AND kind='billing.subscription_completed')
	 FROM point_accounts a JOIN user_subscriptions s ON s.user_id=a.user_id
	 WHERE a.user_id=$1 AND s.purchase_operation_id=$2`, f.buyer, paymentID).Scan(
		&s.Balance, &s.Earned, &s.Version, &s.End, &s.Credits, &s.Events, &s.Audits, &s.Notices)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func renewalEventJob(t *testing.T, f *billingDispatchFixture, checkout BillingCheckout, capture string, amount int) jobs.Job {
	t.Helper()
	purpose, currency, paid := "subscription", "USD", "succeeded"
	amount64 := int64(amount)
	event := minimizedProviderEvent{ProviderEventID: "evt_" + uuid.NewString(), EventType: "subscription.payment_succeeded",
		WaffoVerificationVersion: waffoWebhookContractVersion, WaffoStoreID: f.runtime.identity.StoreID,
		WaffoOrderExternalID: checkout.PaymentID.String(), WaffoBuyerIdentity: f.buyer.String(),
		APIVersion: "waffo-pancake-v1", OccurredAt: time.Now().UTC(), PayloadSHA256: strings.Repeat("a", 64),
		ObjectID: "cs_" + checkout.PaymentID.String(), ObjectType: "order", PaymentID: &checkout.PaymentID,
		ResourceID: &f.plan, Purpose: &purpose, AmountCents: &amount64, Currency: &currency,
		PaymentStatus: &paid, ProviderPaymentID: &capture, Supported: true}
	receipt, err := f.service.receiveProviderEvent(t.Context(), "waffo_pancake", event)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(paymentEventJobPayload{EventID: receipt.EventID})
	if err != nil {
		t.Fatal(err)
	}
	return jobs.Job{Kind: PaymentEventJobKind, Payload: body}
}

func TestWaffoSubscriptionRenewalCaptureConcurrency(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	f := newBillingDispatchFixture(t, pool, "waffo_pancake", "subscription")
	checkout, _, err := f.begin(t.Context(), "renewal-concurrency")
	if err != nil {
		t.Fatal(err)
	}
	f.confirmSubscription(t, checkout, false)
	before := readRenewalSnapshot(t, f, checkout.PaymentID)
	var points int64
	var days int
	if err := pool.QueryRow(t.Context(), `SELECT included_points,billing_period_days FROM subscription_checkout_contracts WHERE payment_id=$1`, checkout.PaymentID).Scan(&points, &days); err != nil {
		t.Fatal(err)
	}
	work := make([]jobs.Job, 6)
	for i := range work {
		work[i] = renewalEventJob(t, f, checkout, "capture-concurrent", checkout.AmountCents)
	}
	start := make(chan struct{})
	errs := make(chan error, len(work))
	var wg sync.WaitGroup
	for _, job := range work {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			var err error
			for attempt := 0; attempt < 10; attempt++ {
				err = f.service.HandlePaymentEventJob(t.Context(), job)
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || pgErr.Code != "40001" {
					break
				}
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	after := readRenewalSnapshot(t, f, checkout.PaymentID)
	// An incomplete legacy credit is not a successful duplicate and must not be
	// repaired by granting another full period without financial reconciliation.
	partial, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = partial.Rollback(t.Context()) }()
	if _, err := partial.Exec(t.Context(), `DELETE FROM point_entries WHERE user_id=$1 AND metadata->>'providerPaymentId'='capture-concurrent'`, f.buyer); err != nil {
		t.Fatal(err)
	}
	if err := fulfillOrRenewWaffoSubscriptionTx(t.Context(), partial, uuid.New(), checkout.PaymentID, checkout.AmountCents, "USD", "capture-concurrent", time.Now()); !errors.Is(err, ErrCheckoutReconciliation) {
		t.Fatalf("incomplete historical credit not held for reconciliation: %v", err)
	}
	_ = partial.Rollback(t.Context())
	if after.Balance != before.Balance+points || after.Earned != before.Earned+points || after.Version != before.Version+1 ||
		after.Credits != before.Credits+1 || after.Events != before.Events+1 || after.Audits != before.Audits+1 || after.Notices != before.Notices+1 ||
		!after.End.Equal(before.End.AddDate(0, 0, days)) {
		t.Fatalf("concurrent deliveries changed more than one period: before=%+v after=%+v", before, after)
	}
	// Invalid repeats are rejected before the successful-capture shortcut.
	invalidRepeat, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer invalidRepeat.Rollback(t.Context())
	if err := fulfillOrRenewWaffoSubscriptionTx(t.Context(), invalidRepeat, uuid.New(), checkout.PaymentID, checkout.AmountCents+1, "USD", "capture-concurrent", time.Now()); err == nil {
		t.Fatal("changed amount accepted")
	}
	_ = invalidRepeat.Rollback(t.Context())
	if got := readRenewalSnapshot(t, f, checkout.PaymentID); got != after {
		t.Fatalf("invalid repeat mutated state: %+v", got)
	}
	next := renewalEventJob(t, f, checkout, "capture-next", checkout.AmountCents)
	if err := f.service.HandlePaymentEventJob(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	last := readRenewalSnapshot(t, f, checkout.PaymentID)
	if last.Balance != after.Balance+points || last.Events != after.Events+1 || !last.End.Equal(after.End.AddDate(0, 0, days)) {
		t.Fatalf("next capture not fulfilled: %+v", last)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE user_subscriptions SET status='cancelled',cancelled_at=now() WHERE purchase_operation_id=$1`, checkout.PaymentID); err != nil {
		t.Fatal(err)
	}
	late := renewalEventJob(t, f, checkout, "capture-concurrent", checkout.AmountCents)
	if err := f.service.HandlePaymentEventJob(t.Context(), late); err != nil {
		t.Fatalf("known capture redelivery after cancellation: %v", err)
	}
	unexpected := renewalEventJob(t, f, checkout, "capture-after-cancellation", checkout.AmountCents)
	if err := f.service.HandlePaymentEventJob(t.Context(), unexpected); err == nil {
		t.Fatal("new capture silently reactivated cancelled subscription")
	}
	if got := readRenewalSnapshot(t, f, checkout.PaymentID); got != last {
		t.Fatalf("late delivery changed cancelled subscription benefits: %+v", got)
	}
}

func TestWaffoSubscriptionRenewalRollbackAndMigration(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := t.Context()
	f := newBillingDispatchFixture(t, pool, "waffo_pancake", "subscription")
	down, err := os.ReadFile("../platform/database/migrations/0139_subscription_renewal_identity.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../platform/database/migrations/0139_subscription_renewal_identity.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	checkout, _, err := f.begin(ctx, "renewal-rollback")
	if err != nil {
		t.Fatal(err)
	}
	f.confirmSubscription(t, checkout, false)
	before := readRenewalSnapshot(t, f, checkout.PaymentID)
	job := renewalEventJob(t, f, checkout, "capture-rollback", checkout.AmountCents)
	if _, err = pool.Exec(ctx, `CREATE FUNCTION reject_test_renewal_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
	 IF NEW.action='billing.subscription_renewal' THEN RAISE EXCEPTION 'injected audit failure'; END IF; RETURN NEW; END $$;
	 CREATE TRIGGER reject_test_renewal_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_test_renewal_audit()`); err != nil {
		t.Fatal(err)
	}
	if err = f.service.HandlePaymentEventJob(ctx, job); err == nil {
		t.Fatal("audit failure ignored")
	}
	if got := readRenewalSnapshot(t, f, checkout.PaymentID); got != before {
		t.Fatalf("failed renewal committed state: %+v", got)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER reject_test_renewal_audit ON audit_events; DROP FUNCTION reject_test_renewal_audit()`); err != nil {
		t.Fatal(err)
	}
	if err = f.service.HandlePaymentEventJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	after := readRenewalSnapshot(t, f, checkout.PaymentID)
	// A legacy worker increments the balance before writing its duplicate ledger
	// entry; the database guard must roll that earlier write back too.
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `UPDATE point_accounts SET balance_points=balance_points+1 WHERE user_id=$1`, f.buyer); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO point_entries(user_id,operation_id,entry_type,direction,amount_points,balance_after_points,description,metadata)
	 SELECT user_id,$2,entry_type,direction,amount_points,balance_after_points,description,metadata FROM point_entries
	 WHERE user_id=$1 AND metadata->>'providerPaymentId'='capture-rollback'`, f.buyer, uuid.New())
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate capture guard: %v", err)
	}
	_ = tx.Rollback(ctx)
	if got := readRenewalSnapshot(t, f, checkout.PaymentID); got != after {
		t.Fatalf("old writer partially committed: %+v", got)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, string(down)); err == nil {
		t.Fatal("downgrade removed renewal guard")
	}
	_ = tx.Rollback(ctx)
	// Historical double credits require reconciliation, never silent deletion.
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `DROP INDEX point_entries_waffo_subscription_capture; DROP INDEX payment_intent_events_waffo_subscription_capture`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO point_entries(user_id,operation_id,entry_type,direction,amount_points,balance_after_points,description,metadata)
	 SELECT user_id,$2,entry_type,direction,amount_points,balance_after_points,description,metadata FROM point_entries
	 WHERE user_id=$1 AND metadata->>'providerPaymentId'='capture-rollback'`, f.buyer, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(up)); err == nil {
		t.Fatal("migration ignored historical double credit")
	}
}
