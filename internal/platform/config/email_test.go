package config

import "testing"

func TestSMTPConfiguration(t *testing.T) {
	clearProviderEnv(t)
	setValidProductionEnv(t)
	t.Setenv("EMAIL_DELIVERY_MODE", "smtp")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "465")
	t.Setenv("SMTP_USERNAME", "support@example.com")
	t.Setenv("SMTP_PASSWORD", "test-secret")
	t.Setenv("SMTP_FROM", "support@example.com")
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ key, value string }{
		{"SMTP_HOST", ""}, {"SMTP_HOST", "smtp://example.com"}, {"SMTP_PORT", "0"}, {"SMTP_PORT", "invalid"},
		{"SMTP_USERNAME", ""}, {"SMTP_PASSWORD", ""}, {"SMTP_FROM", "invalid"}, {"SMTP_FROM", "a@example.com\r\nBcc: x@example.com"},
		{"EMAIL_ACTION_ENCRYPTION_KEY_B64", ""},
	} {
		t.Run(item.key+item.value, func(t *testing.T) {
			t.Setenv(item.key, item.value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid SMTP configuration accepted")
			}
		})
	}
}
