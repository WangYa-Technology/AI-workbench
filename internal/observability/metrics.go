package observability

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

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

	attemptStatuses, err := attemptStatusMetrics(ctx, pool)
	if err != nil {
		return output.String(), err
	}
	output.WriteString("# HELP hcai_job_attempts_total Job attempts by status in the last 24 hours.\n")
	output.WriteString("# TYPE hcai_job_attempts_total gauge\n")
	for _, item := range attemptStatuses {
		output.WriteString("hcai_job_attempts_total{status=\"" + escapeLabel(item.Status) + "\",window=\"24h\"} " + strconv.FormatInt(item.Count, 10) + "\n")
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

func escapeLabel(value string) string {
	return strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n").Replace(value)
}
