package datarights

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ReconcileDeletions restores missing dispatch for a verifiable original owner
// request. Failed/cancelled jobs remain operator-controlled; live jobs suppress
// dispatch until their acknowledgement exposes the current request stage.
func (s *Service) ReconcileDeletions(ctx context.Context, limit int) (int, error) {
	const query = `SELECT request_id,user_id,created_at FROM account_deletion_reconciliation_candidates
 WHERE ($2::timestamptz IS NULL OR (created_at,request_id)>($2,$3::uuid))
 ORDER BY created_at,request_id LIMIT $1`
	return s.deletionReconciliation.run(ctx, s.pool, query, limit, s.reconcileDeletion)
}

func (s *Service) reconcileDeletion(ctx context.Context, requestID, userID uuid.UUID) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, deletionSubjectLockKey(userID)).Scan(&locked); err != nil || !locked {
		return false, err
	}
	// Cancellation, holds, recovery and both worker stages take the same account
	// lifecycle lock. Skip unrelated row contention instead of starving the batch.
	var found uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM data_rights_requests WHERE id=$1 AND user_id=$2 FOR UPDATE SKIP LOCKED`, requestID, userID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var previous *uuid.UUID
	var stage string
	err = tx.QueryRow(ctx, `SELECT previous_job_id,stage FROM account_deletion_reconciliation_candidates WHERE request_id=$1 AND user_id=$2`, requestID, userID).Scan(&previous, &stage)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var jobID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts)
 VALUES($1,jsonb_build_object('requestId',$2::text),5) RETURNING id`, DeletionJobKind, requestID).Scan(&jobID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO account_deletion_reconciliations(job_id,request_id,previous_job_id,stage) VALUES($1,$2,$3,$4)`, jobID, requestID, previous, stage); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,reason,request_id,metadata)
 VALUES('data_rights.deletion_reconciled','data_rights_request',$1,'Original owner request requires resumed deletion execution','deletion-reconciliation',
 jsonb_build_object('jobId',$2::uuid,'previousJobId',$3::uuid,'stage',$4::text))`, requestID, jobID, previous, stage); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// Bind the extra evidence check to the request, not a transient job payload.
// Every successor, including operator recovery or hold resumption, inherits it.
// Caller holds the lifecycle and request locks, and checks again after preparation.
func reconciledDeletionMayExecute(ctx context.Context, tx pgx.Tx, requestID uuid.UUID) (bool, error) {
	var reason string
	err := tx.QueryRow(ctx, `SELECT CASE WHEN EXISTS(SELECT 1 FROM account_deletion_reconciliations WHERE request_id=$1)
 THEN COALESCE((SELECT unavailable_reason FROM account_deletion_reconciliation_policy WHERE request_id=$1),'inconsistent_stage') ELSE '' END`, requestID).Scan(&reason)
	if err != nil {
		return false, err
	}
	if reason == "inconsistent_stage" {
		return false, ErrInvalid
	}
	return reason == "", nil
}
