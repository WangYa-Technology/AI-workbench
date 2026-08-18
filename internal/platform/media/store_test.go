package media_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/platform/media"
)

func TestLocalStoreAtomicLifecycleAndRange(t *testing.T) {
	ctx := context.Background()
	store := media.NewLocalStore(t.TempDir())
	key, err := store.ObjectKey("object.txt")
	if err != nil || key != "object.txt" {
		t.Fatalf("local object key mismatch: key=%q err=%v", key, err)
	}
	if _, err := store.ObjectKey("../object.txt"); !errors.Is(err, media.ErrInvalidKey) {
		t.Fatalf("unsafe local key was accepted: %v", err)
	}
	if err := store.Put(ctx, key, []byte("0123456789"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, key, []byte("replacement"), "text/plain"); !errors.Is(err, media.ErrConflict) {
		t.Fatalf("local object overwrite was accepted: %v", err)
	}
	info, err := store.Stat(ctx, key)
	if err != nil || info.Size != 10 || info.ETag == "" || info.LastModified.IsZero() {
		t.Fatalf("local object metadata mismatch: info=%+v err=%v", info, err)
	}
	object, err := store.Open(ctx, key, &media.ByteRange{Start: 2, End: 5})
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(object.Body)
	closeErr := object.Body.Close()
	if readErr != nil || closeErr != nil || string(body) != "2345" {
		t.Fatalf("local range mismatch: body=%q read=%v close=%v", body, readErr, closeErr)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stat(ctx, key); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("deleted local object remained available: %v", err)
	}
}
