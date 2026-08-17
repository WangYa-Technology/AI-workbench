package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestProductionFailsClosed(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("LOCAL_PROVIDER_ENABLED", "true")
	t.Setenv("COOKIE_SECURE", "true")
	if _, err := Load(); err == nil {
		t.Fatal("expected production local provider configuration to fail")
	}
}

func TestDevelopmentDefaults(t *testing.T) {
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
	if cfg.EmailDeliveryMode != "local_file" || len(cfg.EmailActionKey) != 32 {
		t.Fatalf("unexpected development email defaults: mode=%q key=%d", cfg.EmailDeliveryMode, len(cfg.EmailActionKey))
	}
}

func TestProductionRequiresWebhookEncryptionKeyAndPublicTargets(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("LOCAL_PROVIDER_ENABLED", "false")
	t.Setenv("COOKIE_SECURE", "true")
	t.Setenv("WEBHOOK_ALLOW_LOCAL", "false")
	t.Setenv("WEBHOOK_ENCRYPTION_KEY_B64", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "WEBHOOK_ENCRYPTION_KEY_B64") {
		t.Fatalf("missing production Webhook key did not fail closed: %v", err)
	}
	t.Setenv("WEBHOOK_ENCRYPTION_KEY_B64", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("EMAIL_ACTION_ENCRYPTION_KEY_B64", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("EMAIL_DELIVERY_MODE", "disabled")
	if _, err := Load(); err != nil {
		t.Fatalf("valid production Webhook boundary failed: %v", err)
	}
}

func TestProductionIdentityEmailFailsClosed(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("LOCAL_PROVIDER_ENABLED", "false")
	t.Setenv("COOKIE_SECURE", "true")
	t.Setenv("WEBHOOK_ALLOW_LOCAL", "false")
	t.Setenv("WEBHOOK_ENCRYPTION_KEY_B64", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("EMAIL_ACTION_ENCRYPTION_KEY_B64", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("EMAIL_DELIVERY_MODE", "local_file")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "EMAIL_DELIVERY_MODE") {
		t.Fatalf("production local mailbox mode did not fail closed: %v", err)
	}
}
