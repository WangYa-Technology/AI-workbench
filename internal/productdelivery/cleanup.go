package productdelivery

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const CleanupJobKind = "product.delivery_cleanup"

var ErrLegalHold = errors.New("product delivery remains under legal hold")

func EnqueueCleanupTx(ctx context.Context, tx pgx.Tx, order uuid.UUID) error {
	// Share recovery's order serialization so automatic cleanup cannot race it
	// into creating another queued job. Do not coalesce with running jobs: a
	// worker may have already retained the file but not yet completed its job.
	if _, err := tx.Exec(ctx, `SELECT id FROM payment_intents WHERE order_id=$1 ORDER BY id FOR UPDATE`, order); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts)
	 SELECT $2,jsonb_build_object('orderId',order_id::text),20 FROM product_delivery_snapshots
	 WHERE order_id=$1 AND state<>'removed' AND NOT EXISTS(
	 SELECT 1 FROM jobs WHERE kind=$2 AND payload->>'orderId'=$1::text AND status='queued')`, order, CleanupJobKind)
	return err
}

// CleanupAccountTx runs inside the committed-account deletion phase, so the
// deletion receipt cannot precede removal of this buyer's unneeded copies.
func CleanupAccountTx(ctx context.Context, tx pgx.Tx, stores *media.Catalog, user uuid.UUID) error {
	rows, err := tx.Query(ctx, `SELECT d.order_id FROM product_delivery_snapshots d
 WHERE d.state<>'removed' AND EXISTS(SELECT 1 FROM product_order_media_subjects s
 WHERE s.order_id=d.order_id AND s.user_id=$1) ORDER BY d.order_id`, user)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = cleanupTx(ctx, tx, stores, id); err != nil {
			return err
		}
	}
	return nil
}

func CleanupHandler(pool *pgxpool.Pool, stores *media.Catalog) jobs.Handler {
	return func(ctx context.Context, job jobs.Job) error {
		var input struct {
			OrderID uuid.UUID `json:"orderId"`
		}
		if err := json.Unmarshal(job.Payload, &input); err != nil || input.OrderID == uuid.Nil || job.ID == uuid.Nil {
			return ErrUnavailable
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		var valid, reconciled bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE id=$1 AND kind=$2 AND payload->>'orderId'=$3),
        COALESCE((SELECT payload->>'retentionCheck'='resolved_order' FROM jobs WHERE id=$1),false) OR EXISTS(SELECT 1 FROM product_cleanup_reconciliations WHERE job_id=$1 AND order_id::text=$3)`, job.ID, CleanupJobKind, input.OrderID.String()).Scan(&valid, &reconciled); err != nil {
			return err
		}
		if !valid {
			return ErrUnavailable
		}
		if err = cleanupWithPolicyTx(ctx, tx, stores, input.OrderID, reconciled); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
}

// LockCleanupTx is shared by deletion and recovery. Payment locks precede
// subject locks (also used by legal holds), which precede the snapshot lock.
func LockCleanupTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) error {
	var err error
	// Serialize with fulfillment/refund before locking the snapshot. Legal
	// hold creation locks these same subjects through the physical delete.
	if _, err = tx.Exec(ctx, `SELECT id FROM payment_intents WHERE order_id=$1 ORDER BY id FOR UPDATE`, orderID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT u.id FROM users u WHERE u.id IN (
		 SELECT user_id FROM product_delivery_cleanup_subjects WHERE order_id=$1
		) ORDER BY u.id FOR NO KEY UPDATE`, orderID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT order_id FROM product_delivery_snapshots WHERE order_id=$1 FOR UPDATE`, orderID); err != nil {
		return err
	}
	return nil
}

func cleanupTx(ctx context.Context, tx pgx.Tx, stores *media.Catalog, orderID uuid.UUID) error {
	return cleanupWithPolicyTx(ctx, tx, stores, orderID, false)
}

func cleanupWithPolicyTx(ctx context.Context, tx pgx.Tx, stores *media.Catalog, orderID uuid.UUID, requireResolved bool) error {
	if err := LockCleanupTx(ctx, tx, orderID); err != nil {
		return err
	}
	s, err := Load(ctx, tx, orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if s.State == "removed" {
		return nil
	}
	var needed, held bool
	if err = tx.QueryRow(ctx, `SELECT needed,held FROM product_delivery_cleanup_policy WHERE order_id=$1`, orderID).Scan(&needed, &held); err != nil {
		return err
	}
	if needed {
		return nil
	}
	if held {
		return ErrLegalHold
	}
	if requireResolved {
		// A late payment/refund observation can make a previously resolved
		// order uncertain again. Do not delete on stale reconciliation evidence.
		var resolved bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_cleanup_resolved_orders WHERE order_id=$1)`, orderID).Scan(&resolved); err != nil {
			return err
		}
		if !resolved {
			return nil
		}
	}
	if stores == nil {
		return ErrUnavailable
	}
	// Include original, superseded, ready and interrupted replacement objects.
	// Their locations committed before any object write. The snapshot lock is
	// shared with repair, so cleanup cannot miss an in-flight reservation.
	rows, err := tx.Query(ctx, `SELECT storage_backend,storage_key FROM product_delivery_snapshots WHERE order_id=$1
      UNION SELECT storage_backend,storage_key FROM product_delivery_repairs WHERE order_id=$1
      ORDER BY storage_backend,storage_key`, orderID)
	if err != nil {
		return err
	}
	type location struct{ backend, key string }
	var locations []location
	for rows.Next() {
		var item location
		if err = rows.Scan(&item.backend, &item.key); err != nil {
			rows.Close()
			return err
		}
		locations = append(locations, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range locations {
		store, e := stores.Get(item.backend)
		if e != nil {
			return e
		}
		if e = media.DeleteVerified(ctx, store, item.key); e != nil {
			return e
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE product_delivery_repairs SET state='removed',removed_at=now() WHERE order_id=$1 AND state<>'removed'`, orderID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE product_delivery_snapshots SET state='removed',removed_at=now() WHERE order_id=$1`, s.OrderID); err != nil {
		return err
	}
	// A failed verification or commit retains the old state. Retry repeats
	// idempotent deletion and verifies every location, including earlier removals.
	return nil
}
