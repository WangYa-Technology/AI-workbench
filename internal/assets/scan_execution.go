package assets

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
)

type executionQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Never trust the handler's attempt counters or a payload alone. The binding,
// current token and durable attempt must all match, at database wall time.
func checkScanExecution(ctx context.Context, q executionQuerier, asset uuid.UUID, job jobs.Job) (jobs.Job, error) {
	if job.Kind != ScanJobKind || job.ID == uuid.Nil || job.LeaseToken == uuid.Nil {
		return jobs.Job{}, jobs.ErrLeaseLost
	}
	err := q.QueryRow(ctx, `SELECT j.attempts,j.max_attempts FROM asset_scan_executions e
 JOIN jobs j ON j.id=e.job_id JOIN job_attempts a ON a.job_id=j.id AND a.lease_token=j.lease_token AND a.attempt_number=j.attempts
 WHERE e.asset_id=$1 AND j.id=$2 AND j.kind=$3 AND j.payload->>'assetId'=$1::text
 AND j.status='running' AND j.lease_token=$4 AND j.lease_owner IS NOT NULL AND btrim(j.lease_owner)<>''
 AND isfinite(j.lease_expires_at) AND j.lease_expires_at>clock_timestamp()
 AND a.job_kind=j.kind AND a.status='running'`, asset, job.ID, ScanJobKind, job.LeaseToken).Scan(&job.Attempts, &job.MaxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ErrLeaseLost
	}
	return job, err
}

// Caller holds asset row lock. Do not keep this lock across
// Provider or storage I/O: that would prevent worker heartbeats from renewing.
// Lock first, then check wall time, including time spent waiting for the lock.
func lockScanExecution(ctx context.Context, tx pgx.Tx, asset uuid.UUID, job jobs.Job) (jobs.Job, error) {
	if job.ID == uuid.Nil || job.LeaseToken == uuid.Nil {
		return jobs.Job{}, jobs.ErrLeaseLost
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT j.id FROM jobs j JOIN asset_scan_executions e ON e.job_id=j.id
 WHERE e.asset_id=$1 AND j.id=$2 FOR UPDATE OF j`, asset, job.ID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ErrLeaseLost
	}
	if err != nil {
		return jobs.Job{}, err
	}
	return checkScanExecution(ctx, tx, asset, job)
}
