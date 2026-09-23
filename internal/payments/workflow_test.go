package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type productCheckoutRuntime struct {
	calls       int
	refundCalls int
}

type waffoBillingCheckoutRuntime struct {
	calls          int
	lastSuccessURL string
}

func (*waffoBillingCheckoutRuntime) Provider() string { return "waffo_pancake" }
func (*waffoBillingCheckoutRuntime) ProductCheckoutIdentity(context.Context) (ProductCheckoutIdentity, error) {
	return ProductCheckoutIdentity{Provider: "waffo_pancake", MerchantID: "MER_test", StoreID: "STO_test", Endpoint: "https://connector.example.test", APIVersion: waffoProductCheckoutAPI, RequestVersion: waffoProductCheckoutVersion}, nil
}
func (r *waffoBillingCheckoutRuntime) CreateCheckout(_ context.Context, input CheckoutRequest) (CheckoutSession, error) {
	r.calls++
	r.lastSuccessURL = input.SuccessURL
	return CheckoutSession{
		ProviderID: "CHK_waffo_billing_123", CheckoutURL: "https://pancake.waffo.ai/store/test/checkout/CHK_waffo_billing_123", Status: "open",
		PaymentStatus: "pending", ExpiresAt: time.Now().Add(time.Hour).UTC(), LiveMode: false,
	}, nil
}
func (*waffoBillingCheckoutRuntime) CreateRefund(context.Context, RefundRequest) (Refund, error) {
	return Refund{}, ErrProviderUnavailable
}
func (*waffoBillingCheckoutRuntime) CreateTransfer(context.Context, TransferRequest) (Transfer, error) {
	return Transfer{}, ErrProviderUnavailable
}
func (*waffoBillingCheckoutRuntime) CreateConnectAccount(context.Context, ConnectAccountRequest) (ConnectAccount, error) {
	return ConnectAccount{}, ErrProviderUnavailable
}
func (*waffoBillingCheckoutRuntime) CreateAccountLink(context.Context, AccountLinkRequest) (AccountLink, error) {
	return AccountLink{}, ErrProviderUnavailable
}

func (*productCheckoutRuntime) ProductCheckoutIdentity(context.Context) (ProductCheckoutIdentity, error) {
	return ProductCheckoutIdentity{Provider: "stripe", MerchantID: "acct_workflow", Endpoint: "https://api.stripe.com/v1", APIVersion: "2026-02-25.clover", RequestVersion: stripeProductCheckoutVersion}, nil
}

func (*productCheckoutRuntime) Provider() string { return "stripe" }
func (r *productCheckoutRuntime) CreateCheckout(_ context.Context, input CheckoutRequest) (CheckoutSession, error) {
	r.calls++
	providerID := "cs_workflow123"
	checkoutURL := "https://checkout.stripe.com/c/pay/workflow123"
	if r.calls > 1 {
		providerID = fmt.Sprintf("cs_workflow%03d", r.calls)
		checkoutURL = "https://checkout.stripe.com/c/pay/" + providerID
	}
	return CheckoutSession{
		ProviderID: providerID, CheckoutURL: checkoutURL, Status: "open",
		PaymentStatus: "unpaid", ExpiresAt: time.Now().Add(time.Hour).UTC(), LiveMode: false,
	}, nil
}
func (r *productCheckoutRuntime) CreateRefund(_ context.Context, input RefundRequest) (Refund, error) {
	r.refundCalls++
	providerID := "re_workflow123"
	if r.refundCalls > 1 {
		providerID = "re_workflow456"
	}
	return Refund{
		ProviderID: providerID, ProviderPaymentID: input.ProviderPaymentID, AmountCents: input.AmountCents,
		Currency: "USD", Status: "pending",
	}, nil
}
func (*productCheckoutRuntime) CreateTransfer(context.Context, TransferRequest) (Transfer, error) {
	return Transfer{}, ErrProviderUnavailable
}
func (*productCheckoutRuntime) CreateConnectAccount(context.Context, ConnectAccountRequest) (ConnectAccount, error) {
	return ConnectAccount{}, ErrProviderUnavailable
}
func (*productCheckoutRuntime) CreateAccountLink(context.Context, AccountLinkRequest) (AccountLink, error) {
	return AccountLink{}, ErrProviderUnavailable
}

