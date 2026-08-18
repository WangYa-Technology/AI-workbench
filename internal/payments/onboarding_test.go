package payments

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

type onboardingRuntime struct {
	accountCalls int
	linkCalls    int
}

func (*onboardingRuntime) Provider() string { return "stripe" }
func (*onboardingRuntime) CreateCheckout(context.Context, CheckoutRequest) (CheckoutSession, error) {
	return CheckoutSession{}, ErrProviderUnavailable
}
func (*onboardingRuntime) CreateRefund(context.Context, RefundRequest) (Refund, error) {
	return Refund{}, ErrProviderUnavailable
}
func (*onboardingRuntime) CreateTransfer(context.Context, TransferRequest) (Transfer, error) {
	return Transfer{}, ErrProviderUnavailable
}
func (r *onboardingRuntime) CreateConnectAccount(_ context.Context, input ConnectAccountRequest) (ConnectAccount, error) {
	r.accountCalls++
	return ConnectAccount{ID: "acct_owner_contract", RequirementsDue: true}, nil
}
func (r *onboardingRuntime) CreateAccountLink(_ context.Context, input AccountLinkRequest) (AccountLink, error) {
	r.linkCalls++
	return AccountLink{URL: fmt.Sprintf("https://connect.stripe.com/setup/c/owner-%d", r.linkCalls), ExpiresAt: time.Now().Add(5 * time.Minute).UTC()}, nil
}

func TestPayoutOnboardingIsOwnerScopedAndProviderHosted(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID, outsiderID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role) VALUES
		($1,'payout-owner@example.test',$2,'Payout Owner','creator'),
		($3,'payout-outsider@example.test',$4,'Payout Outsider','creator')`,
		ownerID, "payout_"+ownerID.String()[:8], outsiderID, "payout_"+outsiderID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	disabled := NewService(pool, ServiceConfig{})
	status, err := disabled.GetPayoutStatus(ctx, ownerID)
	if err != nil || status.ProviderAvailable || status.Status != "not_started" || status.CanStartOnboarding {
		t.Fatalf("disabled payout projection mismatch: %#v err=%v", status, err)
	}
	if _, err := disabled.BeginPayoutOnboarding(ctx, ownerID, "https://app.example.test/settings", "https://app.example.test/settings", "disabled-request"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled payout boundary accepted onboarding: %v", err)
	}

	runtime := &onboardingRuntime{}
	service := NewServiceWithRuntimes(pool, ServiceConfig{Enabled: true, LiveMode: false}, NewRuntimeCatalog(runtime))
	link, err := service.BeginPayoutOnboarding(ctx, ownerID,
		"https://app.example.test/settings?section=payouts&connect=refresh",
		"https://app.example.test/settings?section=payouts&connect=return", "payout-request-1")
	if err != nil || runtime.accountCalls != 1 || runtime.linkCalls != 1 || link.Status.Status != "pending_onboarding" || link.Status.DestinationID == nil || *link.Status.DestinationID != "acct_owner_contract" {
		t.Fatalf("first onboarding mismatch: link=%#v accountCalls=%d linkCalls=%d err=%v", link, runtime.accountCalls, runtime.linkCalls, err)
	}
	replayed, err := service.BeginPayoutOnboarding(ctx, ownerID,
		"https://app.example.test/settings?section=payouts&connect=refresh",
		"https://app.example.test/settings?section=payouts&connect=return", "payout-request-2")
	if err != nil || runtime.accountCalls != 1 || runtime.linkCalls != 2 || replayed.URL == link.URL {
		t.Fatalf("single-use link renewal mismatch: replayed=%#v accountCalls=%d linkCalls=%d err=%v", replayed, runtime.accountCalls, runtime.linkCalls, err)
	}
	outsider, err := service.GetPayoutStatus(ctx, outsiderID)
	if err != nil || outsider.Status != "not_started" || outsider.DestinationID != nil {
		t.Fatalf("cross-account payout evidence leaked: %#v err=%v", outsider, err)
	}
	var auditCount, destinationCount int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM audit_events WHERE actor_id=$1 AND resource_type='payment_destination'`, ownerID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM payment_destinations WHERE user_id=$1`, ownerID).Scan(&destinationCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 3 || destinationCount != 1 {
		t.Fatalf("payout evidence mismatch: audits=%d destinations=%d", auditCount, destinationCount)
	}
}

func TestSignedAccountUpdatedSynchronizesPayoutCapabilities(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,'connect-owner@example.test',$2,'Connect Owner','creator')`, ownerID, "connect_"+ownerID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_destinations(provider,user_id,destination_id,account_type,status,requirements_due)
		VALUES('stripe',$1,'acct_webhook_contract','express','pending_onboarding',true)`, ownerID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	service := NewService(pool, ServiceConfig{Enabled: true, LiveMode: false, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute})
	service.verifier.now = func() time.Time { return now }
	body := stripeAccountEvent("evt_account_contract", now.Unix(), ownerID, true, true, true, false)
	receipt, err := service.ReceiveStripeWebhook(ctx, body, "t="+fmt.Sprint(now.Unix())+",v1="+stripeSignature(testStripeWebhookSecret, now.Unix(), body))
	if err != nil || receipt.EventType != "account.updated" || receipt.Status != "received" {
		t.Fatalf("account event receipt mismatch: %#v err=%v", receipt, err)
	}
	if err := service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}); err != nil {
		t.Fatal(err)
	}
	status, err := service.GetPayoutStatus(ctx, ownerID)
	if err != nil || status.Status != "verified" || !status.ChargesEnabled || !status.PayoutsEnabled || !status.DetailsSubmitted || status.RequirementsDue || status.VerifiedAt == nil {
		t.Fatalf("verified payout state mismatch: %#v err=%v", status, err)
	}
	var unsafeRequirementColumns, minimizedEvents int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)::int FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='payment_provider_events'
		AND column_name IN ('requirements','currently_due','past_due','disabled_reason','identity_document','bank_account')`).Scan(&unsafeRequirementColumns); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*)::int FROM payment_provider_events WHERE id=$1 AND destination_user_id=$2 AND destination_id='acct_webhook_contract'
		AND account_charges_enabled AND account_payouts_enabled AND account_details_submitted AND NOT account_requirements_due`, receipt.EventID, ownerID).Scan(&minimizedEvents); err != nil {
		t.Fatal(err)
	}
	if unsafeRequirementColumns != 0 || minimizedEvents != 1 {
		t.Fatalf("Connect minimization mismatch: unsafeColumns=%d minimizedEvents=%d", unsafeRequirementColumns, minimizedEvents)
	}
}

func stripeAccountEvent(eventID string, created int64, userID uuid.UUID, chargesEnabled, payoutsEnabled, detailsSubmitted, requirementsDue bool) []byte {
	currentlyDue := `[]`
	if requirementsDue {
		currentlyDue = `["individual.verification.document"]`
	}
	return []byte(fmt.Sprintf(`{"id":%q,"object":"event","api_version":%q,"created":%d,"livemode":false,"type":"account.updated","data":{"object":{"id":"acct_webhook_contract","object":"account","charges_enabled":%t,"payouts_enabled":%t,"details_submitted":%t,"metadata":{"hcai_user_id":%q},"requirements":{"currently_due":%s,"past_due":[],"pending_verification":[]}}}}`, eventID, testStripeAPIVersion, created, chargesEnabled, payoutsEnabled, detailsSubmitted, userID.String(), currentlyDue))
}
