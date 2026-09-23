package datarights

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestPrivateExportSpoolsAreUnlinkedBoundedAndReleased(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	first, err := newExportFile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := newExportFile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	info, err := first.Stat()
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private mode: %v %v", info, err)
	}
	if _, err = os.Stat(first.Name()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("PII spool remains named: %v", err)
	}
	if _, err = first.WriteString("private account evidence"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if file, err := newExportFile(ctx); !errors.Is(err, ErrExportBusy) || file != nil {
		t.Fatalf("unbounded spools: %v", err)
	}
	cancel()
	if _, err := newExportFile(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation", err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := newExportFile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	replacement.Close()
}
