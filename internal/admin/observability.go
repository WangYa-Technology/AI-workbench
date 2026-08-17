package admin

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type RequestDiagnostics struct {
	Total             int64            `json:"total"`
	ClientErrors      int64            `json:"clientErrors"`
	ServerErrors      int64            `json:"serverErrors"`
	AverageDurationMs float64          `json:"averageDurationMs"`
	P95DurationMs     float64          `json:"p95DurationMs"`
	ByStatus          map[string]int64 `json:"byStatus"`
}

type JobDiagnostics struct {
	ByStatus                    map[string]int64 `json:"byStatus"`
	ByAttemptStatus             map[string]int64 `json:"byAttemptStatus"`
	Retrying                    int64            `json:"retrying"`
	ExpiredLeases               int64            `json:"expiredLeases"`
	AttemptsLast24Hours         int64            `json:"attemptsLast24Hours"`
	LeaseRenewalsLast24Hours    int64            `json:"leaseRenewalsLast24Hours"`
	LeaseExpirationsLast24Hours int64            `json:"leaseExpirationsLast24Hours"`
	TerminalFailuresLast24Hours int64            `json:"terminalFailuresLast24Hours"`
	OldestQueuedAt              *time.Time       `json:"oldestQueuedAt,omitempty"`
}

type AuditIntegrity struct {
	Valid                bool   `json:"valid"`
	EventCount           int64  `json:"eventCount"`
	HeadSequence         int64  `json:"headSequence"`
	HeadHash             string `json:"headHash"`
	FirstInvalidSequence *int64 `json:"firstInvalidSequence,omitempty"`
}

type OperationalDiagnostics struct {
	WindowMinutes int                `json:"windowMinutes"`
	Requests      RequestDiagnostics `json:"requests"`
	Jobs          JobDiagnostics     `json:"jobs"`
	Audit         AuditIntegrity     `json:"audit"`
	DatabaseReady bool               `json:"databaseReady"`
	AsOf          time.Time          `json:"asOf"`
}

func (s *Service) GetOperationalDiagnostics(ctx context.Context) (OperationalDiagnostics, error) {
	result := OperationalDiagnostics{
		WindowMinutes: 15,
		Requests:      RequestDiagnostics{ByStatus: map[string]int64{}},
		Jobs:          JobDiagnostics{ByStatus: map[string]int64{}, ByAttemptStatus: map[string]int64{}},
		DatabaseReady: true,
		AsOf:          time.Now().UTC(),
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.QueryRow(ctx, `
		SELECT count(*),count(*) FILTER (WHERE status BETWEEN 400 AND 499),count(*) FILTER (WHERE status >= 500),
		       COALESCE(avg(duration_ms),0)::float8,COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms),0)::float8
		FROM request_observations WHERE occurred_at >= now()-interval '15 minutes'`).Scan(
		&result.Requests.Total, &result.Requests.ClientErrors, &result.Requests.ServerErrors,
		&result.Requests.AverageDurationMs, &result.Requests.P95DurationMs); err != nil {
		return result, fmt.Errorf("request diagnostics: %w", err)
	}
	requestRows, err := tx.Query(ctx, `SELECT (status/100)::text||'xx',count(*) FROM request_observations WHERE occurred_at >= now()-interval '15 minutes' GROUP BY status/100 ORDER BY status/100`)
	if err != nil {
		return result, err
	}
	for requestRows.Next() {
		var bucket string
		var count int64
		if err := requestRows.Scan(&bucket, &count); err != nil {
			requestRows.Close()
			return result, err
		}
		result.Requests.ByStatus[bucket] = count
	}
	if err := requestRows.Err(); err != nil {
		requestRows.Close()
		return result, err
	}
	requestRows.Close()

	jobRows, err := tx.Query(ctx, `SELECT status,count(*) FROM jobs GROUP BY status ORDER BY status`)
	if err != nil {
		return result, err
	}
	for jobRows.Next() {
		var status string
		var count int64
		if err := jobRows.Scan(&status, &count); err != nil {
			jobRows.Close()
			return result, err
		}
		result.Jobs.ByStatus[status] = count
	}
	if err := jobRows.Err(); err != nil {
		jobRows.Close()
		return result, err
	}
	jobRows.Close()
	if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='queued' AND attempts > 0),count(*) FILTER (WHERE status='running' AND lease_expires_at < now()),min(created_at) FILTER (WHERE status='queued') FROM jobs`).Scan(&result.Jobs.Retrying, &result.Jobs.ExpiredLeases, &result.Jobs.OldestQueuedAt); err != nil {
		return result, fmt.Errorf("job diagnostics: %w", err)
	}
	attemptRows, err := tx.Query(ctx, `SELECT status,count(*) FROM job_attempts WHERE started_at>=now()-interval '24 hours' GROUP BY status ORDER BY status`)
	if err != nil {
		return result, err
	}
	for attemptRows.Next() {
		var status string
		var count int64
		if err := attemptRows.Scan(&status, &count); err != nil {
			attemptRows.Close()
			return result, err
		}
		result.Jobs.ByAttemptStatus[status] = count
		result.Jobs.AttemptsLast24Hours += count
	}
	if err := attemptRows.Err(); err != nil {
		attemptRows.Close()
		return result, err
	}
	attemptRows.Close()
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(sum(lease_renewals),0),count(*) FILTER (WHERE status='lease_expired'),count(*) FILTER (WHERE status='failed')
		FROM job_attempts WHERE started_at>=now()-interval '24 hours'`).Scan(
		&result.Jobs.LeaseRenewalsLast24Hours, &result.Jobs.LeaseExpirationsLast24Hours, &result.Jobs.TerminalFailuresLast24Hours); err != nil {
		return result, fmt.Errorf("job attempt diagnostics: %w", err)
	}

	var invalidCount int64
	if err := tx.QueryRow(ctx, `
		WITH checked AS (
		  SELECT sequence,event_hash,previous_hash,
		         lag(event_hash) OVER (ORDER BY sequence) AS expected_previous_hash,
		         audit_event_hash(sequence,previous_hash,id,actor_id,action,resource_type,resource_id,reason,request_id,metadata,created_at) AS expected_event_hash
		  FROM audit_events
		), invalid AS (
		  SELECT sequence FROM checked
		  WHERE event_hash <> expected_event_hash OR COALESCE(previous_hash,'') <> COALESCE(expected_previous_hash,'')
		)
		SELECT (SELECT count(*) FROM audit_events),s.head_sequence,s.head_hash,count(invalid.sequence),min(invalid.sequence)
		FROM audit_chain_state s LEFT JOIN invalid ON true WHERE s.singleton=true GROUP BY s.head_sequence,s.head_hash`).Scan(
		&result.Audit.EventCount, &result.Audit.HeadSequence, &result.Audit.HeadHash, &invalidCount, &result.Audit.FirstInvalidSequence); err != nil {
		return result, fmt.Errorf("audit diagnostics: %w", err)
	}
	var actualHeadHash string
	var actualHeadSequence int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(sequence),0),COALESCE((array_agg(event_hash ORDER BY sequence DESC))[1],'') FROM audit_events`).Scan(&actualHeadSequence, &actualHeadHash); err != nil {
		return result, err
	}
	result.Audit.Valid = invalidCount == 0 && result.Audit.EventCount == result.Audit.HeadSequence && actualHeadSequence == result.Audit.HeadSequence && actualHeadHash == result.Audit.HeadHash
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	return result, nil
}
