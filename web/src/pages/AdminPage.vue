<script setup lang="ts">
import {
  Activity, Ban, BriefcaseBusiness, CircleDollarSign, Code2, CreditCard, Database, FileCheck2, FlaskConical, LoaderCircle, RefreshCw,
  Globe2, Headphones, KeyRound, ListFilter, MessageSquare, Pencil, Plus, Search, Save, Send, Settings2, ShieldAlert, ShieldCheck, SlidersHorizontal, Trash2, Undo2, WandSparkles, X,
} from 'lucide-vue-next'
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import {
  api, messageFrom, type AdminContent, type AdminFinanceAccount, type AdminOperationalDiagnostics, type AdminPaymentDestination, type AdminPaymentOperation, type AdminPaymentProviderConfig,
  type AdminGeneration, type AdminGovernanceAppeal, type AdminGovernanceReport, type AdminMediaItem, type AdminModelRoutePolicy, type AdminModelRouteUpdate, type AdminOverview, type AdminProvider, type AdminProviderConfig, type AdminProviderConfigCreate, type AdminProviderConfigUpdate, type AdminProviderModel, type ModelCapabilities, type AdminUser,
  type AdminDiscoveryOperations, type AdminProviderCostReconciliation, type AdminRankingPolicy, type AdminRankingUpdate, type AdminRiskRulePolicy, type AdminRiskRuleUpdate, type AdminRiskSignal, type AdminSystemSettings, type AdminSystemSettingUpdate, type AdminTaskOperation, type Asset, type DataRightsLegalHold, type DataRightsRequest, type DeveloperAccess, type DeveloperControlUpdate, type DeveloperWebhookDelivery, type IdentityEmailAction, type ModelPointPricing, type SiteConfiguration, type SubscriptionPlan, type SubscriptionPlanInput, type SupportCase,
} from '../api/client'
import { formatCurrency, formatDateTime } from '../lib/format'
import { adminNavigationItems, type AdminTab } from '../lib/admin-navigation'
import { cloneSiteConfiguration, sitePolicyKeys } from '../lib/siteConfiguration'
import { useSessionStore } from '../stores/session'
import { useSiteConfigStore } from '../stores/siteConfig'
import { useCursorDirectory } from '../composables/useCursorDirectory'
import UiButton from '../components/ui/UiButton.vue'
import UiCheckbox from '../components/ui/UiCheckbox.vue'
import UiDrawer from '../components/ui/UiDrawer.vue'
import UiFileInput from '../components/ui/UiFileInput.vue'
import UiIconButton from '../components/ui/UiIconButton.vue'
import UiInput from '../components/ui/UiInput.vue'
import MarkdownContent from '../components/ui/MarkdownContent.vue'
import MarkdownEditor from '../components/ui/MarkdownEditor.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiSwitch from '../components/ui/UiSwitch.vue'
import UiTable from '../components/ui/UiTable.vue'
import UiTabs from '../components/ui/UiTabs.vue'
import UiTextarea from '../components/ui/UiTextarea.vue'

type Tab = AdminTab
type CommandKind = 'user' | 'content' | 'media' | 'report' | 'appeal' | 'generation' | 'task' | 'provider' | 'finance' | 'payment' | 'paymentEvent' | 'paymentDestination' | 'risk' | 'dataRightsHold' | 'holdRelease'
type OverviewKey = 'users' | 'works' | 'generations' | 'orders' | 'tasks' | 'risks' | 'providers'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const siteConfig = useSiteConfigStore()
const loading = ref(true)
const actionLoading = ref(false)
const adminPageScroll = ref<globalThis.HTMLElement | null>(null)
const error = ref('')
const success = ref('')
const localDemoAvailable = ref(false)
const overview = ref<AdminOverview | null>(null)
const usersDirectory = useCursorDirectory<AdminUser>(cursor => api.adminListUsers(userListQuery(cursor)))
const users = usersDirectory.items
const userQuery = ref('')
const userRole = ref('')
const userStatus = ref('')
const userNextCursor = usersDirectory.nextCursor
const userLoadingMore = usersDirectory.loadingMore
const contentDirectory = useCursorDirectory<AdminContent>(cursor => api.adminListContent(contentListQuery(cursor)))
const content = contentDirectory.items
const contentQuery = ref('')
const contentType = ref('')
const contentStatus = ref('')
const contentNextCursor = contentDirectory.nextCursor
const contentLoadingMore = contentDirectory.loadingMore
const generations = ref<AdminGeneration[]>([])
const generationQuery = ref('')
const generationMode = ref('')
const generationStatus = ref('')
const generationNextCursor = ref<string | null>(null)
const generationLoadingMore = ref(false)
const taskOperations = ref<AdminTaskOperation[]>([])
const taskQuery = ref('')
const taskStatus = ref('')
const taskDisputeStatus = ref('')
const taskNextCursor = ref<string | null>(null)
const taskLoadingMore = ref(false)
const providers = ref<AdminProvider[]>([])
const providerConfigs = ref<AdminProviderConfig[]>([])
const initializedProviderSwitches = ref(new Set<string>())
const providerSyncModes = reactive<Record<string, 'chat' | 'image' | 'video' | 'music'>>({})
const providerConfigForm = reactive<AdminProviderConfigCreate & { id: string }>({ id: '', name: '', protocol: 'openai_chat_completions', endpoint: '', apiKey: '', adminEnabled: true })
type EditableProviderModel = { id: string; providerId: string; mode: 'chat' | 'image' | 'video' | 'music'; modelName: string; displayName: string; description: string; estimatedCostCents: number; pointPricing: ModelPointPricing; capabilities: ModelCapabilities; adminEnabled: boolean }
const defaultModelCapabilities = (mode: EditableProviderModel['mode']): ModelCapabilities => ({
  aspectRatios: ['image', 'video'].includes(mode) ? ['auto', '1:1', '4:5', '16:9'] : [],
  qualities: mode === 'chat' ? [] : ['auto', 'standard', 'high'],
  durationSeconds: (mode === 'video' ? [5, 10, 30] : mode === 'music' ? [5, 10, 30, 60] : []) as ModelCapabilities['durationSeconds'],
  outputFormats: mode === 'chat' ? ['txt'] : mode === 'image' ? ['jpeg', 'png'] : mode === 'video' ? ['mp4'] : ['wav'],
  resultFormats: mode === 'chat' ? ['txt'] : mode === 'image' ? ['jpeg', 'png'] : mode === 'video' ? ['mp4'] : ['wav'],
  referenceKinds: mode === 'chat' ? ['document'] : mode === 'music' ? ['audio'] : ['image'],
  supportsMask: mode === 'image',
})
const defaultModelPointPricing = (mode: EditableProviderModel['mode']): ModelPointPricing => ({
  mode, inputPointsPer1KTokens: mode === 'chat' ? 2 : 0, outputPointsPer1KTokens: mode === 'chat' ? 8 : 0,
  pointsPerSecond: ['video', 'music'].includes(mode) ? 2 : 0, minimumPoints: mode === 'image' ? 10 : 1, version: 0,
  imageResolutionPrices: mode === 'image' ? [{ resolution: '1024x1024', points: 10 }, { resolution: '1024x1536', points: 15 }, { resolution: '1536x1024', points: 15 }] : [],
})
const cloneModelPointPricing = (pricing: ModelPointPricing): ModelPointPricing => ({
  ...pricing,
  imageResolutionPrices: pricing.imageResolutionPrices.map(item => ({ ...item })),
})
const providerModelForm = reactive<EditableProviderModel>({ id: '', providerId: '', mode: 'chat', modelName: '', displayName: '', description: '', estimatedCostCents: 0, pointPricing: defaultModelPointPricing('chat'), capabilities: defaultModelCapabilities('chat'), adminEnabled: true })
const providerOutputFormats = computed(() => ({ chat: ['txt'], image: ['jpeg', 'png'], video: ['mp4'], music: ['wav'] } as const)[providerModelForm.mode])
const providerDurationOptions = computed(() => (providerModelForm.mode === 'music' ? [5, 10, 30, 60] : [5, 10, 30]) as ModelCapabilities['durationSeconds'])
const providerConfigEditorOpen = ref(false)
const providerModelEditorOpen = ref(false)
const modelRoutePolicy = ref<AdminModelRoutePolicy | null>(null)
const modelRouteLoadingMore = ref<Record<string, boolean>>({})
const systemSettings = ref<AdminSystemSettings | null>(null)
const siteConfigurationSection = ref<'general' | 'policies'>('general')
const selectedPolicy = ref<keyof SiteConfiguration['policies']>('terms')
const siteIconMode = ref<'url' | 'upload'>('url')
const siteIconUploading = ref(false)
const modelRouteMode = ref<'chat' | 'image' | 'video' | 'music'>('image')
const finance = ref<AdminFinanceAccount[]>([])
const subscriptionPlans = ref<SubscriptionPlan[]>([])
const subscriptionPlanEditorOpen = ref(false)
const subscriptionPlanForm = reactive<SubscriptionPlanInput & { id: string }>({ id: '', tierCode: '', name: '', description: '', priceCents: 0, currency: 'USD', includedPoints: 10000, billingPeriodDays: 30, sortOrder: 0, active: true, modelIds: [] })
const financeQuery = ref('')
const financeState = ref('')
const financeNextCursor = ref<string | null>(null)
const financeLoadingMore = ref(false)
const paymentOperations = ref<AdminPaymentOperation[]>([])
const paymentQuery = ref('')
const paymentPurpose = ref('')
const paymentStatus = ref('')
const paymentMode = ref('')
const paymentAttention = ref('needs_attention')
const paymentNextCursor = ref<string | null>(null)
const paymentLoadingMore = ref(false)
const paymentDestinations = ref<AdminPaymentDestination[]>([])
const paymentProviderConfigs = ref<AdminPaymentProviderConfig[]>([])
const paymentProviderEditorOpen = ref(false)
const paymentProviderForm = reactive<AdminPaymentProviderConfig>({ id: '', provider: 'waffo_pancake', enabled: false, environment: 'test', merchantId: '', storeId: '', productIdOnetime: '', productIdSubscription: '', secretConfigured: false, connectorConfigured: false, createdAt: '', updatedAt: '' })
type PaymentGatewayTab = 'general' | 'epay' | 'stripe' | 'creem' | 'waffo_pancake' | 'waffo'
type PaymentGatewayMethod = { id: string; name: string; handle: string; icon: string; minimum: string; enabled: boolean; provider: string }
const paymentGatewayTab = ref<PaymentGatewayTab>('general')
const paymentMethodQuery = ref('')
const paymentMethodJsonOpen = ref(false)
const paymentMethodJson = ref('[\n  {\n    "type": "waffo_pancake",\n    "name": "Waffo Pancake",\n    "icon": "LuCreditCard"\n  }\n]')
const paymentMethodOverrides = ref<PaymentGatewayMethod[] | null>(null)
const paymentGatewayGeneral = reactive({ unitPrice: 1, minimumTopup: 1 })
const topupAmounts = ref<number[]>([10, 20, 50, 100, 200])
const newTopupAmount = ref<number | undefined>(undefined)
const discountTiers = ref<Array<{ amount: number; rate: number }>>([{ amount: 100, rate: 0.95 }])
const paymentDestinationNextCursor = ref<string | null>(null)
const providerCostReconciliations = ref<AdminProviderCostReconciliation[]>([])
const providerCostReconciliationAvailable = ref(false)
const providerCostReconciliationLoading = ref(false)
const providerCostReconciliationForm = reactive({ periodStart: '', periodEnd: '' })
const riskSignals = ref<AdminRiskSignal[]>([])
const riskQuery = ref('')
const riskStatus = ref('')
const riskSeverity = ref('')
const riskNextCursor = ref<string | null>(null)
const riskLoadingMore = ref(false)
const riskRulePolicy = ref<AdminRiskRulePolicy | null>(null)
const riskRuleNextCursor = ref<string | null>(null)
const riskRuleLoadingMore = ref(false)
const rankingPolicy = ref<AdminRankingPolicy | null>(null)
const rankingNextCursor = ref<string | null>(null)
const rankingLoadingMore = ref(false)
const discoveryOperations = ref<AdminDiscoveryOperations>({ indexRuns: [], evaluations: [] })
const indexRunNextCursor = ref<string | null>(null)
const indexRunLoadingMore = ref(false)
const evaluationNextCursor = ref<string | null>(null)
const evaluationLoadingMore = ref(false)
const rankingActivationMode = ref<'candidate' | 'immediate'>('candidate')
const rolloutForm = reactive({ percent: 25 })
const operationalDiagnostics = ref<AdminOperationalDiagnostics | null>(null)
const developerAdminAccess = ref<DeveloperAccess | null>(null)
const webhookDeadLetters = ref<DeveloperWebhookDelivery[]>([])
const webhookQuery = ref('')
const webhookEventType = ref('')
const webhookNextCursor = ref<string | null>(null)
const webhookLoadingMore = ref(false)
const emailActionDeadLetters = ref<IdentityEmailAction[]>([])
const emailQuery = ref('')
const emailKind = ref('')
const emailNextCursor = ref<string | null>(null)
const emailLoadingMore = ref(false)
const governanceReports = ref<AdminGovernanceReport[]>([])
const governanceAppeals = ref<AdminGovernanceAppeal[]>([])
const reportQuery = ref('')
const reportType = ref('')
const reportCategory = ref('')
const reportStatus = ref('')
const reportNextCursor = ref<string | null>(null)
const reportLoadingMore = ref(false)
const appealQuery = ref('')
const appealType = ref('')
const appealStatus = ref('')
const appealNextCursor = ref<string | null>(null)
const appealLoadingMore = ref(false)
const mediaDirectory = useCursorDirectory<AdminMediaItem>(cursor => api.adminListMedia(mediaListQuery(cursor)))
const mediaItems = mediaDirectory.items
const mediaQuery = ref('')
const mediaKind = ref('')
const mediaStatus = ref('')
const mediaNextCursor = mediaDirectory.nextCursor
const mediaLoadingMore = mediaDirectory.loadingMore
const dataRightsItems = ref<DataRightsRequest[]>([])
const legalHolds = ref<DataRightsLegalHold[]>([])
const dataRightsNextCursor = ref<string | null>(null)
const legalHoldsNextCursor = ref<string | null>(null)
const dataRightsLoadingMore = ref(false)
const legalHoldsLoadingMore = ref(false)
const supportCases = ref<SupportCase[]>([])
const supportQuery = ref('')
const supportStatus = ref('')
const supportCategory = ref('')
const supportNextCursor = ref<string | null>(null)
const supportLoadingMore = ref(false)
const selectedSupport = ref<SupportCase | null>(null)
const supportReply = reactive({ body: '' })
const supportDecision = reactive({ status: 'in_review', resolutionCode: '' })
const commandDrawerOpen = ref(false)
const command = reactive({ kind: '' as CommandKind | '', id: '', title: '', role: '', status: '', outcome: '', decision: '', action: '', destinationID: '', enabled: false, displayName: '', modelName: '', description: '', estimatedCostCents: 0, deltaCents: 0, authorityReference: '' })
const commandPanel = ref<InstanceType<typeof globalThis.HTMLFormElement> | null>(null)
const rankingForm = reactive<AdminRankingUpdate>({
  name: '', titleExactWeight: 100, titlePrefixWeight: 80, titleContainsWeight: 60,
  creatorExactWeight: 50, creatorMatchWeight: 35, bodyMatchWeight: 25, secondaryMatchWeight: 12,
  recencyWeight: 10, creatorActivityWeight: 15, workTypeBoost: 0, creatorTypeBoost: 0,
  productTypeBoost: 0, demandTypeBoost: 0, expectedVersion: 1,
})
const riskRuleForm = reactive<AdminRiskRuleUpdate>({
  name: '', taskDisputeScore: 85, transactionRefundScore: 55,
  communityReportScore: 35, mediaRejectionScore: 75,
  accountLinkScore: 65, accountLinkMinAccounts: 3, accountLinkWindowHours: 24,
  mediumThreshold: 40, highThreshold: 70, criticalThreshold: 90,
  expectedVersion: 1,
})
const modelRouteForm = reactive<AdminModelRouteUpdate>({ providerProfileId: '', name: '', timeoutSeconds: 120, maxAttempts: 3, expectedVersion: 1 })
const systemSettingForm = reactive<AdminSystemSettingUpdate>({ registrationsEnabled: true, generationsEnabled: true, publishingEnabled: true, marketplaceCheckoutEnabled: true, taskCreationEnabled: true, publicNotice: '' })
const siteConfigurationForm = reactive<SiteConfiguration>(cloneSiteConfiguration(siteConfig.current))
const developerControlForm = reactive<DeveloperControlUpdate>({ enabled: false, maxServiceAccounts: 5, maxActiveKeys: 3, defaultTtlDays: 90, expectedVersion: 1 })

const availableAdminNavigation = computed(() => adminNavigationItems.filter(item => session.user?.permissions.includes(item.permission)))
const tabs = computed(() => availableAdminNavigation.value.map(item => item.tab))
const legacyProviderProfiles = computed(() => providers.value.filter((profile) => !providerConfigs.value.some((config) => config.models.some((model) => model.id === profile.id))))
const availablePlanModels = computed(() => providerConfigs.value.flatMap(provider => provider.models.map(model => ({ ...model, providerName: provider.name }))))
const editingProviderConfig = computed(() => providerConfigs.value.find(item => item.id === providerConfigForm.id) || null)
const activeTab = computed<Tab>(() => {
  const requested = String(route.query.tab || 'overview') as Tab
  return tabs.value.includes(requested) ? requested : tabs.value[0] || 'overview'
})
const paymentGatewayTabs = computed(() => [
  { value: 'general', label: t('admin.paymentGatewayTabs.general'), icon: Settings2 },
  { value: 'epay', label: t('admin.paymentGatewayTabs.epay'), icon: CreditCard },
  { value: 'stripe', label: t('admin.paymentGatewayTabs.stripe'), icon: CreditCard },
  { value: 'creem', label: t('admin.paymentGatewayTabs.creem'), icon: CreditCard },
  { value: 'waffo_pancake', label: t('admin.paymentGatewayTabs.waffoPancake'), icon: CreditCard },
  { value: 'waffo', label: t('admin.paymentGatewayTabs.waffo'), icon: CreditCard },
] as Array<{ value: PaymentGatewayTab; label: string; icon: typeof Settings2 }>)
const selectedPaymentProviderConfig = computed(() => paymentProviderConfigs.value.find(item => item.provider === paymentGatewayTab.value) || null)
const paymentGatewayMethods = computed<PaymentGatewayMethod[]>(() => (paymentMethodOverrides.value || paymentProviderConfigs.value.map(item => ({
  id: item.id || item.provider,
  name: item.provider === 'waffo_pancake' ? t('admin.paymentGatewayTabs.waffoPancake') : item.provider.toUpperCase(),
  handle: item.provider,
  icon: 'LuCreditCard',
  minimum: item.provider === 'waffo_pancake' ? (item.productIdOnetime || '—') : '—',
  enabled: item.enabled,
  provider: item.provider,
}))).filter(item => paymentGatewayTab.value === 'general' || item.provider === paymentGatewayTab.value).filter(item => !paymentMethodQuery.value.trim() || `${item.name} ${item.handle}`.toLowerCase().includes(paymentMethodQuery.value.trim().toLowerCase())))
const hasAdminAccess = computed(() => Boolean(session.user?.permissions.includes('admin:access')))
const siteConfigurationSections = computed(() => [
  { value: 'general', label: t('admin.siteGeneral'), icon: Globe2 },
  { value: 'policies', label: t('admin.sitePolicies'), icon: FileCheck2 },
])
const iconSourceItems = computed(() => [
  { value: 'url', label: t('admin.siteIconUrlMode') },
  { value: 'upload', label: t('admin.siteIconUploadMode') },
])
const supportStatusOptions = computed(() => {
  const transitions: Record<string, string[]> = {
    open: ['in_review', 'waiting_for_requester', 'resolved', 'closed'],
    in_review: ['waiting_for_requester', 'resolved', 'closed'],
    waiting_for_requester: ['in_review', 'resolved', 'closed'],
    resolved: ['in_review', 'closed'], closed: [],
  }
  return transitions[selectedSupport.value?.status || ''] || []
})

function date(value?: string) {
  return value ? formatDateTime(value, locale.value, session.user?.timezone || 'UTC') : t('admin.never')
}

function shortUserId(value: string) {
  return value.slice(0, 8)
}

const roleKeys: Record<string, string> = {
  member: 'admin.roles.member', creator: 'admin.roles.creator', publisher: 'admin.roles.publisher', moderator: 'admin.roles.moderator', admin: 'admin.roles.admin',
}
const resourceTypeKeys: Record<string, string> = {
  work: 'support.resourceTypes.work', product: 'support.resourceTypes.product', post: 'support.resourceTypes.post', asset: 'support.resourceTypes.asset',
  generation: 'support.resourceTypes.generation', order: 'support.resourceTypes.order', task: 'support.resourceTypes.task',
}
const mediaKindKeys: Record<string, string> = {
  image: 'admin.mediaKinds.image', video: 'admin.mediaKinds.video', audio: 'admin.mediaKinds.audio', document: 'admin.mediaKinds.document', prompt: 'admin.mediaKinds.prompt', workflow: 'admin.mediaKinds.workflow',
}
const overviewStatusKeys: Record<OverviewKey, Record<string, string>> = {
  users: { active: 'admin.states.active', suspended: 'admin.states.suspended', deleted: 'admin.states.deleted' },
  works: { draft: 'admin.states.draft', published: 'admin.states.published', hidden: 'admin.states.hidden', removed: 'admin.states.removed' },
  generations: { queued: 'generation.status.queued', running: 'generation.status.running', succeeded: 'generation.status.succeeded', failed: 'generation.status.failed', cancelled: 'generation.status.cancelled' },
	orders: { test_pending: 'admin.orderStates.test_pending', test_paid: 'admin.orderStates.test_paid', payment_pending: 'admin.orderStates.payment_pending', payment_paid: 'admin.orderStates.payment_paid', payment_failed: 'admin.orderStates.payment_failed', fulfilled: 'admin.orderStates.fulfilled', refund_requested: 'admin.orderStates.refund_requested', test_refunded: 'admin.orderStates.test_refunded', refunded: 'admin.orderStates.refunded', cancelled: 'admin.orderStates.cancelled' },
  tasks: { draft: 'admin.taskStates.draft', open: 'admin.taskStates.open', assigned: 'admin.taskStates.assigned', submitted: 'admin.taskStates.submitted', revision: 'admin.taskStates.revision', accepted: 'admin.taskStates.accepted', disputed: 'admin.taskStates.disputed', cancelled: 'admin.taskStates.cancelled' },
  risks: { open: 'admin.riskStatuses.open', reviewing: 'admin.riskStatuses.reviewing', resolved: 'admin.riskStatuses.resolved', dismissed: 'admin.riskStatuses.dismissed' },
  providers: { enabled: 'admin.providerStates.enabled', disabled: 'admin.providerStates.disabled' },
}
const providerProtocolKeys: Record<AdminProviderConfig['protocol'], string> = {
  openai_responses: 'openaiResponses', openai_chat_completions: 'openaiChatCompletions', openai_images: 'openaiImages', hctopup_async_image: 'hctopupAsyncImage', custom: 'custom',
}

function localizedLabel(keys: Record<string, string>, value: string) {
  return t(keys[value] || 'admin.unknownState')
}

function providerProtocolLabel(protocol: AdminProviderConfig['protocol']) {
  return t(`admin.providerProtocols.${providerProtocolKeys[protocol]}`)
}

function overviewStatusLabel(group: OverviewKey, status: string) {
  return localizedLabel(overviewStatusKeys[group], status)
}

function webhookEventLabel(value: string) {
  return t(`account.webhookEventTypes.${value.replaceAll('.', '_')}`)
}

function syncUserFilters() {
  userQuery.value = typeof route.query.q === 'string' ? route.query.q : ''
  userRole.value = typeof route.query.role === 'string' ? route.query.role : ''
  userStatus.value = typeof route.query.status === 'string' ? route.query.status : ''
}

function userListQuery(cursor = '') {
  return {
    q: userQuery.value || undefined,
    role: userRole.value as 'member' | 'creator' | 'publisher' | 'moderator' | 'admin' | undefined,
    status: userStatus.value as 'active' | 'suspended' | 'deleted' | undefined,
    cursor: cursor || undefined,
    limit: 20,
  }
}

async function loadUserDirectory(cursor = '') {
	await usersDirectory.load(cursor)
}

async function applyUserFilters() {
  const query: Record<string, string> = { tab: 'users' }
  if (userQuery.value.trim()) query.q = userQuery.value.trim()
  if (userRole.value) query.role = userRole.value
  if (userStatus.value) query.status = userStatus.value
  await router.push({ query })
  await load()
}

async function clearUserFilters() {
  userQuery.value = ''
  userRole.value = ''
  userStatus.value = ''
  await router.push({ query: { tab: 'users' } })
  await load()
}

async function loadMoreUsers() {
  if (!userNextCursor.value || userLoadingMore.value) return
  userLoadingMore.value = true
  error.value = ''
  try {
    await loadUserDirectory(userNextCursor.value)
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    userLoadingMore.value = false
  }
}

function syncContentFilters() {
  contentQuery.value = typeof route.query.q === 'string' ? route.query.q : ''
  contentType.value = typeof route.query.type === 'string' ? route.query.type : ''
  contentStatus.value = typeof route.query.status === 'string' ? route.query.status : ''
}

function contentListQuery(cursor = '') {
  return {
    q: contentQuery.value || undefined,
    type: contentType.value as 'work' | undefined,
    status: contentStatus.value as 'draft' | 'published' | 'hidden' | 'removed' | undefined,
    cursor: cursor || undefined,
    limit: 20,
  }
}

async function loadContentDirectory(cursor = '') {
	await contentDirectory.load(cursor)
}

async function applyContentFilters() {
  const query: Record<string, string> = { tab: 'content' }
  if (contentQuery.value.trim()) query.q = contentQuery.value.trim()
  if (contentType.value) query.type = contentType.value
  if (contentStatus.value) query.status = contentStatus.value
  await router.push({ query })
  await load()
}

async function clearContentFilters() {
  contentQuery.value = ''
  contentType.value = ''
  contentStatus.value = ''
  await router.push({ query: { tab: 'content' } })
  await load()
}

async function loadMoreContent() {
  if (!contentNextCursor.value || contentLoadingMore.value) return
  contentLoadingMore.value = true
  error.value = ''
  try {
    await loadContentDirectory(contentNextCursor.value)
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    contentLoadingMore.value = false
  }
}

function syncMediaFilters() {
  mediaQuery.value = typeof route.query.q === 'string' ? route.query.q : ''
  mediaKind.value = typeof route.query.kind === 'string' ? route.query.kind : ''
  mediaStatus.value = typeof route.query.status === 'string' ? route.query.status : ''
}

function mediaListQuery(cursor = '') {
  return {
    q: mediaQuery.value || undefined,
    kind: mediaKind.value as 'image' | 'video' | 'audio' | 'document' | 'prompt' | 'workflow' | undefined,
    status: mediaStatus.value as 'pending' | 'clean' | 'review' | 'rejected' | undefined,
    cursor: cursor || undefined,
    limit: 20,
  }
}

async function loadMediaDirectory(cursor = '') {
	await mediaDirectory.load(cursor)
}

async function applyMediaFilters() {
  const query: Record<string, string> = { tab: 'media' }
  if (mediaQuery.value.trim()) query.q = mediaQuery.value.trim()
  if (mediaKind.value) query.kind = mediaKind.value
  if (mediaStatus.value) query.status = mediaStatus.value
  await router.push({ query })
  await load()
}

async function clearMediaFilters() {
  mediaQuery.value = ''
  mediaKind.value = ''
  mediaStatus.value = ''
  await router.push({ query: { tab: 'media' } })
  await load()
}

async function loadMoreMedia() {
  if (!mediaNextCursor.value || mediaLoadingMore.value) return
  mediaLoadingMore.value = true
  error.value = ''
  try {
    await loadMediaDirectory(mediaNextCursor.value)
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    mediaLoadingMore.value = false
  }
}

