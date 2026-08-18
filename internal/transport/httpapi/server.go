package httpapi

import (
	"context"
	_ "embed"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/community"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/developer"
	"github.com/hcai-chat/hcai-chat/internal/discovery"
	"github.com/hcai-chat/hcai-chat/internal/emailactions"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/observability"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/platform/providers"
	"github.com/hcai-chat/hcai-chat/internal/reconciliation"
	"github.com/hcai-chat/hcai-chat/internal/support"
	"github.com/hcai-chat/hcai-chat/internal/tasks"
	"github.com/hcai-chat/hcai-chat/internal/webhooks"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
)

//go:embed openapi.yaml
var openAPIDocument []byte

type Server struct {
	config         config.Config
	pool           *pgxpool.Pool
	logger         *slog.Logger
	started        time.Time
	identity       *identity.Repository
	discovery      *discovery.Repository
	creation       *creation.Service
	billing        *billing.Service
	assets         *assets.Service
	community      *community.Repository
	tasks          *tasks.Service
	marketplace    *marketplace.Service
	notifications  *notifications.Repository
	admin          *admin.Service
	dataRights     *datarights.Service
	developer      *developer.Service
	support        *support.Service
	observability  *observability.Repository
	metrics        *observability.Metrics
	webhooks       *webhooks.Service
	emailActions   *emailactions.Service
	payments       *payments.Service
	reconciliation *reconciliation.Service
}

