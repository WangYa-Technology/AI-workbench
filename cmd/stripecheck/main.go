package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
)

const (
	stripeAcceptanceConfirmation = "I_APPROVE_STRIPE_STAGING_CALLS"
)

func main() {
	if os.Getenv("STRIPE_ACCEPTANCE_CONFIRM") != stripeAcceptanceConfirmation || os.Getenv("STRIPE_ACCEPTANCE_MAX_CALLS") != strconv.Itoa(payments.StripeStagingAcceptanceRequestCount) {
		fmt.Fprintf(os.Stderr, "Stripe staging acceptance disabled: explicit confirmation and STRIPE_ACCEPTANCE_MAX_CALLS=%d are required\n", payments.StripeStagingAcceptanceRequestCount)
		os.Exit(1)
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Stripe staging acceptance configuration invalid:", err)
		os.Exit(1)
	}
	if cfg.Environment != "staging" || !cfg.StripeEnabled || cfg.StripeLiveMode || cfg.StripeLiveModeApproved ||
		!strings.HasPrefix(cfg.StripeSecretKey, "sk_test_") || !strings.HasPrefix(cfg.StripeWebhookSecret, "whsec_") ||
		cfg.StripeBaseURL != "https://api.stripe.com/v1" || !publicHTTPSOrigin(cfg.WebOrigin) {
		fmt.Fprintln(os.Stderr, "Stripe staging acceptance requires staging, enabled test mode, a test secret, a Webhook secret, the official API endpoint, and a public HTTPS Web origin")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	runtime := payments.NewStripeRuntime(payments.StripeRuntimeConfig{
		SecretKey: cfg.StripeSecretKey, BaseURL: cfg.StripeBaseURL, APIVersion: cfg.StripeAPIVersion,
		LiveMode: false, HTTPClient: &http.Client{Timeout: 20 * time.Second},
	})
	result, err := payments.RunStripeStagingAcceptance(ctx, runtime)
	// Failure summaries retain only disposable IDs and command references so
	// operators can find uncertain resources without a hidden cleanup retry.
	if encodeErr := json.NewEncoder(os.Stdout).Encode(result); encodeErr != nil {
		fmt.Fprintln(os.Stderr, "encode Stripe staging acceptance summary:", encodeErr)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Stripe staging acceptance failed:", err)
		os.Exit(1)
	}
}

func publicHTTPSOrigin(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == ""
}
