package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
)

type safeSummary struct {
	Status                  string `json:"status"`
	Environment             string `json:"environment"`
	SecureCookies           bool   `json:"secureCookies"`
	TrustedProxyPrefixCount int    `json:"trustedProxyPrefixCount"`
	LocalProviderEnabled    bool   `json:"localProviderEnabled"`
	EmailDeliveryMode       string `json:"emailDeliveryMode"`
	MediaStorageAdapter     string `json:"mediaStorageAdapter"`
	MediaScannerAdapter     string `json:"mediaScannerAdapter"`
	OpenAIEnabled           bool   `json:"openaiEnabled"`
	OpenAIReconciliation    bool   `json:"openaiReconciliationEnabled"`
	StripeEnabled           bool   `json:"stripeEnabled"`
	StripeLiveMode          bool   `json:"stripeLiveMode"`
}

func main() {
	requireProduction := flag.Bool("require-production", false, "reject non-production configuration")
	flag.Parse()
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration invalid:", err)
		os.Exit(1)
	}
	if *requireProduction && cfg.Environment != "production" {
		fmt.Fprintln(os.Stderr, "configuration invalid: APP_ENV must be production")
		os.Exit(1)
	}
	result := safeSummary{
		Status: "valid", Environment: cfg.Environment, SecureCookies: cfg.CookieSecure,
		TrustedProxyPrefixCount: len(cfg.TrustedProxyCIDRs), LocalProviderEnabled: cfg.LocalProviderEnabled,
		EmailDeliveryMode: cfg.EmailDeliveryMode, OpenAIEnabled: cfg.OpenAIEnabled,
		MediaStorageAdapter: cfg.MediaStorageAdapter, MediaScannerAdapter: cfg.MediaScannerAdapter,
		OpenAIReconciliation: cfg.OpenAIReconciliationEnabled, StripeEnabled: cfg.StripeEnabled,
		StripeLiveMode: cfg.StripeLiveMode,
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, "encode safe configuration summary:", err)
		os.Exit(1)
	}
}
