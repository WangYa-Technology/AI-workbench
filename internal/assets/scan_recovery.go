package assets

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/accountlifecycle"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/jackc/pgx/v5"
)

// Terminal queue state alone is not enough: a matching finished attempt is
// required. Never infer failure from an old asset timestamp, a missing
// job or an unrelated failed duplicate. RecoverExpired supplies lease evidence.
const failedScanExecutionPredicate = `j.kind='asset.scan' AND j.status='failed'
 AND j.payload->>'assetId'=g.id::text
 AND j.lease_owner IS NULL AND j.lease_token IS NULL AND j.lease_expires_at IS NULL
 AND EXISTS(SELECT 1 FROM job_attempts a WHERE a.job_id=j.id AND a.job_kind=j.kind
 AND a.attempt_number=j.attempts AND a.finished_at IS NOT NULL AND isfinite(a.finished_at)
 AND a.error_code=j.last_error_code AND (a.status='failed' OR
 (a.status='lease_expired' AND a.error_code='worker_lease_expired' AND j.attempts>=j.max_attempts)))`

func (s *Service) ReconcileScanExecutions(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT g.id FROM asset_scan_executions e
 JOIN assets g ON g.id=e.asset_id JOIN jobs j ON j.id=e.job_id
 WHERE g.source_type='upload' AND g.scan_status='pending' AND e.recovery_after<=now() AND `+failedScanExecutionPredicate+`
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
		done, recoverErr := s.reconcileScanExecution(ctx, id)
		if recoverErr != nil {
			failures = append(failures, recoverErr)
			// Independent context records a safe backoff even after a pass timeout.
			// Conditional update cannot overwrite another worker's success/backoff.
			recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			_, recordErr := s.pool.Exec(recordCtx, `WITH candidate AS (
 SELECT e.asset_id FROM asset_scan_executions e
 WHERE e.asset_id=$1 AND e.recovery_after<=clock_timestamp()
 AND EXISTS(SELECT 1 FROM assets g WHERE g.id=e.asset_id AND g.source_type='upload' AND g.scan_status='pending')
 FOR UPDATE OF e SKIP LOCKED)
 UPDATE asset_scan_executions e SET recovery_after=clock_timestamp()+interval '1 minute',
 recovery_checks=recovery_checks+1,last_error_code='scan_recovery_failed'
 FROM candidate c WHERE e.asset_id=c.asset_id`, id)
			cancel()
			if recordErr != nil {
				failures = append(failures, recordErr)
			}
		} else if done {
			processed++
		} else if deferErr := s.deferBusyScanRecovery(ctx, id); deferErr != nil {
			failures = append(failures, deferErr)
		}
	}
	return processed, errors.Join(failures...)
}

func (s *Service) reconcileScanExecution(ctx context.Context, id uuid.UUID) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	// Own the recovery binding before taking any account/asset locks. Otherwise
	// a competing pass can miss our account lock and postpone this binding
	// before we reach it, causing both passes to abandon the same recovery.
	// All later account/asset/job lock attempts are nonblocking, so holding the
	// binding first cannot wait on a worker which locks it in the opposite order.
	var jobID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT job_id FROM asset_scan_executions WHERE asset_id=$1 AND recovery_after<=clock_timestamp() FOR UPDATE SKIP LOCKED`, id).Scan(&jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var owner uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT owner_id FROM assets WHERE id=$1`, id).Scan(&owner); err != nil {
		return false, err
	}
	var locked bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, accountlifecycle.Key(owner)).Scan(&locked); err != nil || !locked {
		return false, err
	}
	var status string
	err = tx.QueryRow(ctx, `SELECT owner_id,scan_status FROM assets WHERE id=$1 AND source_type='upload' FOR UPDATE SKIP LOCKED`, id).Scan(&owner, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if status != "pending" {
		return false, tx.Commit(ctx)
	}
	if err = tx.QueryRow(ctx, `SELECT id FROM jobs WHERE id=$1 FOR UPDATE SKIP LOCKED`, jobID).Scan(&jobID); errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assets g JOIN asset_scan_executions e ON e.asset_id=g.id
 JOIN jobs j ON j.id=e.job_id WHERE g.id=$1 AND `+failedScanExecutionPredicate+`)`, id).Scan(&valid)
	if err != nil || !valid {
		return false, err
	}
	if err = finishScanTx(ctx, tx, id, owner, media.ScanResult{Status: "review", ReasonCode: "scanner_execution_failed", Engine: "worker-recovery", Version: "1"}, s.scanner.Adapter()); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,reason,request_id,metadata)
 VALUES('asset.scan_recovered','asset',$1,'Original scan execution ended without a committed result','scan-recovery',jsonb_build_object('jobId',$2::text))`, id, jobID); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE asset_scan_executions SET recovery_checks=recovery_checks+1,last_error_code=NULL WHERE asset_id=$1`, id); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// A busy head must not occupy every subsequent bounded pass. Only postpone a
// still-due, evidenced failed execution; preserve its error and check history.
// Lock no account/asset/job rows here, and never wait for the binding row.
func (s *Service) deferBusyScanRecovery(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `WITH candidate AS (
 SELECT e.asset_id FROM asset_scan_executions e
 JOIN assets g ON g.id=e.asset_id JOIN jobs j ON j.id=e.job_id
 WHERE e.asset_id=$1 AND e.recovery_after<=clock_timestamp()
 AND g.source_type='upload' AND g.scan_status='pending' AND `+failedScanExecutionPredicate+`
 FOR UPDATE OF e SKIP LOCKED)
 UPDATE asset_scan_executions e SET recovery_after=clock_timestamp()+interval '1 minute'
 FROM candidate c WHERE e.asset_id=c.asset_id`, id)
	return err
}