func New(cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) http.Handler {
	started := time.Now()
	metrics := observability.NewMetrics(started)
	providerRuntimes := providers.NewCatalog(cfg)
	costRuntime, costRuntimeErr := providers.NewOpenAICostsRuntime(cfg)
	if costRuntimeErr != nil {
		logger.Error("provider cost reconciliation runtime disabled", "error", costRuntimeErr)
	}
	paymentRuntimes := payments.NewRuntimeCatalog()
	if cfg.StripeEnabled {
		paymentRuntimes = payments.NewRuntimeCatalog(payments.NewStripeRuntime(payments.StripeRuntimeConfig{
			SecretKey: cfg.StripeSecretKey, BaseURL: cfg.StripeBaseURL, APIVersion: cfg.StripeAPIVersion,
			LiveMode: cfg.StripeLiveMode, HTTPClient: &http.Client{Timeout: 20 * time.Second},
		}))
	}
	mediaStores := media.NewCatalogFromConfig(cfg)
	mediaScanner := assets.NewScannerFromConfig(cfg)
	server := &Server{
		config: cfg, pool: pool, logger: logger, started: started,
		identity:      identity.NewRepository(pool),
		discovery:     discovery.NewRepository(pool),
		creation:      creation.NewServiceWithMedia(pool, mediaStores, providerRuntimes),
		billing:       billing.NewService(pool),
		assets:        assets.NewServiceWithMedia(pool, mediaStores, mediaScanner),
		community:     community.NewRepository(pool),
		tasks:         tasks.NewServiceWithPayments(pool, cfg.StripeEnabled),
		marketplace:   marketplace.NewService(pool),
		notifications: notifications.NewRepository(pool),
		admin:         admin.NewServiceWithRuntimes(pool, providerRuntimes),
		dataRights:    datarights.NewServiceWithMedia(pool, cfg.MediaRoot, mediaStores),
		developer:     developer.NewService(pool),
		support:       support.NewService(pool),
		observability: observability.NewRepository(pool),
		metrics:       metrics,
		webhooks:      webhooks.NewService(pool, cfg.WebhookEncryptionKey, cfg.WebhookAllowLocal),
		emailActions:  emailactions.NewService(pool, cfg.EmailActionKey, cfg.EmailDeliveryMode, cfg.MediaRoot, cfg.WebOrigin),
		payments: payments.NewServiceWithRuntimes(pool, payments.ServiceConfig{
			Enabled: cfg.StripeEnabled, LiveMode: cfg.StripeLiveMode, APIVersion: cfg.StripeAPIVersion,
			WebhookSecret: cfg.StripeWebhookSecret, WebhookTolerance: time.Duration(cfg.StripeWebhookToleranceSeconds) * time.Second,
		}, paymentRuntimes),
		reconciliation: reconciliation.NewService(pool, costRuntime, cfg.OpenAIReconciliationOverageThresholdMicros),
	}
	router := chi.NewRouter()
	router.Use(httputil.Middleware(logger, cfg.WebOrigin, server.observability.RecordRequest, server.metrics.RecordRequest))
	router.Get("/health", server.health)
	router.Get("/ready", server.ready)
	router.Get("/metrics", server.metricsEndpoint)
	router.Get("/api/v1", server.developerAPIContract)
	router.Route("/api/v1", func(api chi.Router) {
		api.Get("/", server.developerAPIContract)
		api.Get("/principal", server.developerAPIPrincipal)
		api.Get("/errors", server.developerAPIErrors)
		api.Get("/meta", server.meta)
		api.Post("/payments/webhooks/stripe", server.receiveStripeWebhook)
		api.Get("/auth/session", server.session)
		api.Post("/auth/register", server.register)
		api.Post("/auth/login", server.login)
		api.Post("/auth/demo", server.demoLogin)
		api.Post("/auth/logout", server.logout)
		api.Post("/auth/password-reset-requests", server.requestPasswordReset)
		api.Post("/auth/password-reset-confirm", server.confirmPasswordReset)
		api.Post("/auth/email-verification/confirm", server.confirmEmailVerification)
		api.Patch("/account/profile", server.updateProfile)
		api.Get("/account/payouts", server.getPayoutStatus)
		api.Post("/account/payouts/onboarding", server.beginPayoutOnboarding)
		api.Get("/account/email-actions", server.listAccountEmailActions)
		api.Post("/account/email-verification", server.requestEmailVerification)
		api.Get("/account/sessions", server.listAccountSessions)
		api.Delete("/account/sessions/{sessionID}", server.revokeAccountSession)
		api.Post("/account/sessions/revoke-others", server.revokeOtherAccountSessions)
		api.Get("/account/developer-access", server.getDeveloperAccess)
		api.Post("/account/developer-service-accounts", server.createDeveloperServiceAccount)
		api.Post("/account/developer-service-accounts/{accountID}/revoke", server.revokeDeveloperServiceAccount)
		api.Post("/account/developer-service-accounts/{accountID}/keys", server.issueDeveloperAPIKey)
		api.Post("/account/developer-service-accounts/{accountID}/keys/{keyID}/rotate", server.rotateDeveloperAPIKey)
		api.Post("/account/developer-service-accounts/{accountID}/keys/{keyID}/revoke", server.revokeDeveloperAPIKey)
		api.Get("/account/developer-webhooks", server.getDeveloperWebhooks)
		api.Post("/account/developer-webhooks", server.createDeveloperWebhook)
		api.Get("/account/developer-webhooks/{endpointID}/deliveries", server.listDeveloperWebhookDeliveries)
		api.Post("/account/developer-webhooks/{endpointID}/rotate", server.rotateDeveloperWebhookSecret)
		api.Post("/account/developer-webhooks/{endpointID}/revoke", server.revokeDeveloperWebhook)
		api.Post("/account/developer-webhooks/{endpointID}/test", server.testDeveloperWebhook)
		api.Get("/account/data-rights", server.listDataRightsRequests)
		api.Post("/account/data-rights", server.createDataRightsRequest)
		api.Delete("/account/data-rights/{requestID}", server.cancelDataRightsRequest)
		api.Get("/account/data-rights/{requestID}/export", server.downloadDataExport)
		api.Get("/auth/oauth/providers", server.listOAuthProviders)
		api.Post("/auth/oauth/{provider}/start", server.startOAuth)
		api.Get("/notifications", server.listNotifications)
		api.Get("/notification-deliveries", server.listNotificationDeliveries)
		api.Post("/notifications/read-all", server.markAllNotificationsRead)
		api.Post("/notifications/{notificationID}/read", server.markNotificationRead)
		api.Get("/notification-preferences", server.listNotificationPreferences)
		api.Put("/notification-preferences/{kind}", server.updateNotificationPreference)
		api.Get("/support/cases", server.listSupportCases)
		api.Post("/support/cases", server.createSupportCase)
		api.Get("/support/cases/{caseID}", server.getSupportCase)
		api.Post("/support/cases/{caseID}/messages", server.replySupportCase)
		api.Get("/works", server.listWorks)
		api.Get("/works/{workID}", server.getWork)
		api.Get("/search", server.searchDiscovery)
		api.Get("/creators/{handle}", server.getCreator)
		api.Get("/creation/capabilities", server.creationCapabilities)
		api.Post("/generations", server.submitGeneration)
		api.Post("/generations/batch", server.batchGenerations)
		api.Get("/generations", server.listGenerations)
		api.Get("/generations/{generationID}", server.getGeneration)
		api.Post("/generations/{generationID}/cancel", server.cancelGeneration)
		api.Post("/generations/{generationID}/retry", server.retryGeneration)
		api.Put("/generations/{generationID}/favorite", server.favoriteGeneration)
		api.Get("/billing/statement", server.billingStatement)
		api.Get("/assets", server.listAssets)
		api.Get("/assets/saved-works", server.listSavedWorks)
		api.Post("/assets/uploads", server.uploadAsset)
		api.Get("/assets/{assetID}/usages", server.listAssetUsages)
		api.Get("/assets/{assetID}", server.getAsset)
		api.Get("/assets/{assetID}/content", server.assetContent)
		api.Post("/assets/{assetID}/versions", server.uploadAssetVersion)
		api.Get("/products", server.listProducts)
		api.Get("/products/{productID}", server.getProduct)
		api.Post("/products/{productID}/purchase", server.purchaseProduct)
		api.Post("/products/{productID}/checkout", server.checkoutProduct)
		api.Get("/orders", server.listOrders)
		api.Get("/orders/{orderID}", server.getOrder)
		api.Post("/orders/{orderID}/refund", server.refundOrder)
		api.Post("/publications", server.publish)
		api.Get("/content-drafts", server.listContentDrafts)
		api.Post("/content-drafts", server.createContentDraft)
		api.Get("/content-drafts/{draftID}", server.getContentDraft)
		api.Patch("/content-drafts/{draftID}", server.updateContentDraft)
		api.Delete("/content-drafts/{draftID}", server.discardContentDraft)
		api.Post("/content-drafts/{draftID}/publish", server.publishContentDraft)
		api.Get("/community/posts", server.listPosts)
		api.Get("/community/posts/{postID}/comments", server.listComments)
		api.Post("/community/posts/{postID}/comments", server.createComment)
		api.Put("/community/posts/{postID}/reactions/{kind}", server.setPostReaction)
		api.Put("/community/authors/{authorID}/follow", server.setCommunityFollow)
		api.Post("/community/posts/{postID}/reports", server.reportCommunityPost)
		api.Get("/community/reports/mine", server.listMyCommunityReports)
		api.Post("/community/reports/{reportID}/appeals", server.createCommunityAppeal)
		api.Get("/tasks", server.listTasks)
		api.Post("/tasks", server.createTask)
		api.Get("/tasks/{taskID}", server.getTask)
		api.Post("/tasks/{taskID}/checkout", server.checkoutTask)
		api.Post("/tasks/{taskID}/proposals", server.proposeTask)
		api.Post("/tasks/{taskID}/claim", server.claimTask)
		api.Post("/tasks/{taskID}/proposals/{proposalID}/accept", server.acceptTaskProposal)
		api.Post("/tasks/{taskID}/deliveries", server.deliverTask)
		api.Post("/tasks/{taskID}/review", server.reviewTask)
		api.Post("/tasks/{taskID}/disputes", server.disputeTask)
		api.Post("/tasks/{taskID}/cancel", server.cancelTask)
		api.Get("/admin/overview", server.adminOverview)
		api.Get("/admin/users", server.adminListUsers)
		api.Patch("/admin/users/{userID}", server.adminUpdateUser)
		api.Get("/admin/content", server.adminListContent)
		api.Patch("/admin/content/{workID}", server.adminUpdateContent)
		api.Get("/admin/media", server.adminListMedia)
		api.Post("/admin/media/{assetID}/review", server.adminReviewMedia)
		api.Get("/admin/generations", server.adminListGenerations)
		api.Post("/admin/generations/{generationID}/cancel", server.adminCancelGeneration)
		api.Get("/admin/tasks", server.adminListTasks)
		api.Post("/admin/tasks/{taskID}/resolve", server.adminResolveTaskDispute)
		api.Get("/admin/providers", server.adminListProviders)
		api.Patch("/admin/providers/{providerID}", server.adminUpdateProvider)
		api.Get("/admin/models/routes", server.adminGetModelRoutes)
		api.Post("/admin/models/routes/{mode}", server.adminUpdateModelRoute)
		api.Get("/admin/settings", server.adminGetSystemSettings)
		api.Post("/admin/settings", server.adminUpdateSystemSettings)
		api.Get("/admin/finance/accounts", server.adminListFinance)
		api.Post("/admin/finance/accounts/{userID}/adjust", server.adminAdjustFinance)
		api.Get("/admin/provider-cost-reconciliations", server.adminListProviderCostReconciliations)
		api.Post("/admin/provider-cost-reconciliations", server.adminRequestProviderCostReconciliation)
		api.Get("/admin/payments", server.adminListPayments)
		api.Post("/admin/payments/{paymentID}/recover", server.adminRecoverPayment)
		api.Post("/admin/payments/events/{eventID}/replay", server.adminReplayPaymentEvent)
		api.Get("/admin/payment-destinations", server.adminListPaymentDestinations)
		api.Put("/admin/payment-destinations/{userID}", server.adminUpdatePaymentDestination)
		api.Get("/admin/risk/signals", server.adminListRiskSignals)
		api.Post("/admin/risk/signals/{signalID}/review", server.adminReviewRiskSignal)
		api.Get("/admin/risk/rules", server.adminGetRiskRules)
		api.Post("/admin/risk/rules", server.adminUpdateRiskRules)
		api.Get("/admin/discovery/ranking", server.adminGetRankingPolicy)
		api.Post("/admin/discovery/ranking", server.adminUpdateRankingPolicy)
		api.Post("/admin/discovery/ranking/candidates", server.adminCreateRankingCandidate)
		api.Post("/admin/discovery/ranking/evaluations", server.adminRunRankingEvaluation)
		api.Post("/admin/discovery/ranking/rollout", server.adminUpdateRankingRollout)
		api.Get("/admin/discovery/operations", server.adminGetDiscoveryOperations)
		api.Post("/admin/discovery/index/analyze", server.adminAnalyzeDiscoveryIndex)
		api.Get("/admin/audit", server.adminListAudit)
		api.Get("/admin/observability", server.adminGetOperationalDiagnostics)
		api.Get("/admin/developer/access", server.adminGetDeveloperAccess)
		api.Put("/admin/developer/control", server.adminUpdateDeveloperControl)
		api.Post("/admin/developer/service-accounts/{accountID}/revoke", server.adminRevokeDeveloperServiceAccount)
		api.Post("/admin/developer/keys/{keyID}/revoke", server.adminRevokeDeveloperAPIKey)
		api.Get("/admin/developer/webhooks/dead-letters", server.adminListWebhookDeadLetters)
		api.Post("/admin/developer/webhooks/deliveries/{deliveryID}/replay", server.adminReplayWebhookDelivery)
		api.Get("/admin/email-actions/dead-letters", server.adminListEmailActionDeadLetters)
		api.Post("/admin/email-actions/{actionID}/retry", server.adminRetryEmailAction)
		api.Post("/admin/email-actions/{actionID}/cancel", server.adminCancelEmailAction)
		api.Get("/admin/data-rights", server.adminListDataRights)
		api.Get("/admin/data-rights/holds", server.adminListDataRightsHolds)
		api.Post("/admin/data-rights/holds", server.adminCreateDataRightsHold)
		api.Post("/admin/data-rights/holds/{holdID}/release", server.adminReleaseDataRightsHold)
		api.Get("/admin/governance/reports", server.adminListReports)
		api.Post("/admin/governance/reports/{reportID}/resolve", server.adminResolveReport)
		api.Get("/admin/governance/appeals", server.adminListAppeals)
		api.Post("/admin/governance/appeals/{appealID}/resolve", server.adminResolveAppeal)
		api.Get("/admin/support/cases", server.adminListSupportCases)
		api.Get("/admin/support/cases/{caseID}", server.adminGetSupportCase)
		api.Post("/admin/support/cases/{caseID}/messages", server.adminReplySupportCase)
		api.Patch("/admin/support/cases/{caseID}", server.adminUpdateSupportCase)
	})
	router.Get("/api/openapi.yaml", server.openapi)
	return router
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	httputil.JSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"service":       "hcai-api",
		"uptimeSeconds": int(time.Since(s.started).Seconds()),
	})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "The database is not ready.", true)
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) meta(w http.ResponseWriter, _ *http.Request) {
	httputil.JSON(w, http.StatusOK, map[string]any{
		"name":               "HCAI CHAT",
		"environment":        s.config.Environment,
		"defaultLocale":      "en-US",
		"supportedLocales":   []string{"en-US", "zh-CN"},
		"defaultCurrency":    "USD",
		"localDemoAvailable": s.config.Environment != "production" && s.config.DemoDataEnabled,
		"localProvider":      map[string]any{"enabled": s.config.LocalProviderEnabled, "label": "Deterministic local test provider"},
		"paymentProvider":    map[string]any{"enabled": s.config.StripeEnabled, "provider": "stripe", "liveMode": s.config.StripeLiveMode},
	})
}

func (s *Server) openapi(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openAPIDocument)
}
