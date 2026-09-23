// Package productdelivery owns immutable per-order media evidence. It never
// grants purchase rights: callers must authorize the order or asset separately.
package productdelivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const MaxBytes int64 = media.MaxVerifiedBytes

const (
	FormatSingle = "single"
	FormatZIPV1  = "zip-v1"
)

var ErrUnavailable = errors.New("immutable product delivery is unavailable")

type Snapshot struct {
	OrderID                                                         uuid.UUID
	SourceBackend, SourceKey, Backend, Key, SHA256, MIMEType, State string
	Size                                                            int64
	Format                                                          string
	Manifest                                                        *BundleManifest
}

type Querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func Load(ctx context.Context, db Querier, order uuid.UUID) (Snapshot, error) {
	var s Snapshot
	var manifest []byte
	err := db.QueryRow(ctx, `SELECT d.order_id,COALESCE(d.source_backend,''),COALESCE(d.source_key,''),COALESCE(r.storage_backend,d.storage_backend),COALESCE(r.storage_key,d.storage_key),d.sha256,d.size_bytes,d.mime_type,d.state,d.format,d.file_manifest
	 FROM product_delivery_snapshots d LEFT JOIN LATERAL (
      SELECT storage_backend,storage_key FROM product_delivery_repairs
      WHERE order_id=d.order_id AND state='ready' ORDER BY revision DESC LIMIT 1
    ) r ON true WHERE d.order_id=$1`, order).Scan(&s.OrderID, &s.SourceBackend, &s.SourceKey, &s.Backend, &s.Key, &s.SHA256, &s.Size, &s.MIMEType, &s.State, &s.Format, &manifest)
	if err != nil {
		return s, err
	}
	if s.Format == FormatZIPV1 {
		s.Manifest = &BundleManifest{}
		if json.Unmarshal(manifest, s.Manifest) != nil || s.Manifest.Validate() != nil || s.MIMEType != BundleMIME || s.SourceBackend != "" || s.SourceKey != "" {
			return s, ErrUnavailable
		}
	} else if s.Format != FormatSingle || len(manifest) != 0 || s.SourceBackend == "" || s.SourceKey == "" {
		return s, ErrUnavailable
	}
	return s, nil
}

// ReserveTx is called while checkout holds its source locks, after freezing the
// contract and before committing the order. No destination write occurs here.
// The hash and reserved key commit BEFORE Ensure can create a physical object.
func ReserveTx(ctx context.Context, tx pgx.Tx, stores *media.Catalog, order uuid.UUID) error {
	if stores == nil {
		return ErrUnavailable
	}
	var backend, key, mime, sourceType string
	var bundle bool
	err := tx.QueryRow(ctx, `SELECT contract->'asset'->>'storageBackend',contract->'asset'->>'storageKey',
	 contract->'asset'->>'mimeType',contract->'asset'->>'sourceType',contract ? 'delivery' FROM product_order_contracts WHERE order_id=$1`, order).Scan(&backend, &key, &mime, &sourceType, &bundle)
	if err != nil {
		return err
	}
	if sourceType != "upload" && sourceType != "generation" {
		return ErrUnavailable
	}
	if bundle {
		return reserveBundleTx(ctx, tx, stores, order)
	}
	store, err := stores.Get(backend)
	if err != nil {
		return err
	}
	o, err := store.Open(ctx, key, nil)
	if err != nil {
		return fmt.Errorf("read delivery source: %w", err)
	}
	stage, readErr := stores.Stage(ctx, o.Body, MaxBytes)
	closeErr := o.Body.Close()
	if stage != nil {
		defer stage.Close()
	}
	if readErr != nil || closeErr != nil {
		return errors.Join(readErr, closeErr)
	}
	destination := stores.Primary()
	copyKey, err := destination.ObjectKey("delivery-" + order.String() + "-" + uuid.NewString() + ".bin")
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_delivery_snapshots(order_id,source_backend,source_key,storage_backend,storage_key,sha256,size_bytes,mime_type)
	 VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, order, backend, key, destination.Backend(), copyKey, stage.SHA256, stage.Size, mime)
	return err
}

// Ensure resumes only the originally reserved bytes. A process interruption
// after Put or an uncertain database commit leaves a tracked, verifiable object.
// A ready copy is never recreated from the seller's mutable source.
func Ensure(ctx context.Context, pool *pgxpool.Pool, stores *media.Catalog, order uuid.UUID) error {
	if stores == nil {
		return ErrUnavailable
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var required bool
	if err = tx.QueryRow(ctx, `SELECT delivery_snapshot_required FROM orders WHERE id=$1`, order).Scan(&required); err != nil {
		return err
	}
	if !required {
		return ErrUnavailable
	} // historical orders need a separate recovery decision
	if _, err = tx.Exec(ctx, `SELECT order_id FROM product_delivery_snapshots WHERE order_id=$1 FOR UPDATE`, order); err != nil {
		return err
	}
	s, err := Load(ctx, tx, order)
	if err != nil {
		return err
	}
	if s.State == "removed" {
		return ErrUnavailable
	}
	destination, err := stores.Get(s.Backend)
	if err != nil {
		return err
	}
	check := func() error {
		o, e := stores.OpenVerified(ctx, destination, s.Key, s.SHA256, s.Size, nil)
		if e != nil {
			return e
		}
		return o.Body.Close()
	}
	err = check()
	if errors.Is(err, media.ErrNotFound) && s.State == "prepared" {
		stage, readErr := s.stageOriginal(ctx, tx, stores)
		if stage != nil {
			defer stage.Close()
		}
		if readErr != nil {
			return readErr
		}
		putErr := stage.Put(ctx, destination, s.Key, s.MIMEType)
		// Even a failed request may have committed remotely. Check the frozen
		// object; never delete on uncertainty, overwrite it, or choose a new key.
		err = check()
		if err != nil {
			return errors.Join(putErr, err)
		}
	}
	if err != nil {
		return err
	}
	if s.State == "prepared" {
		if _, err = tx.Exec(ctx, `UPDATE product_delivery_snapshots SET state='ready',ready_at=now() WHERE order_id=$1`, order); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s Snapshot) Open(ctx context.Context, stores *media.Catalog, requested *media.ByteRange) (media.Object, error) {
	if s.State != "ready" || stores == nil || (s.Format != FormatSingle && s.Format != FormatZIPV1) {
		return media.Object{}, ErrUnavailable
	}
	store, err := stores.Get(s.Backend)
	if err != nil {
		return media.Object{}, err
	}
	return stores.OpenVerified(ctx, store, s.Key, s.SHA256, s.Size, requested)
}

// OpenFile is an internal read primitive, not an authorization boundary. The
// caller must enforce order ownership, active entitlement and source eligibility.
func (s Snapshot) OpenFile(ctx context.Context, stores *media.Catalog, index int, requested *media.ByteRange) (media.Object, error) {
	if s.State != "ready" || stores == nil || s.Format != FormatZIPV1 || s.Manifest == nil {
		return media.Object{}, ErrUnavailable
	}
	store, err := stores.Get(s.Backend)
	if err != nil {
		return media.Object{}, err
	}
	return OpenBundleFile(ctx, stores, store, s.Key, s.SHA256, s.Size, *s.Manifest, index, requested)
}
