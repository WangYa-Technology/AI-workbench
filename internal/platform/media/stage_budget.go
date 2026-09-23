package media

import (
	"context"
	"os"
	"sync"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
)

const MaxVerifiedBytes int64 = 100 << 20

type stageResourceError string

func (e stageResourceError) Error() string     { return string(e) }
func (e stageResourceError) ErrorCode() string { return string(e) }
func (e stageResourceError) Retryable() bool   { return true }

const (
	ErrStageBusy    = stageResourceError("media_stage_busy")
	ErrStageStorage = stageResourceError("media_stage_storage_unavailable")
)

// Admission is shared even when API services independently construct Catalogs.
// Requests cannot select settings; production Catalogs use the process config.
var stageResources struct {
	sync.Mutex
	bytes   int64
	objects int
}

func newStagedObject(ctx context.Context, maximum int64, settings config.MediaStageConfig) (*StagedObject, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maximum <= 0 || maximum > MaxVerifiedBytes {
		return nil, ErrIntegrity
	}
	cfg := settings.WithDefaults()
	if cfg.MaxBytes < 1 || cfg.MaxObjects < 1 || cfg.MinFreeBytes < 0 {
		return nil, ErrStageStorage
	}
	// The extra byte used to reject oversized sources also consumes space.
	reservation := maximum + 1
	stageResources.Lock()
	if stageResources.objects >= cfg.MaxObjects || reservation > cfg.MaxBytes-stageResources.bytes {
		stageResources.Unlock()
		return nil, ErrStageBusy
	}
	stageResources.objects++
	stageResources.bytes += reservation
	stageResources.Unlock()
	s := &StagedObject{reservation: reservation, limits: cfg}
	f, err := os.CreateTemp(cfg.TempDir, "hcai-verified-*")
	if err != nil {
		_ = s.Close()
		return nil, ErrStageStorage
	}
	s.file = f
	// Unlink before reading private bytes. Process exit closes the descriptor.
	if err := os.Remove(f.Name()); err != nil {
		_ = s.Close()
		return nil, ErrStageStorage
	}
	s.freeBytes = func() (uint64, error) { return stageFreeBytes(f) }
	if err := s.checkSpace(reservation); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func (s *StagedObject) checkSpace(growth int64) error {
	free, err := s.freeBytes()
	if err != nil || growth < 0 || free < uint64(s.limits.MinFreeBytes)+uint64(growth) {
		return ErrStageStorage
	}
	return nil
}

type stageWriter struct {
	stage *StagedObject
	ctx   context.Context
}

func (w stageWriter) Write(body []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if err := w.stage.checkSpace(int64(len(body))); err != nil {
		return 0, err
	}
	n, err := w.stage.file.Write(body)
	if err != nil {
		return n, ErrStageStorage
	}
	return n, nil
}
