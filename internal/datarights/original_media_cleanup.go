package datarights

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/jackc/pgx/v5"
)

// ReconcileOriginalMediaCleanups only follows proven, completed owner requests.
// Dispatch is independent of ended holds; failures remain operator-controlled.
func (s *Service) ReconcileOriginalMediaCleanups(ctx context.Context, limit int) (int, error) {
	const query = `SELECT request_id,user_id,created_at FROM original_media_cleanup_candidates
 WHERE ($2::timestamptz IS NULL OR (created_at,request_id)>($2,$3::uuid)) ORDER BY created_at,request_id LIMIT $1`
	return s.originalMediaCleanup.run(ctx, s.pool, query, limit, s.reconcileOriginalMediaCleanup)
}

func (s *Service) reconcileOriginalMediaCleanup(ctx context.Context, requestID, userID uuid.UUID) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var subject uuid.UUID
	// No storage operation, payment or asset lock here. A busy subject is skipped;
	// the physical worker subsequently acquires payments before all subjects.
	err = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE SKIP LOCKED`, userID).Scan(&subject)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var previous *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT previous_job_id FROM original_media_cleanup_candidates WHERE request_id=$1 AND user_id=$2`, requestID, userID).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var removable bool
	if err := tx.QueryRow(ctx, retainedMediaCTE+accountMediaLocationsCTE+`SELECT EXISTS(`+removableAccountMediaSelect+`
 AND c.owner_id=$1 AND NOT EXISTS(SELECT 1 FROM original_media_cleanup_receipts receipt
 WHERE receipt.owner_id=c.owner_id AND receipt.storage_backend=c.storage_backend AND receipt.storage_key_sha256=encode(public.digest(c.storage_key,'sha256'),'hex')))`, userID).Scan(&removable); err != nil || !removable {
		return false, err
	}
	var jobID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('userId',$2::text),20) RETURNING id`, MediaCleanupJobKind, userID).Scan(&jobID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO original_media_cleanup_reconciliations(job_id,request_id,user_id,previous_job_id) VALUES($1,$2,$3,$4)`, jobID, requestID, userID, previous); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,reason,request_id,metadata)
 VALUES('data_rights.original_media_cleanup_reconciled','data_rights_request',$1,'Completed deletion has removable original media without verified cleanup','original-media-cleanup',
 jsonb_build_object('jobId',$2::uuid,'previousJobId',$3::uuid))`, requestID, jobID, previous); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// The extra evidence requirement follows the account, including operator and
// hold-release successor jobs. Never trust a caller-provided payload flag.
const originalMediaReconciliationReason = `COALESCE((SELECT CASE WHEN p.unavailable_reason IS NULL THEN 'inconsistent_stage' ELSE p.unavailable_reason END
 FROM original_media_cleanup_reconciliations r LEFT JOIN original_media_cleanup_policy p ON p.request_id=r.request_id AND p.user_id=r.user_id
 WHERE r.user_id=u.id AND COALESCE(p.unavailable_reason,'inconsistent_stage')<>'' LIMIT 1),'')`

func originalMediaReconciliationMayExecute(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	var reason string
	if err := tx.QueryRow(ctx, `SELECT `+originalMediaReconciliationReason+` FROM users u WHERE u.id=$1`, userID).Scan(&reason); err != nil {
		return err
	}
	if reason == "legal_hold" {
		return ErrHoldCutoff
	}
	if reason != "" {
		return ErrInvalid
	}
	return nil
}

type originalMediaLocation struct {
	owner        uuid.UUID
	backend, key string
}

// A receipt means the managed primary location was observed absent after Delete.
// It does not attest to deletion of provider versions, backups or external copies.
func (s *Service) removeOriginalMedia(ctx context.Context, tx pgx.Tx, userID uuid.UUID, jobID, requestID *uuid.UUID, loc originalMediaLocation) error {
	store, err := s.stores.Get(loc.backend)
	if err != nil {
		return fmt.Errorf("resolve owned media storage: %w", err)
	}
	info, err := store.Stat(ctx, loc.key)
	outcome := "removed"
	var size *int64
	if errors.Is(err, media.ErrNotFound) {
		outcome = "already_absent"
	} else if err != nil {
		return fmt.Errorf("inspect owned media before cleanup: %w", err)
	} else {
		size = &info.Size
	}
	if err := media.DeleteVerified(ctx, store, loc.key); err != nil {
		return fmt.Errorf("remove and verify owned media: %w", err)
	}
	var receiptID uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO original_media_cleanup_receipts(owner_id,initiator_user_id,storage_backend,storage_key_sha256,outcome,size_bytes,job_id,request_id)
 VALUES($1,$2,$3,encode(public.digest($4::text,'sha256'),'hex'),$5,$6,$7,$8)
 ON CONFLICT(owner_id,storage_backend,storage_key_sha256) DO NOTHING RETURNING id`, loc.owner, userID, loc.backend, loc.key, outcome, size, jobID, requestID).Scan(&receiptID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,reason,request_id,metadata)
 VALUES('data_rights.original_media_cleanup_verified','original_media_cleanup_receipt',$1,'Managed primary media location verified absent after cleanup','original-media-cleanup',
 jsonb_build_object('ownerId',$2::uuid,'initiatorUserId',$3::uuid,'jobId',$4::uuid,'requestId',$5::uuid,'outcome',$6::text))`, receiptID, loc.owner, userID, jobID, requestID, outcome)
	return err
}
