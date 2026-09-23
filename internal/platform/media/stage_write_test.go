package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
)

func TestStageWriteQuotaFailureAndDigest(t *testing.T) {
	settings := config.MediaStageConfig{MaxBytes: 6, MaxObjects: 1, MinFreeBytes: 1, TempDir: t.TempDir()}
	produce := func(w io.Writer) error { _, err := io.WriteString(w, "bytes"); return err }
	stage, err := StageWrite(t.Context(), 5, produce, settings)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	want := sha256.Sum256([]byte("bytes"))
	if stage.Size != 5 || stage.SHA256 != hex.EncodeToString(want[:]) {
		t.Fatal(stage.Size, stage.SHA256)
	}
	body, err := io.ReadAll(io.NewSectionReader(stage, 0, stage.Size))
	if err != nil || string(body) != "bytes" {
		t.Fatal(string(body), err)
	}
	called := false
	if _, err := StageWrite(t.Context(), 5, func(io.Writer) error { called = true; return nil }, settings); !errors.Is(err, ErrStageBusy) || called {
		t.Fatal("producer ran before capacity admission", called, err)
	}
	_ = stage.Close()
	for _, producer := range []func(io.Writer) error{
		nil,
		func(io.Writer) error { return nil },
		func(w io.Writer) error {
			_, _ = io.WriteString(w, "01234567890123456789")
			n, err := io.WriteString(w, "x")
			if n != 0 || !errors.Is(err, ErrIntegrity) {
				t.Fatal("non-sticky overrun", n, err)
			}
			return nil
		},
		func(w io.Writer) error { _, _ = io.WriteString(w, "x"); return io.ErrUnexpectedEOF },
	} {
		if stage, err := StageWrite(t.Context(), 5, producer, settings); err == nil || stage != nil {
			t.Fatal("failed producer accepted", stage, err)
		}
		s, err := StageWrite(t.Context(), 5, produce, settings)
		if err != nil {
			t.Fatal("quota leaked", err)
		}
		_ = s.Close()
	}
	ctx, cancel := context.WithCancel(t.Context())
	if _, err := StageWrite(ctx, 5, func(w io.Writer) error {
		_, _ = io.WriteString(w, "x")
		cancel()
		_, _ = io.WriteString(w, "y")
		return nil
	}, settings); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s, err := StageWrite(t.Context(), 5, produce, settings)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
}

type panicStageReader struct{}

func (panicStageReader) Read([]byte) (int, error) { panic("reader failed") }

func TestStageProducerPanicReleasesQuota(t *testing.T) {
	settings := config.MediaStageConfig{MaxBytes: 6, MaxObjects: 1, MinFreeBytes: 1, TempDir: t.TempDir()}
	for _, invoke := range []func(){
		func() { _, _ = Stage(t.Context(), panicStageReader{}, 5, settings) },
		func() {
			_, _ = StageWrite(t.Context(), 5, func(w io.Writer) error { _, _ = io.WriteString(w, "x"); panic("producer failed") }, settings)
		},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("panic unexpectedly swallowed")
				}
			}()
			invoke()
		}()
		s, err := Stage(t.Context(), bytes.NewBufferString("bytes"), 5, settings)
		if err != nil {
			t.Fatal("panic leaked quota", err)
		}
		_ = s.Close()
	}
}