func TestProductCheckoutSignedFulfillmentWorkflow(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	buyerID, sellerID, sourceAssetID, productID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role) VALUES
		  ($1,$2,$3,'Payment Buyer','member'),($4,$5,$6,'Payment Seller','creator')`,
		buyerID, buyerID.String()+"@test.local", "buyer_"+buyerID.String()[:8], sellerID, sellerID.String()+"@test.local", "seller_"+sellerID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
		VALUES($1,$2,'image','Provider source Asset',$3,'image/jpeg','clean','upload','hcai-commercial-standard-v1','local_file',$1::uuid::text||'.jpg')`,
		sourceAssetID, sellerID, "/api/v1/assets/"+sourceAssetID.String()+"/content"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status,ai_disclosure,included_files,compatibility)
		VALUES($1,$2,$3,'Provider workflow','Signed fulfillment contract.','workflow',1900,'USD','hcai-commercial-standard-v1','active','AI-assisted.','[]','HCAI CHAT')`,
		productID, sellerID, sourceAssetID); err != nil {
		t.Fatal(err)
	}
	replacePaymentFixtureBytes(t, pool, sourceAssetID, []byte("payment workflow source"))
	runtime := &productCheckoutRuntime{}
	config := ServiceConfig{Enabled: true, LiveMode: false, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}
	service := newPaymentTestService(t, pool, config, NewRuntimeCatalog(runtime))
	checkout, created, err := service.BeginProductCheckout(ctx, buyerID, productID, "product-workflow-001", "checkout-request", "https://app.example.test/workspace/orders?payment=success", "https://app.example.test/market/products/one?payment=cancelled", true, productOfferVersion(t, pool, productID))
	if err != nil || !created || checkout.Status != "checkout_open" || checkout.OrderID == uuid.Nil || checkout.PaymentID == uuid.Nil || runtime.calls != 1 {
		t.Fatalf("create product checkout: checkout=%#v created=%t calls=%d err=%v", checkout, created, runtime.calls, err)
	}
	replayed, created, err := service.BeginProductCheckout(ctx, buyerID, productID, "product-workflow-001", "checkout-replay", "https://app.example.test/workspace/orders?payment=success", "https://app.example.test/market/products/one?payment=cancelled", true, productOfferVersion(t, pool, productID))
	if err != nil || created || !replayed.AlreadyCreated || replayed.PaymentID != checkout.PaymentID || runtime.calls != 1 {
		t.Fatalf("idempotent checkout mismatch: checkout=%#v created=%t calls=%d err=%v", replayed, created, runtime.calls, err)
	}
	var orderStatus string
	var entitlementCount int
	if err := pool.QueryRow(ctx, `SELECT status FROM orders WHERE id=$1`, checkout.OrderID).Scan(&orderStatus); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entitlements WHERE order_id=$1`, checkout.OrderID).Scan(&entitlementCount); err != nil {
		t.Fatal(err)
	}
	if orderStatus != "payment_pending" || entitlementCount != 0 {
		t.Fatalf("checkout granted fulfillment before signed event: status=%s entitlements=%d", orderStatus, entitlementCount)
	}

	now := time.Now().UTC().Truncate(time.Second)
	service.verifier.now = func() time.Time { return now }
	body := productPaidEvent(checkout.PaymentID, productID, now.Unix(), checkout.AmountCents)
	header := "t=" + fmt.Sprint(now.Unix()) + ",v1=" + stripeSignature(testStripeWebhookSecret, now.Unix(), body)
	receipt, err := service.ReceiveStripeWebhook(ctx, body, header)
	if err != nil || receipt.Status != "received" {
		t.Fatalf("receive signed product payment: receipt=%#v err=%v", receipt, err)
	}
	repository := jobs.NewRepository(pool)
	job, err := repository.Claim(ctx, "payment-workflow-worker", time.Minute)
	if err != nil || job.Kind != PaymentEventJobKind {
		t.Fatalf("claim payment event job: job=%#v err=%v", job, err)
	}
	if err := service.HandlePaymentEventJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(ctx, job, "payment-workflow-worker"); err != nil {
		t.Fatal(err)
	}

	var intentStatus, processingStatus string
	var ownedAssetCount, localBillingCount, localLedgerCount int
	if err := pool.QueryRow(ctx, `SELECT status FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&intentStatus); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM payment_provider_event_processing WHERE event_id=$1`, receipt.EventID).Scan(&processingStatus); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM orders WHERE id=$1`, checkout.OrderID).Scan(&orderStatus); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entitlements WHERE order_id=$1 AND user_id=$2 AND status='active'`, checkout.OrderID, buyerID).Scan(&entitlementCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE owner_id=$1 AND source_type='purchase' AND source_id=$2`, buyerID, checkout.OrderID).Scan(&ownedAssetCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM billing_entries WHERE operation_id=$1`, checkout.OrderID).Scan(&localBillingCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE operation_id=$1`, checkout.OrderID).Scan(&localLedgerCount); err != nil {
		t.Fatal(err)
	}
	if intentStatus != "paid" || processingStatus != "processed" || orderStatus != "fulfilled" || entitlementCount != 1 || ownedAssetCount != 1 || localBillingCount != 0 || localLedgerCount != 0 {
		t.Fatalf("signed fulfillment mismatch: intent=%s processing=%s order=%s entitlement=%d asset=%d billing=%d ledger=%d", intentStatus, processingStatus, orderStatus, entitlementCount, ownedAssetCount, localBillingCount, localLedgerCount)
	}
	duplicate, err := service.ReceiveStripeWebhook(ctx, body, header)
	if err != nil || !duplicate.Duplicate {
		t.Fatalf("duplicate paid event was not acknowledged: %#v err=%v", duplicate, err)
	}
	var eventJobs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind=$1 AND payload->>'eventId'=$2`, PaymentEventJobKind, receipt.EventID.String()).Scan(&eventJobs); err != nil || eventJobs != 1 {
		t.Fatalf("duplicate event created duplicate jobs: count=%d err=%v", eventJobs, err)
	}

	started, err := service.BeginProductRefund(ctx, buyerID, checkout.OrderID, "product-refund-001", "refund-request", "The licensed workflow did not fit the documented production requirement.")
	if err != nil || !started || runtime.refundCalls != 0 {
		t.Fatalf("start Provider refund: started=%t calls=%d err=%v", started, runtime.refundCalls, err)
	}
	if err := service.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, checkout.PaymentID)); err != nil {
		t.Fatal(err)
	}
	replayedRefund, err := service.BeginProductRefund(ctx, buyerID, checkout.OrderID, "product-refund-001", "refund-replay", "The licensed workflow did not fit the documented production requirement.")
	if err != nil || replayedRefund || runtime.refundCalls != 1 {
		t.Fatalf("refund replay created another Provider request: started=%t calls=%d err=%v", replayedRefund, runtime.refundCalls, err)
	}
	if _, err := service.BeginProductRefund(ctx, buyerID, checkout.OrderID, "product-refund-conflict", "refund-conflict", "A different request key must not replace an active refund."); !errors.Is(err, ErrRefundConflict) {
		t.Fatalf("active refund accepted a different request key: %v", err)
	}
	assertProductRefundState(t, pool, checkout, "refund_pending", "refund_requested", "active", 0, 0)
	mismatchedBody := productRefundEvent("evt_refundmismatch", "re_unrelated999", "succeeded", checkout.PaymentID, productID, now.Unix(), checkout.AmountCents)
	mismatchedReceipt := receivePaymentWorkflowEvent(t, service, mismatchedBody, now)
	if err := service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, mismatchedReceipt.EventID.String()))}); err == nil {
		t.Fatal("mismatched Provider refund ID revoked rights")
	}
	assertProductRefundState(t, pool, checkout, "refund_pending", "refund_requested", "active", 0, 0)

	failedBody := productRefundEvent("evt_refundfailed", "re_workflow123", "failed", checkout.PaymentID, productID, now.Unix(), checkout.AmountCents)
	failedReceipt := receivePaymentWorkflowEvent(t, service, failedBody, now)
	if err := service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, failedReceipt.EventID.String()))}); err != nil {
		t.Fatalf("process failed refund event: %v", err)
	}
	assertProductRefundState(t, pool, checkout, "paid", "fulfilled", "active", 0, 0)
	if replayedRefund, err := service.BeginProductRefund(ctx, buyerID, checkout.OrderID, "product-refund-001", "failed-refund-replay", "The licensed workflow did not fit the documented production requirement."); err != nil || replayedRefund || runtime.refundCalls != 1 {
		t.Fatalf("failed refund replay created another Provider request: started=%t calls=%d err=%v", replayedRefund, runtime.refundCalls, err)
	}

	started, err = service.BeginProductRefund(ctx, buyerID, checkout.OrderID, "product-refund-002", "refund-retry", "Retry after the Provider confirmed that the first refund did not complete.")
	if err != nil || !started || runtime.refundCalls != 1 {
		t.Fatalf("retry failed Provider refund: started=%t calls=%d err=%v", started, runtime.refundCalls, err)
	}
	if err := service.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, checkout.PaymentID)); err != nil {
		t.Fatal(err)
	}
	succeededBody := productRefundEvent("evt_refundsucceeded", "re_workflow456", "succeeded", checkout.PaymentID, productID, now.Unix(), checkout.AmountCents)
	succeededReceipt := receivePaymentWorkflowEvent(t, service, succeededBody, now)
	refundJob := jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, succeededReceipt.EventID.String()))}
	if err := service.HandlePaymentEventJob(ctx, refundJob); err != nil {
		t.Fatalf("process successful refund event: %v", err)
	}
	if err := service.HandlePaymentEventJob(ctx, refundJob); err != nil {
		t.Fatalf("replay successful refund event: %v", err)
	}
	assertProductRefundState(t, pool, checkout, "refunded", "refunded", "refunded", 0, 0)
}

