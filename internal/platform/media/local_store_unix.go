//go:build unix

package media

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// Pin the configured directory for each operation. All object names are single
// validated path components; no object operation resolves parent paths again.
func (s *LocalStore) directory() (*os.File, error) {
	return s.openDirectory(false)
}

func (s *LocalStore) openDirectory(initialize bool) (*os.File, error) {
	s.rootMu.Lock()
	defer s.rootMu.Unlock()
	// Only an uninitialized development root may be created. Once observed,
	// losing the root must remain an outage, including for concurrent writers.
	if initialize && s.rootIdentity == nil {
		if err := os.MkdirAll(s.root, 0o750); err != nil {
			return nil, fmt.Errorf("prepare local media storage: %w", err)
		}
	}
	fd, err := unix.Open(s.root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.Join(ErrStorageUnavailable, err)
		}
		return nil, localObjectError(err)
	}
	directory := os.NewFile(uintptr(fd), s.root)
	info, err := directory.Stat()
	if err != nil {
		_ = directory.Close()
		return nil, err
	}
	if s.rootIdentity != nil && !os.SameFile(s.rootIdentity, info) {
		_ = directory.Close()
		return nil, ErrIntegrity
	}
	if err := s.sameDirectory(directory); err != nil {
		_ = directory.Close()
		return nil, err
	}
	s.rootIdentity = info
	return directory, nil
}

func (s *LocalStore) sameDirectory(directory *os.File) error {
	opened, err := directory.Stat()
	if err != nil {
		return err
	}
	current, err := os.Stat(s.root)
	if err != nil || !os.SameFile(opened, current) {
		return ErrIntegrity
	}
	return nil
}

func localObjectError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return ErrIntegrity
	}
	return err
}

func openLocalRegular(directory *os.File, key string) (*os.File, ObjectInfo, error) {
	// NONBLOCK prevents a replaced FIFO from hanging a worker. NOFOLLOW is
	// enforced by openat itself, not by a race-prone path precheck.
	fd, err := unix.Openat(int(directory.Fd()), key, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ObjectInfo{}, localObjectError(err)
	}
	file := os.NewFile(uintptr(fd), key)
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, ObjectInfo{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || stat.Nlink != 1 {
		_ = file.Close()
		return nil, ObjectInfo{}, ErrIntegrity
	}
	return file, ObjectInfo{Size: info.Size(), LastModified: info.ModTime().UTC(),
		ETag: fmt.Sprintf(`"local-%x-%x-%x-%x"`, stat.Dev, stat.Ino, info.Size(), info.ModTime().UnixNano())}, nil
}

func (s *LocalStore) PutStream(ctx context.Context, key string, body io.ReadSeeker, size int64, digest, _ string) error {
	if !safeLocalKey(key) {
		return ErrInvalidKey
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if size < 0 || size == math.MaxInt64 || len(digest) != sha256.Size*2 {
		return ErrIntegrity
	}
	directory, err := s.openDirectory(true)
	if err != nil {
		return err
	}
	defer directory.Close()
	temporaryKey := ".media-" + rand.Text()
	fd, err := unix.Openat(int(directory.Fd()), temporaryKey, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return fmt.Errorf("create local media staging file: %w", err)
	}
	temporary := os.NewFile(uintptr(fd), temporaryKey)
	defer temporary.Close()
	defer func() { _ = unix.Unlinkat(int(directory.Fd()), temporaryKey, 0) }()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(&contextReader{ctx: ctx, reader: body}, size+1))
	if err != nil {
		return fmt.Errorf("write local media staging file: %w", err)
	}
	if n != size || hex.EncodeToString(hash.Sum(nil)) != digest {
		return ErrIntegrity
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync local media staging file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close local media staging file: %w", err)
	}
	if err := s.sameDirectory(directory); err != nil {
		return err
	}
	if err := unix.Linkat(int(directory.Fd()), temporaryKey, int(directory.Fd()), key, 0); errors.Is(err, os.ErrExist) {
		return ErrConflict
	} else if err != nil {
		return fmt.Errorf("commit local media object: %w", err)
	}
	// Complete and persist the single-link state before making the object
	// available. A crash/ambiguous commit must be inspected, never overwritten.
	if err := unix.Unlinkat(int(directory.Fd()), temporaryKey, 0); err != nil {
		return fmt.Errorf("remove local media staging link: %w", err)
	}
	if err := directory.Sync(); err != nil {
		return err
	}
	return s.sameDirectory(directory)
}

func (s *LocalStore) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	if !safeLocalKey(key) {
		return ObjectInfo{}, ErrInvalidKey
	}
	if err := ctx.Err(); err != nil {
		return ObjectInfo{}, err
	}
	directory, err := s.directory()
	if err != nil {
		return ObjectInfo{}, err
	}
	defer directory.Close()
	file, info, err := openLocalRegular(directory, key)
	if err != nil {
		return ObjectInfo{}, err
	}
	return info, file.Close()
}

func (s *LocalStore) Open(ctx context.Context, key string, requested *ByteRange) (Object, error) {
	if !safeLocalKey(key) {
		return Object{}, ErrInvalidKey
	}
	if err := ctx.Err(); err != nil {
		return Object{}, err
	}
	directory, err := s.directory()
	if err != nil {
		return Object{}, err
	}
	defer directory.Close()
	file, info, err := openLocalRegular(directory, key)
	if err != nil {
		return Object{}, err
	}
	body := io.ReadCloser(file)
	if requested != nil {
		if requested.Start < 0 || requested.End < requested.Start || requested.End >= info.Size {
			_ = file.Close()
			return Object{}, ErrInvalidKey
		}
		if _, err := file.Seek(requested.Start, io.SeekStart); err != nil {
			_ = file.Close()
			return Object{}, fmt.Errorf("seek local media object: %w", err)
		}
		body = &limitedReadCloser{Reader: io.LimitReader(file, requested.End-requested.Start+1), closer: file}
	}
	return Object{Body: body, Info: info}, nil
}

func (s *LocalStore) Delete(ctx context.Context, key string) error {
	if !safeLocalKey(key) {
		return ErrInvalidKey
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, err := s.directory()
	if err != nil {
		return err
	}
	defer directory.Close()
	file, _, err := openLocalRegular(directory, key)
	if errors.Is(err, ErrNotFound) {
		if err := directory.Sync(); err != nil {
			return err
		}
		return s.sameDirectory(directory)
	}
	if err != nil {
		return err
	}
	defer file.Close()
	if err := s.sameDirectory(directory); err != nil {
		return err
	}
	// Never unlink a directory or traverse a symlink. An unexpected link count
	// above is not absence and must not become a successful cleanup receipt.
	if err := unix.Unlinkat(int(directory.Fd()), key, 0); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete local media object: %w", err)
	}
	if err := directory.Sync(); err != nil {
		return err
	}
	return s.sameDirectory(directory)
}
