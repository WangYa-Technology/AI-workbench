//go:build unix

package media_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/media"
)

type replaceRootReader struct {
	*bytes.Reader
	beforeRead func()
}

func TestLocalStoreDoesNotRecreateUnavailableRoot(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "media")
	store := media.NewLocalStore(root)
	if err := store.Put(ctx, "original.txt", []byte("retained original"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "offline-media")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, "new.txt", []byte("new upload"), "text/plain"); err == nil {
		t.Error("writer recreated unavailable storage")
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Error("writer concealed the storage outage", err)
	}
	if err := media.DeleteVerified(ctx, store, "original.txt"); err == nil {
		t.Error("recreated root falsely proved original deletion")
	}
	if body, err := os.ReadFile(filepath.Join(moved, "original.txt")); err != nil || string(body) != "retained original" {
		t.Fatal("retained original changed", err)
	}
}

func TestLocalStoreRejectsReplacementRootAndRecoversOriginal(t *testing.T) {
	for _, used := range []bool{false, true} {
		name := "before_first_operation"
		if used {
			name = "between_operations"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			root := filepath.Join(t.TempDir(), "media")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			original := []byte("original private file")
			if err := os.WriteFile(filepath.Join(root, "original.txt"), original, 0600); err != nil {
				t.Fatal(err)
			}
			store := media.NewLocalStore(root)
			if used {
				if _, err := store.Stat(ctx, "original.txt"); err != nil {
					t.Fatal(err)
				}
			}
			moved := filepath.Join(t.TempDir(), "offline-media")
			if err := os.Rename(root, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			if err := media.DeleteVerified(ctx, store, "original.txt"); !errors.Is(err, media.ErrIntegrity) {
				t.Error("empty replacement root treated as original storage", err)
			}
			if err := os.WriteFile(filepath.Join(root, "original.txt"), []byte("unrelated replacement"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Stat(ctx, "original.txt"); !errors.Is(err, media.ErrIntegrity) {
				t.Error("replacement metadata accepted", err)
			}
			if object, err := store.Open(ctx, "original.txt", nil); !errors.Is(err, media.ErrIntegrity) {
				if object.Body != nil {
					_ = object.Body.Close()
				}
				t.Error("replacement bytes exposed", err)
			}
			if err := store.Put(ctx, "new.txt", []byte("private new upload"), "text/plain"); !errors.Is(err, media.ErrIntegrity) {
				t.Error("new bytes written into replacement storage", err)
			}
			if err := store.Delete(ctx, "original.txt"); !errors.Is(err, media.ErrIntegrity) {
				t.Error("unrelated replacement deleted", err)
			}
			if body, err := os.ReadFile(filepath.Join(root, "original.txt")); err != nil || string(body) != "unrelated replacement" {
				t.Fatal("replacement content changed", err)
			}
			if err := os.Remove(filepath.Join(root, "original.txt")); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(root); err != nil {
				t.Fatal("unexpected files in replacement root", err)
			}
			if err := os.Rename(moved, root); err != nil {
				t.Fatal(err)
			}
			object, err := store.Open(ctx, "original.txt", nil)
			if err != nil {
				t.Fatal("original directory could not recover", err)
			}
			body, err := io.ReadAll(object.Body)
			_ = object.Body.Close()
			if err != nil || !bytes.Equal(body, original) {
				t.Fatal("original bytes changed", err)
			}
			if err := media.DeleteVerified(ctx, store, "original.txt"); err != nil {
				t.Fatal("restored original could not be cleaned", err)
			}
		})
	}
}

func TestLocalStoreConcurrentFirstWritesShareRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "new-media")
	store := media.NewLocalStore(root)
	var group sync.WaitGroup
	for i := range 16 {
		group.Go(func() {
			key := fmt.Sprintf("object-%d.txt", i)
			if err := store.Put(t.Context(), key, []byte(key), "text/plain"); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 16 {
		t.Fatal("concurrent initialization lost objects or left staging files", len(entries), err)
	}
}

func TestMissingLocalRootCannotProveObjectDeletion(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "media")
	store := media.NewLocalStore(root)
	if err := store.Put(ctx, "private.txt", []byte("retained original"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	moved := root + "-unavailable"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stat(ctx, "private.txt"); !errors.Is(err, media.ErrStorageUnavailable) || errors.Is(err, media.ErrNotFound) {
		t.Error("unavailable root classified as absent object", err)
	}
	if object, err := store.Open(ctx, "private.txt", nil); !errors.Is(err, media.ErrStorageUnavailable) || errors.Is(err, media.ErrNotFound) {
		if object.Body != nil {
			_ = object.Body.Close()
		}
		t.Error("unavailable root classified as absent content", err)
	}
	if err := media.DeleteVerified(ctx, store, "private.txt"); !errors.Is(err, media.ErrStorageUnavailable) {
		t.Error("unavailable root produced verified deletion")
	}
	if body, err := os.ReadFile(filepath.Join(moved, "private.txt")); err != nil || string(body) != "retained original" {
		t.Fatal("original changed while storage unavailable", err)
	}
	if err := os.Rename(moved, root); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := media.DeleteVerified(ctx, store, "private.txt"); err != nil {
			t.Fatal("deletion could not resume idempotently", err)
		}
	}
}

func (r *replaceRootReader) Read(p []byte) (int, error) {
	if r.beforeRead != nil {
		f := r.beforeRead
		r.beforeRead = nil
		f()
	}
	return r.Reader.Read(p)
}

func TestLocalStoreWriteDoesNotRetargetReplacedDirectory(t *testing.T) {
	parent := t.TempDir()
	root, moved := filepath.Join(parent, "media"), filepath.Join(parent, "old-media")
	store := media.NewLocalStore(root)
	body := []byte("private upload")
	digest := sha256.Sum256(body)
	reader := &replaceRootReader{Reader: bytes.NewReader(body), beforeRead: func() {
		if err := os.Rename(root, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
	}}
	if err := store.PutStream(context.Background(), "object.txt", reader, int64(len(body)), hex.EncodeToString(digest[:]), "text/plain"); !errors.Is(err, media.ErrIntegrity) {
		t.Fatal("directory replacement reported upload success", err)
	}
	for _, directory := range []string{root, moved} {
		entries, err := os.ReadDir(directory)
		if err != nil || len(entries) != 0 {
			t.Fatal("upload escaped or left staging data", len(entries), err)
		}
	}
}

func TestLocalStoreRejectsFilesystemAliases(t *testing.T) {
	for _, kind := range []string{"relative_symlink", "external_symlink", "dangling_symlink", "hardlink", "directory", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			outside := filepath.Join(t.TempDir(), "private.txt")
			original := filepath.Join(root, "original.txt")
			for _, name := range []string{outside, original} {
				if err := os.WriteFile(name, []byte("private original"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			alias := filepath.Join(root, "public.txt")
			var err error
			switch kind {
			case "relative_symlink":
				err = os.Symlink("original.txt", alias)
			case "external_symlink":
				err = os.Symlink(outside, alias)
			case "dangling_symlink":
				err = os.Symlink("missing.txt", alias)
			case "hardlink":
				err = os.Link(original, alias)
			case "directory":
				err = os.Mkdir(alias, 0700)
			case "fifo":
				err = syscall.Mkfifo(alias, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			store := media.NewLocalStore(root)
			ctx := context.Background()
			if _, err := store.Stat(ctx, "public.txt"); !errors.Is(err, media.ErrIntegrity) {
				t.Fatalf("unsafe metadata accepted: %v", err)
			}
			for _, requested := range []*media.ByteRange{nil, {Start: 0, End: 3}} {
				object, err := store.Open(ctx, "public.txt", requested)
				if err == nil {
					_ = object.Body.Close()
				}
				if !errors.Is(err, media.ErrIntegrity) || object.Body != nil {
					t.Fatalf("unsafe media exposed: err=%v bodyReturned=%t", err, object.Body != nil)
				}
			}
			if err := store.Delete(ctx, "public.txt"); !errors.Is(err, media.ErrIntegrity) {
				t.Fatalf("unsafe cleanup reported success: %v", err)
			}
			if _, err := os.Lstat(alias); err != nil {
				t.Fatal("cleanup removed the suspicious entry", err)
			}
			for _, name := range []string{outside, original} {
				body, err := os.ReadFile(name)
				if err != nil || string(body) != "private original" {
					t.Fatal("source changed", err)
				}
			}
			if kind == "hardlink" {
				if err := store.Delete(ctx, "original.txt"); !errors.Is(err, media.ErrIntegrity) {
					t.Fatal("cleanup made the other alias readable", err)
				}
			}
		})
	}
}

func TestLocalStoreReplacementChangesIdentity(t *testing.T) {
	root := t.TempDir()
	store := media.NewLocalStore(root)
	ctx := context.Background()
	path := filepath.Join(root, "object.txt")
	if err := store.Put(ctx, "object.txt", []byte("original"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	before, err := store.Stat(ctx, "object.txt")
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(root, "replacement.txt")
	if err := os.WriteFile(replacement, []byte("replaced"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, before.LastModified, before.LastModified); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	object, err := store.Open(ctx, "object.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer object.Body.Close()
	if object.Info.ETag == before.ETag {
		t.Fatal("replacement reused the old object identity")
	}
	body, err := io.ReadAll(object.Body)
	if err != nil || string(body) != "replaced" {
		t.Fatal("regular replacement could not be read", err)
	}
}

func TestLocalStoreLeafReplacementCannotFollowSymlink(t *testing.T) {
	root := t.TempDir()
	store := media.NewLocalStore(root)
	outside := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(outside, []byte("private outside root"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stop := make(chan struct{})
	done := make(chan struct{})
	started := make(chan struct{})
	mutationError := make(chan error, 1)
	go func() {
		defer close(done)
		first := true
		for {
			select {
			case <-stop:
				return
			default:
			}
			// Rename atomically swaps the leaf; there is no directory race in
			// this test. The configured root belongs to the service operator.
			if err := os.WriteFile(filepath.Join(root, "next-regular"), []byte("public sample"), 0600); err != nil {
				mutationError <- err
				return
			}
			if err := os.Rename(filepath.Join(root, "next-regular"), filepath.Join(root, "sample.txt")); err != nil {
				mutationError <- err
				return
			}
			if err := os.Symlink(outside, filepath.Join(root, "next-link")); err != nil {
				mutationError <- err
				return
			}
			if err := os.Rename(filepath.Join(root, "next-link"), filepath.Join(root, "sample.txt")); err != nil {
				mutationError <- err
				return
			}
			if first {
				close(started)
				first = false
			}
		}
	}()
	defer func() {
		close(stop)
		<-done
		select {
		case err := <-mutationError:
			t.Error("filesystem mutation failed", err)
		default:
		}
	}()
	select {
	case <-started:
	case <-done:
		t.Fatal("filesystem mutation never started")
	}
	for i := 0; i < 500; i++ {
		object, err := store.Open(ctx, "sample.txt", nil)
		if err != nil {
			if !errors.Is(err, media.ErrIntegrity) && !errors.Is(err, media.ErrNotFound) {
				t.Fatal(err)
			}
			continue
		}
		body, readErr := io.ReadAll(object.Body)
		_ = object.Body.Close()
		if readErr != nil || string(body) != "public sample" {
			t.Fatalf("leaf replacement escaped local boundary: %q %v", body, readErr)
		}
	}
}