func TestExternalBillingCheckoutFulfillmentWorkflow(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	userID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role)
		VALUES($1,$2,$3,'Billing Buyer','member')`,
		userID, userID.String()+"@test.local", "billing_"+userID.String()[:8]); err != nil {
		t.Fatal(err)
	}

	var planID uuid.UUID
	var planAmount int
	var planPoints int64
	if err := pool.QueryRow(ctx, `
		SELECT id,price_cents,included_points
		FROM subscription_plans WHERE tier_code='creator' AND active=true`).Scan(&planID, &planAmount, &planPoints); err != nil {
		t.Fatal(err)
	}
	var walletBefore int64
	var pointsBefore int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM billing_accounts WHERE user_id=$1 AND currency='USD'`, userID).Scan(&walletBefore); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT balance_points FROM point_accounts WHERE user_id=$1`, userID).Scan(&pointsBefore); err != nil {
		t.Fatal(err)
	}

	runtime := &productCheckoutRuntime{}
	service := newPaymentTestService(t, pool, ServiceConfig{
		Enabled: true, Provider: "stripe", LiveMode: false,
		APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret,
		WebhookTolerance: 5 * time.Minute,
	}, NewRuntimeCatalog(runtime))

	topup, created, err := service.BeginWalletTopupCheckout(ctx, userID, 4200, "wallet-topup-workflow-001", "wallet-request", "https://app.example.test/workspace/billing?payment=success", "https://app.example.test/workspace/billing?payment=cancelled")
	if err != nil || !created || topup.Status != "checkout_open" || topup.Purpose != "wallet_topup" || topup.PaymentMode != "stripe" || runtime.calls != 1 {
		t.Fatalf("create wallet top-up checkout: checkout=%#v created=%t calls=%d err=%v", topup, created, runtime.calls, err)
	}
	topupReplay, created, err := service.BeginWalletTopupCheckout(ctx, userID, 4200, "wallet-topup-workflow-001", "wallet-replay", "https://app.example.test/workspace/billing?payment=success", "https://app.example.test/workspace/billing?payment=cancelled")
	if err != nil || created || !topupReplay.AlreadyCreated || topupReplay.PaymentID != topup.PaymentID || runtime.calls != 1 {
		t.Fatalf("wallet top-up idempotency mismatch: checkout=%#v created=%t calls=%d err=%v", topupReplay, created, runtime.calls, err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	service.verifier.now = func() time.Time { return now }
	walletBody := billingStripeCheckoutEvent(t, pool, "evt_wallet_topup_001", topup.PaymentID, topup.ResourceID, "wallet_topup", topup.AmountCents, "pi_wallet_topup_001", now.Unix())
	walletReceipt, err := service.ReceiveStripeWebhook(ctx, walletBody, "t="+fmt.Sprint(now.Unix())+",v1="+stripeSignature(testStripeWebhookSecret, now.Unix(), walletBody))
	if err != nil || walletReceipt.Status != "received" || walletReceipt.Duplicate {
		t.Fatalf("receive wallet top-up webhook: receipt=%#v err=%v", walletReceipt, err)
	}
	processPaymentEventJob(t, pool, service, "billing-wallet-worker")
	walletDuplicate, err := service.ReceiveStripeWebhook(ctx, walletBody, "t="+fmt.Sprint(now.Unix())+",v1="+stripeSignature(testStripeWebhookSecret, now.Unix(), walletBody))
	if err != nil || !walletDuplicate.Duplicate {
		t.Fatalf("duplicate wallet webhook mismatch: receipt=%#v err=%v", walletDuplicate, err)
	}
	var walletAfter int64
	var walletEntries, walletIntentPaid int
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM billing_accounts WHERE user_id=$1 AND currency='USD'`, userID).Scan(&walletAfter); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM billing_entries WHERE user_id=$1 AND operation_id=$2 AND entry_type='wallet_topup'`, userID, topup.PaymentID).Scan(&walletEntries); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_intents WHERE id=$1 AND status='paid'`, topup.PaymentID).Scan(&walletIntentPaid); err != nil {
		t.Fatal(err)
	}
	if walletAfter != walletBefore+int64(topup.AmountCents) || walletEntries != 1 || walletIntentPaid != 1 {
		t.Fatalf("wallet top-up fulfillment mismatch: before=%d after=%d entries=%d paid=%d", walletBefore, walletAfter, walletEntries, walletIntentPaid)
	}

	subscription, created, err := service.BeginSubscriptionCheckout(ctx, userID, planID, "subscription-workflow-001", "subscription-request", "https://app.example.test/workspace/billing?payment=success", "https://app.example.test/workspace/billing?payment=cancelled")
	if err != nil || !created || subscription.Status != "checkout_open" || subscription.Purpose != "subscription" || subscription.ResourceID != planID || runtime.calls != 2 {
		t.Fatalf("create subscription checkout: checkout=%#v created=%t calls=%d err=%v", subscription, created, runtime.calls, err)
	}
	subscriptionReplay, created, err := service.BeginSubscriptionCheckout(ctx, userID, planID, "subscription-workflow-001", "subscription-replay", "https://app.example.test/workspace/billing?payment=success", "https://app.example.test/workspace/billing?payment=cancelled")
	if err != nil || created || !subscriptionReplay.AlreadyCreated || subscriptionReplay.PaymentID != subscription.PaymentID || runtime.calls != 2 {
		t.Fatalf("subscription idempotency mismatch: checkout=%#v created=%t calls=%d err=%v", subscriptionReplay, created, runtime.calls, err)
	}
	subscriptionBody := billingStripeCheckoutEvent(t, pool, "evt_subscription_001", subscription.PaymentID, planID, "subscription", subscription.AmountCents, "pi_subscription_001", now.Add(time.Second).Unix())
	subscriptionReceipt, err := service.ReceiveStripeWebhook(ctx, subscriptionBody, "t="+fmt.Sprint(now.Add(time.Second).Unix())+",v1="+stripeSignature(testStripeWebhookSecret, now.Add(time.Second).Unix(), subscriptionBody))
	if err != nil || subscriptionReceipt.Status != "received" || subscriptionReceipt.Duplicate {
		t.Fatalf("receive subscription webhook: receipt=%#v err=%v", subscriptionReceipt, err)
	}
	processPaymentEventJob(t, pool, service, "billing-subscription-worker")
	subscriptionDuplicate, err := service.ReceiveStripeWebhook(ctx, subscriptionBody, "t="+fmt.Sprint(now.Add(time.Second).Unix())+",v1="+stripeSignature(testStripeWebhookSecret, now.Add(time.Second).Unix(), subscriptionBody))
	if err != nil || !subscriptionDuplicate.Duplicate {
		t.Fatalf("duplicate subscription webhook mismatch: receipt=%#v err=%v", subscriptionDuplicate, err)
	}
	var activePlanID uuid.UUID
	var activeSubscriptions, subscriptionPointEntries, subscriptionIntentPaid int
	var pointsAfter int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_subscriptions WHERE user_id=$1 AND status='active'`, userID).Scan(&activeSubscriptions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT plan_id FROM user_subscriptions WHERE user_id=$1 AND purchase_operation_id=$2`, userID, subscription.PaymentID).Scan(&activePlanID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM point_entries WHERE user_id=$1 AND operation_id=$2 AND entry_type='subscription_credit'`, userID, subscription.PaymentID).Scan(&subscriptionPointEntries); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT balance_points FROM point_accounts WHERE user_id=$1`, userID).Scan(&pointsAfter); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_intents WHERE id=$1 AND status='paid'`, subscription.PaymentID).Scan(&subscriptionIntentPaid); err != nil {
		t.Fatal(err)
	}
	if activeSubscriptions != 1 || activePlanID != planID || subscriptionPointEntries != 1 || pointsAfter != pointsBefore+planPoints || subscriptionIntentPaid != 1 || subscription.AmountCents != planAmount {
		t.Fatalf("subscription fulfillment mismatch: active=%d plan=%s entries=%d pointsBefore=%d pointsAfter=%d expected=%d paid=%d amount=%d expectedAmount=%d", activeSubscriptions, activePlanID, subscriptionPointEntries, pointsBefore, pointsAfter, pointsBefore+planPoints, subscriptionIntentPaid, subscription.AmountCents, planAmount)
	}
}

func TestWaffoSubscriptionActivationAndRenewalWorkflow(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	userID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role)
		VALUES($1,$2,$3,'Waffo Subscriber','member')`,
		userID, userID.String()+"@test.local", "waffo_"+userID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_provider_configs(provider,enabled,environment,merchant_id,store_id,product_id_onetime,product_id_subscription)
		VALUES('waffo_pancake',true,'test','MER_test','STO_test','PROD_onetime','PROD_subscription')`); err != nil {
		t.Fatal(err)
	}

	var planID uuid.UUID
	var planAmount int
	var planPoints int64
	if err := pool.QueryRow(ctx, `
		SELECT id,price_cents,included_points FROM subscription_plans
		WHERE tier_code='creator' AND active=true`).Scan(&planID, &planAmount, &planPoints); err != nil {
		t.Fatal(err)
	}
	var pointsBefore int64
	if err := pool.QueryRow(ctx, `SELECT balance_points FROM point_accounts WHERE user_id=$1`, userID).Scan(&pointsBefore); err != nil {
		t.Fatal(err)
	}

	runtime := &waffoBillingCheckoutRuntime{}
	service := newPaymentTestService(t, pool, ServiceConfig{
		Enabled: true, Provider: "waffo_pancake", WaffoEnvironment: "test", WaffoMerchantID: "MER_test",
		WaffoStoreID: "STO_test", WaffoProductIDOnetime: "PROD_onetime", WaffoProductIDSubscription: "PROD_subscription",
	}, NewRuntimeCatalog(runtime))
	checkout, created, err := service.BeginSubscriptionCheckout(ctx, userID, planID, "waffo-subscription-001", "waffo-subscription-request", "https://app.example.test/workspace/billing?payment=success", "https://app.example.test/workspace/billing?payment=cancelled")
	if err != nil || !created || checkout.PaymentMode != "waffo_pancake" || checkout.AmountCents != planAmount || runtime.calls != 1 || !strings.Contains(runtime.lastSuccessURL, "paymentId="+checkout.PaymentID.String()) {
		t.Fatalf("create Waffo subscription checkout: checkout=%#v created=%t calls=%d err=%v", checkout, created, runtime.calls, err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	purpose, currency, succeeded := "subscription", "USD", "succeeded"
	activationAmount := int64(planAmount)
	activationPaymentID := "PAY_waffo_initial_123"
	activation := minimizedProviderEvent{
		ProviderEventID: "delivery_waffo_activation_123", EventType: "subscription.activated", APIVersion: "waffo-pancake-v1",
		WaffoVerificationVersion: waffoWebhookContractVersion, WaffoStoreID: "STO_test",
		WaffoOrderExternalID: checkout.PaymentID.String(), WaffoBuyerIdentity: userID.String(),
		OccurredAt: now, PayloadSHA256: strings.Repeat("a", 64), ObjectID: "ORD_waffo_subscription_123", ObjectType: "order",
		PaymentID: &checkout.PaymentID, ResourceID: &planID, Purpose: &purpose, AmountCents: &activationAmount, Currency: &currency,
		PaymentStatus: &succeeded, ProviderPaymentID: &activationPaymentID, Supported: true,
	}
	activationReceipt, err := service.receiveProviderEvent(ctx, "waffo_pancake", activation)
	if err != nil || activationReceipt.Status != "received" || activationReceipt.Duplicate {
		t.Fatalf("receive Waffo activation: receipt=%#v err=%v", activationReceipt, err)
	}
	processPaymentEventJob(t, pool, service, "waffo-activation-worker")

	var initialPeriodEnd time.Time
	if err := pool.QueryRow(ctx, `SELECT current_period_end FROM user_subscriptions WHERE purchase_operation_id=$1`, checkout.PaymentID).Scan(&initialPeriodEnd); err != nil {
		t.Fatal(err)
	}
	renewalPaymentID := "PAY_waffo_renewal_456"
	renewal := activation
	renewal.ProviderEventID = "delivery_waffo_renewal_456"
	renewal.EventType = "subscription.payment_succeeded"
	renewal.OccurredAt = now.Add(31 * 24 * time.Hour)
	renewal.PayloadSHA256 = strings.Repeat("b", 64)
	renewal.ProviderPaymentID = &renewalPaymentID
	renewalReceipt, err := service.receiveProviderEvent(ctx, "waffo_pancake", renewal)
	if err != nil || renewalReceipt.Status != "received" || renewalReceipt.Duplicate {
		t.Fatalf("receive Waffo renewal: receipt=%#v err=%v", renewalReceipt, err)
	}
	processPaymentEventJob(t, pool, service, "waffo-renewal-worker")
	duplicate, err := service.receiveProviderEvent(ctx, "waffo_pancake", renewal)
	if err != nil || !duplicate.Duplicate || duplicate.EventID != renewalReceipt.EventID {
		t.Fatalf("Waffo renewal replay was not idempotent: receipt=%#v err=%v", duplicate, err)
	}
	var endAfterFirstRenewal time.Time
	if err := pool.QueryRow(ctx, `SELECT current_period_end FROM user_subscriptions WHERE purchase_operation_id=$1`, checkout.PaymentID).Scan(&endAfterFirstRenewal); err != nil {
		t.Fatal(err)
	}
	// A provider may assign a new delivery ID to the same captured payment.
	renewal.ProviderEventID = "delivery_waffo_renewal_redelivered"
	renewal.PayloadSHA256 = strings.Repeat("c", 64)
	redelivery, err := service.receiveProviderEvent(ctx, "waffo_pancake", renewal)
	if err != nil || redelivery.Duplicate {
		t.Fatalf("receive distinct renewal delivery: receipt=%#v err=%v", redelivery, err)
	}
	processPaymentEventJob(t, pool, service, "waffo-renewal-redelivery-worker")

	var pointsAfter int64
	var renewedPeriodEnd time.Time
	var pointCredits, activeSubscriptions, renewalEvents int
	if err := pool.QueryRow(ctx, `SELECT balance_points FROM point_accounts WHERE user_id=$1`, userID).Scan(&pointsAfter); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT current_period_end FROM user_subscriptions WHERE purchase_operation_id=$1 AND status='active'`, checkout.PaymentID).Scan(&renewedPeriodEnd); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM point_entries WHERE user_id=$1 AND entry_type='subscription_credit' AND operation_id IN ($2,$3)`, userID, checkout.PaymentID, renewalReceipt.EventID).Scan(&pointCredits); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_subscriptions WHERE user_id=$1 AND status='active'`, userID).Scan(&activeSubscriptions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1 AND event_type='subscription.renewed'`, checkout.PaymentID).Scan(&renewalEvents); err != nil {
		t.Fatal(err)
	}
	if pointsAfter != pointsBefore+2*planPoints || pointCredits != 2 || activeSubscriptions != 1 || renewalEvents != 1 || !renewedPeriodEnd.After(initialPeriodEnd) {
		t.Fatalf("Waffo subscription lifecycle mismatch: pointsBefore=%d pointsAfter=%d expected=%d credits=%d active=%d renewals=%d initialEnd=%s renewedEnd=%s", pointsBefore, pointsAfter, pointsBefore+2*planPoints, pointCredits, activeSubscriptions, renewalEvents, initialPeriodEnd, renewedPeriodEnd)
	}
	if !renewedPeriodEnd.Equal(endAfterFirstRenewal) {
		t.Fatalf("same payment extended subscription again: first=%s after=%s", endAfterFirstRenewal, renewedPeriodEnd)
	}
}

func processPaymentEventJob(t *testing.T, pool *pgxpool.Pool, service *Service, workerID string) {
	t.Helper()
	repository := jobs.NewRepository(pool)
	for attempt := 0; attempt < 10; attempt++ {
		job, err := repository.Claim(context.Background(), workerID, time.Minute)
		if err != nil {
			t.Fatalf("claim payment event job: job=%#v err=%v", job, err)
		}
		switch job.Kind {
		case PaymentEventJobKind:
			if err := service.HandlePaymentEventJob(context.Background(), job); err != nil {
				t.Fatal(err)
			}
			if err := repository.Complete(context.Background(), job, workerID); err != nil {
				t.Fatal(err)
			}
			return
		case notifications.JobKind:
			if err := notifications.NewRepository(pool).HandleDeliveryJob(context.Background(), job); err != nil {
				t.Fatal(err)
			}
			if err := repository.Complete(context.Background(), job, workerID); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unexpected job before payment event: %#v", job)
		}
	}
	t.Fatal("payment event job was not claimed")
}

func billingStripeCheckoutEvent(t *testing.T, pool *pgxpool.Pool, eventID string, paymentID, resourceID uuid.UUID, purpose string, amount int, providerPaymentID string, created int64) []byte {
	t.Helper()
	var checkoutID string
	if err := pool.QueryRow(t.Context(), `SELECT provider_checkout_id FROM payment_intents WHERE id=$1`, paymentID).Scan(&checkoutID); err != nil {
		t.Fatal(err)
	}
	return []byte(fmt.Sprintf(`{"id":%q,"object":"event","api_version":%q,"created":%d,"livemode":false,"type":"checkout.session.completed","data":{"object":{"id":%q,"object":"checkout.session","status":"complete","payment_status":"paid","amount_total":%d,"currency":"usd","payment_intent":%q,"metadata":{"hcai_payment_id":%q,"hcai_resource_id":%q,"hcai_purpose":%q}}}}`, eventID, testStripeAPIVersion, created, checkoutID, amount, providerPaymentID, paymentID.String(), resourceID.String(), purpose))
}

func productPaidEvent(paymentID, productID uuid.UUID, created int64, amount int) []byte {
	return []byte(fmt.Sprintf(`{"id":"evt_productpaid","object":"event","api_version":%q,"created":%d,"livemode":false,"type":"checkout.session.completed","data":{"object":{"id":"cs_workflow123","object":"checkout.session","status":"complete","payment_status":"paid","amount_total":%d,"currency":"usd","payment_intent":"pi_workflow123","metadata":{"hcai_payment_id":%q,"hcai_resource_id":%q,"hcai_purpose":"product"}}}}`, testStripeAPIVersion, created, amount, paymentID.String(), productID.String()))
}

func productRefundEvent(eventID, refundID, status string, paymentID, productID uuid.UUID, created int64, amount int) []byte {
	return []byte(fmt.Sprintf(`{"id":%q,"object":"event","api_version":%q,"created":%d,"livemode":false,"type":"refund.updated","data":{"object":{"id":%q,"object":"refund","status":%q,"amount":%d,"currency":"usd","payment_intent":"pi_workflow123","metadata":{"hcai_payment_id":%q,"hcai_resource_id":%q,"hcai_purpose":"product"}}}}`, eventID, testStripeAPIVersion, created, refundID, status, amount, paymentID.String(), productID.String()))
}

func receivePaymentWorkflowEvent(t *testing.T, service *Service, body []byte, now time.Time) Receipt {
	t.Helper()
	header := "t=" + fmt.Sprint(now.Unix()) + ",v1=" + stripeSignature(testStripeWebhookSecret, now.Unix(), body)
	receipt, err := service.ReceiveStripeWebhook(context.Background(), body, header)
	if err != nil || receipt.Status != "received" {
		t.Fatalf("receive signed payment event: receipt=%#v err=%v", receipt, err)
	}
	return receipt
}

func assertProductRefundState(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, checkout Checkout, intentStatus, orderStatus, entitlementStatus string, billingCount, ledgerCount int) {
	t.Helper()
	var actualIntent, actualOrder, actualEntitlement string
	var actualBilling, actualLedger int
	err := pool.QueryRow(context.Background(), `
		SELECT pi.status,o.status,e.status,
		       (SELECT count(*) FROM billing_entries WHERE user_id IN (pi.payer_id,pi.payee_id)),
		       (SELECT count(*) FROM ledger_entries WHERE account_id IN (pi.payer_id,pi.payee_id))
		FROM payment_intents pi JOIN orders o ON o.id=pi.order_id JOIN entitlements e ON e.order_id=o.id
		WHERE pi.id=$1`, checkout.PaymentID).Scan(&actualIntent, &actualOrder, &actualEntitlement, &actualBilling, &actualLedger)
	if err != nil {
		t.Fatal(err)
	}
	if actualIntent != intentStatus || actualOrder != orderStatus || actualEntitlement != entitlementStatus || actualBilling != billingCount || actualLedger != ledgerCount {
		t.Fatalf("refund state mismatch: intent=%s order=%s entitlement=%s billing=%d ledger=%d", actualIntent, actualOrder, actualEntitlement, actualBilling, actualLedger)
	}
}