function syncGovernanceFilters() {
  reportQuery.value = typeof route.query.reportQ === 'string' ? route.query.reportQ : ''
  reportType.value = typeof route.query.reportType === 'string' ? route.query.reportType : ''
  reportCategory.value = typeof route.query.reportCategory === 'string' ? route.query.reportCategory : ''
  reportStatus.value = typeof route.query.reportStatus === 'string' ? route.query.reportStatus : ''
  appealQuery.value = typeof route.query.appealQ === 'string' ? route.query.appealQ : ''
  appealType.value = typeof route.query.appealType === 'string' ? route.query.appealType : ''
  appealStatus.value = typeof route.query.appealStatus === 'string' ? route.query.appealStatus : ''
}

function reportListQuery(cursor = '') {
  return {
    q: reportQuery.value || undefined,
    type: reportType.value as 'work' | 'post' | 'comment' | undefined,
    category: reportCategory.value as 'spam' | 'harassment' | 'copyright' | 'sexual' | 'violence' | 'misleading' | 'other' | undefined,
    status: reportStatus.value as 'open' | 'reviewing' | 'resolved' | 'dismissed' | undefined,
    cursor: cursor || undefined,
    limit: 20,
  }
}

function appealListQuery(cursor = '') {
  return {
    q: appealQuery.value || undefined,
    type: appealType.value as 'work' | 'post' | 'comment' | undefined,
    status: appealStatus.value as 'pending' | 'upheld' | 'denied' | undefined,
    cursor: cursor || undefined,
    limit: 20,
  }
}

async function loadReportDirectory(cursor = '') {
  const page = await api.adminListGovernanceReports(reportListQuery(cursor))
  if (cursor) {
    const known = new Set(governanceReports.value.map(item => item.id))
    governanceReports.value = [...governanceReports.value, ...page.items.filter(item => !known.has(item.id))]
  } else {
    governanceReports.value = page.items
  }
  reportNextCursor.value = page.nextCursor || null
}

async function loadAppealDirectory(cursor = '') {
  const page = await api.adminListGovernanceAppeals(appealListQuery(cursor))
  if (cursor) {
    const known = new Set(governanceAppeals.value.map(item => item.id))
    governanceAppeals.value = [...governanceAppeals.value, ...page.items.filter(item => !known.has(item.id))]
  } else {
    governanceAppeals.value = page.items
  }
  appealNextCursor.value = page.nextCursor || null
}

function governanceRouteQuery() {
  const query: Record<string, string> = { tab: 'governance' }
  if (reportQuery.value.trim()) query.reportQ = reportQuery.value.trim()
  if (reportType.value) query.reportType = reportType.value
  if (reportCategory.value) query.reportCategory = reportCategory.value
  if (reportStatus.value) query.reportStatus = reportStatus.value
  if (appealQuery.value.trim()) query.appealQ = appealQuery.value.trim()
  if (appealType.value) query.appealType = appealType.value
  if (appealStatus.value) query.appealStatus = appealStatus.value
  return query
}

async function applyReportFilters() {
  await router.push({ query: governanceRouteQuery() })
  await load()
}

async function clearReportFilters() {
  reportQuery.value = ''
  reportType.value = ''
  reportCategory.value = ''
  reportStatus.value = ''
  await router.push({ query: governanceRouteQuery() })
  await load()
}

async function applyAppealFilters() {
  await router.push({ query: governanceRouteQuery() })
  await load()
}

async function clearAppealFilters() {
  appealQuery.value = ''
  appealType.value = ''
  appealStatus.value = ''
  await router.push({ query: governanceRouteQuery() })
  await load()
}

async function loadMoreReports() {
  if (!reportNextCursor.value || reportLoadingMore.value) return
  reportLoadingMore.value = true
  error.value = ''
  try { await loadReportDirectory(reportNextCursor.value) } catch (reason) { error.value = messageFrom(reason) } finally { reportLoadingMore.value = false }
}

async function loadMoreAppeals() {
  if (!appealNextCursor.value || appealLoadingMore.value) return
  appealLoadingMore.value = true
  error.value = ''
  try { await loadAppealDirectory(appealNextCursor.value) } catch (reason) { error.value = messageFrom(reason) } finally { appealLoadingMore.value = false }
}

function syncGenerationFilters() {
  generationQuery.value = typeof route.query.generationQ === 'string' ? route.query.generationQ : ''
  generationMode.value = typeof route.query.generationMode === 'string' ? route.query.generationMode : ''
  generationStatus.value = typeof route.query.generationStatus === 'string' ? route.query.generationStatus : ''
}

function generationListQuery(cursor = '') {
  return {
    q: generationQuery.value || undefined,
    mode: generationMode.value as 'chat' | 'image' | 'video' | 'music' | undefined,
    status: generationStatus.value as 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | undefined,
    cursor: cursor || undefined,
    limit: 20,
  }
}

async function loadGenerationDirectory(cursor = '') {
  const page = await api.adminListGenerations(generationListQuery(cursor))
  if (cursor) {
    const known = new Set(generations.value.map(item => item.id))
    generations.value = [...generations.value, ...page.items.filter(item => !known.has(item.id))]
  } else {
    generations.value = page.items
  }
  generationNextCursor.value = page.nextCursor || null
}

async function applyGenerationFilters() {
  const query: Record<string, string> = { tab: 'generations' }
  if (generationQuery.value.trim()) query.generationQ = generationQuery.value.trim()
  if (generationMode.value) query.generationMode = generationMode.value
  if (generationStatus.value) query.generationStatus = generationStatus.value
  await router.push({ query })
  await load()
}

async function clearGenerationFilters() {
  generationQuery.value = ''
  generationMode.value = ''
  generationStatus.value = ''
  await router.push({ query: { tab: 'generations' } })
  await load()
}

async function loadMoreGenerations() {
  if (!generationNextCursor.value || generationLoadingMore.value) return
  generationLoadingMore.value = true
  error.value = ''
  try { await loadGenerationDirectory(generationNextCursor.value) } catch (reason) { error.value = messageFrom(reason) } finally { generationLoadingMore.value = false }
}

function syncFinanceFilters() {
  financeQuery.value = typeof route.query.financeQ === 'string' ? route.query.financeQ : ''
  financeState.value = typeof route.query.financeState === 'string' ? route.query.financeState : ''
  paymentQuery.value = typeof route.query.paymentQ === 'string' ? route.query.paymentQ : ''
  paymentPurpose.value = typeof route.query.paymentPurpose === 'string' ? route.query.paymentPurpose : ''
  paymentStatus.value = typeof route.query.paymentStatus === 'string' ? route.query.paymentStatus : ''
  paymentMode.value = typeof route.query.paymentMode === 'string' ? route.query.paymentMode : ''
  paymentAttention.value = typeof route.query.paymentAttention === 'string' ? route.query.paymentAttention : 'needs_attention'
}

function financeListQuery(cursor = '') {
  return {
    q: financeQuery.value || undefined,
    state: financeState.value as 'available' | 'reserved' | 'depleted' | undefined,
    cursor: cursor || undefined,
    limit: 20,
  }
}

async function loadFinanceDirectory(cursor = '') {
  const page = await api.adminListFinance(financeListQuery(cursor))
  if (cursor) {
    const known = new Set(finance.value.map(item => `${item.userId}-${item.currency}`))
    finance.value = [...finance.value, ...page.items.filter(item => !known.has(`${item.userId}-${item.currency}`))]
  } else {
    finance.value = page.items
  }
  financeNextCursor.value = page.nextCursor || null
}

async function applyFinanceFilters() {
  const query: Record<string, string> = { tab: 'finance' }
  if (financeQuery.value.trim()) query.financeQ = financeQuery.value.trim()
  if (financeState.value) query.financeState = financeState.value
  await router.push({ query })
  await load()
}

async function clearFinanceFilters() {
  financeQuery.value = ''
  financeState.value = ''
  await router.push({ query: { tab: 'finance' } })
  await load()
}

async function loadMoreFinance() {
  if (!financeNextCursor.value || financeLoadingMore.value) return
  financeLoadingMore.value = true
  error.value = ''
  try { await loadFinanceDirectory(financeNextCursor.value) } catch (reason) { error.value = messageFrom(reason) } finally { financeLoadingMore.value = false }
}

function paymentListQuery(cursor = '') {
  return {
    q: paymentQuery.value || undefined,
    purpose: paymentPurpose.value as 'product' | 'task' | 'wallet_topup' | 'subscription' | undefined,
    status: paymentStatus.value as 'checkout_pending' | 'checkout_open' | 'paid' | 'payment_failed' | 'transfer_pending' | 'transferred' | 'refund_pending' | 'refund_failed' | 'refunded' | 'cancelled' | undefined,
    mode: paymentMode.value as 'test' | 'live' | undefined,
    attention: paymentAttention.value as 'needs_attention' | 'healthy' | undefined,
    cursor: cursor || undefined,
    limit: 20,
  }
}

function paymentPurposeLabel(purpose: string) {
  if (purpose === 'wallet_topup') return locale.value === 'zh-CN' ? '钱包充值' : 'Wallet top-up'
  if (purpose === 'subscription') return locale.value === 'zh-CN' ? '订阅购买' : 'Subscription purchase'
  return t(`admin.paymentPurposes.${purpose}`)
}

async function loadPaymentOperations(cursor = '') {
  const page = await api.adminListPayments(paymentListQuery(cursor))
  if (cursor) {
    const known = new Set(paymentOperations.value.map(item => item.id))
    paymentOperations.value = [...paymentOperations.value, ...page.items.filter(item => !known.has(item.id))]
  } else {
    paymentOperations.value = page.items
  }
  paymentNextCursor.value = page.nextCursor || null
}

async function loadPaymentDestinations(cursor = '') {
  const page = await api.adminListPaymentDestinations({ cursor: cursor || undefined, limit: 20 })
  if (cursor) {
    const known = new Set(paymentDestinations.value.map(item => item.id))
    paymentDestinations.value = [...paymentDestinations.value, ...page.items.filter(item => !known.has(item.id))]
  } else {
    paymentDestinations.value = page.items
  }
  paymentDestinationNextCursor.value = page.nextCursor || null
}

function openPaymentProviderConfig(item: AdminPaymentProviderConfig) {
  Object.assign(paymentProviderForm, item)
  paymentProviderEditorOpen.value = true
}

function closePaymentProviderConfig() {
  paymentProviderEditorOpen.value = false
}

function openSelectedPaymentProviderConfig() {
  const provider = paymentGatewayTab.value
  if (provider === 'general' || provider === 'creem' || provider === 'waffo') return
  const existing = paymentProviderConfigs.value.find(item => item.provider === provider)
  if (existing) {
    openPaymentProviderConfig(existing)
    return
  }
  Object.assign(paymentProviderForm, {
    id: '', provider, enabled: false, environment: 'test', merchantId: '', storeId: '',
    productIdOnetime: '', productIdSubscription: '', secretConfigured: false, connectorConfigured: false,
    createdAt: '', updatedAt: '',
  })
  paymentProviderEditorOpen.value = true
}

function editPaymentGatewayMethod(item: PaymentGatewayMethod) {
  const config = paymentProviderConfigs.value.find(provider => provider.provider === item.provider)
  if (config) openPaymentProviderConfig(config)
}

function savePaymentGatewayGeneral() {
  success.value = t('admin.paymentGatewayGeneralSaved')
  error.value = ''
}

function addTopupAmount() {
  const next = Number(newTopupAmount.value)
  if (Number.isFinite(next) && next > 0) {
    topupAmounts.value = [...topupAmounts.value, Math.round(next)]
    newTopupAmount.value = undefined
    return
  }
  const fallback = topupAmounts.value.at(-1) || 0
  topupAmounts.value = [...topupAmounts.value, Math.max(1, Math.round(fallback * 2))]
}

function removeTopupAmount(index: number) {
  topupAmounts.value = topupAmounts.value.filter((_, itemIndex) => itemIndex !== index)
}

function addDiscountTier() {
  const next = discountTiers.value.at(-1)?.amount || 0
  discountTiers.value = [...discountTiers.value, { amount: Math.max(1, Math.round(next * 2)), rate: 0.95 }]
}

function removeDiscountTier(index: number) {
  discountTiers.value = discountTiers.value.filter((_, itemIndex) => itemIndex !== index)
}

function openPaymentMethodJson() {
  if (!paymentMethodOverrides.value && paymentProviderConfigs.value.length) {
    paymentMethodJson.value = JSON.stringify(paymentProviderConfigs.value.map(item => ({
      type: item.provider,
      name: item.provider === 'waffo_pancake' ? t('admin.paymentGatewayTabs.waffoPancake') : item.provider.toUpperCase(),
      icon: 'LuCreditCard',
      minimum: item.productIdOnetime || undefined,
      enabled: item.enabled,
    })), null, 2)
  }
  paymentMethodJsonOpen.value = true
}

function savePaymentMethodJson() {
  try {
    const parsed = JSON.parse(paymentMethodJson.value)
    if (!Array.isArray(parsed)) throw new Error('invalid')
    paymentMethodOverrides.value = parsed.filter((item): item is Record<string, unknown> => typeof item === 'object' && item !== null && typeof (item as Record<string, unknown>).type === 'string').map((item, index) => ({
      id: String(item.id || item.type || index),
      name: String(item.name || item.type),
      handle: String(item.type),
      icon: String(item.icon || 'LuCreditCard'),
      minimum: String(item.minimum ?? '—'),
      enabled: item.enabled !== false,
      provider: String(item.type),
    }))
    paymentMethodJson.value = JSON.stringify(parsed, null, 2)
    paymentMethodJsonOpen.value = false
    success.value = t('admin.paymentGatewayMethodsSaved')
    error.value = ''
  } catch {
    error.value = t('admin.paymentGatewayInvalidJson')
    success.value = ''
  }
}

async function submitPaymentProviderConfig() {
  actionLoading.value = true
  error.value = ''
  success.value = ''
  try {
    const updated = await api.adminUpdatePaymentProviderConfig(paymentProviderForm.provider, {
      enabled: paymentProviderForm.enabled,
      environment: paymentProviderForm.environment,
      merchantId: paymentProviderForm.merchantId,
      storeId: paymentProviderForm.storeId,
      productIdOnetime: paymentProviderForm.productIdOnetime,
      productIdSubscription: paymentProviderForm.productIdSubscription,
    })
    paymentProviderConfigs.value = paymentProviderConfigs.value.map(item => item.provider === updated.provider ? updated : item)
    success.value = t('admin.commandComplete')
    closePaymentProviderConfig()
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionLoading.value = false
  }
}

async function loadProviderCostReconciliations() {
  providerCostReconciliationLoading.value = true
  try {
    const page = await api.adminListProviderCostReconciliations({ limit: 20 })
    providerCostReconciliations.value = page.items
    providerCostReconciliationAvailable.value = true
  } catch (reason) {
    if (typeof reason !== 'object' || reason === null || !('code' in reason) || reason.code !== 'provider_cost_reconciliation_unavailable') throw reason
    providerCostReconciliations.value = []
    providerCostReconciliationAvailable.value = false
  } finally {
    providerCostReconciliationLoading.value = false
  }
}

async function requestProviderCostReconciliation() {
  actionLoading.value = true
  error.value = ''
  success.value = ''
  try {
    const item = await api.adminRequestProviderCostReconciliation({
      provider: 'openai', periodStart: `${providerCostReconciliationForm.periodStart}T00:00:00Z`, periodEnd: `${providerCostReconciliationForm.periodEnd}T00:00:00Z`,
    })
    providerCostReconciliations.value = [item, ...providerCostReconciliations.value.filter(existing => existing.id !== item.id)]
    success.value = t('admin.providerCostReconciliationQueued')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionLoading.value = false
  }
}

async function applyPaymentFilters() {
  const query: Record<string, string> = { tab: 'finance' }
  if (financeQuery.value.trim()) query.financeQ = financeQuery.value.trim()
  if (financeState.value) query.financeState = financeState.value
  if (paymentQuery.value.trim()) query.paymentQ = paymentQuery.value.trim()
  if (paymentPurpose.value) query.paymentPurpose = paymentPurpose.value
  if (paymentStatus.value) query.paymentStatus = paymentStatus.value
  if (paymentMode.value) query.paymentMode = paymentMode.value
  if (paymentAttention.value) query.paymentAttention = paymentAttention.value
  await router.push({ query })
  await load()
}

async function loadMorePayments() {
  if (!paymentNextCursor.value || paymentLoadingMore.value) return
  paymentLoadingMore.value = true
  error.value = ''
  try { await loadPaymentOperations(paymentNextCursor.value) } catch (reason) { error.value = messageFrom(reason) } finally { paymentLoadingMore.value = false }
}

async function loadMorePaymentDestinations() {
  if (!paymentDestinationNextCursor.value || paymentLoadingMore.value) return
  paymentLoadingMore.value = true
  error.value = ''
  try { await loadPaymentDestinations(paymentDestinationNextCursor.value) } catch (reason) { error.value = messageFrom(reason) } finally { paymentLoadingMore.value = false }
}

function syncTaskFilters() {
  taskQuery.value = typeof route.query.taskQ === 'string' ? route.query.taskQ : ''
  taskStatus.value = typeof route.query.taskStatus === 'string' ? route.query.taskStatus : ''
  taskDisputeStatus.value = typeof route.query.taskDisputeStatus === 'string' ? route.query.taskDisputeStatus : ''
}

function taskListQuery(cursor = '') {
  return {
    q: taskQuery.value || undefined,
    status: taskStatus.value as AdminTaskOperation['status'] | undefined,
    disputeStatus: taskDisputeStatus.value as 'none' | 'open' | 'resolved_creator' | 'resolved_client' | undefined,
    cursor: cursor || undefined,
    limit: 20,
  }
}

async function loadTaskDirectory(cursor = '') {
  const page = await api.adminListTasks(taskListQuery(cursor))
  if (cursor) {
    const known = new Set(taskOperations.value.map(item => item.id))
    taskOperations.value = [...taskOperations.value, ...page.items.filter(item => !known.has(item.id))]
  } else {
    taskOperations.value = page.items
  }
  taskNextCursor.value = page.nextCursor || null
}

async function applyTaskFilters() {
  const query: Record<string, string> = { tab: 'tasks' }
  if (taskQuery.value.trim()) query.taskQ = taskQuery.value.trim()
  if (taskStatus.value) query.taskStatus = taskStatus.value
  if (taskDisputeStatus.value) query.taskDisputeStatus = taskDisputeStatus.value
  await router.push({ query })
  await load()
}

async function clearTaskFilters() {
  taskQuery.value = ''
  taskStatus.value = ''
  taskDisputeStatus.value = ''
  await router.push({ query: { tab: 'tasks' } })
  await load()
}

async function loadMoreTasks() {
  if (!taskNextCursor.value || taskLoadingMore.value) return
  taskLoadingMore.value = true
  error.value = ''
  try { await loadTaskDirectory(taskNextCursor.value) } catch (reason) { error.value = messageFrom(reason) } finally { taskLoadingMore.value = false }
}

function syncSupportFilters() {
  supportQuery.value = typeof route.query.supportQ === 'string' ? route.query.supportQ : ''
  supportStatus.value = typeof route.query.supportStatus === 'string' ? route.query.supportStatus : ''
  supportCategory.value = typeof route.query.supportCategory === 'string' ? route.query.supportCategory : ''
}

function supportListQuery(cursor = '') {
  return {
    q: supportQuery.value || undefined,
    status: supportStatus.value as 'open' | 'in_review' | 'waiting_for_requester' | 'resolved' | 'closed' | undefined,
    category: supportCategory.value as 'general_support' | 'billing' | 'account' | 'task_or_order' | 'copyright' | undefined,
    cursor: cursor || undefined,
    limit: 20,
  }
}

async function loadSupportDirectory(cursor = '') {
  const page = await api.adminListSupportCases(supportListQuery(cursor))
  if (cursor) {
    const known = new Set(supportCases.value.map(item => item.id))
    supportCases.value = [...supportCases.value, ...page.items.filter(item => !known.has(item.id))]
  } else {
    supportCases.value = page.items
  }
  supportNextCursor.value = page.nextCursor || null
}

async function applySupportFilters() {
  const query: Record<string, string> = { tab: 'support' }
  if (supportQuery.value.trim()) query.supportQ = supportQuery.value.trim()
  if (supportStatus.value) query.supportStatus = supportStatus.value
  if (supportCategory.value) query.supportCategory = supportCategory.value
  await router.push({ query })
  await load()
}

async function clearSupportFilters() {
  supportQuery.value = ''
  supportStatus.value = ''
  supportCategory.value = ''
  await router.push({ query: { tab: 'support' } })
  await load()
}

async function loadMoreSupport() {
  if (!supportNextCursor.value || supportLoadingMore.value) return
  supportLoadingMore.value = true
  error.value = ''
  try { await loadSupportDirectory(supportNextCursor.value) } catch (reason) { error.value = messageFrom(reason) } finally { supportLoadingMore.value = false }
}

function syncRiskFilters() {
  riskQuery.value = typeof route.query.riskQ === 'string' ? route.query.riskQ : ''
  riskStatus.value = typeof route.query.riskStatus === 'string' ? route.query.riskStatus : ''
  riskSeverity.value = typeof route.query.riskSeverity === 'string' ? route.query.riskSeverity : ''
}

function riskListQuery(cursor = '') {
  const resourceType = typeof route.query.resourceType === 'string' ? route.query.resourceType : undefined
  const resourceId = typeof route.query.resourceId === 'string' ? route.query.resourceId : undefined
  return {
    q: riskQuery.value || undefined,
    status: riskStatus.value as 'open' | 'reviewing' | 'resolved' | 'dismissed' | undefined,
    severity: riskSeverity.value as 'low' | 'medium' | 'high' | 'critical' | undefined,
    resourceType: resourceType as 'task' | 'order' | 'post' | 'asset' | undefined,
    resourceId,
    cursor: cursor || undefined,
    limit: 20,
  }
}

async function loadRiskDirectory(cursor = '') {
  const page = await api.adminListRiskSignals(riskListQuery(cursor))
  if (cursor) {
    const known = new Set(riskSignals.value.map(item => item.id))
    riskSignals.value = [...riskSignals.value, ...page.items.filter(item => !known.has(item.id))]
  } else {
    riskSignals.value = page.items
  }
  riskNextCursor.value = page.nextCursor || null
}

function riskRouteQuery() {
  const query: Record<string, string> = { tab: 'risk' }
  if (riskQuery.value.trim()) query.riskQ = riskQuery.value.trim()
  if (riskStatus.value) query.riskStatus = riskStatus.value
  if (riskSeverity.value) query.riskSeverity = riskSeverity.value
  if (typeof route.query.resourceType === 'string') query.resourceType = route.query.resourceType
  if (typeof route.query.resourceId === 'string') query.resourceId = route.query.resourceId
  return query
}

async function applyRiskFilters() {
  await router.push({ query: riskRouteQuery() })
  await load()
}

async function clearRiskFilters() {
  riskQuery.value = ''
  riskStatus.value = ''
  riskSeverity.value = ''
  await router.push({ query: riskRouteQuery() })
  await load()
}

async function loadMoreRisk() {
  if (!riskNextCursor.value || riskLoadingMore.value) return
  riskLoadingMore.value = true
  error.value = ''
  try { await loadRiskDirectory(riskNextCursor.value) } catch (reason) { error.value = messageFrom(reason) } finally { riskLoadingMore.value = false }
}

function syncDeveloperRecoveryFilters() {
  webhookQuery.value = typeof route.query.webhookQ === 'string' ? route.query.webhookQ : ''
  webhookEventType.value = typeof route.query.webhookEventType === 'string' ? route.query.webhookEventType : ''
  emailQuery.value = typeof route.query.emailQ === 'string' ? route.query.emailQ : ''
  emailKind.value = typeof route.query.emailKind === 'string' ? route.query.emailKind : ''
}

function webhookRecoveryListQuery(cursor = '') {
  return {
    q: webhookQuery.value || undefined,
    eventType: webhookEventType.value as 'developer.webhook.test' | 'generation.completed' | 'work.published' | 'marketplace.order.fulfilled' | 'marketplace.order.refunded' | undefined,
    cursor: cursor || undefined,
    limit: 20,
  }
}

function emailRecoveryListQuery(cursor = '') {
  return {
    q: emailQuery.value || undefined,
    kind: emailKind.value as 'verify_email' | 'password_reset' | undefined,
    cursor: cursor || undefined,
    limit: 20,
  }
}

async function loadWebhookRecoveryDirectory(cursor = '') {
  const page = await api.adminListWebhookDeadLetters(webhookRecoveryListQuery(cursor))
  if (cursor) {
    const known = new Set(webhookDeadLetters.value.map(item => item.id))
    webhookDeadLetters.value = [...webhookDeadLetters.value, ...page.items.filter(item => !known.has(item.id))]
  } else {
    webhookDeadLetters.value = page.items
  }
  webhookNextCursor.value = page.nextCursor || null
}

async function loadEmailRecoveryDirectory(cursor = '') {
  const page = await api.adminListEmailActionDeadLetters(emailRecoveryListQuery(cursor))
  if (cursor) {
    const known = new Set(emailActionDeadLetters.value.map(item => item.id))
    emailActionDeadLetters.value = [...emailActionDeadLetters.value, ...page.items.filter(item => !known.has(item.id))]
  } else {
    emailActionDeadLetters.value = page.items
  }
  emailNextCursor.value = page.nextCursor || null
}

async function loadDataRightsDirectory(cursor = '') {
  const page = await api.adminListDataRights({ limit: 20, ...(cursor ? { cursor } : {}) })
  if (cursor) {
    const known = new Set(dataRightsItems.value.map(item => item.id))
    dataRightsItems.value = [...dataRightsItems.value, ...page.items.filter(item => !known.has(item.id))]
  } else {
    dataRightsItems.value = page.items
  }
  dataRightsNextCursor.value = page.nextCursor || null
}

async function loadLegalHoldsDirectory(cursor = '') {
  const page = await api.adminListDataRightsHolds({ limit: 20, ...(cursor ? { cursor } : {}) })
  if (cursor) {
    const known = new Set(legalHolds.value.map(item => item.id))
    legalHolds.value = [...legalHolds.value, ...page.items.filter(item => !known.has(item.id))]
  } else {
    legalHolds.value = page.items
  }
  legalHoldsNextCursor.value = page.nextCursor || null
}

async function loadDataRightsDirectories() {
  await Promise.all([loadDataRightsDirectory(), loadLegalHoldsDirectory()])
}

async function loadMoreDataRights() {
  if (!dataRightsNextCursor.value || dataRightsLoadingMore.value) return
  dataRightsLoadingMore.value = true
  error.value = ''
  try { await loadDataRightsDirectory(dataRightsNextCursor.value) } catch (reason) { error.value = messageFrom(reason) } finally { dataRightsLoadingMore.value = false }
}

async function loadMoreLegalHolds() {
  if (!legalHoldsNextCursor.value || legalHoldsLoadingMore.value) return
  legalHoldsLoadingMore.value = true
  error.value = ''
  try { await loadLegalHoldsDirectory(legalHoldsNextCursor.value) } catch (reason) { error.value = messageFrom(reason) } finally { legalHoldsLoadingMore.value = false }
}

function developerRecoveryRouteQuery() {
  const query: Record<string, string> = { tab: 'developer' }
  if (webhookQuery.value.trim()) query.webhookQ = webhookQuery.value.trim()
  if (webhookEventType.value) query.webhookEventType = webhookEventType.value
  if (emailQuery.value.trim()) query.emailQ = emailQuery.value.trim()
  if (emailKind.value) query.emailKind = emailKind.value
  return query
}

async function applyDeveloperRecoveryFilters() {
  await router.push({ query: developerRecoveryRouteQuery() })
  await load()
}

async function clearWebhookRecoveryFilters() {
  webhookQuery.value = ''
  webhookEventType.value = ''
  await applyDeveloperRecoveryFilters()
}

async function clearEmailRecoveryFilters() {
  emailQuery.value = ''
  emailKind.value = ''
  await applyDeveloperRecoveryFilters()
}

async function loadMoreWebhookRecovery() {
  if (!webhookNextCursor.value || webhookLoadingMore.value) return
  webhookLoadingMore.value = true
  error.value = ''
  try { await loadWebhookRecoveryDirectory(webhookNextCursor.value) } catch (reason) { error.value = messageFrom(reason) } finally { webhookLoadingMore.value = false }
}

