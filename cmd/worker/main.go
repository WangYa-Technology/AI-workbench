package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/emailactions"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/webhooks"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		logger.Error("migrate database", "error", err)
		os.Exit(1)
	}

	repository := jobs.NewRepository(pool)
	worker := jobs.NewWorker(repository, "worker-"+uuid.NewString(), logger)
	creationService := creation.NewService(pool, cfg.MediaRoot, cfg.LocalProviderSource, cfg.LocalProviderEnabled)
	assetService := assets.NewService(pool, cfg.MediaRoot)
	dataRightsService := datarights.NewService(pool, cfg.MediaRoot)
	webhookService := webhooks.NewService(pool, cfg.WebhookEncryptionKey, cfg.WebhookAllowLocal)
	emailActionService := emailactions.NewService(pool, cfg.EmailActionKey, cfg.EmailDeliveryMode, cfg.MediaRoot, cfg.WebOrigin)
	notificationService := notifications.NewRepository(pool)
	worker.Handle(creation.JobKind, creationService.HandleJob)
	worker.Handle(assets.ScanJobKind, assetService.HandleScanJob)
	worker.Handle(datarights.ExportJobKind, dataRightsService.HandleExportJob)
	worker.Handle(datarights.ExportExpiryJobKind, dataRightsService.HandleExportExpiryJob)
	worker.Handle(datarights.DeletionJobKind, dataRightsService.HandleDeletionJob)
	worker.Handle(webhooks.JobKind, webhookService.HandleJob)
	worker.Handle(emailactions.DeliveryJobKind, emailActionService.HandleDeliveryJob)
	worker.Handle(emailactions.ExpiryJobKind, emailActionService.HandleExpiryJob)
	worker.Handle(notifications.JobKind, notificationService.HandleDeliveryJob)
	logger.Info("worker started")
	if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("run worker", "error", err)
		os.Exit(1)
	}
}
