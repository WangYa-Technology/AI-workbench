package datarights

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"syscall"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
)

// Slots are shared by all Service instances in a process, including downloads.
var exportSpools = make(chan struct{}, 2)

type exportResourceError struct {
	code  string
	retry bool
}

func (e *exportResourceError) Error() string     { return e.code }
func (e *exportResourceError) ErrorCode() string { return e.code }
func (e *exportResourceError) Retryable() bool   { return e.retry }

var (
	ErrExportBusy        = &exportResourceError{"data_export_busy", true}
	ErrExportTooLarge    = &exportResourceError{"data_export_too_large", false}
	ErrExportRowTooLarge = &exportResourceError{"data_export_record_too_large", false}
	ErrExportStorage     = &exportResourceError{"data_export_storage_unavailable", true}
)

// Keep the descriptor private: promoted os.File.WriteString/ReadFrom/WriteAt
// methods must not bypass the byte budget or cancellation. Seek cannot create a
// sparse file outside the budget. All operations serialize with Close.
type ExportFile struct {
	file         *os.File
	ctx          context.Context
	limits       config.DataExportConfig
	mu           sync.Mutex
	offset, size int64
	closed       bool
	closeErr     error
	freeBytes    func() (uint64, error)
}

func newExportFile(ctx context.Context, settings ...config.DataExportConfig) (*ExportFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg := config.DataExportConfig{}
	if len(settings) > 0 {
		cfg = settings[0]
	}
	cfg = cfg.WithDefaults()
	if cfg.MaxBytes < 0 || cfg.MaxRowBytes < 0 || cfg.MinFreeBytes < 0 {
		return nil, ErrExportStorage
	}
	select {
	case exportSpools <- struct{}{}:
	default:
		return nil, ErrExportBusy
	}
	file, err := os.CreateTemp(cfg.TempDir, "hcai-private-export-*")
	if err != nil {
		<-exportSpools
		return nil, ErrExportStorage
	}
	if err = os.Remove(file.Name()); err != nil {
		_ = file.Close()
		<-exportSpools
		return nil, ErrExportStorage
	}
	f := &ExportFile{file: file, ctx: ctx, limits: cfg}
	f.freeBytes = func() (uint64, error) {
		var stat syscall.Statfs_t
		if err := syscall.Fstatfs(int(file.Fd()), &stat); err != nil {
			return 0, err
		}
		return uint64(stat.Bavail) * uint64(stat.Bsize), nil
	}
	if err := f.checkSpace(0); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func (f *ExportFile) checkSpace(growth int64) error {
	free, err := f.freeBytes()
	if err != nil || free < uint64(f.limits.MinFreeBytes)+uint64(growth) {
		return ErrExportStorage
	}
	return nil
}
func (f *ExportFile) checkOpen() error {
	if f.closed {
		return os.ErrClosed
	}
	return f.ctx.Err()
}
func (f *ExportFile) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkOpen(); err != nil {
		return 0, err
	}
	if int64(len(p)) > f.limits.MaxBytes-f.offset {
		return 0, ErrExportTooLarge
	}
	growth := f.offset + int64(len(p)) - f.size
	if growth > 0 {
		if err := f.checkSpace(growth); err != nil {
			return 0, err
		}
	}
	n, err := f.file.Write(p)
	f.offset += int64(n)
	if f.offset > f.size {
		f.size = f.offset
	}
	if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) {
		err = ErrExportStorage
	}
	return n, err
}
func (f *ExportFile) WriteString(value string) (int, error) { return f.Write([]byte(value)) }
func (f *ExportFile) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkOpen(); err != nil {
		return 0, err
	}
	n, err := f.file.Read(p)
	f.offset += int64(n)
	return n, err
}
func (f *ExportFile) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkOpen(); err != nil {
		return 0, err
	}
	base := int64(0)
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = f.offset
	case io.SeekEnd:
		base = f.size
	default:
		return 0, os.ErrInvalid
	}
	// Subtraction avoids overflow even for adversarial offsets.
	if offset < -base {
		return 0, os.ErrInvalid
	}
	if offset > f.limits.MaxBytes-base {
		return 0, ErrExportTooLarge
	}
	pos, err := f.file.Seek(base+offset, io.SeekStart)
	if err == nil {
		f.offset = pos
	}
	return pos, err
}
func (f *ExportFile) Stat() (os.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkOpen(); err != nil {
		return nil, err
	}
	return f.file.Stat()
}
func (f *ExportFile) Name() string { return f.file.Name() }
func (f *ExportFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		f.closeErr = f.file.Close()
		<-exportSpools
	}
	return f.closeErr
}
