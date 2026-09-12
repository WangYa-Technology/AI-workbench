package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/authchallenges"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/emailactions"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/observability"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/platform/providers"
	"github.com/hcai-chat/hcai-chat/internal/reconciliation"
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
	providerRuntimes := providers.NewCatalogWithRegistry(cfg, pool, cfg.WebhookEncryptionKey)
	costRuntime, err := providers.NewOpenAICostsRuntime(cfg)
	if err != nil {
		logger.Error("provider cost reconciliation runtime disabled", "error", err)
		costRuntime = nil
	}
	reconciliationService := reconciliation.NewService(pool, costRuntime, cfg.OpenAIReconciliationOverageThresholdMicros)
	observabilityRepository := observability.NewRepository(pool)
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				maintenanceCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				if err := observabilityRepository.PurgeExpired(maintenanceCtx, 1000); err != nil {
					logger.Warn("purge request observations", "error", err)
				}
				cancel()
			}
		}
	}()
	mediaStores := media.NewCatalogFromConfig(cfg)
	mediaScanner := assets.NewScannerFromConfig(cfg)
	creationService := creation.NewServiceWithMedia(pool, mediaStores, providerRuntimes)
	assetService := assets.NewServiceWithMedia(pool, mediaStores, mediaScanner)
	dataRightsService := datarights.NewServiceWithMedia(pool, cfg.MediaRoot, mediaStores)
	webhookService := webhooks.NewService(pool, cfg.WebhookEncryptionKey, cfg.WebhookAllowLocal)
	emailActionService := emailactions.NewService(pool, cfg.EmailActionKey, cfg.EmailDeliveryMode, cfg.MediaRoot, cfg.WebOrigin)
	authChallengeService := authchallenges.NewService(pool, cfg.EmailActionKey, cfg.EmailDeliveryMode, cfg.MediaRoot)
	notificationService := notifications.NewRepository(pool)
	paymentService := payments.NewServiceFromConfig(pool, cfg)
	worker.Handle(creation.JobKind, creationService.HandleJob)
	worker.Handle(creation.FailureEvidenceJobKind, creationService.HandleJob)
	worker.Handle(assets.ScanJobKind, assetService.HandleScanJob)
	worker.Handle(datarights.ExportJobKind, dataRightsService.HandleExportJob)
	worker.Handle(datarights.ExportExpiryJobKind, dataRightsService.HandleExportExpiryJob)
	worker.Handle(datarights.DeletionJobKind, dataRightsService.HandleDeletionJob)
	worker.Handle(webhooks.JobKind, webhookService.HandleJob)
	worker.Handle(emailactions.DeliveryJobKind, emailActionService.HandleDeliveryJob)
	worker.Handle(emailactions.ExpiryJobKind, emailActionService.HandleExpiryJob)
	worker.Handle(authchallenges.DeliveryJobKind, authChallengeService.HandleDeliveryJob)
	worker.Handle(authchallenges.ExpiryJobKind, authChallengeService.HandleExpiryJob)
	worker.Handle(notifications.JobKind, notificationService.HandleDeliveryJob)
	worker.Handle(payments.PaymentEventJobKind, paymentService.HandlePaymentEventJob)
	worker.Handle(payments.TaskTransferJobKind, paymentService.HandleTaskTransferJob)
	worker.Handle(payments.TaskRefundJobKind, paymentService.HandleTaskRefundJob)
	worker.Handle(payments.ProductRefundJobKind, paymentService.HandleProductRefundJob)
	worker.Handle(reconciliation.JobKind, reconciliationService.HandleJob)
	logger.Info("worker started")
	if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("run worker", "error", err)
		os.Exit(1)
	}
}
