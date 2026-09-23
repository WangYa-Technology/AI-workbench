package payments

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
)

type unverifiableDeletionStore struct {
	media.Store
	mode       string
	target     string
	deleteSent bool
}

func TestProductDeletionReceiptRequiresVerifiedCopyAbsence(t *testing.T) {
	for _, mode := range []string{"noop", "stat_denied", "root_missing", "root_replaced"} {
		t.Run(mode, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			f := newPurchasedReferenceFixture(t, pool, "video")
			snapshot, err := productdelivery.Load(ctx, pool, f.order)
			if err != nil {
				t.Fatal(err)
			}
			store := &unverifiableDeletionStore{Store: media.NewLocalStore(f.root), target: snapshot.Key, mode: mode}
			service := datarights.NewServiceWithMedia(pool, f.root, media.NewCatalog(store))
			request, job := scheduleMarketplaceDeletion(t, pool, f.root, f.buyer)
			restore := interruptCleanupRoot(t, mode, f.root, snapshot.Key)
			wantError := media.ErrDeletionUnverified
			if mode == "root_missing" {
				wantError = media.ErrStorageUnavailable
			} else if mode == "root_replaced" {
				wantError = media.ErrIntegrity
			}
			if err := service.HandleDeletionJob(ctx, job); !errors.Is(err, wantError) {
				t.Fatal("unverified copy cleanup allowed account completion", err)
			}
			var status string
			var receipts int
			if err := pool.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM data_rights_deletion_receipts WHERE request_id=$1)
 FROM data_rights_requests WHERE id=$1`, request).Scan(&status, &receipts); err != nil || status != "processing" || receipts != 0 {
				t.Fatal("premature account receipt", status, receipts, err)
			}
			copy, err := productdelivery.Load(ctx, pool, f.order)
			if err != nil || copy.State != "ready" || copy.SHA256 != snapshot.SHA256 {
				t.Fatal("premature copy removal", copy, err)
			}
			store.mode = ""
			restore()
			if err := service.HandleDeletionJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM data_rights_deletion_receipts WHERE request_id=$1)
 FROM data_rights_requests WHERE id=$1`, request).Scan(&status, &receipts); err != nil || status != "completed" || receipts != 1 {
				t.Fatal("account cleanup could not resume", status, receipts, err)
			}
			if _, err := store.Stat(ctx, snapshot.Key); !errors.Is(err, media.ErrNotFound) {
				t.Fatal("completed account retained unneeded copy", err)
			}
			if _, err := store.Stat(ctx, snapshot.SourceKey); err != nil {
				t.Fatal("account cleanup deleted seller original", err)
			}
		})
	}
}

func (s *unverifiableDeletionStore) Delete(ctx context.Context, key string) error {
	if key == s.target {
		s.deleteSent = true
		if s.mode == "noop" {
			return nil
		}
	}
	return s.Store.Delete(ctx, key)
}

func (s *unverifiableDeletionStore) Stat(ctx context.Context, key string) (media.ObjectInfo, error) {
	if key == s.target && s.deleteSent && s.mode == "stat_denied" {
		return media.ObjectInfo{}, errors.New("storage inspection denied")
	}
	return s.Store.Stat(ctx, key)
}

func TestProductCleanupRequiresVerifiedAbsence(t *testing.T) {
	for _, mode := range []string{"noop", "stat_denied", "root_missing", "root_replaced"} {
		t.Run(mode, func(t *testing.T) {
			f := newPendingProductClosure(t)
			ctx := context.Background()
			f.store.fail = false
			if err := productdelivery.Ensure(ctx, f.pool, f.service.config.MediaStores, f.order); err != nil {
				t.Fatal(err)
			}
			before, err := productdelivery.Load(ctx, f.pool, f.order)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.close(ctx, "verify-removal-closure"); err != nil {
				t.Fatal(err)
			}
			store := &unverifiableDeletionStore{Store: f.store, mode: mode, target: before.Key}
			handler := productdelivery.CleanupHandler(f.pool, media.NewCatalog(store))
			repo := jobs.NewRepository(f.pool)
			job, err := repo.Claim(ctx, "verify-removal", time.Minute)
			if err != nil || job.Kind != productdelivery.CleanupJobKind {
				t.Fatal("cleanup not claimed", job.Kind, err)
			}
			restore := interruptCleanupRoot(t, mode, paymentTestRoot(t, f.pool), before.Key)
			cause := handler(ctx, job)
			if cause == nil || !store.deleteSent {
				t.Fatal("unverified deletion reported completion", cause)
			}
			if err := repo.Fail(ctx, job, "verify-removal", cause); err != nil {
				t.Fatal(err)
			}
			after, err := productdelivery.Load(ctx, f.pool, f.order)
			if err != nil || after.State != "ready" || after.SHA256 != before.SHA256 || after.Key != before.Key {
				t.Fatal("unverified removal changed evidence", after, err)
			}
			var removedAt *time.Time
			if err := f.pool.QueryRow(ctx, `SELECT removed_at FROM product_delivery_snapshots WHERE order_id=$1`, f.order).Scan(&removedAt); err != nil || removedAt != nil {
				t.Fatal("unverified removal timestamp", removedAt, err)
			}
			_, inspectErr := f.store.Stat(ctx, before.Key)
			if mode == "noop" && inspectErr != nil || mode == "stat_denied" && !errors.Is(inspectErr, media.ErrNotFound) {
				t.Fatal("unexpected physical result", inspectErr)
			}
			// Restore the storage boundary, then continue the same durable job.
			// A previously removed object must be safe to verify again.
			store.mode = ""
			restore()
			if _, err := f.pool.Exec(ctx, `UPDATE jobs SET available_at=now() WHERE id=$1 AND status='queued'`, job.ID); err != nil {
				t.Fatal(err)
			}
			retry, err := repo.Claim(ctx, "verify-removal-retry", time.Minute)
			if err != nil || retry.ID != job.ID {
				t.Fatal("lost original cleanup job", err)
			}
			if err := handler(ctx, retry); err != nil {
				t.Fatal(err)
			}
			if err := repo.Complete(ctx, retry, "verify-removal-retry"); err != nil {
				t.Fatal(err)
			}
			after, err = productdelivery.Load(ctx, f.pool, f.order)
			if err != nil || after.State != "removed" || after.Key != before.Key || after.SHA256 != before.SHA256 {
				t.Fatal("verified cleanup failed", after, err)
			}
			if _, err := f.store.Stat(ctx, before.Key); !errors.Is(err, media.ErrNotFound) {
				t.Fatal("copy remains after completion", err)
			}
			if _, err := f.store.Stat(ctx, before.SourceKey); err != nil {
				t.Fatal("seller original was deleted", err)
			}
		})
	}
}

// Move the entire storage root, leaving its real bytes intact but unavailable
// at the configured path. Recovery must retry the original deletion job.
func interruptCleanupRoot(t *testing.T, mode, root, key string) func() {
	t.Helper()
	if mode != "root_missing" && mode != "root_replaced" {
		return func() {}
	}
	moved := filepath.Join(t.TempDir(), "offline-media")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	if mode == "root_replaced" {
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return func() {
		t.Helper()
		if _, err := os.Stat(filepath.Join(moved, key)); err != nil {
			t.Fatal("file disappeared during storage outage", err)
		}
		if mode == "root_replaced" {
			if err := os.Remove(root); err != nil {
				t.Fatal("replacement root received unexpected writes", err)
			}
		}
		if err := os.Rename(moved, root); err != nil {
			t.Fatal(err)
		}
	}
}
