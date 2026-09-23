package datarights

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type holdExpiryScan struct {
	mu        sync.Mutex
	afterTime *time.Time
	afterID   uuid.UUID
}

// ExpireLegalHolds reconciles a bounded batch of persisted holds, including
// legacy expired rows whose blocked deletion request was never rescheduled.
// Each subject commits independently. Busy subjects are skipped, and failures
// remain discoverable on the next pass rather than exhausting a one-shot job.
func (s *Service) ExpireLegalHolds(ctx context.Context, limit int) (int, error) {
	const query = `SELECT h.id,h.user_id,h.expires_at FROM data_rights_legal_holds h
 WHERE h.expires_at<=now() AND (h.status='active' OR (h.status='expired' AND EXISTS(
  SELECT 1 FROM data_rights_requests r WHERE r.user_id=h.user_id AND r.request_type='account_deletion' AND r.status='blocked'
 ) AND NOT EXISTS(SELECT 1 FROM data_rights_legal_holds current_hold WHERE current_hold.user_id=h.user_id
  AND current_hold.status='active' AND current_hold.expires_at>now())))
 AND ($2::timestamptz IS NULL OR (h.expires_at,h.id)>($2,$3::uuid))
 ORDER BY h.expires_at,h.id LIMIT $1`
	return s.holdExpiry.run(ctx, s.pool, query, limit, s.expireLegalHold)
}

// The progress cursor is only a fairness hint. Durable records are rechecked
// after every restart and every round of the scan.
func (scan *holdExpiryScan) run(ctx context.Context, pool *pgxpool.Pool, query string, limit int, reconcile func(context.Context, uuid.UUID, uuid.UUID) (bool, error)) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, ErrInvalid
	}
	// This cursor is a fairness hint, never durable business state. Advance past
	// busy or failing subjects, wrap at the end, and restart safely from the
	// beginning after a process restart. Concurrent processes remain safe through
	// the per-account transaction lock below.
	if !scan.mu.TryLock() {
		return 0, nil
	}
	defer scan.mu.Unlock()
	rows, err := pool.Query(ctx, query, limit, scan.afterTime, scan.afterID)
	if err != nil {
		return 0, err
	}
	type candidate struct {
		id, user  uuid.UUID
		expiresAt time.Time
	}
	var candidates []candidate
	// If the end of a previous pass was reached, wrap within this invocation.
	if !rows.Next() {
		err = rows.Err()
		rows.Close()
		if err != nil {
			return 0, err
		}
		if scan.afterTime == nil {
			return 0, nil
		}
		scan.afterTime = nil
		rows, err = pool.Query(ctx, query, limit, nil, uuid.Nil)
		if err != nil {
			return 0, err
		}
	} else {
		var item candidate
		if err := rows.Scan(&item.id, &item.user, &item.expiresAt); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, item)
	}
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.user, &item.expiresAt); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	processed := 0
	var failures []error
	for _, item := range candidates {
		if err := ctx.Err(); err != nil {
			return processed, errors.Join(append(failures, err)...)
		}
		changed, err := reconcile(ctx, item.id, item.user)
		scan.afterTime, scan.afterID = &item.expiresAt, item.id
		if err != nil {
			failures = append(failures, err)
		} else if changed {
			processed++
		}
	}
	return processed, errors.Join(failures...)
}

func (s *Service) expireLegalHold(ctx context.Context, holdID, userID uuid.UUID) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, deletionSubjectLockKey(userID)).Scan(&locked); err != nil || !locked {
		return false, err
	}
	// Share legal-hold creation's subject lock, after the lifecycle lock and
	// before the hold/request rows. Skip physical cleanup's busy user as well.
	var lockedUser uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE SKIP LOCKED`, userID).Scan(&lockedUser); errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var status string
	var expired bool
	if err := tx.QueryRow(ctx, `SELECT status,expires_at<=now() FROM data_rights_legal_holds WHERE id=$1 AND user_id=$2 FOR UPDATE`, holdID, userID).Scan(&status, &expired); errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if !expired || !oneOf(status, "active", "expired") {
		return false, nil
	}
	changed := status == "active"
	if changed {
		if err := expireHoldRecord(ctx, tx, holdID); err != nil {
			return false, err
		}
	}
	requestID, err := resumeHeldDeletion(ctx, tx, userID, holdID, nil, "legal_hold_expired", "Legal hold expired at its recorded deadline")
	if err != nil {
		return false, err
	}
	return changed || requestID != nil, tx.Commit(ctx)
}

// Caller holds the subject and hold locks, and has checked the recorded expiry.
func expireHoldRecord(ctx context.Context, tx pgx.Tx, holdID uuid.UUID) error {
	_, err := tx.Exec(ctx, `WITH expired AS (
 UPDATE data_rights_legal_holds SET status='expired' WHERE id=$1 AND status='active' AND expires_at<=now() RETURNING id,user_id,expires_at
 ) INSERT INTO audit_events(action,resource_type,resource_id,reason,request_id,metadata)
 SELECT 'data_rights.legal_hold_expired','data_rights_legal_hold',id,'Legal hold expired at its recorded deadline','data-rights-hold-expiry',
 jsonb_build_object('userId',user_id,'expiresAt',expires_at) FROM expired`, holdID)
	return err
}

// resumeHeldDeletion is shared by explicit release and natural expiry. The
// optional historical request link is not an authorization to revive a closed
// request; resolve the current blocked request under the subject lifecycle lock.
func resumeHeldDeletion(ctx context.Context, tx pgx.Tx, userID, holdID uuid.UUID, actorID *uuid.UUID, eventType, reason string) (*uuid.UUID, error) {
	var held bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM data_rights_legal_holds WHERE user_id=$1 AND status='active' AND expires_at>now())`, userID).Scan(&held); err != nil || held {
		return nil, err
	}
	var requestID uuid.UUID
	var executeAfter time.Time
	var cancelUntil *time.Time
	err := tx.QueryRow(ctx, `SELECT id,execute_after,cancel_until FROM data_rights_requests
 WHERE user_id=$1 AND request_type='account_deletion' AND status='blocked' FOR UPDATE`, userID).Scan(&requestID, &executeAfter, &cancelUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE data_rights_requests SET status='scheduled',version=version+1,updated_at=now() WHERE id=$1`, requestID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE data_rights_legal_holds SET request_id=$2 WHERE id=$1`, holdID, requestID); err != nil {
		return nil, err
	}
	availableAt := executeAfter
	if cancelUntil != nil && cancelUntil.After(availableAt) {
		availableAt = *cancelUntil
	}
	if availableAt.Before(time.Now()) {
		availableAt = time.Now()
	}
	// A running task may have committed blocked but not acknowledged completion.
	// Only reuse a queued task; counting running tasks can lose the wakeup.
	if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts,available_at)
 SELECT $1,jsonb_build_object('requestId',$2::text),5,$3 WHERE NOT EXISTS(
 SELECT 1 FROM jobs WHERE kind=$1 AND payload->>'requestId'=$2::text AND status='queued')`, DeletionJobKind, requestID, availableAt); err != nil {
		return nil, err
	}
	if err := appendEvent(ctx, tx, requestID, actorID, eventType, "blocked", "scheduled", reason, map[string]any{"holdId": holdID}); err != nil {
		return nil, err
	}
	return &requestID, nil
}
