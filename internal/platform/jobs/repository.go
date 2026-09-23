package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNoJob     = errors.New("no job available")
	ErrLeaseLost = errors.New("job lease was lost")
)

type Job struct {
	ID          uuid.UUID
	Kind        string
	Payload     json.RawMessage
	Attempts    int
	MaxAttempts int
	LeaseToken  uuid.UUID
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Enqueue(ctx context.Context, kind string, payload any) (uuid.UUID, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshal job payload: %w", err)
	}
	var id uuid.UUID
	err = r.pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload) VALUES($1,$2) RETURNING id`, kind, body).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("enqueue job: %w", err)
	}
	return id, nil
}

func (r *Repository) Claim(ctx context.Context, owner string, lease time.Duration) (Job, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" || lease < 30*time.Millisecond || lease > 24*time.Hour {
		return Job{}, fmt.Errorf("invalid job lease")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Job{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var job Job
	err = tx.QueryRow(ctx, `
		SELECT id,kind,payload,attempts,max_attempts
		FROM jobs
		WHERE status='queued' AND available_at <= now()
		ORDER BY available_at,created_at
		FOR UPDATE SKIP LOCKED
		LIMIT 1`).Scan(&job.ID, &job.Kind, &job.Payload, &job.Attempts, &job.MaxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNoJob
	}
	if err != nil {
		return Job{}, fmt.Errorf("select job: %w", err)
	}
	job.LeaseToken = uuid.New()
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `
		UPDATE jobs SET status='running',attempts=attempts+1,lease_owner=$2,lease_token=$3,lease_expires_at=clock_timestamp()+$4::interval,updated_at=clock_timestamp()
		WHERE id=$1 RETURNING lease_expires_at`, job.ID, owner, job.LeaseToken, intervalLiteral(lease)).Scan(&expiresAt)
	if err != nil {
		return Job{}, fmt.Errorf("claim job: %w", err)
	}
	job.Attempts++
	ownerHash := sha256.Sum256([]byte(owner))
	if _, err := tx.Exec(ctx, `
		INSERT INTO job_attempts(job_id,attempt_number,job_kind,worker_ref_hash,lease_token,lease_expires_at)
		VALUES($1,$2,$3,$4,$5,$6)`, job.ID, job.Attempts, job.Kind,
		hex.EncodeToString(ownerHash[:]), job.LeaseToken, expiresAt); err != nil {
		return Job{}, fmt.Errorf("record job attempt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Job{}, fmt.Errorf("commit job claim: %w", err)
	}
	return job, nil
}

func (r *Repository) Renew(ctx context.Context, job Job, owner string, lease time.Duration) error {
	if job.ID == uuid.Nil || job.LeaseToken == uuid.Nil || strings.TrimSpace(owner) == "" || lease < 30*time.Millisecond || lease > 24*time.Hour {
		return ErrLeaseLost
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, job, owner); err != nil {
		return err
	}
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `
		UPDATE jobs SET lease_expires_at=GREATEST(lease_expires_at,clock_timestamp()+$4::interval),updated_at=clock_timestamp()
		WHERE id=$1 AND status='running' AND lease_owner=$2 AND lease_token=$3 AND isfinite(lease_expires_at) AND lease_expires_at>clock_timestamp()
		RETURNING lease_expires_at`, job.ID, owner, job.LeaseToken, intervalLiteral(lease)).Scan(&expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("renew job lease: %w", err)
	}
	result, err := tx.Exec(ctx, `
		UPDATE job_attempts SET lease_expires_at=$2,lease_renewals=lease_renewals+1,updated_at=clock_timestamp()
		WHERE lease_token=$1 AND status='running'`, job.LeaseToken, expiresAt)
	if err != nil {
		return fmt.Errorf("renew job attempt: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return tx.Commit(ctx)
}

func (r *Repository) Complete(ctx context.Context, job Job, owner string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, job, owner); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `
		UPDATE jobs SET status='succeeded',lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,last_error=NULL,last_error_code=NULL,updated_at=clock_timestamp()
		WHERE id=$1 AND status='running' AND lease_owner=$2 AND lease_token=$3 AND isfinite(lease_expires_at) AND lease_expires_at>clock_timestamp()`, job.ID, owner, job.LeaseToken)
	if err != nil {
		return fmt.Errorf("complete job: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	result, err = tx.Exec(ctx, `UPDATE job_attempts SET status='succeeded',finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE lease_token=$1 AND status='running'`, job.LeaseToken)
	if err != nil {
		return fmt.Errorf("complete job attempt: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return tx.Commit(ctx)
}

func (r *Repository) Fail(ctx context.Context, job Job, owner string, cause error) error {
	terminal := job.Attempts >= job.MaxAttempts || !ShouldRetry(cause)
	status := "queued"
	if terminal {
		status = "failed"
	}
	delay := time.Duration(job.Attempts*job.Attempts) * time.Second
	var scheduled interface{ RetryDelay() time.Duration }
	if errors.As(cause, &scheduled) {
		delay = scheduled.RetryDelay()
	}
	errorCode := failureCode(cause)
	attemptStatus := "retry_scheduled"
	if terminal {
		attemptStatus = "failed"
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockLease(ctx, tx, job, owner); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `
		UPDATE jobs SET status=$4,last_error=$5,last_error_code=$5,available_at=clock_timestamp()+$6::interval,
		       lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp()
		WHERE id=$1 AND status='running' AND lease_owner=$2 AND lease_token=$3 AND isfinite(lease_expires_at) AND lease_expires_at>clock_timestamp()`,
		job.ID, owner, job.LeaseToken, status, errorCode, intervalLiteral(delay))
	if err != nil {
		return fmt.Errorf("fail job: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	result, err = tx.Exec(ctx, `
		UPDATE job_attempts SET status=$2,error_code=$3,finished_at=clock_timestamp(),updated_at=clock_timestamp()
		WHERE lease_token=$1 AND status='running'`, job.LeaseToken, attemptStatus, errorCode)
	if err != nil {
		return fmt.Errorf("fail job attempt: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return tx.Commit(ctx)
}

func (r *Repository) RecoverExpired(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `
		WITH expired AS (
		  SELECT id,lease_token,attempts,max_attempts FROM jobs j
		  WHERE status='running' AND lease_expires_at <= now() AND isfinite(lease_expires_at)
		    AND lease_owner IS NOT NULL AND btrim(lease_owner)<>'' AND lease_token IS NOT NULL
		    AND EXISTS(SELECT 1 FROM job_attempts a WHERE a.job_id=j.id AND a.lease_token=j.lease_token AND a.attempt_number=j.attempts AND a.status='running')
		  ORDER BY lease_expires_at,id LIMIT 100
		  FOR UPDATE SKIP LOCKED
		), attempts AS (
		  UPDATE job_attempts a SET status='lease_expired',error_code='worker_lease_expired',finished_at=now(),updated_at=now()
		  FROM expired e WHERE a.lease_token=e.lease_token AND a.status='running' RETURNING a.id
		)
		UPDATE jobs j SET status=CASE WHEN e.attempts >= e.max_attempts THEN 'failed' ELSE 'queued' END,
		       available_at=now(),lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now(),
		       last_error='worker_lease_expired',last_error_code='worker_lease_expired'
		FROM expired e WHERE j.id=e.id`)
	return err
}

// Lock before checking wall time in the following UPDATE. A time predicate in
// the locking SELECT alone can be evaluated before waiting for a row lock.
func lockLease(ctx context.Context, tx pgx.Tx, job Job, owner string) error {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM jobs WHERE id=$1 AND status='running' AND lease_owner=$2 AND lease_token=$3 FOR UPDATE`, job.ID, owner, job.LeaseToken).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	return err
}

type errorCoder interface{ ErrorCode() string }

type retryClassifier interface{ Retryable() bool }

func ShouldRetry(cause error) bool {
	if cause == nil {
		return false
	}
	var classified retryClassifier
	return !errors.As(cause, &classified) || classified.Retryable()
}

func failureCode(cause error) string {
	if cause == nil {
		return "handler_failed"
	}
	var coded errorCoder
	if errors.As(cause, &coded) {
		code := strings.TrimSpace(strings.ToLower(coded.ErrorCode()))
		if len(code) >= 3 && len(code) <= 80 {
			valid := true
			for _, char := range code {
				if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
					valid = false
					break
				}
			}
			if valid {
				return code
			}
		}
	}
	if errors.Is(cause, context.Canceled) {
		return "handler_cancelled"
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return "handler_deadline_exceeded"
	}
	if errors.Is(cause, ErrLeaseLost) {
		return "worker_lease_lost"
	}
	return "handler_failed"
}

func intervalLiteral(value time.Duration) string {
	return fmt.Sprintf("%f seconds", value.Seconds())
}
