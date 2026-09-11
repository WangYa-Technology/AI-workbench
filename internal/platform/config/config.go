package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Environment                                string
	HTTPAddr                                   string
	DatabaseURL                                string
	MediaRoot                                  string
	MediaStorageAdapter                        string
	MediaS3Bucket                              string
	MediaS3Region                              string
	MediaS3Endpoint                            string
	MediaS3AccessKeyID                         string
	MediaS3SecretAccessKey                     string
	MediaS3SessionToken                        string
	MediaS3PathStyle                           bool
	MediaS3Prefix                              string
	MediaScannerAdapter                        string
	MediaScannerURL                            string
	MediaScannerToken                          string
	MediaScannerTimeoutSeconds                 int
	LocalProviderSource                        string
	WebOrigin                                  string
	TrustedProxyCIDRs                          []netip.Prefix
	LocalProviderEnabled                       bool
	DemoDataEnabled                            bool
	CookieSecure                               bool
	WebhookEncryptionKey                       []byte
	WebhookAllowLocal                          bool
	EmailDeliveryMode                          string
	EmailActionKey                             []byte
	OpenAIEnabled                              bool
	OpenAIPaidCallsApproved                    bool
	OpenAIAPIKey                               string
	OpenAIChatAPIKey                           string
	OpenAIImageAPIKey                          string
	OpenAIBaseURL                              string
	OpenAIChatAPI                              string
	OpenAIChatModel                            string
	OpenAIImageModel                           string
	OpenAIImageAsync                           bool
	OpenAIImagePollIntervalSeconds             int
	OpenAIImageTimeoutSeconds                  int
	OpenAIChatMaxOutputTokens                  int
	OpenAIImageSize                            string
	OpenAIImageQuality                         string
	OpenAIOrganization                         string
	OpenAIProject                              string
	OpenAIReconciliationEnabled                bool
	OpenAIReconciliationApproved               bool
	OpenAIAdminAPIKey                          string
	OpenAIReconciliationOverageThresholdMicros int64
	VideoEnabled                               bool
	VideoPaidCallsApproved                     bool
	VideoAPIKey                                string
	VideoBaseURL                               string
	VideoModel                                 string
	VideoPollIntervalSeconds                   int
	VideoTimeoutSeconds                        int
	MusicEnabled                               bool
	MusicPaidCallsApproved                     bool
	MusicAPIKey                                string
	MusicBaseURL                               string
	MusicModel                                 string
	StripeEnabled                              bool
	StripeLiveMode                             bool
	StripeLiveModeApproved                     bool
	StripeSecretKey                            string
	StripeWebhookSecret                        string
	StripeBaseURL                              string
	StripeAPIVersion                           string
	StripeWebhookToleranceSeconds              int
	PaymentProvider                            string
	WaffoEnabled                               bool
	WaffoEnvironment                           string
	WaffoProductionApproved                    bool
	WaffoMerchantID                            string
	WaffoStoreID                               string
	WaffoConnectorURL                          string
	WaffoConnectorToken                        string
	WaffoProductIDOnetime                      string
	WaffoProductIDSubscription                 string
}

