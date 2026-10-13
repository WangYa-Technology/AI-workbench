// facilitycheck is an offline inventory, never an external acceptance runner.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
)

type facility struct {
	Name        string   `json:"name"`
	State       string   `json:"state"`
	Missing     []string `json:"missing,omitempty"`
	Unavailable []string `json:"unavailable,omitempty"`
	Gates       []string `json:"gates"`
}
type report struct {
	Status                   string     `json:"status"`
	ConfigurationValid       bool       `json:"configurationValid"`
	ExternalCalls            int        `json:"externalCalls"`
	ExternalAcceptancePassed bool       `json:"externalAcceptancePassed"`
	ErrorCode                string     `json:"errorCode,omitempty"`
	Facilities               []facility `json:"facilities"`
}

func inventory(c config.Config, getenv func(string) string) report {
	gates := []string{"explicit_task_authorization", "test_target_identity", "provider_permissions", "remote_contract_verification"}
	item := func(name, state string, required map[string]string) facility {
		f := facility{Name: name, State: state, Gates: append([]string(nil), gates...)}
		// Keep the output deterministic without exposing any configured value.
		for _, k := range requiredOrder {
			if v, ok := required[k]; ok && strings.TrimSpace(v) == "" {
				f.Missing = append(f.Missing, k)
			}
		}
		if state == "configured_unverified" && len(f.Missing) > 0 {
			f.State = "unavailable"
		}
		return f
	}
	stripeState := "disabled"
	if c.StripeEnabled {
		stripeState = "configured_unverified"
		if c.StripeLiveMode {
			stripeState = "live_mode_blocked"
		}
	}
	stripe := item("stripe", stripeState, map[string]string{"STRIPE_SECRET_KEY": c.StripeSecretKey, "STRIPE_WEBHOOK_SECRET": c.StripeWebhookSecret})
	waffoState := "disabled"
	if c.WaffoEnabled {
		waffoState = "configured_unverified"
		if c.WaffoEnvironment != "test" {
			waffoState = "live_mode_blocked"
		}
	}
	privateKey := getenv("WAFFO_PRIVATE_KEY") + getenv("WAFFO_PRIVATE_KEY_BASE64")
	waffo := item("waffo", waffoState, map[string]string{"WAFFO_MERCHANT_ID": c.WaffoMerchantID, "WAFFO_STORE_ID": c.WaffoStoreID, "WAFFO_PRODUCT_ID_ONETIME": c.WaffoProductIDOnetime, "WAFFO_CONNECTOR_TOKEN": c.WaffoConnectorToken, "WAFFO_PRIVATE_KEY_OR_BASE64": privateKey})
	waffo.Unavailable = []string{"seller_transfer", "bank_payout", "authenticated_refund_lookup", "pre_snapshot_identity_recovery"}
	waffo.Gates = append(waffo.Gates, "pinned_sdk_0_19_1", "merchant_graphql_schema")
	if strings.TrimSpace(getenv("WAFFO_CHECKOUT_LOOKUP_QUERY")) == "" {
		waffo.Unavailable = append(waffo.Unavailable, "checkout_lookup_not_configured")
	}
	bank := item("bank", stripeState, nil)
	if !c.StripeEnabled {
		bank.State = "unavailable"
	}
	bank.Gates = append(bank.Gates, "stripe_connected_test_account", "manual_payout_schedule", "verified_bank_target", "funded_test_balance", "independent_finance_approval")
	bank.Unavailable = []string{"waffo_bank_payout", "direct_bank_adapter"}
	storageState := "local_only"
	if c.MediaStorageAdapter == "s3" {
		storageState = "configured_unverified"
	}
	storage := item("object_storage", storageState, map[string]string{"MEDIA_S3_BUCKET": c.MediaS3Bucket, "MEDIA_S3_REGION": c.MediaS3Region, "MEDIA_S3_ACCESS_KEY_ID": c.MediaS3AccessKeyID, "MEDIA_S3_SECRET_ACCESS_KEY": c.MediaS3SecretAccessKey})
	storage.Gates = append(storage.Gates, "dedicated_test_bucket_prefix", "conditional_write_checksum_range", "bounded_cleanup_plan")
	scannerState := "local_only"
	if c.MediaScannerAdapter == "http" {
		scannerState = "configured_unverified"
	}
	scanner := item("media_scanner", scannerState, map[string]string{"MEDIA_SCANNER_URL": c.MediaScannerURL, "MEDIA_SCANNER_TOKEN": c.MediaScannerToken})
	scanner.Gates = append(scanner.Gates, "contract_v1_digest_binding", "vendor_engine_verdict_contract")
	smtpState := "disabled"
	if c.EmailDeliveryMode == "smtp" {
		smtpState = "configured_unverified"
	} else if c.EmailDeliveryMode == "local_file" {
		smtpState = "local_only"
	}
	smtp := item("smtp", smtpState, map[string]string{"SMTP_HOST": c.SMTPHost, "SMTP_USERNAME": c.SMTPUsername, "SMTP_PASSWORD": c.SMTPPassword, "SMTP_FROM": c.SMTPFrom})
	smtp.Gates = append(smtp.Gates, "approved_test_recipient", "implicit_tls_auth_login", "delivery_and_duplicate_policy")
	return report{Status: "offline_preflight_only", ConfigurationValid: true, Facilities: []facility{stripe, waffo, bank, storage, scanner, smtp}}
}

