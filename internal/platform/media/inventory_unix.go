//go:build unix

package media

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

func (s *LocalStore) InventoryScope() InventoryScope {
	root, err := filepath.Abs(s.root)
	if err != nil {
		root = s.root
	}
	return InventoryScope{Backend: s.Backend(), Location: root}
}

func (s *LocalStore) WalkInventory(ctx context.Context, limit int, visit func([]InventoryEntry) error) error {
	if err := validInventoryLimit(limit); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, err := s.directory()
	if err != nil {
		return err
	}
	defer directory.Close()
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.sameDirectory(directory); err != nil {
			return err
		}
		names, readErr := directory.Readdirnames(InventoryBatchSize)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		entries := make([]InventoryEntry, 0, len(names))
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return err
			}
			count++
			if count > limit {
				return ErrInventoryLimit
			}
			// Never follow links, open FIFOs, or recurse into unknown directories.
			var stat unix.Stat_t
			if err := unix.Fstatat(int(directory.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				// A concurrent removal means this scan is incomplete, not a proven absence.
				return err
			}
			entry := InventoryEntry{Key: name, Kind: "object", Size: stat.Size}
			switch stat.Mode & unix.S_IFMT {
			case unix.S_IFREG:
				if stat.Nlink != 1 {
					entry.Kind = "hardlink"
				}
			case unix.S_IFDIR:
				entry.Kind = "directory"
			case unix.S_IFLNK:
				entry.Kind = "symlink"
			default:
				entry.Kind = "special"
			}
			if !safeLocalKey(name) || !utf8.ValidString(name) {
				entry.Kind = "invalid_key"
			}
			entries = append(entries, entry)
		}
		if len(entries) > 0 {
			if err := visit(entries); err != nil {
				return err
			}
		}
		if err := s.sameDirectory(directory); err != nil {
			return err
		}
		if errors.Is(readErr, io.EOF) {
			return ctx.Err()
		}
	}
}
