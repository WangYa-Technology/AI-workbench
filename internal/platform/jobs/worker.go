package jobs

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type Handler func(context.Context, Job) error

type Worker struct {
	repository  *Repository
	owner       string
	logger      *slog.Logger
	handlers    map[string]Handler
	lease       time.Duration
	concurrency int
	active      atomic.Int64
}

func NewWorker(repository *Repository, owner string, logger *slog.Logger) *Worker {
	return NewWorkerWithLease(repository, owner, logger, 30*time.Second)

}

func NewWorkerWithLease(repository *Repository, owner string, logger *slog.Logger, lease time.Duration) *Worker {
	if lease < 30*time.Millisecond || lease > 24*time.Hour {
		lease = 30 * time.Second
	}
	return &Worker{repository: repository, owner: owner, logger: logger, handlers: map[string]Handler{}, lease: lease, concurrency: 4}
}

// NewWorkerWithOptions configures bounded execution without changing lease semantics.
func NewWorkerWithOptions(repository *Repository, owner string, logger *slog.Logger, lease time.Duration, concurrency int) *Worker {
	w := NewWorkerWithLease(repository, owner, logger, lease)
	if concurrency >= 1 && concurrency <= 128 {
		w.concurrency = concurrency
	}
	return w
}

func (w *Worker) Handle(kind string, handler Handler) {
	w.handlers[kind] = handler
}

func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()
	semaphore := make(chan struct{}, w.concurrency)
	var running sync.WaitGroup
	for {
		select {
		case <-ctx.Done():
			waitDone := make(chan struct{})
			go func() {
				running.Wait()
				close(waitDone)
			}()
			select {
			case <-waitDone:
			case <-time.After(w.lease):
				w.logger.Warn("worker shutdown timed out", "running_jobs", w.active.Load())
			}
			return ctx.Err()
		case <-ticker.C:
			select {
			case semaphore <- struct{}{}:
			default:
				continue
			}
			if err := w.repository.RecoverExpired(ctx); err != nil {
				w.logger.Error("recover jobs", "error", err)
			}
			job, err := w.repository.Claim(ctx, w.owner, w.lease)
			if errors.Is(err, ErrNoJob) {
				<-semaphore
				continue
			}
			if err != nil {
				<-semaphore
				w.logger.Error("claim job", "error", err)
				continue
			}
			running.Add(1)
			w.active.Add(1)
			go func(job Job) {
				defer running.Done()
				defer func() { <-semaphore }()
				defer w.active.Add(-1)
				handler, exists := w.handlers[job.Kind]
				var err error
				if !exists {
					err = codedError("handler_not_registered")
				} else {
					err = w.execute(ctx, job, handler)
				}
				if err != nil {
					w.logger.Error("job failed", "job_id", job.ID, "kind", job.Kind, "error_code", failureCode(err))
					if failErr := w.repository.Fail(ctx, job, w.owner, err); failErr != nil {
						w.logger.Error("record job failure", "job_id", job.ID, "kind", job.Kind, "error_code", failureCode(failErr))
					}
					return
				}
				if err := w.repository.Complete(ctx, job, w.owner); err != nil {
					w.logger.Error("complete job", "job_id", job.ID, "kind", job.Kind, "error_code", failureCode(err))
				}
			}(job)
		}
	}
}

func (w *Worker) execute(ctx context.Context, job Job, handler Handler) error {
	handlerCtx, cancelHandler := context.WithCancel(ctx)
	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
	defer cancelHandler()
	defer cancelHeartbeat()
	heartbeatDone := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(w.lease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				heartbeatDone <- nil
				return
			case <-ticker.C:
				if err := w.repository.Renew(heartbeatCtx, job, w.owner, w.lease); err != nil {
					if heartbeatCtx.Err() != nil {
						heartbeatDone <- nil
						return
					}
					cancelHandler()
					heartbeatDone <- err
					return
				}
			}
		}
	}()
	handlerErr := handler(handlerCtx, job)
	cancelHeartbeat()
	if heartbeatErr := <-heartbeatDone; heartbeatErr != nil {
		return heartbeatErr
	}
	return handlerErr
}

type codedError string

func (e codedError) Error() string     { return string(e) }
func (e codedError) ErrorCode() string { return string(e) }
