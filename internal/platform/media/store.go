package media

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
	"strings"
	"sync"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
)

var (
	ErrNotFound   = errors.New("media object not found")
	ErrConflict   = errors.New("media object already exists")
	ErrInvalidKey = errors.New("invalid media object key")
	// An inaccessible root or bucket is not evidence that an object is absent.
	ErrStorageUnavailable = errors.New("media storage location is unavailable")
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
	primary     Store
	stores      map[string]Store
	stageLimits config.MediaStageConfig
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
		catalog := NewCatalog(local)
		catalog.stageLimits = cfg.MediaStage
		return catalog
	}
	s3Store := NewS3Store(S3Config{
		Bucket: cfg.MediaS3Bucket, Region: cfg.MediaS3Region, Endpoint: cfg.MediaS3Endpoint,
		AccessKeyID: cfg.MediaS3AccessKeyID, SecretAccessKey: cfg.MediaS3SecretAccessKey,
		SessionToken: cfg.MediaS3SessionToken, PathStyle: cfg.MediaS3PathStyle, Prefix: cfg.MediaS3Prefix,
	})
	catalog := NewCatalog(s3Store, local)
	catalog.stageLimits = cfg.MediaStage
	return catalog
}

func (c *Catalog) Stage(ctx context.Context, body io.Reader, maximum int64) (*StagedObject, error) {
	return Stage(ctx, body, maximum, c.stageLimits)
}

func (c *Catalog) StageWrite(ctx context.Context, maximum int64, produce func(io.Writer) error) (*StagedObject, error) {
	return StageWrite(ctx, maximum, produce, c.stageLimits)
}

func (c *Catalog) OpenVerifiedStage(ctx context.Context, store Store, key, digest string, size int64) (*StagedObject, ObjectInfo, error) {
	return OpenVerifiedStage(ctx, store, key, digest, size, c.stageLimits)
}

func (c *Catalog) OpenVerified(ctx context.Context, store Store, key, digest string, size int64, requested *ByteRange) (Object, error) {
	return OpenVerified(ctx, store, key, digest, size, requested, c.stageLimits)
}

func (c *Catalog) Primary() Store { return c.primary }

func (c *Catalog) Get(backend string) (Store, error) {
	store := c.stores[backend]
	if store == nil {
		return nil, fmt.Errorf("media storage backend %q is unavailable", backend)
	}
	return store, nil
}

type LocalStore struct {
	root         string
	rootMu       sync.Mutex
	rootIdentity os.FileInfo
}

func NewLocalStore(root string) *LocalStore {
	s := &LocalStore{root: root}
	// Remember an existing root even before this service's first operation.
	// A cold worker must not adopt a replacement after another service wrote
	// or inspected the original directory. Missing development roots are bound
	// when first initialized; this is not a persistent identity across restarts.
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		s.rootIdentity = info
	}
	return s
}

func (s *LocalStore) Backend() string { return "local_file" }

func (s *LocalStore) ObjectKey(base string) (string, error) {
	if !safeLocalKey(base) {
		return "", ErrInvalidKey
	}
	return base, nil
}

func (s *LocalStore) Put(ctx context.Context, key string, data []byte, mime string) error {
	digest := sha256.Sum256(data)
	return s.PutStream(ctx, key, bytes.NewReader(data), int64(len(data)), hex.EncodeToString(digest[:]), mime)
}

func safeLocalKey(key string) bool {
	return key != "" && len(key) <= 255 && filepath.Base(key) == key && key != "." && key != ".." && !strings.ContainsAny(key, "/\\\x00\r\n")
}

type limitedReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r *limitedReadCloser) Close() error { return r.closer.Close() }
