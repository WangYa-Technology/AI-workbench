import type { components, operations } from './schema'
import { i18n } from '../i18n'
import { runJSONCommand, runUploadCommand, runFinanceCommand, assertCommandSessionReady, CommandSessionChangedError } from '../lib/taskCommands'

export type ProductPaymentDispute = components['schemas']['ProductPaymentDispute']
export type ProductPaymentDisputeDetail = components['schemas']['ProductPaymentDisputeDetail']
export type ProductPaymentDisputeCommand = components['schemas']['ProductPaymentDisputeCommand']
export type ProductPaymentDisputeCommandResult = components['schemas']['ProductPaymentDisputeCommandResult']
export type ProductPaymentDisputeQuery = NonNullable<operations['listProductPaymentDisputes']['parameters']['query']>

export type User = components['schemas']['User']
export type Work = components['schemas']['Work']
export type WorkPage = components['schemas']['WorkPage']
export type SearchPage = components['schemas']['SearchPage']
export type SearchResult = components['schemas']['SearchResult']
export type CreatorProfile = components['schemas']['CreatorProfile']
export type CreatorProduct = components['schemas']['CreatorProduct']
export type Generation = components['schemas']['Generation']
export type GenerationPage = components['schemas']['GenerationPage']
export type CreationCapability = components['schemas']['CreationCapability']
export type CreationModel = components['schemas']['CreationModel']
export type ModelCapabilities = components['schemas']['ModelCapabilities']
export type CreationCapabilities = components['schemas']['CreationCapabilities']
export type Conversation = components['schemas']['Conversation']
export type ConversationPage = components['schemas']['ConversationPage']
export type ConversationCreate = components['schemas']['ConversationCreate']
export type GenerationCreate = components['schemas']['GenerationCreate']
export type GenerationBatchInput = components['schemas']['GenerationBatchInput']
export type GenerationBatchResult = components['schemas']['GenerationBatchResult']
export type Asset = components['schemas']['Asset']
export type AssetPage = components['schemas']['AssetPage']
export type SavedWork = components['schemas']['SavedWork']
export type SavedWorkPage = components['schemas']['SavedWorkPage']
export type AssetUsagePage = components['schemas']['AssetUsagePage']
export type AssetListQuery = NonNullable<operations['listAssets']['parameters']['query']>
export type SavedWorkListQuery = NonNullable<operations['listSavedWorks']['parameters']['query']>
export type AssetUsageListQuery = NonNullable<operations['listAssetUsages']['parameters']['query']>
export type PublicationCreate = components['schemas']['PublicationCreate']
export type Publication = components['schemas']['Publication']
export type ContentDraft = components['schemas']['ContentDraft']
export type ContentDraftPage = components['schemas']['ContentDraftPage']
export type ContentDraftSave = components['schemas']['ContentDraftSave']
export type ContentDraftQuery = NonNullable<operations['listContentDrafts']['parameters']['query']>
export type CommunityPost = components['schemas']['CommunityPost']
export type CommunityPostCreate = components['schemas']['CommunityPostCreate']
export type CommunityPostPage = components['schemas']['CommunityPostPage']
export type CommunityPostQuery = NonNullable<operations['listCommunityPosts']['parameters']['query']>
export type CommunityComment = components['schemas']['CommunityComment']
export type CommunityCommentPage = components['schemas']['CommunityCommentPage']
export type CommunityCommentQuery = NonNullable<operations['listCommunityComments']['parameters']['query']>
export type CommunityInteractionState = components['schemas']['CommunityInteractionState']
export type CommunityFollowState = components['schemas']['CommunityFollowState']
export type CommunityReport = components['schemas']['CommunityReport']
export type CommunityReportCreate = components['schemas']['CommunityReportCreate']
export type CommunityAppeal = components['schemas']['CommunityAppeal']
export type CommunityReportQuery = NonNullable<operations['listMyCommunityReports']['parameters']['query']>
export type TaskSummary = components['schemas']['TaskSummary']
export type TaskDetail = components['schemas']['TaskDetail']
export type TaskCreate = components['schemas']['TaskCreate']
export type TaskProposalCreate = components['schemas']['TaskProposalCreate']
export type TaskDeliveryCreate = components['schemas']['TaskDeliveryCreate']
export type TaskReview = components['schemas']['TaskReview']
export type TaskCheckoutRequest = components['schemas']['TaskCheckoutRequest']
export type TaskPaymentCheckout = components['schemas']['TaskPaymentCheckout']
export type TaskType = { scope?: string; code: string; nameZh: string; nameEn: string; icon: string; sortOrder: number }
export type Product = components['schemas']['Product']
export type ProductDraft = components['schemas']['ProductDraft']
export type SellerProduct = components['schemas']['SellerProduct']
export type SellerSale = components['schemas']['SellerSale']
export type SellerSaleDetail = components['schemas']['SellerSaleDetail']
export type SellerSaleEvent = components['schemas']['SellerSaleEvent']
export type SellerSalesPage = components['schemas']['SellerSalesPage']
export type SellerSaleEventsPage = components['schemas']['SellerSaleEventsPage']
export type SellerFundsBalance = components['schemas']['SellerFundsBalance']
export type SellerPayoutItem = components['schemas']['SellerPayoutItem']
export type SellerPayoutOption = components['schemas']['SellerPayoutOption']
export type SellerPayoutBankOption = components['schemas']['SellerPayoutBankOption']
export type SellerPayoutReviewItem = components['schemas']['SellerPayoutReviewItem']
export type SellerBankPayoutInput = operations['submitSellerBankPayout']['requestBody']['content']['application/json']
export type SellerBankPayoutResumeInput = operations['resumeSellerBankPayout']['requestBody']['content']['application/json']
export type SellerBankPayoutOperation = components['schemas']['SellerBankPayoutOperation']
export type SellerSourceReversalInput = components['schemas']['SellerSourceReversalInput']
export type SellerSourceClosureInput = components['schemas']['SellerSourceClosureInput']
export type SellerSourceReversalOperation = components['schemas']['SellerSourceReversalOperation']
export type SellerFundingAdmissionInput = operations['admitSellerPayoutFunding']['requestBody']['content']['application/json']
export type SellerPayoutReviewInput = operations['reviewSellerPayout']['requestBody']['content']['application/json']
export type ListingPage = components['schemas']['ListingPage']
export type ListingMutation = components['schemas']['ListingMutation']
export type MarketplaceLicense = components['schemas']['MarketplaceLicense']
export type PaymentCheckout = components['schemas']['PaymentCheckout']
export type Order = components['schemas']['Order']
export type OrderPage = components['schemas']['OrderPage']
export type OrderQuery = NonNullable<operations['listOrders']['parameters']['query']>
export type Meta = components['schemas']['Meta']
export type LoginRequest = components['schemas']['LoginRequest']
export type AuthChallenge = components['schemas']['AuthChallenge']
export type UnifiedAuthStartRequest = components['schemas']['UnifiedAuthStartRequest']
export type UnifiedAuthStartResponse = components['schemas']['UnifiedAuthStartResponse']
export type UnifiedAuthCodeRequest = components['schemas']['UnifiedAuthCodeRequest']
export type UnifiedAuthConfirmRequest = components['schemas']['UnifiedAuthConfirmRequest']
export type UnifiedAuthRegisterRequest = components['schemas']['UnifiedAuthRegisterRequest']
export type ProfileUpdate = components['schemas']['ProfileUpdate']
export type PayoutStatus = components['schemas']['PayoutStatus']
export type PayoutOnboardingLink = components['schemas']['PayoutOnboardingLink']
export type AccountSession = components['schemas']['AccountSession']
export type AccountSessionPage = components['schemas']['AccountSessionPage']
export type AccountSessionQuery = NonNullable<operations['listAccountSessions']['parameters']['query']>
export type IdentityEmailAction = components['schemas']['IdentityEmailAction']
export type IdentityEmailTransition = components['schemas']['IdentityEmailTransition']
export type AccountEmailActionQuery = NonNullable<operations['listAccountEmailActions']['parameters']['query']>
export type OAuthProvider = components['schemas']['OAuthProvider']
export type DeveloperAccess = components['schemas']['DeveloperAccess']
export type DeveloperServiceAccount = components['schemas']['DeveloperServiceAccount']
export type DeveloperServiceAccountCreate = components['schemas']['DeveloperServiceAccountCreate']
export type DeveloperAPIKey = components['schemas']['DeveloperAPIKey']
export type DeveloperCredential = components['schemas']['DeveloperCredential']
export type DeveloperKeyCreate = components['schemas']['DeveloperKeyCreate']
export type DeveloperKeyRotate = components['schemas']['DeveloperKeyRotate']
export type DeveloperTransition = components['schemas']['DeveloperTransition']
export type AdminVersionTransition = components['schemas']['AdminVersionTransition']
export type DeveloperControlUpdate = components['schemas']['DeveloperControlUpdate']
export type DeveloperWebhookAccess = components['schemas']['DeveloperWebhookAccess']
export type DeveloperWebhookEndpoint = components['schemas']['DeveloperWebhookEndpoint']
export type DeveloperWebhookCredential = components['schemas']['DeveloperWebhookCredential']
export type DeveloperWebhookDelivery = components['schemas']['DeveloperWebhookDelivery']
export type DeveloperWebhookDeliveryPage = components['schemas']['DeveloperWebhookDeliveryPage']
export type DeveloperWebhookCreate = components['schemas']['DeveloperWebhookCreate']
export type DeveloperWebhookDeliveryQuery = { cursor?: string; limit?: number }
export type AdminWebhookRecoveryQuery = NonNullable<operations['adminListWebhookDeadLetters']['parameters']['query']>
export type AdminEmailRecoveryQuery = NonNullable<operations['adminListIdentityEmailDeadLetters']['parameters']['query']>
export type ExportJob = components['schemas']['ExportJob']
export type ExportJobPage = components['schemas']['ExportJobPage']
export type ExportJobQuery = NonNullable<operations['listAdminExportJobs']['parameters']['query']>
export type DeletionJob = components['schemas']['DeletionJob']
export type DeletionJobPage = components['schemas']['DeletionJobPage']
export type DeletionJobQuery = NonNullable<operations['listAdminDeletionJobs']['parameters']['query']>
export type MediaCleanup = components['schemas']['MediaCleanup']
export type ProductDeliveryStatus = components['schemas']['ProductDeliveryStatus']
export type ProductDeliveryEvidenceGap = components['schemas']['ProductDeliveryEvidenceGap']
export type ProductDeliveryEvidenceFilter = NonNullable<operations['listProductDeliveryEvidenceGaps']['parameters']['query']>
export type ProductDeliveryRepairInput = components['schemas']['ProductDeliveryRepairInput']
export type MediaCleanupPage = components['schemas']['MediaCleanupPage']
export type MediaCleanupQuery = NonNullable<operations['listAdminMediaCleanups']['parameters']['query']>
export type MediaCleanupRetry = components['schemas']['MediaCleanupRetry']
export type DataRightsRequest = components['schemas']['DataRightsRequest']
export type DataRightsCreate = components['schemas']['DataRightsCreate']
export type DataRightsLegalHold = components['schemas']['DataRightsLegalHold']
export type DataRightsLegalHoldCreate = components['schemas']['DataRightsLegalHoldCreate']
export type DataRightsRequestPage = components['schemas']['DataRightsRequestPage']
export type DataRightsLegalHoldPage = components['schemas']['DataRightsLegalHoldPage']
export type DataRightsQuery = NonNullable<operations['listDataRightsRequests']['parameters']['query']>
export type Notification = components['schemas']['Notification']
export type NotificationPage = components['schemas']['NotificationPage']
export type NotificationPreference = components['schemas']['NotificationPreference']
export type NotificationDeliveryEvidence = components['schemas']['NotificationDeliveryEvidence']
export type NotificationDeliveryEvidencePage = components['schemas']['NotificationDeliveryEvidencePage']
export type NotificationDeliveryEvidenceQuery = NonNullable<operations['listNotificationDeliveries']['parameters']['query']>
export type SupportCase = components['schemas']['SupportCase']
export type SupportCaseCreate = components['schemas']['SupportCaseCreate']
export type SupportReply = components['schemas']['SupportReply']
export type SupportQuery = NonNullable<operations['listSupportCases']['parameters']['query']>
export type AdminSupportReply = components['schemas']['AdminSupportReply']
export type AdminSupportUpdate = components['schemas']['AdminSupportUpdate']
export type AdminSupportQuery = NonNullable<operations['listAdminSupportCases']['parameters']['query']>
export type BillingStatement = components['schemas']['BillingStatement']
export type BillingCheckout = components['schemas']['BillingCheckout']
export type PointOverview = components['schemas']['PointOverview']
export type SubscriptionPlan = components['schemas']['SubscriptionPlan']
export type WalletTopupSettings = components['schemas']['WalletTopupSettings']
export type WalletTopupSettingsUpdate = components['schemas']['WalletTopupSettingsUpdate']
export type SubscriptionModel = components['schemas']['SubscriptionModel']
export type SubscriptionPlanInput = components['schemas']['SubscriptionPlanInput']
export type SubscriptionPlanUpdate = components['schemas']['SubscriptionPlanUpdate']
export type ModelPointPricing = components['schemas']['ModelPointPricing']
export type AdminOverview = components['schemas']['AdminOverview']
export type AdminUser = components['schemas']['AdminUser']
export type AdminUserUpdate = components['schemas']['AdminUserUpdate']
export type AdminUserQuery = NonNullable<operations['listAdminUsers']['parameters']['query']>
export type AdminContent = components['schemas']['AdminContent']
export type AdminContentUpdate = components['schemas']['AdminContentUpdate']
export type AdminContentQuery = NonNullable<operations['listAdminContent']['parameters']['query']>
export type AdminMediaItem = components['schemas']['AdminMediaItem']
export type AdminMediaReview = components['schemas']['AdminMediaReview']
export type AdminMediaQuery = NonNullable<operations['listAdminMedia']['parameters']['query']>
export type AdminGeneration = components['schemas']['AdminGeneration']
export type AdminGenerationQuery = NonNullable<operations['listAdminGenerations']['parameters']['query']>
export type AdminTaskOperation = components['schemas']['AdminTaskOperation']
export type AdminTaskDisputeResolution = components['schemas']['AdminTaskDisputeResolution']
export type AdminTaskQuery = NonNullable<operations['listAdminTasks']['parameters']['query']>
export type AdminProvider = components['schemas']['AdminProvider']
export type AdminProviderUpdate = components['schemas']['AdminProviderUpdate']
export type AdminProviderModel = components['schemas']['AdminProviderModel']
export type AdminProviderConfig = components['schemas']['AdminProviderConfig']
export type AdminProviderConfigCreate = components['schemas']['AdminProviderConfigCreate']
export type AdminProviderConfigUpdate = components['schemas']['AdminProviderConfigUpdate']
export type AdminProviderModelCreate = components['schemas']['AdminProviderModelCreate']
export type AdminProviderModelUpdate = components['schemas']['AdminProviderModelUpdate']
export type AdminModelRoutePolicy = components['schemas']['AdminModelRoutePolicy']
export type AdminModelRouteQuery = NonNullable<operations['getAdminModelRoutes']['parameters']['query']>
export type AdminModelRouteRevision = components['schemas']['AdminModelRouteRevision']
export type AdminModelRouteUpdate = components['schemas']['AdminModelRouteUpdate']
export type AdminSystemSettings = components['schemas']['AdminSystemSettings']
export type AdminSystemSettingUpdate = components['schemas']['AdminSystemSettingUpdate']
export type SiteConfiguration = components['schemas']['SiteConfiguration']
export type AdminFinanceAccount = components['schemas']['AdminFinanceAccount']
export type AdminFinanceAdjustment = components['schemas']['AdminFinanceAdjustment']
export type AdminFinanceQuery = NonNullable<operations['listAdminFinanceAccounts']['parameters']['query']>
export type AdminRefundHistory = components['schemas']['AdminRefundHistory']
export type AdminRefundCheckPage = components['schemas']['AdminRefundCheckPage']
export type AdminRefundReadReceiptPage = components['schemas']['AdminRefundReadReceiptPage']
export type AdminRefundCheckDetail = components['schemas']['AdminRefundCheckDetail']
export type ProductWebhookQuarantine = components['schemas']['ProductWebhookQuarantine']
export type ProductWebhookQuarantineFilter = NonNullable<operations['listProductWebhookQuarantines']['parameters']['query']>
export type AdminPaymentOperation = components['schemas']['AdminPaymentOperation']
export type AdminPaymentOperationPage = components['schemas']['AdminPaymentOperationPage']
export type AdminPaymentRecovery = components['schemas']['AdminPaymentRecovery']
export type AdminPaymentEventReplay = components['schemas']['AdminPaymentEventReplay']
export type AdminPaymentDestination = components['schemas']['AdminPaymentDestination']
export type AdminPaymentDestinationUpdate = components['schemas']['AdminPaymentDestinationUpdate']
export type AdminProviderCostReconciliation = components['schemas']['AdminProviderCostReconciliation']
export type AdminProviderCostReconciliationPage = components['schemas']['AdminProviderCostReconciliationPage']
export type AdminProviderCostReconciliationRequest = components['schemas']['AdminProviderCostReconciliationRequest']
export type AdminPaymentQuery = NonNullable<operations['listAdminPayments']['parameters']['query']>
export type AdminPaymentDestinationQuery = NonNullable<operations['listAdminPaymentDestinations']['parameters']['query']>
export type AdminPaymentProviderConfig = {
  id: string
  provider: 'stripe' | 'waffo_pancake' | 'epay'
  enabled: boolean
  environment: 'test' | 'prod'
  merchantId: string
  storeId: string
  productIdOnetime: string
  productIdSubscription: string
  secretConfigured: boolean
  connectorConfigured: boolean
  createdAt: string
  updatedAt: string
}
export type AdminPaymentProviderConfigUpdate = Partial<Pick<AdminPaymentProviderConfig, 'enabled' | 'environment' | 'merchantId' | 'storeId' | 'productIdOnetime' | 'productIdSubscription'>>
export type AdminProviderCostReconciliationQuery = NonNullable<operations['listAdminProviderCostReconciliations']['parameters']['query']>
export type AdminRiskSignal = components['schemas']['AdminRiskSignal']
export type AdminRiskSignalQuery = NonNullable<operations['listAdminRiskSignals']['parameters']['query']>
export type AdminRiskReview = components['schemas']['AdminRiskReview']
export type AdminRiskRulePolicy = components['schemas']['AdminRiskRulePolicy']
export type AdminRiskRuleRevision = components['schemas']['AdminRiskRuleRevision']
export type AdminRiskRuleUpdate = components['schemas']['AdminRiskRuleUpdate']
export type AdminRiskRuleHistoryQuery = NonNullable<operations['getAdminRiskRules']['parameters']['query']>
export type AdminRankingPolicy = components['schemas']['AdminRankingPolicy']
export type AdminRankingRevision = components['schemas']['AdminRankingRevision']
export type AdminRankingUpdate = components['schemas']['AdminRankingUpdate']
export type AdminRankingHistoryQuery = NonNullable<operations['getAdminDiscoveryRanking']['parameters']['query']>
export type AdminRankingRolloutUpdate = components['schemas']['AdminRankingRolloutUpdate']
export type AdminRankingEvaluation = components['schemas']['AdminRankingEvaluation']
export type AdminDiscoveryIndexRun = components['schemas']['AdminDiscoveryIndexRun']
export type AdminDiscoveryOperations = components['schemas']['AdminDiscoveryOperations']
export type AdminDiscoveryHistoryQuery = NonNullable<operations['getAdminDiscoveryOperations']['parameters']['query']>
export type AdminOperationalDiagnostics = components['schemas']['AdminOperationalDiagnostics']
export type AdminGovernanceReport = components['schemas']['AdminGovernanceReport']
export type AdminReportResolution = components['schemas']['AdminReportResolution']
export type AdminGovernanceReportQuery = NonNullable<operations['listAdminGovernanceReports']['parameters']['query']>
export type AdminGovernanceAppeal = components['schemas']['AdminGovernanceAppeal']
export type AdminAppealResolution = components['schemas']['AdminAppealResolution']
export type AdminGovernanceAppealQuery = NonNullable<operations['listAdminGovernanceAppeals']['parameters']['query']>

