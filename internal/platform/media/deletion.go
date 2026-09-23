package media

import (
	"context"
	"errors"
)

var ErrDeletionUnverified = errors.New("media deletion has not been verified")

// DeleteVerified confirms absence at the configured primary location. An
// acknowledgement, permission failure or unavailable metadata is not evidence
// that the object was removed. This does not attest to provider versions,
// backups, caches, or locations outside the configured store.
func DeleteVerified(ctx context.Context, store Store, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if store == nil {
		return ErrDeletionUnverified
	}
	if err := store.Delete(ctx, key); err != nil {
		return err
	}
	_, err := store.Stat(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return ctx.Err()
	}
	return errors.Join(ErrDeletionUnverified, err)
}
