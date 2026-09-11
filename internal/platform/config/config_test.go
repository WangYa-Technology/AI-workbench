package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestProductionFailsClosed(t *testing.T) {
	clearProviderEnv(t)
	setValidProductionEnv(t)
	t.Setenv("LOCAL_PROVIDER_ENABLED", "true")
	if _, err := Load(); err == nil {
		t.Fatal("expected production local provider configuration to fail")
	}
}

func TestDevelopmentDefaults(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("APP_ENV", "development")
	t.Setenv("LOCAL_PROVIDER_ENABLED", "")
	t.Setenv("COOKIE_SECURE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.LocalProviderEnabled || cfg.DemoDataEnabled || cfg.CookieSecure {
		t.Fatalf("unexpected development defaults: %+v", cfg)
	}
	if len(cfg.WebhookEncryptionKey) != 32 || !cfg.WebhookAllowLocal {
		t.Fatalf("unexpected development Webhook defaults: key=%d allowLocal=%v", len(cfg.WebhookEncryptionKey), cfg.WebhookAllowLocal)
	}
	if len(cfg.TrustedProxyCIDRs) != 0 {
		t.Fatalf("unexpected trusted proxy defaults: %+v", cfg.TrustedProxyCIDRs)
	}
	if cfg.EmailDeliveryMode != "local_file" || len(cfg.EmailActionKey) != 32 {
		t.Fatalf("unexpected development email defaults: mode=%q key=%d", cfg.EmailDeliveryMode, len(cfg.EmailActionKey))
	}
	if cfg.MediaStorageAdapter != "local_file" || cfg.MediaScannerAdapter != "local_deterministic" || cfg.MediaScannerTimeoutSeconds != 30 {
		t.Fatalf("unexpected development media defaults: storage=%q scanner=%q timeout=%d", cfg.MediaStorageAdapter, cfg.MediaScannerAdapter, cfg.MediaScannerTimeoutSeconds)
	}
	if cfg.OpenAIEnabled || cfg.OpenAIPaidCallsApproved || cfg.OpenAIAPIKey != "" || cfg.OpenAIBaseURL != "https://api.openai.com/v1" ||
		cfg.OpenAIChatModel != "gpt-5.6-terra" || cfg.OpenAIImageModel != "gpt-image-2" || cfg.OpenAIChatMaxOutputTokens != 2048 ||
		cfg.OpenAIImageSize != "1024x1024" || cfg.OpenAIImageQuality != "medium" || cfg.OpenAIReconciliationEnabled || cfg.OpenAIReconciliationApproved || cfg.OpenAIAdminAPIKey != "" {
		t.Fatalf("unexpected development OpenAI defaults: %+v", cfg)
	}
	if cfg.StripeEnabled || cfg.StripeLiveMode || cfg.StripeLiveModeApproved || cfg.StripeSecretKey != "" || cfg.StripeWebhookSecret != "" ||
		cfg.StripeBaseURL != "https://api.stripe.com/v1" || cfg.StripeAPIVersion != "2026-02-25.clover" || cfg.StripeWebhookToleranceSeconds != 300 {
		t.Fatalf("unexpected development Stripe defaults: %+v", cfg)
	}
}

func TestOpenAIReconciliationRequiresSeparateApprovalAndAdminScope(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("OPENAI_RECONCILIATION_ENABLED", "true")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "OPENAI_RECONCILIATION_APPROVED") {
		t.Fatalf("reconciliation without approval did not fail closed: %v", err)
	}
	t.Setenv("OPENAI_RECONCILIATION_APPROVED", "true")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "OPENAI_ADMIN_API_KEY") {
		t.Fatalf("reconciliation without admin key did not fail closed: %v", err)
	}
	t.Setenv("OPENAI_ADMIN_API_KEY", "admin-test-key-never-sent")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "OPENAI_PROJECT") {
		t.Fatalf("reconciliation without project filter did not fail closed: %v", err)
	}
	t.Setenv("OPENAI_PROJECT", "proj_reconciliation_test")
	cfg, err := Load()
	if err != nil || !cfg.OpenAIReconciliationEnabled || !cfg.OpenAIReconciliationApproved || cfg.OpenAIAdminAPIKey == "" {
		t.Fatalf("valid reconciliation configuration failed: cfg=%+v err=%v", cfg, err)
	}
}

