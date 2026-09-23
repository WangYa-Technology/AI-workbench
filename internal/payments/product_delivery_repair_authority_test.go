package payments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

type repairAuthorityStore struct {
	*media.LocalStore
	key               string
	putGate           bool
	used              atomic.Bool
	puts              atomic.Int32
	entered, released chan struct{}
}

func TestDeliveryRepairPinsAuthorityThroughWriteAndCommit(t *testing.T) {
	for _, action := range []string{"prepare_upload", "resume_stored", "resume_existing", "upload"} {
		for _, change := range []string{"role", "permission"} {
			t.Run(action+"/"+change, func(t *testing.T) {
				pool, cleanup := paymentTestPool(t)
				t.Cleanup(cleanup)
				f := newPurchasedReferenceFixture(t, pool, "video")
				actor := repairActor(t, pool)
				snapshot := corruptDelivery(t, pool, f)
				plain := media.NewLocalStore(f.root)
				var repair uuid.UUID
				var targetKey string
				if action == "resume_stored" {
					_, err := productdelivery.NewRepairService(pool, media.NewCatalog(&failedDeliveryStore{LocalStore: plain, fail: true})).Repair(t.Context(), actor, f.order, "pin-reservation", "reserve", repairCommand(0, nil))
					if err == nil {
						t.Fatal("expected copy failure")
					}
				} else if action != "prepare_upload" {
					_, err := productdelivery.NewRepairService(pool, media.NewCatalog(plain)).PrepareUpload(t.Context(), actor, f.order, "pin-reservation", "reserve", repairCommand(0, nil))
					if err != nil {
						t.Fatal(err)
					}
				}
				if action != "prepare_upload" {
					if err := pool.QueryRow(t.Context(), `SELECT id,storage_key FROM product_delivery_repairs WHERE order_id=$1`, f.order).Scan(&repair, &targetKey); err != nil {
						t.Fatal(err)
					}
				}
				if action == "resume_existing" {
					if err := plain.Put(t.Context(), targetKey, f.content, snapshot.MIMEType); err != nil {
						t.Fatal(err)
					}
				}
				store := &repairAuthorityStore{LocalStore: plain, key: targetKey, putGate: true, entered: make(chan struct{}), released: make(chan struct{})}
				var once sync.Once
				release := func() { once.Do(func() { close(store.released) }) }
				var entered <-chan struct{} = store.entered
				commandPool := pool
				if action == "prepare_upload" {
					commandPool, entered, release = testutil.GateQuery(t, pool, "INSERT INTO product_delivery_repairs(id,order_id,revision,actor_id")
				} else if action == "resume_existing" {
					commandPool, entered, release = testutil.GateQuery(t, pool, "UPDATE product_delivery_repairs SET state='ready'")
				}
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				var workers sync.WaitGroup
				t.Cleanup(func() { release(); cancel(); workers.Wait() })
				service := productdelivery.NewRepairService(commandPool, media.NewCatalog(store))
				done := make(chan error, 1)
				workers.Add(1)
				go func() {
					defer workers.Done()
					var err error
					switch action {
					case "prepare_upload":
						_, err = service.PrepareUpload(ctx, actor, f.order, "pin-new-reservation", "pin-request", repairCommand(0, nil))
					case "upload":
						_, err = service.Upload(ctx, actor, f.order, repair, true, "pin-request", bytes.NewReader(f.content))
					default:
						_, err = service.Resume(ctx, actor, f.order, repair, "pin-request")
					}
					done <- err
				}()
				select {
				case <-entered:
				case err := <-done:
					t.Fatal("did not reach protected write", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				conn, err := pool.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				pid := int32(conn.Conn().PgConn().PID())
				revoked := make(chan error, 1)
				workers.Add(1)
				go func() {
					defer workers.Done()
					defer conn.Release()
					var err error
					if change == "role" {
						_, err = conn.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor)
					} else {
						_, err = conn.Exec(ctx, `DELETE FROM role_permissions WHERE role='admin' AND permission_id='admin:media'`)
					}
					revoked <- err
				}()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					var waiting bool
					if err := pool.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids($1))>0`, pid).Scan(&waiting); err != nil {
						t.Fatal(err)
					}
					if waiting {
						break
					}
					select {
					case err := <-revoked:
						t.Fatal("revocation passed protected write", err)
					case <-ticker.C:
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
				}
				release()
				// The write was authorized through commit. A later read-only
				// Inspect may already see the completed revocation and deny output.
				if err := <-done; err != nil && !errors.Is(err, productdelivery.ErrRepairForbidden) {
					t.Fatal("authorized write failed", err)
				}
				if err := <-revoked; err != nil {
					t.Fatal(err)
				}
				var state string
				var audits int
				auditAction := "marketplace.delivery_repair_completed"
				if action == "prepare_upload" {
					auditAction = "marketplace.delivery_repair_requested"
				}
				if err := pool.QueryRow(ctx, `SELECT state,(SELECT count(*) FROM audit_events WHERE resource_id=$1 AND action=$2 AND request_id='pin-request') FROM product_delivery_repairs WHERE order_id=$1`, f.order, auditAction).Scan(&state, &audits); err != nil || audits != 1 {
					t.Fatal("authorized repair missing audit", state, audits, err)
				}
				if action == "prepare_upload" {
					if state != "prepared" {
						t.Fatal(state)
					}
				} else {
					if state != "ready" {
						t.Fatal(state)
					}
					assertPurchasedBytes(t, pool, f.root, f.buyer, f.purchased, f.content)
				}
				if _, err := service.Inspect(ctx, actor, f.order); !errors.Is(err, productdelivery.ErrRepairForbidden) {
					t.Fatal("fresh revoked inspect accepted", err)
				}
			})
		}
	}
}

func TestDeliveryRepairBundleRevocationDuringReconstruction(t *testing.T) {
	for _, action := range []string{"prepare", "resume"} {
		t.Run(action, func(t *testing.T) {
			f := newBundleSnapshotFixture(t)
			snapshot := f.reserve(t)
			f.pendingPayment(t, snapshot.OrderID)
			if err := productdelivery.Ensure(t.Context(), f.pool, f.stores, snapshot.OrderID); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(f.root, snapshot.Key), []byte("damaged ZIP"), 0600); err != nil {
				t.Fatal(err)
			}
			actor := repairActor(t, f.pool)
			var repair uuid.UUID
			if action == "resume" {
				failed := &failedDeliveryStore{LocalStore: f.store, fail: true}
				if _, err := productdelivery.NewRepairService(f.pool, media.NewCatalog(failed)).Repair(t.Context(), actor, snapshot.OrderID, "bundle-authority-reserve", "reserve", repairCommand(0, nil)); err == nil {
					t.Fatal("expected interrupted copy")
				}
				if err := f.pool.QueryRow(t.Context(), `SELECT id FROM product_delivery_repairs WHERE order_id=$1 AND state='prepared'`, snapshot.OrderID).Scan(&repair); err != nil {
					t.Fatal(err)
				}
			}
			// Pause the second member after reconstruction has already read the first.
			store := &repairAuthorityStore{LocalStore: f.store, key: f.second.String() + ".txt", entered: make(chan struct{}), released: make(chan struct{})}
			service := productdelivery.NewRepairService(f.pool, media.NewCatalog(store))
			before, files := repairAuthoritySnapshot(t, f.pool, snapshot.OrderID), repairAuthorityFiles(t, f.root)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			var once sync.Once
			release := func() { once.Do(func() { close(store.released) }) }
			var workers sync.WaitGroup
			t.Cleanup(func() { release(); cancel(); workers.Wait() })
			done := make(chan error, 1)
			workers.Add(1)
			go func() {
				defer workers.Done()
				var err error
				if action == "prepare" {
					_, err = service.Repair(ctx, actor, snapshot.OrderID, "bundle-authority-new", "request", repairCommand(0, nil))
				} else {
					_, err = service.Resume(ctx, actor, snapshot.OrderID, repair, "request")
				}
				done <- err
			}()
			select {
			case <-store.entered:
			case err := <-done:
				t.Fatal("did not reach second member", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if _, err := f.pool.Exec(ctx, `DELETE FROM role_permissions WHERE role='admin' AND permission_id='admin:media'`); err != nil {
				t.Fatal(err)
			}
			release()
			if err := <-done; !errors.Is(err, productdelivery.ErrRepairForbidden) {
				t.Fatal("revoked bundle repair accepted", err)
			}
			if before != repairAuthoritySnapshot(t, f.pool, snapshot.OrderID) || !reflect.DeepEqual(files, repairAuthorityFiles(t, f.root)) || store.puts.Load() != 0 {
				t.Fatal("revoked reconstruction changed evidence or files")
			}
		})
	}
}

func (s *repairAuthorityStore) gate(ctx context.Context, key string, put bool) error {
	if key == s.key && put == s.putGate && s.used.CompareAndSwap(false, true) {
		close(s.entered)
		select {
		case <-s.released:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (s *repairAuthorityStore) Open(ctx context.Context, key string, r *media.ByteRange) (media.Object, error) {
	if err := s.gate(ctx, key, false); err != nil {
		return media.Object{}, err
	}
	return s.LocalStore.Open(ctx, key, r)
}
func (s *repairAuthorityStore) PutStream(ctx context.Context, key string, body io.ReadSeeker, size int64, digest, mime string) error {
	s.puts.Add(1)
	if err := s.gate(ctx, key, true); err != nil {
		return err
	}
	return s.LocalStore.PutStream(ctx, key, body, size, digest, mime)
}

func repairAuthoritySnapshot(t *testing.T, pool *pgxpool.Pool, order uuid.UUID) string {
	t.Helper()
	var result string
	if err := pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
 'repairs',(SELECT jsonb_agg(to_jsonb(r) ORDER BY r.revision) FROM product_delivery_repairs r WHERE r.order_id=$1),
 'audit',(SELECT jsonb_agg(to_jsonb(a) ORDER BY a.id) FROM audit_events a WHERE a.resource_id=$1 AND a.action LIKE 'marketplace.delivery_repair_%'),
 'contract',(SELECT to_jsonb(c) FROM product_order_contracts c WHERE c.order_id=$1),
 'snapshot',(SELECT to_jsonb(d) FROM product_delivery_snapshots d WHERE d.order_id=$1),
 'rights',(SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM entitlements e WHERE e.order_id=$1))::text`, order).Scan(&result); err != nil {
		t.Fatal(err)
	}
	return result
}
func repairAuthorityFiles(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	files := map[string][32]byte{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = sha256.Sum256(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestDeliveryRepairRejectsAuthorityLostDuringIO(t *testing.T) {
	for _, action := range []string{"inspect", "prepare_stored", "prepare_upload", "resume_stored", "resume_existing", "upload"} {
		for _, change := range []string{"role", "suspended", "permission"} {
			t.Run(action+"/"+change, func(t *testing.T) {
				pool, cleanup := paymentTestPool(t)
				t.Cleanup(cleanup)
				f := newPurchasedReferenceFixture(t, pool, "video")
				actor := repairActor(t, pool)
				snapshot := corruptDelivery(t, pool, f)
				plain := media.NewLocalStore(f.root)
				var repair uuid.UUID
				gateKey := snapshot.Key
				if action == "prepare_stored" {
					gateKey = snapshot.SourceKey
				}
				if action == "resume_stored" {
					failed := &failedDeliveryStore{LocalStore: plain, fail: true}
					_, err := productdelivery.NewRepairService(pool, media.NewCatalog(failed)).Repair(t.Context(), actor, f.order, "reserve-authority-test", "reserve", repairCommand(0, nil))
					if err == nil {
						t.Fatal("expected interrupted reservation")
					}
					if err := pool.QueryRow(t.Context(), `SELECT id FROM product_delivery_repairs WHERE order_id=$1 AND state='prepared'`, f.order).Scan(&repair); err != nil {
						t.Fatal(err)
					}
					gateKey = snapshot.SourceKey
				} else if action == "resume_existing" || action == "upload" {
					status, err := productdelivery.NewRepairService(pool, media.NewCatalog(plain)).PrepareUpload(t.Context(), actor, f.order, "reserve-authority-test", "reserve", repairCommand(0, nil))
					if err != nil {
						t.Fatal(err)
					}
					repair = *status.PendingID
					if err := pool.QueryRow(t.Context(), `SELECT storage_key FROM product_delivery_repairs WHERE id=$1`, repair).Scan(&gateKey); err != nil {
						t.Fatal(err)
					}
					if action == "resume_existing" {
						if err := plain.Put(t.Context(), gateKey, f.content, snapshot.MIMEType); err != nil {
							t.Fatal(err)
						}
					}
				}
				// Stronger pool defaults must not freeze an obsolete role snapshot.
				config := pool.Config()
				config.ConnConfig.RuntimeParams["default_transaction_isolation"] = "repeatable read"
				commandPool, err := pgxpool.NewWithConfig(t.Context(), config)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(commandPool.Close)
				store := &repairAuthorityStore{LocalStore: plain, key: gateKey, entered: make(chan struct{}), released: make(chan struct{})}
				service := productdelivery.NewRepairService(commandPool, media.NewCatalog(store))
				before := repairAuthoritySnapshot(t, pool, f.order)
				files := repairAuthorityFiles(t, f.root)
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				var once sync.Once
				release := func() { once.Do(func() { close(store.released) }) }
				var workers sync.WaitGroup
				t.Cleanup(func() { release(); cancel(); workers.Wait() })
				run := func() (productdelivery.RepairStatus, error) {
					switch action {
					case "inspect":
						return service.Inspect(ctx, actor, f.order)
					case "prepare_stored":
						return service.Repair(ctx, actor, f.order, "new-authority-command", "request", repairCommand(0, nil))
					case "prepare_upload":
						return service.PrepareUpload(ctx, actor, f.order, "new-authority-command", "request", repairCommand(0, nil))
					case "upload":
						return service.Upload(ctx, actor, f.order, repair, true, "request", bytes.NewReader(f.content))
					default:
						return service.Resume(ctx, actor, f.order, repair, "request")
					}
				}
				done := make(chan error, 1)
				workers.Add(1)
				go func() { defer workers.Done(); _, err := run(); done <- err }()
				select {
				case <-store.entered:
				case err := <-done:
					t.Fatal("did not reach IO gate", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				switch change {
				case "role":
					_, err = pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor)
				case "suspended":
					_, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, actor)
				case "permission":
					_, err = pool.Exec(ctx, `DELETE FROM role_permissions WHERE role='admin' AND permission_id='admin:media'`)
				}
				if err != nil {
					t.Fatal(err)
				}
				release()
				if err := <-done; !errors.Is(err, productdelivery.ErrRepairForbidden) && !errors.Is(err, productdelivery.ErrRepairConflict) {
					t.Errorf("revoked operation accepted: %v", err)
				}
				if before != repairAuthoritySnapshot(t, pool, f.order) {
					t.Error("revoked operation changed repair/contract/rights evidence")
				}
				if !reflect.DeepEqual(files, repairAuthorityFiles(t, f.root)) || store.puts.Load() != 0 {
					t.Errorf("revoked operation wrote delivery bytes: puts=%d", store.puts.Load())
				}
				if _, err := run(); !errors.Is(err, productdelivery.ErrRepairForbidden) {
					t.Errorf("fresh revoked command accepted: %v", err)
				}
			})
		}
	}
}
