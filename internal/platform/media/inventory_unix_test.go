//go:build unix

package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLocalInventoryBoundaries(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(outside, []byte("do not read"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"normal", ".media-staging", "bad\nkey"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "directory", "not-enumerated"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(root, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(root, "hardlink")); err != nil {
		t.Fatal(err)
	}
	s := NewLocalStore(root)
	got := map[string]string{}
	err := s.WalkInventory(t.Context(), 7, func(entries []InventoryEntry) error {
		for _, entry := range entries {
			got[entry.Key] = entry.Kind
		}
		return nil
	})
	want := map[string]string{"normal": "object", ".media-staging": "object", "bad\nkey": "invalid_key", "link": "symlink", "directory": "directory", "fifo": "special", "hardlink": "hardlink"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, err)
	}
	body, err := os.ReadFile(outside)
	if err != nil || string(body) != "do not read" {
		t.Fatal("outside file changed", err)
	}
	if err = s.WalkInventory(t.Context(), 6, func([]InventoryEntry) error { return nil }); !errors.Is(err, ErrInventoryLimit) {
		t.Fatal(err)
	}
}

func TestLocalInventoryUnavailableRootAndCancellation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	s := NewLocalStore(root)
	visit := func([]InventoryEntry) error { t.Fatal("unexpected callback"); return nil }
	if err := s.WalkInventory(t.Context(), 10, visit); !errors.Is(err, ErrStorageUnavailable) {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created root", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.WalkInventory(ctx, 10, visit); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, limit := range []int{0, -1, 1000001} {
		if err := s.WalkInventory(t.Context(), limit, visit); !errors.Is(err, ErrInventoryLimit) {
			t.Fatal(err)
		}
	}
}

func TestLocalInventoryDetectsRootReplacementAndVisitorFailure(t *testing.T) {
	root := filepath.Join(t.TempDir(), "media")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	s := NewLocalStore(root)
	if err := os.WriteFile(filepath.Join(root, "one"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	want := errors.New("visitor failure")
	if err := s.WalkInventory(t.Context(), 10, func([]InventoryEntry) error { return want }); !errors.Is(err, want) {
		t.Fatal(err)
	}
	err := s.WalkInventory(t.Context(), 10, func([]InventoryEntry) error {
		if err := os.Rename(root, root+"-old"); err != nil {
			return err
		}
		return os.Mkdir(root, 0700)
	})
	if !errors.Is(err, ErrIntegrity) {
		t.Fatal(err)
	}
	if err := s.WalkInventory(t.Context(), 10, func([]InventoryEntry) error { return nil }); !errors.Is(err, ErrIntegrity) {
		t.Fatal(err)
	}
}
