package payments

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const StripeStagingAcceptanceRequestCount = 5

type StripeStagingAcceptanceResult struct {
	Status                string `json:"status"`
	Provider              string `json:"provider"`
	LiveMode              bool   `json:"liveMode"`
	RequestCount          int    `json:"requestCount"`
	ChargeCreated         bool   `json:"chargeCreated"`
	CheckoutExpired       bool   `json:"checkoutExpired"`
	ConnectAccountCreated bool   `json:"connectAccountCreated"`
	AccountLinkCreated    bool   `json:"accountLinkCreated"`
	ConnectAccountDeleted bool   `json:"connectAccountDeleted"`
}

// RunStripeStagingAcceptance creates only disposable Stripe test-mode objects.
// It never confirms a Checkout Session or creates a PaymentIntent charge.
func RunStripeStagingAcceptance(ctx context.Context, runtime *StripeRuntime) (result StripeStagingAcceptanceResult, err error) {
	if runtime == nil || runtime.config.LiveMode || !strings.HasPrefix(runtime.config.SecretKey, "sk_test_") {
		return result, fmt.Errorf("Stripe staging acceptance requires a test-mode runtime")
	}
	result = StripeStagingAcceptanceResult{Provider: "stripe", LiveMode: false}
	paymentID, resourceID, userID := uuid.New(), uuid.New(), uuid.New()
	checkout, err := runtime.CreateCheckout(ctx, CheckoutRequest{
		PaymentID: paymentID, ResourceID: resourceID, Purpose: "product", Name: "HCAI staging payment check",
		AmountCents: 50, Currency: "usd", SuccessURL: "https://staging.example.test/payments/success", CancelURL: "https://staging.example.test/payments/cancel",
	})
	result.RequestCount++
	if err != nil {
		return result, fmt.Errorf("create Stripe test Checkout Session: %w", err)
	}
	cleanupCheckout := true
	defer func() {
		if cleanupCheckout && !result.CheckoutExpired {
			_ = runtime.ExpireCheckout(context.WithoutCancel(ctx), checkout.ProviderID)
		}
		if result.ConnectAccountCreated && !result.ConnectAccountDeleted {
			_ = runtime.DeleteConnectAccount(context.WithoutCancel(ctx), resultConnectAccountID)
		}
	}()
	if checkout.LiveMode || checkout.Status != "open" || checkout.PaymentStatus != "unpaid" {
		return result, fmt.Errorf("Stripe Checkout Session was not an unpaid test session")
	}
	if err := runtime.ExpireCheckout(ctx, checkout.ProviderID); err != nil {
		result.RequestCount++
		return result, fmt.Errorf("expire Stripe test Checkout Session: %w", err)
	}
	result.RequestCount++
	result.CheckoutExpired = true
	result.ChargeCreated = false

	account, err := runtime.CreateConnectAccount(ctx, ConnectAccountRequest{UserID: userID, Email: "stripe-staging-acceptance@example.test"})
	result.RequestCount++
	if err != nil {
		return result, fmt.Errorf("create Stripe test Connect account: %w", err)
	}
	resultConnectAccountID = account.ID
	result.ConnectAccountCreated = true
	if account.LiveMode {
		return result, fmt.Errorf("Stripe Connect account unexpectedly used live mode")
	}
	link, err := runtime.CreateAccountLink(ctx, AccountLinkRequest{
		DestinationID: account.ID,
		RefreshURL:    "https://staging.example.test/settings/payouts/refresh",
		ReturnURL:     "https://staging.example.test/settings/payouts/return",
	})
	result.RequestCount++
	if err != nil {
		return result, fmt.Errorf("create Stripe test Account Link: %w", err)
	}
	parsed, _ := url.Parse(link.URL)
	if parsed == nil || !strings.EqualFold(parsed.Hostname(), "connect.stripe.com") || link.ExpiresAt.Before(time.Now().UTC()) {
		return result, fmt.Errorf("Stripe test Account Link evidence was invalid")
	}
	result.AccountLinkCreated = true
	if err := runtime.DeleteConnectAccount(ctx, account.ID); err != nil {
		return result, fmt.Errorf("delete Stripe test Connect account: %w", err)
	}
	result.RequestCount++
	result.ConnectAccountDeleted = true
	cleanupCheckout = false
	if result.RequestCount != StripeStagingAcceptanceRequestCount {
		return result, fmt.Errorf("Stripe staging acceptance request count mismatch")
	}
	result.Status = "passed"
	return result, nil
}

var resultConnectAccountID string
