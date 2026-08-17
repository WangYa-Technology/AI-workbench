package config

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Environment          string
	HTTPAddr             string
	DatabaseURL          string
	MediaRoot            string
	LocalProviderSource  string
	WebOrigin            string
	LocalProviderEnabled bool
	DemoDataEnabled      bool
	CookieSecure         bool
	WebhookEncryptionKey []byte
	WebhookAllowLocal    bool
	EmailDeliveryMode    string
	EmailActionKey       []byte
}

func Load() (Config, error) {
	cfg := Config{
		Environment:          value("APP_ENV", "development"),
		HTTPAddr:             value("HTTP_ADDR", ":8080"),
		DatabaseURL:          value("DATABASE_URL", "postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable"),
		MediaRoot:            value("MEDIA_ROOT", "./data/media"),
		LocalProviderSource:  value("LOCAL_PROVIDER_SOURCE", "./web/public/media/home-cinematic.jpg"),
		WebOrigin:            value("WEB_ORIGIN", "http://localhost:5173"),
		LocalProviderEnabled: boolean("LOCAL_PROVIDER_ENABLED", true),
		DemoDataEnabled:      boolean("DEMO_DATA_ENABLED", false),
		CookieSecure:         boolean("COOKIE_SECURE", false),
		WebhookAllowLocal:    boolean("WEBHOOK_ALLOW_LOCAL", true),
		EmailDeliveryMode:    value("EMAIL_DELIVERY_MODE", emailDeliveryDefault(value("APP_ENV", "development"))),
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
	return cfg, nil
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