func TestTrustedProxyCIDRsRequireExplicitNonZeroPrefixes(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("TRUSTED_PROXY_CIDRS", "10.20.0.0/16, 2001:db8:1234::/48")
	cfg, err := Load()
	if err != nil || len(cfg.TrustedProxyCIDRs) != 2 || cfg.TrustedProxyCIDRs[0].String() != "10.20.0.0/16" || cfg.TrustedProxyCIDRs[1].String() != "2001:db8:1234::/48" {
		t.Fatalf("trusted proxy CIDRs were not normalized: %+v err=%v", cfg.TrustedProxyCIDRs, err)
	}
	t.Setenv("TRUSTED_PROXY_CIDRS", "0.0.0.0/0")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXY_CIDRS") {
		t.Fatalf("zero-prefix trusted proxy was accepted: %v", err)
	}
}

func TestProductionRequiresWebhookEncryptionKeyAndPublicTargets(t *testing.T) {
	clearProviderEnv(t)
	setValidProductionEnv(t)
	t.Setenv("WEBHOOK_ENCRYPTION_KEY_B64", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "WEBHOOK_ENCRYPTION_KEY_B64") {
		t.Fatalf("missing production Webhook key did not fail closed: %v", err)
	}
	t.Setenv("WEBHOOK_ENCRYPTION_KEY_B64", productionTestKey(1))
	if _, err := Load(); err != nil {
		t.Fatalf("valid production Webhook boundary failed: %v", err)
	}
}

func TestProductionIdentityEmailFailsClosed(t *testing.T) {
	clearProviderEnv(t)
	setValidProductionEnv(t)
	t.Setenv("EMAIL_DELIVERY_MODE", "local_file")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "EMAIL_DELIVERY_MODE") {
		t.Fatalf("production local mailbox mode did not fail closed: %v", err)
	}
}

func TestOpenAIEnablementRequiresApprovalAndCredentials(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("APP_ENV", "development")
	t.Setenv("OPENAI_ENABLED", "true")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "OPENAI_PAID_CALLS_APPROVED") {
		t.Fatalf("OpenAI enablement without approval did not fail closed: %v", err)
	}
	t.Setenv("OPENAI_PAID_CALLS_APPROVED", "true")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("OpenAI enablement without credentials did not fail closed: %v", err)
	}
	t.Setenv("OPENAI_API_KEY", "test-key-never-sent")
	t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:18080/v1/")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("valid loopback OpenAI test configuration failed: %v", err)
	}
	if !cfg.OpenAIEnabled || !cfg.OpenAIPaidCallsApproved || cfg.OpenAIBaseURL != "http://127.0.0.1:18080/v1" {
		t.Fatalf("OpenAI test configuration was not normalized: %+v", cfg)
	}
}

func TestOpenAIConfigurationRejectsUnsafeValues(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("OPENAI_ENABLED", "invalid")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "OPENAI_ENABLED") {
		t.Fatalf("invalid enablement value was accepted: %v", err)
	}
	t.Setenv("OPENAI_ENABLED", "false")
	t.Setenv("OPENAI_BASE_URL", "http://provider.example/v1")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("non-loopback HTTP endpoint was accepted: %v", err)
	}
	t.Setenv("OPENAI_BASE_URL", "https://api.openai.com/v1")
	t.Setenv("OPENAI_IMAGE_SIZE", "640x480")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "OPENAI_IMAGE_SIZE") {
		t.Fatalf("unsupported image size was accepted: %v", err)
	}
}

