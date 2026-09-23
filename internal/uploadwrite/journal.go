// Package uploadwrite records media locations before external writes, so
// commit acknowledgement loss and process exits cannot erase ownership evidence.
package uploadwrite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/jackc/pgx/v5"
)

var ErrClosed = errors.New("upload write is no longer pending")
var ErrRetained = errors.New("upload write is retained")
var ErrReferenced = errors.New("upload write has an unexpected reference")

type Intent struct {
	ID                   uuid.UUID
	Backend, Key, Digest string
	Size                 int64
}

// RegisterTx requires the account lifecycle lock. Commit this
// transaction successfully BEFORE starting any storage write.
func RegisterTx(ctx context.Context, tx pgx.Tx, owner, asset uuid.UUID, store media.Store, content []byte, extension string) (Intent, error) {
	i := Intent{ID: uuid.New(), Backend: store.Backend(), Size: int64(len(content))}
	var err error
	i.Key, err = store.ObjectKey("upload-" + i.ID.String() + extension)
	if err != nil {
		return Intent{}, err
	}
	digest := sha256.Sum256(content)
	i.Digest = hex.EncodeToString(digest[:])
	_, err = tx.Exec(ctx, `INSERT INTO upload_writes(id,owner_id,asset_id,storage_backend,storage_key,checksum_sha256,size_bytes)
 VALUES($1,$2,$3,$4,$5,$6,$7)`, i.ID, owner, asset, i.Backend, i.Key, i.Digest, i.Size)
	return i, err
}

// WriteTx requires the same lifecycle lock. Keep it and the intent
// row lock until result commit, including Put and byte verification. Never delete
// here: an error may mean a write or database commit succeeded without its reply.
func WriteTx(ctx context.Context, tx pgx.Tx, stores *media.Catalog, i Intent, content []byte, mime string) error {
	var pending bool
	if err := tx.QueryRow(ctx, `SELECT status='pending' AND storage_backend=$2 AND storage_key=$3 AND checksum_sha256=$4 AND size_bytes=$5
 FROM upload_writes WHERE id=$1 FOR UPDATE`, i.ID, i.Backend, i.Key, i.Digest, i.Size).Scan(&pending); err != nil {
		return err
	}
	if !pending {
		return ErrClosed
	}
	digest := sha256.Sum256(content)
	if int64(len(content)) != i.Size || hex.EncodeToString(digest[:]) != i.Digest {
		return fmt.Errorf("upload write differs from recorded intent")
	}
	store, err := stores.Get(i.Backend)
	if err != nil {
		return err
	}
	if err = store.Put(ctx, i.Key, content, mime); err != nil && !errors.Is(err, media.ErrConflict) {
		return err
	}
	// A conflicting key is reusable only when the exact recorded bytes are present.
	object, err := stores.OpenVerified(ctx, store, i.Key, i.Digest, i.Size, nil)
	if err != nil {
		return err
	}
	return object.Body.Close()
}

// AttachTx belongs to the SAME transaction as the asset, scan job and audit. The database checks the asset/owner binding.
func AttachTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	result, err := tx.Exec(ctx, `UPDATE upload_writes SET status='attached',attached_at=now(),last_error_code=NULL WHERE id=$1 AND status='pending'`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrClosed
	}
	return nil
}
