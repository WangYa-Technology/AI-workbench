package datarights

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/jackc/pgx/v5"
)

const holdCleanupOrderBatch = 20

// ResumeLegalHoldCleanups catches both natural expiry and explicit releases,
// including historical ended holds. It dispatches only; physical cleanup always
// rechecks current retention under the original payment/subject/snapshot locks.
func (s *Service) ResumeLegalHoldCleanups(ctx context.Context, limit int) (int, error) {
	const query = `SELECT h.id,h.user_id,h.created_at FROM data_rights_legal_holds h
 LEFT JOIN legal_hold_cleanup_checks c ON c.hold_id=h.id
 WHERE (h.status='released' OR (h.status='expired' AND h.expires_at<=now())) AND c.completed_at IS NULL
 AND NOT EXISTS(SELECT 1 FROM data_rights_legal_holds current_hold WHERE current_hold.user_id=h.user_id AND current_hold.status='active' AND current_hold.expires_at>now())
 AND ($2::timestamptz IS NULL OR (h.created_at,h.id)>($2,$3::uuid))
 ORDER BY h.created_at,h.id LIMIT $1`
	return s.holdCleanup.run(ctx, s.pool, query, limit, s.resumeHoldCleanup)
}

func (s *Service) resumeHoldCleanup(ctx context.Context, holdID, userID uuid.UUID) (bool, error) {
	changed, err := s.dispatchHoldProductBatch(ctx, holdID, userID)
	if err != nil {
		return changed, err
	}
	// Each account has its own transaction. Never retain a payment lock while
	// waiting for an account lock, or lock several accounts before cleanup's own
	// product locks. Persisted cursors permit safe interleaving and restart.
	for range holdCleanupOrderBatch {
		processed, done, err := s.dispatchHeldAccount(ctx, holdID, userID)
		changed = changed || processed
		if err != nil {
			return changed, err
		}
		if done {
			break
		}
	}
	return changed, nil
}

func (s *Service) dispatchHoldProductBatch(ctx context.Context, holdID, userID uuid.UUID) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, "legal-hold-cleanup:"+holdID.String()).Scan(&locked); err != nil || !locked {
		return false, err
	}
	// No account lock here: dispatch may need payment locks first. A new hold
	// appearing after this check is authoritative at actual deletion time.
	var eligible bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM data_rights_legal_holds h WHERE h.id=$1 AND h.user_id=$2
 AND (h.status='released' OR (h.status='expired' AND h.expires_at<=now()))
 AND NOT EXISTS(SELECT 1 FROM data_rights_legal_holds current_hold WHERE current_hold.user_id=h.user_id AND current_hold.status='active' AND current_hold.expires_at>now()))`, holdID, userID).Scan(&eligible); err != nil || !eligible {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO legal_hold_cleanup_checks(hold_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, holdID, userID); err != nil {
		return false, err
	}
	var after *uuid.UUID
	var completed bool
	if err := tx.QueryRow(ctx, `SELECT after_order_id,orders_completed FROM legal_hold_cleanup_checks WHERE hold_id=$1 FOR UPDATE`, holdID).Scan(&after, &completed); err != nil {
		return false, err
	}
	if completed {
		return false, tx.Commit(ctx)
	}
	rows, err := tx.Query(ctx, `SELECT order_id FROM product_delivery_cleanup_subjects WHERE user_id=$1 AND ($2::uuid IS NULL OR order_id>$2) ORDER BY order_id LIMIT $3`, userID, after, holdCleanupOrderBatch+1)
	if err != nil {
		return false, err
	}
	var orders []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		orders = append(orders, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	more := len(orders) > holdCleanupOrderBatch
	if more {
		orders = orders[:holdCleanupOrderBatch]
	}
	for _, orderID := range orders {
		// Serialize automatic and operator dispatch without holding a user lock in
		// front of payment locks. These locks are retained until evidence commits.
		if _, err := tx.Exec(ctx, `SELECT id FROM payment_intents WHERE order_id=$1 ORDER BY id FOR UPDATE`, orderID); err != nil {
			return false, err
		}
		var needed, held bool
		var state string
		err := tx.QueryRow(ctx, `SELECT state,needed,held FROM product_delivery_cleanup_policy WHERE order_id=$1`, orderID).Scan(&state, &needed, &held)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, err
		}
		if !needed && !held && state != "removed" {
			if err := productdelivery.EnqueueCleanupTx(ctx, tx, orderID); err != nil {
				return false, err
			}
			var jobID uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT id FROM jobs WHERE kind=$1 AND payload->>'orderId'=$2 AND status='queued' ORDER BY created_at,id LIMIT 1`, productdelivery.CleanupJobKind, orderID.String()).Scan(&jobID); err != nil {
				return false, err
			}
			if err := recordHoldCleanupDispatch(ctx, tx, holdID, "product", orderID, jobID); err != nil {
				return false, err
			}
		}
	}

	if len(orders) > 0 {
		last := orders[len(orders)-1]
		after = &last
	}
	if _, err := tx.Exec(ctx, `UPDATE legal_hold_cleanup_checks SET after_order_id=$2,orders_completed=$3 WHERE hold_id=$1`, holdID, after, !more); err != nil {
		return false, err
	}

	return true, tx.Commit(ctx)
}

