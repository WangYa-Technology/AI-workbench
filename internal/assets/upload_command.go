package assets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/accountlifecycle"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/uploadwrite"
	"github.com/jackc/pgx/v5"
)

var ErrUploadKey = errors.New("invalid upload idempotency key")
var ErrUploadConflict = errors.New("upload key belongs to different content")
var uploadKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)

func ValidUploadKey(key string) bool { return uploadKeyPattern.MatchString(key) }

func uploadRequestHash(input UploadInput, base *uuid.UUID, note, mime string, data []byte) string {
	digest := sha256.Sum256(data)
	// Ordered, versioned JSON binds normalized metadata and the actual bytes,
	// including whether this is an ordinary upload or a specific version target.
	body, _ := json.Marshal([]any{"upload-v1", input.Title, input.Filename, base, note, mime, len(data), hex.EncodeToString(digest[:])})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

type uploadReservation struct {
	CommandID uuid.UUID
	Intent    uploadwrite.Intent
	Replay    *Asset
}

func (s *Service) reserveUpload(ctx context.Context, owner, asset uuid.UUID, key, hash string, store media.Store, data []byte, extension string) (uploadReservation, error) {
	var r uploadReservation
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return r, err
	}
	defer tx.Rollback(ctx)
	if err = accountlifecycle.Lock(ctx, tx, owner); err != nil {
		return r, err
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT status='active' FROM users WHERE id=$1 FOR SHARE`, owner).Scan(&active); errors.Is(err, pgx.ErrNoRows) {
		return r, ErrForbidden
	} else if err != nil {
		return r, err
	}
	if !active {
		return r, ErrForbidden
	}
	var storedHash string
	err = tx.QueryRow(ctx, `SELECT id,request_hash FROM asset_upload_commands WHERE owner_id=$1 AND idempotency_key=$2 FOR UPDATE`, owner, key).Scan(&r.CommandID, &storedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		r.CommandID = uuid.New()
		_, err = tx.Exec(ctx, `INSERT INTO asset_upload_commands(id,owner_id,idempotency_key,request_hash) VALUES($1,$2,$3,$4)`, r.CommandID, owner, key, hash)
	} else if err == nil && storedHash != hash {
		return r, ErrUploadConflict
	}
	if err != nil {
		return r, err
	}
	if r.Replay, err = uploadCommandResult(ctx, tx, r.CommandID, owner); err != nil || r.Replay != nil {
		return r, err
	}
	r.Intent, err = uploadwrite.RegisterTx(ctx, tx, owner, asset, store, data, extension)
	if err != nil {
		return r, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO asset_upload_attempts(command_id,write_id) VALUES($1,$2)`, r.CommandID, r.Intent.ID); err != nil {
		return r, err
	}
	// An uncertain COMMIT stops before Put. A retry resolves the same command and
	// uses a new attempt; old locations remain journaled for safe cleanup.
	return r, tx.Commit(ctx)
}

func uploadCommandResult(ctx context.Context, tx pgx.Tx, command, owner uuid.UUID) (*Asset, error) {
	var result *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT result_asset_id FROM asset_upload_commands WHERE id=$1 AND owner_id=$2 FOR UPDATE`, command, owner).Scan(&result); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	a, actualOwner, err := scanAsset(tx.QueryRow(ctx, assetSelect+` WHERE a.id=$1`, *result))
	if err != nil {
		return nil, err
	}
	if actualOwner != owner || a.SourceType != "upload" {
		return nil, ErrUploadConflict
	}
	a.UploadReplayed = true
	return &a, nil
}
