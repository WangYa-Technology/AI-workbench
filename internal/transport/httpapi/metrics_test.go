package httpapi_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/observability"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestMetricsEndpointExposesSafeOperationalSeries(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO jobs(kind,status,created_at,available_at)
		VALUES('identity.email_action.expire','queued',now()-interval '2 days',now()+interval '1 hour')`); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	response, err := http.Get(server.URL + "/api/v1")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	response, err = http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("metrics response mismatch: status=%d contentType=%q body=%s", response.StatusCode, response.Header.Get("Content-Type"), text)
	}
	for _, required := range []string{
		"# TYPE hcai_database_ready gauge",
		"hcai_database_ready 1",
		"# TYPE hcai_http_requests_total counter",
		"hcai_http_requests_total{status_class=\"2xx\"}",
		"# TYPE hcai_jobs_total gauge",
		"hcai_jobs_total{status=\"queued\"} 1",
		"# HELP hcai_jobs_oldest_queued_age_seconds Age of the oldest runnable queued job.",
		"hcai_jobs_oldest_queued_age_seconds 0.000000",
		"hcai_jobs_expired_leases 0",
		"hcai_jobs_oldest_expired_lease_age_seconds 0.000000",
		"hcai_jobs_invalid_leases 0",
		`hcai_product_webhook_quarantines{mode="live"} 0`,
		`hcai_product_webhook_quarantine_oldest_age_seconds{mode="test"} 0.000000`,
		"hcai_generation_output_cleanup_failed 0",
		"hcai_upload_write_cleanup_failed 0",
		"hcai_upload_write_cleanup_due 0",
		"hcai_upload_write_cleanup_oldest_due_age_seconds 0.000000",
		"hcai_upload_write_pending 0",
		"hcai_upload_write_oldest_pending_age_seconds 0.000000",
		"hcai_upload_write_pending_invalid_timestamps 0",
		"hcai_generation_output_pending 0",
		"hcai_generation_output_oldest_pending_age_seconds 0.000000",
		"hcai_generation_output_pending_invalid_timestamps 0",
		"hcai_asset_scan_pending 0",
		"hcai_asset_scan_oldest_pending_age_seconds 0.000000",
		"hcai_asset_scan_recovery_failed 0",
		"hcai_asset_scan_recovery_due 0",
		"hcai_asset_scan_recovery_unresolved 0",
		"hcai_asset_scan_recovery_oldest_due_age_seconds 0.000000",
		"hcai_generation_recovery_failed 0",
		"hcai_generation_recovery_due 0",
		"hcai_generation_recovery_unresolved 0",
		"hcai_generation_recovery_oldest_due_age_seconds 0.000000",
		"hcai_generation_output_cleanup_due 0",
		"hcai_generation_output_cleanup_oldest_due_age_seconds 0.000000",
		"# TYPE hcai_job_attempts_total gauge",
		"# TYPE hcai_recovery_jobs_failed gauge",
		"hcai_recovery_jobs_failed{kind=\"media_product\"} 0",
		"hcai_recovery_jobs_failed{kind=\"media_account\"} 0",
		"hcai_recovery_jobs_failed{kind=\"export_export\"} 0",
		"hcai_recovery_jobs_failed{kind=\"export_expiry\"} 0",
		"hcai_recovery_jobs_failed{kind=\"account_deletion\"} 0",
		"hcai_product_payment_backlog{kind=\"checkout_pending\",mode=\"live\"} 0",
		"hcai_product_payment_oldest_age_seconds{kind=\"refund_unresolved\",mode=\"test\"} 0.000000",
		"hcai_product_payment_problems{kind=\"event_failed\",mode=\"live\"} 0",
		`hcai_product_payment_problems{kind="checkout_evidence_conflict",mode="live"} 0`,
		`hcai_product_payment_problems{kind="checkout_evidence_conflict",mode="test"} 0`,
		`hcai_product_payment_problems{kind="closed_checkout_paid",mode="live"} 0`,
		`hcai_product_payment_problems{kind="closed_checkout_paid",mode="test"} 0`,
		`hcai_product_payment_problems{kind="checkout_check_stopped",mode="live"} 0`,
		`hcai_product_payment_problems{kind="checkout_check_stopped",mode="test"} 0`,
		`hcai_product_payment_problems{kind="checkout_check_missing",mode="live"} 0`,
		`hcai_product_payment_problems{kind="refund_observation_unresolved",mode="live"} 0`,
		`hcai_product_payment_problems{kind="refund_observation_unresolved",mode="test"} 0`,
		`hcai_product_payment_backlog{kind="settlement_due",mode="live"} 0`,
		`hcai_product_payment_backlog{kind="settlement_due",mode="test"} 0`,
		`hcai_product_payment_backlog{kind="settlement_unresolved",mode="live"} 0`,
		`hcai_product_payment_backlog{kind="settlement_unresolved",mode="test"} 0`,
		`hcai_product_payment_oldest_age_seconds{kind="settlement_due",mode="live"} 0.000000`,
		`hcai_product_payment_oldest_age_seconds{kind="settlement_due",mode="test"} 0.000000`,
		`hcai_product_payment_oldest_age_seconds{kind="settlement_unresolved",mode="live"} 0.000000`,
		`hcai_product_payment_oldest_age_seconds{kind="settlement_unresolved",mode="test"} 0.000000`,
		`hcai_product_payment_problems{kind="settlement_missing",mode="live"} 0`,
		`hcai_product_payment_problems{kind="settlement_missing",mode="test"} 0`,
		`hcai_product_payment_problems{kind="settlement_check_stopped",mode="live"} 0`,
		`hcai_product_payment_problems{kind="settlement_check_stopped",mode="test"} 0`,
		`hcai_product_payment_backlog{kind="seller_funding_unresolved",mode="live"} 0`,
		`hcai_product_payment_backlog{kind="seller_funding_unresolved",mode="test"} 0`,
		`hcai_product_payment_backlog{kind="seller_funding_check_due",mode="live"} 0`,
		`hcai_product_payment_backlog{kind="seller_funding_check_due",mode="test"} 0`,
		`hcai_product_payment_oldest_age_seconds{kind="seller_funding_unresolved",mode="live"} 0.000000`,
		`hcai_product_payment_oldest_age_seconds{kind="seller_funding_unresolved",mode="test"} 0.000000`,
		`hcai_product_payment_oldest_age_seconds{kind="seller_funding_check_due",mode="live"} 0.000000`,
		`hcai_product_payment_oldest_age_seconds{kind="seller_funding_check_due",mode="test"} 0.000000`,
		`hcai_product_payment_problems{kind="seller_funding_dispatch_missing",mode="live"} 0`,
		`hcai_product_payment_problems{kind="seller_funding_dispatch_missing",mode="test"} 0`,
		`hcai_product_payment_problems{kind="seller_funding_admission_missing",mode="live"} 0`,
		`hcai_product_payment_problems{kind="seller_funding_admission_missing",mode="test"} 0`,
		`hcai_product_payment_problems{kind="seller_funding_stopped",mode="live"} 0`,
		`hcai_product_payment_problems{kind="seller_funding_stopped",mode="test"} 0`,
		`hcai_product_payment_problems{kind="seller_funding_review",mode="live"} 0`,
		`hcai_product_payment_problems{kind="seller_funding_review",mode="test"} 0`,
		`hcai_product_payment_problems{kind="seller_funding_read_unrecorded",mode="live"} 0`,
		`hcai_product_payment_problems{kind="seller_funding_read_unrecorded",mode="test"} 0`,
		"hcai_maintenance_success_seen{kind=\"legal_hold_expiry\"} 0",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("metrics response omitted %q: %s", required, text)
		}
	}
	for _, forbidden := range []string{"request_id", "prompt", "email", "user_id", "provider_api_key"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("metrics response exposed forbidden field %q: %s", forbidden, text)
		}
	}
}

func TestMetricsRecoveryFailureAlertsAndQueryFailure(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	for _, kind := range []string{observability.LegalHoldExpiry, observability.LegalHoldCleanup, observability.ProductCleanupReconciliation, observability.AccountDeletionReconciliation, observability.OriginalMediaCleanupReconciliation, observability.ProductRefundReconciliation, observability.ProductCheckoutReconciliation, observability.ProductSettlementReconciliation, observability.SellerFundingReconciliation, observability.SellerBankReconciliation, observability.GenerationOutputCleanup, observability.AssetScanExecutionRecovery, observability.UploadWriteCleanup} {
		if err := observability.NewRepository(pool).RecordMaintenance(t.Context(), kind, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO jobs(kind,status,created_at,updated_at,last_error)
 VALUES('product.delivery_cleanup','failed',now()-interval '3 days',now()-interval '2 days','private storage key')`); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	check := func(want string, success bool) {
		t.Helper()
		command := exec.Command("bash", "../../../scripts/metrics-alert-check.sh")
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "ALERT_") && !strings.HasPrefix(value, "METRICS_URL=") {
				command.Env = append(command.Env, value)
			}
		}
		command.Env = append(command.Env, "METRICS_URL="+server.URL+"/metrics")
		output, err := command.CombinedOutput()
		if (err == nil) != success || !strings.Contains(string(output), want) {
			t.Fatalf("check err=%v output=%s want=%s", err, output, want)
		}
		if strings.Contains(string(output), "private storage key") {
			t.Fatal("private failure text leaked")
		}
	}
	// A newly deployed recovery scanner has no successful heartbeat yet. Its
	// alert must remain visible alongside the existing failed cleanup job.
	check("ALERT generation_execution_recovery_never_succeeded recovery_job_failures_exceeded", false)
	if err := observability.NewRepository(pool).RecordMaintenance(t.Context(), observability.GenerationExecutionRecovery, true); err != nil {
		t.Fatal(err)
	}
	check("ALERT recovery_job_failures_exceeded", false)
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET status='succeeded' WHERE kind='product.delivery_cleanup'`); err != nil {
		t.Fatal(err)
	}
	check("ok ", true)
	// A successful recovery heartbeat must not hide unbound pending business data.
	scanOwner, scanAsset := uuid.New(), uuid.New()
	if _, err := pool.Exec(t.Context(), `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Private scan owner','creator')`, scanOwner, scanOwner.String()+"@test.local", "metrics_"+scanOwner.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,source_type,scan_status,license_code,storage_backend,storage_key,created_at)
 VALUES($1,$2,'document','private scan title','/private','text/plain','upload','pending','hcai-commercial-standard-v1','local_file','private scan object',now()-interval '20 minutes')`, scanAsset, scanOwner); err != nil {
		t.Fatal(err)
	}
	check("ALERT asset_scan_recovery_unresolved asset_scan_pending_overdue", false)
	response, err := http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	rendered, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != 200 {
		t.Fatal(response.StatusCode, readErr)
	}
	for _, secret := range []string{scanOwner.String(), scanAsset.String(), "private scan title", "private scan object"} {
		if strings.Contains(string(rendered), secret) {
			t.Fatalf("scan metrics leaked %q", secret)
		}
	}
	if _, err := pool.Exec(t.Context(), `UPDATE assets SET scan_status='review' WHERE id=$1`, scanAsset); err != nil {
		t.Fatal(err)
	}
	check("ok ", true)
	// Healthy maintenance heartbeats also cannot hide abandoned uploads. Expose
	// only aggregate counts, never the private object key or checksum.
	uploadID := uuid.New()
	uploadKey := "upload-" + uploadID.String() + ".txt"
	if _, err := pool.Exec(t.Context(), `INSERT INTO upload_writes(id,owner_id,asset_id,storage_backend,storage_key,checksum_sha256,size_bytes,next_check_at,last_error_code)
 VALUES($1,$2,$3,'local_file',$4,repeat('a',64),32,now()-interval '10 minutes','upload_write_cleanup_failed')`, uploadID, scanOwner, uuid.New(), uploadKey); err != nil {
		t.Fatal(err)
	}
	check("ALERT upload_write_cleanup_failed upload_write_cleanup_overdue", false)
	response, err = http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	rendered, readErr = io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != 200 {
		t.Fatal(response.StatusCode, readErr)
	}
	for _, secret := range []string{uploadID.String(), uploadKey, scanOwner.String(), strings.Repeat("a", 64)} {
		if strings.Contains(string(rendered), secret) {
			t.Fatal("upload metrics exposed private evidence")
		}
	}
	if _, err := pool.Exec(t.Context(), `UPDATE upload_writes SET last_error_code=NULL,next_check_at=now()+interval '1 hour' WHERE id=$1`, uploadID); err != nil {
		t.Fatal(err)
	}
	check("ok ", true)
	if _, err := pool.Exec(t.Context(), `ALTER TABLE upload_writes RENAME TO upload_metrics_unavailable`); err != nil {
		t.Fatal(err)
	}
	check("ALERT metrics endpoint unavailable", false)
	if _, err := pool.Exec(t.Context(), `ALTER TABLE upload_metrics_unavailable RENAME TO upload_writes`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE asset_scan_executions RENAME TO scan_metrics_unavailable`); err != nil {
		t.Fatal(err)
	}
	check("ALERT metrics endpoint unavailable", false)
	if _, err := pool.Exec(t.Context(), `ALTER TABLE scan_metrics_unavailable RENAME TO asset_scan_executions`); err != nil {
		t.Fatal(err)
	}

	repository := jobs.NewRepository(pool)
	id, err := repository.Enqueue(t.Context(), "metrics.lease", nil)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := repository.Claim(t.Context(), "metrics-worker", time.Minute)
	if err != nil || claimed.ID != id {
		t.Fatal(claimed, err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET lease_expires_at=now()-interval '2 minutes' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	check("ALERT expired_job_lease_age_exceeded", false)
	if err := repository.RecoverExpired(t.Context()); err != nil {
		t.Fatal(err)
	}
	check("ok ", true)
	if _, err := pool.Exec(t.Context(), `INSERT INTO jobs(kind,status,lease_expires_at) VALUES('metrics.missing_evidence','running','infinity')`); err != nil {
		t.Fatal(err)
	}
	check("ALERT invalid_job_leases", false)
	// A failed SQL projection must not produce a healthy partial scrape.
	if _, err := pool.Exec(t.Context(), `DROP VIEW data_export_job_policy`); err != nil {
		t.Fatal(err)
	}
	check("ALERT metrics endpoint unavailable", false)
}