func Load() (Config, error) {
	openAIEnabled, err := strictBoolean("OPENAI_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	openAIPaidCallsApproved, err := strictBoolean("OPENAI_PAID_CALLS_APPROVED", false)
	if err != nil {
		return Config{}, err
	}
	openAIChatMaxOutputTokens, err := integer("OPENAI_CHAT_MAX_OUTPUT_TOKENS", 2048)
	if err != nil {
		return Config{}, err
	}
	openAIImagePollIntervalSeconds, err := integer("OPENAI_IMAGE_POLL_INTERVAL_SECONDS", 10)
	if err != nil {
		return Config{}, err
	}
	openAIImageTimeoutSeconds, err := integer("OPENAI_IMAGE_TIMEOUT_SECONDS", 300)
	if err != nil {
		return Config{}, err
	}
	openAIReconciliationEnabled, err := strictBoolean("OPENAI_RECONCILIATION_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	openAIReconciliationApproved, err := strictBoolean("OPENAI_RECONCILIATION_APPROVED", false)
	if err != nil {
		return Config{}, err
	}
	openAIReconciliationOverageThresholdMicros, err := integer64("OPENAI_RECONCILIATION_OVERAGE_THRESHOLD_MICROS", 10_000)
	if err != nil {
		return Config{}, err
	}
	videoEnabled, err := strictBoolean("VIDEO_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	videoPaidCallsApproved, err := strictBoolean("VIDEO_PAID_CALLS_APPROVED", false)
	if err != nil {
		return Config{}, err
	}
	videoPollIntervalSeconds, err := integer("VIDEO_POLL_INTERVAL_SECONDS", 10)
	if err != nil {
		return Config{}, err
	}
	videoTimeoutSeconds, err := integer("VIDEO_TIMEOUT_SECONDS", 300)
	if err != nil {
		return Config{}, err
	}
	musicEnabled, err := strictBoolean("MUSIC_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	musicPaidCallsApproved, err := strictBoolean("MUSIC_PAID_CALLS_APPROVED", false)
	if err != nil {
		return Config{}, err
	}
	stripeEnabled, err := strictBoolean("STRIPE_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	stripeLiveMode, err := strictBoolean("STRIPE_LIVE_MODE", false)
	if err != nil {
		return Config{}, err
	}
	stripeLiveModeApproved, err := strictBoolean("STRIPE_LIVE_MODE_APPROVED", false)
	if err != nil {
		return Config{}, err
	}
	stripeWebhookToleranceSeconds, err := integer("STRIPE_WEBHOOK_TOLERANCE_SECONDS", 300)
	if err != nil {
		return Config{}, err
	}
	waffoEnabled, err := strictBoolean("WAFFO_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	waffoProductionApproved, err := strictBoolean("WAFFO_PRODUCTION_APPROVED", false)
	if err != nil {
		return Config{}, err
	}
	trustedProxyCIDRs, err := prefixes("TRUSTED_PROXY_CIDRS")
	if err != nil {
		return Config{}, err
	}
	mediaS3PathStyle, err := strictBoolean("MEDIA_S3_PATH_STYLE", false)
	if err != nil {
		return Config{}, err
	}
	mediaScannerTimeoutSeconds, err := integer("MEDIA_SCANNER_TIMEOUT_SECONDS", 30)
	if err != nil {
		return Config{}, err
	}
	openAIAPIKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	openAIChatAPIKey := strings.TrimSpace(os.Getenv("OPENAI_CHAT_API_KEY"))
	openAIImageAPIKey := strings.TrimSpace(os.Getenv("OPENAI_IMAGE_API_KEY"))
	if openAIChatAPIKey == "" {
		openAIChatAPIKey = openAIAPIKey
	}
	if openAIImageAPIKey == "" {
		openAIImageAPIKey = openAIAPIKey
	}
	cfg := Config{
		Environment:                    value("APP_ENV", "development"),
		HTTPAddr:                       value("HTTP_ADDR", ":8080"),
		DatabaseURL:                    value("DATABASE_URL", "postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable"),
		MediaRoot:                      value("MEDIA_ROOT", "./data/media"),
		MediaStorageAdapter:            value("MEDIA_STORAGE_ADAPTER", "local_file"),
		MediaS3Bucket:                  strings.TrimSpace(os.Getenv("MEDIA_S3_BUCKET")),
		MediaS3Region:                  strings.TrimSpace(os.Getenv("MEDIA_S3_REGION")),
		MediaS3Endpoint:                strings.TrimSpace(os.Getenv("MEDIA_S3_ENDPOINT")),
		MediaS3AccessKeyID:             strings.TrimSpace(os.Getenv("MEDIA_S3_ACCESS_KEY_ID")),
		MediaS3SecretAccessKey:         strings.TrimSpace(os.Getenv("MEDIA_S3_SECRET_ACCESS_KEY")),
		MediaS3SessionToken:            strings.TrimSpace(os.Getenv("MEDIA_S3_SESSION_TOKEN")),
		MediaS3PathStyle:               mediaS3PathStyle,
		MediaS3Prefix:                  strings.Trim(strings.TrimSpace(os.Getenv("MEDIA_S3_PREFIX")), "/"),
		MediaScannerAdapter:            value("MEDIA_SCANNER_ADAPTER", "local_deterministic"),
		MediaScannerURL:                strings.TrimSpace(os.Getenv("MEDIA_SCANNER_URL")),
		MediaScannerToken:              strings.TrimSpace(os.Getenv("MEDIA_SCANNER_TOKEN")),
		MediaScannerTimeoutSeconds:     mediaScannerTimeoutSeconds,
		LocalProviderSource:            value("LOCAL_PROVIDER_SOURCE", "./web/public/media/home-cinematic.jpg"),
		WebOrigin:                      value("WEB_ORIGIN", "http://localhost:5173"),
		TrustedProxyCIDRs:              trustedProxyCIDRs,
		LocalProviderEnabled:           boolean("LOCAL_PROVIDER_ENABLED", true),
		DemoDataEnabled:                boolean("DEMO_DATA_ENABLED", false),
		CookieSecure:                   boolean("COOKIE_SECURE", false),
		WebhookAllowLocal:              boolean("WEBHOOK_ALLOW_LOCAL", true),
		EmailDeliveryMode:              value("EMAIL_DELIVERY_MODE", emailDeliveryDefault(value("APP_ENV", "development"))),
		OpenAIEnabled:                  openAIEnabled,
		OpenAIPaidCallsApproved:        openAIPaidCallsApproved,
		OpenAIAPIKey:                   openAIAPIKey,
		OpenAIChatAPIKey:               openAIChatAPIKey,
		OpenAIImageAPIKey:              openAIImageAPIKey,
		OpenAIBaseURL:                  value("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		OpenAIChatAPI:                  value("OPENAI_CHAT_API", "responses"),
		OpenAIChatModel:                value("OPENAI_CHAT_MODEL", "gpt-5.6-terra"),
		OpenAIImageModel:               value("OPENAI_IMAGE_MODEL", "gpt-image-2"),
		OpenAIImageAsync:               boolean("OPENAI_IMAGE_ASYNC", false),
		OpenAIImagePollIntervalSeconds: openAIImagePollIntervalSeconds,
		OpenAIImageTimeoutSeconds:      openAIImageTimeoutSeconds,
		OpenAIChatMaxOutputTokens:      openAIChatMaxOutputTokens,
		OpenAIImageSize:                value("OPENAI_IMAGE_SIZE", "1024x1024"),
		OpenAIImageQuality:             value("OPENAI_IMAGE_QUALITY", "medium"),
		OpenAIOrganization:             strings.TrimSpace(os.Getenv("OPENAI_ORGANIZATION")),
		OpenAIProject:                  strings.TrimSpace(os.Getenv("OPENAI_PROJECT")),
		OpenAIReconciliationEnabled:    openAIReconciliationEnabled,
		OpenAIReconciliationApproved:   openAIReconciliationApproved,
		OpenAIAdminAPIKey:              strings.TrimSpace(os.Getenv("OPENAI_ADMIN_API_KEY")),
		OpenAIReconciliationOverageThresholdMicros: openAIReconciliationOverageThresholdMicros,
		VideoEnabled:                  videoEnabled,
		VideoPaidCallsApproved:        videoPaidCallsApproved,
		VideoAPIKey:                   strings.TrimSpace(os.Getenv("VIDEO_API_KEY")),
		VideoBaseURL:                  value("VIDEO_BASE_URL", "https://ark.ap-southeast.bytepluses.com/api/v3"),
		VideoModel:                    value("VIDEO_MODEL", "dreamina-seedance-2-0-fast-260128"),
		VideoPollIntervalSeconds:      videoPollIntervalSeconds,
		VideoTimeoutSeconds:           videoTimeoutSeconds,
		MusicEnabled:                  musicEnabled,
		MusicPaidCallsApproved:        musicPaidCallsApproved,
		MusicAPIKey:                   strings.TrimSpace(os.Getenv("MUSIC_API_KEY")),
		MusicBaseURL:                  value("MUSIC_BASE_URL", "https://api.minimaxi.com/v1"),
		MusicModel:                    value("MUSIC_MODEL", "music-3.0"),
		StripeEnabled:                 stripeEnabled,
		StripeLiveMode:                stripeLiveMode,
		StripeLiveModeApproved:        stripeLiveModeApproved,
		StripeSecretKey:               strings.TrimSpace(os.Getenv("STRIPE_SECRET_KEY")),
		StripeWebhookSecret:           strings.TrimSpace(os.Getenv("STRIPE_WEBHOOK_SECRET")),
		StripeBaseURL:                 value("STRIPE_BASE_URL", "https://api.stripe.com/v1"),
		StripeAPIVersion:              value("STRIPE_API_VERSION", "2026-02-25.clover"),
		StripeWebhookToleranceSeconds: stripeWebhookToleranceSeconds,
		PaymentProvider:               value("PAYMENT_PROVIDER", "stripe"),
		WaffoEnabled:                  waffoEnabled,
		WaffoEnvironment:              value("WAFFO_ENVIRONMENT", "test"),
		WaffoProductionApproved:       waffoProductionApproved,
		WaffoMerchantID:               strings.TrimSpace(os.Getenv("WAFFO_MERCHANT_ID")),
		WaffoStoreID:                  strings.TrimSpace(os.Getenv("WAFFO_STORE_ID")),
		WaffoConnectorURL:             value("WAFFO_CONNECTOR_URL", "http://127.0.0.1:8091"),
		WaffoConnectorToken:           strings.TrimSpace(os.Getenv("WAFFO_CONNECTOR_TOKEN")),
		WaffoProductIDOnetime:         strings.TrimSpace(os.Getenv("WAFFO_PRODUCT_ID_ONETIME")),
		WaffoProductIDSubscription:    strings.TrimSpace(os.Getenv("WAFFO_PRODUCT_ID_SUBSCRIPTION")),
	}
	webhookKey := strings.TrimSpace(os.Getenv("WEBHOOK_ENCRYPTION_KEY_B64"))
	if webhookKey == "" && cfg.Environment != "production" {
		fallback := sha256.Sum256([]byte("hcai-chat-deterministic-local-test-webhook-key"))
		cfg.WebhookEncryptionKey = fallback[:]
	} else if decoded, err := base64.StdEncoding.DecodeString(webhookKey); err != nil || len(decoded) != 32 {
		return Config{}, fmt.Errorf("WEBHOOK_ENCRYPTION_KEY_B64 must decode to exactly 32 bytes")
	} else {
		cfg.WebhookEncryptionKey = decoded
	}
	emailActionKey := strings.TrimSpace(os.Getenv("EMAIL_ACTION_ENCRYPTION_KEY_B64"))
	if emailActionKey == "" && cfg.Environment != "production" {
		fallback := sha256.Sum256([]byte("hcai-chat-deterministic-local-test-email-action-key"))
		cfg.EmailActionKey = fallback[:]
	} else if decoded, err := base64.StdEncoding.DecodeString(emailActionKey); err != nil || len(decoded) != 32 {
		return Config{}, fmt.Errorf("EMAIL_ACTION_ENCRYPTION_KEY_B64 must decode to exactly 32 bytes")
	} else {
		cfg.EmailActionKey = decoded
	}
	if cfg.EmailDeliveryMode != "local_file" && cfg.EmailDeliveryMode != "disabled" {
		return Config{}, fmt.Errorf("EMAIL_DELIVERY_MODE must be local_file or disabled")
	}
	if err := validateOpenAI(&cfg); err != nil {
		return Config{}, err
	}
	if err := validateCreativeProviders(&cfg); err != nil {
		return Config{}, err
	}
	if err := validateStripe(&cfg); err != nil {
		return Config{}, err
	}
	if err := validatePayment(&cfg); err != nil {
		return Config{}, err
	}
	if err := validateMedia(&cfg); err != nil {
		return Config{}, err
	}

	if cfg.Environment == "production" && cfg.LocalProviderEnabled {
		return Config{}, fmt.Errorf("LOCAL_PROVIDER_ENABLED must be false in production")
	}
	if cfg.Environment == "production" && !cfg.CookieSecure {
		return Config{}, fmt.Errorf("COOKIE_SECURE must be true in production")
	}
	if cfg.Environment == "production" && cfg.WebhookAllowLocal {
		return Config{}, fmt.Errorf("WEBHOOK_ALLOW_LOCAL must be false in production")
	}
	if cfg.Environment == "production" && cfg.EmailDeliveryMode != "disabled" {
		return Config{}, fmt.Errorf("EMAIL_DELIVERY_MODE must be disabled in production until an approved provider is implemented")
	}
	if cfg.Environment == "production" {
		if err := validateProduction(cfg); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

func validateProduction(cfg Config) error {
	databaseURL, err := url.Parse(cfg.DatabaseURL)
	if err != nil || !oneOf(databaseURL.Scheme, "postgres", "postgresql") || databaseURL.Host == "" || databaseURL.User == nil || strings.Trim(databaseURL.Path, "/") == "" {
		return fmt.Errorf("DATABASE_URL must be an absolute PostgreSQL URL with user, host, and database in production")
	}
	if strings.Contains(strings.ToUpper(cfg.DatabaseURL), "REPLACE_") || databaseURL.Query().Get("sslmode") != "verify-full" {
		return fmt.Errorf("DATABASE_URL must replace all placeholders and use sslmode=verify-full in production")
	}
	webOrigin, err := url.Parse(cfg.WebOrigin)
	if err != nil || webOrigin.Scheme != "https" || webOrigin.Host == "" || webOrigin.User != nil || webOrigin.RawQuery != "" || webOrigin.Fragment != "" ||
		(webOrigin.Path != "" && webOrigin.Path != "/") || isLoopbackHost(webOrigin.Hostname()) || strings.Contains(strings.ToUpper(cfg.WebOrigin), "REPLACE_") {
		return fmt.Errorf("WEB_ORIGIN must be a public HTTPS origin without credentials, path, query, fragment, or placeholders in production")
	}
	if bytes.Equal(cfg.WebhookEncryptionKey, cfg.EmailActionKey) {
		return fmt.Errorf("WEBHOOK_ENCRYPTION_KEY_B64 and EMAIL_ACTION_ENCRYPTION_KEY_B64 must use independent keys in production")
	}
	return nil
}

func validateMedia(cfg *Config) error {
	if !oneOf(cfg.MediaStorageAdapter, "local_file", "s3") {
		return fmt.Errorf("MEDIA_STORAGE_ADAPTER must be local_file or s3")
	}
	if !oneOf(cfg.MediaScannerAdapter, "local_deterministic", "http") {
		return fmt.Errorf("MEDIA_SCANNER_ADAPTER must be local_deterministic or http")
	}
	if cfg.MediaScannerTimeoutSeconds < 1 || cfg.MediaScannerTimeoutSeconds > 120 {
		return fmt.Errorf("MEDIA_SCANNER_TIMEOUT_SECONDS must be between 1 and 120")
	}
	for key, candidate := range map[string]string{
		"MEDIA_S3_ACCESS_KEY_ID": cfg.MediaS3AccessKeyID, "MEDIA_S3_SECRET_ACCESS_KEY": cfg.MediaS3SecretAccessKey,
		"MEDIA_S3_SESSION_TOKEN": cfg.MediaS3SessionToken, "MEDIA_SCANNER_TOKEN": cfg.MediaScannerToken,
	} {
		if strings.ContainsAny(candidate, "\r\n") {
			return fmt.Errorf("%s cannot contain line breaks", key)
		}
	}
	if cfg.MediaStorageAdapter == "s3" {
		if cfg.Environment == "production" && containsPlaceholder(
			cfg.MediaS3Bucket, cfg.MediaS3Region, cfg.MediaS3Endpoint, cfg.MediaS3AccessKeyID,
			cfg.MediaS3SecretAccessKey, cfg.MediaS3SessionToken, cfg.MediaS3Prefix,
		) {
			return fmt.Errorf("production S3 media configuration must replace all placeholders")
		}
		if cfg.MediaS3Bucket == "" || cfg.MediaS3Region == "" || cfg.MediaS3AccessKeyID == "" || cfg.MediaS3SecretAccessKey == "" {
			return fmt.Errorf("MEDIA_S3_BUCKET, MEDIA_S3_REGION, MEDIA_S3_ACCESS_KEY_ID, and MEDIA_S3_SECRET_ACCESS_KEY are required for s3 storage")
		}
		if len(cfg.MediaS3Bucket) < 3 || len(cfg.MediaS3Bucket) > 63 || strings.ContainsAny(cfg.MediaS3Bucket, "/\\\r\n") {
			return fmt.Errorf("MEDIA_S3_BUCKET must be a valid 3-63 character bucket name")
		}
		if len(cfg.MediaS3Prefix) > 256 || strings.Contains(cfg.MediaS3Prefix, "..") || strings.ContainsAny(cfg.MediaS3Prefix, "\\\r\n") {
			return fmt.Errorf("MEDIA_S3_PREFIX must be a safe path prefix")
		}
		if cfg.MediaS3Endpoint != "" {
			endpoint, err := url.Parse(cfg.MediaS3Endpoint)
			if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
				return fmt.Errorf("MEDIA_S3_ENDPOINT must be an absolute origin without credentials, path, query, or fragment")
			}
			if endpoint.Scheme != "https" && (endpoint.Scheme != "http" || !isLoopbackHost(endpoint.Hostname()) || cfg.Environment == "production") {
				return fmt.Errorf("MEDIA_S3_ENDPOINT must use HTTPS, except for loopback development tests")
			}
			cfg.MediaS3Endpoint = strings.TrimRight(endpoint.String(), "/")
		}
	}
	if cfg.MediaScannerAdapter == "http" {
		if cfg.Environment == "production" && containsPlaceholder(cfg.MediaScannerURL, cfg.MediaScannerToken) {
			return fmt.Errorf("production media scanner configuration must replace all placeholders")
		}
		if len(cfg.MediaScannerToken) < 16 {
			return fmt.Errorf("MEDIA_SCANNER_TOKEN must contain at least 16 characters for the HTTP scanner")
		}
		endpoint, err := url.Parse(cfg.MediaScannerURL)
		if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
			return fmt.Errorf("MEDIA_SCANNER_URL must be an absolute URL without credentials, query, or fragment")
		}
		if endpoint.Scheme != "https" && (endpoint.Scheme != "http" || !isLoopbackHost(endpoint.Hostname()) || cfg.Environment == "production") {
			return fmt.Errorf("MEDIA_SCANNER_URL must use HTTPS, except for loopback development tests")
		}
		cfg.MediaScannerURL = endpoint.String()
	}
	if cfg.Environment == "production" && cfg.MediaStorageAdapter != "s3" {
		return fmt.Errorf("MEDIA_STORAGE_ADAPTER must be s3 in production")
	}
	if cfg.Environment == "production" && cfg.MediaScannerAdapter != "http" {
		return fmt.Errorf("MEDIA_SCANNER_ADAPTER must be http in production")
	}
	return nil
}

func containsPlaceholder(values ...string) bool {
	for _, value := range values {
		if strings.Contains(strings.ToUpper(value), "REPLACE_") {
			return true
		}
	}
	return false
}

func validateStripe(cfg *Config) error {
	if cfg.StripeWebhookToleranceSeconds < 60 || cfg.StripeWebhookToleranceSeconds > 900 {
		return fmt.Errorf("STRIPE_WEBHOOK_TOLERANCE_SECONDS must be between 60 and 900")
	}
	for key, candidate := range map[string]string{
		"STRIPE_SECRET_KEY": cfg.StripeSecretKey, "STRIPE_WEBHOOK_SECRET": cfg.StripeWebhookSecret, "STRIPE_API_VERSION": cfg.StripeAPIVersion,
	} {
		if strings.ContainsAny(candidate, "\r\n") {
			return fmt.Errorf("%s cannot contain line breaks", key)
		}
	}
	if len(cfg.StripeAPIVersion) < 10 || len(cfg.StripeAPIVersion) > 40 {
		return fmt.Errorf("STRIPE_API_VERSION must be a pinned Stripe API version")
	}
	for _, char := range cfg.StripeAPIVersion {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' && char != '.' {
			return fmt.Errorf("STRIPE_API_VERSION must be a pinned Stripe API version")
		}
	}
	parsed, err := url.Parse(cfg.StripeBaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("STRIPE_BASE_URL must be an absolute URL without credentials, query, or fragment")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("STRIPE_BASE_URL must use HTTPS or loopback HTTP")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return fmt.Errorf("STRIPE_BASE_URL HTTP is allowed only for loopback development tests")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.Path == "" {
		parsed.Path = "/v1"
	}
	parsed.RawPath = ""
	cfg.StripeBaseURL = strings.TrimRight(parsed.String(), "/")
	if cfg.Environment == "production" && cfg.StripeBaseURL != "https://api.stripe.com/v1" {
		return fmt.Errorf("STRIPE_BASE_URL must be https://api.stripe.com/v1 in production")
	}
	if cfg.StripeLiveMode && !cfg.StripeLiveModeApproved {
		return fmt.Errorf("STRIPE_LIVE_MODE_APPROVED must be true before STRIPE_LIVE_MODE can be enabled")
	}
	if cfg.StripeEnabled {
		if cfg.StripeSecretKey == "" || cfg.StripeWebhookSecret == "" {
			return fmt.Errorf("STRIPE_SECRET_KEY and STRIPE_WEBHOOK_SECRET are required when STRIPE_ENABLED is true")
		}
		keyPrefix := "sk_test_"
		if cfg.StripeLiveMode {
			keyPrefix = "sk_live_"
		}
		if !strings.HasPrefix(cfg.StripeSecretKey, keyPrefix) || len(cfg.StripeSecretKey) < len(keyPrefix)+8 {
			return fmt.Errorf("STRIPE_SECRET_KEY does not match the configured test/live mode")
		}
		if !strings.HasPrefix(cfg.StripeWebhookSecret, "whsec_") || len(cfg.StripeWebhookSecret) < 14 {
			return fmt.Errorf("STRIPE_WEBHOOK_SECRET must be a Stripe endpoint secret")
		}
	}
	if cfg.Environment == "production" && cfg.StripeEnabled {
		if !cfg.StripeLiveMode || !cfg.StripeLiveModeApproved {
			return fmt.Errorf("production Stripe enablement requires explicitly approved live mode")
		}
		webOrigin, err := url.Parse(cfg.WebOrigin)
		if err != nil || webOrigin.Scheme != "https" || webOrigin.Host == "" || isLoopbackHost(webOrigin.Hostname()) {
			return fmt.Errorf("WEB_ORIGIN must be a public HTTPS origin when Stripe is enabled in production")
		}
	}
	return nil
}

func validatePayment(cfg *Config) error {
	cfg.PaymentProvider = strings.ToLower(strings.TrimSpace(cfg.PaymentProvider))
	if !oneOf(cfg.PaymentProvider, "stripe", "waffo_pancake", "epay") {
		return fmt.Errorf("PAYMENT_PROVIDER must be stripe, waffo_pancake, or epay")
	}
	cfg.WaffoEnvironment = strings.ToLower(strings.TrimSpace(cfg.WaffoEnvironment))
	if !oneOf(cfg.WaffoEnvironment, "test", "prod") {
		return fmt.Errorf("WAFFO_ENVIRONMENT must be test or prod")
	}
	for key, candidate := range map[string]string{
		"WAFFO_MERCHANT_ID": cfg.WaffoMerchantID, "WAFFO_STORE_ID": cfg.WaffoStoreID,
		"WAFFO_CONNECTOR_TOKEN": cfg.WaffoConnectorToken, "WAFFO_PRODUCT_ID_ONETIME": cfg.WaffoProductIDOnetime,
		"WAFFO_PRODUCT_ID_SUBSCRIPTION": cfg.WaffoProductIDSubscription,
	} {
		if strings.ContainsAny(candidate, "\r\n") {
			return fmt.Errorf("%s cannot contain line breaks", key)
		}
	}
	connector, err := url.Parse(strings.TrimRight(strings.TrimSpace(cfg.WaffoConnectorURL), "/"))
	if err != nil || connector.Host == "" || connector.User != nil || connector.RawQuery != "" || connector.Fragment != "" || !oneOf(connector.Scheme, "http", "https") {
		return fmt.Errorf("WAFFO_CONNECTOR_URL must be an absolute URL without credentials, query, or fragment")
	}
	if connector.Scheme == "http" && (cfg.WaffoEnabled && (cfg.Environment == "production" || !isLoopbackHost(connector.Hostname()))) {
		return fmt.Errorf("WAFFO_CONNECTOR_URL HTTP is allowed only for loopback development tests")
	}
	cfg.WaffoConnectorURL = strings.TrimRight(connector.String(), "/")
	if cfg.WaffoEnabled {
		if cfg.PaymentProvider != "waffo_pancake" {
			return fmt.Errorf("PAYMENT_PROVIDER must be waffo_pancake when WAFFO_ENABLED is true")
		}
		if cfg.WaffoMerchantID == "" || cfg.WaffoConnectorToken == "" {
			return fmt.Errorf("WAFFO_MERCHANT_ID and WAFFO_CONNECTOR_TOKEN are required when WAFFO_ENABLED is true")
		}
		if len(cfg.WaffoConnectorToken) < 16 {
			return fmt.Errorf("WAFFO_CONNECTOR_TOKEN must contain at least 16 characters")
		}
	}
	if cfg.WaffoEnvironment == "prod" && !cfg.WaffoProductionApproved {
		return fmt.Errorf("WAFFO_PRODUCTION_APPROVED must be true before WAFFO prod mode can be enabled")
	}
	if cfg.Environment == "production" && cfg.WaffoEnabled {
		if cfg.WaffoEnvironment != "prod" || cfg.WaffoConnectorURL == "" || !strings.HasPrefix(cfg.WaffoConnectorURL, "https://") {
			return fmt.Errorf("production Waffo enablement requires approved prod mode and an HTTPS connector")
		}
	}
	return nil
}

func validateOpenAI(cfg *Config) error {
	if cfg.OpenAIChatMaxOutputTokens < 1 || cfg.OpenAIChatMaxOutputTokens > 32768 {
		return fmt.Errorf("OPENAI_CHAT_MAX_OUTPUT_TOKENS must be between 1 and 32768")
	}
	if !oneOf(cfg.OpenAIChatAPI, "responses", "chat_completions") {
		return fmt.Errorf("OPENAI_CHAT_API must be responses or chat_completions")
	}
	if cfg.OpenAIImagePollIntervalSeconds < 1 || cfg.OpenAIImagePollIntervalSeconds > 60 {
		return fmt.Errorf("OPENAI_IMAGE_POLL_INTERVAL_SECONDS must be between 1 and 60")
	}
	if cfg.OpenAIImageTimeoutSeconds < 30 || cfg.OpenAIImageTimeoutSeconds > 600 || cfg.OpenAIImagePollIntervalSeconds >= cfg.OpenAIImageTimeoutSeconds {
		return fmt.Errorf("OPENAI_IMAGE_TIMEOUT_SECONDS must be between 30 and 600 and greater than OPENAI_IMAGE_POLL_INTERVAL_SECONDS")
	}
	if !oneOf(cfg.OpenAIImageSize, "1024x1024", "1536x1024", "1024x1536", "auto") {
		return fmt.Errorf("OPENAI_IMAGE_SIZE must be 1024x1024, 1536x1024, 1024x1536, or auto")
	}
	if !oneOf(cfg.OpenAIImageQuality, "low", "medium", "high", "auto") {
		return fmt.Errorf("OPENAI_IMAGE_QUALITY must be low, medium, high, or auto")
	}
	for key, candidate := range map[string]string{
		"OPENAI_API_KEY": cfg.OpenAIAPIKey, "OPENAI_CHAT_API_KEY": cfg.OpenAIChatAPIKey, "OPENAI_IMAGE_API_KEY": cfg.OpenAIImageAPIKey,
		"OPENAI_ORGANIZATION": cfg.OpenAIOrganization, "OPENAI_PROJECT": cfg.OpenAIProject, "OPENAI_ADMIN_API_KEY": cfg.OpenAIAdminAPIKey,
	} {
		if strings.ContainsAny(candidate, "\r\n") {
			return fmt.Errorf("%s cannot contain line breaks", key)
		}
	}
	parsed, err := url.Parse(cfg.OpenAIBaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("OPENAI_BASE_URL must be an absolute URL without credentials, query, or fragment")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("OPENAI_BASE_URL must use HTTPS or loopback HTTP")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return fmt.Errorf("OPENAI_BASE_URL HTTP is allowed only for loopback development tests")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.Path == "" {
		parsed.Path = "/v1"
	}
	parsed.RawPath = ""
	cfg.OpenAIBaseURL = strings.TrimRight(parsed.String(), "/")
	if cfg.Environment == "production" && cfg.OpenAIBaseURL != "https://api.openai.com/v1" {
		return fmt.Errorf("OPENAI_BASE_URL must be https://api.openai.com/v1 in production")
	}
	if cfg.OpenAIEnabled {
		if !cfg.OpenAIPaidCallsApproved {
			return fmt.Errorf("OPENAI_PAID_CALLS_APPROVED must be true before OPENAI_ENABLED can be enabled")
		}
		if cfg.OpenAIChatAPIKey == "" || cfg.OpenAIImageAPIKey == "" {
			return fmt.Errorf("OPENAI_API_KEY or both OPENAI_CHAT_API_KEY and OPENAI_IMAGE_API_KEY are required when OPENAI_ENABLED is true")
		}
		if cfg.OpenAIChatModel == "" || cfg.OpenAIImageModel == "" {
			return fmt.Errorf("OPENAI_CHAT_MODEL and OPENAI_IMAGE_MODEL are required when OPENAI_ENABLED is true")
		}
	}
	if cfg.OpenAIReconciliationEnabled {
		if !cfg.OpenAIReconciliationApproved {
			return fmt.Errorf("OPENAI_RECONCILIATION_APPROVED must be true before reconciliation can be enabled")
		}
		if cfg.OpenAIAdminAPIKey == "" {
			return fmt.Errorf("OPENAI_ADMIN_API_KEY is required when reconciliation is enabled")
		}
		if cfg.OpenAIProject == "" {
			return fmt.Errorf("OPENAI_PROJECT is required when reconciliation is enabled")
		}
	}
	if cfg.OpenAIReconciliationOverageThresholdMicros < 0 || cfg.OpenAIReconciliationOverageThresholdMicros > 1_000_000_000_000 {
		return fmt.Errorf("OPENAI_RECONCILIATION_OVERAGE_THRESHOLD_MICROS must be between 0 and 1000000000000")
	}
	return nil
}

func validateCreativeProviders(cfg *Config) error {
	for key, candidate := range map[string]string{
		"VIDEO_API_KEY": cfg.VideoAPIKey,
		"VIDEO_MODEL":   cfg.VideoModel,
		"MUSIC_API_KEY": cfg.MusicAPIKey,
		"MUSIC_MODEL":   cfg.MusicModel,
	} {
		if strings.ContainsAny(candidate, "\r\n") {
			return fmt.Errorf("%s cannot contain line breaks", key)
		}
	}
	if cfg.VideoPollIntervalSeconds < 1 || cfg.VideoPollIntervalSeconds > 60 {
		return fmt.Errorf("VIDEO_POLL_INTERVAL_SECONDS must be between 1 and 60")
	}
	if cfg.VideoTimeoutSeconds < 30 || cfg.VideoTimeoutSeconds > 600 || cfg.VideoPollIntervalSeconds >= cfg.VideoTimeoutSeconds {
		return fmt.Errorf("VIDEO_TIMEOUT_SECONDS must be between 30 and 600 and greater than VIDEO_POLL_INTERVAL_SECONDS")
	}
	for key, raw := range map[string]string{"VIDEO_BASE_URL": cfg.VideoBaseURL, "MUSIC_BASE_URL": cfg.MusicBaseURL} {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("%s must be an absolute URL without credentials, query, or fragment", key)
		}
		if parsed.Scheme != "https" && (parsed.Scheme != "http" || !isLoopbackHost(parsed.Hostname())) {
			return fmt.Errorf("%s must use HTTPS or loopback HTTP", key)
		}
		parsed.Path = strings.TrimRight(parsed.Path, "/")
		parsed.RawPath = ""
		if key == "VIDEO_BASE_URL" {
			cfg.VideoBaseURL = parsed.String()
		} else {
			cfg.MusicBaseURL = parsed.String()
		}
	}
	if cfg.Environment == "production" {
		if cfg.VideoBaseURL != "https://ark.ap-southeast.bytepluses.com/api/v3" {
			return fmt.Errorf("VIDEO_BASE_URL must be the official BytePlus ModelArk endpoint in production")
		}
		if cfg.MusicBaseURL != "https://api.minimaxi.com/v1" {
			return fmt.Errorf("MUSIC_BASE_URL must be the official MiniMax endpoint in production")
		}
	}
	if cfg.VideoEnabled {
		if !cfg.VideoPaidCallsApproved {
			return fmt.Errorf("VIDEO_PAID_CALLS_APPROVED must be true before VIDEO_ENABLED can be enabled")
		}
		if cfg.VideoAPIKey == "" || cfg.VideoModel == "" {
			return fmt.Errorf("VIDEO_API_KEY and VIDEO_MODEL are required when VIDEO_ENABLED is true")
		}
	}
	if cfg.MusicEnabled {
		if !cfg.MusicPaidCallsApproved {
			return fmt.Errorf("MUSIC_PAID_CALLS_APPROVED must be true before MUSIC_ENABLED can be enabled")
		}
		if cfg.MusicAPIKey == "" || cfg.MusicModel == "" {
			return fmt.Errorf("MUSIC_API_KEY and MUSIC_MODEL are required when MUSIC_ENABLED is true")
		}
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func emailDeliveryDefault(environment string) string {
	if environment == "production" {
		return "disabled"
	}
	return "local_file"
}

func value(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func boolean(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "true", "1", "yes", "enabled":
		return true
	case "false", "0", "no", "disabled":
		return false
	default:
		return fallback
	}
}

func strictBoolean(key string, fallback bool) (bool, error) {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return fallback, nil
	}
	switch raw {
	case "true", "1", "yes", "enabled":
		return true, nil
	case "false", "0", "no", "disabled":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true or false", key)
	}
}

func integer(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	result, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	return result, nil
}

func integer64(key string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	result, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	return result, nil
}

func prefixes(key string) ([]netip.Prefix, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil, nil
	}
	items := strings.Split(raw, ",")
	result := make([]netip.Prefix, 0, len(items))
	for _, item := range items {
		value := strings.TrimSpace(item)
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix.Bits() == 0 {
			return nil, fmt.Errorf("%s must contain non-zero CIDR prefixes", key)
		}
		result = append(result, prefix.Masked())
	}
	return result, nil
}
