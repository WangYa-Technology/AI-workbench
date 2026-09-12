package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	testStripeWebhookSecret = "whsec_contract_signing_secret"
	testStripeAPIVersion    = "2026-02-25.clover"
)

func TestStripeWebhookVerifierSupportsRotatedSignaturesAndTolerance(t *testing.T) {
	now := time.Date(2026, time.August, 18, 4, 45, 0, 0, time.UTC)
	body := []byte(`{"id":"evt_contract123"}`)
	verifier := NewStripeWebhookVerifier(testStripeWebhookSecret, 5*time.Minute)
	verifier.now = func() time.Time { return now }
	valid := stripeSignature(testStripeWebhookSecret, now.Unix(), body)
	header := "t=" + fmt.Sprint(now.Unix()) + ",v1=" + strings.Repeat("0", 64) + ",v0=ignored,v1=" + valid
	if err := verifier.Verify(body, header); err != nil {
		t.Fatalf("valid rotated signature was rejected: %v", err)
	}
	for name, candidate := range map[string]string{
		"wrong signature": "t=" + fmt.Sprint(now.Unix()) + ",v1=" + strings.Repeat("1", 64),
		"expired":         "t=" + fmt.Sprint(now.Add(-6*time.Minute).Unix()) + ",v1=" + valid,
		"duplicate time":  "t=" + fmt.Sprint(now.Unix()) + ",t=" + fmt.Sprint(now.Unix()) + ",v1=" + valid,
		"malformed":       "t=abc,v1=xyz",
	} {
		t.Run(name, func(t *testing.T) {
			if err := verifier.Verify(body, candidate); !errors.Is(err, ErrInvalidSignature) {
				t.Fatalf("invalid signature header was accepted: %v", err)
			}
		})
	}
}

