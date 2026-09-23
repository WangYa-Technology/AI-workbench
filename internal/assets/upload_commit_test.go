package assets_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/accountlifecycle"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Drop a successful server COMMIT frame, not the request: database state is
// committed even though the client's Commit method returns an I/O error.
type uploadCommitDropConn struct {
	net.Conn
	armed, dropped *atomic.Bool
	pending        []byte
}

func (c *uploadCommitDropConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(c.pending) == 0 {
		var header [5]byte
		if _, err := io.ReadFull(c.Conn, header[:]); err != nil {
			return 0, err
		}
		length := int(binary.BigEndian.Uint32(header[1:]))
		if length < 4 || length > 16*1024*1024 {
			return 0, fmt.Errorf("invalid test frame")
		}
		frame := make([]byte, length+1)
		copy(frame, header[:])
		if _, err := io.ReadFull(c.Conn, frame[5:]); err != nil {
			return 0, err
		}
		if frame[0] == 'C' && string(frame[5:]) == "COMMIT\x00" && c.armed.CompareAndSwap(true, false) {
			c.dropped.Store(true)
			c.Conn.Close()
			return 0, io.ErrUnexpectedEOF
		}
		c.pending = frame
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

type uploadTrackingStore struct {
	media.Store
	afterPut      func()
	puts, deletes atomic.Int32
	key           string
}

func (s *uploadTrackingStore) Put(ctx context.Context, key string, b []byte, mime string) error {
	s.puts.Add(1)
	s.key = key
	if err := s.Store.Put(ctx, key, b, mime); err != nil {
		return err
	}
	if s.afterPut != nil {
		s.afterPut()
	}
	return nil
}
func (s *uploadTrackingStore) Delete(ctx context.Context, key string) error {
	s.deletes.Add(1)
	return s.Store.Delete(ctx, key)
}

func TestUploadLostCommitReplyPreservesOriginal(t *testing.T) {
	for _, mode := range []string{"new", "version", "verification_unavailable"} {
		t.Run(mode, func(t *testing.T) {
			pool, cleanup := assetTestPool(t)
			defer cleanup()
			ctx := t.Context()
			owner := uuid.New()
			if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Upload owner','creator')`, owner, owner.String()+"@test.local", "upload_"+owner.String()[:8]); err != nil {
				t.Fatal(err)
			}
			var armed, dropped atomic.Bool
			config := pool.Config()
			config.MinConns = 0
			config.MaxConns = 1
			config.ConnConfig.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
				if mode == "verification_unavailable" && dropped.Load() {
					return nil, errors.New("verification database unavailable")
				}
				c, err := (&net.Dialer{}).DialContext(ctx, network, address)
				if err != nil {
					return nil, err
				}
				return &uploadCommitDropConn{Conn: c, armed: &armed, dropped: &dropped}, nil
			}
			faulty, err := pgxpool.NewWithConfig(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer faulty.Close()
			store := &uploadTrackingStore{Store: media.NewLocalStore(t.TempDir()), afterPut: func() { armed.Store(true) }}
			service := assets.NewServiceWithMedia(faulty, media.NewCatalog(store), &executionScanner{})
			var a assets.Asset
			var uploadErr error
			input := assets.UploadInput{Title: "Original for market", Filename: "original.txt", Reader: strings.NewReader("Immutable market original"), RequestID: "upload-lost-commit"}
			if mode == "version" {
				base, err := assets.NewServiceWithMedia(pool, media.NewCatalog(store.Store), &executionScanner{}).Upload(ctx, owner, assets.UploadInput{Title: "Base original", Filename: "base.txt", Reader: strings.NewReader("Base immutable original"), RequestID: "base"})
				if err != nil {
					t.Fatal(err)
				}
				a, uploadErr = service.UploadVersion(ctx, owner, base.ID, assets.VersionInput{UploadInput: input, Note: "New original version"})
			} else {
				a, uploadErr = service.Upload(ctx, owner, input)
			}
			if !dropped.Load() {
				t.Fatal("successful commit response was not dropped")
			}
			var id uuid.UUID
			var scans, audits int
			if err = pool.QueryRow(ctx, `SELECT id,(SELECT count(*) FROM asset_scan_executions WHERE asset_id=a.id),(SELECT count(*) FROM audit_events WHERE resource_id=a.id AND action IN ('asset.uploaded','asset.version_uploaded')) FROM assets a WHERE owner_id=$1 AND storage_key=$2`, owner, store.key).Scan(&id, &scans, &audits); err != nil || scans != 1 || audits != 1 {
				t.Fatalf("upload did not commit atomically: %s %d %d %v", id, scans, audits, err)
			}
			if _, err = store.Stat(ctx, store.key); err != nil || store.deletes.Load() != 0 {
				t.Errorf("committed original deleted after acknowledgement loss: deletes=%d err=%v", store.deletes.Load(), err)
			}
			if mode == "verification_unavailable" {
				if uploadErr == nil {
					t.Fatal("unavailable verification claimed success")
				}
			} else if uploadErr != nil || a.ID != id {
				t.Errorf("committed upload not recovered: %s expected %s err=%v", a.ID, id, uploadErr)
			}
		})
	}
}

func TestUploadRechecksAccountBeforeStorage(t *testing.T) {
	for _, mode := range []string{"deleted", "suspended", "deleted_while_waiting"} {
		t.Run(mode, func(t *testing.T) {
			pool, cleanup := assetTestPool(t)
			defer cleanup()
			ctx := t.Context()
			owner := uuid.New()
			if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Upload owner','creator')`, owner, owner.String()+"@test.local", "upload_"+owner.String()[:8]); err != nil {
				t.Fatal(err)
			}
			store := &uploadTrackingStore{Store: media.NewLocalStore(t.TempDir())}
			service := assets.NewServiceWithMedia(pool, media.NewCatalog(store), &executionScanner{})
			upload := func() error {
				_, err := service.Upload(ctx, owner, assets.UploadInput{Title: "Original for market", Filename: "original.txt", Reader: strings.NewReader("Immutable market original"), RequestID: "inactive-upload"})
				return err
			}
			var uploadErr error
			if mode == "deleted_while_waiting" {
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(ctx)
				if err = accountlifecycle.Lock(ctx, tx, owner); err != nil {
					t.Fatal(err)
				}
				var pid int
				if err = tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
					t.Fatal(err)
				}
				result := make(chan error, 1)
				go func() { result <- upload() }()
				deadline := time.Now().Add(2 * time.Second)
				waiting := false
				for !waiting && time.Now().Before(deadline) {
					select {
					case uploadErr = <-result:
						t.Fatalf("upload bypassed lifecycle lock: %v", uploadErr)
					default:
					}
					if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid).Scan(&waiting); err != nil {
						t.Fatal(err)
					}
					if !waiting {
						time.Sleep(10 * time.Millisecond)
					}
				}
				if !waiting {
					t.Fatal("upload never waited for lifecycle lock")
				}
				if store.puts.Load() != 0 {
					t.Error("upload wrote private bytes before waiting")
				}
				if _, err = tx.Exec(ctx, `UPDATE users SET status='deleted' WHERE id=$1`, owner); err != nil {
					t.Fatal(err)
				}
				if err = tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				uploadErr = <-result
			} else {
				if _, err := pool.Exec(ctx, `UPDATE users SET status=$2 WHERE id=$1`, owner, mode); err != nil {
					t.Fatal(err)
				}
				uploadErr = upload()
			}
			if !errors.Is(uploadErr, assets.ErrForbidden) || store.puts.Load() != 0 {
				t.Fatalf("inactive upload allowed storage: err=%v puts=%d", uploadErr, store.puts.Load())
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE owner_id=$1`, owner).Scan(&count); err != nil || count != 0 {
				t.Fatalf("inactive asset created: %d %v", count, err)
			}
		})
	}
}

func TestUploadDefiniteRollbackCleansOnlyNewOriginal(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := t.Context()
	owner := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Upload owner','creator')`, owner, owner.String()+"@test.local", "upload_"+owner.String()[:8]); err != nil {
		t.Fatal(err)
	}
	store := &uploadTrackingStore{Store: media.NewLocalStore(t.TempDir())}
	service := assets.NewServiceWithMedia(pool, media.NewCatalog(store), &executionScanner{})
	base, err := service.Upload(ctx, owner, assets.UploadInput{Title: "Original for market", Filename: "original.txt", Reader: strings.NewReader("Original content"), RequestID: "base"})
	if err != nil {
		t.Fatal(err)
	}
	baseKey := store.key
	if _, err = pool.Exec(ctx, `CREATE FUNCTION reject_upload_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture upload commit rejected'; END $$;
 CREATE CONSTRAINT TRIGGER reject_upload_commit AFTER INSERT ON assets DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_upload_commit()`); err != nil {
		t.Fatal(err)
	}
	_, err = service.UploadVersion(ctx, owner, base.ID, assets.VersionInput{UploadInput: assets.UploadInput{Title: "New original", Filename: "original.txt", Reader: strings.NewReader("New original content"), RequestID: "rejected"}, Note: "Replace original"})
	if err == nil {
		t.Fatal("rejected commit reported success")
	}
	if store.deletes.Load() != 1 {
		t.Fatalf("rollback did not clean uncommitted original: %d", store.deletes.Load())
	}
	if _, err = store.Stat(ctx, store.key); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("uncommitted bytes remain: %v", err)
	}
	if _, err = store.Stat(ctx, baseKey); err != nil {
		t.Fatalf("prior original lost: %v", err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE owner_id=$1`, owner).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rollback created metadata: %d %v", count, err)
	}
}

func TestUploadHoldsLifecycleThroughStorageAndRejectsForeignVersion(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := t.Context()
	owners := []uuid.UUID{uuid.New(), uuid.New()}
	for _, owner := range owners {
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Upload owner','creator')`, owner, owner.String()+"@test.local", "upload_"+owner.String()[:8]); err != nil {
			t.Fatal(err)
		}
	}
	store := &uploadTrackingStore{Store: media.NewLocalStore(t.TempDir())}
	store.afterPut = func() {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		var acquired bool
		if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, accountlifecycle.Key(owners[0])).Scan(&acquired); err != nil || acquired {
			t.Fatalf("storage write not protected against deletion: acquired=%t err=%v", acquired, err)
		}
	}
	service := assets.NewServiceWithMedia(pool, media.NewCatalog(store), &executionScanner{})
	base, err := service.Upload(ctx, owners[0], assets.UploadInput{Title: "Private original", Filename: "private.txt", Reader: strings.NewReader("Private content"), RequestID: "protected"})
	if err != nil {
		t.Fatal(err)
	}
	store.afterPut = nil
	_, err = service.UploadVersion(ctx, owners[1], base.ID, assets.VersionInput{UploadInput: assets.UploadInput{Title: "Foreign original", Filename: "foreign.txt", Reader: strings.NewReader("Unowned content"), RequestID: "foreign"}, Note: "Unowned version"})
	if !errors.Is(err, assets.ErrForbidden) || store.puts.Load() != 1 {
		t.Fatalf("foreign version wrote bytes: %v puts=%d", err, store.puts.Load())
	}
}
