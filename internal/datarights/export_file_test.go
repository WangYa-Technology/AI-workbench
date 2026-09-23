package datarights

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func TestExportFileBudgetCannotBeBypassed(t *testing.T) {
	f, err := newExportFile(context.Background(), config.DataExportConfig{MaxBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if n, err := io.WriteString(f, "12345678"); n != 8 || err != nil {
		t.Fatal(n, err)
	}
	for _, write := range []func() (int64, error){
		func() (int64, error) { n, err := f.Write([]byte("x")); return int64(n), err },
		func() (int64, error) { n, err := f.WriteString("x"); return int64(n), err },
		func() (int64, error) { return io.Copy(f, strings.NewReader("x")) },
		func() (int64, error) { return io.Copy(f, bytes.NewBufferString("x")) },
	} {
		if n, err := write(); n != 0 || !errors.Is(err, ErrExportTooLarge) {
			t.Fatalf("budget bypass: %d %v", n, err)
		}
	}
	if _, err = f.Seek(math.MaxInt64, io.SeekCurrent); !errors.Is(err, ErrExportTooLarge) {
		t.Fatal("seek overflow", err)
	}
	if _, err = f.Seek(math.MinInt64, io.SeekEnd); !errors.Is(err, os.ErrInvalid) {
		t.Fatal("negative seek", err)
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("A"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(f)
	if err != nil || string(body) != "A2345678" {
		t.Fatal(string(body), err)
	}
	if jobs.ShouldRetry(ErrExportTooLarge) || jobs.ShouldRetry(ErrExportRowTooLarge) || !jobs.ShouldRetry(ErrExportStorage) || !jobs.ShouldRetry(ErrExportBusy) {
		t.Fatal("unsafe retry classification")
	}
}

func TestExportFileStorageCancellationAndRelease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f, err := newExportFile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.freeBytes = func() (uint64, error) { return uint64(f.limits.MinFreeBytes) + 3, nil }
	if _, err = f.WriteString("1234"); !errors.Is(err, ErrExportStorage) {
		t.Fatal("free floor ignored", err)
	}
	info, err := f.Stat()
	if err != nil || info.Size() != 0 {
		t.Fatal("failed write grew file", err)
	}
	if _, err = f.WriteString("123"); err != nil {
		t.Fatal("exact free floor", err)
	}
	f.freeBytes = func() (uint64, error) { return 0, errors.New("private disk details") }
	if _, err = f.WriteString("x"); !errors.Is(err, ErrExportStorage) || strings.Contains(err.Error(), "private") {
		t.Fatal("storage error", err)
	}
	cancel()
	if _, err = f.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = f.WriteString("x"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = f.Seek(0, io.SeekStart); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("x"); !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err = newExportFile(context.Background(), config.DataExportConfig{TempDir: t.TempDir() + "/missing"}); !errors.Is(err, ErrExportStorage) {
			t.Fatal(err)
		}
	}
	a, err := newExportFile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := newExportFile(context.Background())
	if err != nil {
		t.Fatal("leaked slot", err)
	}
	defer b.Close()
}

func TestExportFileConcurrentWritersRespectBudget(t *testing.T) {
	f, err := newExportFile(context.Background(), config.DataExportConfig{MaxBytes: 16})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := f.WriteString("x"); errs <- err }()
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrExportTooLarge) {
			t.Fatal(err)
		}
	}
	info, err := f.Stat()
	if err != nil || info.Size() != 16 || successes != 16 {
		t.Fatal(successes, info, err)
	}
}
