package creation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/accountlifecycle"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/jackc/pgx/v5"
)

func failGenerationTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, code, message, reason string) error {
	if _, err := tx.Exec(ctx, `UPDATE generations SET status='failed',progress=0,error_code=$2,error_message=NULLIF($3,''),updated_at=now() WHERE id=$1`, id, code, message); err != nil {
		return err
	}
	if err := billing.ReleaseGenerationPointsTx(ctx, tx, id, reason); err != nil {
		return err
	}
	if err := billing.ReleaseGenerationTx(ctx, tx, id, reason); err != nil {
		return err
	}
	payload, err := json.Marshal(jobPayload{GenerationID: id})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,$2,5)`, FailureEvidenceJobKind, payload)
	return err
}

// Terminal queue state alone is not enough: a matching finished attempt is
// required. Never infer failure from an old generation timestamp, a missing
// job or an unrelated failed duplicate. RecoverExpired supplies lease evidence.
const failedExecutionPredicate = `j.kind='generation.generate' AND j.status='failed'
 AND j.payload->>'generationId'=g.id::text
 AND j.lease_owner IS NULL AND j.lease_token IS NULL AND j.lease_expires_at IS NULL
 AND EXISTS(SELECT 1 FROM job_attempts a WHERE a.job_id=j.id AND a.job_kind=j.kind
 AND a.attempt_number=j.attempts AND a.finished_at IS NOT NULL AND isfinite(a.finished_at)
 AND a.error_code=j.last_error_code AND (a.status='failed' OR
 (a.status='lease_expired' AND a.error_code='worker_lease_expired' AND j.attempts>=j.max_attempts)))`

func (s *Service) ReconcileExecutions(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT g.id FROM generation_executions e
 JOIN generations g ON g.id=e.generation_id JOIN jobs j ON j.id=e.job_id
 WHERE g.status IN ('queued','running') AND e.recovery_after<=now() AND `+failedExecutionPredicate+`
 ORDER BY e.recovery_after,g.id LIMIT $1 FOR UPDATE OF e SKIP LOCKED`, limit)
	if err != nil {
		return 0, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	processed := 0
	var failures []error
	for _, id := range ids {
		if ctx.Err() != nil {
			return processed, errors.Join(append(failures, ctx.Err())...)
		}
		done, recoverErr := s.reconcileExecution(ctx, id)
		if recoverErr != nil {
			failures = append(failures, recoverErr)
			// Independent context records a safe backoff even after a pass timeout.
			// Conditional update cannot overwrite another worker's success/backoff.
			recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			_, recordErr := s.pool.Exec(recordCtx, `WITH candidate AS (
 SELECT e.generation_id FROM generation_executions e
 WHERE e.generation_id=$1 AND e.recovery_after<=clock_timestamp()
 AND EXISTS(SELECT 1 FROM generations g WHERE g.id=e.generation_id AND g.status IN ('queued','running'))
 FOR UPDATE OF e SKIP LOCKED)
 UPDATE generation_executions e SET recovery_after=clock_timestamp()+interval '1 minute',
 recovery_checks=recovery_checks+1,last_error_code='generation_recovery_failed'
 FROM candidate c WHERE e.generation_id=c.generation_id`, id)
			cancel()
			if recordErr != nil {
				failures = append(failures, recordErr)
			}
		} else if done {
			processed++
		} else if deferErr := s.deferBusyExecutionRecovery(ctx, id); deferErr != nil {
			failures = append(failures, deferErr)
		}
	}
	return processed, errors.Join(failures...)
}

func (s *Service) reconcileExecution(ctx context.Context, id uuid.UUID) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	// Reserve the binding before account/generation locks so another pass
	// cannot postpone an in-flight recovery when it skips our account lock.
	// Every later lifecycle/entity/job lock is nonblocking: an ordinary worker
	// holding those rows never waits on us while we wait on that worker.
	var jobID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT job_id FROM generation_executions WHERE generation_id=$1 AND recovery_after<=clock_timestamp() FOR UPDATE SKIP LOCKED`, id).Scan(&jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	owner, err := generationOwner(ctx, tx, id)
	if err != nil {
		return false, err
	}
	// Skip an account currently being deleted/finalized instead of consuming the
	// entire maintenance budget waiting behind external storage I/O.
	var locked bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, accountlifecycle.Key(owner)).Scan(&locked); err != nil || !locked {
		return false, err
	}
	var account string
	err = tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR SHARE SKIP LOCKED`, owner).Scan(&account)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM generations WHERE id=$1 FOR UPDATE SKIP LOCKED`, id).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if status != "queued" && status != "running" {
		return false, tx.Commit(ctx)
	}
	if err = tx.QueryRow(ctx, `SELECT id FROM jobs WHERE id=$1 FOR UPDATE SKIP LOCKED`, jobID).Scan(&jobID); errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM generations g JOIN generation_executions e ON e.generation_id=g.id
 JOIN jobs j ON j.id=e.job_id WHERE g.id=$1 AND `+failedExecutionPredicate+`)`, id).Scan(&valid)
	if err != nil || !valid {
		return false, err
	}
	message := "The generation worker could not complete this request. Reserved points were released."
	if account == "deleted" {
		message = ""
	}
	if err = failGenerationTx(ctx, tx, id, "generation_execution_failed", message, "generation execution ended without a committed result"); err != nil {
		return false, fmt.Errorf("finalize interrupted generation: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE generation_executions SET recovery_checks=recovery_checks+1,last_error_code=NULL WHERE generation_id=$1`, id); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// A busy head must not occupy every subsequent bounded pass. Only postpone a
// still-due, evidenced failed execution; preserve its error and check history.
// Lock no account/generation/job rows here, and never wait for the binding row.
func (s *Service) deferBusyExecutionRecovery(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `WITH candidate AS (
 SELECT e.generation_id FROM generation_executions e
 JOIN generations g ON g.id=e.generation_id JOIN jobs j ON j.id=e.job_id
 WHERE e.generation_id=$1 AND e.recovery_after<=clock_timestamp()
 AND g.status IN ('queued','running') AND `+failedExecutionPredicate+`
 FOR UPDATE OF e SKIP LOCKED)
 UPDATE generation_executions e SET recovery_after=clock_timestamp()+interval '1 minute'
 FROM candidate c WHERE e.generation_id=c.generation_id`, id)
	return err
}