func TestPaymentWebhookReceiptIsMinimizedAndIdempotent(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	now := time.Now().UTC().Truncate(time.Second)
	service := NewService(pool, ServiceConfig{Enabled: true, LiveMode: false, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute})
	service.verifier.now = func() time.Time { return now }
	body := stripeCheckoutEvent("evt_contract123", "checkout.session.completed", now.Unix(), 1250)
	header := "t=" + fmt.Sprint(now.Unix()) + ",v1=" + stripeSignature(testStripeWebhookSecret, now.Unix(), body)

	receipt, err := service.ReceiveStripeWebhook(context.Background(), body, header)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Duplicate || receipt.Status != "received" || receipt.EventType != "checkout.session.completed" {
		t.Fatalf("unexpected first receipt: %#v", receipt)
	}
	duplicate, err := service.ReceiveStripeWebhook(context.Background(), body, header)
	if err != nil || !duplicate.Duplicate || duplicate.EventID != receipt.EventID || duplicate.Status != "received" {
		t.Fatalf("duplicate receipt mismatch: %#v err=%v", duplicate, err)
	}

	var count, amount int
	var paymentID, resourceID uuid.UUID
	var purpose, currency, providerPaymentID, payloadHash string
	if err := pool.QueryRow(context.Background(), `SELECT count(*)::int FROM payment_provider_events WHERE provider='stripe' AND provider_event_id='evt_contract123'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT payment_id,resource_id,purpose,amount_cents,currency,provider_payment_id,payload_sha256
		FROM payment_provider_events WHERE provider='stripe' AND provider_event_id='evt_contract123'`).Scan(
		&paymentID, &resourceID, &purpose, &amount, &currency, &providerPaymentID, &payloadHash,
	); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(body)
	if count != 1 || paymentID != testPaymentID || resourceID != testResourceID || purpose != "product" || amount != 1250 || currency != "USD" || providerPaymentID != "pi_contract123" || payloadHash != hex.EncodeToString(hash[:]) {
		t.Fatalf("minimized event evidence mismatch: count=%d payment=%s resource=%s purpose=%s amount=%d currency=%s providerPayment=%s hash=%s", count, paymentID, resourceID, purpose, amount, currency, providerPaymentID, payloadHash)
	}
	var unsafeColumns int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='payment_provider_events'
		  AND column_name IN ('payload','raw_body','customer_email','card','secret')`).Scan(&unsafeColumns); err != nil || unsafeColumns != 0 {
		t.Fatalf("payment event table exposes unsafe payload columns: count=%d err=%v", unsafeColumns, err)
	}

	changed := stripeCheckoutEvent("evt_contract123", "checkout.session.completed", now.Unix(), 1300)
	changedHeader := "t=" + fmt.Sprint(now.Unix()) + ",v1=" + stripeSignature(testStripeWebhookSecret, now.Unix(), changed)
	if _, err := service.ReceiveStripeWebhook(context.Background(), changed, changedHeader); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("same provider event ID with different bytes did not fail closed: %v", err)
	}
}

func TestPaymentWebhookIgnoresUnsupportedAndRejectsModeOrVersionMismatch(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	now := time.Now().UTC().Truncate(time.Second)
	service := NewService(pool, ServiceConfig{Enabled: true, LiveMode: false, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute})
	service.verifier.now = func() time.Time { return now }
	body := stripeCheckoutEvent("evt_unsupported123", "checkout.session.expired", now.Unix(), 1250)
	header := "t=" + fmt.Sprint(now.Unix()) + ",v1=" + stripeSignature(testStripeWebhookSecret, now.Unix(), body)
	receipt, err := service.ReceiveStripeWebhook(context.Background(), body, header)
	if err != nil || receipt.Status != "ignored" {
		t.Fatalf("unsupported valid event was not safely acknowledged: %#v err=%v", receipt, err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE payment_provider_event_processing SET status='received',processed_at=NULL WHERE event_id=$1`, receipt.EventID); err == nil {
		t.Fatal("terminal ignored processing evidence accepted mutation")
	}

	wrongVersion := []byte(strings.Replace(string(body), testStripeAPIVersion, "2025-01-01.acacia", 1))
	if _, err := service.ReceiveStripeWebhook(context.Background(), wrongVersion, "t="+fmt.Sprint(now.Unix())+",v1="+stripeSignature(testStripeWebhookSecret, now.Unix(), wrongVersion)); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("API-version mismatch was accepted: %v", err)
	}
	wrongMode := []byte(strings.Replace(string(body), `"livemode":false`, `"livemode":true`, 1))
	if _, err := service.ReceiveStripeWebhook(context.Background(), wrongMode, "t="+fmt.Sprint(now.Unix())+",v1="+stripeSignature(testStripeWebhookSecret, now.Unix(), wrongMode)); !errors.Is(err, ErrModeMismatch) {
		t.Fatalf("live/test mismatch was accepted: %v", err)
	}
	disabled := NewService(pool, ServiceConfig{})
	if _, err := disabled.ReceiveStripeWebhook(context.Background(), body, header); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled payment boundary accepted a webhook: %v", err)
	}
}

func stripeCheckoutEvent(eventID, eventType string, created int64, amount int) []byte {
	return []byte(fmt.Sprintf(`{"id":%q,"object":"event","api_version":%q,"created":%d,"livemode":false,"type":%q,"data":{"object":{"id":"cs_test_contract","object":"checkout.session","status":"complete","payment_status":"paid","amount_total":%d,"currency":"usd","payment_intent":"pi_contract123","customer_email":"must-not-persist@example.com","metadata":{"hcai_payment_id":%q,"hcai_resource_id":%q,"hcai_purpose":"product","untrusted_extra":"must-not-persist"}}}}`, eventID, testStripeAPIVersion, created, eventType, amount, testPaymentID.String(), testResourceID.String()))
}

func stripeSignature(secret string, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.", timestamp)
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func paymentTestPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		baseURL = "postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable"
	}
	root, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		testutil.DatabaseUnavailable(t, err)
	}
	if err := root.Ping(ctx); err != nil {
		root.Close()
		testutil.DatabaseUnavailable(t, err)
	}
	schema := "test_payments_" + uuid.NewString()[:8]
	if _, err := root.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		root.Close()
		t.Fatal(err)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	pool, err := database.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		_, _ = root.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		root.Close()
	}
}
