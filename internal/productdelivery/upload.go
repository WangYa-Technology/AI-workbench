package productdelivery

import (
	"context"
	"errors"
	"io"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/jackc/pgx/v5"
)

type uploadTarget struct {
	revision                  int
	backend, key, state, kind string
}

func loadUpload(ctx context.Context, db Querier, order, repair uuid.UUID) (uploadTarget, error) {
	var target uploadTarget
	err := db.QueryRow(ctx, `SELECT revision,storage_backend,storage_key,state,source_kind FROM product_delivery_repairs WHERE order_id=$1 AND id=$2`, order, repair).Scan(&target.revision, &target.backend, &target.key, &target.state, &target.kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return target, ErrRepairNotFound
	}
	if err != nil {
		return target, err
	}
	if target.kind != "upload" || (target.state != "prepared" && target.state != "ready") {
		return target, ErrRepairConflict
	}
	current, err := repairState(ctx, db, order)
	if err != nil {
		return target, err
	}
	if current.State != "ready" || !current.Needed || target.revision != current.Revision {
		return target, ErrRepairConflict
	}
	return target, nil
}

// Upload reads into an unlinked, bounded temporary file without holding a DB
// transaction. The exact accepted bytes are verified before taking business
// locks or writing a destination. After upload, permissions, revision and
// retention are checked again under the same locks as refund/cleanup.
func (s *RepairService) Upload(ctx context.Context, actor, order, repair uuid.UUID, confirmed bool, requestID string, body io.Reader) (RepairStatus, error) {
	item, err := s.upload(ctx, actor, order, repair, confirmed, requestID, body)
	return item, repairCommandError(err)
}

func (s *RepairService) upload(ctx context.Context, actor, order, repair uuid.UUID, confirmed bool, requestID string, body io.Reader) (RepairStatus, error) {
	var empty RepairStatus
	if !confirmed || body == nil || actor == uuid.Nil || order == uuid.Nil || repair == uuid.Nil {
		return empty, ErrRepairInvalid
	}
	if err := repairPermission(ctx, s.pool, actor); err != nil {
		return empty, err
	}
	if _, err := loadUpload(ctx, s.pool, order, repair); err != nil {
		return empty, err
	}
	if s.stores == nil {
		return empty, ErrUnavailable
	}
	select {
	case s.uploadSlots <- struct{}{}:
		defer func() { <-s.uploadSlots }()
	case <-ctx.Done():
		return empty, ctx.Err()
	default:
		return empty, ErrRepairBusy
	}
	snapshot, err := Load(ctx, s.pool, order)
	if err != nil {
		return empty, err
	}
	stage, err := s.stores.Stage(ctx, body, snapshot.Size)
	if err != nil {
		return empty, err
	}
	defer stage.Close()
	if stage.Size != snapshot.Size || stage.SHA256 != snapshot.SHA256 {
		return empty, media.ErrIntegrity
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	if err = repairPermission(ctx, tx, actor); err != nil {
		return empty, err
	}
	if err = LockCleanupTx(ctx, tx, order); err != nil {
		return empty, err
	}
	if err = repairPermission(ctx, tx, actor); err != nil {
		return empty, err
	}
	target, err := loadUpload(ctx, tx, order, repair)
	if err != nil {
		return empty, err
	}
	if target.state == "ready" {
		if err = tx.Commit(ctx); err != nil {
			return empty, err
		}
		return s.Inspect(ctx, actor, order)
	}
	destination, err := s.stores.Get(target.backend)
	if err != nil {
		return empty, err
	}
	check := func() error {
		obj, e := s.stores.OpenVerified(ctx, destination, target.key, snapshot.SHA256, snapshot.Size, nil)
		if e != nil {
			return e
		}
		return obj.Body.Close()
	}
	err = check()
	if errors.Is(err, media.ErrNotFound) {
		if err = pinRepairPermission(ctx, tx, actor); err != nil {
			return empty, err
		}
		putErr := stage.Put(ctx, destination, target.key, snapshot.MIMEType)
		if err = check(); err != nil {
			return empty, errors.Join(putErr, err)
		}
	} else if err != nil {
		return empty, err
	}
	if err = completeRepair(ctx, tx, actor, order, repair, target.revision, requestID); err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return s.Inspect(ctx, actor, order)
}
