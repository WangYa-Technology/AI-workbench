package creation_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/accountlifecycle"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/generationoutput"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/jackc/pgx/v5/pgxpool"
)

func abandonedOutput(t *testing.T, pool *pgxpool.Pool, store media.Store) (uuid.UUID, uuid.UUID, generationoutput.Intent) {
	t.Helper()
	ctx := t.Context()
	owner := lifecycleOwner(t, pool)
	svc := creation.NewServiceWithMedia(pool, media.NewCatalog(store), creation.NewRuntimeCatalog(newLifecycleRuntime(t)))
	g, err := svc.SubmitCommand(ctx, owner, creation.SubmitInput{Mode: "image", Prompt: "Unattached cleanup fairness"}, "cleanup-fairness", "test")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = accountlifecycle.Lock(ctx, tx, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM generations WHERE id=$1 FOR UPDATE`, g.ID); err != nil {
		t.Fatal(err)
	}
	intent, err := generationoutput.RegisterTx(ctx, tx, owner, g.ID, uuid.New(), store, []byte("recorded abandoned output"), ".jpg")
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.Put(ctx, intent.Key, []byte("recorded abandoned output"), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	makeOutputDue(t, pool, intent.ID)
	return owner, g.ID, intent
}

func TestGenerationOutputCleanupRejectsReplacementRoot(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	root := t.TempDir()
	store := media.NewLocalStore(root)
	_, _, intent := abandonedOutput(t, pool, store)
	moved := filepath.Join(t.TempDir(), "offline-media")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	sweeper := generationoutput.NewService(pool, media.NewCatalog(store))
	if _, err := sweeper.Reconcile(t.Context(), 100); !errors.Is(err, generationoutput.ErrReferenced) {
		t.Fatal("replacement root accepted as absent generation bytes", err)
	}
	var state string
	var absent bool
	if err := pool.QueryRow(t.Context(), `SELECT status,verified_absent_at IS NOT NULL FROM generation_output_writes WHERE id=$1`, intent.ID).Scan(&state, &absent); err != nil || state != "pending" || absent {
		t.Fatal("false generation cleanup evidence", state, absent, err)
	}
	if body, err := os.ReadFile(filepath.Join(moved, intent.Key)); err != nil || string(body) != "recorded abandoned output" {
		t.Fatal("original output changed", err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal("replacement root received writes", err)
	}
	if err := os.Rename(moved, root); err != nil {
		t.Fatal(err)
	}
	makeOutputDue(t, pool, intent.ID)
	if _, err := sweeper.Reconcile(t.Context(), 100); err != nil {
		t.Fatal("restored generation cleanup failed", err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT status,verified_absent_at IS NOT NULL FROM generation_output_writes WHERE id=$1`, intent.ID).Scan(&state, &absent); err != nil || state != "cleaned" || !absent {
		t.Fatal("restored output not verified", state, absent, err)
	}
	if _, err := store.Stat(t.Context(), intent.Key); !errors.Is(err, media.ErrNotFound) {
		t.Fatal("output still present after recovery", err)
	}
}

func TestGenerationOutputCleanupLockedHeadMakesProgress(t *testing.T) {
	for _, kind := range []string{"owner", "generation", "intent"} {
		t.Run(kind, func(t *testing.T) {
			pool, cleanup := testPool(t)
			defer cleanup()
			ctx := t.Context()
			store := media.NewLocalStore(t.TempDir())
			owner, generation, first := abandonedOutput(t, pool, store)
			_, _, second := abandonedOutput(t, pool, store)
			if _, err := pool.Exec(ctx, `UPDATE generation_output_writes SET next_check_at=now()-interval '1 day' WHERE id=$1`, first.ID); err != nil {
				t.Fatal(err)
			}
			locked, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer locked.Rollback(ctx)
			switch kind {
			case "owner":
				err = accountlifecycle.Lock(ctx, locked, owner)
			case "generation":
				_, err = locked.Exec(ctx, `SELECT id FROM generations WHERE id=$1 FOR UPDATE`, generation)
			case "intent":
				_, err = locked.Exec(ctx, `SELECT id FROM generation_output_writes WHERE id=$1 FOR UPDATE`, first.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			sweeper := generationoutput.NewService(pool, media.NewCatalog(store))
			for range 2 {
				pass, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
				_, err = sweeper.Reconcile(pass, 1)
				cancel()
				if err != nil {
					t.Fatalf("busy %s consumed cleanup budget: %v", kind, err)
				}
			}
			if _, err = store.Stat(ctx, second.Key); !errors.Is(err, media.ErrNotFound) {
				t.Fatal("locked head starved later cleanup", err)
			}
			if _, err = store.Stat(ctx, first.Key); err != nil {
				t.Fatal("active write was removed", err)
			}
			if err = locked.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			makeOutputDue(t, pool, first.ID)
			if _, err = sweeper.Reconcile(ctx, 100); err != nil {
				t.Fatal(err)
			}
			if _, err = store.Stat(ctx, first.Key); !errors.Is(err, media.ErrNotFound) {
				t.Fatal("unlock did not recover cleanup", err)
			}
		})
	}
}

type cleanupGateStore struct {
	media.Store
	target           string
	entered, release chan struct{}
	deletes          atomic.Int64
}

func (s *cleanupGateStore) Delete(ctx context.Context, key string) error {
	s.deletes.Add(1)
	if key == s.target {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.Store.Delete(ctx, key)
}

func TestGenerationOutputCleanupSkipsInFlightStorageCheck(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	store := &cleanupGateStore{Store: media.NewLocalStore(t.TempDir()), entered: make(chan struct{}), release: make(chan struct{})}
	_, _, first := abandonedOutput(t, pool, store)
	_, _, second := abandonedOutput(t, pool, store)
	if _, err := pool.Exec(ctx, `UPDATE generation_output_writes SET next_check_at=now()-interval '1 day' WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	var originalDue time.Time
	if err := pool.QueryRow(ctx, `SELECT next_check_at FROM generation_output_writes WHERE id=$1`, first.ID).Scan(&originalDue); err != nil {
		t.Fatal(err)
	}
	store.target = first.Key
	sweeper := generationoutput.NewService(pool, media.NewCatalog(store))
	result := make(chan error, 1)
	go func() { _, err := sweeper.Reconcile(ctx, 1); result <- err }()
	select {
	case <-store.entered:
	case err := <-result:
		t.Fatalf("cleanup ended before storage check: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	pass, stop := context.WithTimeout(ctx, 500*time.Millisecond)
	n, err := sweeper.Reconcile(pass, 1)
	stop()
	if err != nil || n != 1 {
		t.Fatalf("in-flight cleanup blocked another owner: %d %v", n, err)
	}
	if _, err = store.Stat(ctx, second.Key); !errors.Is(err, media.ErrNotFound) {
		t.Fatal("later file not cleaned", err)
	}
	var due time.Time
	var checks int
	if err = pool.QueryRow(ctx, `SELECT next_check_at,cleanup_checks FROM generation_output_writes WHERE id=$1`, first.ID).Scan(&due, &checks); err != nil || !due.Equal(originalDue) || checks != 0 {
		t.Fatal("competing pass changed in-flight bookkeeping", due, checks, err)
	}
	close(store.release)
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	if _, err = store.Stat(ctx, first.Key); !errors.Is(err, media.ErrNotFound) {
		t.Fatal("original cleanup did not finish", err)
	}
	if store.deletes.Load() != 2 {
		t.Fatal("concurrent cleaners repeated a delete", store.deletes.Load())
	}
}
