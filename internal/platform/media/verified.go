package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
)

var ErrIntegrity = errors.New("media content does not match immutable evidence")

// StagedObject holds verified bytes in a private temporary file. Consumers must
// close it. Verification finishes before any bytes are exposed to a caller;
// hashing while streaming to the client would detect corruption too late.
type StagedObject struct {
	file        *os.File
	Size        int64
	SHA256      string
	reservation int64
	limits      config.MediaStageConfig
	freeBytes   func() (uint64, error)
	closeOnce   sync.Once
	closeErr    error
}

func Stage(ctx context.Context, body io.Reader, maximum int64, settings ...config.MediaStageConfig) (*StagedObject, error) {
	cfg := config.MediaStageConfig{}
	if len(settings) > 0 {
		cfg = settings[0]
	}
	s, err := newStagedObject(ctx, maximum, cfg)
	if err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = s.Close()
		}
	}()
	if err := s.read(ctx, body, maximum); err != nil {
		return nil, err
	}
	complete = true
	return s, nil
}

func (s *StagedObject) read(ctx context.Context, body io.Reader, maximum int64) error {
	if body == nil {
		return ErrIntegrity
	}
	return s.write(ctx, maximum, func(w io.Writer) error {
		_, err := io.Copy(w, io.LimitReader(&contextReader{ctx: ctx, reader: body}, maximum+1))
		return err
	})
}

// StageWrite admits the complete resource budget before invoking the producer.
// The writer cannot seek or exceed maximum bytes; no goroutine or full in-memory
// buffer is needed to build an immutable archive. Errors are sticky even if a
// producer accidentally ignores a failed Write.
func StageWrite(ctx context.Context, maximum int64, produce func(io.Writer) error, settings ...config.MediaStageConfig) (*StagedObject, error) {
	if produce == nil {
		return nil, ErrIntegrity
	}
	cfg := config.MediaStageConfig{}
	if len(settings) > 0 {
		cfg = settings[0]
	}
	s, err := newStagedObject(ctx, maximum, cfg)
	if err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = s.Close()
		}
	}()
	if err := s.write(ctx, maximum, produce); err != nil {
		return nil, err
	}
	complete = true
	return s, nil
}

func (s *StagedObject) write(ctx context.Context, maximum int64, produce func(io.Writer) error) error {
	h := sha256.New()
	w := &boundedStageWriter{target: io.MultiWriter(stageWriter{s, ctx}, h), maximum: maximum}
	err := produce(w)
	if err = errors.Join(err, w.err, ctx.Err()); err != nil {
		return err
	}
	if w.total <= 0 || w.total > maximum {
		return ErrIntegrity
	}
	s.Size, s.SHA256 = w.total, hex.EncodeToString(h.Sum(nil))
	if _, err = s.file.Seek(0, io.SeekStart); err != nil {
		return ErrStageStorage
	}
	return nil
}

type boundedStageWriter struct {
	target         io.Writer
	maximum, total int64
	err            error
}

func (w *boundedStageWriter) Write(body []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	limit := int64(len(body))
	if remaining := w.maximum + 1 - w.total; limit > remaining {
		limit = remaining
	}
	n, err := w.target.Write(body[:limit])
	w.total += int64(n)
	if err == nil && w.total > w.maximum {
		err = ErrIntegrity
	}
	if err == nil && n != len(body) {
		err = io.ErrShortWrite
	}
	w.err = err
	return n, err
}

// ReadAt only reads the admitted private file and does not change its cursor.
// It supports bounded archive inspection without exposing a filesystem path.
func (s *StagedObject) ReadAt(p []byte, offset int64) (int, error) {
	return s.file.ReadAt(p, offset)
}

func (s *StagedObject) Close() error {
	s.closeOnce.Do(func() {
		if s.file != nil {
			s.closeErr = s.file.Close()
		}
		stageResources.Lock()
		if s.reservation > 0 {
			stageResources.bytes -= s.reservation
			stageResources.objects--
		}
		stageResources.Unlock()
	})
	return s.closeErr
}

// Put uses the same create-only semantics as Store.Put without buffering a
// complete video in memory. Only supported storage adapters may receive copies.
func (s *StagedObject) Put(ctx context.Context, store Store, key, mime string) error {
	uploader, ok := store.(interface {
		PutStream(context.Context, string, io.ReadSeeker, int64, string, string) error
	})
	if !ok {
		return fmt.Errorf("storage does not support verified streaming writes")
	}
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return uploader.PutStream(ctx, key, s.file, s.Size, s.SHA256, mime)
}

func (s *StagedObject) Object(requested *ByteRange, info ObjectInfo) (Object, error) {
	start, length := int64(0), s.Size
	if requested != nil {
		if requested.Start < 0 || requested.End < requested.Start || requested.End >= s.Size {
			return Object{}, ErrInvalidKey
		}
		start, length = requested.Start, requested.End-requested.Start+1
	}
	if _, err := s.file.Seek(start, io.SeekStart); err != nil {
		return Object{}, err
	}
	info.Size = s.Size
	info.ETag = `"sha256-` + s.SHA256 + `"`
	return Object{Body: &limitedReadCloser{Reader: io.LimitReader(s.file, length), closer: s}, Info: info}, nil
}

// OpenVerified checks the entire object, including for a range request, and
// returns those exact staged bytes. The store cannot change bytes between
// verification and delivery by replacing or modifying the original object.
func OpenVerified(ctx context.Context, store Store, key, digest string, size int64, requested *ByteRange, settings ...config.MediaStageConfig) (Object, error) {
	s, info, err := OpenVerifiedStage(ctx, store, key, digest, size, settings...)
	if err != nil {
		return Object{}, err
	}
	result, err := s.Object(requested, info)
	if err != nil {
		_ = s.Close()
	}
	return result, err
}

// OpenVerifiedStage returns seek-independent access only after full immutable
// verification. The caller owns Close, including on archive parsing failures.
func OpenVerifiedStage(ctx context.Context, store Store, key, digest string, size int64, settings ...config.MediaStageConfig) (*StagedObject, ObjectInfo, error) {
	var info ObjectInfo
	if len(digest) != 64 {
		return nil, info, ErrIntegrity
	}
	decoded, digestErr := hex.DecodeString(digest)
	if size <= 0 || size > MaxVerifiedBytes || digestErr != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != digest || store == nil {
		return nil, info, ErrIntegrity
	}
	cfg := config.MediaStageConfig{}
	if len(settings) > 0 {
		cfg = settings[0]
	}
	s, err := newStagedObject(ctx, size, cfg)
	if err != nil {
		return nil, info, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = s.Close()
		}
	}()
	o, err := store.Open(ctx, key, nil)
	if err != nil {
		return nil, info, err
	}
	if o.Body == nil {
		return nil, info, ErrIntegrity
	}
	closed := false
	defer func() {
		if !closed {
			_ = o.Body.Close()
		}
	}()
	readErr := s.read(ctx, o.Body, size)
	closed = true
	closeErr := o.Body.Close()
	if readErr != nil || closeErr != nil {
		return nil, info, errors.Join(readErr, closeErr)
	}
	if s.Size != size || s.SHA256 != digest {
		return nil, info, ErrIntegrity
	}
	complete = true
	return s, o.Info, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