async function loadMoreEmailRecovery() {
  if (!emailNextCursor.value || emailLoadingMore.value) return
  emailLoadingMore.value = true
  error.value = ''
  try { await loadEmailRecoveryDirectory(emailNextCursor.value) } catch (reason) { error.value = messageFrom(reason) } finally { emailLoadingMore.value = false }
}

async function load() {
  loading.value = true
  error.value = ''
  closeCommand()
  try {
    const user = await session.ensure()
    if (!user || !user.permissions.includes('admin:access')) return
    const tab = activeTab.value
    if (tab === 'overview') {
      const [overviewResult] = await Promise.all([api.adminOverview(), loadSystemSettings()])
      overview.value = overviewResult
      resetSystemSettingForm()
    }
    if (tab === 'users') {
      syncUserFilters()
      await loadUserDirectory()
    }
    if (tab === 'content') {
      syncContentFilters()
      await loadContentDirectory()
    }
    if (tab === 'media') {
      syncMediaFilters()
      await loadMediaDirectory()
    }
    if (tab === 'governance') {
      syncGovernanceFilters()
      await Promise.all([loadReportDirectory(), loadAppealDirectory()])
    }
    if (tab === 'support') {
	  syncSupportFilters()
	  await loadSupportDirectory()
      selectedSupport.value = null
    }
    if (tab === 'generations') {
      syncGenerationFilters()
      await loadGenerationDirectory()
    }
    if (tab === 'tasks') {
      syncTaskFilters()
      await loadTaskDirectory()
    }
    if (tab === 'providers') providerConfigs.value = (await api.adminListProviderConfigs()).items
    if (tab === 'models') {
      const [policy, providerResult] = await Promise.all([api.adminGetModelRoutes({ limit: 20 }), api.adminListProviders()])
      modelRoutePolicy.value = policy
      providers.value = providerResult.items
      resetModelRouteForm()
    }
    if (tab === 'settings') { await loadSystemSettings(); resetSystemSettingForm() }
    if (tab === 'developer') {
		syncDeveloperRecoveryFilters()
		const [access] = await Promise.all([api.adminGetDeveloperAccess(), loadWebhookRecoveryDirectory(), loadEmailRecoveryDirectory()])
		developerAdminAccess.value = access
		resetDeveloperControlForm()
    }
    if (tab === 'finance') {
      syncFinanceFilters()
      const [, , , , planResult, providerResult, paymentProviderResult] = await Promise.all([
        loadFinanceDirectory(),
        loadPaymentOperations(),
        loadPaymentDestinations(),
        providerCostReconciliationAvailable.value ? loadProviderCostReconciliations() : Promise.resolve(),
        api.adminListSubscriptionPlans(),
        api.adminListProviderConfigs(),
        api.adminListPaymentProviderConfigs(),
      ])
      subscriptionPlans.value = planResult.items
      providerConfigs.value = providerResult.items
      paymentProviderConfigs.value = paymentProviderResult.items
    }
    if (tab === 'risk') {
	  syncRiskFilters()
	  await loadRiskDirectory()
    }
    if (tab === 'riskRules') {
      await loadRiskRuleHistory()
      resetRiskRuleForm()
    }
    if (tab === 'ranking') {
      await Promise.all([loadRankingHistory(), loadDiscoveryHistories()])
      resetRankingForm()
    }
    if (tab === 'dataRights') {
		await loadDataRightsDirectories()
    }
    if (tab === 'diagnostics') operationalDiagnostics.value = await api.adminGetOperationalDiagnostics()
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    loading.value = false
  }
}

async function useAdminDemo() {
  loading.value = true
  error.value = ''
  const user = await session.startDemoSession('admin')
  if (!user) error.value = session.error
  const tabWillChange = route.query.tab !== 'overview'
  await router.replace({ query: { tab: 'overview' } })
  if (!tabWillChange) await load()
}

async function initialize() {
  const runtime = await api.meta().catch(() => null)
  localDemoAvailable.value = Boolean(runtime?.localDemoAvailable)
  providerCostReconciliationAvailable.value = Boolean(runtime?.providerCostReconciliation?.enabled)
  await load()
}

function resetCommand() {
  Object.assign(command, { kind: '', id: '', title: '', role: '', status: '', outcome: '', decision: '', action: '', destinationID: '', enabled: false, displayName: '', modelName: '', description: '', estimatedCostCents: 0, deltaCents: 0, authorityReference: '' })
}

function closeCommand() {
  if (commandDrawerOpen.value) commandDrawerOpen.value = false
  else resetCommand()
}

watch(() => command.kind, (kind) => {
  if (kind) commandDrawerOpen.value = true
})

function openUser(item: AdminUser) {
  Object.assign(command, { kind: 'user', id: item.id, title: item.displayName, role: item.role, status: item.status })
}

function openContent(item: AdminContent) {
  Object.assign(command, { kind: 'content', id: item.id, title: item.title, status: item.status === 'draft' ? 'hidden' : item.status })
}

function openGeneration(item: AdminGeneration) {
  Object.assign(command, { kind: 'generation', id: item.id, title: item.prompt })
}

function openTaskOperation(item: AdminTaskOperation) {
  if (!item.disputeVersion) return
  Object.assign(command, { kind: 'task', id: item.id, title: item.title, decision: 'cancel_without_settlement', status: String(item.disputeVersion) })
  focusCommandPanel()
}

function openMedia(item: AdminMediaItem) {
  Object.assign(command, { kind: 'media', id: item.id, title: item.title, status: item.scanStatus === 'pending' ? 'review' : item.scanStatus })
}

function openReport(item: AdminGovernanceReport) {
  Object.assign(command, { kind: 'report', id: item.id, title: item.resourceTitle, outcome: 'no_action' })
}

function openAppeal(item: AdminGovernanceAppeal) {
  Object.assign(command, { kind: 'appeal', id: item.id, title: item.resourceTitle, decision: 'denied' })
}

function resetProviderConfigForm() {
  Object.assign(providerConfigForm, { id: '', name: '', protocol: 'openai_chat_completions', endpoint: '', apiKey: '', adminEnabled: true })
  providerConfigEditorOpen.value = false
}

function openNewProviderConfig() {
  resetProviderConfigForm()
  providerConfigEditorOpen.value = true
}

function editProviderConfig(item: AdminProviderConfig) {
  Object.assign(providerConfigForm, { id: item.id, name: item.name, protocol: item.protocol, endpoint: item.endpoint, apiKey: '', adminEnabled: item.adminEnabled })
  providerConfigEditorOpen.value = true
}

function openLegacyProvider(item: AdminProvider) {
  Object.assign(command, { kind: 'provider', id: item.id, title: item.displayName, enabled: item.adminEnabled, displayName: item.displayName, modelName: item.modelName, description: item.description, estimatedCostCents: item.estimatedCostCents })
  focusCommandPanel()
}

function openProviderModel(item: AdminProviderConfig) {
  const mode = item.models[0]?.mode || 'chat'
  Object.assign(providerModelForm, { id: '', providerId: item.id, mode, modelName: '', displayName: '', description: '', estimatedCostCents: 0, pointPricing: defaultModelPointPricing(mode), capabilities: defaultModelCapabilities(mode), adminEnabled: true })
  providerModelEditorOpen.value = true
}

function openNewProviderModel() {
  const provider = providerConfigs.value[0]
  if (provider) openProviderModel(provider)
}

function editProviderModel(provider: AdminProviderConfig, item: AdminProviderModel) {
  Object.assign(providerModelForm, { id: item.id, providerId: provider.id, mode: item.mode, modelName: item.modelName, displayName: item.displayName, description: item.description, estimatedCostCents: item.estimatedCostCents, pointPricing: cloneModelPointPricing(item.pointPricing || defaultModelPointPricing(item.mode)), capabilities: item.capabilities || defaultModelCapabilities(item.mode), adminEnabled: item.adminEnabled })
  providerModelEditorOpen.value = true
}

function resetProviderModelCapabilities() {
  providerModelForm.capabilities = defaultModelCapabilities(providerModelForm.mode)
  providerModelForm.pointPricing = defaultModelPointPricing(providerModelForm.mode)
}

function providerPointPricingSummary(model: AdminProviderModel) {
  const rule = model.pointPricing || defaultModelPointPricing(model.mode)
  if (model.mode === 'chat') return t('admin.chatPointRateSummary', { input: rule.inputPointsPer1KTokens, output: rule.outputPointsPer1KTokens })
  if (model.mode === 'image') return t('admin.imagePointRateSummary', { min: Math.min(...(rule.imageResolutionPrices.length ? rule.imageResolutionPrices : defaultModelPointPricing('image').imageResolutionPrices).map(item => item.points)) })
  return t('admin.durationPointRateSummary', { points: rule.pointsPerSecond })
}

function addImageResolutionPrice() {
  const used = new Set(providerModelForm.pointPricing.imageResolutionPrices.map(item => item.resolution))
  const resolution = ['512x512', '1024x1024', '1024x1536', '1536x1024', '2048x2048'].find(item => !used.has(item)) || `${1024 + used.size * 256}x${1024 + used.size * 256}`
  providerModelForm.pointPricing.imageResolutionPrices.push({ resolution, points: Math.max(1, providerModelForm.pointPricing.minimumPoints) })
}

function removeImageResolutionPrice(index: number) {
  if (providerModelForm.pointPricing.imageResolutionPrices.length > 1) providerModelForm.pointPricing.imageResolutionPrices.splice(index, 1)
}

function toggleProviderCapability(key: 'aspectRatios' | 'qualities' | 'outputFormats' | 'resultFormats' | 'referenceKinds', value: string) {
  const values = providerModelForm.capabilities[key]
  const next = values.includes(value) ? values.filter(item => item !== value) : [...values, value]
  providerModelForm.capabilities[key] = next
  if (key === 'outputFormats') providerModelForm.capabilities.resultFormats = [...next]
}

function toggleProviderDuration(value: number) {
  const values = providerModelForm.capabilities.durationSeconds
  providerModelForm.capabilities.durationSeconds = values.includes(value) ? values.filter(item => item !== value) : [...values, value]
}

function providerModelCapabilitySummary(model: AdminProviderModel) {
  const capabilities = model.capabilities || defaultModelCapabilities(model.mode)
  const parts = [capabilities.outputFormats.join(' / ')]
  if (capabilities.aspectRatios.length) parts.push(capabilities.aspectRatios.join(' · '))
  if (capabilities.durationSeconds.length) parts.push(capabilities.durationSeconds.map(value => `${value}s`).join(' · '))
  if (capabilities.supportsMask) parts.push(t('admin.capabilityMask'))
  return parts.filter(Boolean).join(' · ')
}

function providerModelCapabilityBadges(model: AdminProviderModel) {
  const capabilities = model.capabilities || defaultModelCapabilities(model.mode)
  const badges = [t(`create.modes.${model.mode}`), ...capabilities.outputFormats.slice(0, 2).map(format => format.toUpperCase())]
  if (capabilities.referenceKinds.length) badges.push(t('admin.capabilityReferences'))
  if (capabilities.supportsMask) badges.push(t('admin.capabilityMask'))
  return badges.slice(0, 4)
}

function closeProviderEditors() {
  providerConfigEditorOpen.value = false
  providerModelEditorOpen.value = false
}

function resetSubscriptionPlanForm() {
  Object.assign(subscriptionPlanForm, { id: '', tierCode: '', name: '', description: '', priceCents: 0, currency: 'USD', includedPoints: 10000, billingPeriodDays: 30, sortOrder: subscriptionPlans.value.length * 10, active: true, modelIds: [] })
}

function closeSubscriptionPlanEditor() {
  subscriptionPlanEditorOpen.value = false
}

function openNewSubscriptionPlan() {
  resetSubscriptionPlanForm()
  subscriptionPlanEditorOpen.value = true
}

function editSubscriptionPlan(plan: SubscriptionPlan) {
  Object.assign(subscriptionPlanForm, { id: plan.id, tierCode: plan.tierCode, name: plan.name, description: plan.description, priceCents: plan.priceCents, currency: plan.currency, includedPoints: plan.includedPoints, billingPeriodDays: plan.billingPeriodDays, sortOrder: plan.sortOrder, active: plan.active, modelIds: [...plan.modelIds] })
  subscriptionPlanEditorOpen.value = true
}

function toggleSubscriptionPlanModel(modelId: string) {
  subscriptionPlanForm.modelIds = subscriptionPlanForm.modelIds.includes(modelId) ? subscriptionPlanForm.modelIds.filter(id => id !== modelId) : [...subscriptionPlanForm.modelIds, modelId]
}

async function submitSubscriptionPlan() {
  actionLoading.value = true; error.value = ''; success.value = ''
  try {
    const payload: SubscriptionPlanInput = { tierCode: subscriptionPlanForm.tierCode, name: subscriptionPlanForm.name, description: subscriptionPlanForm.description, priceCents: subscriptionPlanForm.priceCents, currency: subscriptionPlanForm.currency, includedPoints: subscriptionPlanForm.includedPoints, billingPeriodDays: subscriptionPlanForm.billingPeriodDays, sortOrder: subscriptionPlanForm.sortOrder, active: subscriptionPlanForm.active, modelIds: subscriptionPlanForm.modelIds }
    const saved = subscriptionPlanForm.id ? await api.adminUpdateSubscriptionPlan(subscriptionPlanForm.id, payload) : await api.adminCreateSubscriptionPlan(payload)
    subscriptionPlans.value = subscriptionPlanForm.id ? subscriptionPlans.value.map(plan => plan.id === saved.id ? saved : plan) : [...subscriptionPlans.value, saved]
    closeSubscriptionPlanEditor(); success.value = t('admin.subscriptionPlanSaved')
  } catch (reason) { error.value = messageFrom(reason) } finally { actionLoading.value = false }
}

async function submitProviderConfig() {
  actionLoading.value = true; error.value = ''; success.value = ''
  try {
    const payload = { name: providerConfigForm.name, protocol: providerConfigForm.protocol, endpoint: providerConfigForm.endpoint, adminEnabled: providerConfigForm.adminEnabled, ...(providerConfigForm.apiKey.trim() ? { apiKey: providerConfigForm.apiKey } : {}) }
    const updated = providerConfigForm.id ? await api.adminUpdateProviderConfig(providerConfigForm.id, payload as AdminProviderConfigUpdate) : await api.adminCreateProviderConfig(payload as AdminProviderConfigCreate)
    providerConfigs.value = providerConfigForm.id ? providerConfigs.value.map((item) => item.id === updated.id ? updated : item) : [...providerConfigs.value, updated]
    closeProviderEditors(); success.value = t('admin.providerConfigSaved')
  } catch (reason) { error.value = messageFrom(reason) } finally { actionLoading.value = false }
}

function providerSyncMode(item: AdminProviderConfig) {
  return providerSyncModes[item.id] || (item.protocol === 'hctopup_async_image' ? 'image' : 'chat')
}

function setProviderSyncMode(providerID: string, event: globalThis.Event) {
  const value = (event.target as InstanceType<typeof globalThis.HTMLSelectElement>).value
  if (['chat', 'image', 'video', 'music'].includes(value)) providerSyncModes[providerID] = value as 'chat' | 'image' | 'video' | 'music'
}

async function syncProviderModels(item: AdminProviderConfig) {
  actionLoading.value = true; error.value = ''; success.value = ''
  try {
    const updated = await api.adminSyncProviderModels(item.id, { mode: providerSyncMode(item) })
    providerConfigs.value = providerConfigs.value.map(candidate => candidate.id === updated.id ? updated : candidate)
    success.value = t('admin.providerModelsSynced')
  } catch (reason) { error.value = messageFrom(reason) } finally { actionLoading.value = false }
}

async function syncEditingProviderModels() {
  const provider = providerConfigs.value.find(item => item.id === providerConfigForm.id)
  if (provider) await syncProviderModels(provider)
}

async function submitProviderModel() {
  actionLoading.value = true; error.value = ''; success.value = ''
  try {
    const saved = providerModelForm.id
      ? await api.adminUpdateProviderModel(providerModelForm.id, { mode: providerModelForm.mode, modelName: providerModelForm.modelName, displayName: providerModelForm.displayName, description: providerModelForm.description, estimatedCostCents: 0, pointPricing: providerModelForm.pointPricing, capabilities: providerModelForm.capabilities, adminEnabled: providerModelForm.adminEnabled })
      : await api.adminCreateProviderModel(providerModelForm.providerId, { mode: providerModelForm.mode, modelName: providerModelForm.modelName, displayName: providerModelForm.displayName, description: providerModelForm.description, estimatedCostCents: 0, pointPricing: providerModelForm.pointPricing, capabilities: providerModelForm.capabilities, adminEnabled: providerModelForm.adminEnabled })
    providerConfigs.value = providerConfigs.value.map((item) => item.id === saved.providerId ? { ...item, models: providerModelForm.id ? item.models.map((model) => model.id === saved.id ? saved : model) : [...item.models, saved] } : item)
    closeProviderEditors(); success.value = t('admin.providerModelSaved')
  } catch (reason) { error.value = messageFrom(reason) } finally { actionLoading.value = false }
}

