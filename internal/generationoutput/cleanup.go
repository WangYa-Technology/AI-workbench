package generationoutput

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/accountlifecycle"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	pool   *pgxpool.Pool
	stores *media.Catalog
}

func NewService(pool *pgxpool.Pool, stores *media.Catalog) *Service {
	return &Service{pool: pool, stores: stores}
}

// Reconcile processes a bounded, due-ordered set. Tombstones are checked again:
// a timed-out external PUT can arrive late even after absence was observed.
// Attached objects belong to ordinary asset/contract retention and never enter
// this sweeper. Each row advances independently so one bad store cannot starve it.
func (s *Service) Reconcile(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 1000 {
		return 0, fmt.Errorf("invalid generation cleanup limit")
	}
	rows, err := s.pool.Query(ctx, `SELECT id FROM generation_output_writes WHERE status<>'attached' AND next_check_at<=now() ORDER BY next_check_at,id LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return 0, err
	}
	ids := make([]uuid.UUID, 0, limit)
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
	var failures error
	for _, id := range ids {
		if err = ctx.Err(); err != nil {
			return processed, errors.Join(failures, err)
		}
		done, err := s.reconcile(ctx, id)
		if done {
			processed++
		}
		if err != nil {
			// A timeout rolls back its cleanup transaction. Record backoff on a
			// fresh bounded context so a poison location cannot monopolize the
			// first page on every maintenance pass. Never overwrite a commit
			// whose acknowledgement was lost (its next check is already later).
			if !done {
				err = errors.Join(err, s.recordInterruptedCheck(ctx, id))
			}
			failures = errors.Join(failures, err)
		}
	}
	return processed, failures
}

func (s *Service) recordInterruptedCheck(parent context.Context, id uuid.UUID) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 2*time.Second)
	defer cancel()
	_, err := s.pool.Exec(ctx, `WITH candidate AS (SELECT id FROM generation_output_writes
 WHERE id=$1 AND status<>'attached' AND next_check_at<=clock_timestamp() FOR UPDATE SKIP LOCKED)
 UPDATE generation_output_writes w SET cleanup_checks=w.cleanup_checks+1,last_error_code='generation_output_cleanup_failed',next_check_at=clock_timestamp()+interval '1 minute'
 FROM candidate c WHERE w.id=c.id`, id)
	return err
}

func (s *Service) reconcile(ctx context.Context, id uuid.UUID) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var owner, generation uuid.UUID
	var due bool
	// A writer takes lifecycle -> generation -> intent. Cleanup takes the intent
	// without waiting, then only TRIES the earlier locks: never wait in reverse
	// order. Keep the intent locked through deferral so another cleaner cannot
	// move the deadline of an in-flight storage verification.
	if err = tx.QueryRow(ctx, `SELECT owner_id,generation_id,status<>'attached' AND next_check_at<=now() FROM generation_output_writes WHERE id=$1 FOR UPDATE SKIP LOCKED`, id).Scan(&owner, &generation, &due); errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	} else if err != nil || !due {
		return false, err
	}
	deferBusy := func() (bool, error) {
		if _, err := tx.Exec(ctx, `UPDATE generation_output_writes SET next_check_at=clock_timestamp()+interval '1 minute' WHERE id=$1`, id); err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	// Skip busy owners instead of spending the pass budget waiting on an upload.
	var acquired bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, accountlifecycle.Key(owner)).Scan(&acquired); err != nil {
		return false, err
	}
	if !acquired {
		return deferBusy()
	}
	var locked uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT id FROM generations WHERE id=$1 FOR UPDATE SKIP LOCKED`, generation).Scan(&locked); errors.Is(err, pgx.ErrNoRows) {
		return deferBusy()
	} else if err != nil {
		return false, err
	}
	cleanupErr := cleanupOneTx(ctx, tx, s.stores, id, owner)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	code := any(nil)
	delay := time.Hour
	if cleanupErr != nil {
		code = "generation_output_cleanup_failed"
		delay = time.Minute
		if errors.Is(cleanupErr, ErrRetained) {
			code = "generation_output_retained"
			delay = time.Hour
		}
		if errors.Is(cleanupErr, ErrReferenced) {
			code = "generation_output_conflict"
			delay = time.Hour
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE generation_output_writes SET cleanup_checks=cleanup_checks+1,last_error_code=$2,next_check_at=now()+$3::interval WHERE id=$1`, id, code, fmt.Sprintf("%d seconds", int64(delay.Seconds()))); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	if errors.Is(cleanupErr, ErrRetained) {
		return true, nil
	}
	return true, cleanupErr
}

// CleanupOwnerTx is invoked during deletion while holding its lifecycle lock,
// AFTER cancelling pending work. Failure prevents a completed deletion receipt.
// Stream bounded IDs, including tombstones, to recheck late writes on retries.
func CleanupOwnerTx(ctx context.Context, tx pgx.Tx, stores *media.Catalog, owner uuid.UUID) error {
	var after *uuid.UUID
	for {
		rows, err := tx.Query(ctx, `SELECT id FROM generation_output_writes WHERE owner_id=$1 AND status<>'attached' AND ($2::uuid IS NULL OR id>$2) ORDER BY id LIMIT 100 FOR UPDATE`, owner, after)
		if err != nil {
			return err
		}
		ids := make([]uuid.UUID, 0, 100)
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
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			if err = cleanupOneTx(ctx, tx, stores, id, owner); err != nil {
				return err
			}
			after = &id
		}
	}
}

func cleanupOneTx(ctx context.Context, tx pgx.Tx, stores *media.Catalog, id, owner uuid.UUID) error {
	var status, backend, key, digest string
	var size int64
	if err := tx.QueryRow(ctx, `SELECT status,storage_backend,storage_key,checksum_sha256,size_bytes FROM generation_output_writes WHERE id=$1 AND owner_id=$2 FOR UPDATE`, id, owner).Scan(&status, &backend, &key, &digest, &size); err != nil {
		return err
	}
	if status == "attached" {
		return nil
	}
	var held, referenced bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM data_rights_legal_holds WHERE user_id=$1 AND status='active' AND expires_at>now()),
 EXISTS(SELECT 1 FROM assets WHERE storage_backend=$2 AND storage_key=$3)
 OR EXISTS(SELECT 1 FROM product_order_media_sources WHERE storage_backend=$2 AND storage_key=$3)
 OR EXISTS(SELECT 1 FROM product_delivery_snapshots WHERE storage_backend=$2 AND storage_key=$3)
 OR EXISTS(SELECT 1 FROM product_delivery_repairs WHERE storage_backend=$2 AND storage_key=$3)`, owner, backend, key).Scan(&held, &referenced); err != nil {
		return err
	}
	if held {
		return ErrRetained
	}
	if referenced {
		return ErrReferenced
	}
	store, err := stores.Get(backend)
	if err != nil {
		return err
	}
	// Refuse unrecognized bytes instead of treating a coincident key as ownership.
	object, verifyErr := stores.OpenVerified(ctx, store, key, digest, size, nil)
	if verifyErr == nil {
		if err := object.Body.Close(); err != nil {
			return err
		}
	} else if errors.Is(verifyErr, media.ErrIntegrity) {
		return ErrReferenced
	} else if !errors.Is(verifyErr, media.ErrNotFound) {
		return verifyErr
	}
	if err = media.DeleteVerified(ctx, store, key); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE generation_output_writes SET status='cleaned',verified_absent_at=clock_timestamp(),next_check_at=clock_timestamp()+interval '1 hour',last_error_code=NULL WHERE id=$1`, id)
	return err
}
