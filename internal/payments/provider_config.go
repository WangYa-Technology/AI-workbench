package payments

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// persistedProviderConfig is the non-secret operational configuration that an
// administrator may change without changing the deployment's credentials.
type persistedProviderConfig struct {
	Provider              string
	Enabled               bool
	Environment           string
	MerchantID            string
	StoreID               string
	ProductIDOnetime      string
	ProductIDSubscription string
}

// resolveProductProvider returns the active product checkout configuration.
// A persisted enabled provider wins; when no provider has been configured yet,
// the deployment default is used so existing Stripe installations keep their
// behavior after the migration.
func (s *Service) resolveProductProvider(ctx context.Context) (persistedProviderConfig, error) {
	if s == nil {
		return persistedProviderConfig{}, ErrDisabled
	}
	var persisted persistedProviderConfig
	configuredDisabled := false
	if s.pool != nil {
		var configuredProvider string
		err := s.pool.QueryRow(ctx, `
			SELECT provider,enabled,environment,merchant_id,store_id,product_id_onetime,product_id_subscription
			FROM payment_provider_configs WHERE provider=$1`, s.productProvider()).Scan(
			&configuredProvider, &persisted.Enabled, &persisted.Environment, &persisted.MerchantID, &persisted.StoreID, &persisted.ProductIDOnetime, &persisted.ProductIDSubscription)
		if err == nil {
			persisted.Provider = strings.ToLower(strings.TrimSpace(configuredProvider))
			configuredDisabled = !persisted.Enabled
			if configuredDisabled {
				// A different enabled row may represent an explicit provider
				// switch, so do not stop before checking it below.
				persisted.Provider = ""
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return persistedProviderConfig{}, err
		}
		if persisted.Provider == "" {
			err = s.pool.QueryRow(ctx, `
				SELECT provider,enabled,environment,merchant_id,store_id,product_id_onetime,product_id_subscription
				FROM payment_provider_configs WHERE enabled=true ORDER BY provider LIMIT 1`).Scan(
				&persisted.Provider, &persisted.Enabled, &persisted.Environment, &persisted.MerchantID, &persisted.StoreID, &persisted.ProductIDOnetime, &persisted.ProductIDSubscription)
			if err == nil {
				persisted.Provider = strings.ToLower(strings.TrimSpace(persisted.Provider))
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return persistedProviderConfig{}, err
			}
		}
	}
	if persisted.Provider == "" && configuredDisabled {
		return persistedProviderConfig{}, ErrDisabled
	}
	if persisted.Provider == "" {
		persisted.Provider = s.productProvider()
		persisted.Enabled = s.config.Enabled
		persisted.Environment = "test"
		if persisted.Provider == "waffo_pancake" {
			persisted.Environment = s.config.WaffoEnvironment
			persisted.MerchantID = s.config.WaffoMerchantID
			persisted.StoreID = s.config.WaffoStoreID
			persisted.ProductIDOnetime = s.config.WaffoProductIDOnetime
			persisted.ProductIDSubscription = s.config.WaffoProductIDSubscription
		}
	}
	if persisted.Provider == "waffo_pancake" {
		if persisted.MerchantID == "" {
			persisted.MerchantID = s.config.WaffoMerchantID
		}
		if s.config.WaffoMerchantID != "" && !strings.EqualFold(persisted.MerchantID, s.config.WaffoMerchantID) {
			return persistedProviderConfig{}, ErrProviderConfigMismatch
		}
		if persisted.Environment == "" {
			persisted.Environment = s.config.WaffoEnvironment
		}
		if s.config.WaffoEnvironment != "" && !strings.EqualFold(persisted.Environment, s.config.WaffoEnvironment) {
			return persistedProviderConfig{}, ErrProviderConfigMismatch
		}
		if persisted.StoreID == "" {
			persisted.StoreID = s.config.WaffoStoreID
		}
		if persisted.ProductIDOnetime == "" {
			persisted.ProductIDOnetime = s.config.WaffoProductIDOnetime
		}
		if persisted.ProductIDSubscription == "" {
			persisted.ProductIDSubscription = s.config.WaffoProductIDSubscription
		}
	}
	if persisted.Provider == "stripe" {
		expectedEnvironment := "test"
		if s.config.LiveMode {
			expectedEnvironment = "prod"
		}
		if persisted.Environment != "" && !strings.EqualFold(persisted.Environment, expectedEnvironment) {
			return persistedProviderConfig{}, ErrProviderConfigMismatch
		}
	}
	return persisted, nil
}

func (s *Service) providerConfigReady(provider persistedProviderConfig) bool {
	if s == nil || !provider.Enabled {
		return false
	}
	if provider.Provider != "waffo_pancake" {
		return true
	}
	return strings.TrimSpace(provider.MerchantID) != "" && strings.TrimSpace(provider.StoreID) != "" && strings.TrimSpace(provider.ProductIDOnetime) != ""
}

func (s *Service) providerConfigReadyForPurpose(provider persistedProviderConfig, purpose string) bool {
	if !s.providerConfigReady(provider) {
		return false
	}
	if provider.Provider == "waffo_pancake" && purpose == "subscription" {
		return strings.TrimSpace(provider.ProductIDSubscription) != ""
	}
	return true
}

func (s *Service) waffoWebhookSettings(ctx context.Context) (environment, storeID string) {
	if s == nil {
		return "test", ""
	}
	environment, storeID = s.config.WaffoEnvironment, s.config.WaffoStoreID
	if s.pool == nil {
		return environment, storeID
	}
	var configuredEnvironment, configuredStoreID string
	if err := s.pool.QueryRow(ctx, `SELECT environment,store_id FROM payment_provider_configs WHERE provider='waffo_pancake' AND enabled=true`).Scan(&configuredEnvironment, &configuredStoreID); err == nil {
		if strings.TrimSpace(configuredEnvironment) != "" {
			if strings.TrimSpace(environment) != "" && !strings.EqualFold(strings.TrimSpace(environment), strings.TrimSpace(configuredEnvironment)) {
				return "", ""
			}
			environment = configuredEnvironment
		}
		if strings.TrimSpace(configuredStoreID) != "" {
			storeID = configuredStoreID
		}
	}
	return environment, storeID
}