// Process a single source owner with no payment or other account locks held.
func (s *Service) dispatchHeldAccount(ctx context.Context, holdID, userID uuid.UUID) (bool, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback(ctx)
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, "legal-hold-cleanup:"+holdID.String()).Scan(&locked); err != nil || !locked {
		return false, true, err
	}
	var after *uuid.UUID
	var ordersDone, complete bool
	err = tx.QueryRow(ctx, `SELECT after_user_id,orders_completed,completed_at IS NOT NULL FROM legal_hold_cleanup_checks WHERE hold_id=$1 FOR UPDATE`, holdID).Scan(&after, &ordersDone, &complete)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, true, nil
	}
	if err != nil {
		return false, false, err
	}
	if !ordersDone || complete {
		return false, true, nil
	}
	var held bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM data_rights_legal_holds WHERE user_id=$1 AND status='active' AND expires_at>now())`, userID).Scan(&held); err != nil || held {
		return false, true, err
	}
	var owner uuid.UUID
	err = tx.QueryRow(ctx, originalMediaSubjectsCTE+`, owners AS (
 SELECT $1::uuid AS id UNION
 SELECT a.owner_id FROM original_media_subjects s JOIN assets a
 ON a.id IN(s.source_asset_id,s.root_asset_id)
 OR (a.storage_backend=s.storage_backend AND a.storage_key=s.storage_key)
 WHERE s.user_id=$1
 UNION
 SELECT a.owner_id FROM product_delivery_cleanup_subjects subjects
 JOIN product_order_media_sources c ON c.order_id=subjects.order_id
 JOIN assets a ON a.id IN(c.source_asset_id,c.root_asset_id)
 WHERE subjects.user_id=$1
 ) SELECT id FROM owners WHERE $2::uuid IS NULL OR id>$2 ORDER BY id LIMIT 1`, userID, after).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx, `UPDATE legal_hold_cleanup_checks SET completed_at=now() WHERE hold_id=$1`, holdID); err != nil {
			return false, false, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events(action,resource_type,resource_id,reason,request_id,metadata)
 VALUES('data_rights.hold_cleanup_scheduled','data_rights_legal_hold',$1,'Ended legal hold cleanup eligibility was reevaluated','legal-hold-cleanup',
 jsonb_build_object('dispatchCount',(SELECT count(*) FROM legal_hold_cleanup_dispatches WHERE hold_id=$1)))`, holdID); err != nil {
			return false, false, err
		}
		return true, true, tx.Commit(ctx)
	}
	if err != nil {
		return false, false, err
	}
	if err := enqueueHeldAccountCleanup(ctx, tx, holdID, owner); err != nil {
		return false, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE legal_hold_cleanup_checks SET after_user_id=$2 WHERE hold_id=$1`, holdID, owner); err != nil {
		return false, false, err
	}
	return true, false, tx.Commit(ctx)
}

func enqueueHeldAccountCleanup(ctx context.Context, tx pgx.Tx, holdID, userID uuid.UUID) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM legal_hold_cleanup_dispatches WHERE hold_id=$1 AND kind='account' AND user_id=$2)`, holdID, userID).Scan(&exists); err != nil || exists {
		return err
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&status); err != nil {
		return err
	}
	if status != "deleted" {
		return nil
	}
	var held bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM data_rights_legal_holds WHERE user_id=$1 AND status='active' AND expires_at>now())`, userID).Scan(&held); err != nil || held {
		return err
	}
	var jobID uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM jobs WHERE kind=$1 AND payload->>'userId'=$2 AND status='queued' ORDER BY created_at,id LIMIT 1`, MediaCleanupJobKind, userID.String()).Scan(&jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('userId',$2::text),20) RETURNING id`, MediaCleanupJobKind, userID).Scan(&jobID)
	}
	if err != nil {
		return err
	}
	return recordHoldCleanupDispatch(ctx, tx, holdID, "account", userID, jobID)
}

func recordHoldCleanupDispatch(ctx context.Context, tx pgx.Tx, holdID uuid.UUID, kind string, subjectID, jobID uuid.UUID) error {
	var userID, orderID *uuid.UUID
	if kind == "account" {
		userID = &subjectID
	} else {
		orderID = &subjectID
	}
	_, err := tx.Exec(ctx, `INSERT INTO legal_hold_cleanup_dispatches(hold_id,kind,user_id,order_id,job_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, holdID, kind, userID, orderID, jobID)
	return err
}
