package observability

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Metrics contains process-local counters. Durable queue and request health
// values are read from PostgreSQL when the scrape is rendered.
type Metrics struct {
	started       time.Time
	requests      [6]atomic.Uint64
	durationNanos atomic.Uint64
	responseBytes atomic.Uint64
}

func NewMetrics(started time.Time) *Metrics {
	return &Metrics{started: started}
}

func (m *Metrics) RecordRequest(_ context.Context, item httputil.RequestObservation) error {
	class := item.Status / 100
	if class < 1 || class > 5 {
		class = 0
	}
	m.requests[class].Add(1)
	if item.Duration > 0 {
		m.durationNanos.Add(uint64(item.Duration))
	}
	if item.ResponseBytes > 0 {
		m.responseBytes.Add(uint64(item.ResponseBytes))
	}
	return nil
}

func (m *Metrics) Render(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	var output strings.Builder
	output.WriteString("# HELP hcai_process_uptime_seconds Process uptime in seconds.\n")
	output.WriteString("# TYPE hcai_process_uptime_seconds gauge\n")
	output.WriteString("hcai_process_uptime_seconds " + formatFloat(time.Since(m.started).Seconds()) + "\n")
	output.WriteString("# HELP hcai_http_requests_total API requests observed by status class.\n")
	output.WriteString("# TYPE hcai_http_requests_total counter\n")
	for class := 0; class <= 5; class++ {
		if class == 0 && m.requests[class].Load() == 0 {
			continue
		}
		label := "unknown"
		if class > 0 {
			label = strconv.Itoa(class) + "xx"
		}
		output.WriteString("hcai_http_requests_total{status_class=\"" + label + "\"} " + strconv.FormatUint(m.requests[class].Load(), 10) + "\n")
	}
	output.WriteString("# HELP hcai_http_request_duration_seconds_sum Total observed API request duration.\n")
	output.WriteString("# TYPE hcai_http_request_duration_seconds_sum counter\n")
	output.WriteString("hcai_http_request_duration_seconds_sum " + formatFloat(float64(m.durationNanos.Load())/float64(time.Second)) + "\n")
	output.WriteString("# HELP hcai_http_request_duration_seconds_count Total observed API requests used for duration.\n")
	output.WriteString("# TYPE hcai_http_request_duration_seconds_count counter\n")
	output.WriteString("hcai_http_request_duration_seconds_count " + strconv.FormatUint(m.totalRequests(), 10) + "\n")
	output.WriteString("# HELP hcai_http_response_bytes_total Total response bytes observed for API requests.\n")
	output.WriteString("# TYPE hcai_http_response_bytes_total counter\n")
	output.WriteString("hcai_http_response_bytes_total " + strconv.FormatUint(m.responseBytes.Load(), 10) + "\n")

	databaseReady := 1
	if err := pool.Ping(ctx); err != nil {
		databaseReady = 0
	}
	output.WriteString("# HELP hcai_database_ready Whether the API can reach PostgreSQL.\n")
	output.WriteString("# TYPE hcai_database_ready gauge\n")
	output.WriteString("hcai_database_ready " + strconv.Itoa(databaseReady) + "\n")
	if databaseReady == 0 {
		return output.String(), fmt.Errorf("database is not ready")
	}

	jobStatuses, err := jobStatusMetrics(ctx, pool)
	if err != nil {
		return output.String(), err
	}
	output.WriteString("# HELP hcai_jobs_total Durable jobs by current status.\n")
	output.WriteString("# TYPE hcai_jobs_total gauge\n")
	for _, item := range jobStatuses {
		output.WriteString("hcai_jobs_total{status=\"" + escapeLabel(item.Status) + "\"} " + strconv.FormatInt(item.Count, 10) + "\n")
	}
	var oldest *time.Time
	if err := pool.QueryRow(ctx, `SELECT min(available_at) FILTER (WHERE status='queued' AND available_at<=now()) FROM jobs`).Scan(&oldest); err != nil {
		return output.String(), err
	}
	oldestAge := 0.0
	if oldest != nil && oldest.Before(time.Now().UTC()) {
		oldestAge = time.Since(oldest.UTC()).Seconds()
	}
	output.WriteString("# HELP hcai_jobs_oldest_queued_age_seconds Age of the oldest runnable queued job.\n")
	output.WriteString("# TYPE hcai_jobs_oldest_queued_age_seconds gauge\n")
	output.WriteString("hcai_jobs_oldest_queued_age_seconds " + formatFloat(oldestAge) + "\n")
	var expiredLeases, invalidLeases int64
	var expiredAge float64
	if err := pool.QueryRow(ctx, `SELECT
 count(*) FILTER(WHERE lease_expires_at<=now() AND isfinite(lease_expires_at)),
 count(*) FILTER(WHERE lease_owner IS NULL OR btrim(lease_owner)='' OR lease_token IS NULL OR lease_expires_at IS NULL OR NOT isfinite(lease_expires_at)
 OR NOT EXISTS(SELECT 1 FROM job_attempts a WHERE a.job_id=j.id AND a.lease_token=j.lease_token AND a.attempt_number=j.attempts AND a.status='running')),
 COALESCE(max(extract(epoch FROM now()-lease_expires_at)) FILTER(WHERE lease_expires_at<=now() AND isfinite(lease_expires_at)),0)::float8
 FROM jobs j WHERE status='running'`).Scan(&expiredLeases, &invalidLeases, &expiredAge); err != nil {
		return output.String(), err
	}
	output.WriteString("# HELP hcai_jobs_expired_leases Running jobs with finite expired leases.\n# TYPE hcai_jobs_expired_leases gauge\n")
	output.WriteString("hcai_jobs_expired_leases " + strconv.FormatInt(expiredLeases, 10) + "\n")
	output.WriteString("# HELP hcai_jobs_oldest_expired_lease_age_seconds Longest time a running job has remained past lease expiry.\n# TYPE hcai_jobs_oldest_expired_lease_age_seconds gauge\n")
	output.WriteString("hcai_jobs_oldest_expired_lease_age_seconds " + formatFloat(expiredAge) + "\n")
	output.WriteString("# HELP hcai_jobs_invalid_leases Running jobs with missing or invalid lease or active-attempt evidence.\n# TYPE hcai_jobs_invalid_leases gauge\n")
	output.WriteString("hcai_jobs_invalid_leases " + strconv.FormatInt(invalidLeases, 10) + "\n")
	output.WriteString("# HELP hcai_product_webhook_quarantines Signed product receipts awaiting verification.\n# TYPE hcai_product_webhook_quarantines gauge\n")
	output.WriteString("# HELP hcai_product_webhook_quarantine_oldest_age_seconds Age of oldest retained pending signed product receipt.\n# TYPE hcai_product_webhook_quarantine_oldest_age_seconds gauge\n")
	for _, mode := range []string{"live", "test"} {
		var count int64
		var age float64
		if err := pool.QueryRow(ctx, `SELECT count(*),COALESCE(max(GREATEST(0,extract(epoch FROM now()-received_at))),0)::float8 FROM product_webhook_quarantines WHERE state='pending' AND live_mode=$1`, mode == "live").Scan(&count, &age); err != nil {
			return output.String(), err
		}
		labels := "{mode=\"" + mode + "\"} "
		output.WriteString("hcai_product_webhook_quarantines" + labels + strconv.FormatInt(count, 10) + "\n")
		output.WriteString("hcai_product_webhook_quarantine_oldest_age_seconds" + labels + formatFloat(age) + "\n")
	}

	attemptStatuses, err := attemptStatusMetrics(ctx, pool)
	if err != nil {
		return output.String(), err
	}
	output.WriteString("# HELP hcai_job_attempts_total Job attempts by status in the last 24 hours.\n")
	output.WriteString("# TYPE hcai_job_attempts_total gauge\n")
	for _, item := range attemptStatuses {
		output.WriteString("hcai_job_attempts_total{status=\"" + escapeLabel(item.Status) + "\",window=\"24h\"} " + strconv.FormatInt(item.Count, 10) + "\n")
	}
	recoveryFailures, err := datarights.RecoveryFailureCounts(ctx, pool)
	if err != nil {
		return output.String(), err
	}
	output.WriteString("# HELP hcai_recovery_jobs_failed Failed cleanup, export and deletion jobs without a linked replacement or closed subject.\n")
	output.WriteString("# TYPE hcai_recovery_jobs_failed gauge\n")
	kinds := make([]string, 0, len(recoveryFailures))
	for kind := range recoveryFailures {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		output.WriteString("hcai_recovery_jobs_failed{kind=\"" + escapeLabel(kind) + "\"} " + strconv.FormatInt(recoveryFailures[kind], 10) + "\n")
	}

	financial, err := payments.ProductOperationalMetrics(ctx, pool)
	if err != nil {
		return output.String(), err
	}
	output.WriteString("# HELP hcai_product_payment_backlog Current product transaction backlog by stage and mode.\n")
	output.WriteString("# TYPE hcai_product_payment_backlog gauge\n")
	output.WriteString("# HELP hcai_product_payment_oldest_age_seconds Oldest backlog age from original evidence or scheduled due time.\n")
	output.WriteString("# TYPE hcai_product_payment_oldest_age_seconds gauge\n")
	for _, item := range financial.Backlogs {
		labels := "{kind=\"" + escapeLabel(item.Kind) + "\",mode=\"" + escapeLabel(item.Mode) + "\"} "
		output.WriteString("hcai_product_payment_backlog" + labels + strconv.FormatInt(item.Count, 10) + "\n")
		output.WriteString("hcai_product_payment_oldest_age_seconds" + labels + formatFloat(item.OldestAgeSeconds) + "\n")
	}
	output.WriteString("# HELP hcai_product_payment_problems Product transaction failures and evidence gaps requiring investigation.\n")
	output.WriteString("# TYPE hcai_product_payment_problems gauge\n")
	for _, item := range financial.Problems {
		output.WriteString("hcai_product_payment_problems{kind=\"" + escapeLabel(item.Kind) + "\",mode=\"" + escapeLabel(item.Mode) + "\"} " + strconv.FormatInt(item.Count, 10) + "\n")
	}

	var outputFailures, outputDue int64
	writes, err := mediaWriteBacklogs(ctx, pool)
	if err != nil {
		return output.String(), err
	}
	for _, item := range writes {
		prefix := "hcai_" + item.kind
		output.WriteString("# HELP " + prefix + "_pending Unattached pending write intents without an active legal hold.\n# TYPE " + prefix + "_pending gauge\n")
		output.WriteString(prefix + "_pending " + strconv.FormatInt(item.pending, 10) + "\n")
		output.WriteString("# HELP " + prefix + "_oldest_pending_age_seconds Age from original pending write registration, unaffected by retry scheduling.\n# TYPE " + prefix + "_oldest_pending_age_seconds gauge\n")
		output.WriteString(prefix + "_oldest_pending_age_seconds " + formatFloat(item.oldestAge) + "\n")
		output.WriteString("# HELP " + prefix + "_pending_invalid_timestamps Pending write intents with future or non-finite registration times.\n# TYPE " + prefix + "_pending_invalid_timestamps gauge\n")
		output.WriteString(prefix + "_pending_invalid_timestamps " + strconv.FormatInt(item.invalid, 10) + "\n")
	}
	var outputAge float64
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE last_error_code IN ('generation_output_cleanup_failed','generation_output_conflict')),
 count(*) FILTER(WHERE next_check_at<=now()),COALESCE(max(GREATEST(0,extract(epoch FROM now()-next_check_at))),0)::float8
 FROM generation_output_writes WHERE status<>'attached'`).Scan(&outputFailures, &outputDue, &outputAge); err != nil {
		return output.String(), err
	}
	output.WriteString("# HELP hcai_generation_output_cleanup_failed Recorded generation media cleanup failures or conflicting references.\n# TYPE hcai_generation_output_cleanup_failed gauge\n")
	output.WriteString("hcai_generation_output_cleanup_failed " + strconv.FormatInt(outputFailures, 10) + "\n")
	output.WriteString("# HELP hcai_generation_output_cleanup_due Unattached output locations due for cleanup or recheck.\n# TYPE hcai_generation_output_cleanup_due gauge\n")
	output.WriteString("hcai_generation_output_cleanup_due " + strconv.FormatInt(outputDue, 10) + "\n")
	output.WriteString("# HELP hcai_generation_output_cleanup_oldest_due_age_seconds Oldest overdue output cleanup check.\n# TYPE hcai_generation_output_cleanup_oldest_due_age_seconds gauge\n")
	output.WriteString("hcai_generation_output_cleanup_oldest_due_age_seconds " + formatFloat(outputAge) + "\n")

	var uploadFailures, uploadDue int64
	var uploadAge float64
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE last_error_code IN ('upload_write_cleanup_failed','upload_write_conflict')),
 count(*) FILTER(WHERE next_check_at<=now()),COALESCE(max(GREATEST(0,extract(epoch FROM now()-next_check_at))),0)::float8
 FROM upload_writes WHERE status<>'attached'`).Scan(&uploadFailures, &uploadDue, &uploadAge); err != nil {
		return output.String(), err
	}
	output.WriteString("# HELP hcai_upload_write_cleanup_failed Recorded uploaded media cleanup failures or conflicting references.\n# TYPE hcai_upload_write_cleanup_failed gauge\n")
	output.WriteString("hcai_upload_write_cleanup_failed " + strconv.FormatInt(uploadFailures, 10) + "\n")
	output.WriteString("# HELP hcai_upload_write_cleanup_due Unattached upload locations due for cleanup or recheck.\n# TYPE hcai_upload_write_cleanup_due gauge\n")
	output.WriteString("hcai_upload_write_cleanup_due " + strconv.FormatInt(uploadDue, 10) + "\n")
	output.WriteString("# HELP hcai_upload_write_cleanup_oldest_due_age_seconds Oldest overdue upload cleanup check.\n# TYPE hcai_upload_write_cleanup_oldest_due_age_seconds gauge\n")
	output.WriteString("hcai_upload_write_cleanup_oldest_due_age_seconds " + formatFloat(uploadAge) + "\n")

	executions, err := creation.ExecutionMetrics(ctx, pool)
	if err != nil {
		return output.String(), err
	}
	for _, metric := range []struct{ name, help, value string }{
		{"hcai_generation_recovery_failed", "Unfinished generations with a failed recovery check.", strconv.FormatInt(executions.Failed, 10)},
		{"hcai_generation_recovery_due", "Generations with evidenced terminal execution awaiting recovery.", strconv.FormatInt(executions.Due, 10)},
		{"hcai_generation_recovery_unresolved", "Unfinished generations with missing or ambiguous execution evidence.", strconv.FormatInt(executions.Unresolved, 10)},
		{"hcai_generation_recovery_oldest_due_age_seconds", "Age of oldest due generation recovery.", formatFloat(executions.OldestDueAge)},
	} {
		output.WriteString("# HELP " + metric.name + " " + metric.help + "\n# TYPE " + metric.name + " gauge\n" + metric.name + " " + metric.value + "\n")
	}

	scans, err := assets.ScanExecutionMetrics(ctx, pool)
	if err != nil {
		return output.String(), err
	}
	for _, metric := range []struct{ name, help, value string }{
		{"hcai_asset_scan_pending", "Uploaded assets awaiting a verified scan or review.", strconv.FormatInt(scans.Pending, 10)},
		{"hcai_asset_scan_oldest_pending_age_seconds", "Age of oldest pending upload.", formatFloat(scans.OldestPendingAge)},
		{"hcai_asset_scan_recovery_failed", "Pending scans whose last recovery check failed.", strconv.FormatInt(scans.Failed, 10)},
		{"hcai_asset_scan_recovery_due", "Pending scans with evidenced terminal jobs due for recovery.", strconv.FormatInt(scans.Due, 10)},
		{"hcai_asset_scan_recovery_unresolved", "Pending scans with missing or inconsistent execution evidence or invalid creation times.", strconv.FormatInt(scans.Unresolved, 10)},
		{"hcai_asset_scan_recovery_oldest_due_age_seconds", "Age since oldest due scan recovery check.", formatFloat(scans.OldestDueAge)},
	} {
		output.WriteString("# HELP " + metric.name + " " + metric.help + "\n# TYPE " + metric.name + " gauge\n" + metric.name + " " + metric.value + "\n")
	}

	maintenance, err := maintenanceMetrics(ctx, pool)
	if err != nil {
		return output.String(), err
	}
	for _, definition := range []struct{ name, kind, help string }{
		{"hcai_maintenance_passes_total", "counter", "Recorded maintenance pass completions across workers."},
		{"hcai_maintenance_failures_total", "counter", "Recorded failed maintenance passes across workers."},
		{"hcai_maintenance_success_seen", "gauge", "Whether a successful pass has ever been recorded."},
		{"hcai_maintenance_last_pass_failed", "gauge", "Whether the most recently recorded pass failed."},
		{"hcai_maintenance_invalid_timestamps", "gauge", "Whether maintenance timestamps are ahead of database time."},
		{"hcai_maintenance_last_success_age_seconds", "gauge", "Age of the last successful recorded pass; consult success_seen for missing history."},
	} {
		output.WriteString("# HELP " + definition.name + " " + definition.help + "\n# TYPE " + definition.name + " " + definition.kind + "\n")
	}
	for _, item := range maintenance {
		labels := "{kind=\"" + escapeLabel(item.Kind) + "\"} "
		output.WriteString("hcai_maintenance_passes_total" + labels + strconv.FormatInt(item.Passes, 10) + "\n")
		output.WriteString("hcai_maintenance_failures_total" + labels + strconv.FormatInt(item.Failures, 10) + "\n")
		output.WriteString("hcai_maintenance_success_seen" + labels + boolMetric(item.SuccessSeen) + "\n")
		output.WriteString("hcai_maintenance_last_pass_failed" + labels + boolMetric(item.LastFailed) + "\n")
		output.WriteString("hcai_maintenance_invalid_timestamps" + labels + boolMetric(item.InvalidTimestamps) + "\n")
		output.WriteString("hcai_maintenance_last_success_age_seconds" + labels + formatFloat(item.SuccessAgeSeconds) + "\n")
	}

	auditValid, err := auditChainValid(ctx, pool)
	if err != nil {
		return output.String(), err
	}
	output.WriteString("# HELP hcai_audit_chain_valid Whether the append-only audit chain is internally consistent.\n")
	output.WriteString("# TYPE hcai_audit_chain_valid gauge\n")
	if auditValid {
		output.WriteString("hcai_audit_chain_valid 1\n")
	} else {
		output.WriteString("hcai_audit_chain_valid 0\n")
	}
	return output.String(), nil
}

