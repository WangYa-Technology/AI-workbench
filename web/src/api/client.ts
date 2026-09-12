import type { components, operations } from './schema'
import { i18n } from '../i18n'

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
export type TaskType = { code: string; nameZh: string; nameEn: string; icon: string; sortOrder: number }
export type Product = components['schemas']['Product']
export type PaymentCheckout = components['schemas']['PaymentCheckout']
export type Order = components['schemas']['Order']
export type OrderPage = components['schemas']['OrderPage']
export type OrderQuery = NonNullable<operations['listOrders']['parameters']['query']>
export type Meta = components['schemas']['Meta']
export type RegisterRequest = components['schemas']['RegisterRequest']
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

function queryParameters(query: Record<string, unknown>): URLSearchParams {
  const params = new URLSearchParams()
  Object.entries(query).forEach(([key, value]) => {
    if (value !== undefined && value !== '') params.set(key, String(value))
  })
  return params
}

export const api = {
  meta: () => request<Meta>('/meta'),
  siteConfiguration: () => request<SiteConfiguration>('/site-config'),
  creationCapabilities: () => request<CreationCapabilities>('/creation/capabilities'),
  session: () => request<Session>('/auth/session'),
  register: (input: RegisterRequest) => request<Session>('/auth/register', {
    method: 'POST', body: JSON.stringify(input),
  }),
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
  startDemoSession: (actor: 'creator' | 'publisher' | 'admin' = 'creator') => request<Session>('/auth/demo', {
    method: 'POST', body: JSON.stringify({ actor }),
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
  getWork: (id: string) => request<Work>(`/works/${encodeURIComponent(id)}`),
  search: (query: { q: string; types?: string[]; page?: number; limit?: number }) => {
    const params = new URLSearchParams({ q: query.q })
    if (query.types?.length) params.set('types', query.types.join(','))
    if (query.page) params.set('page', String(query.page))
    if (query.limit) params.set('limit', String(query.limit))
    return request<SearchPage>(`/search?${params}`)
  },
  getCreator: (handle: string) => request<CreatorProfile>(`/creators/${encodeURIComponent(handle)}`),
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
	pointOverview: () => request<PointOverview>('/billing/points'),
	checkoutWalletTopup: (amountCents: number, idempotencyKey = crypto.randomUUID()) => request<BillingCheckout>('/billing/topups/checkout', { method: 'POST', headers: { 'Idempotency-Key': idempotencyKey }, body: JSON.stringify({ amountCents }) }),
	checkoutSubscription: (planId: string, idempotencyKey = crypto.randomUUID()) => request<BillingCheckout>('/billing/subscriptions/checkout', { method: 'POST', headers: { 'Idempotency-Key': idempotencyKey }, body: JSON.stringify({ planId }) }),
	listAssets: (query: AssetListQuery = {}) => {
		const params = new URLSearchParams()
		if (query.cursor) params.set('cursor', query.cursor)
		if (query.limit) params.set('limit', String(query.limit))
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
	uploadAsset: (form: globalThis.FormData) => request<Asset>('/assets/uploads', { method: 'POST', body: form }),
	uploadAssetVersion: (id: string, form: globalThis.FormData) => request<Asset>(`/assets/${encodeURIComponent(id)}/versions`, { method: 'POST', body: form }),
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
    return request<CommunityPostPage>(`/community/posts${params.size ? `?${params}` : ''}`)
  },
  createCommunityPost: (input: CommunityPostCreate) => request<CommunityPost>('/community/posts', {
    method: 'POST', body: JSON.stringify(input),
  }),
  getCommunityPost: (postId: string) => request<CommunityPost>(`/community/posts/${encodeURIComponent(postId)}`),
  listCommunityComments: (postId: string, query: CommunityCommentQuery = {}) => {
    const params = new URLSearchParams()
    if (query.cursor) params.set('cursor', query.cursor)
    if (query.limit) params.set('limit', String(query.limit))
    return request<CommunityCommentPage>(`/community/posts/${encodeURIComponent(postId)}/comments${params.size ? `?${params}` : ''}`)
  },
  createCommunityComment: (postId: string, body: string) => request<CommunityComment>(`/community/posts/${encodeURIComponent(postId)}/comments`, {
    method: 'POST', body: JSON.stringify({ body }),
  }),
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
  listProducts: (query: { q?: string; type?: string; license?: string; sort?: string } = {}) => {
    const params = new URLSearchParams()
    Object.entries(query).forEach(([key, value]) => {
      if (value) params.set(key, value)
    })
    return request<{ items: Product[] }>(`/products${params.size ? `?${params}` : ''}`)
  },
  getProduct: (id: string) => request<Product>(`/products/${encodeURIComponent(id)}`),
  checkoutProduct: (id: string, licenseAccepted: boolean) => marketplaceCommand<PaymentCheckout>(`/products/${encodeURIComponent(id)}/checkout`, { licenseAccepted }),
  listOrders: (query: OrderQuery = {}) => {
    const params = new URLSearchParams()
    if (query.cursor) params.set('cursor', query.cursor)
    if (query.limit) params.set('limit', String(query.limit))
    return request<OrderPage>(`/orders${params.size ? `?${params}` : ''}`)
  },
  getOrder: (id: string) => request<Order>(`/orders/${encodeURIComponent(id)}`),
  refundOrder: (id: string, reason: string) => marketplaceCommand<Order>(`/orders/${encodeURIComponent(id)}/refund`, { reason }),
  listTasks: (query: { q?: string; type?: string; status?: string; sort?: string; mine?: boolean } = {}) => {
    const params = new URLSearchParams()
    Object.entries(query).forEach(([key, value]) => {
      if (value !== undefined && value !== '' && value !== false) params.set(key, String(value))
    })
    return request<{ items: TaskSummary[] }>(`/tasks${params.size ? `?${params}` : ''}`)
  },
  listTaskTypes: () => request<{ items: TaskType[] }>('/task-types'),
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
  adminListPayments: (query: AdminPaymentQuery = {}) => {
    const params = queryParameters(query)
    return request<AdminPaymentOperationPage>(`/admin/payments${params.size ? `?${params}` : ''}`)
  },
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
  adminListTaskTypes: () => request<{ items: TaskType[] }>('/task-types'),
  adminCreateTaskType: (input: Omit<TaskType, 'version'>) => request<TaskType>('/admin/task-types', { method: 'POST', body: JSON.stringify(input) }),
  adminUpdateTaskType: (code: string, input: Omit<TaskType, 'code' | 'version'>) => request<TaskType>(`/admin/task-types/${encodeURIComponent(code)}`, { method: 'PATCH', body: JSON.stringify(input) }),
  adminDeleteTaskType: (code: string, replacement = '') => request<{ deleted: boolean }>(`/admin/task-types/${encodeURIComponent(code)}`, { method: 'DELETE', body: JSON.stringify({ replacement }) }),
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
  adminAdjustFinance: (id: string, input: AdminFinanceAdjustment) => request<AdminFinanceAccount>(`/admin/finance/accounts/${encodeURIComponent(id)}/adjust`, { method: 'POST', body: JSON.stringify(input) }),
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
  return request<T>(path, {
    method,
    headers: { 'Idempotency-Key': crypto.randomUUID() },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
}

function marketplaceCommand<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, {
    method: 'POST', headers: { 'Idempotency-Key': crypto.randomUUID() }, body: JSON.stringify(body),
  })
}

function generationCommand<T>(path: string, body?: unknown): Promise<T> {
  return request<T>(path, {
    method: 'POST',
    headers: { 'Idempotency-Key': crypto.randomUUID() },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
}

export function messageFrom(error: unknown): string {
  if (error instanceof APIError) {
    const codeKey = `errors.codes.${error.code}`
    const key = i18n.global.te(codeKey) ? codeKey : (error.retryable ? 'errors.retryable' : 'errors.generic')
    const message = i18n.global.t(key)
    return error.requestId ? `${message} ${i18n.global.t('errors.requestId', { id: error.requestId })}` : message
  }
  if (error instanceof TypeError) return i18n.global.t('errors.network')
  return error instanceof Error && error.message ? error.message : i18n.global.t('errors.generic')
}
