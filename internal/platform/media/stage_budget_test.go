package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
)

type stageOpenCounter struct {
	Store
	opens atomic.Int32
}

func (s *stageOpenCounter) Open(ctx context.Context, key string, r *ByteRange) (Object, error) {
	s.opens.Add(1)
	return s.Store.Open(ctx, key, r)
}

func TestStageBudgetSharedAcrossCatalogsAndDownloadBodies(t *testing.T) {
	ctx := t.Context()
	cfg := config.Config{MediaRoot: t.TempDir(), MediaStage: config.MediaStageConfig{MaxBytes: 11, MaxObjects: 8, MinFreeBytes: 1, TempDir: t.TempDir()}}
	first, second := NewCatalogFromConfig(cfg), NewCatalogFromConfig(cfg)
	store := &stageOpenCounter{Store: second.Primary()}
	if err := store.Put(ctx, "sample", []byte("bytes"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("bytes"))
	digest := hex.EncodeToString(sum[:])
	held, err := first.Stage(ctx, bytes.NewBufferString("bytes"), 5)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if _, err := second.OpenVerified(ctx, store, "sample", digest, 5, nil); !errors.Is(err, ErrStageBusy) || store.opens.Load() != 0 {
		t.Fatal("exhaustion read remote bytes", err, store.opens.Load())
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	response, err := second.OpenVerified(ctx, store, "sample", digest, 5, &ByteRange{Start: 1, End: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := first.Stage(ctx, bytes.NewBufferString("bytes"), 5); !errors.Is(err, ErrStageBusy) {
		t.Fatal("response released capacity before Close", err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "yte" {
		t.Fatal(string(body), err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal("double close", err)
	}
	retry, err := first.Stage(ctx, bytes.NewBufferString("bytes"), 5)
	if err != nil {
		t.Fatal(err)
	}
	defer retry.Close()
	if _, err := second.Stage(ctx, bytes.NewBufferString("bytes"), 5); !errors.Is(err, ErrStageBusy) {
		t.Fatal("double Close over-released capacity", err)
	}
	entries, err := os.ReadDir(cfg.MediaStage.TempDir)
	if err != nil || len(entries) != 0 {
		t.Fatal("private staged file has a directory entry", entries, err)
	}
}

func TestStageBudgetConcurrentAdmissionAndClose(t *testing.T) {
	cfg := config.MediaStageConfig{MaxBytes: 12, MaxObjects: 3, MinFreeBytes: 1, TempDir: t.TempDir()}
	type result struct {
		stage *StagedObject
		err   error
	}
	results := make(chan result, 16)
	for range cap(results) {
		go func() {
			s, err := Stage(t.Context(), bytes.NewBufferString("bytes"), 5, cfg)
			results <- result{s, err}
		}()
	}
	var held []*StagedObject
	for range cap(results) {
		r := <-results
		if r.err == nil {
			held = append(held, r.stage)
		} else if !errors.Is(r.err, ErrStageBusy) {
			t.Error(r.err)
		}
	}
	defer func() {
		for _, s := range held {
			_ = s.Close()
		}
	}()
	if len(held) != 2 {
		t.Fatalf("atomic byte reservation: got %d admitted, want 2", len(held))
	}
	var wg sync.WaitGroup
	for _, s := range held {
		for range 4 {
			wg.Go(func() { _ = s.Close() })
		}
	}
	wg.Wait()
	s, err := Stage(t.Context(), bytes.NewBufferString("12345678901"), 11, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := Stage(t.Context(), bytes.NewBufferString("x"), 1, cfg); !errors.Is(err, ErrStageBusy) {
		t.Fatal("concurrent Close over-released", err)
	}
}

type failingStageReader struct{}

func (failingStageReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestStageFailureReleasesCapacity(t *testing.T) {
	cfg := config.MediaStageConfig{MaxBytes: 6, MaxObjects: 1, MinFreeBytes: 1, TempDir: t.TempDir()}
	for _, body := range []io.Reader{nil, bytes.NewBufferString(""), bytes.NewBufferString("toolong"), failingStageReader{}} {
		if s, err := Stage(t.Context(), body, 5, cfg); err == nil || s != nil {
			t.Fatal("invalid input staged", s, err)
		}
		s, err := Stage(t.Context(), bytes.NewBufferString("bytes"), 5, cfg)
		if err != nil {
			t.Fatal("failed input leaked budget", err)
		}
		_ = s.Close()
	}
	for _, settings := range []config.MediaStageConfig{{MaxBytes: 6, MaxObjects: 1, TempDir: cfg.TempDir + "/missing"}, {MaxBytes: 6, MaxObjects: 1, MinFreeBytes: math.MaxInt64, TempDir: cfg.TempDir}} {
		if s, err := Stage(t.Context(), bytes.NewBufferString("bytes"), 5, settings); !errors.Is(err, ErrStageStorage) || s != nil {
			t.Fatal(s, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Stage(ctx, bytes.NewBufferString("bytes"), 5, cfg); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := Stage(t.Context(), bytes.NewBufferString("x"), math.MaxInt64, cfg); !errors.Is(err, ErrIntegrity) {
		t.Fatal("oversized evidence accepted", err)
	}
	s, err := Stage(t.Context(), bytes.NewBufferString("bytes"), 5, cfg)
	if err != nil {
		t.Fatal("storage failure leaked budget", err)
	}
	_ = s.Close()
}

func TestStageRechecksFreeSpaceBeforeWriting(t *testing.T) {
	s, err := newStagedObject(t.Context(), 4, config.MediaStageConfig{MinFreeBytes: 100, TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.freeBytes = func() (uint64, error) { return 101, nil }
	if err := s.read(t.Context(), bytes.NewBufferString("data"), 4); !errors.Is(err, ErrStageStorage) {
		t.Fatal(err)
	}
	stat, err := s.file.Stat()
	if err != nil || stat.Size() != 0 {
		t.Fatal("wrote beyond free-space floor", stat, err)
	}
}
