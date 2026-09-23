package media_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/platform/media"
)

func TestVerifiedMediaExposesOnlyFrozenBytes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := media.NewLocalStore(root)
	body := []byte("0123456789")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	stage, err := media.Stage(ctx, bytes.NewReader(body), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if err = stage.Put(ctx, store, "copy.bin", "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	if err = stage.Put(ctx, store, "copy.bin", "application/octet-stream"); !errors.Is(err, media.ErrConflict) {
		t.Fatalf("overwrite: %v", err)
	}
	rangeBytes := &media.ByteRange{Start: 2, End: 5}
	o, err := media.OpenVerified(ctx, store, "copy.bin", digest, 10, rangeBytes)
	if err != nil {
		t.Fatal(err)
	}
	// In-place tampering after verification cannot change the open response.
	if err = os.WriteFile(filepath.Join(root, "copy.bin"), []byte("XXXXXXXXXX"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(o.Body)
	closeErr := o.Body.Close()
	if err != nil || closeErr != nil || string(got) != "2345" {
		t.Fatalf("unstable response: %q %v %v", got, err, closeErr)
	}
	if _, err = media.OpenVerified(ctx, store, "copy.bin", digest, 10, rangeBytes); !errors.Is(err, media.ErrIntegrity) {
		t.Fatalf("corrupt range exposed: %v", err)
	}
	// Corruption outside the requested range must also be detected.
	if err = os.WriteFile(filepath.Join(root, "copy.bin"), []byte("012345678X"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = media.OpenVerified(ctx, store, "copy.bin", digest, 10, rangeBytes); !errors.Is(err, media.ErrIntegrity) {
		t.Fatalf("unverified suffix: %v", err)
	}
	if err = os.WriteFile(filepath.Join(root, "copy.bin"), body[:9], 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = media.OpenVerified(ctx, store, "copy.bin", digest, 10, nil); !errors.Is(err, media.ErrIntegrity) {
		t.Fatalf("truncated copy: %v", err)
	}
}

func TestVerifiedMediaBoundsAndCancellation(t *testing.T) {
	for _, body := range []string{"", "12345"} {
		if s, err := media.Stage(context.Background(), bytes.NewBufferString(body), 4); !errors.Is(err, media.ErrIntegrity) || s != nil {
			t.Fatalf("invalid size accepted: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s, err := media.Stage(ctx, bytes.NewBufferString("x"), 4); !errors.Is(err, context.Canceled) || s != nil {
		t.Fatalf("cancel ignored: %v", err)
	}
	store := media.NewLocalStore(t.TempDir())
	if err := store.PutStream(context.Background(), "invalid.bin", bytes.NewReader([]byte("wrong")), 5, "not-a-digest", "text/plain"); !errors.Is(err, media.ErrIntegrity) {
		t.Fatal(err)
	}
	if _, err := store.Stat(context.Background(), "invalid.bin"); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("invalid bytes published: %v", err)
	}
}

func TestVerifiedMediaBoundsConcurrentOpenFiles(t *testing.T) {
	var held []*media.StagedObject
	defer func() {
		for _, stage := range held {
			_ = stage.Close()
		}
	}()
	for range 64 {
		stage, err := media.Stage(t.Context(), bytes.NewBufferString("x"), 1)
		if err != nil {
			if len(held) == 0 {
				t.Fatal("staging unavailable before any capacity was used", err)
			}
			// The response owns its temporary file until Close; releasing one
			// must immediately allow another request to proceed.
			if err := held[0].Close(); err != nil {
				t.Fatal(err)
			}
			held = held[1:]
			retry, err := media.Stage(t.Context(), bytes.NewBufferString("x"), 1)
			if err != nil {
				t.Fatal("capacity was not returned on close", err)
			}
			held = append(held, retry)
			return
		}
		held = append(held, stage)
	}
	t.Fatal("64 simultaneous verified temporary files were admitted without backpressure")
}
