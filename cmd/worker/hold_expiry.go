package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/observability"
)

type legalHoldExpirer interface {
	ExpireLegalHolds(context.Context, int) (int, error)
	ResumeLegalHoldCleanups(context.Context, int) (int, error)
	ReconcileProductCleanups(context.Context, int) (int, error)
	ReconcileDeletions(context.Context, int) (int, error)
	ReconcileOriginalMediaCleanups(context.Context, int) (int, error)
}

// Start immediately so a restart catches up without waiting for the first tick.
// Failed and skipped records remain in the database for subsequent passes.
func startHoldExpiry(parent context.Context, service legalHoldExpirer, logger *slog.Logger, interval, budget time.Duration, recorder maintenanceRecorder) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		tasks := []maintenanceTask{
			{observability.LegalHoldExpiry, service.ExpireLegalHolds},
			{observability.LegalHoldCleanup, service.ResumeLegalHoldCleanups},
			{observability.ProductCleanupReconciliation, service.ReconcileProductCleanups},
			{observability.AccountDeletionReconciliation, service.ReconcileDeletions},
			{observability.OriginalMediaCleanupReconciliation, service.ReconcileOriginalMediaCleanups},
		}
		for ctx.Err() == nil {
			for _, task := range tasks {
				if ctx.Err() != nil {
					return
				}
				runMaintenancePass(ctx, task, recorder, logger, budget)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}
