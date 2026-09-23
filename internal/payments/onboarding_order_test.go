package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func payoutAccountReceipt(t *testing.T, service *Service, user uuid.UUID, destination string, occurredAt time.Time, charges, payouts, details, due bool) Receipt {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(stripeAccountEvent("evt_"+fmt.Sprintf("%x", uuid.New()), occurredAt.Unix(), user, charges, payouts, details, due), &body); err != nil {
		t.Fatal(err)
	}
	body["data"].(map[string]any)["object"].(map[string]any)["id"] = destination
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return receivePaymentWorkflowEvent(t, service, encoded, time.Now().UTC())
}

func payoutOrderingOwner(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, string) {
	t.Helper()
	user := uuid.New()
	destination := "acct_" + fmt.Sprintf("%x", user)
	if _, err := pool.Exec(context.Background(), `INSERT INTO users(id,email,handle,display_name,role)
	 VALUES($1,$2,$3,'Payout ordering owner','creator')`, user, user.String()+"@example.test", "po_"+user.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO payment_destinations(provider,user_id,destination_id,account_type,status,requirements_due)
	 VALUES('stripe',$1,$2,'express','pending_onboarding',true)`, user, destination); err != nil {
		t.Fatal(err)
	}
	// This fixture represents a destination created by the current onboarding
	// flow. Historical/manual destinations intentionally remain unbound and are
	// rejected by destinationIdentityMatchesTx in production.
	if _, err := pool.Exec(context.Background(), `UPDATE payment_destinations
	 SET original_merchant_id='acct_workflow',original_live_mode=false,
	     original_endpoint='https://api.stripe.com/v1',original_api_version='2026-02-25.clover',
	     original_request_version=$2
	 WHERE provider='stripe' AND user_id=$1 AND destination_id=$3`, user, stripeProductCheckoutVersion, destination); err != nil {
		t.Fatal(err)
	}
	return user, destination
}

func TestPayoutAccountEventsRespectProviderOrder(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	service := NewServiceWithRuntimes(pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion,
		WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(&productCheckoutRuntime{}))
	ctx := context.Background()
	for _, scenario := range []struct {
		name       string
		tied       bool
		restricted bool
		order      []int
	}{
		{"older_approval_arrives_last", false, true, []int{1, 0}},
		{"newer_restriction_arrives_last", false, true, []int{0, 1}},
		{"older_restriction_arrives_last", false, false, []int{1, 0}},
		{"same_second_approval_arrives_last", true, true, []int{1, 0, 2}},
		{"same_second_restriction_arrives_last", true, true, []int{0, 2, 1}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			user, destination := payoutOrderingOwner(t, pool)
			base := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
			newer := base.Add(time.Second)
			if scenario.tied {
				newer = base
			}
			first := payoutAccountReceipt(t, service, user, destination, base, true, scenario.restricted, true, !scenario.restricted)
			second := payoutAccountReceipt(t, service, user, destination, newer, true, !scenario.restricted, true, scenario.restricted)
			third := payoutAccountReceipt(t, service, user, destination, newer, true, true, true, false)
			receipts := []Receipt{first, second, third}
			for _, index := range scenario.order {
				processStripeReceipt(t, service, pool, receipts[index])
			}
			status, err := service.GetPayoutStatus(ctx, user)
			want := "verified"
			if scenario.restricted {
				want = "restricted"
			}
			if err != nil || status.Status != want || status.PayoutsEnabled == scenario.restricted || status.RequirementsDue != scenario.restricted {
				t.Fatalf("out-of-order capabilities: status=%+v want=%s err=%v", status, want, err)
			}
			var processing string
			if err := pool.QueryRow(ctx, `SELECT status FROM payment_provider_event_processing WHERE event_id=$1`, first.EventID).Scan(&processing); err != nil {
				t.Fatal(err)
			}
			if !scenario.tied && scenario.order[0] == 1 && processing != "ignored" {
				t.Fatalf("stale event was not ignored: %s", processing)
			}
			for _, index := range scenario.order {
				processStripeReceipt(t, service, pool, receipts[index])
			}
			replayed, err := service.GetPayoutStatus(ctx, user)
			if err != nil || replayed.Version != status.Version || replayed.Status != status.Status {
				t.Fatalf("replay changed state: %+v err=%v", replayed, err)
			}
			// A strictly newer provider decision can restore eligibility.
			fresh := payoutAccountReceipt(t, service, user, destination, newer.Add(time.Second), true, true, true, false)
			processStripeReceipt(t, service, pool, fresh)
			restored, err := service.GetPayoutStatus(ctx, user)
			if err != nil || restored.Status != "verified" || !restored.PayoutsEnabled || restored.RequirementsDue {
				t.Fatalf("new provider approval did not restore state: %+v err=%v", restored, err)
			}
		})
	}
}

func TestProductSettlementRejectsStalePayoutApproval(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	runtime := &productSettlementRuntime{}
	service, _, job, settlement := productSettlementFixture(t, pool, runtime)
	var seller uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT seller_id FROM product_settlements WHERE id=$1`, settlement).Scan(&seller); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	approval := payoutAccountReceipt(t, service, seller, "acct_settlement", base, true, true, true, false)
	restriction := payoutAccountReceipt(t, service, seller, "acct_settlement", base.Add(time.Second), true, false, true, true)
	processStripeReceipt(t, service, pool, restriction)
	processStripeReceipt(t, service, pool, approval)
	if err := service.HandleProductSettlementJob(ctx, job); err == nil {
		t.Fatal("stale approval authorized settlement")
	}
	var batches int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_payout_batch_items WHERE settlement_id=$1`, settlement).Scan(&batches); err != nil {
		t.Fatal(err)
	}
	if runtime.transfers.Load() != 0 || batches != 0 {
		t.Fatalf("restricted destination dispatched: transfers=%d batches=%d", runtime.transfers.Load(), batches)
	}
	fresh := payoutAccountReceipt(t, service, seller, "acct_settlement", base.Add(2*time.Second), true, true, true, false)
	processStripeReceipt(t, service, pool, fresh)
	if err := service.HandleProductSettlementJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if runtime.transfers.Load() != 1 {
		t.Fatalf("new approval did not allow one settlement: %d", runtime.transfers.Load())
	}
}

func TestPayoutAccountEventsMergeCapabilitiesAndRespectAdminDisable(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	service := NewServiceWithRuntimes(pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion,
		WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(&productCheckoutRuntime{}))
	user, destination := payoutOrderingOwner(t, pool)
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	for _, evidence := range [][4]bool{
		{false, true, true, false},
		{true, false, true, false},
		{true, true, false, false},
		{true, true, true, true},
		{true, true, true, false},
	} {
		receipt := payoutAccountReceipt(t, service, user, destination, base, evidence[0], evidence[1], evidence[2], evidence[3])
		processStripeReceipt(t, service, pool, receipt)
	}
	status, err := service.GetPayoutStatus(ctx, user)
	if err != nil || status.ChargesEnabled || status.PayoutsEnabled || status.DetailsSubmitted || !status.RequirementsDue || status.VerifiedAt != nil {
		t.Fatalf("conflicting capability evidence lost: %+v err=%v", status, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_destinations SET admin_disabled=true,status='disabled',verified_at=NULL WHERE user_id=$1`, user); err != nil {
		t.Fatal(err)
	}
	fresh := payoutAccountReceipt(t, service, user, destination, base.Add(time.Second), true, true, true, false)
	processStripeReceipt(t, service, pool, fresh)
	status, err = service.GetPayoutStatus(ctx, user)
	if err != nil || status.Status != "disabled" || status.VerifiedAt != nil {
		t.Fatalf("provider event overrode admin disable: %+v err=%v", status, err)
	}
	var merged int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events a JOIN payment_destinations d ON d.id=a.resource_id
	 WHERE d.user_id=$1 AND a.action='payment.payout_destination_synced' AND a.metadata->>'mergedSameSecond'='true'`, user).Scan(&merged); err != nil || merged != 4 {
		t.Fatalf("missing conflict audit: merged=%d err=%v", merged, err)
	}
}

func TestPayoutAccountEventsSerializeConcurrentUpdates(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	service := NewServiceWithRuntimes(pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion,
		WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(&productCheckoutRuntime{}))
	for _, tied := range []bool{false, true} {
		t.Run(fmt.Sprintf("same_second_%t", tied), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			user, destination := payoutOrderingOwner(t, pool)
			base := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
			newer := base.Add(time.Second)
			if tied {
				newer = base
			}
			approval := payoutAccountReceipt(t, service, user, destination, base, true, true, true, false)
			restriction := payoutAccountReceipt(t, service, user, destination, newer, true, false, true, true)
			gate, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Rollback(context.Background())
			if _, err := gate.Exec(ctx, `SELECT id FROM payment_destinations WHERE user_id=$1 FOR UPDATE`, user); err != nil {
				t.Fatal(err)
			}
			first, second := payoutEventJob(restriction.EventID), payoutEventJob(approval.EventID)
			firstDone, secondDone := make(chan error, 1), make(chan error, 1)
			go func() { firstDone <- service.HandlePaymentEventJob(ctx, first) }()
			firstPID := waitForProductBlockingTx(t, ctx, pool, int32(gate.Conn().PgConn().PID()))
			go func() { secondDone <- service.HandlePaymentEventJob(ctx, second) }()
			waitForProductBlockingTx(t, ctx, pool, firstPID)
			if err := gate.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-firstDone; err != nil {
				t.Fatal(err)
			}
			var conflict *pgconn.PgError
			err = <-secondDone
			if !errors.As(err, &conflict) || conflict.Code != "40001" || !jobs.ShouldRetry(err) {
				t.Fatalf("expected worker-retryable serialization conflict: %v", err)
			}
			if err := service.HandlePaymentEventJob(ctx, second); err != nil {
				t.Fatal(err)
			}
			status, err := service.GetPayoutStatus(ctx, user)
			if err != nil || status.Status != "restricted" || status.PayoutsEnabled || !status.RequirementsDue {
				t.Fatalf("concurrent approval undid restriction: %+v err=%v", status, err)
			}
		})
	}
}

func payoutEventJob(eventID uuid.UUID) jobs.Job {
	return jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, eventID.String()))}
}
