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
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/emailactions"
	"github.com/hcai-chat/hcai-chat/internal/generationoutput"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/observability"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/mailer"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/platform/providers"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/hcai-chat/hcai-chat/internal/reconciliation"
	"github.com/hcai-chat/hcai-chat/internal/tasks"
	"github.com/hcai-chat/hcai-chat/internal/uploadwrite"
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
	billingService := billing.NewService(pool)
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
				if _, err := billingService.ExpireSubscriptions(maintenanceCtx, 1000); err != nil {
					logger.Warn("expire subscriptions", "error", err)
				}
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
	dataRightsService := datarights.NewServiceWithMedia(pool, cfg.MediaRoot, mediaStores, cfg.DataExport)
	webhookService := webhooks.NewService(pool, cfg.WebhookEncryptionKey, cfg.WebhookAllowLocal)
	emailSender := mailer.New(cfg)
	emailActionService := emailactions.NewService(pool, cfg.EmailActionKey, cfg.EmailDeliveryMode, cfg.MediaRoot, cfg.WebOrigin, emailSender)
	authChallengeService := authchallenges.NewService(pool, cfg.EmailActionKey, cfg.EmailDeliveryMode, cfg.MediaRoot, emailSender)
	notificationService := notifications.NewRepository(pool)
	paymentService := payments.NewServiceFromConfig(pool, cfg)
	worker.Handle(creation.JobKind, creationService.HandleJob)
	worker.Handle(creation.FailureEvidenceJobKind, creationService.HandleJob)
	worker.Handle(assets.ScanJobKind, assetService.HandleScanJob)
	worker.Handle(datarights.ExportJobKind, dataRightsService.HandleExportJob)
	worker.Handle(datarights.ExportExpiryJobKind, dataRightsService.HandleExportExpiryJob)
	worker.Handle(datarights.DeletionJobKind, dataRightsService.HandleDeletionJob)
	worker.Handle(datarights.MediaCleanupJobKind, dataRightsService.HandleMediaCleanupJob)
	worker.Handle(productdelivery.CleanupJobKind, productdelivery.CleanupHandler(pool, mediaStores))
	worker.Handle(webhooks.JobKind, webhookService.HandleJob)
	worker.Handle(emailactions.DeliveryJobKind, emailActionService.HandleDeliveryJob)
	worker.Handle(emailactions.ExpiryJobKind, emailActionService.HandleExpiryJob)
	worker.Handle(authchallenges.DeliveryJobKind, authChallengeService.HandleDeliveryJob)
	worker.Handle(authchallenges.ExpiryJobKind, authChallengeService.HandleExpiryJob)
	worker.Handle(notifications.JobKind, notificationService.HandleDeliveryJob)
	worker.Handle(payments.PaymentEventJobKind, paymentService.HandlePaymentEventJob)
	worker.Handle(tasks.ExpiryJobKind, tasks.NewServiceWithPayments(pool, true).HandleExpiryJob)
	worker.Handle(payments.TaskTransferJobKind, paymentService.HandleTaskTransferJob)
	worker.Handle(payments.TaskRefundJobKind, paymentService.HandleTaskRefundJob)
	worker.Handle(payments.ProductRefundJobKind, paymentService.HandleProductRefundJob)
	worker.Handle(payments.ProductSettlementJobKind, paymentService.HandleProductSettlementJob)
	worker.Handle(payments.SellerPayoutFundingJobKind, paymentService.HandleSellerPayoutFundingJob)
	worker.Handle(payments.SellerPayoutFundingCheckJobKind, paymentService.HandleSellerPayoutFundingCheckJob)
	worker.Handle(payments.SellerBankPayoutJobKind, paymentService.HandleSellerBankPayoutJob)
	worker.Handle(payments.SellerBankPayoutCheckJobKind, paymentService.HandleSellerBankPayoutCheckJob)
	worker.Handle(payments.SellerSourceReversalJobKind, paymentService.HandleSellerSourceReversalJob)
	worker.Handle(payments.SellerSourceReversalCheckJobKind, paymentService.HandleSellerSourceReversalCheckJob)
	worker.Handle(payments.ProductSettlementCheckJobKind, paymentService.HandleProductSettlementCheckJob)
	worker.Handle(payments.ProductRefundCheckJobKind, paymentService.HandleProductRefundCheckJob)
	worker.Handle(payments.ProductCheckoutCheckJobKind, paymentService.HandleProductCheckoutCheckJob)
	worker.Handle(payments.ProductIdentityRecoveryJobKind, paymentService.HandleProductIdentityRecoveryJob)
	worker.Handle(payments.ProductCheckoutLookupJobKind, paymentService.HandleProductCheckoutLookupJob)
	worker.Handle(reconciliation.JobKind, reconciliationService.HandleJob)
	stopHoldExpiry := startHoldExpiry(ctx, dataRightsService, logger, time.Minute, 10*time.Second, observabilityRepository)
	stopRefundReconciliation := startRefundReconciliation(ctx, paymentService, logger, time.Minute, 10*time.Second, observabilityRepository)
	stopCheckoutReconciliation := startPeriodicMaintenance(ctx, maintenanceTask{observability.ProductCheckoutReconciliation, paymentService.ReconcileProductCheckouts}, logger, time.Minute, 10*time.Second, observabilityRepository)
	stopSettlementReconciliation := startPeriodicMaintenance(ctx, maintenanceTask{observability.ProductSettlementReconciliation, paymentService.ReconcileProductSettlements}, logger, time.Minute, 10*time.Second, observabilityRepository)
	stopSellerFundingReconciliation := startPeriodicMaintenance(ctx, maintenanceTask{observability.SellerFundingReconciliation, paymentService.ReconcileSellerPayoutFunding}, logger, time.Minute, 10*time.Second, observabilityRepository)
	stopSellerBankReconciliation := startPeriodicMaintenance(ctx, maintenanceTask{observability.SellerBankReconciliation, paymentService.ReconcileSellerBankPayouts}, logger, time.Minute, 10*time.Second, observabilityRepository)
	stopSellerReversalReconciliation := startPeriodicMaintenance(ctx, maintenanceTask{observability.SellerReversalReconciliation, paymentService.ReconcileSellerSourceReversals}, logger, time.Minute, 10*time.Second, observabilityRepository)
	outputCleanup := generationoutput.NewService(pool, mediaStores)
	stopOutputCleanup := startPeriodicMaintenance(ctx, maintenanceTask{observability.GenerationOutputCleanup, outputCleanup.Reconcile}, logger, time.Minute, 10*time.Second, observabilityRepository)
	stopGenerationRecovery := startPeriodicMaintenance(ctx, maintenanceTask{observability.GenerationExecutionRecovery, creationService.ReconcileExecutions}, logger, time.Minute, 10*time.Second, observabilityRepository)
	stopScanRecovery := startPeriodicMaintenance(ctx, maintenanceTask{observability.AssetScanExecutionRecovery, assetService.ReconcileScanExecutions}, logger, time.Minute, 10*time.Second, observabilityRepository)
	stopUploadCleanup := startPeriodicMaintenance(ctx, maintenanceTask{observability.UploadWriteCleanup, uploadwrite.NewService(pool, mediaStores).Reconcile}, logger, time.Minute, 10*time.Second, observabilityRepository)
	logger.Info("worker started")
	runErr := worker.Run(ctx)
	stopUploadCleanup()
	stopScanRecovery()
	stopGenerationRecovery()
	stopOutputCleanup()
	stopRefundReconciliation()
	stopCheckoutReconciliation()
	stopSettlementReconciliation()
	stopSellerFundingReconciliation()
	stopSellerBankReconciliation()
	stopSellerReversalReconciliation()
	stopHoldExpiry()
	if err := runErr; err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("run worker", "error", err)
		os.Exit(1)
	}
}