func (m *Metrics) totalRequests() uint64 {
	var total uint64
	for index := range m.requests {
		total += m.requests[index].Load()
	}
	return total
}

type statusMetric struct {
	Status string
	Count  int64
}

func jobStatusMetrics(ctx context.Context, pool *pgxpool.Pool) ([]statusMetric, error) {
	rows, err := pool.Query(ctx, `SELECT status,count(*) FROM jobs GROUP BY status ORDER BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []statusMetric{}
	for rows.Next() {
		var item statusMetric
		if err := rows.Scan(&item.Status, &item.Count); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func attemptStatusMetrics(ctx context.Context, pool *pgxpool.Pool) ([]statusMetric, error) {
	rows, err := pool.Query(ctx, `SELECT status,count(*) FROM job_attempts WHERE started_at>=now()-interval '24 hours' GROUP BY status ORDER BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []statusMetric{}
	for rows.Next() {
		var item statusMetric
		if err := rows.Scan(&item.Status, &item.Count); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func auditChainValid(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var eventCount, headSequence, invalidCount int64
	var headHash, actualHeadHash string
	if err := pool.QueryRow(ctx, `
		WITH checked AS (
		  SELECT sequence,event_hash,previous_hash,
		         lag(event_hash) OVER (ORDER BY sequence) AS expected_previous_hash,
		         audit_event_hash(sequence,previous_hash,id,actor_id,action,resource_type,resource_id,reason,request_id,metadata,created_at) AS expected_event_hash
		  FROM audit_events
		), invalid AS (
		  SELECT sequence FROM checked
		  WHERE event_hash <> expected_event_hash OR COALESCE(previous_hash,'') <> COALESCE(expected_previous_hash,'')
		)
		SELECT (SELECT count(*) FROM audit_events),s.head_sequence,s.head_hash,count(invalid.sequence)
		FROM audit_chain_state s LEFT JOIN invalid ON true WHERE s.singleton=true GROUP BY s.head_sequence,s.head_hash`).Scan(&eventCount, &headSequence, &headHash, &invalidCount); err != nil {
		return false, err
	}
	var actualHeadSequence int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(max(sequence),0),COALESCE((array_agg(event_hash ORDER BY sequence DESC))[1],'') FROM audit_events`).Scan(&actualHeadSequence, &actualHeadHash); err != nil {
		return false, err
	}
	return invalidCount == 0 && eventCount == headSequence && actualHeadSequence == headSequence && actualHeadHash == headHash, nil
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 6, 64)
}

func boolMetric(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func escapeLabel(value string) string {
	return strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n").Replace(value)
}
