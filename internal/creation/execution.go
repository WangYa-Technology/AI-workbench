package creation

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
)

func enqueueGenerationTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, maxAttempts int) error {
	payload, err := json.Marshal(jobPayload{GenerationID: id})
	if err != nil {
		return err
	}
	var jobID uuid.UUID
	if err = tx.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,$2,$3) RETURNING id`, JobKind, payload, maxAttempts).Scan(&jobID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO generation_executions(generation_id,job_id) VALUES($1,$2)`, id, jobID)
	return err
}

type executionQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Never trust the handler's attempt counters or a payload alone. The binding,
// current token and durable attempt must all match, at database wall time.
func checkExecution(ctx context.Context, q executionQuerier, generation uuid.UUID, job jobs.Job) (jobs.Job, error) {
	if job.Kind != JobKind || job.ID == uuid.Nil || job.LeaseToken == uuid.Nil {
		return jobs.Job{}, jobs.ErrLeaseLost
	}
	err := q.QueryRow(ctx, `SELECT j.attempts,j.max_attempts FROM generation_executions e
 JOIN jobs j ON j.id=e.job_id JOIN job_attempts a ON a.job_id=j.id AND a.lease_token=j.lease_token AND a.attempt_number=j.attempts
 WHERE e.generation_id=$1 AND j.id=$2 AND j.kind=$3 AND j.payload->>'generationId'=$1::text
 AND j.status='running' AND j.lease_token=$4 AND j.lease_owner IS NOT NULL AND btrim(j.lease_owner)<>''
 AND isfinite(j.lease_expires_at) AND j.lease_expires_at>clock_timestamp()
 AND a.job_kind=j.kind AND a.status='running'`, generation, job.ID, JobKind, job.LeaseToken).Scan(&job.Attempts, &job.MaxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ErrLeaseLost
	}
	return job, err
}

// Caller holds lifecycle/account/generation locks. Do not keep this lock across
// Provider or storage I/O: that would prevent worker heartbeats from renewing.
// Lock first, then check wall time, including time spent waiting for the lock.
func lockExecution(ctx context.Context, tx pgx.Tx, generation uuid.UUID, job jobs.Job) (jobs.Job, error) {
	if job.ID == uuid.Nil || job.LeaseToken == uuid.Nil {
		return jobs.Job{}, jobs.ErrLeaseLost
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT j.id FROM jobs j JOIN generation_executions e ON e.job_id=j.id
 WHERE e.generation_id=$1 AND j.id=$2 FOR UPDATE OF j`, generation, job.ID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ErrLeaseLost
	}
	if err != nil {
		return jobs.Job{}, err
	}
	return checkExecution(ctx, tx, generation, job)
}

func (s *Service) startExecution(ctx context.Context, owner, generation uuid.UUID, job jobs.Job) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	account, err := lockGenerationAccount(ctx, tx, owner)
	if err != nil {
		return false, err
	}
	if account != "active" {
		return false, ErrAccountUnavailable
	}
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM generations WHERE id=$1 FOR UPDATE`, generation).Scan(&status); err != nil {
		return false, err
	}
	if status != "queued" && status != "running" {
		return false, tx.Commit(ctx)
	}
	if _, err = lockExecution(ctx, tx, generation, job); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE generations SET status='running',progress=35,error_code=NULL,error_message=NULL,updated_at=now() WHERE id=$1 AND status='queued'`, generation); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