func TestVideoAndMusicEnablementRequiresApprovalAndCredentials(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("VIDEO_ENABLED", "true")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "VIDEO_PAID_CALLS_APPROVED") {
		t.Fatalf("Video enablement without approval did not fail closed: %v", err)
	}
	t.Setenv("VIDEO_PAID_CALLS_APPROVED", "true")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "VIDEO_API_KEY") {
		t.Fatalf("Video enablement without credentials did not fail closed: %v", err)
	}
	t.Setenv("VIDEO_API_KEY", "video-test-key")
	t.Setenv("VIDEO_BASE_URL", "http://127.0.0.1:18083/api/v3/")
	t.Setenv("MUSIC_ENABLED", "true")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "MUSIC_PAID_CALLS_APPROVED") {
		t.Fatalf("Music enablement without approval did not fail closed: %v", err)
	}
	t.Setenv("MUSIC_PAID_CALLS_APPROVED", "true")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "MUSIC_API_KEY") {
		t.Fatalf("Music enablement without credentials did not fail closed: %v", err)
	}
	t.Setenv("MUSIC_API_KEY", "music-test-key")
	t.Setenv("MUSIC_BASE_URL", "http://127.0.0.1:18084/v1/")
	cfg, err := Load()
	if err != nil || !cfg.VideoEnabled || !cfg.MusicEnabled || cfg.VideoBaseURL != "http://127.0.0.1:18083/api/v3" || cfg.MusicBaseURL != "http://127.0.0.1:18084/v1" {
		t.Fatalf("valid loopback Video/Music configuration failed: cfg=%+v err=%v", cfg, err)
	}
}

func TestProductionVideoAndMusicRequireOfficialEndpoints(t *testing.T) {
	clearProviderEnv(t)
	setValidProductionEnv(t)
	t.Setenv("VIDEO_BASE_URL", "https://video-gateway.example/api/v3")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "official BytePlus") {
		t.Fatalf("unofficial production Video endpoint was accepted: %v", err)
	}
	t.Setenv("VIDEO_BASE_URL", "https://ark.ap-southeast.bytepluses.com/api/v3")
	t.Setenv("MUSIC_BASE_URL", "https://music-gateway.example/v1")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "official MiniMax") {
		t.Fatalf("unofficial production Music endpoint was accepted: %v", err)
	}
}

func TestProductionOpenAIRequiresOfficialEndpoint(t *testing.T) {
	clearProviderEnv(t)
	setValidProductionEnv(t)
	t.Setenv("OPENAI_ENABLED", "true")
	t.Setenv("OPENAI_PAID_CALLS_APPROVED", "true")
	t.Setenv("OPENAI_API_KEY", "production-test-key-never-sent")
	t.Setenv("OPENAI_BASE_URL", "https://gateway.example/v1")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "api.openai.com") {
		t.Fatalf("non-official production OpenAI endpoint was accepted: %v", err)
	}
	t.Setenv("OPENAI_BASE_URL", "https://api.openai.com/v1/")
	if _, err := Load(); err != nil {
		t.Fatalf("official production OpenAI endpoint failed: %v", err)
	}
}

func TestStripeEnablementAndLiveModeFailClosed(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("APP_ENV", "development")
	t.Setenv("STRIPE_ENABLED", "true")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "STRIPE_SECRET_KEY") {
		t.Fatalf("Stripe enablement without credentials did not fail closed: %v", err)
	}
	t.Setenv("STRIPE_SECRET_KEY", "sk_test_contract_key")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_contract_secret")
	t.Setenv("STRIPE_BASE_URL", "http://127.0.0.1:18082/v1/")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("valid Stripe test configuration failed: %v", err)
	}
	if !cfg.StripeEnabled || cfg.StripeLiveMode || cfg.StripeBaseURL != "http://127.0.0.1:18082/v1" {
		t.Fatalf("Stripe test configuration was not normalized: %+v", cfg)
	}
	t.Setenv("STRIPE_LIVE_MODE", "true")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "STRIPE_LIVE_MODE_APPROVED") {
		t.Fatalf("Stripe live mode without approval did not fail closed: %v", err)
	}
	t.Setenv("STRIPE_LIVE_MODE_APPROVED", "true")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "test/live mode") {
		t.Fatalf("test key was accepted in live mode: %v", err)
	}
}