type Session = components['schemas']['Session']
type ErrorEnvelope = components['schemas']['ErrorEnvelope']

export class APIError extends Error {
  readonly status: number
  readonly code: string
  readonly retryable: boolean
  readonly requestId?: string

  constructor(status: number, error: ErrorEnvelope['error']) {
    super(error.message)
    this.name = 'APIError'
    this.status = status
    this.code = error.code
    this.retryable = error.retryable
    this.requestId = error.requestId
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const method = (init?.method || 'GET').toUpperCase()
  // Authentication writes advance the transition; other writes must not pick
  // up a new cookie while their page still describes the preceding account.
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && !path.startsWith('/auth/')) assertCommandSessionReady()
  const headers = new Headers(init?.headers)
  const formDataBody = init?.body && Object.prototype.toString.call(init.body) === '[object FormData]'
  if (init?.body && !formDataBody && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  const response = await fetch(`/api/v1${path}`, { ...init, headers, credentials: 'include' })
  if (!response.ok) {
    let envelope: ErrorEnvelope | undefined
    try {
      envelope = (await response.json()) as ErrorEnvelope
    } catch {
      // The fallback below preserves a useful error when an intermediary returns non-JSON.
    }
    throw new APIError(response.status, envelope?.error ?? {
      code: 'unexpected_response',
      message: `The server returned HTTP ${response.status}.`,
      retryable: response.status >= 500,
    })
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

function communityCommand<T>(path: string, body: unknown): Promise<T> {
  return runJSONCommand(path, 'POST', body,
    (key, serialized) => request<T>(path, { method: 'POST', body: serialized, headers: { 'Idempotency-Key': key } }),
    error => error instanceof APIError && error.status >= 400 && error.status < 500 && error.status !== 408 && error.status !== 429)
}

function uploadCommand(path: string, form: FormData): Promise<Asset> {
  return runUploadCommand(path, form,
    (key, snapshot) => request<Asset>(path, { method: 'POST', body: snapshot, headers: { 'Idempotency-Key': key } }),
    error => error instanceof APIError && error.status >= 400 && error.status < 500 && error.status !== 408 && error.status !== 429)
}

function financeAdjustmentCommand(id: string, input: AdminFinanceAdjustment) {
  const path = `/admin/finance/accounts/${encodeURIComponent(id)}/adjust`
  const snapshot = { ...input }
  return runFinanceCommand(path, snapshot, key => request<components['schemas']['AdminFinanceAdjustmentResult']>(path, {
    method: 'POST', headers: { 'Idempotency-Key': key }, body: JSON.stringify(snapshot),
  }))
}

function queryParameters(query: Record<string, unknown>): URLSearchParams {
  const params = new URLSearchParams()
  Object.entries(query).forEach(([key, value]) => {
    if (value !== undefined && value !== '') params.set(key, String(value))
  })
  return params
}

export const api = {
  communityCapabilities: () => request<components['schemas']['CommunityCapabilities']>('/community/capabilities'),
  meta: () => request<Meta>('/meta'),
  siteConfiguration: () => request<SiteConfiguration>('/site-config'),
  creationCapabilities: () => request<CreationCapabilities>('/creation/capabilities'),
  session: () => request<Session>('/auth/session'),
  login: (input: LoginRequest) => request<Session>('/auth/login', {
    method: 'POST', body: JSON.stringify(input),
  }),
  unifiedAuthStart: (input: UnifiedAuthStartRequest) => request<UnifiedAuthStartResponse>('/auth/unified/start', {
    method: 'POST', body: JSON.stringify(input),
  }),
  unifiedAuthSendCode: (input: UnifiedAuthCodeRequest) => request<{ challenge: AuthChallenge }>('/auth/unified/send-code', {
    method: 'POST', body: JSON.stringify(input),
  }),
  unifiedAuthLoginCode: (input: UnifiedAuthConfirmRequest) => request<Session>('/auth/unified/login-code', {
    method: 'POST', body: JSON.stringify(input),
  }),
  unifiedAuthRegister: (input: UnifiedAuthRegisterRequest) => request<Session>('/auth/unified/register', {
    method: 'POST', body: JSON.stringify(input),
  }),
  logout: () => request<void>('/auth/logout', { method: 'POST' }),
  requestPasswordReset: (email: string) => request<{ accepted: boolean }>('/auth/password-reset-requests', { method: 'POST', body: JSON.stringify({ email }) }),
  confirmPasswordReset: (token: string, password: string) => request<{ revokedSessionCount: number }>('/auth/password-reset-confirm', { method: 'POST', body: JSON.stringify({ token, password }) }),
  confirmEmailVerification: (token: string) => request<void>('/auth/email-verification/confirm', { method: 'POST', body: JSON.stringify({ token }) }),
  updateProfile: (input: ProfileUpdate) => request<{ user: User }>('/account/profile', {
    method: 'PATCH', body: JSON.stringify(input),
  }),
  getPayoutStatus: () => request<PayoutStatus>('/account/payouts'),
  beginPayoutOnboarding: () => request<PayoutOnboardingLink>('/account/payouts/onboarding', { method: 'POST' }),
  listAccountSessions: (query: AccountSessionQuery = {}) => {
    const params = new URLSearchParams()
    if (query.cursor) params.set('cursor', query.cursor)
    if (query.limit) params.set('limit', String(query.limit))
    return request<AccountSessionPage>(`/account/sessions${params.size ? `?${params}` : ''}`)
  },
  listAccountEmailActions: (query: AccountEmailActionQuery = {}) => {
    const params = queryParameters(query)
    return request<{ items: IdentityEmailAction[]; nextCursor?: string }>(`/account/email-actions${params.size ? `?${params}` : ''}`)
  },
  requestEmailVerification: () => request<IdentityEmailAction>('/account/email-verification', { method: 'POST' }),
  revokeAccountSession: (id: string) => request<void>(`/account/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  revokeOtherAccountSessions: () => request<{ revokedCount: number }>('/account/sessions/revoke-others', { method: 'POST' }),
  getDeveloperAccess: () => request<DeveloperAccess>('/account/developer-access'),
  createDeveloperServiceAccount: (input: DeveloperServiceAccountCreate) => request<DeveloperServiceAccount>('/account/developer-service-accounts', { method: 'POST', body: JSON.stringify(input) }),
  revokeDeveloperServiceAccount: (id: string, input: DeveloperTransition) => request<DeveloperServiceAccount>(`/account/developer-service-accounts/${encodeURIComponent(id)}/revoke`, { method: 'POST', body: JSON.stringify(input) }),
  issueDeveloperAPIKey: (accountId: string, input: DeveloperKeyCreate) => request<DeveloperCredential>(`/account/developer-service-accounts/${encodeURIComponent(accountId)}/keys`, { method: 'POST', body: JSON.stringify(input) }),
  rotateDeveloperAPIKey: (accountId: string, keyId: string, input: DeveloperKeyRotate) => request<DeveloperCredential>(`/account/developer-service-accounts/${encodeURIComponent(accountId)}/keys/${encodeURIComponent(keyId)}/rotate`, { method: 'POST', body: JSON.stringify(input) }),
  revokeDeveloperAPIKey: (accountId: string, keyId: string, input: DeveloperTransition) => request<DeveloperAPIKey>(`/account/developer-service-accounts/${encodeURIComponent(accountId)}/keys/${encodeURIComponent(keyId)}/revoke`, { method: 'POST', body: JSON.stringify(input) }),
  getDeveloperWebhooks: () => request<DeveloperWebhookAccess>('/account/developer-webhooks'),
  listDeveloperWebhookDeliveries: (id: string, query: DeveloperWebhookDeliveryQuery = {}) => {
    const params = queryParameters(query)
    return request<DeveloperWebhookDeliveryPage>(`/account/developer-webhooks/${encodeURIComponent(id)}/deliveries${params.size ? `?${params}` : ''}`)
  },
  createDeveloperWebhook: (input: DeveloperWebhookCreate) => request<DeveloperWebhookCredential>('/account/developer-webhooks', { method: 'POST', body: JSON.stringify(input) }),
  rotateDeveloperWebhook: (id: string, input: DeveloperTransition) => request<DeveloperWebhookCredential>(`/account/developer-webhooks/${encodeURIComponent(id)}/rotate`, { method: 'POST', body: JSON.stringify(input) }),
  revokeDeveloperWebhook: (id: string, input: DeveloperTransition) => request<DeveloperWebhookEndpoint>(`/account/developer-webhooks/${encodeURIComponent(id)}/revoke`, { method: 'POST', body: JSON.stringify(input) }),
  testDeveloperWebhook: (id: string) => request<DeveloperWebhookDelivery>(`/account/developer-webhooks/${encodeURIComponent(id)}/test`, { method: 'POST' }),
  listDataRightsRequests: (query: DataRightsQuery = {}) => {
    const params = queryParameters(query)
    return request<DataRightsRequestPage>(`/account/data-rights${params.size ? `?${params}` : ''}`)
  },
  createDataRightsRequest: (input: DataRightsCreate) => request<DataRightsRequest>('/account/data-rights', { method: 'POST', body: JSON.stringify(input) }),
  cancelDataRightsRequest: (id: string) => request<DataRightsRequest>(`/account/data-rights/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  listOAuthProviders: () => request<{ items: OAuthProvider[] }>('/auth/oauth/providers'),
  listNotifications: (query: { readState?: 'all' | 'unread' | 'read'; kind?: string; cursor?: string; limit?: number } = {}) => {
    const params = queryParameters(query)
    return request<NotificationPage>(`/notifications${params.size ? `?${params}` : ''}`)
  },
  markNotificationRead: (id: string) => request<Notification>(`/notifications/${encodeURIComponent(id)}/read`, { method: 'POST' }),
  markAllNotificationsRead: () => request<{ markedCount: number }>('/notifications/read-all', { method: 'POST' }),
  listNotificationPreferences: () => request<{ items: NotificationPreference[] }>('/notification-preferences'),
  listNotificationDeliveries: (query: NotificationDeliveryEvidenceQuery = {}) => {
    const params = new URLSearchParams()
    if (query.cursor) params.set('cursor', query.cursor)
    if (query.limit) params.set('limit', String(query.limit))
    return request<NotificationDeliveryEvidencePage>(`/notification-deliveries${params.size ? `?${params}` : ''}`)
  },
  updateNotificationPreference: (kind: string, inAppEnabled: boolean, expectedVersion: number) => request<NotificationPreference>(`/notification-preferences/${encodeURIComponent(kind)}`, {
    method: 'PUT', body: JSON.stringify({ inAppEnabled, expectedVersion }),
  }),
  listSupportCases: (query: SupportQuery = {}) => {
    const params = queryParameters(query)
    return request<{ items: SupportCase[]; nextCursor?: string }>(`/support/cases${params.size ? `?${params}` : ''}`)
  },
  createSupportCase: (input: SupportCaseCreate) => request<SupportCase>('/support/cases', { method: 'POST', body: JSON.stringify(input) }),
  getSupportCase: (id: string) => request<SupportCase>(`/support/cases/${encodeURIComponent(id)}`),
  replySupportCase: (id: string, input: SupportReply) => request<SupportCase>(`/support/cases/${encodeURIComponent(id)}/messages`, { method: 'POST', body: JSON.stringify(input) }),
  listWorks: (cursor?: string) => request<WorkPage>(`/works${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`),
  browseWorks: (query: { q?: string; kind?: string; promptVisibility?: string; cursor?: string } = {}) => {
    const params = new URLSearchParams()
    Object.entries(query).forEach(([key, value]) => { if (value) params.set(key, value) })
    return request<WorkPage>(`/works${params.size ? `?${params}` : ''}`)
  },
  getWork: (id: string) => request<Work>(`/works/${encodeURIComponent(id)}`),
  search: (query: { q: string; types?: string[]; page?: number; limit?: number }) => {
    const params = new URLSearchParams({ q: query.q })
    if (query.types?.length) params.set('types', query.types.join(','))
    if (query.page !== undefined) params.set('page', String(query.page))
    if (query.limit !== undefined) params.set('limit', String(query.limit))
    return request<SearchPage>(`/search?${params}`)
  },
  getCreator: (handle: string, query: { worksPage?: number; productsPage?: number; limit?: number } = {}) => request<CreatorProfile>(`/creators/${encodeURIComponent(handle)}?${queryParameters(query)}`),
  createConversation: (input: ConversationCreate = {}) => request<Conversation>('/conversations', {
    method: 'POST', body: JSON.stringify(input),
  }),
  listConversations: () => request<ConversationPage>('/conversations'),
  createGeneration: (input: GenerationCreate) => request<Generation>('/generations', {
    method: 'POST', headers: { 'Idempotency-Key': crypto.randomUUID() }, body: JSON.stringify(input),
  }),
  favoriteGeneration: (id: string, active: boolean) => request<Generation>(`/generations/${encodeURIComponent(id)}/favorite`, { method: 'PUT', body: JSON.stringify({ active }) }),
  batchGenerations: (input: GenerationBatchInput) => generationCommand<GenerationBatchResult>('/generations/batch', input),
  getGeneration: (id: string) => request<Generation>(`/generations/${encodeURIComponent(id)}`),
  listGenerations: (query: { conversationId?: string; mode?: string; status?: string; dateFrom?: string; dateTo?: string; cursor?: string; limit?: number } = {}) => {
    const params = queryParameters(query)
    return request<GenerationPage>(`/generations${params.size ? `?${params}` : ''}`)
  },
  cancelGeneration: (id: string, reason: string) => generationCommand<Generation>(`/generations/${encodeURIComponent(id)}/cancel`, { reason }),
  retryGeneration: (id: string) => generationCommand<Generation>(`/generations/${encodeURIComponent(id)}/retry`),
	billingStatement: (query: { direction?: string; entryType?: string; dateFrom?: string; dateTo?: string; cursor?: string; limit?: number } = {}) => {
    const params = queryParameters(query)
    return request<BillingStatement>(`/billing/statement${params.size ? `?${params}` : ''}`)
	},
	pointOverview: (query: { cursor?: string; limit?: number } = {}) => {
		const params = queryParameters(query)
		return request<PointOverview>(`/billing/points${params.size ? `?${params}` : ''}`)
	},
	checkoutWalletTopup: (amountCents: number, idempotencyKey?: string) => billingCheckout('/billing/topups/checkout', { amountCents }, idempotencyKey),
	checkoutSubscription: (planId: string, idempotencyKey?: string) => billingCheckout('/billing/subscriptions/checkout', { planId }, idempotencyKey),
	listAssets: (query: AssetListQuery = {}) => {
		const params = queryParameters(query)
		return request<AssetPage>(`/assets${params.size ? `?${params}` : ''}`)
	},
	listSavedWorks: (query: SavedWorkListQuery = {}) => {
		const params = new URLSearchParams()
		if (query.cursor) params.set('cursor', query.cursor)
		if (query.limit) params.set('limit', String(query.limit))
		return request<SavedWorkPage>(`/assets/saved-works${params.size ? `?${params}` : ''}`)
	},
	listAssetUsages: (id: string, query: AssetUsageListQuery = {}) => {
		const params = new URLSearchParams()
		if (query.cursor) params.set('cursor', query.cursor)
		if (query.limit) params.set('limit', String(query.limit))
		return request<AssetUsagePage>(`/assets/${encodeURIComponent(id)}/usages${params.size ? `?${params}` : ''}`)
	},
	uploadAsset: (form: globalThis.FormData) => uploadCommand('/assets/uploads', form),
	uploadAssetVersion: (id: string, form: globalThis.FormData) => uploadCommand(`/assets/${encodeURIComponent(id)}/versions`, form),
	getAsset: (id: string) => request<Asset>(`/assets/${encodeURIComponent(id)}`),
	publish: (input: PublicationCreate) => request<Publication>('/publications', {
		method: 'POST', body: JSON.stringify(input),
	}),
		listContentDrafts: (query: ContentDraftQuery = {}) => {
			const params = new URLSearchParams()
			if (query.cursor) params.set('cursor', query.cursor)
			if (query.limit) params.set('limit', String(query.limit))
			return request<ContentDraftPage>(`/content-drafts${params.size ? `?${params}` : ''}`)
		},
	getContentDraft: (id: string) => request<ContentDraft>(`/content-drafts/${encodeURIComponent(id)}`),
	createContentDraft: (input: ContentDraftSave) => request<ContentDraft>('/content-drafts', { method: 'POST', body: JSON.stringify(input) }),
	updateContentDraft: (id: string, input: ContentDraftSave) => request<ContentDraft>(`/content-drafts/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(input) }),
	publishContentDraft: (id: string, expectedVersion: number) => request<Publication>(`/content-drafts/${encodeURIComponent(id)}/publish`, { method: 'POST', body: JSON.stringify({ expectedVersion }) }),
	discardContentDraft: (id: string, expectedVersion: number) => request<void>(`/content-drafts/${encodeURIComponent(id)}`, { method: 'DELETE', body: JSON.stringify({ expectedVersion }) }),
  listCommunityPosts: (query: CommunityPostQuery = {}) => {
    const params = new URLSearchParams()
    if (query.cursor) params.set('cursor', query.cursor)
    if (query.limit) params.set('limit', String(query.limit))
    if (query.mine !== undefined) params.set('mine', String(query.mine))
    if (query.view) params.set('view', query.view)
    if (query.category) params.set('category', query.category)
    if (query.sort) params.set('sort', query.sort)
    if (query.q) params.set('q', query.q)
    return request<CommunityPostPage>(`/community/posts${params.size ? `?${params}` : ''}`)
  },
  createCommunityPost: (input: CommunityPostCreate) => communityCommand<CommunityPost>('/community/posts', input),
  getOwnedCommunityPost: (id: string) => request<CommunityPost>(`/community/posts/${encodeURIComponent(id)}/owned`),
  updateCommunityPost: (id: string, input: components['schemas']['CommunityPostUpdate']) => request<CommunityPost>(`/community/posts/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(input) }),
  deleteCommunityPost: (id: string, expectedVersion: number) => request<void>(`/community/posts/${encodeURIComponent(id)}`, { method: 'DELETE', body: JSON.stringify({ expectedVersion }) }),
  getCommunityReport: (id: string) => request<CommunityReport>(`/community/reports/${encodeURIComponent(id)}`),
  getCommunityPost: (postId: string) => request<CommunityPost>(`/community/posts/${encodeURIComponent(postId)}`),
  listCommunityComments: (postId: string, query: CommunityCommentQuery = {}) => {
    const params = new URLSearchParams()
    if (query.cursor) params.set('cursor', query.cursor)
    if (query.limit) params.set('limit', String(query.limit))
    return request<CommunityCommentPage>(`/community/posts/${encodeURIComponent(postId)}/comments${params.size ? `?${params}` : ''}`)
  },
  createCommunityComment: (postId: string, body: string) => communityCommand<CommunityComment>(`/community/posts/${encodeURIComponent(postId)}/comments`, { body }),
  setCommunityReaction: (postId: string, kind: 'like' | 'bookmark', active: boolean) => request<CommunityInteractionState>(`/community/posts/${encodeURIComponent(postId)}/reactions/${kind}`, {
    method: 'PUT', body: JSON.stringify({ active }),
  }),
  setCommunityFollow: (authorId: string, active: boolean) => request<CommunityFollowState>(`/community/authors/${encodeURIComponent(authorId)}/follow`, {
    method: 'PUT', body: JSON.stringify({ active }),
  }),
  reportCommunityPost: (postId: string, input: CommunityReportCreate) => request<CommunityReport>(`/community/posts/${encodeURIComponent(postId)}/reports`, {
    method: 'POST', body: JSON.stringify(input),
  }),
  listMyCommunityReports: (query: CommunityReportQuery = {}) => {
    const params = queryParameters(query)
    return request<{ items: CommunityReport[]; nextCursor?: string }>(`/community/reports/mine${params.size ? `?${params}` : ''}`)
  },
  createCommunityAppeal: (reportId: string, reason: string) => request<CommunityAppeal>(`/community/reports/${encodeURIComponent(reportId)}/appeals`, {
    method: 'POST', body: JSON.stringify({ reason }),
  }),
  listProducts: (query: { q?: string; type?: string; category?: string; license?: string; sort?: string; limit?: number; cursor?: string } = {}) => {
    const params = queryParameters(query)
    return request<{ items: Product[]; total: number; categoryCounts: Record<string, number>; nextCursor?: string }>(`/products${params.size ? `?${params}` : ''}`)
  },
  listSellerProducts: (review: boolean, query: { status?: string; cursor?: string; limit?: number } = {}) => request<ListingPage>(`/${review ? 'admin' : 'seller'}/products?${queryParameters(query)}`),
  getSellerProduct: (id: string, review = false) => request<SellerProduct>(`/${review ? 'admin' : 'seller'}/products/${encodeURIComponent(id)}`),
  listSellerLicenses: () => request<{ items: MarketplaceLicense[] }>('/seller/licenses'),
  listSellerSales: (query: { status?: string; environment?: string; productId?: string; cursor?: string; limit?: number } = {}) => request<SellerSalesPage>(`/seller/sales?${queryParameters(query)}`),
  getSellerSale: (id: string) => request<SellerSaleDetail>(`/seller/sales/${encodeURIComponent(id)}`),
  listSellerSaleEvents: (id: string, query: { cursor?: string; limit?: number } = {}) => request<SellerSaleEventsPage>(`/seller/sales/${encodeURIComponent(id)}/events?${queryParameters(query)}`),
  getSellerFunds: () => request<SellerFundsBalance>('/seller/funds', { cache: 'no-store' }),
  listSellerPayoutOptions: (query: { cursor?: string; limit?: number } = {}) => request<components['schemas']['SellerPayoutOptions']>(`/seller/payout-options?${queryParameters(query)}`, { cache: 'no-store' }),
  listProductPaymentDisputes: (query: ProductPaymentDisputeQuery = {}) => request<components['schemas']['ProductPaymentDisputePage']>(`/admin/product-disputes?${queryParameters(query)}`, { cache: 'no-store' }),
  getProductPaymentDispute: (id: string) => request<ProductPaymentDisputeDetail>(`/admin/product-disputes/${encodeURIComponent(id)}`, { cache: 'no-store' }),
  operateProductPaymentDispute: (id: string, input: ProductPaymentDisputeCommand) => {
    const path = `/admin/product-disputes/${encodeURIComponent(id)}/operations`
    const snapshot = { ...input }
    return runFinanceCommand(path, snapshot, key => request<ProductPaymentDisputeCommandResult>(path, {
      method: 'POST', headers: { 'Idempotency-Key': key }, body: JSON.stringify(snapshot),
    }))
  },
  listSellerPayoutReviews: (query: { cursor?: string; limit?: number } = {}) => request<components['schemas']['SellerPayoutReviewPage']>(`/admin/seller-payout-requests?${queryParameters(query)}`, { cache: 'no-store' }),
  getSellerPayoutReview: (id: string) => request<SellerPayoutReviewItem>(`/admin/seller-payout-requests/${encodeURIComponent(id)}`, { cache: 'no-store' }),
  reviewSellerPayout: (id: string, input: SellerPayoutReviewInput) => {
    const snapshot = { ...input }
    const path = `/admin/seller-payout-requests/${encodeURIComponent(id)}/review`
    return runFinanceCommand(path, snapshot, key => request<components['schemas']['SellerPayoutReviewResult']>(path, {
      method: 'POST', headers: { 'Idempotency-Key': key }, body: JSON.stringify(snapshot),
    }))
  },
  admitSellerPayoutFunding: (id: string, input: SellerFundingAdmissionInput) => {
    const path = `/admin/seller-payout-requests/${encodeURIComponent(id)}/funding`
    const snapshot = { reviewId: input.reviewId, expectedRevision: input.expectedRevision, settlementId: input.settlementId, amountCents: input.amountCents, bankDestinationId: input.bankDestinationId, reason: input.reason, confirmed: input.confirmed }
    return runFinanceCommand(path, snapshot, key => request<components['schemas']['SellerFundingAdmissionResult']>(path, {
      method: 'POST', headers: { 'Idempotency-Key': key }, body: JSON.stringify(snapshot),
    }))
  },
  getSellerBankPayoutOperation: (id: string) => request<SellerBankPayoutOperation>(`/admin/seller-payout-requests/${encodeURIComponent(id)}/bank-payout`, { cache: 'no-store' }),
  getSellerSourceReversalOperation: (id: string) => request<SellerSourceReversalOperation>(`/admin/seller-payout-requests/${encodeURIComponent(id)}/source-reversal`, { cache: 'no-store' }),
  submitSellerSourceReversal: (id: string, input: SellerSourceReversalInput) => {
    const path = `/admin/seller-payout-requests/${encodeURIComponent(id)}/source-reversal`
    const snapshot = { sourceTransferId: input.sourceTransferId, expectedUpdatedAt: input.expectedUpdatedAt, bankCommandId: input.bankCommandId ?? null, bankResultId: input.bankResultId ?? null, reason: input.reason, confirmed: input.confirmed }
    return runFinanceCommand(path, snapshot, key => request<components['schemas']['SellerSourceReversalSubmission']>(path, {
      method: 'POST', headers: { 'Idempotency-Key': key }, body: JSON.stringify(snapshot),
    }))
  },
  closeSellerSourceReversal: (id: string, input: SellerSourceClosureInput) => {
    const path = `/admin/seller-source-reversals/${encodeURIComponent(id)}/close`
    const snapshot = { readId: input.readId, expectedUpdatedAt: input.expectedUpdatedAt, reason: input.reason, confirmed: input.confirmed }
    return runFinanceCommand(path, snapshot, key => request<components['schemas']['SellerSourceClosureSubmission']>(path, {
      method: 'POST', headers: { 'Idempotency-Key': key }, body: JSON.stringify(snapshot),
    }))
  },
  resumeSellerBankPayout: (id: string, input: SellerBankPayoutResumeInput) => {
    const path = `/admin/seller-payout-requests/${encodeURIComponent(id)}/bank-payout/resume`
    const snapshot = { commandId: input.commandId, expectedJobId: input.expectedJobId, reason: input.reason, confirmed: input.confirmed }
    return runFinanceCommand(path, snapshot, key => request<components['schemas']['SellerBankPayoutResumeResult']>(path, {
      method: 'POST', headers: { 'Idempotency-Key': key }, body: JSON.stringify(snapshot),
    }))
  },
  submitSellerBankPayout: (id: string, input: SellerBankPayoutInput) => {
    const path = `/admin/seller-payout-requests/${encodeURIComponent(id)}/bank-payout`
    const snapshot = { sourceTransferId: input.sourceTransferId, reviewId: input.reviewId, expectedRevision: input.expectedRevision, amountCents: input.amountCents, bankDestinationId: input.bankDestinationId, reason: input.reason, confirmed: input.confirmed }
    return runFinanceCommand(path, snapshot, key => request<components['schemas']['SellerBankPayoutSubmission']>(path, {
      method: 'POST', headers: { 'Idempotency-Key': key }, body: JSON.stringify(snapshot),
    }))
  },
  listSellerPayoutRequests: (query: { cursor?: string; limit?: number } = {}) => request<components['schemas']['SellerPayoutPage']>(`/seller/payout-requests?${queryParameters(query)}`, { cache: 'no-store' }),
  getSellerPayoutRequest: (id: string) => request<SellerPayoutItem>(`/seller/payout-requests/${encodeURIComponent(id)}`, { cache: 'no-store' }),
  createSellerPayoutRequest: (input: operations['createSellerPayoutRequest']['requestBody']['content']['application/json']) => {
    const snapshot = { settlementId: input.settlementId, amountCents: input.amountCents }
    const path = '/seller/payout-requests'
    return runFinanceCommand(path, snapshot, key => request<components['schemas']['SellerPayoutRequest']>(path, {
      method: 'POST', headers: { 'Idempotency-Key': key }, body: JSON.stringify(snapshot),
    }))
  },
  listSellerPayoutBanks: (id: string) => request<components['schemas']['SellerPayoutBankDirectory']>(`/seller/payout-requests/${encodeURIComponent(id)}/banks`, { cache: 'no-store' }),
  bindSellerPayoutBank: (id: string, bankDestinationId: string) => taskCommand<components['schemas']['SellerPayoutBankTarget']>(`/seller/payout-requests/${encodeURIComponent(id)}/bank-destination`, 'PUT', { bankDestinationId }),
  cancelSellerPayoutRequest: (id: string) => taskCommand<components['schemas']['SellerPayoutRequest']>(`/seller/payout-requests/${encodeURIComponent(id)}`, 'DELETE'),
  mutateSellerProduct: (id: string | null, action: string, input: ListingMutation, review = false) => {
    const path = `/${review ? 'admin' : 'seller'}/products${id ? `/${encodeURIComponent(id)}` : ''}${!['create', 'edit'].includes(action) ? `/${encodeURIComponent(action)}` : ''}`
    return taskCommand<SellerProduct>(path, action === 'edit' ? 'PUT' : 'POST', input)
  },
  getProduct: (id: string) => request<Product>(`/products/${encodeURIComponent(id)}`),
  setProductPreview: (id: string, previewAssetId: string | null, offerVersion: string) => request<{ previewAssetId: string | null; offerVersion: string }>(`/products/${encodeURIComponent(id)}/preview`, { method: 'PUT', body: JSON.stringify({ previewAssetId, offerVersion }) }),
  adminAssignCategory: (scope: string, id: string, category: string) => request<{ updated: boolean }>(`/admin/content-category/${encodeURIComponent(id)}?scope=${encodeURIComponent(scope)}`, { method: 'PATCH', body: JSON.stringify({ category }) }),
  adminCategoryContent: (scope: string, q = '', cursor = '') => request<{ items: { id: string; title: string; category: string }[]; nextCursor: string }>(`/admin/content-category?${new URLSearchParams({ scope, q, cursor })}`),
  checkoutProduct: (id: string, licenseAccepted: boolean, offerVersion: string) => marketplaceCommand<PaymentCheckout>(`/products/${encodeURIComponent(id)}/checkout`, { licenseAccepted, offerVersion }),
  listOrders: (query: OrderQuery = {}) => {
    const params = new URLSearchParams()
    if (query.cursor) params.set('cursor', query.cursor)
    if (query.limit) params.set('limit', String(query.limit))
    return request<OrderPage>(`/orders${params.size ? `?${params}` : ''}`)
  },
  getOrder: (id: string, options?: Pick<RequestInit, 'signal'>) => request<Order>(`/orders/${encodeURIComponent(id)}`, { ...options, cache: 'no-store' }),
  closeProductCheckout: (id: string, expectedVersion: number) => marketplaceCommand<Order>(`/orders/${encodeURIComponent(id)}/close-checkout`, { expectedVersion, confirmed: true }),
  refundOrder: (id: string, reason: string) => marketplaceCommand<Order>(`/orders/${encodeURIComponent(id)}/refund`, { reason }),
  listTasks: (query: { q?: string; type?: string; status?: string; sort?: string; mine?: boolean; cursor?: string; limit?: number } = {}) => {
    const params = new URLSearchParams()
    Object.entries(query).forEach(([key, value]) => {
      if (value !== undefined && value !== '' && value !== false) params.set(key, String(value))
    })
    return request<components['schemas']['TaskPage']>(`/tasks${params.size ? `?${params}` : ''}`)
  },
  listTaskTypes: (scope = 'task') => request<{ items: TaskType[] }>(`/task-types?scope=${encodeURIComponent(scope)}`),
  changeTaskDeadline: (id: string, input: components['schemas']['TaskDeadlineInput']) => taskCommand<TaskDetail>(`/tasks/${encodeURIComponent(id)}/deadline`, 'POST', input),
  getTask: (id: string) => request<TaskDetail>(`/tasks/${encodeURIComponent(id)}`),
  checkoutTask: (id: string, input: TaskCheckoutRequest, idempotencyKey: string) => request<TaskPaymentCheckout>(`/tasks/${encodeURIComponent(id)}/checkout`, {
    method: 'POST', headers: { 'Idempotency-Key': idempotencyKey }, body: JSON.stringify(input),
  }),
  createTask: (input: TaskCreate) => taskCommand<TaskDetail>('/tasks', 'POST', input),
  proposeTask: (id: string, input: TaskProposalCreate) => taskCommand<TaskDetail>(`/tasks/${encodeURIComponent(id)}/proposals`, 'POST', input),
  claimTask: (id: string) => taskCommand<TaskDetail>(`/tasks/${encodeURIComponent(id)}/claim`, 'POST'),
  acceptTaskProposal: (id: string, proposalId: string) => taskCommand<TaskDetail>(`/tasks/${encodeURIComponent(id)}/proposals/${encodeURIComponent(proposalId)}/accept`, 'POST'),
  deliverTask: (id: string, input: TaskDeliveryCreate) => taskCommand<TaskDetail>(`/tasks/${encodeURIComponent(id)}/deliveries`, 'POST', input),
  reviewTask: (id: string, input: TaskReview) => taskCommand<TaskDetail>(`/tasks/${encodeURIComponent(id)}/review`, 'POST', input),
  disputeTask: (id: string, reason: string) => taskCommand<TaskDetail>(`/tasks/${encodeURIComponent(id)}/disputes`, 'POST', { reason }),
  cancelTask: (id: string, reason: string) => taskCommand<TaskDetail>(`/tasks/${encodeURIComponent(id)}/cancel`, 'POST', { reason }),
  adminOverview: () => request<AdminOverview>('/admin/overview'),
  adminListUsers: (query: AdminUserQuery = {}) => {
    const params = queryParameters(query)
    return request<{ items: AdminUser[]; nextCursor?: string }>(`/admin/users${params.size ? `?${params}` : ''}`)
  },
  adminUpdateUser: (id: string, input: AdminUserUpdate) => request<AdminUser>(`/admin/users/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(input) }),
  adminListContent: (query: AdminContentQuery = {}) => {
    const params = queryParameters(query)
    return request<{ items: AdminContent[]; nextCursor?: string }>(`/admin/content${params.size ? `?${params}` : ''}`)
  },
  adminUpdateContent: (id: string, input: AdminContentUpdate) => request<AdminContent>(`/admin/content/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(input) }),
  adminListMedia: (query: AdminMediaQuery = {}) => {
    const params = queryParameters(query)
    return request<{ items: AdminMediaItem[]; nextCursor?: string }>(`/admin/media${params.size ? `?${params}` : ''}`)
  },
  adminReviewMedia: (id: string, input: AdminMediaReview) => request<AdminMediaItem>(`/admin/media/${encodeURIComponent(id)}/review`, { method: 'POST', body: JSON.stringify(input) }),
  adminListGenerations: (query: AdminGenerationQuery = {}) => {
    const params = queryParameters(query)
    return request<{ items: AdminGeneration[]; nextCursor?: string }>(`/admin/generations${params.size ? `?${params}` : ''}`)
  },
  adminCancelGeneration: (id: string) => request<AdminGeneration>(`/admin/generations/${encodeURIComponent(id)}/cancel`, { method: 'POST' }),
  adminListTasks: (query: AdminTaskQuery = {}) => {
    const params = queryParameters(query)
    return request<{ items: AdminTaskOperation[]; nextCursor?: string }>(`/admin/tasks${params.size ? `?${params}` : ''}`)
  },
  adminResolveTaskDispute: (id: string, input: AdminTaskDisputeResolution) => request<AdminTaskOperation>(`/admin/tasks/${encodeURIComponent(id)}/resolve`, { method: 'POST', body: JSON.stringify(input) }),
  adminWebhookQuarantines: (query: ProductWebhookQuarantineFilter = {}) => {
    const params = queryParameters(query)
    return request<components['schemas']['ProductWebhookQuarantinePage']>(`/admin/payments/webhook-quarantines${params.size ? `?${params}` : ''}`, { cache: 'no-store' })
  },
  adminRecheckWebhook: (id: string, input: components['schemas']['ProductWebhookRecheck']) => request<ProductWebhookQuarantine>(`/admin/payments/webhook-quarantines/${encodeURIComponent(id)}/recheck`, { method: 'POST', body: JSON.stringify(input) }),
  adminListPayments: (query: AdminPaymentQuery = {}) => {
    const params = queryParameters(query)
    return request<AdminPaymentOperationPage>(`/admin/payments${params.size ? `?${params}` : ''}`)
  },
  adminRefundHistory: (id: string, cursor = '', signal?: globalThis.AbortSignal) => request<AdminRefundHistory>(`/admin/payments/${encodeURIComponent(id)}/refund-history${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`, { signal, cache: 'no-store' }),
  adminRefundChecks: (id: string, review: 'all' | 'unresolved', cursor = '', signal?: globalThis.AbortSignal) => request<AdminRefundCheckPage>(`/admin/payments/${encodeURIComponent(id)}/refund-checks?${new URLSearchParams({ review, ...(cursor ? { cursor } : {}) })}`, { signal, cache: 'no-store' }),
  adminRefundReadReceipts: (id: string, checkId: string, cursor = '', signal?: globalThis.AbortSignal) => request<AdminRefundReadReceiptPage>(`/admin/payments/${encodeURIComponent(id)}/refund-checks/${encodeURIComponent(checkId)}/receipts${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`, { signal, cache: 'no-store' }),
  adminRefundCheck: (id: string, checkId: string, signal?: globalThis.AbortSignal) => request<AdminRefundCheckDetail>(`/admin/payments/${encodeURIComponent(id)}/refund-checks/${encodeURIComponent(checkId)}`, { signal, cache: 'no-store' }),
  adminRequestRefundCheck: (id: string, expectedVersion: number) => request<AdminRefundHistory>(`/admin/payments/${encodeURIComponent(id)}/refund-checks`, { method: 'POST', body: JSON.stringify({ expectedVersion }) }),
  adminRecoverPayment: (id: string, input: AdminPaymentRecovery) => request<AdminPaymentOperation>(`/admin/payments/${encodeURIComponent(id)}/recover`, { method: 'POST', body: JSON.stringify(input) }),
  adminReplayPaymentEvent: (id: string, input: AdminPaymentEventReplay) => request<AdminPaymentOperation>(`/admin/payments/events/${encodeURIComponent(id)}/replay`, { method: 'POST', body: JSON.stringify(input) }),
  adminListPaymentDestinations: (query: AdminPaymentDestinationQuery = {}) => {
    const params = queryParameters(query)
    return request<{ items: AdminPaymentDestination[]; nextCursor?: string }>(`/admin/payment-destinations${params.size ? `?${params}` : ''}`)
  },
  adminUpdatePaymentDestination: (userId: string, input: AdminPaymentDestinationUpdate) => request<AdminPaymentDestination>(`/admin/payment-destinations/${encodeURIComponent(userId)}`, { method: 'PUT', body: JSON.stringify(input) }),
  adminListPaymentProviderConfigs: () => request<{ items: AdminPaymentProviderConfig[] }>('/admin/payment-providers'),
  adminUpdatePaymentProviderConfig: (provider: string, input: AdminPaymentProviderConfigUpdate) => request<AdminPaymentProviderConfig>(`/admin/payment-providers/${encodeURIComponent(provider)}`, { method: 'PUT', body: JSON.stringify(input) }),
  adminListProviders: () => request<{ items: AdminProvider[] }>('/admin/providers'),
  adminListTaskTypes: (scope = 'task') => request<{ items: TaskType[] }>(`/task-types?scope=${scope}`),
  adminCreateTaskType: (input: Omit<TaskType, 'version'>) => request<TaskType>(`/admin/task-types?scope=${input.scope || 'task'}`,  { method: 'POST', body: JSON.stringify(input) }),
  adminUpdateTaskType: (code: string, input: Omit<TaskType, 'code' | 'version'>) => request<TaskType>(`/admin/task-types/${encodeURIComponent(code)}?scope=${input.scope || 'task'}`, { method: 'PATCH', body: JSON.stringify(input) }),
  adminDeleteTaskType: (code: string, replacement = '', scope = 'task') => request<{ deleted: boolean }>(`/admin/task-types/${encodeURIComponent(code)}?scope=${scope}`, { method: 'DELETE', body: JSON.stringify({ replacement }) }),
  adminUpdateProvider: (id: string, input: AdminProviderUpdate) => request<AdminProvider>(`/admin/providers/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(input) }),
  adminListProviderConfigs: () => request<{ items: AdminProviderConfig[] }>('/admin/provider-configs'),
  adminCreateProviderConfig: (input: AdminProviderConfigCreate) => request<AdminProviderConfig>('/admin/provider-configs', { method: 'POST', body: JSON.stringify(input) }),
  adminUpdateProviderConfig: (id: string, input: AdminProviderConfigUpdate) => request<AdminProviderConfig>(`/admin/provider-configs/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(input) }),
  adminArchiveProviderConfig: (id: string) => request<{ archived: boolean }>(`/admin/provider-configs/${encodeURIComponent(id)}/archive`, { method: 'POST' }),
  adminSyncProviderModels: (id: string, input: { mode: 'chat' | 'image' | 'video' | 'music' }) => request<AdminProviderConfig>(`/admin/provider-configs/${encodeURIComponent(id)}/sync-models`, { method: 'POST', body: JSON.stringify(input) }),
  adminCreateProviderModel: (providerId: string, input: AdminProviderModelCreate) => request<AdminProviderModel>(`/admin/provider-configs/${encodeURIComponent(providerId)}/models`, { method: 'POST', body: JSON.stringify(input) }),
  adminUpdateProviderModel: (id: string, input: AdminProviderModelUpdate) => request<AdminProviderModel>(`/admin/provider-models/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(input) }),
  adminArchiveProviderModel: (modelId: string) => request<{ archived: boolean }>(`/admin/provider-models/${encodeURIComponent(modelId)}/archive`, { method: 'POST' }),
  adminListSubscriptionPlans: () => request<{ items: SubscriptionPlan[] }>('/admin/subscription-plans'),
  adminListSubscriptionModels: () => request<{ items: SubscriptionModel[] }>('/admin/subscription-models'),
  walletTopupSettings: () => request<WalletTopupSettings>('/billing/topup-settings'),
  adminWalletTopupSettings: () => request<WalletTopupSettings>('/admin/wallet-topup-settings'),
  adminUpdateWalletTopupSettings: (input: WalletTopupSettingsUpdate) => request<WalletTopupSettings>('/admin/wallet-topup-settings', { method: 'PUT', body: JSON.stringify(input) }),
  adminCreateSubscriptionPlan: (input: SubscriptionPlanInput) => request<SubscriptionPlan>('/admin/subscription-plans', { method: 'POST', body: JSON.stringify(input) }),
  adminUpdateSubscriptionPlan: (id: string, input: SubscriptionPlanUpdate) => request<SubscriptionPlan>(`/admin/subscription-plans/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(input) }),
  adminGetModelRoutes: (query: AdminModelRouteQuery = {}) => {
    const params = new URLSearchParams()
    if (query.mode) params.set('mode', query.mode)
    if (query.cursor) params.set('cursor', query.cursor)
    if (query.limit) params.set('limit', String(query.limit))
    return request<AdminModelRoutePolicy>(`/admin/models/routes${params.size ? `?${params}` : ''}`)
  },
  adminUpdateModelRoute: (mode: 'chat' | 'image' | 'video' | 'music', input: AdminModelRouteUpdate) => request<AdminModelRoutePolicy>(`/admin/models/routes/${mode}`, { method: 'POST', body: JSON.stringify(input) }),
  adminGetSystemSettings: () => request<AdminSystemSettings>('/admin/settings'),
  adminUpdateSystemSettings: (input: AdminSystemSettingUpdate) => request<AdminSystemSettings>('/admin/settings', { method: 'PUT', body: JSON.stringify(input) }),
  adminUpdateSiteConfiguration: (input: SiteConfiguration) => request<SiteConfiguration>('/admin/site-config', { method: 'PUT', body: JSON.stringify(input) }),
  adminListFinance: (query: AdminFinanceQuery = {}) => {
    const params = queryParameters(query)
    return request<{ items: AdminFinanceAccount[]; nextCursor?: string }>(`/admin/finance/accounts${params.size ? `?${params}` : ''}`)
  },
  adminAdjustFinance: financeAdjustmentCommand,
  adminListProviderCostReconciliations: (query: AdminProviderCostReconciliationQuery = {}) => {
    const params = queryParameters(query)
    return request<AdminProviderCostReconciliationPage>(`/admin/provider-cost-reconciliations${params.size ? `?${params}` : ''}`)
  },
  adminRequestProviderCostReconciliation: (input: AdminProviderCostReconciliationRequest) => request<AdminProviderCostReconciliation>('/admin/provider-cost-reconciliations', { method: 'POST', body: JSON.stringify(input) }),
  adminListRiskSignals: (query: AdminRiskSignalQuery = {}) => {
    const params = queryParameters(query)
    return request<{ items: AdminRiskSignal[]; nextCursor?: string }>(`/admin/risk/signals${params.size ? `?${params}` : ''}`)
  },
  adminReviewRiskSignal: (id: string, input: AdminRiskReview) => request<AdminRiskSignal>(`/admin/risk/signals/${encodeURIComponent(id)}/review`, { method: 'POST', body: JSON.stringify(input) }),
  adminGetRiskRules: (query: AdminRiskRuleHistoryQuery = {}) => {
    const params = queryParameters(query)
    return request<AdminRiskRulePolicy>(`/admin/risk/rules${params.size ? `?${params}` : ''}`)
  },
  adminUpdateRiskRules: (input: AdminRiskRuleUpdate) => request<AdminRiskRulePolicy>('/admin/risk/rules', { method: 'POST', body: JSON.stringify(input) }),
  adminGetRankingPolicy: (query: AdminRankingHistoryQuery = {}) => {
    const params = queryParameters(query)
    return request<AdminRankingPolicy>(`/admin/discovery/ranking${params.size ? `?${params}` : ''}`)
  },
  adminUpdateRankingPolicy: (input: AdminRankingUpdate) => request<AdminRankingPolicy>('/admin/discovery/ranking', { method: 'POST', body: JSON.stringify(input) }),
  adminCreateRankingCandidate: (input: AdminRankingUpdate) => request<AdminRankingPolicy>('/admin/discovery/ranking/candidates', { method: 'POST', body: JSON.stringify(input) }),
  adminRunRankingEvaluation: () => request<AdminRankingEvaluation>('/admin/discovery/ranking/evaluations', { method: 'POST' }),
  adminUpdateRankingRollout: (input: AdminRankingRolloutUpdate) => request<AdminRankingPolicy>('/admin/discovery/ranking/rollout', { method: 'POST', body: JSON.stringify(input) }),
  adminGetDiscoveryOperations: (query: AdminDiscoveryHistoryQuery = {}) => {
    const params = queryParameters(query)
    return request<AdminDiscoveryOperations>(`/admin/discovery/operations${params.size ? `?${params}` : ''}`)
  },
  adminAnalyzeDiscoveryIndex: () => request<AdminDiscoveryIndexRun>('/admin/discovery/index/analyze', { method: 'POST' }),
  adminGetOperationalDiagnostics: () => request<AdminOperationalDiagnostics>('/admin/observability'),
  adminGetDeveloperAccess: () => request<DeveloperAccess>('/admin/developer/access'),
  adminUpdateDeveloperControl: (input: DeveloperControlUpdate) => request<DeveloperAccess['control']>('/admin/developer/control', { method: 'PUT', body: JSON.stringify(input) }),
  adminRevokeDeveloperServiceAccount: (id: string, input: AdminVersionTransition) => request<DeveloperServiceAccount>(`/admin/developer/service-accounts/${encodeURIComponent(id)}/revoke`, { method: 'POST', body: JSON.stringify(input) }),
  adminRevokeDeveloperAPIKey: (id: string, input: AdminVersionTransition) => request<DeveloperAPIKey>(`/admin/developer/keys/${encodeURIComponent(id)}/revoke`, { method: 'POST', body: JSON.stringify(input) }),
	adminListWebhookDeadLetters: (query: AdminWebhookRecoveryQuery = {}) => {
		const params = queryParameters(query)
		return request<{ items: DeveloperWebhookDelivery[]; nextCursor?: string }>(`/admin/developer/webhooks/dead-letters${params.size ? `?${params}` : ''}`)
	},
	adminReplayWebhookDelivery: (id: string, input: AdminVersionTransition) => request<DeveloperWebhookDelivery>(`/admin/developer/webhooks/deliveries/${encodeURIComponent(id)}/replay`, { method: 'POST', body: JSON.stringify(input) }),
	adminListEmailActionDeadLetters: (query: AdminEmailRecoveryQuery = {}) => {
		const params = queryParameters(query)
		return request<{ items: IdentityEmailAction[]; nextCursor?: string }>(`/admin/email-actions/dead-letters${params.size ? `?${params}` : ''}`)
	},
  adminRetryEmailAction: (id: string, input: IdentityEmailTransition) => request<IdentityEmailAction>(`/admin/email-actions/${encodeURIComponent(id)}/retry`, { method: 'POST', body: JSON.stringify(input) }),
  adminCancelEmailAction: (id: string, input: IdentityEmailTransition) => request<IdentityEmailAction>(`/admin/email-actions/${encodeURIComponent(id)}/cancel`, { method: 'POST', body: JSON.stringify(input) }),
  adminListDeletionJobs: (query: DeletionJobQuery = {}) => {
    const params = queryParameters(query)
    return request<DeletionJobPage>(`/admin/data-rights/deletion-jobs${params.size ? `?${params}` : ''}`)
  },
  adminRetryDeletionJob: (id: string, input: MediaCleanupRetry) => request<DeletionJob>(`/admin/data-rights/deletion-jobs/${encodeURIComponent(id)}/retry`, { method: 'POST', body: JSON.stringify(input) }),
  adminListExportJobs: (query: ExportJobQuery = {}) => {
    const params = queryParameters(query)
    return request<ExportJobPage>(`/admin/data-rights/export-jobs${params.size ? `?${params}` : ''}`)
  },
  adminRetryExportJob: (id: string, input: MediaCleanupRetry) => request<ExportJob>(`/admin/data-rights/export-jobs/${encodeURIComponent(id)}/retry`, { method: 'POST', body: JSON.stringify(input) }),
  adminListMediaCleanups: (query: MediaCleanupQuery = {}) => {
    const params = queryParameters(query)
    return request<MediaCleanupPage>(`/admin/data-rights/media-cleanups${params.size ? `?${params}` : ''}`)
  },
  adminRetryMediaCleanup: (id: string, input: MediaCleanupRetry) => request<MediaCleanup>(`/admin/data-rights/media-cleanups/${encodeURIComponent(id)}/retry`, { method: 'POST', body: JSON.stringify(input) }),
  adminDeliveryEvidenceGaps: (filter: ProductDeliveryEvidenceFilter = {}) => {
    const query = new URLSearchParams()
    for (const [key, value] of Object.entries(filter)) if (value !== undefined && value !== '') query.set(key, String(value))
    return request<components['schemas']['ProductDeliveryEvidencePage']>(`/admin/product-deliveries/evidence-gaps?${query}`)
  },
  adminInspectDelivery: (order: string) => request<ProductDeliveryStatus>(`/admin/product-deliveries/${encodeURIComponent(order)}`),
  adminRepairDelivery: (order: string, input: ProductDeliveryRepairInput, key: string) => request<ProductDeliveryStatus>(`/admin/product-deliveries/${encodeURIComponent(order)}/repair`, { method: 'POST', headers: { 'Idempotency-Key': key }, body: JSON.stringify(input) }),
  adminPrepareDeliveryUpload: (order: string, input: components['schemas']['ProductDeliveryUploadInput'], key: string) => request<ProductDeliveryStatus>(`/admin/product-deliveries/${encodeURIComponent(order)}/repair-upload`, { method: 'POST', headers: { 'Idempotency-Key': key }, body: JSON.stringify(input) }),
  adminUploadDeliveryRepair: (order: string, repair: string, file: Blob, signal?: AbortSignal) => request<ProductDeliveryStatus>(`/admin/product-deliveries/${encodeURIComponent(order)}/repairs/${encodeURIComponent(repair)}/content?confirmed=true`, { method: 'PUT', headers: { 'Content-Type': 'application/octet-stream' }, body: file, signal }),
  adminResumeDeliveryRepair: (order: string, repair: string) => request<ProductDeliveryStatus>(`/admin/product-deliveries/${encodeURIComponent(order)}/repairs/${encodeURIComponent(repair)}/resume`, { method: 'POST', body: JSON.stringify({ confirmed: true }) }),
  adminListDataRights: (query: DataRightsQuery = {}) => {
    const params = queryParameters(query)
    return request<DataRightsRequestPage>(`/admin/data-rights${params.size ? `?${params}` : ''}`)
  },
  adminListDataRightsHolds: (query: DataRightsQuery = {}) => {
    const params = queryParameters(query)
    return request<DataRightsLegalHoldPage>(`/admin/data-rights/holds${params.size ? `?${params}` : ''}`)
  },
  adminCreateDataRightsHold: (input: DataRightsLegalHoldCreate) => request<DataRightsLegalHold>('/admin/data-rights/holds', { method: 'POST', body: JSON.stringify(input) }),
  adminReleaseDataRightsHold: (id: string) => request<DataRightsLegalHold>(`/admin/data-rights/holds/${encodeURIComponent(id)}/release`, { method: 'POST' }),
  adminListGovernanceReports: (query: AdminGovernanceReportQuery = {}) => {
    const params = queryParameters(query)
    return request<{ items: AdminGovernanceReport[]; nextCursor?: string }>(`/admin/governance/reports${params.size ? `?${params}` : ''}`)
  },
  adminResolveGovernanceReport: (id: string, input: AdminReportResolution) => request<AdminGovernanceReport>(`/admin/governance/reports/${encodeURIComponent(id)}/resolve`, { method: 'POST', body: JSON.stringify(input) }),
  adminListGovernanceAppeals: (query: AdminGovernanceAppealQuery = {}) => {
    const params = queryParameters(query)
    return request<{ items: AdminGovernanceAppeal[]; nextCursor?: string }>(`/admin/governance/appeals${params.size ? `?${params}` : ''}`)
  },
  adminResolveGovernanceAppeal: (id: string, input: AdminAppealResolution) => request<AdminGovernanceAppeal>(`/admin/governance/appeals/${encodeURIComponent(id)}/resolve`, { method: 'POST', body: JSON.stringify(input) }),
  adminListSupportCases: (query: AdminSupportQuery = {}) => {
    const params = queryParameters(query)
    return request<{ items: SupportCase[]; nextCursor?: string }>(`/admin/support/cases${params.size ? `?${params}` : ''}`)
  },
  adminGetSupportCase: (id: string) => request<SupportCase>(`/admin/support/cases/${encodeURIComponent(id)}`),
  adminReplySupportCase: (id: string, input: AdminSupportReply) => request<SupportCase>(`/admin/support/cases/${encodeURIComponent(id)}/messages`, { method: 'POST', body: JSON.stringify(input) }),
  adminUpdateSupportCase: (id: string, input: AdminSupportUpdate) => request<SupportCase>(`/admin/support/cases/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(input) }),
}

function taskCommand<T>(path: string, method: string, body?: unknown): Promise<T> {
  return runJSONCommand(path, method, body, (key, serialized) => request<T>(path, {
    method,
    headers: { 'Idempotency-Key': key },
    body: serialized,
  }), error => error instanceof APIError && [401, 403, 404, 422].includes(error.status))
}

function billingCheckout(path: string, body: { amountCents: number } | { planId: string }, explicitKey?: string): Promise<BillingCheckout> {
  const serialized = JSON.stringify(body)
  const send = (key: string) => request<BillingCheckout>(path, {
    method: 'POST', headers: { 'Idempotency-Key': key }, body: serialized,
  })
  // A denied retry cannot prove an earlier checkout did not reach the provider.
  // Preserve pending keys through reload and same-account reauthentication.
  return explicitKey === undefined ? runFinanceCommand(path, body, send) : send(explicitKey)
}

function marketplaceCommand<T>(path: string, body: unknown): Promise<T> {
  return runJSONCommand<T>(path, 'POST', body, (key, serialized) => request<T>(path, {
    method: 'POST', headers: { 'Idempotency-Key': key }, body: serialized,
  }), error => error instanceof APIError && ([401, 403, 404, 422].includes(error.status) || ['payment_checkout_expired', 'payment_checkout_closed'].includes(error.code)))
}

function generationCommand<T>(path: string, body?: unknown): Promise<T> {
  return request<T>(path, {
    method: 'POST',
    headers: { 'Idempotency-Key': crypto.randomUUID() },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
}

export function messageFrom(error: unknown): string {
  if (error instanceof CommandSessionChangedError) return i18n.global.t('errors.codes.command_session_changed')
  if (error instanceof APIError) {
    const codeKey = `errors.codes.${error.code}`
    const key = i18n.global.te(codeKey) ? codeKey : (error.retryable ? 'errors.retryable' : 'errors.generic')
    const message = i18n.global.t(key)
    return error.requestId ? `${message} ${i18n.global.t('errors.requestId', { id: error.requestId })}` : message
  }
  if (error instanceof TypeError) return i18n.global.t('errors.network')
  return error instanceof Error && error.message ? error.message : i18n.global.t('errors.generic')
}
