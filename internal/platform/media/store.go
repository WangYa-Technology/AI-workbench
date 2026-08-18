package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
)

var (
	ErrNotFound   = errors.New("media object not found")
	ErrConflict   = errors.New("media object already exists")
	ErrInvalidKey = errors.New("invalid media object key")
)

type ByteRange struct {
	Start int64
	End   int64
}

type ObjectInfo struct {
	Size         int64
	LastModified time.Time
	ETag         string
}

type Object struct {
	Body io.ReadCloser
	Info ObjectInfo
}

type Store interface {
	Backend() string
	ObjectKey(base string) (string, error)
	Put(context.Context, string, []byte, string) error
	Stat(context.Context, string) (ObjectInfo, error)
	Open(context.Context, string, *ByteRange) (Object, error)
	Delete(context.Context, string) error
}

type Catalog struct {
	primary Store
	stores  map[string]Store
}

func NewCatalog(primary Store, additional ...Store) *Catalog {
	stores := make(map[string]Store, len(additional)+1)
	stores[primary.Backend()] = primary
	for _, store := range additional {
		if store != nil {
			stores[store.Backend()] = store
		}
	}
	return &Catalog{primary: primary, stores: stores}
}

func NewCatalogFromConfig(cfg config.Config) *Catalog {
	local := NewLocalStore(cfg.MediaRoot)
	if cfg.MediaStorageAdapter != "s3" {
		return NewCatalog(local)
	}
	s3Store := NewS3Store(S3Config{
		Bucket: cfg.MediaS3Bucket, Region: cfg.MediaS3Region, Endpoint: cfg.MediaS3Endpoint,
		AccessKeyID: cfg.MediaS3AccessKeyID, SecretAccessKey: cfg.MediaS3SecretAccessKey,
		SessionToken: cfg.MediaS3SessionToken, PathStyle: cfg.MediaS3PathStyle, Prefix: cfg.MediaS3Prefix,
	})
	return NewCatalog(s3Store, local)
}

func (c *Catalog) Primary() Store { return c.primary }

func (c *Catalog) Get(backend string) (Store, error) {
	store := c.stores[backend]
	if store == nil {
		return nil, fmt.Errorf("media storage backend %q is unavailable", backend)
	}
	return store, nil
}

type LocalStore struct{ root string }

func NewLocalStore(root string) *LocalStore { return &LocalStore{root: root} }

func (s *LocalStore) Backend() string { return "local_file" }

func (s *LocalStore) ObjectKey(base string) (string, error) {
	if !safeLocalKey(base) {
		return "", ErrInvalidKey
	}
	return base, nil
}

func (s *LocalStore) Put(_ context.Context, key string, data []byte, _ string) error {
	if !safeLocalKey(key) {
		return ErrInvalidKey
	}
	if err := os.MkdirAll(s.root, 0o750); err != nil {
		return fmt.Errorf("prepare local media storage: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".media-*")
	if err != nil {
		return fmt.Errorf("create local media staging file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write local media staging file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync local media staging file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close local media staging file: %w", err)
	}
	finalPath := filepath.Join(s.root, key)
	if err := os.Link(temporaryPath, finalPath); errors.Is(err, os.ErrExist) {
		return ErrConflict
	} else if err != nil {
		return fmt.Errorf("commit local media object: %w", err)
	}
	return nil
}

func (s *LocalStore) Stat(_ context.Context, key string) (ObjectInfo, error) {
	path, err := s.path(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ObjectInfo{}, ErrNotFound
	}
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("stat local media object: %w", err)
	}
	return ObjectInfo{Size: info.Size(), LastModified: info.ModTime().UTC(), ETag: fmt.Sprintf("\"local-%x-%x\"", info.Size(), info.ModTime().UnixNano())}, nil
}

func (s *LocalStore) Open(_ context.Context, key string, requested *ByteRange) (Object, error) {
	path, err := s.path(key)
	if err != nil {
		return Object{}, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Object{}, ErrNotFound
	}
	if err != nil {
		return Object{}, fmt.Errorf("open local media object: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return Object{}, fmt.Errorf("stat open local media object: %w", err)
	}
	body := io.ReadCloser(file)
	if requested != nil {
		if requested.Start < 0 || requested.End < requested.Start || requested.End >= info.Size() {
			_ = file.Close()
			return Object{}, ErrInvalidKey
		}
		if _, err := file.Seek(requested.Start, io.SeekStart); err != nil {
			_ = file.Close()
			return Object{}, fmt.Errorf("seek local media object: %w", err)
		}
		body = &limitedReadCloser{Reader: io.LimitReader(file, requested.End-requested.Start+1), closer: file}
	}
	return Object{Body: body, Info: ObjectInfo{Size: info.Size(), LastModified: info.ModTime().UTC(), ETag: fmt.Sprintf("\"local-%x-%x\"", info.Size(), info.ModTime().UnixNano())}}, nil
}

func (s *LocalStore) Delete(_ context.Context, key string) error {
	path, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete local media object: %w", err)
	}
	return nil
}

func (s *LocalStore) path(key string) (string, error) {
	if !safeLocalKey(key) {
		return "", ErrInvalidKey
	}
	return filepath.Join(s.root, key), nil
}

func safeLocalKey(key string) bool {
	return key != "" && len(key) <= 255 && filepath.Base(key) == key && key != "." && key != ".." && !strings.ContainsAny(key, "/\\\x00\r\n")
}

type limitedReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r *limitedReadCloser) Close() error { return r.closer.Close() }