func TestStripeConfigurationRejectsUnsafeEndpointAndTolerance(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("STRIPE_BASE_URL", "http://payments.example/v1")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("non-loopback Stripe HTTP endpoint was accepted: %v", err)
	}
	t.Setenv("STRIPE_BASE_URL", "https://api.stripe.com/v1")
	t.Setenv("STRIPE_WEBHOOK_TOLERANCE_SECONDS", "30")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "STRIPE_WEBHOOK_TOLERANCE_SECONDS") {
		t.Fatalf("unsafe Stripe webhook tolerance was accepted: %v", err)
	}
}

func TestWaffoEnablementRequiresConnectorConfiguration(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("PAYMENT_PROVIDER", "waffo_pancake")
	t.Setenv("WAFFO_ENABLED", "true")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "WAFFO_MERCHANT_ID") {
		t.Fatalf("Waffo enablement without connector configuration did not fail closed: %v", err)
	}
	t.Setenv("WAFFO_MERCHANT_ID", "MER_contract")
	t.Setenv("WAFFO_CONNECTOR_TOKEN", "connector-contract-token")
	t.Setenv("WAFFO_CONNECTOR_URL", "http://127.0.0.1:18091")
	cfg, err := Load()
	if err != nil || !cfg.WaffoEnabled || cfg.PaymentProvider != "waffo_pancake" || cfg.WaffoEnvironment != "test" {
		t.Fatalf("valid Waffo test configuration failed: cfg=%+v err=%v", cfg, err)
	}
}

func TestProductionStripeRequiresLiveApprovalAndPublicOrigin(t *testing.T) {
	clearProviderEnv(t)
	setValidProductionEnv(t)
	t.Setenv("WEB_ORIGIN", "http://localhost:5173")
	t.Setenv("STRIPE_ENABLED", "true")
	t.Setenv("STRIPE_SECRET_KEY", "sk_test_contract_key")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_contract_secret")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "approved live mode") {
		t.Fatalf("production Stripe test mode was accepted: %v", err)
	}
	t.Setenv("STRIPE_LIVE_MODE", "true")
	t.Setenv("STRIPE_LIVE_MODE_APPROVED", "true")
	t.Setenv("STRIPE_SECRET_KEY", "sk_live_contract_key")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "WEB_ORIGIN") {
		t.Fatalf("production Stripe accepted a local Web origin: %v", err)
	}
	t.Setenv("WEB_ORIGIN", "https://app.hcai.example")
	if _, err := Load(); err != nil {
		t.Fatalf("valid production Stripe boundary failed: %v", err)
	}
}

func TestProductionRequiresVerifiedDatabasePublicOriginAndIndependentKeys(t *testing.T) {
	t.Run("database TLS", func(t *testing.T) {
		clearProviderEnv(t)
		setValidProductionEnv(t)
		t.Setenv("DATABASE_URL", "postgres://service:secret@db.example/hcai?sslmode=require")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "sslmode=verify-full") {
			t.Fatalf("production database without verified TLS was accepted: %v", err)
		}
	})
	t.Run("public Web origin", func(t *testing.T) {
		clearProviderEnv(t)
		setValidProductionEnv(t)
		t.Setenv("WEB_ORIGIN", "http://app.hcai.example")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "public HTTPS origin") {
			t.Fatalf("non-HTTPS production Web origin was accepted: %v", err)
		}
	})
	t.Run("independent encryption keys", func(t *testing.T) {
		clearProviderEnv(t)
		setValidProductionEnv(t)
		t.Setenv("EMAIL_ACTION_ENCRYPTION_KEY_B64", productionTestKey(1))
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "independent keys") {
			t.Fatalf("reused production encryption key was accepted: %v", err)
		}
	})
	t.Run("deployment placeholders", func(t *testing.T) {
		clearProviderEnv(t)
		setValidProductionEnv(t)
		t.Setenv("DATABASE_URL", "postgres://REPLACE_USER:secret@db.example/hcai?sslmode=verify-full")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "placeholders") {
			t.Fatalf("production database placeholder was accepted: %v", err)
		}
	})
}

