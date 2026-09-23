package observability

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestMetricsAlertScript(t *testing.T) {
	healthy := "hcai_database_ready 1\nhcai_audit_chain_valid 1\nhcai_jobs_oldest_queued_age_seconds 0.000000\n"
	healthy += "hcai_jobs_expired_leases 0\nhcai_jobs_oldest_expired_lease_age_seconds 0.000000\nhcai_jobs_invalid_leases 0\n"
	healthy += "hcai_generation_output_cleanup_failed 0\nhcai_generation_output_cleanup_due 0\nhcai_generation_output_cleanup_oldest_due_age_seconds 0.000000\n"
	healthy += "hcai_upload_write_cleanup_failed 0\nhcai_upload_write_cleanup_due 0\nhcai_upload_write_cleanup_oldest_due_age_seconds 0.000000\n"
	for _, kind := range []string{"generation_output", "upload_write"} {
		healthy += fmt.Sprintf("hcai_%s_pending 0\nhcai_%s_oldest_pending_age_seconds 0.000000\nhcai_%s_pending_invalid_timestamps 0\n", kind, kind, kind)
	}
	healthy += "hcai_generation_recovery_failed 0\nhcai_generation_recovery_due 0\nhcai_generation_recovery_unresolved 0\nhcai_generation_recovery_oldest_due_age_seconds 0.000000\n"
	healthy += "hcai_asset_scan_pending 0\nhcai_asset_scan_oldest_pending_age_seconds 0.000000\nhcai_asset_scan_recovery_failed 0\nhcai_asset_scan_recovery_due 0\nhcai_asset_scan_recovery_unresolved 0\nhcai_asset_scan_recovery_oldest_due_age_seconds 0.000000\n"
	for _, kind := range []string{"account_deletion", "export_expiry", "export_export", "media_account", "media_product"} {
		healthy += fmt.Sprintf("hcai_recovery_jobs_failed{kind=%q} 0\n", kind)
	}
	for _, mode := range []string{"live", "test"} {
		healthy += fmt.Sprintf("hcai_product_webhook_quarantines{mode=%q} 0\nhcai_product_webhook_quarantine_oldest_age_seconds{mode=%q} 0.000000\n", mode, mode)
		for _, kind := range []string{"checkout_pending", "checkout_expired", "refund_unresolved", "refund_check_due", "settlement_due", "settlement_unresolved", "seller_funding_unresolved", "seller_funding_check_due", "seller_bank_unresolved", "seller_bank_check_due", "seller_reversal_unresolved", "seller_reversal_closure_due"} {
			healthy += fmt.Sprintf("hcai_product_payment_backlog{kind=%q,mode=%q} 0\n", kind, mode)
			healthy += fmt.Sprintf("hcai_product_payment_oldest_age_seconds{kind=%q,mode=%q} 0.000000\n", kind, mode)
		}
		for _, kind := range []string{"event_failed", "checkout_check_missing", "checkout_check_stopped", "refund_check_stopped", "refund_observation_unresolved", "refund_read_unrecorded", "refund_evidence_missing", "checkout_evidence_missing", "checkout_evidence_conflict", "closed_checkout_paid", "settlement_missing", "settlement_check_stopped", "seller_funding_dispatch_missing", "seller_funding_admission_missing", "seller_funding_stopped", "seller_funding_review", "seller_funding_read_unrecorded", "seller_bank_failed", "seller_bank_review", "seller_bank_read_unrecorded", "seller_bank_dispatch_stopped", "seller_reversal_review", "seller_reversal_read_unrecorded", "seller_reversal_stopped", "invalid_timestamp"} {
			healthy += fmt.Sprintf("hcai_product_payment_problems{kind=%q,mode=%q} 0\n", kind, mode)
		}
	}
	for _, kind := range maintenanceKinds {
		healthy += fmt.Sprintf("hcai_maintenance_success_seen{kind=%q} 1\nhcai_maintenance_last_pass_failed{kind=%q} 0\nhcai_maintenance_invalid_timestamps{kind=%q} 0\nhcai_maintenance_last_success_age_seconds{kind=%q} 0.000000\n", kind, kind, kind, kind)
	}
	noReversalHistory := strings.Replace(healthy, `hcai_maintenance_success_seen{kind="seller_reversal_reconciliation"} 1`, `hcai_maintenance_success_seen{kind="seller_reversal_reconciliation"} 0`, 1)
	noReversalHistory = strings.Replace(noReversalHistory, `hcai_maintenance_last_success_age_seconds{kind="seller_reversal_reconciliation"} 0.000000`, `hcai_maintenance_last_success_age_seconds{kind="seller_reversal_reconciliation"} 9999.000000`, 1)
	withBacklog := func(kind, mode, age string) string {
		labels := fmt.Sprintf("{kind=%q,mode=%q}", kind, mode)
		body := strings.Replace(healthy, "hcai_product_payment_backlog"+labels+" 0", "hcai_product_payment_backlog"+labels+" 1", 1)
		return strings.Replace(body, "hcai_product_payment_oldest_age_seconds"+labels+" 0.000000", "hcai_product_payment_oldest_age_seconds"+labels+" "+age, 1)
	}
	scanPending := strings.Replace(healthy, "hcai_asset_scan_pending 0", "hcai_asset_scan_pending 1", 1)
	writePending := strings.Replace(healthy, "hcai_upload_write_pending 0", "hcai_upload_write_pending 1", 1)
	for _, tc := range []struct {
		name, body, want string
		status           int
		env              []string
	}{
		{name: "healthy", body: healthy, want: "ok "},
		{name: "reversal_scanner_not_applicable", body: noReversalHistory, want: "ok "},
		{name: "bank_due", body: withBacklog("seller_bank_check_due", "live", "901.000000"), want: "ALERT live_seller_bank_check_due_age_exceeded"},
		{name: "bank_custom_due", body: withBacklog("seller_bank_check_due", "live", "61.000000"), env: []string{"ALERT_MAX_SELLER_BANK_CHECK_DUE_AGE_SECONDS=60"}, want: "ALERT live_seller_bank_check_due_age_exceeded"},
		{name: "bank_unresolved", body: withBacklog("seller_bank_unresolved", "live", "901.000000"), want: "ALERT live_seller_bank_unresolved_age_exceeded"},
		{name: "bank_test_ignored", body: withBacklog("seller_bank_unresolved", "test", "901.000000"), want: "ok "},
		{name: "bank_test_selected", body: withBacklog("seller_bank_unresolved", "test", "901.000000"), env: []string{"ALERT_PAYMENT_MODE=all"}, want: "ALERT test_seller_bank_unresolved_age_exceeded"},
		{name: "bank_review", body: strings.Replace(healthy, `kind="seller_bank_review",mode="live"} 0`, `kind="seller_bank_review",mode="live"} 1`, 1), want: "ALERT payment_problems_exceeded"},
		{name: "bank_missing_read", body: strings.Replace(healthy, `kind="seller_bank_read_unrecorded",mode="live"} 0`, `kind="seller_bank_read_unrecorded",mode="live"} 1`, 1), want: "ALERT payment_problems_exceeded"},
		{name: "bank_stopped", body: strings.Replace(healthy, `kind="seller_bank_dispatch_stopped",mode="live"} 0`, `kind="seller_bank_dispatch_stopped",mode="live"} 1`, 1), want: "ALERT payment_problems_exceeded"},
		{name: "bank_failed", body: strings.Replace(healthy, `kind="seller_bank_failed",mode="live"} 0`, `kind="seller_bank_failed",mode="live"} 1`, 1), want: "ALERT payment_problems_exceeded"},
		{name: "bank_failed_test_ignored", body: strings.Replace(healthy, `kind="seller_bank_failed",mode="test"} 0`, `kind="seller_bank_failed",mode="test"} 1`, 1), want: "ok "},
		{name: "bank_failed_test_selected", body: strings.Replace(healthy, `kind="seller_bank_failed",mode="test"} 0`, `kind="seller_bank_failed",mode="test"} 1`, 1), env: []string{"ALERT_PAYMENT_MODE=all"}, want: "ALERT payment_problems_exceeded"},
		{name: "bank_failed_missing", body: strings.Replace(healthy, `hcai_product_payment_problems{kind="seller_bank_failed",mode="live"} 0`+"\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "bank_failed_duplicate", body: healthy + `hcai_product_payment_problems{kind="seller_bank_failed",mode="live"} 0` + "\n", want: "ALERT missing_or_invalid_metric"},
		{name: "bank_scanner_never", body: strings.Replace(healthy, `hcai_maintenance_success_seen{kind="seller_bank_reconciliation"} 1`, `hcai_maintenance_success_seen{kind="seller_bank_reconciliation"} 0`, 1), want: "ALERT seller_bank_reconciliation_never_succeeded"},
		{name: "reversal_scanner_never", body: strings.Replace(withBacklog("seller_reversal_unresolved", "live", "1.000000"), `hcai_maintenance_success_seen{kind="seller_reversal_reconciliation"} 1`, `hcai_maintenance_success_seen{kind="seller_reversal_reconciliation"} 0`, 1), want: "ALERT seller_reversal_reconciliation_never_succeeded"},
		{name: "reversal_scanner_failed", body: strings.Replace(healthy, `hcai_maintenance_last_pass_failed{kind="seller_reversal_reconciliation"} 0`, `hcai_maintenance_last_pass_failed{kind="seller_reversal_reconciliation"} 1`, 1), want: "ALERT seller_reversal_reconciliation_pass_failed"},
		{name: "reversal_scanner_stale", body: strings.Replace(withBacklog("seller_reversal_unresolved", "live", "1.000000"), `hcai_maintenance_last_success_age_seconds{kind="seller_reversal_reconciliation"} 0.000000`, `hcai_maintenance_last_success_age_seconds{kind="seller_reversal_reconciliation"} 301.000000`, 1), want: "ALERT seller_reversal_reconciliation_success_stale"},
		{name: "reversal_scanner_missing", body: strings.Replace(healthy, "hcai_maintenance_success_seen{kind=\"seller_reversal_reconciliation\"} 1\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "funding_admission_missing", body: strings.Replace(healthy, `kind="seller_funding_admission_missing",mode="live"} 0`, `kind="seller_funding_admission_missing",mode="live"} 1`, 1), want: "ALERT payment_problems_exceeded"},
		{name: "funding_admission_test_ignored", body: strings.Replace(healthy, `kind="seller_funding_admission_missing",mode="test"} 0`, `kind="seller_funding_admission_missing",mode="test"} 1`, 1), want: "ok "},
		{name: "funding_admission_test_selected", body: strings.Replace(healthy, `kind="seller_funding_admission_missing",mode="test"} 0`, `kind="seller_funding_admission_missing",mode="test"} 1`, 1), env: []string{"ALERT_PAYMENT_MODE=test"}, want: "ALERT payment_problems_exceeded"},
		{name: "funding_admission_all_selected", body: strings.Replace(healthy, `kind="seller_funding_admission_missing",mode="test"} 0`, `kind="seller_funding_admission_missing",mode="test"} 1`, 1), env: []string{"ALERT_PAYMENT_MODE=all"}, want: "ALERT payment_problems_exceeded"},
		{name: "funding_admission_threshold", body: strings.Replace(healthy, `kind="seller_funding_admission_missing",mode="live"} 0`, `kind="seller_funding_admission_missing",mode="live"} 1`, 1), env: []string{"ALERT_MAX_PAYMENT_PROBLEMS=1"}, want: "ok "},
		{name: "funding_admission_series_missing", body: strings.Replace(healthy, `hcai_product_payment_problems{kind="seller_funding_admission_missing",mode="live"} 0`+"\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "funding_admission_series_duplicate", body: healthy + `hcai_product_payment_problems{kind="seller_funding_admission_missing",mode="live"} 0` + "\n", want: "ALERT missing_or_invalid_metric"},
		{name: "funding_admission_series_invalid", body: strings.Replace(healthy, `hcai_product_payment_problems{kind="seller_funding_admission_missing",mode="live"} 0`, `hcai_product_payment_problems{kind="seller_funding_admission_missing",mode="live"} NaN`, 1), want: "ALERT missing_or_invalid_metric"},
		{name: "funding_read_missing", body: strings.Replace(healthy, `kind="seller_funding_read_unrecorded",mode="live"} 0`, `kind="seller_funding_read_unrecorded",mode="live"} 1`, 1), want: "ALERT payment_problems_exceeded"},
		{name: "funding_read_test_ignored", body: strings.Replace(healthy, `kind="seller_funding_read_unrecorded",mode="test"} 0`, `kind="seller_funding_read_unrecorded",mode="test"} 1`, 1), want: "ok "},
		{name: "funding_read_test_selected", body: strings.Replace(healthy, `kind="seller_funding_read_unrecorded",mode="test"} 0`, `kind="seller_funding_read_unrecorded",mode="test"} 1`, 1), env: []string{"ALERT_PAYMENT_MODE=all"}, want: "ALERT payment_problems_exceeded"},
		{name: "funding_read_series_missing", body: strings.Replace(healthy, "hcai_product_payment_problems{kind=\"seller_funding_read_unrecorded\",mode=\"live\"} 0\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "funding_read_series_duplicate", body: healthy + "hcai_product_payment_problems{kind=\"seller_funding_read_unrecorded\",mode=\"live\"} 0\n", want: "ALERT missing_or_invalid_metric"},
		{name: "funding_unresolved", body: withBacklog("seller_funding_unresolved", "live", "901.000000"), want: "ALERT live_seller_funding_unresolved_age_exceeded"},
		{name: "funding_check_due", body: withBacklog("seller_funding_check_due", "live", "901.000000"), want: "ALERT live_seller_funding_check_due_age_exceeded"},
		{name: "funding_age_equal", body: withBacklog("seller_funding_unresolved", "live", "900.000000"), want: "ok "},
		{name: "funding_due_equal", body: withBacklog("seller_funding_check_due", "live", "900.000000"), want: "ok "},
		{name: "funding_custom_age", body: withBacklog("seller_funding_unresolved", "live", "61.000000"), env: []string{"ALERT_MAX_SELLER_FUNDING_UNRESOLVED_AGE_SECONDS=60"}, want: "ALERT live_seller_funding_unresolved_age_exceeded"},
		{name: "funding_custom_due", body: withBacklog("seller_funding_check_due", "live", "61.000000"), env: []string{"ALERT_MAX_SELLER_FUNDING_CHECK_DUE_AGE_SECONDS=60"}, want: "ALERT live_seller_funding_check_due_age_exceeded"},
		{name: "funding_invalid_age", body: healthy, env: []string{"ALERT_MAX_SELLER_FUNDING_UNRESOLVED_AGE_SECONDS=NaN"}, want: "Age thresholds must"},
		{name: "funding_invalid_due", body: healthy, env: []string{"ALERT_MAX_SELLER_FUNDING_CHECK_DUE_AGE_SECONDS=-1"}, want: "Age thresholds must"},
		{name: "funding_test_ignored", body: withBacklog("seller_funding_unresolved", "test", "901.000000"), want: "ok "},
		{name: "funding_test_enabled", body: withBacklog("seller_funding_unresolved", "test", "901.000000"), env: []string{"ALERT_PAYMENT_MODE=test"}, want: "ALERT test_seller_funding_unresolved_age_exceeded"},
		{name: "funding_all_enabled", body: withBacklog("seller_funding_check_due", "test", "901.000000"), env: []string{"ALERT_PAYMENT_MODE=all"}, want: "ALERT test_seller_funding_check_due_age_exceeded"},
		{name: "funding_dispatch_missing", body: strings.Replace(healthy, `kind="seller_funding_dispatch_missing",mode="live"} 0`, `kind="seller_funding_dispatch_missing",mode="live"} 1`, 1), want: "ALERT payment_problems_exceeded"},
		{name: "funding_stopped", body: strings.Replace(healthy, `kind="seller_funding_stopped",mode="live"} 0`, `kind="seller_funding_stopped",mode="live"} 1`, 1), want: "ALERT payment_problems_exceeded"},
		{name: "funding_review", body: strings.Replace(healthy, `kind="seller_funding_review",mode="live"} 0`, `kind="seller_funding_review",mode="live"} 1`, 1), want: "ALERT payment_problems_exceeded"},
		{name: "funding_review_test_ignored", body: strings.Replace(healthy, `kind="seller_funding_review",mode="test"} 0`, `kind="seller_funding_review",mode="test"} 1`, 1), want: "ok "},
		{name: "funding_review_test_enabled", body: strings.Replace(healthy, `kind="seller_funding_review",mode="test"} 0`, `kind="seller_funding_review",mode="test"} 1`, 1), env: []string{"ALERT_PAYMENT_MODE=all"}, want: "ALERT payment_problems_exceeded"},
		{name: "funding_series_missing", body: strings.Replace(healthy, "hcai_product_payment_backlog{kind=\"seller_funding_unresolved\",mode=\"live\"} 0\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "funding_problem_missing", body: strings.Replace(healthy, "hcai_product_payment_problems{kind=\"seller_funding_stopped\",mode=\"live\"} 0\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "funding_problem_duplicate", body: healthy + "hcai_product_payment_problems{kind=\"seller_funding_review\",mode=\"live\"} 0\n", want: "ALERT missing_or_invalid_metric"},
		{name: "funding_inconsistent", body: strings.Replace(healthy, "hcai_product_payment_oldest_age_seconds{kind=\"seller_funding_check_due\",mode=\"live\"} 0.000000", "hcai_product_payment_oldest_age_seconds{kind=\"seller_funding_check_due\",mode=\"live\"} 1.000000", 1), want: "ALERT inconsistent_payment_metrics"},
		{name: "funding_scanner_never", body: strings.Replace(healthy, `hcai_maintenance_success_seen{kind="seller_funding_reconciliation"} 1`, `hcai_maintenance_success_seen{kind="seller_funding_reconciliation"} 0`, 1), want: "ALERT seller_funding_reconciliation_never_succeeded"},
		{name: "funding_scanner_failed", body: strings.Replace(healthy, `hcai_maintenance_last_pass_failed{kind="seller_funding_reconciliation"} 0`, `hcai_maintenance_last_pass_failed{kind="seller_funding_reconciliation"} 1`, 1), want: "ALERT seller_funding_reconciliation_pass_failed"},
		{name: "funding_scanner_stale", body: strings.Replace(healthy, `hcai_maintenance_last_success_age_seconds{kind="seller_funding_reconciliation"} 0.000000`, `hcai_maintenance_last_success_age_seconds{kind="seller_funding_reconciliation"} 301.000000`, 1), want: "ALERT seller_funding_reconciliation_success_stale"},
		{name: "funding_scanner_missing", body: strings.Replace(healthy, "hcai_maintenance_success_seen{kind=\"seller_funding_reconciliation\"} 1\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "settlement_due", body: withBacklog("settlement_due", "live", "901.000000"), want: "ALERT live_settlement_due_age_exceeded"},
		{name: "settlement_due_limit", body: withBacklog("settlement_due", "live", "900.000000"), want: "ok "},
		{name: "settlement_due_custom", body: withBacklog("settlement_due", "live", "901.000000"), env: []string{"ALERT_MAX_SETTLEMENT_DUE_AGE_SECONDS=1000"}, want: "ok "},
		{name: "settlement_unresolved", body: withBacklog("settlement_unresolved", "live", "901.000000"), want: "ALERT live_settlement_unresolved_age_exceeded"},
		{name: "settlement_unresolved_limit", body: withBacklog("settlement_unresolved", "live", "900.000000"), want: "ok "},
		{name: "settlement_unresolved_custom", body: withBacklog("settlement_unresolved", "live", "901.000000"), env: []string{"ALERT_MAX_SETTLEMENT_UNRESOLVED_AGE_SECONDS=1000"}, want: "ok "},
		{name: "settlement_test_ignored", body: withBacklog("settlement_unresolved", "test", "901.000000"), want: "ok "},
		{name: "settlement_test_opt_in", body: withBacklog("settlement_unresolved", "test", "901.000000"), env: []string{"ALERT_PAYMENT_MODE=test"}, want: "ALERT test_settlement_unresolved_age_exceeded"},
		{name: "settlement_all_modes", body: withBacklog("settlement_due", "test", "901.000000"), env: []string{"ALERT_PAYMENT_MODE=all"}, want: "ALERT test_settlement_due_age_exceeded"},
		{name: "settlement_due_threshold_invalid", body: healthy, env: []string{"ALERT_MAX_SETTLEMENT_DUE_AGE_SECONDS=-1"}, want: "Age thresholds must"},
		{name: "settlement_unresolved_threshold_invalid", body: healthy, env: []string{"ALERT_MAX_SETTLEMENT_UNRESOLVED_AGE_SECONDS=NaN"}, want: "Age thresholds must"},
		{name: "settlement_metric_missing", body: strings.Replace(healthy, "hcai_product_payment_backlog{kind=\"settlement_due\",mode=\"live\"} 0\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "settlement_metric_duplicate", body: healthy + "hcai_product_payment_backlog{kind=\"settlement_due\",mode=\"live\"} 0\n", want: "ALERT missing_or_invalid_metric"},
		{name: "settlement_metric_invalid", body: withBacklog("settlement_unresolved", "live", "NaN"), want: "ALERT missing_or_invalid_metric"},
		{name: "settlement_metric_inconsistent", body: strings.Replace(healthy, `hcai_product_payment_oldest_age_seconds{kind="settlement_due",mode="live"} 0.000000`, `hcai_product_payment_oldest_age_seconds{kind="settlement_due",mode="live"} 1.000000`, 1), want: "ALERT inconsistent_payment_metrics"},
		{name: "settlement_snapshot_missing", body: strings.Replace(healthy, `kind="settlement_missing",mode="live"} 0`, `kind="settlement_missing",mode="live"} 1`, 1), want: "ALERT payment_problems_exceeded"},
		{name: "settlement_snapshot_test_ignored", body: strings.Replace(healthy, `kind="settlement_missing",mode="test"} 0`, `kind="settlement_missing",mode="test"} 1`, 1), want: "ok "},
		{name: "settlement_snapshot_test_opt_in", body: strings.Replace(healthy, `kind="settlement_missing",mode="test"} 0`, `kind="settlement_missing",mode="test"} 1`, 1), env: []string{"ALERT_PAYMENT_MODE=all"}, want: "ALERT payment_problems_exceeded"},
		{name: "settlement_check_stopped", body: strings.Replace(healthy, `kind="settlement_check_stopped",mode="live"} 0`, `kind="settlement_check_stopped",mode="live"} 1`, 1), want: "ALERT payment_problems_exceeded"},
		{name: "settlement_problem_missing", body: strings.Replace(healthy, "hcai_product_payment_problems{kind=\"settlement_check_stopped\",mode=\"live\"} 0\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "settlement_scanner_never", body: strings.Replace(healthy, `hcai_maintenance_success_seen{kind="product_settlement_reconciliation"} 1`, `hcai_maintenance_success_seen{kind="product_settlement_reconciliation"} 0`, 1), want: "ALERT product_settlement_reconciliation_never_succeeded"},
		{name: "settlement_scanner_failed", body: strings.Replace(healthy, `hcai_maintenance_last_pass_failed{kind="product_settlement_reconciliation"} 0`, `hcai_maintenance_last_pass_failed{kind="product_settlement_reconciliation"} 1`, 1), want: "ALERT product_settlement_reconciliation_pass_failed"},
		{name: "settlement_scanner_stale", body: strings.Replace(healthy, `hcai_maintenance_last_success_age_seconds{kind="product_settlement_reconciliation"} 0.000000`, `hcai_maintenance_last_success_age_seconds{kind="product_settlement_reconciliation"} 301.000000`, 1), want: "ALERT product_settlement_reconciliation_success_stale"},
		{name: "settlement_scanner_clock", body: strings.Replace(healthy, `hcai_maintenance_invalid_timestamps{kind="product_settlement_reconciliation"} 0`, `hcai_maintenance_invalid_timestamps{kind="product_settlement_reconciliation"} 1`, 1), want: "ALERT product_settlement_reconciliation_clock_invalid"},
		{name: "settlement_scanner_missing", body: strings.Replace(healthy, "hcai_maintenance_success_seen{kind=\"product_settlement_reconciliation\"} 1\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "write_pending_overdue", body: strings.Replace(writePending, "hcai_upload_write_oldest_pending_age_seconds 0.000000", "hcai_upload_write_oldest_pending_age_seconds 1801.000000", 1), want: "ALERT upload_write_pending_overdue"},
		{name: "write_pending_limit", body: strings.Replace(writePending, "hcai_upload_write_oldest_pending_age_seconds 0.000000", "hcai_upload_write_oldest_pending_age_seconds 1800.000000", 1), want: "ok "},
		{name: "write_pending_clock", body: strings.Replace(writePending, "hcai_upload_write_pending_invalid_timestamps 0", "hcai_upload_write_pending_invalid_timestamps 1", 1), want: "ALERT upload_write_pending_clock_invalid"},
		{name: "write_pending_missing", body: strings.Replace(healthy, "hcai_generation_output_pending 0\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "write_pending_duplicate", body: healthy + "hcai_generation_output_pending 0\n", want: "ALERT missing_or_invalid_metric"},
		{name: "write_pending_inconsistent", body: strings.Replace(healthy, "hcai_generation_output_oldest_pending_age_seconds 0.000000", "hcai_generation_output_oldest_pending_age_seconds 1.000000", 1), want: "ALERT inconsistent_media_write_metrics"},
		{name: "write_pending_clock_subcount", body: strings.Replace(healthy, "hcai_generation_output_pending_invalid_timestamps 0", "hcai_generation_output_pending_invalid_timestamps 1", 1), want: "ALERT inconsistent_media_write_metrics"},
		{name: "write_pending_threshold", body: writePending, env: []string{"ALERT_MAX_MEDIA_WRITE_PENDING_AGE_SECONDS=-1"}, want: "Age thresholds must"},
		{name: "webhook_pending", body: strings.Replace(healthy, `hcai_product_webhook_quarantines{mode="live"} 0`, `hcai_product_webhook_quarantines{mode="live"} 1`, 1), want: "ALERT product_webhook_evidence_pending_live"},
		{name: "webhook_missing", body: strings.Replace(healthy, `hcai_product_webhook_quarantines{mode="live"} 0`+"\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "webhook_inconsistent", body: strings.Replace(healthy, `hcai_product_webhook_quarantine_oldest_age_seconds{mode="live"} 0.000000`, `hcai_product_webhook_quarantine_oldest_age_seconds{mode="live"} 1.000000`, 1), want: "ALERT inconsistent_product_webhook_metrics"},

		{name: "scan_unresolved", body: strings.Replace(scanPending, "hcai_asset_scan_recovery_unresolved 0", "hcai_asset_scan_recovery_unresolved 1", 1), want: "ALERT asset_scan_recovery_unresolved"},
		{name: "scan_failed", body: strings.Replace(scanPending, "hcai_asset_scan_recovery_failed 0", "hcai_asset_scan_recovery_failed 1", 1), want: "ALERT asset_scan_recovery_failed"},
		{name: "scan_due", body: strings.Replace(strings.Replace(scanPending, "hcai_asset_scan_recovery_due 0", "hcai_asset_scan_recovery_due 1", 1), "hcai_asset_scan_recovery_oldest_due_age_seconds 0.000000", "hcai_asset_scan_recovery_oldest_due_age_seconds 301.000000", 1), want: "ALERT asset_scan_recovery_overdue"},
		{name: "scan_pending", body: strings.Replace(strings.Replace(healthy, "hcai_asset_scan_pending 0", "hcai_asset_scan_pending 1", 1), "hcai_asset_scan_oldest_pending_age_seconds 0.000000", "hcai_asset_scan_oldest_pending_age_seconds 901.000000", 1), want: "ALERT asset_scan_pending_overdue"},
		{name: "scan_missing", body: strings.Replace(healthy, "hcai_asset_scan_recovery_due 0\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "scan_inconsistent", body: strings.Replace(healthy, "hcai_asset_scan_recovery_oldest_due_age_seconds 0.000000", "hcai_asset_scan_recovery_oldest_due_age_seconds 1.000000", 1), want: "ALERT inconsistent_asset_scan_metrics"},

		{name: "scan_pending_limit", body: strings.Replace(scanPending, "hcai_asset_scan_oldest_pending_age_seconds 0.000000", "hcai_asset_scan_oldest_pending_age_seconds 900.000000", 1), want: "ok "},
		{name: "scan_custom_threshold", body: strings.Replace(scanPending, "hcai_asset_scan_oldest_pending_age_seconds 0.000000", "hcai_asset_scan_oldest_pending_age_seconds 901.000000", 1), env: []string{"ALERT_MAX_ASSET_SCAN_PENDING_AGE_SECONDS=1000"}, want: "ok "},
		{name: "scan_threshold_invalid", body: healthy, env: []string{"ALERT_MAX_ASSET_SCAN_RECOVERY_AGE_SECONDS=-1"}, want: "Age thresholds must"},
		{name: "scan_pending_inconsistent", body: strings.Replace(healthy, "hcai_asset_scan_oldest_pending_age_seconds 0.000000", "hcai_asset_scan_oldest_pending_age_seconds 10.000000", 1), want: "ALERT inconsistent_asset_scan_metrics"},
		{name: "scan_subcount_inconsistent", body: strings.Replace(healthy, "hcai_asset_scan_recovery_unresolved 0", "hcai_asset_scan_recovery_unresolved 1", 1), want: "ALERT inconsistent_asset_scan_metrics"},
		{name: "scan_duplicate", body: healthy + "hcai_asset_scan_recovery_unresolved 0\n", want: "ALERT missing_or_invalid_metric"},
		{name: "scan_fractional", body: strings.Replace(healthy, "hcai_asset_scan_pending 0", "hcai_asset_scan_pending 0.1", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "scan_nan", body: strings.Replace(healthy, "hcai_asset_scan_oldest_pending_age_seconds 0.000000", "hcai_asset_scan_oldest_pending_age_seconds NaN", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "generation_failed", body: strings.Replace(healthy, "hcai_generation_recovery_failed 0", "hcai_generation_recovery_failed 1", 1), want: "ALERT generation_recovery_failed"},
		{name: "generation_unresolved", body: strings.Replace(healthy, "hcai_generation_recovery_unresolved 0", "hcai_generation_recovery_unresolved 1", 1), want: "ALERT generation_recovery_unresolved"},
		{name: "generation_due", body: strings.Replace(strings.Replace(healthy, "hcai_generation_recovery_due 0", "hcai_generation_recovery_due 1", 1), "hcai_generation_recovery_oldest_due_age_seconds 0.000000", "hcai_generation_recovery_oldest_due_age_seconds 301.000000", 1), want: "ALERT generation_recovery_overdue"},
		{name: "generation_missing", body: strings.Replace(healthy, "hcai_generation_recovery_due 0\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "generation_inconsistent", body: strings.Replace(healthy, "hcai_generation_recovery_oldest_due_age_seconds 0.000000", "hcai_generation_recovery_oldest_due_age_seconds 1.000000", 1), want: "ALERT inconsistent_generation_recovery_metrics"},
		{name: "output_failed", body: strings.Replace(healthy, "hcai_generation_output_cleanup_failed 0", "hcai_generation_output_cleanup_failed 1", 1), want: "ALERT generation_output_cleanup_failed"},
		{name: "output_due", body: strings.Replace(strings.Replace(healthy, "hcai_generation_output_cleanup_due 0", "hcai_generation_output_cleanup_due 1", 1), "hcai_generation_output_cleanup_oldest_due_age_seconds 0.000000", "hcai_generation_output_cleanup_oldest_due_age_seconds 301.000000", 1), want: "ALERT generation_output_cleanup_overdue"},
		{name: "output_missing", body: strings.Replace(healthy, "hcai_generation_output_cleanup_failed 0\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "output_inconsistent", body: strings.Replace(healthy, "hcai_generation_output_cleanup_oldest_due_age_seconds 0.000000", "hcai_generation_output_cleanup_oldest_due_age_seconds 1.000000", 1), want: "ALERT inconsistent_generation_output_metrics"},
		{name: "upload_at_limit", body: strings.Replace(strings.Replace(healthy, "hcai_upload_write_cleanup_due 0", "hcai_upload_write_cleanup_due 1", 1), "hcai_upload_write_cleanup_oldest_due_age_seconds 0.000000", "hcai_upload_write_cleanup_oldest_due_age_seconds 300.000000", 1), want: "ok "},
		{name: "upload_custom_threshold", body: strings.Replace(strings.Replace(healthy, "hcai_upload_write_cleanup_due 0", "hcai_upload_write_cleanup_due 1", 1), "hcai_upload_write_cleanup_oldest_due_age_seconds 0.000000", "hcai_upload_write_cleanup_oldest_due_age_seconds 301.000000", 1), env: []string{"ALERT_MAX_UPLOAD_WRITE_CLEANUP_AGE_SECONDS=400"}, want: "ok "},
		{name: "upload_threshold_invalid", body: healthy, env: []string{"ALERT_MAX_UPLOAD_WRITE_CLEANUP_AGE_SECONDS=-1"}, want: "Age thresholds must"},
		{name: "upload_duplicate", body: healthy + "hcai_upload_write_cleanup_due 0\n", want: "ALERT missing_or_invalid_metric"},
		{name: "upload_fractional", body: strings.Replace(healthy, "hcai_upload_write_cleanup_failed 0", "hcai_upload_write_cleanup_failed 0.1", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "upload_nan", body: strings.Replace(healthy, "hcai_upload_write_cleanup_oldest_due_age_seconds 0.000000", "hcai_upload_write_cleanup_oldest_due_age_seconds NaN", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "upload_failed", body: strings.Replace(healthy, "hcai_upload_write_cleanup_failed 0", "hcai_upload_write_cleanup_failed 1", 1), want: "ALERT upload_write_cleanup_failed"},
		{name: "upload_due", body: strings.Replace(strings.Replace(healthy, "hcai_upload_write_cleanup_due 0", "hcai_upload_write_cleanup_due 1", 1), "hcai_upload_write_cleanup_oldest_due_age_seconds 0.000000", "hcai_upload_write_cleanup_oldest_due_age_seconds 301.000000", 1), want: "ALERT upload_write_cleanup_overdue"},
		{name: "upload_missing", body: strings.Replace(healthy, "hcai_upload_write_cleanup_failed 0\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "upload_inconsistent", body: strings.Replace(healthy, "hcai_upload_write_cleanup_oldest_due_age_seconds 0.000000", "hcai_upload_write_cleanup_oldest_due_age_seconds 1.000000", 1), want: "ALERT inconsistent_upload_write_metrics"},
		{name: "expired_lease", body: strings.Replace(strings.Replace(healthy, "hcai_jobs_expired_leases 0", "hcai_jobs_expired_leases 1", 1), "hcai_jobs_oldest_expired_lease_age_seconds 0.000000", "hcai_jobs_oldest_expired_lease_age_seconds 61.000000", 1), want: "ALERT expired_job_lease_age_exceeded"},
		{name: "invalid_lease", body: strings.Replace(healthy, "hcai_jobs_invalid_leases 0", "hcai_jobs_invalid_leases 1", 1), want: "ALERT invalid_job_leases"},
		{name: "lease_inconsistent", body: strings.Replace(healthy, "hcai_jobs_oldest_expired_lease_age_seconds 0.000000", "hcai_jobs_oldest_expired_lease_age_seconds 5.000000", 1), want: "ALERT inconsistent_lease_metrics"},
		{name: "lease_at_limit", body: strings.Replace(strings.Replace(healthy, "hcai_jobs_expired_leases 0", "hcai_jobs_expired_leases 1", 1), "hcai_jobs_oldest_expired_lease_age_seconds 0.000000", "hcai_jobs_oldest_expired_lease_age_seconds 60.000000", 1), want: "ok "},
		{name: "old_failure", body: strings.Replace(healthy, `kind="media_product"} 0`, `kind="media_product"} 1`, 1), want: "ALERT recovery_job_failures_exceeded"},
		{name: "threshold", body: strings.Replace(healthy, `kind="media_product"} 0`, `kind="media_product"} 1`, 1), env: []string{"ALERT_MAX_RECOVERY_FAILURES=1"}, want: "ok "},
		{name: "missing", body: strings.Replace(healthy, "hcai_recovery_jobs_failed{kind=\"media_product\"} 0\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "duplicate", body: healthy + "hcai_recovery_jobs_failed{kind=\"media_product\"} 0\n", want: "ALERT missing_or_invalid_metric"},
		{name: "invalid", body: strings.Replace(healthy, `kind="media_product"} 0`, `kind="media_product"} NaN`, 1), want: "ALERT missing_or_invalid_metric"},
		{name: "negative", body: strings.Replace(healthy, `kind="media_product"} 0`, `kind="media_product"} -1`, 1), want: "ALERT missing_or_invalid_metric"},
		{name: "fractional_count", body: strings.Replace(healthy, `kind="media_product"} 0`, `kind="media_product"} 0.1`, 1), want: "ALERT missing_or_invalid_metric"},
		{name: "queue_invalid", body: strings.Replace(healthy, "0.000000", "garbage", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "queue_old", body: strings.Replace(healthy, "0.000000", "901.000000", 1), want: "ALERT queued_job_age_exceeded"},
		{name: "daily_failure", body: healthy + "hcai_job_attempts_total{status=\"failed\",window=\"24h\"} 1\n", want: "ALERT failed_job_attempts_exceeded"},
		{name: "database", body: strings.Replace(healthy, "hcai_database_ready 1", "hcai_database_ready 0", 1), want: "ALERT database_not_ready"},
		{name: "audit", body: strings.Replace(healthy, "hcai_audit_chain_valid 1", "hcai_audit_chain_valid 0", 1), want: "ALERT audit_chain_invalid"},
		{name: "unavailable", status: 503, want: "ALERT metrics endpoint unavailable"},
		{name: "invalid_threshold", body: healthy, env: []string{"ALERT_MAX_RECOVERY_FAILURES=-1"}, want: "Alert thresholds must"},
		{name: "financial_problem", body: strings.Replace(healthy, `kind="event_failed",mode="live"} 0`, `kind="event_failed",mode="live"} 1`, 1), want: "ALERT payment_problems_exceeded"},

		{name: "checkout_evidence_conflict", body: strings.Replace(healthy, `kind="checkout_evidence_conflict",mode="live"} 0`, `kind="checkout_evidence_conflict",mode="live"} 1`, 1), want: "ALERT payment_problems_exceeded"},
		{name: "checkout_evidence_conflict_test_ignored", body: strings.Replace(healthy, `kind="checkout_evidence_conflict",mode="test"} 0`, `kind="checkout_evidence_conflict",mode="test"} 1`, 1), want: "ok "},
		{name: "checkout_evidence_conflict_test_opt_in", body: strings.Replace(healthy, `kind="checkout_evidence_conflict",mode="test"} 0`, `kind="checkout_evidence_conflict",mode="test"} 1`, 1), env: []string{"ALERT_PAYMENT_MODE=all"}, want: "ALERT payment_problems_exceeded"},
		{name: "checkout_evidence_conflict_missing", body: strings.Replace(healthy, "hcai_product_payment_problems{kind=\"checkout_evidence_conflict\",mode=\"live\"} 0\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "closed_checkout_paid", body: strings.Replace(healthy, `kind="closed_checkout_paid",mode="live"} 0`, `kind="closed_checkout_paid",mode="live"} 1`, 1), want: "ALERT payment_problems_exceeded"},
		{name: "closed_checkout_paid_test_ignored", body: strings.Replace(healthy, `kind="closed_checkout_paid",mode="test"} 0`, `kind="closed_checkout_paid",mode="test"} 1`, 1), want: "ok "},
		{name: "closed_checkout_paid_test_opt_in", body: strings.Replace(healthy, `kind="closed_checkout_paid",mode="test"} 0`, `kind="closed_checkout_paid",mode="test"} 1`, 1), env: []string{"ALERT_PAYMENT_MODE=all"}, want: "ALERT payment_problems_exceeded"},
		{name: "closed_checkout_paid_missing", body: strings.Replace(healthy, "hcai_product_payment_problems{kind=\"closed_checkout_paid\",mode=\"live\"} 0\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "checkout_check_stopped", body: strings.Replace(healthy, `kind="checkout_check_stopped",mode="live"} 0`, `kind="checkout_check_stopped",mode="live"} 1`, 1), want: "ALERT payment_problems_exceeded"},
		{name: "checkout_missing_job_problem", body: strings.Replace(healthy, `kind="checkout_check_missing",mode="live"} 0`, `kind="checkout_check_missing",mode="live"} 1`, 1), want: "ALERT payment_problems_exceeded"},
		{name: "refund_read_missing_response", body: strings.Replace(healthy, `kind="refund_read_unrecorded",mode="live"} 0`, `kind="refund_read_unrecorded",mode="live"} 1`, 1), want: "ALERT payment_problems_exceeded"},
		{name: "refund_read_metric_missing", body: strings.Replace(healthy, "hcai_product_payment_problems{kind=\"refund_read_unrecorded\",mode=\"live\"} 0\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "refund_observation_problem", body: strings.Replace(healthy, `kind="refund_observation_unresolved",mode="live"} 0`, `kind="refund_observation_unresolved",mode="live"} 1`, 1), want: "ALERT payment_problems_exceeded"},
		{name: "refund_observation_test_mode", body: strings.Replace(healthy, `kind="refund_observation_unresolved",mode="test"} 0`, `kind="refund_observation_unresolved",mode="test"} 1`, 1), env: []string{"ALERT_PAYMENT_MODE=all"}, want: "ALERT payment_problems_exceeded"},
		{name: "refund_observation_mode_isolation", body: strings.Replace(healthy, `kind="refund_observation_unresolved",mode="test"} 0`, `kind="refund_observation_unresolved",mode="test"} 1`, 1), want: "ok "},
		{name: "refund_observation_missing", body: strings.Replace(healthy, "hcai_product_payment_problems{kind=\"refund_observation_unresolved\",mode=\"live\"} 0\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "refund_observation_duplicate", body: healthy + "hcai_product_payment_problems{kind=\"refund_observation_unresolved\",mode=\"live\"} 0\n", want: "ALERT missing_or_invalid_metric"},
		{name: "checkout_missing_job_series_missing", body: strings.Replace(healthy, "hcai_product_payment_problems{kind=\"checkout_check_missing\",mode=\"live\"} 0\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "checkout_reconciliation_never", body: strings.Replace(healthy, `hcai_maintenance_success_seen{kind="product_checkout_reconciliation"} 1`, `hcai_maintenance_success_seen{kind="product_checkout_reconciliation"} 0`, 1), want: "ALERT product_checkout_reconciliation_never_succeeded"},
		{name: "checkout_check_test_ignored", body: strings.Replace(healthy, `kind="checkout_check_stopped",mode="test"} 0`, `kind="checkout_check_stopped",mode="test"} 1`, 1), want: "ok "},
		{name: "checkout_check_test_opt_in", body: strings.Replace(healthy, `kind="checkout_check_stopped",mode="test"} 0`, `kind="checkout_check_stopped",mode="test"} 1`, 1), env: []string{"ALERT_PAYMENT_MODE=all"}, want: "ALERT payment_problems_exceeded"},
		{name: "checkout_check_missing", body: strings.Replace(healthy, "hcai_product_payment_problems{kind=\"checkout_check_stopped\",mode=\"live\"} 0\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "checkout_check_duplicate", body: healthy + "hcai_product_payment_problems{kind=\"checkout_check_stopped\",mode=\"live\"} 0\n", want: "ALERT missing_or_invalid_metric"},
		{name: "test_problem_ignored_by_default", body: strings.Replace(healthy, `kind="event_failed",mode="test"} 0`, `kind="event_failed",mode="test"} 1`, 1), want: "ok "},
		{name: "test_problem_opt_in", body: strings.Replace(healthy, `kind="event_failed",mode="test"} 0`, `kind="event_failed",mode="test"} 1`, 1), env: []string{"ALERT_PAYMENT_MODE=all"}, want: "ALERT payment_problems_exceeded"},
		{name: "pending_checkout", body: withBacklog("checkout_pending", "live", "901.000000"), want: "ALERT live_checkout_pending_age_exceeded"},
		{name: "expired_checkout", body: withBacklog("checkout_expired", "live", "901.000000"), want: "ALERT live_checkout_expired_age_exceeded"},
		{name: "refund_unresolved", body: withBacklog("refund_unresolved", "live", "86401.000000"), want: "ALERT live_refund_unresolved_age_exceeded"},
		{name: "refund_query_due", body: withBacklog("refund_check_due", "live", "901.000000"), want: "ALERT live_refund_check_due_age_exceeded"},
		{name: "refund_threshold_equal", body: withBacklog("refund_unresolved", "live", "86400.000000"), want: "ok "},
		{name: "financial_mode_invalid", body: healthy, env: []string{"ALERT_PAYMENT_MODE=production"}, want: "ALERT_PAYMENT_MODE must"},
		{name: "financial_threshold_invalid", body: healthy, env: []string{"ALERT_MAX_REFUND_UNRESOLVED_AGE_SECONDS=NaN"}, want: "Age thresholds must"},
		{name: "financial_series_missing", body: strings.Replace(healthy, "hcai_product_payment_problems{kind=\"event_failed\",mode=\"live\"} 0\n", "", 1), want: "ALERT missing_or_invalid_metric"},
		{name: "financial_inconsistent", body: strings.Replace(healthy, "hcai_product_payment_oldest_age_seconds{kind=\"checkout_pending\",mode=\"live\"} 0.000000", "hcai_product_payment_oldest_age_seconds{kind=\"checkout_pending\",mode=\"live\"} 5.000000", 1), want: "ALERT inconsistent_payment_metrics"},
		{name: "maintenance_never", body: strings.Replace(healthy, `hcai_maintenance_success_seen{kind="legal_hold_expiry"} 1`, `hcai_maintenance_success_seen{kind="legal_hold_expiry"} 0`, 1), want: "ALERT legal_hold_expiry_never_succeeded"},
		{name: "maintenance_failed", body: strings.Replace(healthy, `hcai_maintenance_last_pass_failed{kind="legal_hold_expiry"} 0`, `hcai_maintenance_last_pass_failed{kind="legal_hold_expiry"} 1`, 1), want: "ALERT legal_hold_expiry_pass_failed"},
		{name: "maintenance_clock", body: strings.Replace(healthy, `hcai_maintenance_invalid_timestamps{kind="legal_hold_expiry"} 0`, `hcai_maintenance_invalid_timestamps{kind="legal_hold_expiry"} 1`, 1), want: "ALERT legal_hold_expiry_clock_invalid"},
		{name: "maintenance_stale", body: strings.Replace(healthy, `hcai_maintenance_last_success_age_seconds{kind="legal_hold_expiry"} 0.000000`, `hcai_maintenance_last_success_age_seconds{kind="legal_hold_expiry"} 301.000000`, 1), want: "ALERT legal_hold_expiry_success_stale"},
		{name: "maintenance_limit", body: strings.Replace(healthy, `hcai_maintenance_last_success_age_seconds{kind="legal_hold_expiry"} 0.000000`, `hcai_maintenance_last_success_age_seconds{kind="legal_hold_expiry"} 300.000000`, 1), want: "ok "},
		{name: "maintenance_missing", body: strings.Replace(healthy, "hcai_maintenance_success_seen{kind=\"legal_hold_expiry\"} 1\n", "", 1), want: "ALERT missing_or_invalid_metric"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			command := exec.Command("bash", "../../scripts/metrics-alert-check.sh")
			for _, value := range os.Environ() {
				if !strings.HasPrefix(value, "ALERT_") && !strings.HasPrefix(value, "METRICS_URL=") {
					command.Env = append(command.Env, value)
				}
			}
			command.Env = append(command.Env, "METRICS_URL="+server.URL)
			command.Env = append(command.Env, tc.env...)
			output, err := command.CombinedOutput()
			if !strings.Contains(string(output), tc.want) || (err == nil) != strings.HasPrefix(tc.want, "ok ") {
				t.Fatalf("err=%v output=%s want=%s", err, output, tc.want)
			}
		})
	}
}