async function toggleProviderModelStatus(item: AdminProviderModel) {
  actionLoading.value = true; error.value = ''; success.value = ''
  try {
    const saved = await api.adminUpdateProviderModel(item.id, { adminEnabled: !item.adminEnabled })
    initializedProviderSwitches.value = new Set(initializedProviderSwitches.value).add(item.id)
    providerConfigs.value = providerConfigs.value.map((provider) => ({ ...provider, models: provider.models.map((model) => model.id === saved.id ? saved : model) }))
    success.value = t('admin.providerModelSaved')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally { actionLoading.value = false }
}

async function archiveProviderConfig(item: AdminProviderConfig) {
  actionLoading.value = true; error.value = ''; success.value = ''
  try { await api.adminArchiveProviderConfig(item.id); providerConfigs.value = providerConfigs.value.filter((candidate) => candidate.id !== item.id); closeProviderEditors(); success.value = t('admin.providerConfigArchived') } catch (reason) { error.value = messageFrom(reason) } finally { actionLoading.value = false }
}

async function archiveEditingProviderConfig() {
  const provider = providerConfigs.value.find(item => item.id === providerConfigForm.id)
  if (provider) await archiveProviderConfig(provider)
}

async function archiveProviderModel(item: AdminProviderModel) {
  actionLoading.value = true; error.value = ''; success.value = ''
  try { await api.adminArchiveProviderModel(item.id); providerConfigs.value = providerConfigs.value.map((provider) => ({ ...provider, models: provider.models.filter((model) => model.id !== item.id) })); success.value = t('admin.providerModelArchived') } catch (reason) { error.value = messageFrom(reason) } finally { actionLoading.value = false }
}

function openFinance(item: AdminFinanceAccount) {
  Object.assign(command, { kind: 'finance', id: item.userId, title: item.displayName, deltaCents: 0 })
}

function openPaymentRecovery(item: AdminPaymentOperation, action: 'retry_transfer' | 'retry_refund') {
  Object.assign(command, { kind: 'payment', id: item.id, title: item.resourceTitle, action, status: String(item.version) })
  focusCommandPanel()
}

function openPaymentEventReplay(item: AdminPaymentOperation) {
  if (!item.providerEvent) return
  Object.assign(command, { kind: 'paymentEvent', id: item.providerEvent.id, title: item.providerEvent.eventType, status: String(item.providerEvent.version) })
  focusCommandPanel()
}

function openPaymentDestination(item: AdminPaymentOperation) {
  if (!item.payeeId) return
  Object.assign(command, { kind: 'paymentDestination', id: item.payeeId, title: item.payeeDisplayName || item.payeeHandle || item.resourceTitle, destinationID: item.destination?.destinationId || '', status: String(item.destination?.version || 0), enabled: item.destination?.status === 'verified' })
  focusCommandPanel()
}

function focusCommandPanel() {
  void nextTick(() => {
    commandPanel.value?.querySelector<InstanceType<typeof globalThis.HTMLElement>>('input, textarea, [role="combobox"], button[type="submit"]')?.focus({ preventScroll: true })
  })
}

function openRisk(item: AdminRiskSignal) {
  Object.assign(command, { kind: 'risk', id: item.id, title: item.resourceTitle, decision: 'monitor', status: String(item.version) })
  focusCommandPanel()
}

function openDataRightsHold(item: DataRightsRequest) {
  if (!item.ownerId) return
  Object.assign(command, { kind: 'dataRightsHold', id: item.ownerId, title: `@${item.ownerHandle}`, authorityReference: '' })
}

function resetRankingForm() {
  if (!rankingPolicy.value) return
  const current = rankingPolicy.value.current
  Object.assign(rankingForm, {
    name: current.name, titleExactWeight: current.titleExactWeight, titlePrefixWeight: current.titlePrefixWeight,
    titleContainsWeight: current.titleContainsWeight, creatorExactWeight: current.creatorExactWeight,
    creatorMatchWeight: current.creatorMatchWeight, bodyMatchWeight: current.bodyMatchWeight,
    secondaryMatchWeight: current.secondaryMatchWeight, recencyWeight: current.recencyWeight,
    creatorActivityWeight: current.creatorActivityWeight, workTypeBoost: current.workTypeBoost,
    creatorTypeBoost: current.creatorTypeBoost, productTypeBoost: current.productTypeBoost,
    demandTypeBoost: current.demandTypeBoost, expectedVersion: current.version,
  })
}

function resetRiskRuleForm() {
  if (!riskRulePolicy.value) return
  const current = riskRulePolicy.value.current
  Object.assign(riskRuleForm, {
    name: current.name, taskDisputeScore: current.taskDisputeScore, transactionRefundScore: current.transactionRefundScore,
    communityReportScore: current.communityReportScore, mediaRejectionScore: current.mediaRejectionScore,
    accountLinkScore: current.accountLinkScore, accountLinkMinAccounts: current.accountLinkMinAccounts, accountLinkWindowHours: current.accountLinkWindowHours,
    mediumThreshold: current.mediumThreshold, highThreshold: current.highThreshold, criticalThreshold: current.criticalThreshold,
    expectedVersion: current.version,
  })
}

function resetModelRouteForm() {
  const current = modelRoutePolicy.value?.routes[modelRouteMode.value]
  if (!current) return
  Object.assign(modelRouteForm, { providerProfileId: current.providerProfileId, name: current.name, timeoutSeconds: current.timeoutSeconds, maxAttempts: current.maxAttempts, expectedVersion: current.version })
}

async function submitModelRoute() {
  actionLoading.value = true; error.value = ''; success.value = ''
  try {
    modelRoutePolicy.value = await api.adminUpdateModelRoute(modelRouteMode.value, { ...modelRouteForm })
    resetModelRouteForm(); success.value = t('admin.modelRouteUpdated')
  } catch (reason) { error.value = messageFrom(reason) } finally { actionLoading.value = false }
}

async function loadMoreModelRoutes() {
  const mode = modelRouteMode.value
  const cursor = modelRoutePolicy.value?.nextCursors?.[mode]
  if (!cursor || modelRouteLoadingMore.value[mode]) return
  modelRouteLoadingMore.value = { ...modelRouteLoadingMore.value, [mode]: true }
  error.value = ''
  try {
    const page = await api.adminGetModelRoutes({ mode, cursor, limit: 20 })
    if (!modelRoutePolicy.value) return
    const known = new Set((modelRoutePolicy.value.history[mode] || []).map(item => item.id))
    const nextCursors = { ...(modelRoutePolicy.value.nextCursors || {}) }
    if (page.nextCursors?.[mode]) nextCursors[mode] = page.nextCursors[mode]
    else delete nextCursors[mode]
    modelRoutePolicy.value = {
      routes: page.routes,
      history: { ...modelRoutePolicy.value.history, [mode]: [...(modelRoutePolicy.value.history[mode] || []), ...(page.history[mode] || []).filter(item => !known.has(item.id))] },
      nextCursors,
    }
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    modelRouteLoadingMore.value = { ...modelRouteLoadingMore.value, [mode]: false }
  }
}

function resetSystemSettingForm() {
	const current = systemSettings.value; if (!current) return
	Object.assign(systemSettingForm, { registrationsEnabled: current.registrationsEnabled, generationsEnabled: current.generationsEnabled, publishingEnabled: current.publishingEnabled, marketplaceCheckoutEnabled: current.marketplaceCheckoutEnabled, taskCreationEnabled: current.taskCreationEnabled, publicNotice: current.publicNotice })
	Object.assign(siteConfigurationForm, cloneSiteConfiguration(current.siteConfiguration))
	siteConfig.apply(current.siteConfiguration)
}

async function loadSystemSettings() {
	systemSettings.value = await api.adminGetSystemSettings()
}

async function submitSystemSettings() {
  actionLoading.value = true; error.value = ''; success.value = ''
  try {
		systemSettings.value = await api.adminUpdateSystemSettings({ ...systemSettingForm })
		resetSystemSettingForm(); success.value = t('admin.systemSettingsUpdated')
  }
  catch (reason) { error.value = messageFrom(reason) } finally { actionLoading.value = false }
}

async function waitForCleanSiteIcon(asset: Asset) {
  let current = asset
  for (let attempt = 0; attempt < 20; attempt += 1) {
    if (current.scanStatus === 'clean') return current
    if (['review', 'rejected'].includes(current.scanStatus)) throw new Error(t('admin.siteIconRejected'))
    await new Promise(resolve => globalThis.setTimeout(resolve, 800))
    current = await api.getAsset(current.id)
  }
  throw new Error(t('admin.siteIconProcessing'))
}

async function uploadSiteIcon(event: globalThis.Event) {
  const input = event.target as globalThis.HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  siteIconUploading.value = true
  error.value = ''
  try {
    const form = new globalThis.FormData()
    form.append('title', t('admin.siteIconAssetTitle'))
    form.append('file', file)
    const uploaded = await waitForCleanSiteIcon(await api.uploadAsset(form))
    siteConfigurationForm.siteIconUrl = uploaded.mediaUrl
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    siteIconUploading.value = false
    input.value = ''
  }
}

async function submitSiteConfiguration() {
  actionLoading.value = true
  error.value = ''
  success.value = ''
  try {
		const configuration = await api.adminUpdateSiteConfiguration(cloneSiteConfiguration(siteConfigurationForm))
		Object.assign(siteConfigurationForm, cloneSiteConfiguration(configuration))
		siteConfig.apply(configuration)
		if (systemSettings.value) systemSettings.value = { ...systemSettings.value, siteConfiguration: configuration }
		success.value = t('admin.siteConfigurationUpdated')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionLoading.value = false
  }
}

function resetDeveloperControlForm() {
  const current = developerAdminAccess.value?.control; if (!current) return
  Object.assign(developerControlForm, { enabled: current.enabled, maxServiceAccounts: current.maxServiceAccounts, maxActiveKeys: current.maxActiveKeys, defaultTtlDays: current.defaultTtlDays, expectedVersion: current.version })
}

async function submitDeveloperControl() {
  actionLoading.value = true; error.value = ''; success.value = ''
  try {
    const control = await api.adminUpdateDeveloperControl({ ...developerControlForm })
    if (developerAdminAccess.value) developerAdminAccess.value = { ...developerAdminAccess.value, control }
    resetDeveloperControlForm(); success.value = t('admin.developerControlUpdated')
  } catch (reason) { error.value = messageFrom(reason) } finally { actionLoading.value = false }
}

async function adminRevokeDeveloperAccount(id: string, expectedVersion: number) {
  actionLoading.value = true; error.value = ''; success.value = ''
  try {
    await api.adminRevokeDeveloperServiceAccount(id, { expectedVersion })
    developerAdminAccess.value = await api.adminGetDeveloperAccess()
    success.value = t('admin.developerAccountRevoked')
  } catch (reason) { error.value = messageFrom(reason) } finally { actionLoading.value = false }
}

async function adminRevokeDeveloperKey(id: string, expectedVersion: number) {
  actionLoading.value = true; error.value = ''; success.value = ''
  try {
    await api.adminRevokeDeveloperAPIKey(id, { expectedVersion })
    developerAdminAccess.value = await api.adminGetDeveloperAccess()
    success.value = t('admin.developerKeyRevoked')
  } catch (reason) { error.value = messageFrom(reason) } finally { actionLoading.value = false }
}

async function adminReplayWebhook(delivery: DeveloperWebhookDelivery) {
  actionLoading.value = true; error.value = ''; success.value = ''
  try {
    await api.adminReplayWebhookDelivery(delivery.id, { expectedVersion: delivery.version })
		await loadWebhookRecoveryDirectory()
    success.value = t('admin.webhookReplayQueued')
  } catch (reason) { error.value = messageFrom(reason) } finally { actionLoading.value = false }
}

async function adminRecoverEmailAction(item: IdentityEmailAction, operation: 'retry' | 'cancel') {
  actionLoading.value = true; error.value = ''; success.value = ''
  try {
    const input = { expectedVersion: item.version }
    if (operation === 'retry') await api.adminRetryEmailAction(item.id, input)
    else await api.adminCancelEmailAction(item.id, input)
		await loadEmailRecoveryDirectory()
    success.value = t(operation === 'retry' ? 'admin.emailRetryQueued' : 'admin.emailActionCancelled')
  } catch (reason) { error.value = messageFrom(reason) } finally { actionLoading.value = false }
}

async function submitRiskRulePolicy() {
  actionLoading.value = true
  error.value = ''
  success.value = ''
  try {
    riskRulePolicy.value = await api.adminUpdateRiskRules({ ...riskRuleForm })
    riskRuleNextCursor.value = riskRulePolicy.value.nextCursor || null
    resetRiskRuleForm()
    success.value = t('admin.riskRulesUpdated')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionLoading.value = false
  }
}

async function loadRiskRuleHistory(cursor = '') {
  const page = await api.adminGetRiskRules({ cursor: cursor || undefined, limit: 20 })
  if (cursor && riskRulePolicy.value) {
    const known = new Set(riskRulePolicy.value.history.map(item => item.id))
    riskRulePolicy.value = { ...page, history: [...riskRulePolicy.value.history, ...page.history.filter(item => !known.has(item.id))] }
  } else {
    riskRulePolicy.value = page
  }
  riskRuleNextCursor.value = page.nextCursor || null
}

async function loadMoreRiskRuleHistory() {
  if (!riskRuleNextCursor.value || riskRuleLoadingMore.value) return
  riskRuleLoadingMore.value = true; error.value = ''
  try { await loadRiskRuleHistory(riskRuleNextCursor.value) }
  catch (reason) { error.value = messageFrom(reason) }
  finally { riskRuleLoadingMore.value = false }
}

async function submitRankingPolicy() {
  actionLoading.value = true
  error.value = ''
  success.value = ''
  try {
    rankingPolicy.value = rankingActivationMode.value === 'candidate'
      ? await api.adminCreateRankingCandidate({ ...rankingForm })
      : await api.adminUpdateRankingPolicy({ ...rankingForm })
    rankingNextCursor.value = rankingPolicy.value.nextCursor || null
    resetRankingForm()
    success.value = rankingActivationMode.value === 'candidate' ? t('admin.rankingCandidateCreated') : t('admin.rankingUpdated')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionLoading.value = false
  }
}

async function loadRankingHistory(cursor = '') {
  const page = await api.adminGetRankingPolicy({ cursor: cursor || undefined, limit: 20 })
  if (cursor && rankingPolicy.value) {
    const known = new Set(rankingPolicy.value.history.map(item => item.id))
    rankingPolicy.value = { ...page, history: [...rankingPolicy.value.history, ...page.history.filter(item => !known.has(item.id))] }
  } else {
    rankingPolicy.value = page
  }
  rankingNextCursor.value = page.nextCursor || null
}

async function loadMoreRankingHistory() {
  if (!rankingNextCursor.value || rankingLoadingMore.value) return
  rankingLoadingMore.value = true; error.value = ''
  try { await loadRankingHistory(rankingNextCursor.value) }
  catch (reason) { error.value = messageFrom(reason) }
  finally { rankingLoadingMore.value = false }
}

async function loadDiscoveryHistories(kind: 'all' | 'index' | 'evaluation' = 'all', cursor = '') {
  const page = await api.adminGetDiscoveryOperations({
    limit: 20,
    indexCursor: kind === 'index' && cursor ? cursor : undefined,
    evaluationCursor: kind === 'evaluation' && cursor ? cursor : undefined,
  })
  if (kind === 'index' && cursor) {
    const known = new Set(discoveryOperations.value.indexRuns.map(item => item.id))
    discoveryOperations.value = { ...discoveryOperations.value, indexRuns: [...discoveryOperations.value.indexRuns, ...page.indexRuns.filter(item => !known.has(item.id))] }
    indexRunNextCursor.value = page.indexNextCursor || null
  } else if (kind === 'evaluation' && cursor) {
    const known = new Set(discoveryOperations.value.evaluations.map(item => item.id))
    discoveryOperations.value = { ...discoveryOperations.value, evaluations: [...discoveryOperations.value.evaluations, ...page.evaluations.filter(item => !known.has(item.id))] }
    evaluationNextCursor.value = page.evaluationNextCursor || null
  } else {
    discoveryOperations.value = page
    indexRunNextCursor.value = page.indexNextCursor || null
    evaluationNextCursor.value = page.evaluationNextCursor || null
  }
}

async function loadMoreIndexRuns() {
  if (!indexRunNextCursor.value || indexRunLoadingMore.value) return
  indexRunLoadingMore.value = true; error.value = ''
  try { await loadDiscoveryHistories('index', indexRunNextCursor.value) }
  catch (reason) { error.value = messageFrom(reason) }
  finally { indexRunLoadingMore.value = false }
}

async function loadMoreEvaluations() {
  if (!evaluationNextCursor.value || evaluationLoadingMore.value) return
  evaluationLoadingMore.value = true; error.value = ''
  try { await loadDiscoveryHistories('evaluation', evaluationNextCursor.value) }
  catch (reason) { error.value = messageFrom(reason) }
  finally { evaluationLoadingMore.value = false }
}

async function runRankingEvaluation() {
  actionLoading.value = true
  error.value = ''
  success.value = ''
  try {
    await api.adminRunRankingEvaluation()
    await loadDiscoveryHistories()
    success.value = t('admin.rankingEvaluationComplete')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionLoading.value = false
  }
}

async function updateRankingRollout() {
  if (!rankingPolicy.value) return
  actionLoading.value = true
  error.value = ''
  success.value = ''
  try {
    rankingPolicy.value = await api.adminUpdateRankingRollout({
      percent: rolloutForm.percent as 0 | 5 | 10 | 25 | 50 | 100,
      expectedVersion: rankingPolicy.value.rollout.version,
    })
    rankingNextCursor.value = rankingPolicy.value.nextCursor || null
    Object.assign(rolloutForm, { percent: 25 })
    success.value = t('admin.rankingRolloutUpdated')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionLoading.value = false
  }
}

async function analyzeDiscoveryIndex() {
  actionLoading.value = true
  error.value = ''
  success.value = ''
  try {
    await api.adminAnalyzeDiscoveryIndex()
    await loadDiscoveryHistories()
    success.value = t('admin.discoveryIndexAnalyzed')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionLoading.value = false
  }
}

function decimal(value: number) {
  return new Intl.NumberFormat(locale.value, { maximumFractionDigits: 3 }).format(value)
}

function bytes(value: number) {
  return new Intl.NumberFormat(locale.value, { style: 'unit', unit: 'kilobyte', maximumFractionDigits: 1 }).format(value / 1024)
}

function openHoldRelease(item: DataRightsLegalHold) {
  Object.assign(command, { kind: 'holdRelease', id: item.id, title: `@${item.ownerHandle}` })
}

async function openSupport(item: SupportCase) {
  actionLoading.value = true
  error.value = ''
  try {
    selectedSupport.value = await api.adminGetSupportCase(item.id)
    Object.assign(supportReply, { body: '' })
    Object.assign(supportDecision, { status: selectedSupport.value.status === 'open' ? 'in_review' : 'waiting_for_requester', resolutionCode: '' })
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionLoading.value = false
  }
}

function storeSupport(item: SupportCase) {
  selectedSupport.value = item
  supportCases.value = supportCases.value.map(current => current.id === item.id ? item : current)
}

async function submitSupportReply() {
  if (!selectedSupport.value) return
  actionLoading.value = true
  error.value = ''
  success.value = ''
  try {
    const updated = await api.adminReplySupportCase(selectedSupport.value.id, { ...supportReply, expectedVersion: selectedSupport.value.version })
	await loadSupportDirectory()
	storeSupport(updated)
    Object.assign(supportReply, { body: '' })
    success.value = t('admin.commandComplete')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionLoading.value = false
  }
}

async function submitSupportDecision() {
  if (!selectedSupport.value) return
  actionLoading.value = true
  error.value = ''
  success.value = ''
  try {
    const updated = await api.adminUpdateSupportCase(selectedSupport.value.id, {
      status: supportDecision.status as 'open' | 'in_review' | 'waiting_for_requester' | 'resolved' | 'closed',
      resolutionCode: supportDecision.resolutionCode as '' | 'answered' | 'fixed' | 'refund_guidance' | 'content_restricted' | 'no_action' | 'duplicate' | 'withdrawn',
      expectedVersion: selectedSupport.value.version,
    })
	await loadSupportDirectory()
	storeSupport(updated)
    Object.assign(supportDecision, { status: updated.status === 'resolved' ? 'closed' : 'waiting_for_requester', resolutionCode: '' })
    success.value = t('admin.commandComplete')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionLoading.value = false
  }
}

async function submitCommand() {
  if (!command.kind) return
  actionLoading.value = true
  error.value = ''
  success.value = ''
  try {
    if (command.kind === 'user') {
      const updated = await api.adminUpdateUser(command.id, { role: command.role as AdminUser['role'], status: command.status as AdminUser['status'] })
      if (updated.id) await loadUserDirectory()
    } else if (command.kind === 'content') {
      await api.adminUpdateContent(command.id, { status: command.status as 'published' | 'hidden' | 'removed' })
      await loadContentDirectory()
    } else if (command.kind === 'media') {
      await api.adminReviewMedia(command.id, { status: command.status as 'clean' | 'review' | 'rejected' })
      await loadMediaDirectory()
    } else if (command.kind === 'report') {
      await api.adminResolveGovernanceReport(command.id, { outcome: command.outcome as 'no_action' | 'hidden' | 'removed' })
      await loadReportDirectory()
    } else if (command.kind === 'appeal') {
      await api.adminResolveGovernanceAppeal(command.id, { decision: command.decision as 'upheld' | 'denied' })
      await loadAppealDirectory()
    } else if (command.kind === 'generation') {
      await api.adminCancelGeneration(command.id)
      await loadGenerationDirectory()
    } else if (command.kind === 'task') {
      await api.adminResolveTaskDispute(command.id, {
        decision: command.decision as 'release_creator' | 'cancel_without_settlement', expectedVersion: Number(command.status),
      })
      await loadTaskDirectory()
    } else if (command.kind === 'provider') {
      const updated = await api.adminUpdateProvider(command.id, {
        enabled: command.enabled, displayName: command.displayName, modelName: command.modelName,
        description: command.description, estimatedCostCents: command.estimatedCostCents,
      })
      providers.value = providers.value.map((item) => item.id === updated.id ? updated : item)
    } else if (command.kind === 'finance') {
      await api.adminAdjustFinance(command.id, { deltaCents: command.deltaCents, currency: 'USD' })
      await loadFinanceDirectory()
    } else if (command.kind === 'payment') {
      await api.adminRecoverPayment(command.id, { action: command.action as 'retry_transfer' | 'retry_refund', expectedVersion: Number(command.status) })
      await loadPaymentOperations()
    } else if (command.kind === 'paymentEvent') {
      await api.adminReplayPaymentEvent(command.id, { expectedVersion: Number(command.status) })
      await loadPaymentOperations()
    } else if (command.kind === 'paymentDestination') {
      await api.adminUpdatePaymentDestination(command.id, { destinationId: command.destinationID, enabled: command.enabled, expectedVersion: Number(command.status) })
      await Promise.all([loadPaymentOperations(), loadPaymentDestinations()])
    } else if (command.kind === 'risk') {
	  await api.adminReviewRiskSignal(command.id, { decision: command.decision as 'monitor' | 'no_action' | 'escalated', expectedVersion: Number(command.status) })
	  await loadRiskDirectory()
    } else if (command.kind === 'dataRightsHold') {
		await api.adminCreateDataRightsHold({ userId: command.id, authorityReference: command.authorityReference })
		await loadDataRightsDirectories()
    } else if (command.kind === 'holdRelease') {
		await api.adminReleaseDataRightsHold(command.id)
		await loadDataRightsDirectories()
    }
    success.value = t('admin.commandComplete')
    closeCommand()
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionLoading.value = false
  }
}

watch(() => route.query.tab, (tab, previousTab) => {
  if (tab === previousTab) return
  adminPageScroll.value?.scrollTo({ top: 0 })
  if (hasAdminAccess.value) void load()
})

onMounted(() => void initialize())
</script>

<template>
  <Teleport to="#admin-header-slot">
    <div class="admin-topbar-title">
      <h1>{{ t('admin.title') }}</h1>
    </div>
  </Teleport>

  <section class="admin-page content-width">
    <div class="admin-page-body" :class="{ 'has-admin-content': hasAdminAccess }">
      <div v-if="!loading && !hasAdminAccess" class="admin-access-state">
        <ShieldAlert :size="28" /><h2>{{ t('admin.accessRequired') }}</h2><p>{{ t('admin.accessRequiredDetail') }}</p>
        <UiButton v-if="localDemoAvailable" class="command-button primary" variant="primary" @click="useAdminDemo">
          <template #start>
            <ShieldCheck :size="17" />
          </template>{{ t('admin.useAdminDemo') }}
        </UiButton>
        <UiButton v-else as="RouterLink" class="command-button primary" variant="primary" :to="{ path: '/auth', query: { auth: 'login', returnTo: route.fullPath } }">
          <template #start>
            <ShieldCheck :size="17" />
          </template>{{ t('account.signIn') }}
        </UiButton>
      </div>

      <template v-else-if="hasAdminAccess">
        <header class="admin-section-heading">
          <h2>{{ t(`admin.tabs.${activeTab}`) }}</h2>
        </header>
        <div ref="adminPageScroll" class="admin-page-scroll">
          <div v-if="success" class="task-feedback success" role="status">
            <FileCheck2 :size="18" />{{ success }}
          </div>
          <div v-if="error" class="task-feedback error" role="alert">
            <ShieldAlert :size="18" />{{ error }}
          </div>

          <UiDrawer :open="commandDrawerOpen" size="md" :label="`${t('admin.controlledAction')} · ${command.title}`" @update:open="!$event && closeCommand()" @after-close="resetCommand">
            <form v-if="command.kind" ref="commandPanel" class="admin-command-panel admin-edit-drawer" @submit.prevent="submitCommand">
              <header>
                <div><span>{{ t('admin.controlledAction') }}</span><h2>{{ command.title }}</h2></div><UiIconButton class="icon-button" type="button" :label="t('actions.close')" @click="closeCommand">
                  <X :size="17" />
                </UiIconButton>
              </header>
              <div class="admin-edit-drawer-body">
                <div v-if="command.kind === 'user'" class="admin-command-fields">
                  <label>{{ t('admin.role') }}<UiSelect v-model="command.role"><option v-for="role in ['member','creator','publisher','moderator','admin']" :key="role" :value="role">{{ localizedLabel(roleKeys, role) }}</option></UiSelect></label>
                  <label>{{ t('admin.status') }}<UiSelect v-model="command.status"><option v-for="status in ['active','suspended','deleted']" :key="status" :value="status">{{ t(`admin.states.${status}`) }}</option></UiSelect></label>
                </div>
                <label v-if="command.kind === 'content'">{{ t('admin.status') }}<UiSelect v-model="command.status"><option v-for="status in ['published','hidden','removed']" :key="status" :value="status">{{ t(`admin.states.${status}`) }}</option></UiSelect></label>
                <label v-if="command.kind === 'media'">{{ t('admin.scanDecision') }}<UiSelect v-model="command.status"><option v-for="status in ['clean','review','rejected']" :key="status" :value="status">{{ t(`workspace.scanStatus.${status}`) }}</option></UiSelect></label>
                <label v-if="command.kind === 'report'">{{ t('admin.reportOutcome') }}<UiSelect v-model="command.outcome"><option v-for="outcome in ['no_action','hidden','removed']" :key="outcome" :value="outcome">{{ t(`admin.outcomes.${outcome}`) }}</option></UiSelect></label>
                <label v-if="command.kind === 'appeal'">{{ t('admin.appealDecision') }}<UiSelect v-model="command.decision"><option v-for="decision in ['denied','upheld']" :key="decision" :value="decision">{{ t(`admin.appealDecisions.${decision}`) }}</option></UiSelect></label>
                <div v-if="command.kind === 'provider'" class="admin-command-fields provider-edit-fields">
                  <label>{{ t('admin.providerDisplayName') }}<UiInput v-model.trim="command.displayName" type="text" minlength="2" maxlength="120" required /></label>
                  <label>{{ t('admin.providerModel') }}<UiInput v-model.trim="command.modelName" type="text" minlength="1" maxlength="160" required /></label>
                  <label>{{ t('admin.providerEstimatedCost') }}<UiInput v-model.number="command.estimatedCostCents" type="number" min="0" max="1000000" step="1" required /></label>
                  <label class="admin-checkbox"><UiCheckbox v-model="command.enabled" />{{ t('admin.providerEnabled') }}</label>
                  <label class="provider-description-field">{{ t('admin.providerDescription') }}<UiTextarea v-model.trim="command.description" rows="3" minlength="10" maxlength="1000" required /></label>
                  <p class="provider-config-note">
                    <Settings2 :size="15" />{{ t('admin.providerRuntimeNote') }}
                  </p>
                </div>
                <label v-if="command.kind === 'finance'">{{ t('admin.adjustmentCents') }}<UiInput v-model.number="command.deltaCents" type="number" min="-1000000" max="1000000" step="1" required /></label>
                <label v-if="command.kind === 'payment'">{{ t('admin.paymentRecoveryAction') }}<UiSelect v-model="command.action"><option value="retry_transfer">{{ t('admin.retryTransfer') }}</option><option value="retry_refund">{{ t('admin.retryRefund') }}</option></UiSelect></label>
                <template v-if="command.kind === 'paymentDestination'">
                  <label>{{ t('admin.paymentDestinationId') }}<UiInput v-model.trim="command.destinationID" type="text" minlength="6" maxlength="255" pattern="acct_[A-Za-z0-9_]+" required /></label>
                  <label class="admin-checkbox"><UiCheckbox v-model="command.enabled" />{{ t('admin.paymentDestinationVerified') }}</label>
                </template>
                <label v-if="command.kind === 'risk'">{{ t('admin.riskDecision') }}<UiSelect v-model="command.decision"><option v-for="decision in ['monitor','no_action','escalated']" :key="decision" :value="decision">{{ t(`admin.riskDecisions.${decision}`) }}</option></UiSelect></label>
                <label v-if="command.kind === 'task'">{{ t('admin.taskDecision') }}<UiSelect v-model="command.decision"><option v-for="decision in ['cancel_without_settlement','release_creator']" :key="decision" :value="decision">{{ t(`admin.taskDecisions.${decision}`) }}</option></UiSelect></label>
                <label v-if="command.kind === 'dataRightsHold'">{{ t('admin.authorityReference') }}<UiInput v-model.trim="command.authorityReference" minlength="6" maxlength="200" required :placeholder="t('admin.authorityReferencePlaceholder')" /></label>
              </div>
              <footer>
                <UiButton class="command-button secondary" variant="secondary" type="button" @click="closeCommand">
                  {{ t('actions.cancel') }}
                </UiButton>
                <UiButton class="command-button primary" variant="primary" type="submit" :loading="actionLoading">
                  <template #start>
                    <ShieldCheck v-if="!actionLoading" :size="17" />
                  </template>{{ t('admin.applyAction') }}
                </UiButton>
              </footer>
            </form>
          </UiDrawer>

          <div v-if="loading" class="page-state" aria-live="polite">
            {{ t('admin.loading') }}
          </div>

          <div v-else-if="activeTab === 'overview' && overview" class="admin-overview">
            <div class="admin-overview-grid overview-metrics-grid" role="list" :aria-label="t('admin.tabs.overview')">
              <article v-for="key in ['users','works','generations','orders','tasks','risks','providers'] as const" :key="key" role="listitem">
                <span>{{ t(`admin.metrics.${key}`) }}</span>
                <strong>{{ overview[key].total }}</strong>
                <div class="admin-metric-breakdown">
                  <small v-for="(count, status) in overview[key].byStatus" :key="status"><span>{{ overviewStatusLabel(key, status) }}</span><b>{{ count }}</b></small>
                </div>
              </article>
            </div>

            <section v-if="systemSettings" class="site-configuration-workspace">
              <header class="site-configuration-heading">
                <div><span>{{ t('admin.siteConfigurationLabel') }}</span><h2>{{ t('admin.siteConfigurationTitle') }}</h2><p>{{ t('admin.siteConfigurationSummary') }}</p></div>
              </header>
              <UiTabs v-model="siteConfigurationSection" class="site-configuration-tabs" :items="siteConfigurationSections" :label="t('admin.siteConfigurationTitle')" />

              <form class="site-configuration-form" @submit.prevent="submitSiteConfiguration">
                <div v-if="siteConfigurationSection === 'general'" class="site-general-layout">
                  <div class="site-general-fields">
                    <label>{{ t('admin.siteName') }}<UiInput v-model.trim="siteConfigurationForm.siteName" minlength="2" maxlength="80" required /></label>
                    <label>{{ t('admin.serverUrl') }}<UiInput v-model.trim="siteConfigurationForm.serverUrl" type="url" maxlength="2048" :placeholder="t('admin.serverUrlPlaceholder')" required /></label>
                    <fieldset class="site-icon-fieldset">
                      <legend>{{ t('admin.siteIcon') }}</legend>
                      <UiTabs v-model="siteIconMode" :items="iconSourceItems" :label="t('admin.siteIconSource')" />
                      <label v-if="siteIconMode === 'url'">{{ t('admin.siteIconUrl') }}<UiInput v-model.trim="siteConfigurationForm.siteIconUrl" maxlength="2048" :placeholder="t('admin.siteIconUrlPlaceholder')" required /></label>
                      <label v-else>{{ t('admin.siteIconFile') }}<UiFileInput accept="image/jpeg,image/png" :disabled="siteIconUploading" @change="uploadSiteIcon" /><small>{{ siteIconUploading ? t('admin.siteIconUploading') : t('admin.siteIconUploadHint') }}</small></label>
                    </fieldset>
                    <div class="site-footer-fields">
                      <div class="site-markdown-field">
                        <span class="site-markdown-label">{{ t('admin.footerTextEnglish') }}</span>
                        <MarkdownEditor v-model="siteConfigurationForm.footerText.enUS" rows="3" :maxlength="1000" :aria-label="t('admin.footerTextEnglish')" />
                      </div>
                      <div class="site-markdown-field">
                        <span class="site-markdown-label">{{ t('admin.footerTextChinese') }}</span>
                        <MarkdownEditor v-model="siteConfigurationForm.footerText.zhCN" rows="3" :maxlength="1000" :aria-label="t('admin.footerTextChinese')" />
                      </div>
                    </div>
                  </div>
                  <aside class="site-brand-preview" :aria-label="t('admin.sitePreview')">
                    <span>{{ t('admin.sitePreview') }}</span>
                    <div><img :src="siteConfigurationForm.siteIconUrl" alt="" /><strong>{{ siteConfigurationForm.siteName || t('brand') }}</strong></div>
                    <dl><div><dt>{{ t('admin.serverUrl') }}</dt><dd>{{ siteConfigurationForm.serverUrl || '—' }}</dd></div><div><dt>{{ t('admin.footerContent') }}</dt><dd><MarkdownContent :source="locale === 'zh-CN' ? siteConfigurationForm.footerText.zhCN : siteConfigurationForm.footerText.enUS" inline /></dd></div></dl>
                  </aside>
                </div>

                <div v-else class="site-policy-layout">
                  <nav :aria-label="t('admin.sitePolicies')">
                    <UiButton v-for="key in sitePolicyKeys" :key="key" type="button" variant="ghost" :class="{ active: selectedPolicy === key }" @click="selectedPolicy = key">
                      <span>{{ t(`legal.topics.${key}.title`) }}</span><small>{{ t(`legal.topics.${key}.summary`) }}</small>
                    </UiButton>
                  </nav>
                  <section class="site-policy-editor">
                    <header><div><span>{{ t('admin.sitePolicyEditor') }}</span><h3>{{ t(`legal.topics.${selectedPolicy}.title`) }}</h3></div><FileCheck2 :size="18" /></header>
                    <div class="site-markdown-field">
                      <span class="site-markdown-label">{{ t('admin.policyContentEnglish') }}</span>
                      <MarkdownEditor v-model="siteConfigurationForm.policies[selectedPolicy].enUS" rows="10" :maxlength="50000" :aria-label="t('admin.policyContentEnglish')" />
                    </div>
                    <div class="site-markdown-field">
                      <span class="site-markdown-label">{{ t('admin.policyContentChinese') }}</span>
                      <MarkdownEditor v-model="siteConfigurationForm.policies[selectedPolicy].zhCN" rows="10" :maxlength="50000" :aria-label="t('admin.policyContentChinese')" />
                    </div>
                  </section>
                </div>

                <footer class="site-configuration-actions">
                  <p><ShieldCheck :size="15" />{{ t('admin.siteConfigurationSaveNote') }}</p>
                  <UiButton type="submit" variant="primary" :loading="actionLoading" :disabled="siteIconUploading">
                    <template #start>
                      <Save v-if="!actionLoading" :size="16" />
                    </template>{{ t('admin.saveSiteConfiguration') }}
                  </UiButton>
                </footer>
              </form>
            </section>
          </div>

          <div v-else-if="activeTab === 'users'" class="admin-user-directory">
            <form class="admin-user-filters" @submit.prevent="applyUserFilters">
              <label>{{ t('admin.userSearch') }}<UiInput v-model="userQuery" type="search" maxlength="120" :placeholder="t('admin.userSearchPlaceholder')" /></label>
              <label>{{ t('admin.role') }}<UiSelect v-model="userRole"><option value="">{{ t('admin.allRoles') }}</option><option v-for="role in ['member','creator','publisher','moderator','admin']" :key="role" :value="role">{{ localizedLabel(roleKeys, role) }}</option></UiSelect></label>
              <label>{{ t('admin.status') }}<UiSelect v-model="userStatus"><option value="">{{ t('admin.allStatuses') }}</option><option v-for="status in ['active','suspended','deleted']" :key="status" :value="status">{{ t(`admin.states.${status}`) }}</option></UiSelect></label>
              <UiButton class="command-button secondary" variant="secondary" type="submit">
                <template #start>
                  <ListFilter :size="16" />
                </template>{{ t('actions.applyFilters') }}
              </UiButton>
              <UiIconButton class="icon-button" :label="t('actions.clearFilters')" @click="clearUserFilters">
                <X :size="16" />
              </UiIconButton>
            </form>
            <UiTable v-if="users.length" class="admin-user-table-shell" table-class="admin-user-table" :caption="t('admin.userTableCaption')">
              <thead>
                <tr>
                  <th scope="col" class="admin-user-col-identity">
                    {{ t('admin.userIdentity') }}
                  </th>
                  <th scope="col" class="admin-user-col-id">
                    {{ t('admin.userId') }}
                  </th>
                  <th scope="col" class="admin-user-col-status">
                    {{ t('admin.status') }}
                  </th>
                  <th scope="col" class="admin-user-col-role">
                    {{ t('admin.role') }}
                  </th>
                  <th scope="col" class="admin-user-col-locale">
                    {{ t('admin.localeAndTimezone') }}
                  </th>
                  <th scope="col" class="admin-user-col-created">
                    {{ t('admin.createdAt') }}
                  </th>
                  <th scope="col" class="admin-user-col-last-active">
                    {{ t('admin.lastActive') }}
                  </th>
                  <th scope="col" class="admin-user-col-actions">
                    <span class="sr-only">{{ t('admin.userActions') }}</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="item in users" :key="item.id" :data-status="item.status">
                  <td class="admin-user-col-identity">
                    <div class="admin-user-identity">
                      <strong>{{ item.displayName }}</strong>
                      <span>@{{ item.handle }} · {{ item.email }}</span>
                    </div>
                  </td>
                  <td class="admin-user-col-id">
                    <code :title="item.id">{{ shortUserId(item.id) }}</code>
                  </td>
                  <td class="admin-user-col-status">
                    <span class="admin-user-status" :data-status="item.status"><i aria-hidden="true"></i>{{ t(`admin.states.${item.status}`) }}</span>
                  </td>
                  <td class="admin-user-col-role" :title="localizedLabel(roleKeys, item.role)">
                    {{ localizedLabel(roleKeys, item.role) }}
                  </td>
                  <td class="admin-user-col-locale">
                    <span>{{ item.locale }}</span><small>{{ item.timezone }}</small>
                  </td>
                  <td class="admin-user-col-created">
                    <time :datetime="item.createdAt">{{ date(item.createdAt) }}</time>
                  </td>
                  <td class="admin-user-col-last-active">
                    <time v-if="item.lastSeenAt" :datetime="item.lastSeenAt">{{ date(item.lastSeenAt) }}</time><span v-else>{{ t('admin.neverActive') }}</span>
                  </td>
                  <td class="admin-user-col-actions">
                    <UiIconButton size="sm" variant="ghost" :label="t('admin.manage')" @click="openUser(item)">
                      <Pencil :size="15" />
                    </UiIconButton>
                  </td>
                </tr>
              </tbody>
            </UiTable>
            <p v-if="!users.length" class="inline-empty">
              {{ t('admin.noUsers') }}
            </p>
            <UiButton v-if="userNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="userLoadingMore" variant="secondary" @click="loadMoreUsers">
              <LoaderCircle v-if="userLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
            </UiButton>
          </div>

          <div v-else-if="activeTab === 'content'" class="admin-content-directory">
            <form class="admin-user-filters" @submit.prevent="applyContentFilters">
              <label>{{ t('admin.contentSearch') }}<UiInput v-model="contentQuery" type="search" maxlength="120" :placeholder="t('admin.contentSearchPlaceholder')" /></label>
              <label>{{ t('admin.resourceType') }}<UiSelect v-model="contentType"><option value="">{{ t('admin.allContentTypes') }}</option><option value="work">{{ localizedLabel(resourceTypeKeys, 'work') }}</option></UiSelect></label>
              <label>{{ t('admin.status') }}<UiSelect v-model="contentStatus"><option value="">{{ t('admin.allStatuses') }}</option><option v-for="status in ['draft','published','hidden','removed']" :key="status" :value="status">{{ t(`admin.states.${status}`) }}</option></UiSelect></label>
              <UiButton class="command-button secondary" type="submit" variant="secondary">
                <ListFilter :size="16" />{{ t('actions.applyFilters') }}
              </UiButton>
              <UiIconButton class="icon-button" type="button" :title="t('actions.clearFilters')" :label="t('actions.clearFilters')" @click="clearContentFilters">
                <X :size="16" />
              </UiIconButton>
            </form>
            <div class="admin-list">
              <article v-for="item in content" :key="item.id">
                <div><strong>{{ item.title }}</strong><span>@{{ item.authorHandle }} · {{ item.aiDisclosure }}</span></div><span>{{ localizedLabel(resourceTypeKeys, item.resourceType) }}</span><span :data-status="item.status">{{ t(`admin.states.${item.status}`) }}</span><small>{{ date(item.updatedAt) }}</small><UiButton class="command-button secondary" type="button" variant="secondary" @click="openContent(item)">
                  <ShieldCheck :size="16" />{{ t('admin.review') }}
                </UiButton>
              </article>
            </div>
            <p v-if="!content.length" class="inline-empty">
              {{ t('admin.noContent') }}
            </p>
            <UiButton v-if="contentNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="contentLoadingMore" variant="secondary" @click="loadMoreContent">
              <LoaderCircle v-if="contentLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
            </UiButton>
          </div>

          <div v-else-if="activeTab === 'media'" class="admin-media-directory">
            <form class="admin-user-filters" @submit.prevent="applyMediaFilters">
              <label>{{ t('admin.mediaSearch') }}<UiInput v-model="mediaQuery" type="search" maxlength="120" :placeholder="t('admin.mediaSearchPlaceholder')" /></label>
              <label>{{ t('admin.mediaType') }}<UiSelect v-model="mediaKind"><option value="">{{ t('admin.allMediaTypes') }}</option><option v-for="kind in ['image','video','audio','document','prompt','workflow']" :key="kind" :value="kind">{{ localizedLabel(mediaKindKeys, kind) }}</option></UiSelect></label>
              <label>{{ t('admin.scanDecision') }}<UiSelect v-model="mediaStatus"><option value="">{{ t('admin.allScanStatuses') }}</option><option v-for="status in ['pending','clean','review','rejected']" :key="status" :value="status">{{ t(`workspace.scanStatus.${status}`) }}</option></UiSelect></label>
              <UiButton class="command-button secondary" type="submit" variant="secondary">
                <ListFilter :size="16" />{{ t('actions.applyFilters') }}
              </UiButton>
              <UiIconButton class="icon-button" type="button" :title="t('actions.clearFilters')" :label="t('actions.clearFilters')" @click="clearMediaFilters">
                <X :size="16" />
              </UiIconButton>
            </form>
            <div class="admin-list media-admin-list">
              <article v-for="item in mediaItems" :key="item.id">
                <div><strong>{{ item.title }}</strong><span>@{{ item.ownerHandle }} · {{ item.uploadedFilename || item.mimeType }}</span><small>{{ item.scanReason || t('workspace.scanPendingDetail') }}</small></div><span>{{ localizedLabel(mediaKindKeys, item.kind) }}</span><span :data-status="item.scanStatus">{{ t(`workspace.scanStatus.${item.scanStatus}`) }}</span><small>{{ date(item.scannedAt || item.createdAt) }}</small><UiButton class="command-button secondary" type="button" variant="secondary" @click="openMedia(item)">
                  <ShieldCheck :size="16" />{{ t('admin.review') }}
                </UiButton>
              </article>
            </div>
            <p v-if="!mediaItems.length" class="inline-empty">
              {{ t('admin.noMedia') }}
            </p>
            <UiButton v-if="mediaNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="mediaLoadingMore" variant="secondary" @click="loadMoreMedia">
              <LoaderCircle v-if="mediaLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
            </UiButton>
          </div>

          <div v-else-if="activeTab === 'governance'" class="admin-governance">
            <section>
              <header><div><h2>{{ t('admin.reportQueue') }}</h2><p>{{ t('admin.reportQueueSummary') }}</p></div><span>{{ governanceReports.filter(item => ['open','reviewing'].includes(item.status)).length }}</span></header>
              <form class="admin-user-filters admin-governance-filters" @submit.prevent="applyReportFilters">
                <label>{{ t('admin.reportSearch') }}<UiInput v-model="reportQuery" type="search" maxlength="120" :placeholder="t('admin.reportSearchPlaceholder')" /></label>
                <label>{{ t('admin.resourceType') }}<UiSelect v-model="reportType"><option value="">{{ t('admin.allResourceTypes') }}</option><option v-for="type in ['work','post','comment']" :key="type" :value="type">{{ localizedLabel(resourceTypeKeys, type) }}</option></UiSelect></label>
                <label>{{ t('admin.reportCategory') }}<UiSelect v-model="reportCategory"><option value="">{{ t('admin.allReportCategories') }}</option><option v-for="category in ['spam','harassment','copyright','sexual','violence','misleading','other']" :key="category" :value="category">{{ t(`community.reportCategories.${category}`) }}</option></UiSelect></label>
                <label>{{ t('admin.status') }}<UiSelect v-model="reportStatus"><option value="">{{ t('admin.allStatuses') }}</option><option v-for="status in ['open','reviewing','resolved','dismissed']" :key="status" :value="status">{{ t(`community.reportStates.${status}`) }}</option></UiSelect></label>
                <UiButton class="command-button secondary" type="submit" variant="secondary">
                  <ListFilter :size="16" />{{ t('actions.applyFilters') }}
                </UiButton>
                <UiIconButton class="icon-button" type="button" :title="t('actions.clearFilters')" :label="t('actions.clearFilters')" @click="clearReportFilters">
                  <X :size="16" />
                </UiIconButton>
              </form>
              <div v-if="governanceReports.length" class="admin-list governance-admin-list">
                <article v-for="item in governanceReports" :key="item.id">
                  <div><strong>{{ item.resourceTitle }}</strong><span>@{{ item.reporterHandle }} → @{{ item.subjectHandle }} · {{ t(`community.reportCategories.${item.category}`) }}</span><small>{{ item.details }}</small></div><span>{{ localizedLabel(resourceTypeKeys, item.resourceType) }}</span><span :data-status="item.status">{{ t(`community.reportStates.${item.status}`) }}</span><small>{{ date(item.createdAt) }}</small><UiButton v-if="['open','reviewing'].includes(item.status)" class="command-button secondary" type="button" variant="secondary" @click="openReport(item)">
                    <ShieldCheck :size="16" />{{ t('admin.resolve') }}
                  </UiButton><span v-else>{{ item.outcome ? t(`admin.outcomes.${item.outcome}`) : '' }}</span>
                </article>
              </div>
              <p v-else class="inline-empty">
                {{ t('admin.noReports') }}
              </p>
              <UiButton v-if="reportNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="reportLoadingMore" variant="secondary" @click="loadMoreReports">
                <LoaderCircle v-if="reportLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
              </UiButton>
            </section>
            <section>
              <header><div><h2>{{ t('admin.appealQueue') }}</h2><p>{{ t('admin.appealQueueSummary') }}</p></div><span>{{ governanceAppeals.filter(item => item.status === 'pending').length }}</span></header>
              <form class="admin-user-filters" @submit.prevent="applyAppealFilters">
                <label>{{ t('admin.appealSearch') }}<UiInput v-model="appealQuery" type="search" maxlength="120" :placeholder="t('admin.appealSearchPlaceholder')" /></label>
                <label>{{ t('admin.resourceType') }}<UiSelect v-model="appealType"><option value="">{{ t('admin.allResourceTypes') }}</option><option v-for="type in ['work','post','comment']" :key="type" :value="type">{{ localizedLabel(resourceTypeKeys, type) }}</option></UiSelect></label>
                <label>{{ t('admin.status') }}<UiSelect v-model="appealStatus"><option value="">{{ t('admin.allStatuses') }}</option><option v-for="status in ['pending','upheld','denied']" :key="status" :value="status">{{ t(`community.appealStates.${status}`) }}</option></UiSelect></label>
                <UiButton class="command-button secondary" type="submit" variant="secondary">
                  <ListFilter :size="16" />{{ t('actions.applyFilters') }}
                </UiButton>
                <UiIconButton class="icon-button" type="button" :title="t('actions.clearFilters')" :label="t('actions.clearFilters')" @click="clearAppealFilters">
                  <X :size="16" />
                </UiIconButton>
              </form>
              <div v-if="governanceAppeals.length" class="admin-list governance-admin-list">
                <article v-for="item in governanceAppeals" :key="item.id">
                  <div><strong>{{ item.resourceTitle }}</strong><span>@{{ item.appellantHandle }} · {{ localizedLabel(resourceTypeKeys, item.resourceType) }}</span><small>{{ item.reason }}</small></div><span>{{ localizedLabel(resourceTypeKeys, item.resourceType) }}</span><span :data-status="item.status">{{ t(`community.appealStates.${item.status}`) }}</span><small>{{ date(item.createdAt) }}</small><UiButton v-if="item.status === 'pending'" class="command-button secondary" type="button" variant="secondary" @click="openAppeal(item)">
                    <Undo2 :size="16" />{{ t('admin.resolve') }}
                  </UiButton><span v-else>{{ t(`admin.appealDecisions.${item.status}`) }}</span>
                </article>
              </div>
              <p v-else class="inline-empty">
                {{ t('admin.noAppeals') }}
              </p>
              <UiButton v-if="appealNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="appealLoadingMore" variant="secondary" @click="loadMoreAppeals">
                <LoaderCircle v-if="appealLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
              </UiButton>
            </section>
          </div>

          <div v-else-if="activeTab === 'support'" class="admin-user-directory">
            <form class="admin-user-filters admin-operations-filters" @submit.prevent="applySupportFilters">
              <label>{{ t('admin.supportSearch') }}<UiInput v-model="supportQuery" type="search" maxlength="120" :placeholder="t('admin.supportSearchPlaceholder')" /></label>
              <label>{{ t('admin.status') }}<UiSelect v-model="supportStatus"><option value="">{{ t('admin.allSupportStatuses') }}</option><option v-for="status in ['open','in_review','waiting_for_requester','resolved','closed']" :key="status" :value="status">{{ t(`support.statuses.${status}`) }}</option></UiSelect></label>
              <label>{{ t('admin.supportCategory') }}<UiSelect v-model="supportCategory"><option value="">{{ t('admin.allSupportCategories') }}</option><option v-for="category in ['general_support','billing','account','task_or_order','copyright']" :key="category" :value="category">{{ t(`support.categories.${category}`) }}</option></UiSelect></label>
              <UiButton class="command-button primary" type="submit" variant="primary">
                <ListFilter :size="16" />{{ t('actions.applyFilters') }}
              </UiButton>
              <UiIconButton class="icon-button" type="button" :title="t('actions.clearFilters')" :label="t('actions.clearFilters')" @click="clearSupportFilters">
                <Undo2 :size="16" />
              </UiIconButton>
            </form>
            <div class="admin-support-layout">
              <section class="admin-support-queue">
                <header><div><h2>{{ t('admin.supportQueue') }}</h2><p>{{ t('admin.supportQueueSummary') }}</p></div><span>{{ supportCases.filter(item => !['resolved','closed'].includes(item.status)).length }}</span></header>
                <UiButton v-for="item in supportCases" :key="item.id" variant="ghost" :content-wrapper="false" :class="{ active: item.id === selectedSupport?.id }" @click="openSupport(item)">
                  <span><strong>{{ item.subject }}</strong><small>@{{ item.requesterHandle }} · {{ t(`support.categories.${item.category}`) }}</small></span><span><em :data-status="item.status">{{ t(`support.statuses.${item.status}`) }}</em><small>{{ date(item.updatedAt) }}</small></span>
                </UiButton>
                <p v-if="!supportCases.length" class="inline-empty">
                  {{ t('admin.noSupportCases') }}
                </p>
                <UiButton v-if="supportNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="supportLoadingMore" variant="secondary" @click="loadMoreSupport">
                  <LoaderCircle v-if="supportLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
                </UiButton>
              </section>

              <section v-if="selectedSupport" class="admin-support-detail">
                <header><div><span>{{ t('support.caseReference', { id: selectedSupport.id.slice(0, 8), version: selectedSupport.version }) }}</span><h2>{{ selectedSupport.subject }}</h2><p>@{{ selectedSupport.requesterHandle }} · {{ t(`support.categories.${selectedSupport.category}`) }}</p></div><em :data-status="selectedSupport.status">{{ t(`support.statuses.${selectedSupport.status}`) }}</em></header>
                <dl class="support-evidence">
                  <div><dt>{{ t('support.opened') }}</dt><dd>{{ date(selectedSupport.createdAt) }}</dd></div><div><dt>{{ t('support.assignedTo') }}</dt><dd>{{ selectedSupport.assignedOperatorHandle ? `@${selectedSupport.assignedOperatorHandle}` : t('support.unassigned') }}</dd></div>
                  <div v-if="selectedSupport.relatedResourceId">
                    <dt>{{ t('support.relatedResource') }}</dt><dd>{{ localizedLabel(resourceTypeKeys, selectedSupport.relatedResourceType || '') }} · {{ selectedSupport.relatedResourceId }}</dd>
                  </div><div v-if="selectedSupport.claimantRelationship">
                    <dt>{{ t('support.claimantRelationship') }}</dt><dd>{{ t(`support.relationships.${selectedSupport.claimantRelationship}`) }}</dd>
                  </div>
                </dl>
                <div class="support-thread admin-support-thread">
                  <article v-for="message in selectedSupport.messages" :key="message.id" :class="message.authorRole">
                    <header><strong>{{ message.authorRole === 'operator' ? `@${message.authorHandle || t('support.supportTeam')}` : `@${message.authorHandle || selectedSupport.requesterHandle}` }}</strong><span>{{ date(message.createdAt) }}</span></header><p>{{ message.body }}</p>
                  </article>
                </div>

                <div v-if="supportStatusOptions.length" class="admin-support-controls">
                  <form @submit.prevent="submitSupportReply">
                    <h3><MessageSquare :size="17" />{{ t('admin.replyToCase') }}</h3>
                    <label>{{ t('admin.replyBody') }}<UiTextarea v-model.trim="supportReply.body" rows="4" minlength="2" maxlength="4000" required /></label>
                    <UiButton class="command-button secondary" type="submit" :disabled="actionLoading" variant="secondary">
                      <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><Send v-else :size="17" />{{ t('admin.sendReply') }}
                    </UiButton>
                  </form>
                  <form @submit.prevent="submitSupportDecision">
                    <h3><ShieldCheck :size="17" />{{ t('admin.updateCase') }}</h3>
                    <label>{{ t('admin.status') }}<UiSelect v-model="supportDecision.status"><option v-for="status in supportStatusOptions" :key="status" :value="status">{{ t(`support.statuses.${status}`) }}</option></UiSelect></label>
                    <label v-if="['resolved','closed'].includes(supportDecision.status)">{{ t('admin.resolutionCode') }}<UiSelect v-model="supportDecision.resolutionCode" required><option value="" disabled>{{ t('admin.chooseResolution') }}</option><option v-for="code in ['answered','fixed','refund_guidance','content_restricted','no_action','duplicate','withdrawn']" :key="code" :value="code">{{ t(`support.resolutions.${code}`) }}</option></UiSelect></label>
                    <UiButton class="command-button primary" type="submit" :disabled="actionLoading" variant="primary">
                      <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><ShieldCheck v-else :size="17" />{{ t('admin.applyAction') }}
                    </UiButton>
                  </form>
                </div>
              </section>
              <section v-else class="admin-support-detail support-welcome">
                <Headphones :size="28" /><h2>{{ t('admin.selectSupportTitle') }}</h2><p>{{ t('admin.selectSupportSummary') }}</p>
              </section>
            </div>
          </div>

          <div v-else-if="activeTab === 'generations'" class="admin-user-directory">
            <form class="admin-user-filters admin-operations-filters" @submit.prevent="applyGenerationFilters">
              <label>{{ t('admin.generationSearch') }}<UiInput v-model="generationQuery" type="search" maxlength="120" :placeholder="t('admin.generationSearchPlaceholder')" /></label>
              <label>{{ t('admin.creationMode') }}<UiSelect v-model="generationMode"><option value="">{{ t('admin.allGenerationModes') }}</option><option v-for="mode in ['chat','image','video','music']" :key="mode" :value="mode">{{ t(`create.modes.${mode}`) }}</option></UiSelect></label>
              <label>{{ t('admin.generationStatus') }}<UiSelect v-model="generationStatus"><option value="">{{ t('admin.allGenerationStatuses') }}</option><option v-for="status in ['queued','running','succeeded','failed','cancelled']" :key="status" :value="status">{{ t(`generation.status.${status}`) }}</option></UiSelect></label>
              <UiButton class="command-button primary" type="submit" variant="primary">
                <ListFilter :size="16" />{{ t('actions.applyFilters') }}
              </UiButton>
              <UiIconButton class="icon-button" type="button" :title="t('actions.clearFilters')" :label="t('actions.clearFilters')" @click="clearGenerationFilters">
                <Undo2 :size="16" />
              </UiIconButton>
            </form>
            <div v-if="generations.length" class="admin-list">
              <article v-for="item in generations" :key="item.id">
                <div><strong>{{ item.prompt }}</strong><span>@{{ item.ownerHandle }} · {{ item.modelName }}</span></div><span>{{ formatCurrency(item.chargedCostCents || item.estimatedCostCents, 'USD', locale) }}</span><span :data-status="item.status">{{ t(`generation.status.${item.status}`) }}</span><small>{{ date(item.createdAt) }}</small><UiButton v-if="['queued','running'].includes(item.status)" class="command-button secondary" type="button" variant="secondary" @click="openGeneration(item)">
                  <Ban :size="16" />{{ t('actions.cancel') }}
                </UiButton><span v-else></span>
              </article>
            </div>
            <div v-else class="workspace-empty">
              <WandSparkles :size="22" /><p>{{ t('admin.noGenerationOperations') }}</p>
            </div>
            <UiButton v-if="generationNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="generationLoadingMore" variant="secondary" @click="loadMoreGenerations">
              <LoaderCircle v-if="generationLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
            </UiButton>
          </div>

          <div v-else-if="activeTab === 'tasks'" class="admin-governance task-operations-admin">
            <section>
              <header><div><h2>{{ t('admin.taskOperationsQueue') }}</h2><p>{{ t('admin.taskOperationsSummary') }}</p></div><span>{{ taskOperations.filter(item => item.disputeStatus === 'open').length }}</span></header>
              <form class="admin-user-filters admin-task-filters" @submit.prevent="applyTaskFilters">
                <label>{{ t('admin.taskSearch') }}<UiInput v-model="taskQuery" type="search" maxlength="120" :placeholder="t('admin.taskSearchPlaceholder')" /></label>
                <label>{{ t('admin.taskStatus') }}<UiSelect v-model="taskStatus"><option value="">{{ t('admin.allTaskStatuses') }}</option><option v-for="status in ['draft','open','assigned','submitted','revision','accepted','disputed','cancelled']" :key="status" :value="status">{{ t(`admin.taskStates.${status}`) }}</option></UiSelect></label>
                <label>{{ t('admin.taskDisputeStatus') }}<UiSelect v-model="taskDisputeStatus"><option value="">{{ t('admin.allTaskDisputeStatuses') }}</option><option value="none">{{ t('admin.noTaskDispute') }}</option><option v-for="status in ['open','resolved_creator','resolved_client']" :key="status" :value="status">{{ t(`admin.taskDisputeStates.${status}`) }}</option></UiSelect></label>
                <UiButton class="command-button primary" type="submit" variant="primary">
                  <ListFilter :size="16" />{{ t('actions.applyFilters') }}
                </UiButton>
                <UiIconButton class="icon-button" type="button" :title="t('actions.clearFilters')" :label="t('actions.clearFilters')" @click="clearTaskFilters">
                  <Undo2 :size="16" />
                </UiIconButton>
              </form>
              <div v-if="taskOperations.length" class="admin-list task-operations-list">
                <article v-for="item in taskOperations" :key="item.id">
                  <div>
                    <RouterLink class="text-link" :to="`/market/demands/${item.id}`">
                      {{ item.title }}
                    </RouterLink><span>@{{ item.clientHandle }} → {{ item.assigneeHandle ? `@${item.assigneeHandle}` : t('admin.unassignedCreator') }}</span><small>{{ item.disputeReason || t('admin.noTaskDispute') }}</small>
                  </div>
                  <span>{{ formatCurrency(item.amountCents, item.currency, locale) }}<small v-if="item.latestDeliveryVersion"> · v{{ item.latestDeliveryVersion }}</small></span>
                  <span :data-status="item.status">{{ t(`admin.taskStates.${item.status}`) }}</span>
                  <small>{{ t('admin.taskDeadline', { date: date(item.deadline) }) }}<template v-if="item.riskStatus"> · {{ t(`admin.riskStatuses.${item.riskStatus}`) }}</template></small>
                  <UiButton v-if="item.disputeStatus === 'open'" class="command-button secondary" type="button" variant="secondary" @click="openTaskOperation(item)">
                    <ShieldAlert :size="16" />{{ t('admin.resolveTask') }}
                  </UiButton><span v-else-if="item.disputeStatus">{{ t(`admin.taskDisputeStates.${item.disputeStatus}`) }}</span><span v-else></span>
                </article>
              </div>
              <div v-else class="workspace-empty">
                <BriefcaseBusiness :size="22" /><p>{{ t('admin.noTaskOperations') }}</p>
              </div>
              <UiButton v-if="taskNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="taskLoadingMore" variant="secondary" @click="loadMoreTasks">
                <LoaderCircle v-if="taskLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
              </UiButton>
            </section>
          </div>

          <div v-else-if="activeTab === 'providers'" class="provider-registry-admin">
            <header class="provider-registry-header">
              <div><h2>{{ t('admin.providerRegistryTitle') }}</h2><p>{{ t('admin.providerRegistrySummary') }}</p></div>
              <div class="provider-registry-actions">
                <UiButton class="command-button secondary" type="button" variant="secondary" @click="openNewProviderConfig">
                  <Plus :size="16" />{{ t('admin.addProvider') }}
                </UiButton>
                <UiButton class="command-button primary" type="button" :disabled="!providerConfigs.length" variant="primary" @click="openNewProviderModel">
                  <Plus :size="16" />{{ t('admin.addModel') }}
                </UiButton>
              </div>
            </header>
            <UiTable v-if="providerConfigs.length" class="provider-model-table-wrap" table-class="provider-model-table">
              <thead>
                <tr>
                  <th scope="col">
                    {{ t('admin.providerTableProvider') }}
                  </th>
                  <th scope="col">
                    {{ t('admin.providerTableModel') }}
                  </th>
                  <th scope="col">
                    {{ t('admin.providerCapabilities') }}
                  </th>
                  <th scope="col">
                    {{ t('admin.providerPointPricing') }}
                  </th>
                  <th scope="col">
                    {{ t('admin.status') }}
                  </th>
                  <th scope="col">
                    <span class="sr-only">{{ t('admin.providerTableActions') }}</span>
                  </th>
                </tr>
              </thead>
              <tbody v-for="item in providerConfigs" :key="item.id">
                <tr v-for="model in item.models" :key="model.id">
                  <td class="provider-table-provider-cell">
                    <UiButton class="provider-table-name" variant="ghost" :content-wrapper="false" @click="editProviderConfig(item)">
                      <span>{{ item.name }}</span><Pencil :size="13" />
                    </UiButton>
                    <small>{{ providerProtocolLabel(item.protocol) }}</small>
                    <span :data-status="item.adminEnabled && item.credentialConfigured ? 'active' : 'suspended'">{{ item.credentialConfigured ? t('admin.providerCredentialConfigured') : t('admin.providerCredentialMissing') }}</span>
                  </td>
                  <td class="provider-table-model-cell">
                    <strong>{{ model.displayName }}</strong>
                    <span>{{ model.modelName }}</span>
                    <small v-if="model.description">{{ model.description }}</small>
                  </td>
                  <td>
                    <div class="provider-capability-badges" :title="providerModelCapabilitySummary(model)">
                      <span v-for="badge in providerModelCapabilityBadges(model)" :key="badge">{{ badge }}</span>
                    </div>
                  </td>
                  <td class="provider-table-price">
                    {{ providerPointPricingSummary(model) }}
                  </td>
                  <td>
                    <div class="provider-table-status">
                      <UiSwitch class="provider-status-switch" :class="{ 'is-init': initializedProviderSwitches.has(model.id) }" :model-value="model.adminEnabled" :disabled="actionLoading || !item.adminEnabled" :label="t('admin.providerModelStatus', { name: model.displayName })" :title="model.adminEnabled ? t('admin.available') : t('admin.unavailable')" @update:model-value="toggleProviderModelStatus(model)" />
                      <small>{{ model.adminEnabled && item.adminEnabled ? t('admin.available') : t('admin.unavailable') }}</small>
                    </div>
                  </td>
                  <td>
                    <div class="provider-table-actions">
                      <UiIconButton class="icon-button" type="button" :title="t('admin.editModel')" :label="t('admin.editModel')" @click="editProviderModel(item, model)">
                        <Pencil :size="15" />
                      </UiIconButton>
                      <UiIconButton class="icon-button provider-delete-action" type="button" :title="t('admin.archiveModel')" :label="t('admin.archiveModel')" @click="archiveProviderModel(model)">
                        <Trash2 :size="15" />
                      </UiIconButton>
                    </div>
                  </td>
                </tr>
                <tr v-if="!item.models.length" class="provider-empty-model-row">
                  <td class="provider-table-provider-cell">
                    <UiButton class="provider-table-name" variant="ghost" :content-wrapper="false" @click="editProviderConfig(item)">
                      <span>{{ item.name }}</span><Pencil :size="13" />
                    </UiButton>
                    <small>{{ item.protocol }}</small>
                    <span :data-status="item.adminEnabled && item.credentialConfigured ? 'active' : 'suspended'">{{ item.credentialConfigured ? t('admin.providerCredentialConfigured') : t('admin.providerCredentialMissing') }}</span>
                  </td>
                  <td colspan="4">
                    <span class="provider-table-empty">{{ t('admin.noProviderModels') }}</span>
                  </td>
                  <td>
                    <UiIconButton class="icon-button" type="button" :title="t('admin.addModel')" :label="t('admin.addModel')" @click="openProviderModel(item)">
                      <Plus :size="15" />
                    </UiIconButton>
                  </td>
                </tr>
              </tbody>
            </UiTable>
            <div v-else class="workspace-empty">
              <SlidersHorizontal :size="22" /><p>{{ t('admin.noProviderConfigs') }}</p>
            </div>
            <section v-if="legacyProviderProfiles.length" class="provider-legacy-list">
              <header><div><h2>{{ t('admin.legacyProviderTitle') }}</h2><p>{{ t('admin.legacyProviderSummary') }}</p></div></header>
              <article v-for="item in legacyProviderProfiles" :key="item.id" class="provider-legacy-row">
                <div><strong>{{ item.displayName }}</strong><span>{{ t(`create.modes.${item.mode}`) }} · {{ item.provider }} · {{ item.modelName }}</span></div>
                <span :data-status="item.adminEnabled ? 'active' : 'suspended'">{{ item.adminEnabled ? t('admin.available') : t('admin.unavailable') }}</span>
                <UiButton class="command-button secondary" type="button" variant="secondary" @click="openLegacyProvider(item)">
                  <Settings2 :size="15" />{{ t('admin.editProvider') }}
                </UiButton>
              </article>
            </section>
            <UiDrawer :open="providerConfigEditorOpen" size="lg" :label="providerConfigForm.id ? t('admin.editProviderConfig') : t('admin.addProvider')" @update:open="!$event && closeProviderEditors()">
              <section class="provider-editor-modal" aria-labelledby="provider-editor-title">
                <header>
                  <div>
                    <span>{{ providerConfigForm.id ? t('admin.editProviderConfig') : t('admin.addProvider') }}</span><h2 id="provider-editor-title">
                      {{ providerConfigForm.id ? providerConfigForm.name : t('admin.newProvider') }}
                    </h2>
                  </div>
                  <UiIconButton class="icon-button" type="button" :label="t('actions.close')" @click="closeProviderEditors">
                    <X :size="17" />
                  </UiIconButton>
                </header>
                <form @submit.prevent="submitProviderConfig">
                  <div class="provider-editor-fields">
                    <label>{{ t('admin.providerName') }}<UiInput v-model.trim="providerConfigForm.name" minlength="2" maxlength="120" required /></label>
                    <label>{{ t('admin.providerProtocol') }}<UiSelect v-model="providerConfigForm.protocol"><option value="openai_responses">{{ t('admin.providerProtocols.openaiResponses') }}</option><option value="openai_chat_completions">{{ t('admin.providerProtocols.openaiChatCompletions') }}</option><option value="openai_images">{{ t('admin.providerProtocols.openaiImages') }}</option><option value="hctopup_async_image">{{ t('admin.providerProtocols.hctopupAsyncImage') }}</option><option value="custom">{{ t('admin.providerProtocols.custom') }}</option></UiSelect></label>
                    <label class="provider-editor-wide">{{ t('admin.providerEndpoint') }}<UiInput v-model.trim="providerConfigForm.endpoint" type="url" :placeholder="t('admin.providerEndpointPlaceholder')" required /></label>
                    <label class="provider-editor-wide">{{ t('admin.providerApiKey') }}<UiInput v-model="providerConfigForm.apiKey" type="password" autocomplete="new-password" :placeholder="providerConfigForm.id ? t('admin.providerApiKeyKeep') : t('admin.providerApiKeyRequired')" :required="!providerConfigForm.id" /></label>
                    <label class="admin-checkbox provider-editor-wide"><UiCheckbox v-model="providerConfigForm.adminEnabled" />{{ t('admin.providerEnabled') }}</label>
                  </div>
                  <div v-if="providerConfigForm.id && editingProviderConfig" class="provider-sync-panel">
                    <div><strong>{{ t('admin.syncProviderModels') }}</strong><span>{{ t('admin.providerSyncSummary') }}</span></div>
                    <UiSelect :value="providerSyncMode(editingProviderConfig)" :aria-label="t('admin.providerSyncMode')" @change="setProviderSyncMode(providerConfigForm.id, $event)">
                      <option v-for="mode in ['chat', 'image', 'video', 'music']" :key="mode" :value="mode">
                        {{ t(`create.modes.${mode}`) }}
                      </option>
                    </UiSelect>
                    <UiButton class="command-button secondary" type="button" :disabled="actionLoading" variant="secondary" @click="syncEditingProviderModels">
                      <RefreshCw :size="15" />{{ t('admin.syncProviderModels') }}
                    </UiButton>
                  </div>
                  <footer class="provider-modal-actions">
                    <UiButton v-if="providerConfigForm.id" class="command-button provider-delete-button" type="button" :disabled="actionLoading" variant="primary" @click="archiveEditingProviderConfig">
                      <Trash2 :size="15" />{{ t('admin.archiveProvider') }}
                    </UiButton>
                    <span></span>
                    <UiButton class="command-button secondary" type="button" variant="secondary" @click="closeProviderEditors">
                      {{ t('actions.cancel') }}
                    </UiButton>
                    <UiButton class="command-button primary" type="submit" :disabled="actionLoading" variant="primary">
                      <LoaderCircle v-if="actionLoading" class="spin" :size="16" /><ShieldCheck v-else :size="16" />{{ t('admin.saveProvider') }}
                    </UiButton>
                  </footer>
                </form>
              </section>
            </UiDrawer>
            <UiDrawer :open="providerModelEditorOpen" size="xl" :label="providerModelForm.id ? t('admin.editModel') : t('admin.addModel')" @update:open="!$event && closeProviderEditors()">
              <section class="provider-editor-modal provider-model-editor-modal" aria-labelledby="provider-model-editor-title">
                <header>
                  <div>
                    <span>{{ providerModelForm.id ? t('admin.editModel') : t('admin.addModel') }}</span><h2 id="provider-model-editor-title">
                      {{ providerModelForm.id ? providerModelForm.displayName : t('admin.newModel') }}
                    </h2>
                  </div>
                  <UiIconButton class="icon-button" type="button" :label="t('actions.close')" @click="closeProviderEditors">
                    <X :size="17" />
                  </UiIconButton>
                </header>
                <form @submit.prevent="submitProviderModel">
                  <div class="provider-editor-fields">
                    <label>{{ t('admin.providerTableProvider') }}<UiSelect v-model="providerModelForm.providerId" :disabled="Boolean(providerModelForm.id)" required><option v-for="provider in providerConfigs" :key="provider.id" :value="provider.id">{{ provider.name }}</option></UiSelect></label>
                    <label>{{ t('admin.providerModelType') }}<UiSelect v-model="providerModelForm.mode" @change="resetProviderModelCapabilities"><option v-for="mode in ['chat','image','music','video']" :key="mode" :value="mode">{{ t(`create.modes.${mode}`) }}</option></UiSelect></label>
                    <label>{{ t('admin.providerModelName') }}<UiInput v-model.trim="providerModelForm.modelName" required /></label>
                    <label>{{ t('admin.providerModelDisplayName') }}<UiInput v-model.trim="providerModelForm.displayName" required /></label>
                    <label class="admin-checkbox provider-model-enabled"><UiCheckbox v-model="providerModelForm.adminEnabled" />{{ t('admin.providerModelEnabled') }}</label>
                    <label class="provider-editor-wide">{{ t('admin.providerDescription') }}<UiTextarea v-model.trim="providerModelForm.description" rows="3" /></label>
                  </div>
                  <fieldset class="provider-capabilities-fieldset">
                    <legend>{{ t('admin.providerCapabilities') }}</legend>
                    <p>{{ t('admin.providerCapabilitiesSummary') }}</p>
                    <div class="provider-capability-groups">
                      <div v-if="['image', 'video'].includes(providerModelForm.mode)">
                        <span>{{ t('admin.capabilityAspectRatios') }}</span><label v-for="item in ['auto', '1:1', '4:5', '16:9']" :key="item" class="admin-checkbox"><UiCheckbox :checked="providerModelForm.capabilities.aspectRatios.includes(item)" @change="toggleProviderCapability('aspectRatios', item)" />{{ item }}</label>
                      </div>
                      <div v-if="providerModelForm.mode !== 'chat'">
                        <span>{{ t('admin.capabilityQualities') }}</span><label v-for="item in ['auto', 'standard', 'high']" :key="item" class="admin-checkbox"><UiCheckbox :checked="providerModelForm.capabilities.qualities.includes(item)" @change="toggleProviderCapability('qualities', item)" />{{ t(`create.studio.qualities.${item}`) }}</label>
                      </div>
                      <div><span>{{ t('admin.capabilityOutputFormats') }}</span><label v-for="item in providerOutputFormats" :key="item" class="admin-checkbox"><UiCheckbox :checked="providerModelForm.capabilities.outputFormats.includes(item)" @change="toggleProviderCapability('outputFormats', item)" />{{ item.toUpperCase() }}</label></div>
                      <div v-if="['video', 'music'].includes(providerModelForm.mode)">
                        <span>{{ t('admin.capabilityDurations') }}</span><label v-for="item in providerDurationOptions" :key="item" class="admin-checkbox"><UiCheckbox :checked="providerModelForm.capabilities.durationSeconds.includes(item)" @change="toggleProviderDuration(item)" />{{ t('create.studio.durationValue', { value: item }) }}</label>
                      </div>
                      <div v-if="providerModelForm.mode !== 'chat'">
                        <span>{{ t('admin.capabilityReferences') }}</span><label v-for="item in ['image', 'video', 'audio', 'document']" :key="item" class="admin-checkbox"><UiCheckbox :checked="providerModelForm.capabilities.referenceKinds.includes(item)" @change="toggleProviderCapability('referenceKinds', item)" />{{ item }}</label>
                      </div>
                      <label v-if="providerModelForm.mode === 'image'" class="admin-checkbox"><UiCheckbox v-model="providerModelForm.capabilities.supportsMask" />{{ t('admin.capabilityMask') }}</label>
                    </div>
                  </fieldset>
                  <fieldset class="provider-capabilities-fieldset provider-pricing-fieldset">
                    <legend>{{ t('admin.providerPointPricing') }}</legend>
                    <p>{{ t('admin.providerPointPricingSummary') }}</p>
                    <div class="provider-pricing-grid">
                      <template v-if="providerModelForm.mode === 'chat'">
                        <label>{{ t('admin.inputPointsPer1KTokens') }}<UiInput v-model.number="providerModelForm.pointPricing.inputPointsPer1KTokens" type="number" min="1" step="1" required /></label>
                        <label>{{ t('admin.outputPointsPer1KTokens') }}<UiInput v-model.number="providerModelForm.pointPricing.outputPointsPer1KTokens" type="number" min="1" step="1" required /></label>
                      </template>
                      <template v-else-if="providerModelForm.mode === 'image'">
                        <div v-for="(price, index) in providerModelForm.pointPricing.imageResolutionPrices" :key="index" class="provider-resolution-price-row">
                          <label>{{ t('admin.imageResolution') }}<UiInput v-model.trim="price.resolution" inputmode="numeric" pattern="[0-9]+x[0-9]+" :placeholder="t('admin.imageResolutionPlaceholder')" required /></label>
                          <label>{{ t('admin.pointsPerImage') }}<UiInput v-model.number="price.points" type="number" min="1" step="1" required /></label>
                          <UiIconButton class="icon-button" type="button" :disabled="providerModelForm.pointPricing.imageResolutionPrices.length <= 1" :title="t('admin.removeImageResolution')" :label="t('admin.removeImageResolution')" @click="removeImageResolutionPrice(index)">
                            <Trash2 :size="15" />
                          </UiIconButton>
                        </div>
                        <UiButton class="command-button secondary provider-add-resolution" type="button" variant="secondary" @click="addImageResolutionPrice">
                          <Plus :size="15" />{{ t('admin.addImageResolution') }}
                        </UiButton>
                      </template>
                      <label v-else>{{ t('admin.pointsPerSecond') }}<UiInput v-model.number="providerModelForm.pointPricing.pointsPerSecond" type="number" min="1" step="1" required /></label>
                      <label>{{ t('admin.minimumPoints') }}<UiInput v-model.number="providerModelForm.pointPricing.minimumPoints" type="number" min="1" step="1" required /></label>
                    </div>
                  </fieldset>
                  <footer class="provider-modal-actions">
                    <span></span><span></span><UiButton class="command-button secondary" type="button" variant="secondary" @click="closeProviderEditors">
                      {{ t('actions.cancel') }}
                    </UiButton><UiButton class="command-button primary" type="submit" :disabled="actionLoading" variant="primary">
                      <LoaderCircle v-if="actionLoading" class="spin" :size="16" /><ShieldCheck v-else :size="16" />{{ t('admin.saveModel') }}
                    </UiButton>
                  </footer>
                </form>
              </section>
            </UiDrawer>
          </div>

          <div v-else-if="activeTab === 'models' && modelRoutePolicy" class="admin-governance model-routes-admin">
            <section>
              <header><div><h2>{{ t('admin.modelRoutesTitle') }}</h2><p>{{ t('admin.modelRoutesSummary') }}</p></div><span>v{{ modelRoutePolicy.routes[modelRouteMode]?.version }}</span></header>
              <p class="admin-policy-note">
                <ShieldCheck :size="15" /><span>{{ t('admin.modelRouteSelectorNote') }}</span>
              </p>
              <form class="admin-command-panel ranking-policy-form" @submit.prevent="submitModelRoute">
                <label>{{ t('admin.creationMode') }}<UiSelect v-model="modelRouteMode" @change="resetModelRouteForm"><option v-for="mode in ['chat','image','video','music']" :key="mode" :value="mode">{{ t(`create.modes.${mode}`) }}</option></UiSelect></label>
                <label>{{ t('admin.providerProfile') }}<UiSelect v-model="modelRouteForm.providerProfileId" required><option v-for="item in providers.filter(item => item.mode === modelRouteMode)" :key="item.id" :value="item.id">{{ item.displayName }} · {{ item.modelName }} · {{ item.runtimeAvailable ? t('admin.runtimeReady') : t('admin.externalConfig') }}</option></UiSelect></label>
                <label>{{ t('admin.modelRouteName') }}<UiInput v-model.trim="modelRouteForm.name" minlength="3" maxlength="80" required /></label>
                <div class="ranking-weight-grid">
                  <label>{{ t('admin.timeoutSeconds') }}<UiInput v-model.number="modelRouteForm.timeoutSeconds" type="number" min="5" max="600" required /></label>
                  <label>{{ t('admin.maxAttempts') }}<UiInput v-model.number="modelRouteForm.maxAttempts" type="number" min="1" max="5" required /></label>
                </div>
                <UiButton class="command-button primary" type="submit" :disabled="actionLoading" variant="primary">
                  <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><ShieldCheck v-else :size="17" />{{ t('admin.activateRevision') }}
                </UiButton>
              </form>
            </section>
            <section>
              <header><div><h2>{{ t('admin.modelRouteHistory') }}</h2><p>{{ t('admin.modelRouteHistorySummary') }}</p></div><span>{{ modelRoutePolicy.history[modelRouteMode]?.length || 0 }}</span></header>
              <div class="admin-list ranking-history-list">
                <article v-for="revision in modelRoutePolicy.history[modelRouteMode] || []" :key="revision.id" :data-model-route-id="revision.id">
                  <div><strong>{{ revision.name }}</strong></div><span>v{{ revision.version }}</span><span>{{ revision.providerDisplayName }} · {{ revision.modelName }}</span><small>{{ date(revision.createdAt) }}</small><span :data-status="revision.id === modelRoutePolicy.routes[modelRouteMode]?.id ? 'active' : ''">{{ revision.id === modelRoutePolicy.routes[modelRouteMode]?.id ? t('admin.activeRevision') : t('admin.supersededRevision') }}</span>
                </article>
              </div>
              <UiButton v-if="modelRoutePolicy.nextCursors?.[modelRouteMode]" class="command-button secondary admin-history-load-more" type="button" :disabled="modelRouteLoadingMore[modelRouteMode]" variant="secondary" @click="loadMoreModelRoutes">
                <LoaderCircle v-if="modelRouteLoadingMore[modelRouteMode]" class="spin" :size="16" />{{ t('actions.loadMore') }}
              </UiButton>
            </section>
          </div>

          <div v-else-if="activeTab === 'settings' && systemSettings" class="admin-governance system-settings-admin">
            <section>
              <header><div><h2>{{ t('admin.systemSettingsTitle') }}</h2><p>{{ t('admin.systemSettingsSummary') }}</p></div></header>
              <form class="admin-command-panel ranking-policy-form" @submit.prevent="submitSystemSettings">
                <fieldset>
                  <legend>{{ t('admin.writeAvailability') }}</legend><div class="system-setting-toggles">
                    <label class="admin-checkbox"><UiCheckbox v-model="systemSettingForm.registrationsEnabled" />{{ t('admin.registrationsEnabled') }}</label>
                    <label class="admin-checkbox"><UiCheckbox v-model="systemSettingForm.generationsEnabled" />{{ t('admin.generationsEnabled') }}</label>
                    <label class="admin-checkbox"><UiCheckbox v-model="systemSettingForm.publishingEnabled" />{{ t('admin.publishingEnabled') }}</label>
                    <label class="admin-checkbox"><UiCheckbox v-model="systemSettingForm.marketplaceCheckoutEnabled" />{{ t('admin.marketplaceCheckoutEnabled') }}</label>
                    <label class="admin-checkbox"><UiCheckbox v-model="systemSettingForm.taskCreationEnabled" />{{ t('admin.taskCreationEnabled') }}</label>
                  </div>
                </fieldset>
                <label>{{ t('admin.publicNotice') }}<UiTextarea v-model.trim="systemSettingForm.publicNotice" rows="2" maxlength="240" :placeholder="t('admin.publicNoticePlaceholder')" /></label>
                <UiButton class="command-button primary" type="submit" :disabled="actionLoading" variant="primary">
                  <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><Save v-else :size="17" />{{ t('admin.saveSystemSettings') }}
                </UiButton>
              </form>
            </section>
          </div>

          <div v-else-if="activeTab === 'developer' && developerAdminAccess" class="admin-governance system-settings-admin">
            <section>
              <header><div><h2>{{ t('admin.developerControlTitle') }}</h2><p>{{ t('admin.developerControlSummary') }}</p></div><span>v{{ developerAdminAccess.control.version }}</span></header>
              <form class="admin-command-panel ranking-policy-form" @submit.prevent="submitDeveloperControl">
                <label class="admin-checkbox"><UiCheckbox v-model="developerControlForm.enabled" />{{ t('admin.developerEnabled') }}</label>
                <div class="ranking-weight-grid">
                  <label>{{ t('admin.maxServiceAccounts') }}<UiInput v-model.number="developerControlForm.maxServiceAccounts" type="number" min="1" max="20" required /></label>
                  <label>{{ t('admin.maxActiveKeys') }}<UiInput v-model.number="developerControlForm.maxActiveKeys" type="number" min="1" max="10" required /></label>
                  <label>{{ t('admin.defaultTtlDays') }}<UiInput v-model.number="developerControlForm.defaultTtlDays" type="number" min="1" max="365" required /></label>
                </div>
                <UiButton class="command-button primary" type="submit" :disabled="actionLoading" variant="primary">
                  <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><ShieldCheck v-else :size="17" />{{ t('admin.applyDeveloperControl') }}
                </UiButton>
              </form>
            </section>
            <section>
              <header><div><h2>{{ t('admin.developerInventory') }}</h2><p>{{ t('admin.developerInventorySummary') }}</p></div><span>{{ developerAdminAccess.accounts.length }}</span></header>
              <form v-if="developerAdminAccess.accounts.some(account => account.status === 'active' || account.keys.some(key => key.status === 'active'))" class="admin-command-panel ranking-policy-form developer-emergency-form" @submit.prevent>
                <fieldset>
                  <legend>{{ t('admin.developerEmergencyTitle') }}</legend>
                  <p>{{ t('admin.developerEmergencySummary') }}</p>
                </fieldset>
              </form>
              <div v-if="developerAdminAccess.accounts.length" class="developer-admin-accounts">
                <article v-for="account in developerAdminAccess.accounts" :key="account.id" class="developer-admin-account">
                  <header>
                    <div><strong>{{ account.name }}</strong><span>@{{ account.ownerHandle }} · v{{ account.version }}</span></div>
                    <div class="developer-admin-actions">
                      <span :data-status="account.status === 'active' ? 'active' : 'suspended'">{{ t(`account.developerStatuses.${account.status}`) }}</span>
                      <UiButton v-if="account.status === 'active'" class="command-button danger" type="button" :disabled="actionLoading" :aria-label="t('admin.revokeDeveloperAccountNamed', { name: account.name })" variant="destructive" @click="adminRevokeDeveloperAccount(account.id, account.version)">
                        <Ban :size="16" />{{ t('admin.revokeDeveloperAccount') }}
                      </UiButton>
                    </div>
                  </header>
                  <div class="developer-admin-meta">
                    <span>{{ t('admin.activeKeyCount', { count: account.keys.filter(key => key.status === 'active').length }) }}</span>
                    <small>{{ t('admin.updatedAt', { date: date(account.updatedAt) }) }}</small>
                  </div>
                  <div v-if="account.keys.length" class="developer-admin-keys">
                    <div v-for="key in account.keys" :key="key.id" class="developer-admin-key">
                      <div>
                        <strong>{{ t('account.keyDisplay', { prefix: key.publicPrefix, hint: key.displayHint }) }}</strong>
                        <span>{{ key.scopes.join(', ') }} · v{{ key.version }}</span>
                        <small>{{ t('admin.developerKeyEvidence', { uses: key.usageCount, date: date(key.expiresAt) }) }}</small>
                      </div>
                      <div class="developer-admin-actions">
                        <span :data-status="key.status === 'active' ? 'active' : 'suspended'">{{ t(`account.developerStatuses.${key.status}`) }}</span>
                        <UiButton v-if="key.status === 'active'" class="command-button danger" type="button" :disabled="actionLoading" :aria-label="t('admin.revokeDeveloperKeyNamed', { prefix: key.publicPrefix })" variant="destructive" @click="adminRevokeDeveloperKey(key.id, key.version)">
                          <KeyRound :size="16" />{{ t('admin.revokeDeveloperKey') }}
                        </UiButton>
                      </div>
                    </div>
                  </div>
                  <p v-else class="inline-empty">
                    {{ t('admin.noDeveloperKeys') }}
                  </p>
                </article>
              </div>
              <p v-else class="inline-empty">
                {{ t('admin.noDeveloperAccounts') }}
              </p>
            </section>
            <section>
              <header><div><h2>{{ t('admin.webhookDeadLetters') }}</h2><p>{{ t('admin.webhookDeadLettersSummary') }}</p></div><span>{{ webhookDeadLetters.length }}</span></header>
              <form class="admin-user-filters admin-finance-filters" @submit.prevent="applyDeveloperRecoveryFilters">
                <label>{{ t('admin.webhookRecoverySearch') }}<UiInput v-model="webhookQuery" type="search" maxlength="120" :placeholder="t('admin.webhookRecoverySearchPlaceholder')" /></label>
                <label>{{ t('admin.webhookEventType') }}<UiSelect v-model="webhookEventType"><option value="">{{ t('admin.allWebhookEventTypes') }}</option><option v-for="eventType in ['developer.webhook.test','generation.completed','work.published','marketplace.order.fulfilled','marketplace.order.refunded']" :key="eventType" :value="eventType">{{ webhookEventLabel(eventType) }}</option></UiSelect></label>
                <UiButton class="command-button primary" type="submit" variant="primary">
                  <ListFilter :size="16" />{{ t('actions.applyFilters') }}
                </UiButton>
                <UiIconButton class="icon-button" type="button" :title="t('actions.clearFilters')" :label="t('actions.clearFilters')" @click="clearWebhookRecoveryFilters">
                  <Undo2 :size="16" />
                </UiIconButton>
              </form>
              <form v-if="webhookDeadLetters.length" class="admin-command-panel ranking-policy-form" @submit.prevent>
              </form>
              <div v-if="webhookDeadLetters.length" class="admin-list webhook-dead-letter-list">
                <article v-for="delivery in webhookDeadLetters" :key="delivery.id">
                  <div><strong>{{ delivery.endpointName }}</strong><span>@{{ delivery.ownerHandle }} · {{ delivery.endpointHost }}</span></div>
                  <span>{{ webhookEventLabel(delivery.eventType) }}</span>
                  <span>{{ t('admin.webhookAttempts', { count: delivery.attemptCount }) }}</span>
                  <small>{{ delivery.lastStatusCode ? t('account.webhookHttpStatus', { code: delivery.lastStatusCode }) : delivery.lastErrorCode }} · {{ date(delivery.updatedAt) }}</small>
                  <UiButton class="command-button secondary" type="button" :disabled="actionLoading" :aria-label="t('admin.replayWebhookNamed', { name: delivery.endpointName })" variant="secondary" @click="adminReplayWebhook(delivery)">
                    <RefreshCw :size="16" />{{ t('admin.replayWebhook') }}
                  </UiButton>
                </article>
              </div>
              <p v-else class="inline-empty">
                {{ t('admin.noWebhookDeadLetters') }}
              </p>
              <UiButton v-if="webhookNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="webhookLoadingMore" variant="secondary" @click="loadMoreWebhookRecovery">
                <LoaderCircle v-if="webhookLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
              </UiButton>
            </section>
            <section>
              <header><div><h2>{{ t('admin.emailDeadLetters') }}</h2><p>{{ t('admin.emailDeadLettersSummary') }}</p></div><span>{{ emailActionDeadLetters.length }}</span></header>
              <form class="admin-user-filters admin-finance-filters" @submit.prevent="applyDeveloperRecoveryFilters">
                <label>{{ t('admin.emailRecoverySearch') }}<UiInput v-model="emailQuery" type="search" maxlength="120" :placeholder="t('admin.emailRecoverySearchPlaceholder')" /></label>
                <label>{{ t('admin.emailActionKind') }}<UiSelect v-model="emailKind"><option value="">{{ t('admin.allEmailActionKinds') }}</option><option v-for="kind in ['verify_email','password_reset']" :key="kind" :value="kind">{{ t(`account.emailActionKinds.${kind}`) }}</option></UiSelect></label>
                <UiButton class="command-button primary" type="submit" variant="primary">
                  <ListFilter :size="16" />{{ t('actions.applyFilters') }}
                </UiButton>
                <UiIconButton class="icon-button" type="button" :title="t('actions.clearFilters')" :label="t('actions.clearFilters')" @click="clearEmailRecoveryFilters">
                  <Undo2 :size="16" />
                </UiIconButton>
              </form>
              <form v-if="emailActionDeadLetters.length" class="admin-command-panel ranking-policy-form" @submit.prevent>
              </form>
              <div v-if="emailActionDeadLetters.length" class="admin-list webhook-dead-letter-list">
                <article v-for="item in emailActionDeadLetters" :key="item.id">
                  <div><strong>{{ t(`account.emailActionKinds.${item.kind}`) }}</strong><span>@{{ item.ownerHandle }} · {{ item.emailHint }}</span></div>
                  <span>{{ t(`account.emailActionStatuses.${item.status}`) }}</span>
                  <span>{{ t('admin.emailAttempts', { count: item.attemptCount }) }}</span>
                  <small>{{ item.attempts.at(-1)?.errorCode || t('admin.unknownState') }} · {{ date(item.updatedAt) }}</small>
                  <div class="developer-admin-actions">
                    <UiButton class="command-button secondary" type="button" :disabled="actionLoading" variant="secondary" @click="adminRecoverEmailAction(item, 'retry')">
                      <RefreshCw :size="16" />{{ t('admin.retryEmail') }}
                    </UiButton>
                    <UiButton class="command-button danger" type="button" :disabled="actionLoading" variant="destructive" @click="adminRecoverEmailAction(item, 'cancel')">
                      <Ban :size="16" />{{ t('admin.cancelEmailAction') }}
                    </UiButton>
                  </div>
                </article>
              </div>
              <p v-else class="inline-empty">
                {{ t('admin.noEmailDeadLetters') }}
              </p>
              <UiButton v-if="emailNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="emailLoadingMore" variant="secondary" @click="loadMoreEmailRecovery">
                <LoaderCircle v-if="emailLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
              </UiButton>
            </section>
          </div>

          <div v-else-if="activeTab === 'finance'" class="admin-user-directory">
            <section class="admin-finance-section subscription-plan-admin">
              <header>
                <div><h2>{{ t('admin.subscriptionPlansTitle') }}</h2><p>{{ t('admin.subscriptionPlansSummary') }}</p></div><UiButton class="command-button primary" type="button" variant="primary" @click="openNewSubscriptionPlan">
                  <Plus :size="16" />{{ t('admin.addSubscriptionPlan') }}
                </UiButton>
              </header>
              <div class="subscription-plan-admin-list">
                <article v-for="plan in subscriptionPlans" :key="plan.id" :data-status="plan.active ? 'active' : 'suspended'">
                  <div><strong>{{ plan.name }}</strong><small>{{ plan.tierCode }} · {{ plan.description }}</small></div>
                  <span><strong>{{ formatCurrency(plan.priceCents, plan.currency, locale) }}</strong><small>/ {{ plan.billingPeriodDays }} {{ t('workspace.days') }}</small></span>
                  <span><strong>{{ plan.includedPoints.toLocaleString(locale) }}</strong><small>{{ t('workspace.pointsUnit') }}</small></span>
                  <span>{{ t('workspace.modelsIncluded', { count: plan.modelIds.length }) }}</span>
                  <UiIconButton class="icon-button" type="button" :title="t('admin.editSubscriptionPlan')" :label="t('admin.editSubscriptionPlan')" @click="editSubscriptionPlan(plan)">
                    <Pencil :size="15" />
                  </UiIconButton>
                </article>
              </div>
              <UiDrawer :open="subscriptionPlanEditorOpen" size="lg" :label="subscriptionPlanForm.id ? t('admin.editSubscriptionPlan') : t('admin.addSubscriptionPlan')" @update:open="!$event && closeSubscriptionPlanEditor()" @after-close="resetSubscriptionPlanForm">
                <form class="subscription-plan-editor" @submit.prevent="submitSubscriptionPlan">
                  <header>
                    <div><span>{{ subscriptionPlanForm.id ? t('admin.editSubscriptionPlan') : t('admin.addSubscriptionPlan') }}</span><h2>{{ subscriptionPlanForm.name || t('admin.subscriptionPlansTitle') }}</h2></div>
                    <UiIconButton class="icon-button" type="button" :label="t('actions.close')" @click="closeSubscriptionPlanEditor">
                      <X :size="17" />
                    </UiIconButton>
                  </header>
                  <div class="subscription-plan-editor-body">
                    <div class="subscription-plan-fields">
                      <label>{{ t('admin.subscriptionTierCode') }}<UiInput v-model.trim="subscriptionPlanForm.tierCode" maxlength="32" required /></label>
                      <label>{{ t('admin.subscriptionPlanName') }}<UiInput v-model.trim="subscriptionPlanForm.name" maxlength="80" required /></label>
                      <label>{{ t('admin.subscriptionPrice') }}<UiInput v-model.number="subscriptionPlanForm.priceCents" type="number" min="0" step="1" required /></label>
                      <label>{{ t('admin.subscriptionPoints') }}<UiInput v-model.number="subscriptionPlanForm.includedPoints" type="number" min="1" step="1" required /></label>
                      <label>{{ t('admin.subscriptionPeriodDays') }}<UiInput v-model.number="subscriptionPlanForm.billingPeriodDays" type="number" min="1" max="366" step="1" required /></label>
                      <label>{{ t('admin.subscriptionSortOrder') }}<UiInput v-model.number="subscriptionPlanForm.sortOrder" type="number" step="1" required /></label>
                      <label class="provider-editor-wide">{{ t('admin.subscriptionDescription') }}<UiTextarea v-model.trim="subscriptionPlanForm.description" maxlength="500" rows="3" required /></label>
                      <label class="admin-checkbox"><UiCheckbox v-model="subscriptionPlanForm.active" />{{ t('admin.subscriptionActive') }}</label>
                    </div>
                    <fieldset class="subscription-model-selector">
                      <legend>{{ t('admin.subscriptionModels') }}</legend><p>{{ t('admin.subscriptionModelsSummary') }}</p>
                      <label v-for="model in availablePlanModels" :key="model.id" class="admin-checkbox"><UiCheckbox :checked="subscriptionPlanForm.modelIds.includes(model.id)" @change="toggleSubscriptionPlanModel(model.id)" /><span>{{ model.displayName }}<small>{{ model.providerName }} · {{ t(`create.modes.${model.mode}`) }}</small></span></label>
                    </fieldset>
                  </div>
                  <footer>
                    <UiButton class="command-button secondary" type="button" variant="secondary" @click="closeSubscriptionPlanEditor">
                      {{ t('actions.cancel') }}
                    </UiButton><UiButton class="command-button primary" type="submit" :disabled="actionLoading" variant="primary">
                      <LoaderCircle v-if="actionLoading" class="spin" :size="16" /><ShieldCheck v-else :size="16" />{{ t('admin.saveSubscriptionPlan') }}
                    </UiButton>
                  </footer>
                </form>
              </UiDrawer>
            </section>

            <section class="admin-finance-section payment-gateway-admin">
              <UiTabs v-model="paymentGatewayTab" class="payment-gateway-tabs" :items="paymentGatewayTabs" :label="t('admin.paymentGatewayTitle')" />

              <div v-if="paymentGatewayTab === 'general'" class="payment-gateway-panel">
                <section class="payment-gateway-block payment-gateway-common">
                  <header>
                    <div><h3>{{ t('admin.paymentGatewayGeneralTitle') }}</h3><p>{{ t('admin.paymentGatewayGeneralSummary') }}</p></div><UiButton class="command-button primary" type="button" variant="primary" @click="savePaymentGatewayGeneral">
                      <Save :size="16" />{{ t('actions.save') }}
                    </UiButton>
                  </header>
                  <div class="payment-gateway-fields">
                    <label>{{ t('admin.paymentGatewayUnitPrice') }}<UiInput v-model.number="paymentGatewayGeneral.unitPrice" type="number" min="0" step="0.01" /></label>
                    <label>{{ t('admin.paymentGatewayMinimumTopup') }}<UiInput v-model.number="paymentGatewayGeneral.minimumTopup" type="number" min="0" step="0.01" /></label>
                  </div>
                </section>

                <section class="payment-gateway-block payment-gateway-methods">
                  <header>
                    <div><h3>{{ t('admin.paymentGatewayMethodsTitle') }}</h3><p>{{ t('admin.paymentGatewayMethodsSummary') }}</p></div><UiButton class="command-button secondary" type="button" variant="secondary" @click="openPaymentMethodJson">
                      <Code2 :size="16" />{{ t('admin.paymentGatewayJsonEdit') }}
                    </UiButton>
                  </header>
                  <div class="payment-method-toolbar">
                    <label class="payment-method-search"><Search :size="16" /><span class="sr-only">{{ t('admin.paymentGatewayMethodSearch') }}</span><UiInput v-model="paymentMethodQuery" type="search" :placeholder="t('admin.paymentGatewayMethodSearchPlaceholder')" /></label>
                    <UiButton class="command-button secondary" type="button" variant="secondary" @click="openPaymentMethodJson">
                      <SlidersHorizontal :size="16" />{{ t('admin.paymentGatewayTemplate') }}
                    </UiButton>
                    <UiButton class="command-button primary" type="button" variant="primary" @click="openPaymentMethodJson">
                      <Plus :size="16" />{{ t('admin.paymentGatewayAddMethod') }}
                    </UiButton>
                  </div>
                  <div class="payment-method-table-wrap">
                    <UiTable table-class="payment-method-table">
                      <thead><tr><th>{{ t('admin.paymentGatewayMethodName') }}</th><th>{{ t('admin.paymentGatewayMethodType') }}</th><th>{{ t('admin.paymentGatewayMethodIcon') }}</th><th>{{ t('admin.paymentGatewayMethodMinimum') }}</th><th>{{ t('admin.paymentGatewayMethodActions') }}</th></tr></thead><tbody>
                        <tr v-for="item in paymentGatewayMethods" :key="item.id">
                          <td><strong>{{ item.name }}</strong></td><td><code>{{ item.handle }}</code></td><td><span class="payment-method-icon"><CreditCard :size="16" />{{ item.icon.replace('Lu', '') }}</span></td><td>{{ item.minimum }}</td><td>
                            <UiIconButton class="icon-button" type="button" :title="t('admin.editPaymentProvider')" :label="t('admin.editPaymentProvider')" @click="editPaymentGatewayMethod(item)">
                              <Pencil :size="15" />
                            </UiIconButton>
                          </td>
                        </tr>
                        <tr v-if="!paymentGatewayMethods.length">
                          <td colspan="5" class="payment-method-empty">
                            {{ t('admin.paymentGatewayNoMethods') }}
                          </td>
                        </tr>
                      </tbody>
                    </UiTable>
                  </div>
                  <p class="payment-method-note">
                    {{ t('admin.paymentGatewayMethodsNote') }}
                  </p>
                </section>

                <div class="payment-gateway-grid">
                  <section class="payment-gateway-block payment-gateway-amounts">
                    <header>
                      <div><h3>{{ t('admin.paymentGatewayAmountsTitle') }}</h3><p>{{ t('admin.paymentGatewayAmountsSummary') }}</p></div><UiButton class="command-button secondary" type="button" variant="secondary" @click="openPaymentMethodJson">
                        <Code2 :size="16" />{{ t('admin.paymentGatewayJsonEdit') }}
                      </UiButton>
                    </header>
                    <div class="payment-amount-chips">
                      <span v-for="(amount, index) in topupAmounts" :key="`${amount}-${index}`" class="payment-amount-chip"><UiInput v-model.number="topupAmounts[index]" type="number" min="1" step="1" /><UiIconButton class="icon-button" type="button" :label="t('admin.paymentGatewayRemoveAmount')" @click="removeTopupAmount(index)"><X :size="14" /></UiIconButton></span>
                    </div>
                    <div class="payment-gateway-add-row">
                      <UiInput v-model.number="newTopupAmount" type="number" min="1" step="1" :placeholder="t('admin.paymentGatewayAmountPlaceholder')" /><UiButton class="command-button primary" type="button" variant="primary" @click="addTopupAmount">
                        <Plus :size="16" />{{ t('admin.paymentGatewayAdd') }}
                      </UiButton>
                    </div>
                  </section>
                  <section class="payment-gateway-block payment-gateway-discounts">
                    <header>
                      <div><h3>{{ t('admin.paymentGatewayDiscountsTitle') }}</h3><p>{{ t('admin.paymentGatewayDiscountsSummary') }}</p></div><UiButton class="command-button secondary" type="button" variant="secondary" @click="openPaymentMethodJson">
                        <Code2 :size="16" />{{ t('admin.paymentGatewayJsonEdit') }}
                      </UiButton>
                    </header>
                    <div class="payment-discount-table-wrap">
                      <UiTable table-class="payment-discount-table">
                        <thead><tr><th>{{ t('admin.paymentGatewayDiscountAmount') }}</th><th>{{ t('admin.paymentGatewayDiscountRate') }}</th><th>{{ t('admin.paymentGatewayDiscountSaving') }}</th><th>{{ t('admin.paymentGatewayMethodActions') }}</th></tr></thead><tbody>
                          <tr v-for="(tier, index) in discountTiers" :key="`${tier.amount}-${index}`">
                            <td><UiInput v-model.number="tier.amount" type="number" min="1" step="1" /></td><td><UiInput v-model.number="tier.rate" type="number" min="0" max="1" step="0.01" /></td><td class="payment-discount-saving">
                              {{ Math.round((1 - tier.rate) * 100) }}%
                            </td><td>
                              <UiIconButton class="icon-button" type="button" :label="t('admin.paymentGatewayRemoveDiscount')" @click="removeDiscountTier(index)">
                                <X :size="14" />
                              </UiIconButton>
                            </td>
                          </tr>
                        </tbody>
                      </UiTable>
                    </div>
                    <UiButton class="command-button primary" type="button" variant="primary" @click="addDiscountTier">
                      <Plus :size="16" />{{ t('admin.paymentGatewayAddDiscount') }}
                    </UiButton>
                  </section>
                </div>
              </div>

              <div v-else class="payment-gateway-panel payment-gateway-channel-panel">
                <section class="payment-gateway-block payment-gateway-channel-summary">
                  <header>
                    <div><span class="payment-gateway-eyebrow">{{ t('admin.paymentGatewayChannel') }}</span><h3>{{ paymentGatewayTabs.find(item => item.value === paymentGatewayTab)?.label }}</h3><p>{{ selectedPaymentProviderConfig ? t('admin.paymentGatewayChannelSummary') : t('admin.paymentGatewayChannelUnavailable') }}</p></div><div class="payment-gateway-channel-actions">
                      <span class="payment-gateway-status" :data-status="selectedPaymentProviderConfig?.enabled ? 'active' : 'inactive'">{{ selectedPaymentProviderConfig?.enabled ? t('admin.paymentProviderEnabled') : t('admin.paymentProviderDisabled') }}</span><UiButton v-if="paymentGatewayTab === 'stripe' || paymentGatewayTab === 'epay' || paymentGatewayTab === 'waffo_pancake'" class="command-button primary" type="button" variant="primary" @click="openSelectedPaymentProviderConfig">
                        <Pencil :size="16" />{{ t('admin.editPaymentProvider') }}
                      </UiButton>
                    </div>
                  </header>
                  <div v-if="selectedPaymentProviderConfig" class="payment-gateway-config-grid">
                    <div><span>{{ t('admin.paymentProviderEnvironment') }}</span><strong>{{ selectedPaymentProviderConfig.environment }}</strong></div><div><span>{{ t('admin.paymentProviderMerchantId') }}</span><strong>{{ selectedPaymentProviderConfig.merchantId || '—' }}</strong></div><div><span>{{ t('admin.paymentProviderStoreId') }}</span><strong>{{ selectedPaymentProviderConfig.storeId || '—' }}</strong></div><div><span>{{ t('admin.paymentProviderSecret') }}</span><strong :data-status="selectedPaymentProviderConfig.secretConfigured ? 'active' : 'inactive'">{{ selectedPaymentProviderConfig.secretConfigured ? t('admin.paymentProviderSecretReady') : t('admin.paymentProviderSecretMissing') }}</strong></div>
                  </div>
                  <div v-else class="payment-gateway-unavailable">
                    <CreditCard :size="22" /><div><strong>{{ t('admin.paymentGatewayNotConnected') }}</strong><p>{{ t('admin.paymentGatewayNotConnectedSummary') }}</p></div>
                  </div>
                </section>
                <section class="payment-gateway-block payment-gateway-methods">
                  <header>
                    <div><h3>{{ t('admin.paymentGatewayMethodsTitle') }}</h3><p>{{ t('admin.paymentGatewayChannelMethodsSummary') }}</p></div><UiButton class="command-button secondary" type="button" variant="secondary" @click="openPaymentMethodJson">
                      <Code2 :size="16" />{{ t('admin.paymentGatewayJsonEdit') }}
                    </UiButton>
                  </header><div class="payment-method-table-wrap">
                    <UiTable table-class="payment-method-table">
                      <thead><tr><th>{{ t('admin.paymentGatewayMethodName') }}</th><th>{{ t('admin.paymentGatewayMethodType') }}</th><th>{{ t('admin.paymentGatewayMethodIcon') }}</th><th>{{ t('admin.paymentGatewayMethodMinimum') }}</th><th>{{ t('admin.paymentGatewayMethodActions') }}</th></tr></thead><tbody>
                        <tr v-for="item in paymentGatewayMethods" :key="item.id">
                          <td><strong>{{ item.name }}</strong></td><td><code>{{ item.handle }}</code></td><td><span class="payment-method-icon"><CreditCard :size="16" />{{ item.icon.replace('Lu', '') }}</span></td><td>{{ item.minimum }}</td><td>
                            <UiIconButton class="icon-button" type="button" :title="t('admin.editPaymentProvider')" :label="t('admin.editPaymentProvider')" @click="editPaymentGatewayMethod(item)">
                              <Pencil :size="15" />
                            </UiIconButton>
                          </td>
                        </tr><tr v-if="!paymentGatewayMethods.length">
                          <td colspan="5" class="payment-method-empty">
                            {{ t('admin.paymentGatewayNoMethods') }}
                          </td>
                        </tr>
                      </tbody>
                    </UiTable>
                  </div>
                </section>
              </div>

              <UiDrawer :open="paymentMethodJsonOpen" size="md" :label="t('admin.paymentGatewayJsonEdit')" @update:open="paymentMethodJsonOpen = $event">
                <form class="payment-gateway-json-editor" @submit.prevent="savePaymentMethodJson">
                  <header>
                    <div><span>{{ t('admin.paymentGatewayJsonEdit') }}</span><h2>{{ t('admin.paymentGatewayMethodsTitle') }}</h2></div><UiIconButton class="icon-button" type="button" :label="t('actions.close')" @click="paymentMethodJsonOpen = false">
                      <X :size="17" />
                    </UiIconButton>
                  </header><div class="payment-gateway-json-body">
                    <label>{{ t('admin.paymentGatewayJsonLabel') }}<UiTextarea v-model="paymentMethodJson" rows="16" spellcheck="false" /></label><p class="provider-config-note">
                      <Code2 :size="15" />{{ t('admin.paymentGatewayJsonSummary') }}
                    </p>
                  </div><footer>
                    <UiButton class="command-button secondary" type="button" variant="secondary" @click="paymentMethodJsonOpen = false">
                      {{ t('actions.cancel') }}
                    </UiButton><UiButton class="command-button primary" type="submit" variant="primary">
                      <Save :size="16" />{{ t('actions.save') }}
                    </UiButton>
                  </footer>
                </form>
              </UiDrawer>
              <UiDrawer :open="paymentProviderEditorOpen" size="lg" :label="t('admin.editPaymentProvider')" @update:open="!$event && closePaymentProviderConfig()">
                <form class="subscription-plan-editor" @submit.prevent="submitPaymentProviderConfig">
                  <header>
                    <div><span>{{ t('admin.editPaymentProvider') }}</span><h2>{{ paymentGatewayTabs.find(item => item.value === paymentProviderForm.provider)?.label || paymentProviderForm.provider }}</h2></div><UiIconButton class="icon-button" type="button" :label="t('actions.close')" @click="closePaymentProviderConfig">
                      <X :size="17" />
                    </UiIconButton>
                  </header>
                  <div class="subscription-plan-editor-body">
                    <div class="subscription-plan-fields">
                      <label>{{ t('admin.paymentProviderEnvironment') }}<UiSelect v-model="paymentProviderForm.environment" :aria-label="t('admin.paymentProviderEnvironment')"><option value="test">{{ t('admin.paymentProviderEnvironmentTest') }}</option><option value="prod">{{ t('admin.paymentProviderEnvironmentProd') }}</option></UiSelect></label><label class="admin-checkbox"><UiCheckbox v-model="paymentProviderForm.enabled" />{{ t('admin.paymentProviderEnabled') }}</label><label>{{ t('admin.paymentProviderMerchantId') }}<UiInput v-model.trim="paymentProviderForm.merchantId" maxlength="255" /></label><label>{{ t('admin.paymentProviderStoreId') }}<UiInput v-model.trim="paymentProviderForm.storeId" maxlength="255" /></label><label v-if="paymentProviderForm.provider === 'waffo_pancake'">{{ t('admin.paymentProviderOnetimeProduct') }}<UiInput v-model.trim="paymentProviderForm.productIdOnetime" maxlength="255" /></label><label v-if="paymentProviderForm.provider === 'waffo_pancake'">{{ t('admin.paymentProviderSubscriptionProduct') }}<UiInput v-model.trim="paymentProviderForm.productIdSubscription" maxlength="255" /></label>
                    </div><p class="provider-config-note">
                      <Settings2 :size="15" />{{ t('admin.paymentProviderSecretNote') }}
                    </p>
                  </div>
                  <footer>
                    <UiButton class="command-button secondary" type="button" variant="secondary" @click="closePaymentProviderConfig">
                      {{ t('actions.cancel') }}
                    </UiButton><UiButton class="command-button primary" type="submit" :disabled="actionLoading" variant="primary">
                      <ShieldCheck :size="16" />{{ t('actions.save') }}
                    </UiButton>
                  </footer>
                </form>
              </UiDrawer>
            </section>

            <form class="admin-user-filters admin-finance-filters" @submit.prevent="applyFinanceFilters">
              <label>{{ t('admin.financeSearch') }}<UiInput v-model="financeQuery" type="search" maxlength="120" :placeholder="t('admin.financeSearchPlaceholder')" /></label>
              <label>{{ t('admin.financeState') }}<UiSelect v-model="financeState"><option value="">{{ t('admin.allFinanceStates') }}</option><option v-for="state in ['available','reserved','depleted']" :key="state" :value="state">{{ t(`admin.financeStates.${state}`) }}</option></UiSelect></label>
              <UiButton class="command-button primary" type="submit" variant="primary">
                <ListFilter :size="16" />{{ t('actions.applyFilters') }}
              </UiButton>
              <UiIconButton class="icon-button" type="button" :title="t('actions.clearFilters')" :label="t('actions.clearFilters')" @click="clearFinanceFilters">
                <Undo2 :size="16" />
              </UiIconButton>
            </form>
            <div v-if="finance.length" class="admin-list finance-admin-list">
              <article v-for="item in finance" :key="`${item.userId}-${item.currency}`">
                <div><strong>{{ item.displayName }}</strong><span>@{{ item.handle }} · {{ item.email }}</span></div><span>{{ formatCurrency(item.availableCents, item.currency, locale) }}</span><span>{{ t('admin.reserved', { amount: formatCurrency(item.reservedCents, item.currency, locale) }) }}</span><small>{{ date(item.updatedAt) }}</small><UiButton class="command-button secondary" type="button" variant="secondary" @click="openFinance(item)">
                  <CircleDollarSign :size="16" />{{ t('admin.adjust') }}
                </UiButton>
              </article>
            </div>
            <div v-else class="workspace-empty">
              <CircleDollarSign :size="22" /><p>{{ t('admin.noFinanceAccounts') }}</p>
            </div>
            <UiButton v-if="financeNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="financeLoadingMore" variant="secondary" @click="loadMoreFinance">
              <LoaderCircle v-if="financeLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
            </UiButton>

            <section class="admin-finance-section provider-cost-reconciliation-admin">
              <header><div><h2>{{ t('admin.providerCostReconciliationTitle') }}</h2><p>{{ t('admin.providerCostReconciliationSummary') }}</p></div></header>
              <form v-if="providerCostReconciliationAvailable" class="admin-user-filters admin-finance-filters" @submit.prevent="requestProviderCostReconciliation">
                <label>{{ t('admin.providerCostPeriodStart') }}<UiInput v-model="providerCostReconciliationForm.periodStart" type="date" required /></label>
                <label>{{ t('admin.providerCostPeriodEnd') }}<UiInput v-model="providerCostReconciliationForm.periodEnd" type="date" required /></label>
                <UiButton class="command-button primary" type="submit" :disabled="actionLoading" variant="primary">
                  <LoaderCircle v-if="actionLoading" class="spin" :size="16" /><CircleDollarSign v-else :size="16" />{{ t('admin.requestProviderCostReconciliation') }}
                </UiButton>
              </form>
              <p v-else-if="!providerCostReconciliationLoading" class="inline-empty">
                {{ t('admin.providerCostReconciliationUnavailable') }}
              </p>
              <div v-if="providerCostReconciliations.length" class="admin-list payment-operation-list">
                <article v-for="item in providerCostReconciliations" :key="item.id">
                  <div><strong>{{ item.provider.toUpperCase() }} · {{ item.status }}</strong><span>{{ date(item.periodStart) }} - {{ date(item.periodEnd) }}</span></div>
                  <span>{{ item.providerCostMicros === undefined ? '—' : formatCurrency(item.providerCostMicros / 10000, item.currency || 'USD', locale) }}</span>
                  <span>{{ item.localEstimatedCostMicros === undefined ? '—' : formatCurrency(item.localEstimatedCostMicros / 10000, 'USD', locale) }}</span>
                  <span :class="{ 'status-attention': item.status === 'overage' || item.status === 'failed' }">{{ t(`admin.providerCostReconciliationStates.${item.status}`) }}</span>
                  <small>{{ t('admin.providerCostThreshold', { amount: formatCurrency(item.overageThresholdMicros / 10000, 'USD', locale) }) }} · {{ date(item.completedAt || item.createdAt) }}</small>
                </article>
              </div>
              <p v-else-if="providerCostReconciliationAvailable && !providerCostReconciliationLoading" class="inline-empty">
                {{ t('admin.noProviderCostReconciliations') }}
              </p>
            </section>

            <section class="admin-finance-section payment-operations-admin">
              <header><div><h2>{{ t('admin.paymentOperationsTitle') }}</h2><p>{{ t('admin.paymentOperationsSummary') }}</p></div></header>
              <form class="admin-user-filters admin-finance-filters" @submit.prevent="applyPaymentFilters">
                <label>{{ t('admin.paymentSearch') }}<UiInput v-model="paymentQuery" type="search" maxlength="120" :placeholder="t('admin.paymentSearchPlaceholder')" /></label>
                <label>{{ t('admin.paymentPurpose') }}<UiSelect v-model="paymentPurpose"><option value="">{{ t('admin.allPaymentPurposes') }}</option><option value="product">{{ t('admin.paymentPurposes.product') }}</option><option value="task">{{ t('admin.paymentPurposes.task') }}</option><option value="wallet_topup">{{ paymentPurposeLabel('wallet_topup') }}</option><option value="subscription">{{ paymentPurposeLabel('subscription') }}</option></UiSelect></label>
                <label>{{ t('admin.paymentStatus') }}<UiSelect v-model="paymentStatus"><option value="">{{ t('admin.allPaymentStatuses') }}</option><option v-for="state in ['checkout_pending','checkout_open','paid','payment_failed','transfer_pending','transferred','refund_pending','refund_failed','refunded','cancelled']" :key="state" :value="state">{{ t(`admin.paymentStatuses.${state}`) }}</option></UiSelect></label>
                <label>{{ t('admin.paymentMode') }}<UiSelect v-model="paymentMode"><option value="">{{ t('admin.allPaymentModes') }}</option><option value="test">{{ t('admin.paymentModes.test') }}</option><option value="live">{{ t('admin.paymentModes.live') }}</option></UiSelect></label>
                <label>{{ t('admin.paymentAttention') }}<UiSelect v-model="paymentAttention"><option value="needs_attention">{{ t('admin.paymentAttentionStates.needs_attention') }}</option><option value="healthy">{{ t('admin.paymentAttentionStates.healthy') }}</option><option value="">{{ t('admin.paymentAttentionStates.all') }}</option></UiSelect></label>
                <UiButton class="command-button primary" type="submit" variant="primary">
                  <ListFilter :size="16" />{{ t('actions.applyFilters') }}
                </UiButton>
              </form>
              <div v-if="paymentOperations.length" class="admin-list payment-operation-list">
                <article v-for="item in paymentOperations" :key="item.id">
                  <div>
                    <RouterLink :to="item.targetPath">
                      <strong>{{ item.resourceTitle }}</strong>
                    </RouterLink><span>{{ paymentPurposeLabel(item.purpose) }} · @{{ item.payerHandle }}<template v-if="item.payeeHandle"> → @{{ item.payeeHandle }}</template></span>
                  </div>
                  <span>{{ formatCurrency(item.amountCents, item.currency, locale) }}</span>
                  <span>{{ t(`admin.paymentStatuses.${item.status}`) }}</span>
                  <span :class="{ 'status-attention': item.attentionCode !== 'none' }">{{ t(`admin.paymentAttentionCodes.${item.attentionCode}`) }}</span>
                  <small>{{ item.liveMode ? t('admin.paymentModes.live') : t('admin.paymentModes.test') }} · v{{ item.version }} · {{ date(item.updatedAt) }}</small>
                  <div class="admin-row-actions">
                    <UiButton v-if="item.payeeId && item.status === 'transfer_pending'" class="command-button secondary" type="button" variant="secondary" @click="openPaymentDestination(item)">
                      <Settings2 :size="16" />{{ t('admin.manageDestination') }}
                    </UiButton>
                    <UiButton v-if="item.status === 'transfer_pending' && item.destination?.status === 'verified' && ['transfer_job_failed','transfer_job_missing'].includes(item.attentionCode)" class="command-button secondary" type="button" variant="secondary" @click="openPaymentRecovery(item, 'retry_transfer')">
                      <RefreshCw :size="16" />{{ t('admin.retryTransfer') }}
                    </UiButton>
                    <UiButton v-if="['refund_failed','refund_job_failed','refund_job_missing'].includes(item.attentionCode)" class="command-button secondary" type="button" variant="secondary" @click="openPaymentRecovery(item, 'retry_refund')">
                      <RefreshCw :size="16" />{{ t('admin.retryRefund') }}
                    </UiButton>
                    <UiButton v-if="item.providerEvent && (item.providerEvent.processingState === 'failed' || item.providerEvent.job?.status === 'failed')" class="command-button secondary" type="button" variant="secondary" @click="openPaymentEventReplay(item)">
                      <RefreshCw :size="16" />{{ t('admin.replayPaymentEvent') }}
                    </UiButton>
                  </div>
                </article>
              </div>
              <p v-else class="inline-empty">
                {{ t('admin.noPaymentOperations') }}
              </p>
              <UiButton v-if="paymentNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="paymentLoadingMore" variant="secondary" @click="loadMorePayments">
                <LoaderCircle v-if="paymentLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
              </UiButton>
            </section>

            <section class="admin-finance-section payment-destinations-admin">
              <header><div><h2>{{ t('admin.paymentDestinationsTitle') }}</h2><p>{{ t('admin.paymentDestinationsSummary') }}</p></div></header>
              <div v-if="paymentDestinations.length" class="admin-list finance-admin-list">
                <article v-for="item in paymentDestinations" :key="item.id">
                  <div><strong>{{ item.displayName || item.handle }}</strong><span>@{{ item.handle }} · {{ item.email }}</span></div><span>{{ item.destinationId }}</span><span>{{ t(`admin.paymentDestinationStatuses.${item.status}`) }}</span><small>v{{ item.version }} · {{ date(item.updatedAt) }}</small>
                </article>
              </div>
              <p v-else class="inline-empty">
                {{ t('admin.noPaymentDestinations') }}
              </p>
              <UiButton v-if="paymentDestinationNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="paymentLoadingMore" variant="secondary" @click="loadMorePaymentDestinations">
                <LoaderCircle v-if="paymentLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
              </UiButton>
            </section>
          </div>

          <div v-else-if="activeTab === 'ranking' && rankingPolicy" class="admin-governance ranking-admin">
            <section>
              <header><div><h2>{{ t('admin.rankingTitle') }}</h2><p>{{ t('admin.rankingSummary') }}</p></div><span>v{{ rankingPolicy.current.version }}</span></header>
              <form class="admin-command-panel ranking-policy-form" @submit.prevent="submitRankingPolicy">
                <fieldset>
                  <legend>{{ t('admin.rankingActivationMode') }}</legend>
                  <div class="ranking-mode-control">
                    <label><UiRadio v-model="rankingActivationMode" value="candidate" /><FlaskConical :size="16" /><span><strong>{{ t('admin.rankingCandidateMode') }}</strong><small>{{ t('admin.rankingCandidateModeSummary') }}</small></span></label>
                    <label><UiRadio v-model="rankingActivationMode" value="immediate" /><Activity :size="16" /><span><strong>{{ t('admin.rankingImmediateMode') }}</strong><small>{{ t('admin.rankingImmediateModeSummary') }}</small></span></label>
                  </div>
                </fieldset>
                <label>{{ t('admin.rankingName') }}<UiInput v-model.trim="rankingForm.name" minlength="3" maxlength="80" required /></label>
                <fieldset>
                  <legend>{{ t('admin.textRelevance') }}</legend>
                  <div class="ranking-weight-grid">
                    <label>{{ t('admin.titleExactWeight') }}<UiInput v-model.number="rankingForm.titleExactWeight" type="number" min="0" max="200" required /></label>
                    <label>{{ t('admin.titlePrefixWeight') }}<UiInput v-model.number="rankingForm.titlePrefixWeight" type="number" min="0" max="200" required /></label>
                    <label>{{ t('admin.titleContainsWeight') }}<UiInput v-model.number="rankingForm.titleContainsWeight" type="number" min="0" max="200" required /></label>
                    <label>{{ t('admin.creatorExactWeight') }}<UiInput v-model.number="rankingForm.creatorExactWeight" type="number" min="0" max="200" required /></label>
                    <label>{{ t('admin.creatorMatchWeight') }}<UiInput v-model.number="rankingForm.creatorMatchWeight" type="number" min="0" max="200" required /></label>
                    <label>{{ t('admin.bodyMatchWeight') }}<UiInput v-model.number="rankingForm.bodyMatchWeight" type="number" min="0" max="200" required /></label>
                    <label>{{ t('admin.secondaryMatchWeight') }}<UiInput v-model.number="rankingForm.secondaryMatchWeight" type="number" min="0" max="200" required /></label>
                  </div>
                </fieldset>
                <fieldset>
                  <legend>{{ t('admin.qualitySignals') }}</legend>
                  <div class="ranking-weight-grid">
                    <label>{{ t('admin.recencyWeight') }}<UiInput v-model.number="rankingForm.recencyWeight" type="number" min="0" max="50" required /></label>
                    <label>{{ t('admin.creatorActivityWeight') }}<UiInput v-model.number="rankingForm.creatorActivityWeight" type="number" min="0" max="50" required /></label>
                  </div>
                </fieldset>
                <fieldset>
                  <legend>{{ t('admin.resultTypeBoosts') }}</legend>
                  <div class="ranking-weight-grid">
                    <label>{{ t('admin.workTypeBoost') }}<UiInput v-model.number="rankingForm.workTypeBoost" type="number" min="-50" max="50" required /></label>
                    <label>{{ t('admin.creatorTypeBoost') }}<UiInput v-model.number="rankingForm.creatorTypeBoost" type="number" min="-50" max="50" required /></label>
                    <label>{{ t('admin.productTypeBoost') }}<UiInput v-model.number="rankingForm.productTypeBoost" type="number" min="-50" max="50" required /></label>
                    <label>{{ t('admin.demandTypeBoost') }}<UiInput v-model.number="rankingForm.demandTypeBoost" type="number" min="-50" max="50" required /></label>
                  </div>
                </fieldset>
                <UiButton class="command-button primary" type="submit" :disabled="actionLoading" variant="primary">
                  <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><ShieldCheck v-else :size="17" />{{ rankingActivationMode === 'candidate' ? t('admin.createCandidateRevision') : t('admin.activateRevision') }}
                </UiButton>
              </form>
            </section>
            <section class="ranking-rollout-section">
              <header><div><h2>{{ t('admin.rankingRolloutTitle') }}</h2><p>{{ t('admin.rankingRolloutSummary') }}</p></div><span>{{ rankingPolicy.rollout.percent }}%</span></header>
              <div class="ranking-release-evidence">
                <article><span>{{ t('admin.rankingBaseline') }}</span><strong>v{{ rankingPolicy.current.version }} · {{ rankingPolicy.current.name }}</strong></article>
                <article><span>{{ t('admin.rankingCandidate') }}</span><strong>{{ rankingPolicy.candidate ? `v${rankingPolicy.candidate.version} · ${rankingPolicy.candidate.name}` : t('admin.noRankingCandidate') }}</strong></article>
              </div>
              <div v-if="rankingPolicy.candidate" class="ranking-control-grid">
                <form class="admin-command-panel compact-operation-form" @submit.prevent="runRankingEvaluation">
                  <h3><FlaskConical :size="17" />{{ t('admin.runOfflineEvaluation') }}</h3>
                  <p>{{ t('admin.runOfflineEvaluationSummary') }}</p>
                  <UiButton class="command-button secondary" type="submit" :disabled="actionLoading" variant="secondary">
                    <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><FlaskConical v-else :size="17" />{{ t('admin.runEvaluation') }}
                  </UiButton>
                </form>
                <form class="admin-command-panel compact-operation-form" @submit.prevent="updateRankingRollout">
                  <h3><Activity :size="17" />{{ t('admin.configureRollout') }}</h3>
                  <p>{{ t('admin.configureRolloutSummary') }}</p>
                  <label>{{ t('admin.rolloutPercent') }}<UiSelect v-model.number="rolloutForm.percent"><option v-for="percent in [0,5,10,25,50,100]" :key="percent" :value="percent">{{ percent === 100 ? t('admin.promoteCandidate') : `${percent}%` }}</option></UiSelect></label>
                  <UiButton class="command-button primary" type="submit" :disabled="actionLoading" variant="primary">
                    <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><Activity v-else :size="17" />{{ t('admin.applyRollout') }}
                  </UiButton>
                </form>
              </div>
              <p v-else class="inline-empty">
                {{ t('admin.noRankingCandidateSummary') }}
              </p>
              <div v-if="discoveryOperations.evaluations.length" class="admin-list ranking-evaluation-list">
                <article v-for="evaluation in discoveryOperations.evaluations" :key="evaluation.id">
                  <div><strong>{{ t('admin.evaluationVersions', { candidate: evaluation.candidateVersion, baseline: evaluation.baselineVersion }) }}</strong></div><span :data-status="evaluation.status === 'passed' ? 'active' : 'suspended'">{{ t(`admin.evaluationStates.${evaluation.status}`) }}</span><span>{{ t('admin.evaluationMrr', { candidate: decimal(evaluation.candidateMrr), baseline: decimal(evaluation.baselineMrr) }) }}</span><small>{{ date(evaluation.createdAt) }}</small><span>{{ t('admin.evaluationCases', { count: evaluation.caseCount }) }}</span>
                </article>
              </div>
              <UiButton v-if="evaluationNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="evaluationLoadingMore" variant="secondary" @click="loadMoreEvaluations">
                <LoaderCircle v-if="evaluationLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
              </UiButton>
            </section>
            <section class="ranking-index-section">
              <header><div><h2>{{ t('admin.discoveryIndexTitle') }}</h2><p>{{ t('admin.discoveryIndexSummary') }}</p></div><Database :size="20" /></header>
              <form class="admin-command-panel compact-operation-form" @submit.prevent="analyzeDiscoveryIndex">
                <UiButton class="command-button secondary" type="submit" :disabled="actionLoading" variant="secondary">
                  <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><Database v-else :size="17" />{{ t('admin.analyzeIndex') }}
                </UiButton>
              </form>
              <div v-if="discoveryOperations.indexRuns.length" class="admin-list ranking-index-list">
                <article v-for="run in discoveryOperations.indexRuns" :key="run.id">
                  <div><strong>{{ t('admin.indexRunTitle', { count: Object.values(run.documentCounts).reduce((total, count) => total + count, 0) }) }}</strong></div><span :data-status="run.status === 'succeeded' ? 'active' : 'suspended'">{{ t(`admin.indexRunStates.${run.status}`) }}</span><span>{{ t('admin.indexSize', { size: bytes(Object.values(run.indexSizes).reduce((total, size) => total + size, 0)) }) }}</span><small>{{ date(run.completedAt) }}</small><span>{{ t('admin.indexTypes', { count: Object.keys(run.documentCounts).length }) }}</span>
                </article>
              </div>
              <UiButton v-if="indexRunNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="indexRunLoadingMore" variant="secondary" @click="loadMoreIndexRuns">
                <LoaderCircle v-if="indexRunLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
              </UiButton>
              <p v-if="!discoveryOperations.indexRuns.length" class="inline-empty">
                {{ t('admin.noIndexRuns') }}
              </p>
            </section>
            <section>
              <header><div><h2>{{ t('admin.rankingHistory') }}</h2><p>{{ t('admin.rankingHistorySummary') }}</p></div><span>{{ rankingPolicy.history.length }}</span></header>
              <div class="admin-list ranking-history-list">
                <article v-for="revision in rankingPolicy.history" :key="revision.id">
                  <div><strong>{{ revision.name }}</strong></div>
                  <span>v{{ revision.version }}</span>
                  <span>{{ revision.createdByHandle ? `@${revision.createdByHandle}` : t('admin.systemActor') }}</span>
                  <small>{{ date(revision.createdAt) }}</small>
                  <span :data-status="revision.id === rankingPolicy.current.id ? 'active' : ''">{{ revision.id === rankingPolicy.current.id ? t('admin.activeRevision') : t('admin.supersededRevision') }}</span>
                </article>
              </div>
              <UiButton v-if="rankingNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="rankingLoadingMore" variant="secondary" @click="loadMoreRankingHistory">
                <LoaderCircle v-if="rankingLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
              </UiButton>
            </section>
          </div>

          <div v-else-if="activeTab === 'risk'" class="admin-governance">
            <section>
              <header><div><h2>{{ t('admin.riskQueue') }}</h2><p>{{ t('admin.riskQueueSummary') }}</p></div><span>{{ riskSignals.length }}</span></header>
              <form class="admin-user-filters admin-operations-filters" @submit.prevent="applyRiskFilters">
                <label>{{ t('admin.riskSearch') }}<UiInput v-model="riskQuery" type="search" maxlength="120" :placeholder="t('admin.riskSearchPlaceholder')" /></label>
                <label>{{ t('admin.status') }}<UiSelect v-model="riskStatus"><option value="">{{ t('admin.allRiskStatuses') }}</option><option v-for="status in ['open','reviewing','resolved','dismissed']" :key="status" :value="status">{{ t(`admin.riskStatuses.${status}`) }}</option></UiSelect></label>
                <label>{{ t('admin.riskSeverity') }}<UiSelect v-model="riskSeverity"><option value="">{{ t('admin.allRiskSeverities') }}</option><option v-for="severity in ['low','medium','high','critical']" :key="severity" :value="severity">{{ t(`admin.riskSeverities.${severity}`) }}</option></UiSelect></label>
                <UiButton class="command-button primary" type="submit" variant="primary">
                  <ListFilter :size="16" />{{ t('actions.applyFilters') }}
                </UiButton>
                <UiIconButton class="icon-button" type="button" :title="t('actions.clearFilters')" :label="t('actions.clearFilters')" @click="clearRiskFilters">
                  <Undo2 :size="16" />
                </UiIconButton>
              </form>
              <div class="admin-list risk-admin-list">
                <article v-for="item in riskSignals" :key="item.id">
                  <div>
                    <RouterLink class="text-link" :to="item.targetPath">
                      {{ item.resourceTitle }}
                    </RouterLink><span>@{{ item.subjectHandle }} · {{ t(`admin.riskSignals.${item.signalType}`) }}</span><small>{{ item.summary }}</small>
                  </div>
                  <span>{{ t(`admin.riskSeverities.${item.severity}`) }} · {{ t('admin.riskScore', { score: item.score }) }}</span>
                  <span :data-status="item.status">{{ t(`admin.riskStatuses.${item.status}`) }}</span>
                  <small>{{ date(item.detectedAt) }} · v{{ item.version }}</small>
                  <UiButton v-if="item.status === 'open' || item.status === 'reviewing'" class="command-button secondary" type="button" variant="secondary" @click="openRisk(item)">
                    <Activity :size="16" />{{ t('admin.reviewRisk') }}
                  </UiButton><UiButton v-else as="RouterLink" class="command-button secondary" variant="secondary" :to="item.targetPath">
                    {{ t('admin.openResource') }}
                  </UiButton>
                </article>
              </div>
              <div v-if="!riskSignals.length" class="workspace-empty">
                <Activity :size="22" /><p>
                  {{ t('admin.noRiskSignals') }}
                </p>
              </div>
              <UiButton v-if="riskNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="riskLoadingMore" variant="secondary" @click="loadMoreRisk">
                <LoaderCircle v-if="riskLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
              </UiButton>
            </section>
          </div>

          <div v-else-if="activeTab === 'riskRules' && riskRulePolicy" class="admin-governance risk-rules-admin">
            <section>
              <header><div><h2>{{ t('admin.riskRulesTitle') }}</h2><p>{{ t('admin.riskRulesSummary') }}</p></div><span>v{{ riskRulePolicy.current.version }}</span></header>
              <form class="admin-command-panel ranking-policy-form" @submit.prevent="submitRiskRulePolicy">
                <label>{{ t('admin.riskRuleName') }}<UiInput v-model.trim="riskRuleForm.name" minlength="3" maxlength="80" required /></label>
                <fieldset>
                  <legend>{{ t('admin.riskSignalScores') }}</legend>
                  <div class="ranking-weight-grid">
                    <label>{{ t('admin.taskDisputeScore') }}<UiInput v-model.number="riskRuleForm.taskDisputeScore" type="number" min="0" max="100" required /></label>
                    <label>{{ t('admin.transactionRefundScore') }}<UiInput v-model.number="riskRuleForm.transactionRefundScore" type="number" min="0" max="100" required /></label>
                    <label>{{ t('admin.communityReportScore') }}<UiInput v-model.number="riskRuleForm.communityReportScore" type="number" min="0" max="100" required /></label>
                    <label>{{ t('admin.mediaRejectionScore') }}<UiInput v-model.number="riskRuleForm.mediaRejectionScore" type="number" min="0" max="100" required /></label>
                    <label>{{ t('admin.accountLinkScore') }}<UiInput v-model.number="riskRuleForm.accountLinkScore" type="number" min="0" max="100" required /></label>
                  </div>
                </fieldset>
                <fieldset>
                  <legend>{{ t('admin.accountLinkBoundary') }}</legend>
                  <div class="ranking-weight-grid">
                    <label>{{ t('admin.accountLinkMinAccounts') }}<UiInput v-model.number="riskRuleForm.accountLinkMinAccounts" type="number" min="2" max="20" required /></label>
                    <label>{{ t('admin.accountLinkWindowHours') }}<UiInput v-model.number="riskRuleForm.accountLinkWindowHours" type="number" min="1" max="168" required /></label>
                  </div>
                  <small>{{ t('admin.accountLinkPrivacy') }}</small>
                </fieldset>
                <fieldset>
                  <legend>{{ t('admin.severityThresholds') }}</legend>
                  <div class="ranking-weight-grid">
                    <label>{{ t('admin.mediumThreshold') }}<UiInput v-model.number="riskRuleForm.mediumThreshold" type="number" min="1" max="98" required /></label>
                    <label>{{ t('admin.highThreshold') }}<UiInput v-model.number="riskRuleForm.highThreshold" type="number" min="2" max="99" required /></label>
                    <label>{{ t('admin.criticalThreshold') }}<UiInput v-model.number="riskRuleForm.criticalThreshold" type="number" min="3" max="100" required /></label>
                  </div>
                  <small>{{ t('admin.thresholdOrder') }}</small>
                </fieldset>
                <UiButton class="command-button primary" type="submit" :disabled="actionLoading" variant="primary">
                  <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><ShieldCheck v-else :size="17" />{{ t('admin.activateRiskRules') }}
                </UiButton>
              </form>
            </section>
            <section>
              <header><div><h2>{{ t('admin.riskRulesHistory') }}</h2><p>{{ t('admin.riskRulesHistorySummary') }}</p></div><span>{{ riskRulePolicy.history.length }}</span></header>
              <div class="admin-list ranking-history-list">
                <article v-for="revision in riskRulePolicy.history" :key="revision.id">
                  <div><strong>{{ revision.name }}</strong></div>
                  <span>v{{ revision.version }}</span>
                  <span>{{ t('admin.riskRuleScoreSummary', { dispute: revision.taskDisputeScore, refund: revision.transactionRefundScore, report: revision.communityReportScore, media: revision.mediaRejectionScore, link: revision.accountLinkScore }) }}</span>
                  <small>{{ date(revision.createdAt) }} · {{ revision.createdByHandle ? `@${revision.createdByHandle}` : t('admin.systemActor') }}</small>
                  <span :data-status="revision.id === riskRulePolicy.current.id ? 'active' : ''">{{ revision.id === riskRulePolicy.current.id ? t('admin.activeRevision') : t('admin.supersededRevision') }}</span>
                </article>
              </div>
              <UiButton v-if="riskRuleNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="riskRuleLoadingMore" variant="secondary" @click="loadMoreRiskRuleHistory">
                <LoaderCircle v-if="riskRuleLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
              </UiButton>
            </section>
          </div>

          <div v-else-if="activeTab === 'dataRights'" class="admin-governance data-rights-admin">
            <section>
              <header><div><h2>{{ t('admin.dataRightsQueue') }}</h2><p>{{ t('admin.dataRightsQueueSummary') }}</p></div><span>{{ dataRightsItems.filter(item => !['completed','cancelled','failed'].includes(item.status)).length }}</span></header>
              <div v-if="dataRightsItems.length" class="admin-list">
                <article v-for="item in dataRightsItems" :key="item.id">
                  <div><strong>@{{ item.ownerHandle }}</strong><span>{{ t(`account.rightsTypes.${item.requestType}`) }} · {{ item.subjectRef }}</span><small v-if="item.export">SHA-256 {{ item.export.checksumSha256.slice(0, 16) }}…</small><small v-else>{{ t('account.cancelUntil', { date: date(item.cancelUntil || item.executeAfter) }) }}</small></div>
                  <span>{{ t(`account.rightsTypes.${item.requestType}`) }}</span><span :data-status="item.status">{{ t(`account.rightsStatuses.${item.status}`) }}</span><small>{{ date(item.createdAt) }}</small><UiButton v-if="item.requestType === 'account_deletion' && item.status === 'scheduled'" class="command-button secondary" type="button" variant="secondary" @click="openDataRightsHold(item)">
                    <ShieldAlert :size="16" />{{ t('admin.placeLegalHold') }}
                  </UiButton><span v-else></span>
                </article>
              </div>
              <p v-else class="inline-empty">
                {{ t('admin.noDataRights') }}
              </p>
              <UiButton v-if="dataRightsNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="dataRightsLoadingMore" variant="secondary" @click="loadMoreDataRights">
                <LoaderCircle v-if="dataRightsLoadingMore" class="spin" :size="16" />{{ t('actions.loadMore') }}
              </UiButton>
            </section>
            <section>
              <header><div><h2>{{ t('admin.legalHolds') }}</h2><p>{{ t('admin.legalHoldsSummary') }}</p></div><span>{{ legalHolds.filter(item => item.status === 'active').length }}</span></header>
              <div v-if="legalHolds.length" class="admin-list">
                <article v-for="item in legalHolds" :key="item.id">
                  <div><strong>@{{ item.ownerHandle }}</strong><small>SHA-256 {{ item.authorityReferenceHash.slice(0, 16) }}…</small></div><span>{{ t('admin.reviewDue', { date: date(item.reviewAt) }) }}</span><span :data-status="item.status">{{ t(`admin.holdStates.${item.status}`) }}</span><small>{{ t('admin.expiresAt', { date: date(item.expiresAt) }) }}</small><UiButton v-if="item.status === 'active'" class="command-button secondary" type="button" variant="secondary" @click="openHoldRelease(item)">
                    <Undo2 :size="16" />{{ t('admin.releaseHold') }}
                  </UiButton><span v-else></span>
                </article>
              </div>
              <p v-else class="inline-empty">
                {{ t('admin.noLegalHolds') }}
              </p>
              <UiButton v-if="legalHoldsNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="legalHoldsLoadingMore" variant="secondary" @click="loadMoreLegalHolds">
                <LoaderCircle v-if="legalHoldsLoadingMore" class="spin" :size="16" />{{ t('actions.loadMore') }}
              </UiButton>
            </section>
          </div>

          <div v-else-if="activeTab === 'diagnostics' && operationalDiagnostics" class="admin-governance diagnostics-admin">
            <section>
              <header><div><h2>{{ t('admin.requestHealth') }}</h2><p>{{ t('admin.requestHealthSummary', { minutes: operationalDiagnostics.windowMinutes }) }}</p></div><span :data-status="operationalDiagnostics.requests.serverErrors ? 'failed' : 'active'">{{ operationalDiagnostics.requests.serverErrors ? t('admin.attentionRequired') : t('admin.healthy') }}</span></header>
              <div class="admin-overview-grid diagnostics-grid">
                <article><span>{{ t('admin.requestTotal') }}</span><strong>{{ operationalDiagnostics.requests.total }}</strong><small>{{ t('admin.statusBreakdown', { success: operationalDiagnostics.requests.byStatus['2xx'] || 0, client: operationalDiagnostics.requests.byStatus['4xx'] || 0, server: operationalDiagnostics.requests.byStatus['5xx'] || 0 }) }}</small></article>
                <article><span>{{ t('admin.clientErrors') }}</span><strong>{{ operationalDiagnostics.requests.clientErrors }}</strong><small>{{ t('admin.clientErrorClass') }}</small></article>
                <article><span>{{ t('admin.serverErrors') }}</span><strong>{{ operationalDiagnostics.requests.serverErrors }}</strong><small>{{ t('admin.serverErrorClass') }}</small></article>
                <article><span>{{ t('admin.p95Duration') }}</span><strong>{{ t('admin.durationMilliseconds', { value: Math.round(operationalDiagnostics.requests.p95DurationMs) }) }}</strong><small>{{ t('admin.averageDuration', { value: Math.round(operationalDiagnostics.requests.averageDurationMs) }) }}</small></article>
              </div>
            </section>
            <section>
              <header><div><h2>{{ t('admin.jobHealth') }}</h2><p>{{ t('admin.jobHealthSummary') }}</p></div><span :data-status="operationalDiagnostics.jobs.expiredLeases ? 'failed' : 'active'">{{ operationalDiagnostics.jobs.expiredLeases ? t('admin.attentionRequired') : t('admin.healthy') }}</span></header>
              <div class="admin-overview-grid diagnostics-grid">
                <article v-for="status in ['queued','running','succeeded','failed','cancelled']" :key="status">
                  <span>{{ t(`admin.jobStates.${status}`) }}</span><strong>{{ operationalDiagnostics.jobs.byStatus[status] || 0 }}</strong>
                </article>
                <article><span>{{ t('admin.retryingJobs') }}</span><strong>{{ operationalDiagnostics.jobs.retrying }}</strong><small>{{ operationalDiagnostics.jobs.oldestQueuedAt ? t('admin.oldestQueued', { date: date(operationalDiagnostics.jobs.oldestQueuedAt) }) : t('admin.noQueuedJobs') }}</small></article>
                <article><span>{{ t('admin.expiredLeases') }}</span><strong>{{ operationalDiagnostics.jobs.expiredLeases }}</strong></article>
                <article><span>{{ t('admin.recentJobAttempts') }}</span><strong>{{ operationalDiagnostics.jobs.attemptsLast24Hours }}</strong><small>{{ t('admin.attemptWindow') }}</small></article>
                <article><span>{{ t('admin.leaseRenewals') }}</span><strong>{{ operationalDiagnostics.jobs.leaseRenewalsLast24Hours }}</strong><small>{{ t('admin.attemptWindow') }}</small></article>
                <article><span>{{ t('admin.recentLeaseExpirations') }}</span><strong>{{ operationalDiagnostics.jobs.leaseExpirationsLast24Hours }}</strong><small>{{ t('admin.attemptWindow') }}</small></article>
                <article><span>{{ t('admin.recentTerminalFailures') }}</span><strong>{{ operationalDiagnostics.jobs.terminalFailuresLast24Hours }}</strong><small>{{ t('admin.attemptWindow') }}</small></article>
              </div>
            </section>
            <section>
              <header><div><h2>{{ t('admin.databaseReadiness') }}</h2></div><span :data-status="operationalDiagnostics.databaseReady ? 'active' : 'failed'">{{ operationalDiagnostics.databaseReady ? t('admin.ready') : t('admin.unavailable') }}</span></header>
              <p>{{ t('admin.observedAt', { date: date(operationalDiagnostics.asOf) }) }}</p>
            </section>
          </div>
        </div>
      </template>
    </div>
  </section>
</template>
