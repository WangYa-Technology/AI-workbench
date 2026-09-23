package payments

import (
	"net/http"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewServiceFromConfig is the single payment runtime assembly point shared by
// API and Worker processes. Keeping provider gates and webhook settings here
// prevents the two binaries from drifting apart.
func NewServiceFromConfig(pool *pgxpool.Pool, cfg config.Config) *Service {
	runtimes := make([]ProviderRuntime, 0, 2)
	if cfg.StripeEnabled {
		runtimes = append(runtimes, NewStripeRuntime(StripeRuntimeConfig{
			SecretKey: cfg.StripeSecretKey, BaseURL: cfg.StripeBaseURL, APIVersion: cfg.StripeAPIVersion,
			LiveMode: cfg.StripeLiveMode, HTTPClient: &http.Client{Timeout: 20 * time.Second},
		}))
	}
	if cfg.WaffoEnabled {
		runtimes = append(runtimes, NewWaffoRuntime(WaffoRuntimeConfig{
			ConnectorURL: cfg.WaffoConnectorURL, ConnectorToken: cfg.WaffoConnectorToken, Environment: cfg.WaffoEnvironment,
			StoreID: cfg.WaffoStoreID, ProductIDOnetime: cfg.WaffoProductIDOnetime, ProductIDSubscription: cfg.WaffoProductIDSubscription,
			HTTPClient: &http.Client{Timeout: 20 * time.Second},
		}))
	}
	return NewServiceWithRuntimes(pool, ServiceConfig{
		MediaStores: media.NewCatalogFromConfig(cfg),
		Enabled:     cfg.StripeEnabled || cfg.WaffoEnabled, Provider: cfg.PaymentProvider, LiveMode: cfg.StripeLiveMode,
		APIVersion: cfg.StripeAPIVersion, WebhookSecret: cfg.StripeWebhookSecret,
		WebhookTolerance: time.Duration(cfg.StripeWebhookToleranceSeconds) * time.Second,
		WaffoWebhookURL:  cfg.WaffoConnectorURL, WaffoConnectorToken: cfg.WaffoConnectorToken,
		WaffoEnvironment: cfg.WaffoEnvironment, WaffoMerchantID: cfg.WaffoMerchantID, WaffoStoreID: cfg.WaffoStoreID,
		WaffoProductIDOnetime: cfg.WaffoProductIDOnetime, WaffoProductIDSubscription: cfg.WaffoProductIDSubscription,
	}, NewRuntimeCatalog(runtimes...))
}
