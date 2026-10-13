package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
)

func TestInventoryNeverClaimsRemoteAcceptance(t *testing.T) {
	secret := "DO_NOT_PRINT_PRIVATE_ACCOUNT_OR_SECRET"
	c := config.Config{StripeEnabled: true, StripeSecretKey: secret, StripeWebhookSecret: secret,
		WaffoEnabled: true, WaffoEnvironment: "test", WaffoMerchantID: secret, WaffoStoreID: secret, WaffoProductIDOnetime: secret, WaffoConnectorToken: secret,
		MediaStorageAdapter: "s3", MediaS3Bucket: secret, MediaS3Region: secret, MediaS3AccessKeyID: secret, MediaS3SecretAccessKey: secret,
		MediaScannerAdapter: "http", MediaScannerURL: secret, MediaScannerToken: secret,
		EmailDeliveryMode: "smtp", SMTPHost: secret, SMTPUsername: secret, SMTPPassword: secret, SMTPFrom: secret}
	r := inventory(c, func(string) string { return secret })
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), secret) || r.ExternalCalls != 0 || r.ExternalAcceptancePassed || len(r.Facilities) != 6 {
		t.Fatalf("unsafe report: %s", raw)
	}
	for _, f := range r.Facilities {
		if f.State != "configured_unverified" || len(f.Gates) == 0 {
			t.Fatalf("incorrect readiness: %#v", f)
		}
	}
	c.StripeLiveMode = true
	c.WaffoEnvironment = "prod"
	r = inventory(c, func(string) string { return secret })
	for _, i := range []int{0, 1, 2} {
		if r.Facilities[i].State != "live_mode_blocked" {
			t.Fatal("live financial capability was not blocked")
		}
	}
}

func TestMissingWaffoAndBankCapabilitiesRemainUnavailable(t *testing.T) {
	r := inventory(config.Config{WaffoEnabled: true, WaffoEnvironment: "test"}, func(string) string { return "" })
	if r.Facilities[1].State != "unavailable" || r.Facilities[2].State != "unavailable" {
		t.Fatalf("incomplete provider enabled: %#v", r)
	}
	raw, _ := json.Marshal(r.Facilities[1])
	for _, code := range []string{"bank_payout", "authenticated_refund_lookup", "checkout_lookup_not_configured", "WAFFO_PRIVATE_KEY_OR_BASE64"} {
		if !strings.Contains(string(raw), code) {
			t.Fatalf("missing blocker %s", code)
		}
	}
}

func TestLiteralEnvRejectsExecutionAndDoesNotApplyPartialInput(t *testing.T) {
	for _, input := range []string{"SMTP_PASSWORD=$(touch forbidden)", "SMTP_PASSWORD=`echo secret`", "SMTP_PASSWORD=\"${SECRET}\"", "SMTP_PASSWORD=ok\nSMTP_PASSWORD=duplicate", "export BAD-NAME=secret", "SMTP_PASSWORD=\"unterminated"} {
		if _, err := parseEnv(strings.NewReader(input)); err == nil {
			t.Fatalf("accepted executable/ambiguous syntax: %q", input)
		}
	}
	values, err := parseEnv(strings.NewReader("export SMTP_PASSWORD='literal$not_expanded'\n# comment\nSMTP_PORT=465\n"))
	if err != nil || values["SMTP_PASSWORD"] != "literal$not_expanded" {
		t.Fatal("single quoted literal not preserved")
	}
	t.Setenv("SMTP_PASSWORD", "original")
	path := filepath.Join(t.TempDir(), "private.env")
	if err := os.WriteFile(path, []byte("SMTP_PASSWORD=TOP_SECRET_VALUE\nINVALID LINE"), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if run([]string{"-env-file", path}, &output) != 1 || os.Getenv("SMTP_PASSWORD") != "original" {
		t.Fatal("failed parse changed environment")
	}
	if strings.Contains(output.String(), "TOP_SECRET_VALUE") || strings.Contains(output.String(), path) {
		t.Fatal("private input exposed")
	}
}

func TestOfflineCLIWithConfiguredEndpointsMakesNoRequests(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	// Pin each externally active facility to a counting loopback endpoint. No
	// acceptance gate is inferred even when legacy opt-in variables are present.
	for k, v := range map[string]string{
		"APP_ENV": "test", "STRIPE_ENABLED": "true", "STRIPE_SECRET_KEY": "sk_test_12345678", "STRIPE_WEBHOOK_SECRET": "whsec_12345678_secret", "STRIPE_LIVE_MODE": "false", "STRIPE_LIVE_MODE_APPROVED": "false", "STRIPE_BASE_URL": server.URL, "PAYMENT_PROVIDER": "stripe", "WAFFO_ENABLED": "false",
		"MEDIA_STORAGE_ADAPTER": "s3", "MEDIA_S3_BUCKET": "private-bucket", "MEDIA_S3_REGION": "us-east-1", "MEDIA_S3_ENDPOINT": server.URL, "MEDIA_S3_ACCESS_KEY_ID": "private-key", "MEDIA_S3_SECRET_ACCESS_KEY": "private-secret", "MEDIA_S3_PATH_STYLE": "true",
		"MEDIA_SCANNER_ADAPTER": "http", "MEDIA_SCANNER_URL": server.URL, "MEDIA_SCANNER_TOKEN": "private-token-12345678", "EMAIL_DELIVERY_MODE": "disabled",
		"STRIPE_ACCEPTANCE_CONFIRM": "I_APPROVE_STRIPE_STAGING_CALLS", "STRIPE_ACCEPTANCE_MAX_CALLS": "6", "MEDIA_ACCEPTANCE_CONFIRM": "I_APPROVE_MEDIA_ACCEPTANCE_CALLS",
	} {
		t.Setenv(k, v)
	}
	var output bytes.Buffer
	if code := run(nil, &output); code != 0 {
		t.Fatalf("preflight failed: %s", output.String())
	}
	if calls.Load() != 0 {
		t.Fatal("offline inventory contacted a provider")
	}
	var result report
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ExternalAcceptancePassed || result.ExternalCalls != 0 {
		t.Fatal("incorrect remote success")
	}
	for _, private := range []string{server.URL, "private-bucket", "private-key", "private-secret", "private-token-12345678", "sk_test_12345678"} {
		if strings.Contains(output.String(), private) {
			t.Fatal("configuration value leaked")
		}
	}
	t.Setenv("STRIPE_ENABLED", "typo")
	output.Reset()
	if run(nil, &output) != 1 || !strings.Contains(output.String(), "configuration_invalid") {
		t.Fatal("ambiguous enable flag accepted")
	}
}