var requiredOrder = []string{"STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET", "WAFFO_MERCHANT_ID", "WAFFO_STORE_ID", "WAFFO_PRODUCT_ID_ONETIME", "WAFFO_CONNECTOR_TOKEN", "WAFFO_PRIVATE_KEY_OR_BASE64", "MEDIA_S3_BUCKET", "MEDIA_S3_REGION", "MEDIA_S3_ACCESS_KEY_ID", "MEDIA_S3_SECRET_ACCESS_KEY", "MEDIA_SCANNER_URL", "MEDIA_SCANNER_TOKEN", "SMTP_HOST", "SMTP_USERNAME", "SMTP_PASSWORD", "SMTP_FROM"}
var envName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// Parse literal dotenv assignments only. Never source a shell, expand a value,
// or include a failing line, path, configured identifier or secret in output.
func parseEnv(r io.Reader) (map[string]string, error) {
	body, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return nil, errors.New("env_file_invalid")
	}
	values := map[string]string{}
	s := bufio.NewScanner(strings.NewReader(string(body)))
	s.Buffer(make([]byte, 4096), 1<<20)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, v, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		v = strings.TrimSpace(v)
		if !ok || !envName.MatchString(key) || strings.ContainsRune(v, 0) {
			return nil, errors.New("env_file_invalid")
		}
		if _, exists := values[key]; exists {
			return nil, errors.New("env_file_invalid")
		}
		single := strings.HasPrefix(v, "'")
		if single || strings.HasPrefix(v, `"`) {
			if len(v) < 2 || v[len(v)-1] != v[0] {
				return nil, errors.New("env_file_invalid")
			}
			quote := v[0]
			v = v[1 : len(v)-1]
			if strings.ContainsRune(v, rune(quote)) {
				return nil, errors.New("env_file_invalid")
			}
		} else if strings.ContainsAny(v, " \t\"'") {
			return nil, errors.New("env_file_invalid")
		}
		if !single && strings.ContainsAny(v, "$`\\") {
			return nil, errors.New("env_file_invalid")
		}
		values[key] = v
	}
	if s.Err() != nil {
		return nil, errors.New("env_file_invalid")
	}
	return values, nil
}

func run(args []string, w io.Writer) int {
	result := report{Status: "blocked", Facilities: []facility{}}
	fail := func(code string) int { result.ErrorCode = code; _ = json.NewEncoder(w).Encode(result); return 1 }
	flags := flag.NewFlagSet("facilitycheck", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	file := flags.String("env-file", "", "explicit literal dotenv input; overrides process values; never executed")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return fail("arguments_invalid")
	}
	if *file != "" {
		f, err := os.Open(*file)
		if err != nil {
			return fail("env_file_unavailable")
		}
		values, err := parseEnv(f)
		_ = f.Close()
		if err != nil {
			return fail("env_file_invalid")
		}
		for k, v := range values {
			if os.Setenv(k, v) != nil {
				return fail("env_file_invalid")
			}
		}
	}
	// config.Load intentionally tolerates some malformed booleans with defaults;
	// an acceptance inventory must not silently classify those as disabled.
	for _, key := range []string{"STRIPE_ENABLED", "STRIPE_LIVE_MODE", "STRIPE_LIVE_MODE_APPROVED", "WAFFO_ENABLED", "WAFFO_PRODUCTION_APPROVED", "MEDIA_S3_PATH_STYLE"} {
		if v := os.Getenv(key); v != "" {
			if _, err := strconv.ParseBool(v); err != nil {
				return fail("configuration_invalid")
			}
		}
	}
	cfg, err := config.Load()
	if err != nil {
		return fail("configuration_invalid")
	}
	result = inventory(cfg, os.Getenv)
	if json.NewEncoder(w).Encode(result) != nil {
		return 1
	}
	return 0
}
func main() { os.Exit(run(os.Args[1:], os.Stdout)) }
