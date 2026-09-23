package payments

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const StripeStagingAcceptanceRequestCount = 6
const stripeStagingCleanupTimeout = 10 * time.Second

type StripeStagingAcceptanceResource struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type StripeStagingAcceptanceCreation struct {
	Kind           string `json:"kind"`
	IdempotencyKey string `json:"idempotencyKey"`
}

type StripeStagingAcceptanceResult struct {
	Status                string                            `json:"status"`
	Provider              string                            `json:"provider"`
	LiveMode              bool                              `json:"liveMode"`
	RequestCount          int                               `json:"requestCount"`
	ChargeCreated         bool                              `json:"chargeCreated"`
	CheckoutExpired       bool                              `json:"checkoutExpired"`
	ConnectAccountCreated bool                              `json:"connectAccountCreated"`
	AccountLinkCreated    bool                              `json:"accountLinkCreated"`
	ConnectAccountDeleted bool                              `json:"connectAccountDeleted"`
	CleanupComplete       bool                              `json:"cleanupComplete"`
	PendingCleanup        []StripeStagingAcceptanceResource `json:"pendingCleanup,omitempty"`
	UnconfirmedCreations  []StripeStagingAcceptanceCreation `json:"unconfirmedCreations,omitempty"`
}

// RunStripeStagingAcceptance creates only disposable Stripe test-mode objects.
// It never confirms a Checkout Session or creates a PaymentIntent charge.
func RunStripeStagingAcceptance(ctx context.Context, runtime *StripeRuntime) (result StripeStagingAcceptanceResult, err error) {
	result = StripeStagingAcceptanceResult{Status: "failed", Provider: "stripe", LiveMode: false, CleanupComplete: true}
	if runtime == nil || runtime.config.LiveMode || !strings.HasPrefix(runtime.config.SecretKey, "sk_test_") {
		return result, fmt.Errorf("Stripe staging acceptance requires a test-mode runtime")
	}
	// A reused HTTP connection can transparently replay an idempotent POST.
	// Isolate this acceptance run and use fresh HTTP/1 connections so each
	// budget reservation permits at most one provider request. Do not change
	// the application's shared pool, TLS trust, proxy or client timeout.
	base := runtime.client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	transport, ok := base.(*http.Transport)
	if !ok || transport == nil {
		return result, fmt.Errorf("Stripe staging acceptance requires a standard HTTP transport to bound requests")
	}
	transport = transport.Clone()
	transport.DisableKeepAlives = true
	transport.ForceAttemptHTTP2 = false
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	transport.TLSNextProto = nil
	if transport.TLSClientConfig != nil {
		transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
	}
	defer transport.CloseIdleConnections()
	copyRuntime, copyClient := *runtime, *runtime.client
	copyClient.Transport = transport
	copyRuntime.client = &copyClient
	runtime = &copyRuntime
	paymentID, resourceID, userID := uuid.New(), uuid.New(), uuid.New()
	// Ownership and call accounting belong to this invocation. Recovery must
	// never use another run's account or exceed the approved external-call cap.
	var checkoutID, accountID string
	defer func() {
		cleanup := func(kind, id string, complete *bool, remove func(context.Context, string) error) {
			if id == "" || *complete {
				return
			}
			if result.RequestCount < StripeStagingAcceptanceRequestCount {
				cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stripeStagingCleanupTimeout)
				result.RequestCount++
				cleanupErr := remove(cleanupCtx, id)
				cancel()
				if cleanupErr == nil {
					*complete = true
				} else {
					err = errors.Join(err, fmt.Errorf("clean up Stripe test %s: %w", kind, cleanupErr))
				}
			}
			if !*complete {
				result.PendingCleanup = append(result.PendingCleanup, StripeStagingAcceptanceResource{Kind: kind, ID: id})
			}
		}
		cleanup("checkout_session", checkoutID, &result.CheckoutExpired, runtime.ExpireCheckout)
		cleanup("connected_account", accountID, &result.ConnectAccountDeleted, runtime.DeleteConnectAccount)
		result.CleanupComplete = len(result.PendingCleanup) == 0 && len(result.UnconfirmedCreations) == 0
		if !result.CleanupComplete {
			err = errors.Join(err, fmt.Errorf("Stripe staging acceptance has unresolved test resources"))
		}
		if err != nil {
			result.Status = "failed"
		}
	}()
	result.RequestCount++
	if err := runtime.verifyCredentialMode(ctx); err != nil {
		return result, fmt.Errorf("verify Stripe test credential mode: %w", err)
	}
	result.RequestCount++
	checkout, err := runtime.CreateCheckout(ctx, CheckoutRequest{
		PaymentID: paymentID, ResourceID: resourceID, Purpose: "product", Name: "HCAI staging payment check",
		AmountCents: 50, Currency: "usd", SuccessURL: "https://staging.example.test/payments/success", CancelURL: "https://staging.example.test/payments/cancel",
	})
	if err != nil {
		result.UnconfirmedCreations = append(result.UnconfirmedCreations, StripeStagingAcceptanceCreation{Kind: "checkout_session", IdempotencyKey: "checkout-" + paymentID.String()})
		return result, fmt.Errorf("create Stripe test Checkout Session: %w", err)
	}
	checkoutID = checkout.ProviderID
	if checkout.LiveMode || checkout.Status != "open" || checkout.PaymentStatus != "unpaid" {
		return result, fmt.Errorf("Stripe Checkout Session was not an unpaid test session")
	}
	result.RequestCount++
	if err := runtime.ExpireCheckout(ctx, checkoutID); err != nil {
		return result, fmt.Errorf("expire Stripe test Checkout Session: %w", err)
	}
	result.CheckoutExpired = true
	result.ChargeCreated = false

	result.RequestCount++
	account, err := runtime.createConnectAccountAfterModeCheck(ctx, ConnectAccountRequest{UserID: userID, Email: "stripe-staging-acceptance@example.test"})
	if err != nil {
		result.UnconfirmedCreations = append(result.UnconfirmedCreations, StripeStagingAcceptanceCreation{Kind: "connected_account", IdempotencyKey: "connect-account-" + userID.String()})
		return result, fmt.Errorf("create Stripe test Connect account: %w", err)
	}
	accountID = account.ID
	result.ConnectAccountCreated = true
	if account.LiveMode {
		return result, fmt.Errorf("Stripe Connect account unexpectedly used live mode")
	}
	result.RequestCount++
	link, err := runtime.CreateAccountLink(ctx, AccountLinkRequest{
		DestinationID: account.ID,
		RefreshURL:    "https://staging.example.test/settings/payouts/refresh",
		ReturnURL:     "https://staging.example.test/settings/payouts/return",
	})
	if err != nil {
		return result, fmt.Errorf("create Stripe test Account Link: %w", err)
	}
	parsed, _ := url.Parse(link.URL)
	if parsed == nil || !strings.EqualFold(parsed.Hostname(), "connect.stripe.com") || link.ExpiresAt.Before(time.Now().UTC()) {
		return result, fmt.Errorf("Stripe test Account Link evidence was invalid")
	}
	result.AccountLinkCreated = true
	result.RequestCount++
	if err := runtime.DeleteConnectAccount(ctx, accountID); err != nil {
		return result, fmt.Errorf("delete Stripe test Connect account: %w", err)
	}
	result.ConnectAccountDeleted = true
	if result.RequestCount != StripeStagingAcceptanceRequestCount {
		return result, fmt.Errorf("Stripe staging acceptance request count mismatch")
	}
	result.Status = "passed"
	return result, nil
}
