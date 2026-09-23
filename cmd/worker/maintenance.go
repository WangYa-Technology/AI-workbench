package main

import (
	"context"
	"log/slog"
	"time"
)

type maintenanceRecorder interface {
	RecordMaintenance(context.Context, string, bool) error
}

type maintenanceTask struct {
	kind string
	run  func(context.Context, int) (int, error)
}

func runMaintenancePass(parent context.Context, task maintenanceTask, recorder maintenanceRecorder, logger *slog.Logger, budget time.Duration) {
	pass, stop := context.WithTimeout(parent, budget)
	processed, err := task.run(pass, 100)
	if err == nil {
		err = pass.Err()
	}
	stop()
	if parent.Err() != nil {
		return
	}
	if err != nil {
		logger.Warn("maintenance pass failed", "code", task.kind+"_failed", "processed", processed)
	} else if processed > 0 {
		logger.Info("maintenance pass completed", "kind", task.kind, "processed", processed)
	}
	if recorder != nil {
		// The pass may have exhausted its deadline. Recording has a separate
		// bounded context, and failure here must not prevent subsequent scans.
		recordCtx, cancel := context.WithTimeout(parent, 2*time.Second)
		defer cancel()
		if recordErr := recorder.RecordMaintenance(recordCtx, task.kind, err == nil); recordErr != nil && parent.Err() == nil {
			logger.Warn("maintenance observation unavailable", "code", "maintenance_observation_failed", "kind", task.kind)
		}
	}
}

// Independent periodic work gets its own deadline and startup pass.
func startPeriodicMaintenance(parent context.Context, task maintenanceTask, logger *slog.Logger, interval, budget time.Duration, recorder maintenanceRecorder) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for ctx.Err() == nil {
			runMaintenancePass(ctx, task, recorder, logger, budget)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}