func TestMediaAdaptersFailClosed(t *testing.T) {
	t.Run("production placeholders", func(t *testing.T) {
		clearProviderEnv(t)
		setValidProductionEnv(t)
		t.Setenv("MEDIA_S3_BUCKET", "REPLACE_MEDIA_BUCKET")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "placeholders") {
			t.Fatalf("production media placeholder was accepted: %v", err)
		}
	})
	t.Run("production local storage", func(t *testing.T) {
		clearProviderEnv(t)
		setValidProductionEnv(t)
		t.Setenv("MEDIA_STORAGE_ADAPTER", "local_file")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "must be s3 in production") {
			t.Fatalf("production local media storage was accepted: %v", err)
		}
	})
	t.Run("production local scanner", func(t *testing.T) {
		clearProviderEnv(t)
		setValidProductionEnv(t)
		t.Setenv("MEDIA_SCANNER_ADAPTER", "local_deterministic")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "must be http in production") {
			t.Fatalf("production local scanner was accepted: %v", err)
		}
	})
	t.Run("unsafe S3 endpoint", func(t *testing.T) {
		clearProviderEnv(t)
		t.Setenv("MEDIA_STORAGE_ADAPTER", "s3")
		t.Setenv("MEDIA_S3_BUCKET", "hcai-test")
		t.Setenv("MEDIA_S3_REGION", "test-1")
		t.Setenv("MEDIA_S3_ACCESS_KEY_ID", "test-access")
		t.Setenv("MEDIA_S3_SECRET_ACCESS_KEY", "test-secret")
		t.Setenv("MEDIA_S3_ENDPOINT", "http://objects.example")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "loopback") {
			t.Fatalf("unsafe S3 endpoint was accepted: %v", err)
		}
	})
	t.Run("loopback contracts", func(t *testing.T) {
		clearProviderEnv(t)
		t.Setenv("MEDIA_STORAGE_ADAPTER", "s3")
		t.Setenv("MEDIA_S3_BUCKET", "hcai-test")
		t.Setenv("MEDIA_S3_REGION", "test-1")
		t.Setenv("MEDIA_S3_ACCESS_KEY_ID", "test-access")
		t.Setenv("MEDIA_S3_SECRET_ACCESS_KEY", "test-secret")
		t.Setenv("MEDIA_S3_ENDPOINT", "http://127.0.0.1:19090")
		t.Setenv("MEDIA_S3_PATH_STYLE", "true")
		t.Setenv("MEDIA_SCANNER_ADAPTER", "http")
		t.Setenv("MEDIA_SCANNER_URL", "http://127.0.0.1:19091/v1/scan")
		t.Setenv("MEDIA_SCANNER_TOKEN", "local-contract-token")
		cfg, err := Load()
		if err != nil || !cfg.MediaS3PathStyle || cfg.MediaStorageAdapter != "s3" || cfg.MediaScannerAdapter != "http" {
			t.Fatalf("valid loopback media contracts failed: cfg=%+v err=%v", cfg, err)
		}
	})
}

func clearProviderEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"OPENAI_ENABLED", "OPENAI_PAID_CALLS_APPROVED", "OPENAI_API_KEY", "OPENAI_BASE_URL",
		"OPENAI_CHAT_MODEL", "OPENAI_IMAGE_MODEL", "OPENAI_CHAT_MAX_OUTPUT_TOKENS",
		"OPENAI_IMAGE_SIZE", "OPENAI_IMAGE_QUALITY", "OPENAI_ORGANIZATION", "OPENAI_PROJECT", "OPENAI_RECONCILIATION_ENABLED", "OPENAI_RECONCILIATION_APPROVED", "OPENAI_ADMIN_API_KEY",
		"TRUSTED_PROXY_CIDRS",
		"VIDEO_ENABLED", "VIDEO_PAID_CALLS_APPROVED", "VIDEO_API_KEY", "VIDEO_BASE_URL", "VIDEO_MODEL", "VIDEO_POLL_INTERVAL_SECONDS", "VIDEO_TIMEOUT_SECONDS",
		"MUSIC_ENABLED", "MUSIC_PAID_CALLS_APPROVED", "MUSIC_API_KEY", "MUSIC_BASE_URL", "MUSIC_MODEL",
	} {
		t.Setenv(key, "")
	}
	for _, key := range []string{
		"STRIPE_ENABLED", "STRIPE_LIVE_MODE", "STRIPE_LIVE_MODE_APPROVED", "STRIPE_SECRET_KEY",
		"STRIPE_WEBHOOK_SECRET", "STRIPE_BASE_URL", "STRIPE_API_VERSION", "STRIPE_WEBHOOK_TOLERANCE_SECONDS",
	} {
		t.Setenv(key, "")
	}
	for _, key := range []string{
		"PAYMENT_PROVIDER", "WAFFO_ENABLED", "WAFFO_ENVIRONMENT", "WAFFO_PRODUCTION_APPROVED", "WAFFO_MERCHANT_ID", "WAFFO_STORE_ID",
		"WAFFO_CONNECTOR_URL", "WAFFO_CONNECTOR_TOKEN", "WAFFO_PRODUCT_ID_ONETIME", "WAFFO_PRODUCT_ID_SUBSCRIPTION",
	} {
		t.Setenv(key, "")
	}
	for _, key := range []string{
		"MEDIA_STORAGE_ADAPTER", "MEDIA_S3_BUCKET", "MEDIA_S3_REGION", "MEDIA_S3_ENDPOINT",
		"MEDIA_S3_ACCESS_KEY_ID", "MEDIA_S3_SECRET_ACCESS_KEY", "MEDIA_S3_SESSION_TOKEN", "MEDIA_S3_PATH_STYLE", "MEDIA_S3_PREFIX",
		"MEDIA_SCANNER_ADAPTER", "MEDIA_SCANNER_URL", "MEDIA_SCANNER_TOKEN", "MEDIA_SCANNER_TIMEOUT_SECONDS",
	} {
		t.Setenv(key, "")
	}
}

func setValidProductionEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://service:secret@db.example/hcai?sslmode=verify-full")
	t.Setenv("WEB_ORIGIN", "https://app.hcai.example")
	t.Setenv("LOCAL_PROVIDER_ENABLED", "false")
	t.Setenv("COOKIE_SECURE", "true")
	t.Setenv("WEBHOOK_ALLOW_LOCAL", "false")
	t.Setenv("WEBHOOK_ENCRYPTION_KEY_B64", productionTestKey(1))
	t.Setenv("EMAIL_ACTION_ENCRYPTION_KEY_B64", productionTestKey(2))
	t.Setenv("EMAIL_DELIVERY_MODE", "disabled")
	t.Setenv("MEDIA_STORAGE_ADAPTER", "s3")
	t.Setenv("MEDIA_S3_BUCKET", "hcai-production-test")
	t.Setenv("MEDIA_S3_REGION", "us-east-1")
	t.Setenv("MEDIA_S3_ACCESS_KEY_ID", "production-test-access")
	t.Setenv("MEDIA_S3_SECRET_ACCESS_KEY", "production-test-secret")
	t.Setenv("MEDIA_S3_PREFIX", "media")
	t.Setenv("MEDIA_SCANNER_ADAPTER", "http")
	t.Setenv("MEDIA_SCANNER_URL", "https://scanner.hcai.example/v1/scan")
	t.Setenv("MEDIA_SCANNER_TOKEN", "production-test-scanner-token")
}

func productionTestKey(fill byte) string {
	value := make([]byte, 32)
	for index := range value {
		value[index] = fill
	}
	return base64.StdEncoding.EncodeToString(value)
}
