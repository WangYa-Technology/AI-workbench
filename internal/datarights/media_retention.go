package datarights

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/jackc/pgx/v5"
)

const MediaCleanupJobKind = "data_rights.media_cleanup"

// EnqueueProductMediaCleanupTx reevaluates deleted owners after a refund or a
// verified unpaid checkout closure commits. Active owners keep their original files.
func EnqueueProductMediaCleanupTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) error {
	if err := productdelivery.EnqueueCleanupTx(ctx, tx, orderID); err != nil {
		return err
	}
	// Serialize with account deletion's source locks. Otherwise a refund could
	// see an active seller while deletion still sees the pre-refund entitlement,
	// leaving both transactions convinced the other will schedule cleanup.
	if _, err := tx.Exec(ctx, `SELECT a.id FROM assets a WHERE a.id IN (
		SELECT c.source_asset_id FROM product_order_media_sources c WHERE c.order_id=$1
		UNION SELECT c.root_asset_id FROM product_order_media_sources c WHERE c.order_id=$1
		UNION SELECT purchased.origin_asset_id FROM entitlements e JOIN assets purchased ON purchased.id=e.asset_id
		  WHERE e.order_id=$1
	) ORDER BY a.id FOR UPDATE OF a`, orderID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts)
		SELECT $2,jsonb_build_object('userId',owner.id::text),20
		FROM users owner WHERE owner.status='deleted' AND owner.id IN (
		  SELECT user_id FROM product_order_media_subjects WHERE order_id=$1
		  UNION
		  SELECT source.owner_id FROM entitlements e JOIN assets purchased ON purchased.id=e.asset_id
		    JOIN assets source ON source.id=purchased.origin_asset_id WHERE e.order_id=$1
		)`, orderID, MediaCleanupJobKind)
	return err
}

func (s *Service) HandleMediaCleanupJob(ctx context.Context, job jobs.Job) error {
	var payload struct {
		UserID uuid.UUID `json:"userId"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.UserID == uuid.Nil || job.ID == uuid.Nil {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var valid bool
	// Receipts reference this job. Acquire the FK lock before payments/subjects
	// so an operator holding the original job cannot form job -> user -> job.
	if err := tx.QueryRow(ctx, `SELECT true FROM jobs WHERE id=$1 AND kind=$2 AND payload->>'userId'=$3 FOR KEY SHARE`, job.ID, MediaCleanupJobKind, payload.UserID.String()).Scan(&valid); errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalid
	} else if err != nil {
		return err
	}
	if !valid {
		return ErrInvalid
	}
	if err := lockAccountCleanupPayments(ctx, tx, payload.UserID); err != nil {
		return err
	}
	if err := lockAccountCleanupSubjects(ctx, tx, payload.UserID); err != nil {
		return err
	}
	// CreateHold takes this same subject lock. Keep it through physical cleanup
	// so a hold committed before cleanup cannot be missed by a later delete.
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1`, payload.UserID).Scan(&status); err != nil {
		return err
	}
	if status != "deleted" {
		return ErrInvalid
	}
	var held bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM data_rights_legal_holds WHERE user_id=$1 AND status='active' AND expires_at>now())`, payload.UserID).Scan(&held); err != nil {
		return err
	}
	if held {
		return ErrHoldCutoff
	}
	if err := s.cleanupDeletedAccountMedia(ctx, tx, payload.UserID, &job.ID, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Both callers hold related payments, then all media subjects, before entering.
func (s *Service) cleanupDeletedAccountMedia(ctx context.Context, tx pgx.Tx, userID uuid.UUID, jobID, requestID *uuid.UUID) error {
	var deleted bool
	if err := tx.QueryRow(ctx, `SELECT status='deleted' FROM users WHERE id=$1`, userID).Scan(&deleted); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if !deleted {
		return ErrInvalid
	}
	if err := originalMediaReconciliationMayExecute(ctx, tx, userID); err != nil {
		return err
	}
	if err := prepareMediaDeletion(ctx, tx, userID); err != nil {
		return err
	}
	if err := preserveContractedAssetState(ctx, tx, userID); err != nil {
		return err
	}
	return s.removeOwnedMedia(ctx, tx, userID, jobID, requestID)
}

// Account cleanup can reach both this buyer's copies and originals owned by a
// deleted seller. Lock the entire related payment set in stable order before
// any user, product or source-asset lock: fulfillment/refunds and copy cleanup
// already acquire payments first. Contracts and purchase origins, not the
// current product owner, determine which historical media may be touched.
func lockAccountCleanupPayments(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT p.id FROM payment_intents p JOIN orders o ON o.id=p.order_id
 WHERE p.purpose='product' AND (o.buyer_id=$1 OR EXISTS(
  SELECT 1 FROM product_order_media_subjects s WHERE s.order_id=o.id AND s.user_id=$1
 ) OR EXISTS(
  SELECT 1 FROM entitlements e JOIN assets purchased ON purchased.id=e.asset_id
  JOIN assets source ON source.id=purchased.origin_asset_id
  WHERE e.order_id=o.id AND source.owner_id=$1
 )) ORDER BY p.id FOR UPDATE OF p`, userID)
	return err
}

// Original media can belong to another account, or be shared by aliases. Frozen
// contract locations remain evidence even when the source asset moves. Include
// legacy purchases and task grants that have no independent delivery snapshot.
// This relation is shared by retention, subject locks and ended-hold dispatch.
const originalMediaSubjectsCTE = `WITH original_media_subjects AS (
 SELECT a.id AS source_asset_id,a.id AS root_asset_id,a.storage_backend,a.storage_key,a.owner_id AS user_id
 FROM assets a
 UNION
 SELECT a.id,a.id,a.storage_backend,a.storage_key,subject.user_id
 FROM task_delivery_grants g JOIN assets a ON a.id=g.source_asset_id
 CROSS JOIN LATERAL (VALUES(g.client_id),(g.creator_id)) subject(user_id)
 UNION
 SELECT c.source_asset_id,c.root_asset_id,c.storage_backend,c.storage_key,subject.user_id
 FROM product_order_media_sources c JOIN product_order_media_subjects subject ON subject.order_id=c.order_id
 UNION
 SELECT source.id,source.id,source.storage_backend,source.storage_key,e.user_id
 FROM entitlements e JOIN assets purchased ON purchased.id=e.asset_id
 JOIN assets source ON source.id=purchased.origin_asset_id
 WHERE NOT EXISTS(SELECT 1 FROM product_order_contracts c WHERE c.order_id=e.order_id)
) `

// Deliberately include active counterpart owners as well: a subject may finish
// deletion while we wait for its row. Re-evaluate candidate eligibility only
// after acquiring all these locks in one global order, never our own row first.
const accountMediaLocationsCTE = `, account_media_locations AS (
 SELECT a.storage_backend,a.storage_key,a.owner_id FROM assets a
 WHERE a.source_type IN ('generation','upload') AND (a.owner_id=$1 OR
  EXISTS(SELECT 1 FROM task_delivery_grants g WHERE g.source_asset_id=a.id AND g.client_id=$1) OR
  EXISTS(SELECT 1 FROM assets purchased JOIN entitlements e ON e.asset_id=purchased.id
   WHERE purchased.origin_asset_id=a.id AND e.user_id=$1) OR
  EXISTS(SELECT 1 FROM product_order_media_sources c JOIN orders o ON o.id=c.order_id
   WHERE a.id IN(c.source_asset_id,c.root_asset_id) AND o.buyer_id=$1))
 UNION
 SELECT c.storage_backend,c.storage_key,COALESCE(root.owner_id,c.accepted_owner_id)
 FROM product_order_media_sources c LEFT JOIN assets root ON root.id=c.root_asset_id JOIN orders o ON o.id=c.order_id
 WHERE c.source_type IN ('generation','upload')
 AND (COALESCE(root.owner_id,c.accepted_owner_id)=$1 OR c.accepted_owner_id=$1 OR o.buyer_id=$1)
) `

func lockAccountCleanupSubjects(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	// NO KEY UPDATE still serializes holds/status changes, but permits FK key
	// shares: a concurrent hold's operator can be another related media owner.
	// FOR UPDATE here would wait for the hold while blocking its created_by FK.
	_, err := tx.Exec(ctx, originalMediaSubjectsCTE+accountMediaLocationsCTE+`
 SELECT u.id FROM users u WHERE u.id IN (
  SELECT $1::uuid
  UNION SELECT s.user_id FROM original_media_subjects s JOIN account_media_locations l
   ON l.storage_backend=s.storage_backend AND l.storage_key=s.storage_key
  UNION SELECT s.user_id FROM product_delivery_cleanup_subjects s
   WHERE EXISTS(SELECT 1 FROM product_order_media_subjects mine WHERE mine.order_id=s.order_id AND mine.user_id=$1)
 ) ORDER BY u.id FOR NO KEY UPDATE OF u`, userID)
	return err
}

// $1 is the account being erased. Unsettled checkouts/refunds remain obligations
// after access revocation. The shared funds view protects both independent
// deliveries and historical originals without manufacturing missing contracts.
const retainedMediaCTE = originalMediaSubjectsCTE + `, retained_media AS (
	SELECT s.source_asset_id,s.root_asset_id,s.storage_backend,s.storage_key
	FROM original_media_subjects s WHERE EXISTS(
	 SELECT 1 FROM data_rights_legal_holds h WHERE h.user_id=s.user_id AND h.status='active' AND h.expires_at>now())
	UNION
	SELECT a.id,a.id,a.storage_backend,a.storage_key FROM assets a JOIN users owner ON owner.id=a.owner_id
	WHERE a.owner_id<>$1 AND owner.status<>'deleted' AND a.source_type IN ('generation','upload')
	UNION
	SELECT tg.source_asset_id, tg.source_asset_id AS root_asset_id,
	       a.storage_backend, a.storage_key
	FROM task_delivery_grants tg JOIN assets a ON a.id=tg.source_asset_id
	JOIN users recipient ON recipient.id=tg.client_id
	WHERE tg.client_id<>$1 AND recipient.status<>'deleted'
	UNION
	SELECT c.source_asset_id,c.root_asset_id,
	       COALESCE(d.storage_backend,c.storage_backend),COALESCE(d.storage_key,c.storage_key)
	FROM product_order_media_sources c JOIN orders o ON o.id=c.order_id
 LEFT JOIN product_delivery_snapshots d ON d.order_id=o.id AND d.state='ready'
	JOIN users buyer ON buyer.id=o.buyer_id
	WHERE o.buyer_id<>$1 AND buyer.status<>'deleted' AND (
	  EXISTS(SELECT 1 FROM entitlements e WHERE e.order_id=o.id AND e.status='active')
	  OR EXISTS(SELECT 1 FROM payment_intents pi WHERE pi.order_id=o.id AND pi.purpose='product'
	    AND pi.status IN ('checkout_pending','checkout_open')))
	UNION
	SELECT c.source_asset_id,c.root_asset_id,r.storage_backend,r.storage_key
	FROM product_delivery_repairs r JOIN product_order_media_sources c ON c.order_id=r.order_id
	JOIN product_delivery_cleanup_policy p ON p.order_id=r.order_id
	WHERE r.state<>'removed' AND (p.needed OR p.held)
	UNION
	SELECT c.source_asset_id,c.root_asset_id,
	       COALESCE(d.storage_backend,c.storage_backend),COALESCE(d.storage_key,c.storage_key)
	FROM product_order_media_sources c
	LEFT JOIN product_delivery_snapshots d ON d.order_id=c.order_id AND d.state='ready'
	WHERE EXISTS(SELECT 1 FROM product_order_funds_retention f WHERE f.order_id=c.order_id)
	UNION
	SELECT source.id,source.id,source.storage_backend,source.storage_key
	FROM entitlements e JOIN assets purchased ON purchased.id=e.asset_id
	JOIN assets source ON source.id=purchased.origin_asset_id
	JOIN users buyer ON buyer.id=e.user_id
	WHERE ((e.user_id<>$1 AND buyer.status<>'deleted' AND e.status='active')
	  OR EXISTS(SELECT 1 FROM product_order_funds_retention f WHERE f.order_id=e.order_id))
	  AND NOT EXISTS(SELECT 1 FROM product_order_contracts c WHERE c.order_id=e.order_id)
) `

func prepareMediaDeletion(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	// Checkout locks products before source assets. Withdraw supply first, then
	// wait for earlier contracts before evaluating retention in a fresh statement.
	if _, err := tx.Exec(ctx, `UPDATE products SET status='removed',description='[Deleted by account owner]',updated_at=now() WHERE seller_id=$1`, userID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `SELECT a.id FROM assets a WHERE a.owner_id=$1 ORDER BY a.id FOR UPDATE OF a`, userID)
	return err
}

func preserveContractedAssetState(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	_, err := tx.Exec(ctx, retainedMediaCTE+`
	UPDATE assets a SET scan_status=CASE WHEN EXISTS(
	  SELECT 1 FROM retained_media r WHERE a.id IN (r.source_asset_id,r.root_asset_id)
	    OR (a.storage_backend=r.storage_backend AND a.storage_key=r.storage_key))
	  THEN a.scan_status ELSE 'rejected' END,
	  uploaded_filename=NULL,version_note=NULL,
	  scan_reason='Account deletion completed; required contract media retained.',scanned_at=now()
	WHERE a.owner_id=$1`, userID)
	return err
}

const removableAccountMediaSelect = `SELECT DISTINCT c.owner_id,c.storage_backend,c.storage_key FROM account_media_locations c
	JOIN users owner ON owner.id=c.owner_id
	WHERE (c.owner_id=$1 OR owner.status='deleted') AND NOT EXISTS(SELECT 1 FROM retained_media r
	  WHERE r.storage_backend=c.storage_backend AND r.storage_key=c.storage_key)
 AND NOT EXISTS(SELECT 1 FROM product_delivery_snapshots d
   WHERE d.storage_backend=c.storage_backend AND d.storage_key=c.storage_key)
 AND NOT EXISTS(SELECT 1 FROM product_delivery_repairs r
   WHERE r.storage_backend=c.storage_backend AND r.storage_key=c.storage_key)
 AND NOT EXISTS(SELECT 1 FROM original_media_cleanup_reconciliations proof LEFT JOIN original_media_cleanup_policy policy
 ON policy.request_id=proof.request_id AND policy.user_id=proof.user_id
 WHERE proof.user_id=c.owner_id AND COALESCE(policy.unavailable_reason,'inconsistent_stage')<>'')`

func (s *Service) removeOwnedMedia(ctx context.Context, tx pgx.Tx, userID uuid.UUID, jobID, requestID *uuid.UUID) error {
	var afterBackend, afterKey *string
	var afterOwner *uuid.UUID
	for {
		// Bound memory and close the result stream before recording each physical
		// outcome on this transaction. Subject/source locks span all batches.
		rows, err := tx.Query(ctx, retainedMediaCTE+accountMediaLocationsCTE+removableAccountMediaSelect+`
 AND ($2::text IS NULL OR (c.storage_backend,c.storage_key,c.owner_id)>($2,$3,$4::uuid))
 ORDER BY c.storage_backend,c.storage_key,c.owner_id LIMIT 100`, userID, afterBackend, afterKey, afterOwner)
		if err != nil {
			return err
		}
		var locations []originalMediaLocation
		for rows.Next() {
			var loc originalMediaLocation
			if err := rows.Scan(&loc.owner, &loc.backend, &loc.key); err != nil {
				rows.Close()
				return err
			}
			locations = append(locations, loc)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(locations) == 0 {
			break
		}
		for _, loc := range locations {
			if err := s.removeOriginalMedia(ctx, tx, userID, jobID, requestID, loc); err != nil {
				return err
			}
		}
		last := locations[len(locations)-1]
		afterBackend, afterKey, afterOwner = &last.backend, &last.key, &last.owner
	}
	return productdelivery.CleanupAccountTx(ctx, tx, s.stores, userID)
}
