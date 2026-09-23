package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/observability"
)

type productRefundReconciler interface {
	ReconcileProductRefunds(context.Context, int) (int, error)
}

// Own context and budget: retention maintenance cannot starve funds checks.
func startRefundReconciliation(parent context.Context, service productRefundReconciler, logger *slog.Logger, interval, budget time.Duration, recorder maintenanceRecorder) func() {
	return startPeriodicMaintenance(parent, maintenanceTask{observability.ProductRefundReconciliation, service.ReconcileProductRefunds}, logger, interval, budget, recorder)
}
