package assets

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ScanRecoveryMetrics struct {
	Pending, Failed, Due, Unresolved int64
	OldestPendingAge, OldestDueAge   float64
}

// ScanExecutionMetrics counts business obligations, independently of the
// maintenance heartbeat. Unresolved evidence is an investigation signal, never
// permission to synthesize a binding, restart a scan or grant a clean verdict.
func ScanExecutionMetrics(ctx context.Context, pool *pgxpool.Pool) (ScanRecoveryMetrics, error) {
	var m ScanRecoveryMetrics
	err := pool.QueryRow(ctx, `WITH pending AS (
 SELECT g.created_at,e.recovery_after,e.last_error_code,
 COALESCE(`+failedScanExecutionPredicate+`,false) AS recoverable,
 COALESCE(j.kind='asset.scan' AND j.payload->>'assetId'=g.id::text AND (
 (j.status='queued' AND j.attempts<j.max_attempts AND isfinite(j.available_at)
 AND j.lease_owner IS NULL AND j.lease_token IS NULL AND j.lease_expires_at IS NULL)
 OR (j.status='running' AND j.lease_owner IS NOT NULL AND btrim(j.lease_owner)<>''
 AND j.lease_token IS NOT NULL AND isfinite(j.lease_expires_at)
 AND EXISTS(SELECT 1 FROM job_attempts a WHERE a.job_id=j.id AND a.job_kind=j.kind
 AND a.attempt_number=j.attempts AND a.lease_token=j.lease_token AND a.status='running'))),false) AS executable
 FROM assets g LEFT JOIN asset_scan_executions e ON e.asset_id=g.id
 LEFT JOIN jobs j ON j.id=e.job_id WHERE g.source_type='upload' AND g.scan_status='pending')
 SELECT count(*),count(*) FILTER(WHERE last_error_code IS NOT NULL),
 count(*) FILTER(WHERE recoverable AND recovery_after<=now()),
 count(*) FILTER(WHERE (NOT recoverable AND NOT executable) OR NOT isfinite(created_at) OR created_at>now()),
 COALESCE(max(GREATEST(0,extract(epoch FROM now()-created_at))) FILTER(WHERE isfinite(created_at)),0)::float8,
 COALESCE(max(extract(epoch FROM now()-recovery_after)) FILTER(WHERE recoverable AND recovery_after<=now()),0)::float8
 FROM pending`).Scan(&m.Pending, &m.Failed, &m.Due, &m.Unresolved, &m.OldestPendingAge, &m.OldestDueAge)
	return m, err
}
