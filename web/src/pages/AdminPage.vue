<script setup lang="ts">
import {
  Activity, Ban, BriefcaseBusiness, CircleDollarSign, Database, FileCheck2, FileKey2, FlaskConical, Gauge, LoaderCircle, RefreshCw,
  Headphones, KeyRound, ListFilter, MessageSquare, Send, Settings2, ShieldAlert, ShieldCheck, SlidersHorizontal, Undo2, Users, WandSparkles, X,
} from 'lucide-vue-next'
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import {
  api, messageFrom, type AdminAuditEvent, type AdminContent, type AdminFinanceAccount, type AdminOperationalDiagnostics,
  type AdminGeneration, type AdminGovernanceAppeal, type AdminGovernanceReport, type AdminMediaItem, type AdminModelRoutePolicy, type AdminModelRouteUpdate, type AdminOverview, type AdminProvider, type AdminUser,
  type AdminDiscoveryOperations, type AdminRankingPolicy, type AdminRankingUpdate, type AdminRiskRulePolicy, type AdminRiskRuleUpdate, type AdminRiskSignal, type AdminSystemSettingPolicy, type AdminSystemSettingUpdate, type AdminTaskOperation, type DataRightsLegalHold, type DataRightsRequest, type DeveloperAccess, type DeveloperControlUpdate, type DeveloperWebhookDelivery, type IdentityEmailAction, type SupportCase,
} from '../api/client'
import { formatCurrency, formatDateTime } from '../lib/format'
import { useSessionStore } from '../stores/session'

type Tab = 'overview' | 'users' | 'content' | 'media' | 'governance' | 'support' | 'generations' | 'tasks' | 'providers' | 'models' | 'settings' | 'developer' | 'finance' | 'risk' | 'riskRules' | 'ranking' | 'dataRights' | 'diagnostics' | 'audit'
type CommandKind = 'user' | 'content' | 'media' | 'report' | 'appeal' | 'generation' | 'task' | 'provider' | 'finance' | 'risk' | 'dataRightsHold' | 'holdRelease'
type OverviewKey = 'users' | 'works' | 'generations' | 'orders' | 'tasks' | 'risks' | 'providers'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const loading = ref(true)
const actionLoading = ref(false)
const error = ref('')
const success = ref('')
const overview = ref<AdminOverview | null>(null)
const users = ref<AdminUser[]>([])
const userQuery = ref('')
const userRole = ref('')
const userStatus = ref('')
const userNextCursor = ref<string | null>(null)
const userLoadingMore = ref(false)
const content = ref<AdminContent[]>([])
const contentQuery = ref('')
const contentType = ref('')
const contentStatus = ref('')
const contentNextCursor = ref<string | null>(null)
const contentLoadingMore = ref(false)
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
const modelRoutePolicy = ref<AdminModelRoutePolicy | null>(null)
const modelRouteLoadingMore = ref<Record<string, boolean>>({})
const systemSettingPolicy = ref<AdminSystemSettingPolicy | null>(null)
const systemSettingNextCursor = ref<string | null>(null)
const systemSettingLoadingMore = ref(false)
const modelRouteMode = ref<'chat' | 'image' | 'video' | 'music'>('image')
const finance = ref<AdminFinanceAccount[]>([])
const financeQuery = ref('')
const financeState = ref('')
const financeNextCursor = ref<string | null>(null)
const financeLoadingMore = ref(false)
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
const evaluationForm = reactive({ reason: '', confirmed: false })
const indexForm = reactive({ reason: '', confirmed: false })
const rolloutForm = reactive({ percent: 25, reason: '', confirmed: false })
const auditEvents = ref<AdminAuditEvent[]>([])
const auditQuery = ref('')
const auditAction = ref('')
const auditResourceType = ref('')
const auditNextCursor = ref<string | null>(null)
const auditLoadingMore = ref(false)
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
const mediaItems = ref<AdminMediaItem[]>([])
const mediaQuery = ref('')
const mediaKind = ref('')
const mediaStatus = ref('')
const mediaNextCursor = ref<string | null>(null)
const mediaLoadingMore = ref(false)
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
const supportReply = reactive({ body: '', reason: '', confirmed: false })
const supportDecision = reactive({ status: 'in_review', resolutionCode: '', reason: '', confirmed: false })
const command = reactive({ kind: '' as CommandKind | '', id: '', title: '', role: '', status: '', outcome: '', decision: '', enabled: false, deltaCents: 0, authorityReference: '', reason: '', confirmed: false })
const commandPanel = ref<InstanceType<typeof globalThis.HTMLFormElement> | null>(null)
const tabsNav = ref<InstanceType<typeof globalThis.HTMLElement> | null>(null)
const rankingForm = reactive<AdminRankingUpdate>({
  name: '', titleExactWeight: 100, titlePrefixWeight: 80, titleContainsWeight: 60,
  creatorExactWeight: 50, creatorMatchWeight: 35, bodyMatchWeight: 25, secondaryMatchWeight: 12,
  recencyWeight: 10, creatorActivityWeight: 15, workTypeBoost: 0, creatorTypeBoost: 0,
  productTypeBoost: 0, demandTypeBoost: 0, reason: '', expectedVersion: 1, confirmed: false,
})
const riskRuleForm = reactive<AdminRiskRuleUpdate>({
  name: '', taskDisputeScore: 85, transactionRefundScore: 55,
  communityReportScore: 35, mediaRejectionScore: 75,
  accountLinkScore: 65, accountLinkMinAccounts: 3, accountLinkWindowHours: 24,
  mediumThreshold: 40, highThreshold: 70, criticalThreshold: 90,
  reason: '', expectedVersion: 1, confirmed: false,
})
const modelRouteForm = reactive<AdminModelRouteUpdate>({ providerProfileId: '', name: '', timeoutSeconds: 120, maxAttempts: 3, reason: '', expectedVersion: 1, confirmed: false })
const systemSettingForm = reactive<AdminSystemSettingUpdate>({ name: '', registrationsEnabled: true, generationsEnabled: true, publishingEnabled: true, marketplaceCheckoutEnabled: true, taskCreationEnabled: true, publicNotice: '', reason: '', expectedVersion: 1, confirmed: false })
const developerControlForm = reactive<DeveloperControlUpdate>({ enabled: false, maxServiceAccounts: 5, maxActiveKeys: 3, defaultTtlDays: 90, reason: '', expectedVersion: 1, confirmed: false })
const developerEmergencyForm = reactive({ reason: '', confirmed: false })
const webhookReplayForm = reactive({ reason: '', confirmed: false })
const emailRecoveryForm = reactive({ reason: '', confirmed: false })

const tabPermissions: Record<Tab, string> = {
  overview: 'admin:overview', users: 'admin:users', content: 'admin:content', media: 'admin:media', generations: 'admin:generations',
  governance: 'admin:governance', support: 'admin:support', tasks: 'admin:tasks', providers: 'admin:providers', models: 'admin:models', settings: 'admin:settings', developer: 'admin:developer', finance: 'admin:finance', risk: 'admin:risk', riskRules: 'admin:risk_rules', ranking: 'admin:ranking', dataRights: 'admin:data-rights', diagnostics: 'admin:observability', audit: 'admin:audit',
}
const tabIcons = { overview: Gauge, users: Users, content: FileCheck2, media: ShieldCheck, governance: ShieldAlert, support: Headphones, generations: WandSparkles, tasks: BriefcaseBusiness, providers: SlidersHorizontal, models: WandSparkles, settings: Settings2, developer: KeyRound, finance: CircleDollarSign, risk: Activity, riskRules: Settings2, ranking: ListFilter, dataRights: FileKey2, diagnostics: Activity, audit: ShieldCheck }
const tabs = computed(() => (Object.keys(tabPermissions) as Tab[]).filter((tab) => session.user?.permissions.includes(tabPermissions[tab])))
const activeTab = computed<Tab>(() => {
  const requested = String(route.query.tab || 'overview') as Tab
  return tabs.value.includes(requested) ? requested : tabs.value[0] || 'overview'
})
const hasAdminAccess = computed(() => Boolean(session.user?.permissions.includes('admin:access')))
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
const providerModeKeys: Record<string, string> = {
  chat: 'create.modes.chat', image: 'create.modes.image', video: 'create.modes.video', music: 'create.modes.music',
}
const overviewStatusKeys: Record<OverviewKey, Record<string, string>> = {
  users: { active: 'admin.states.active', suspended: 'admin.states.suspended', deleted: 'admin.states.deleted' },
  works: { draft: 'admin.states.draft', published: 'admin.states.published', hidden: 'admin.states.hidden', removed: 'admin.states.removed' },
  generations: { queued: 'generation.status.queued', running: 'generation.status.running', succeeded: 'generation.status.succeeded', failed: 'generation.status.failed', cancelled: 'generation.status.cancelled' },
  orders: { test_pending: 'admin.orderStates.test_pending', test_paid: 'admin.orderStates.test_paid', fulfilled: 'admin.orderStates.fulfilled', refund_requested: 'admin.orderStates.refund_requested', test_refunded: 'admin.orderStates.test_refunded', cancelled: 'admin.orderStates.cancelled' },
  tasks: { draft: 'admin.taskStates.draft', open: 'admin.taskStates.open', assigned: 'admin.taskStates.assigned', submitted: 'admin.taskStates.submitted', revision: 'admin.taskStates.revision', accepted: 'admin.taskStates.accepted', disputed: 'admin.taskStates.disputed', cancelled: 'admin.taskStates.cancelled' },
  risks: { open: 'admin.riskStatuses.open', reviewing: 'admin.riskStatuses.reviewing', resolved: 'admin.riskStatuses.resolved', dismissed: 'admin.riskStatuses.dismissed' },
  providers: { enabled: 'admin.providerStates.enabled', disabled: 'admin.providerStates.disabled' },
}

function localizedLabel(keys: Record<string, string>, value: string) {
  return t(keys[value] || 'admin.unknownState')
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
  const page = await api.adminListUsers(userListQuery(cursor))
  if (cursor) {
    const known = new Set(users.value.map(item => item.id))
    users.value = [...users.value, ...page.items.filter(item => !known.has(item.id))]
  } else {
    users.value = page.items
  }
  userNextCursor.value = page.nextCursor || null
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
  const page = await api.adminListContent(contentListQuery(cursor))
  if (cursor) {
    const known = new Set(content.value.map(item => item.id))
    content.value = [...content.value, ...page.items.filter(item => !known.has(item.id))]
  } else {
    content.value = page.items
  }
  contentNextCursor.value = page.nextCursor || null
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
  const page = await api.adminListMedia(mediaListQuery(cursor))
  if (cursor) {
    const known = new Set(mediaItems.value.map(item => item.id))
    mediaItems.value = [...mediaItems.value, ...page.items.filter(item => !known.has(item.id))]
  } else {
    mediaItems.value = page.items
  }
  mediaNextCursor.value = page.nextCursor || null
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

function syncAuditFilters() {
  auditQuery.value = typeof route.query.auditQ === 'string' ? route.query.auditQ : ''
  auditAction.value = typeof route.query.auditAction === 'string' ? route.query.auditAction : ''
  auditResourceType.value = typeof route.query.auditResourceType === 'string' ? route.query.auditResourceType : ''
}

function auditListQuery(cursor = '') {
  return {
    q: auditQuery.value || undefined,
    action: auditAction.value || undefined,
    resourceType: auditResourceType.value || undefined,
    cursor: cursor || undefined,
    limit: 20,
  }
}

async function loadAuditDirectory(cursor = '') {
  const page = await api.adminListAudit(auditListQuery(cursor))
  if (cursor) {
    const known = new Set(auditEvents.value.map(item => item.id))
    auditEvents.value = [...auditEvents.value, ...page.items.filter(item => !known.has(item.id))]
  } else {
    auditEvents.value = page.items
  }
  auditNextCursor.value = page.nextCursor || null
}

async function applyAuditFilters() {
  const query: Record<string, string> = { tab: 'audit' }
  if (auditQuery.value.trim()) query.auditQ = auditQuery.value.trim()
  if (auditAction.value.trim()) query.auditAction = auditAction.value.trim()
  if (auditResourceType.value.trim()) query.auditResourceType = auditResourceType.value.trim()
  await router.push({ query })
  await load()
}

async function clearAuditFilters() {
  auditQuery.value = ''
  auditAction.value = ''
  auditResourceType.value = ''
  await router.push({ query: { tab: 'audit' } })
  await load()
}

async function loadMoreAudit() {
  if (!auditNextCursor.value || auditLoadingMore.value) return
  auditLoadingMore.value = true
  error.value = ''
  try { await loadAuditDirectory(auditNextCursor.value) } catch (reason) { error.value = messageFrom(reason) } finally { auditLoadingMore.value = false }
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
    if (tab === 'overview') overview.value = await api.adminOverview()
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
    if (tab === 'providers') providers.value = (await api.adminListProviders()).items
    if (tab === 'models') {
      const [policy, providerResult] = await Promise.all([api.adminGetModelRoutes({ limit: 20 }), api.adminListProviders()])
      modelRoutePolicy.value = policy
      providers.value = providerResult.items
      resetModelRouteForm()
    }
    if (tab === 'settings') { await loadSystemSettingHistory(); resetSystemSettingForm() }
    if (tab === 'developer') {
		syncDeveloperRecoveryFilters()
		const [access] = await Promise.all([api.adminGetDeveloperAccess(), loadWebhookRecoveryDirectory(), loadEmailRecoveryDirectory()])
		developerAdminAccess.value = access
		resetDeveloperControlForm()
    }
    if (tab === 'finance') {
      syncFinanceFilters()
      await loadFinanceDirectory()
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
    if (tab === 'audit') {
      syncAuditFilters()
      await loadAuditDirectory()
    }
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    loading.value = false
    void nextTick(() => {
      tabsNav.value?.querySelector<InstanceType<typeof globalThis.HTMLElement>>(`[data-tab="${activeTab.value}"]`)?.scrollIntoView({ block: 'nearest', inline: 'center' })
    })
  }
}

async function selectTab(tab: Tab) {
	await router.push({ query: { tab } })
  await load()
}

async function useAdminDemo() {
  loading.value = true
  error.value = ''
  const user = await session.startDemoSession('admin')
  if (!user) error.value = session.error
  await router.replace({ query: { tab: 'overview' } })
  await load()
}

function closeCommand() {
  Object.assign(command, { kind: '', id: '', title: '', role: '', status: '', outcome: '', decision: '', enabled: false, deltaCents: 0, authorityReference: '', reason: '', confirmed: false })
}

function openUser(item: AdminUser) {
  Object.assign(command, { kind: 'user', id: item.id, title: item.displayName, role: item.role, status: item.status, reason: '', confirmed: false })
}

function openContent(item: AdminContent) {
  Object.assign(command, { kind: 'content', id: item.id, title: item.title, status: item.status === 'draft' ? 'hidden' : item.status, reason: '', confirmed: false })
}

function openGeneration(item: AdminGeneration) {
  Object.assign(command, { kind: 'generation', id: item.id, title: item.prompt, reason: '', confirmed: false })
}

function openTaskOperation(item: AdminTaskOperation) {
  if (!item.disputeVersion) return
  Object.assign(command, { kind: 'task', id: item.id, title: item.title, decision: 'cancel_without_settlement', status: String(item.disputeVersion), reason: '', confirmed: false })
  void nextTick(() => {
    commandPanel.value?.scrollIntoView({ behavior: globalThis.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth', block: 'start' })
    commandPanel.value?.querySelector<InstanceType<typeof globalThis.HTMLElement>>('select, textarea, input, button')?.focus({ preventScroll: true })
  })
}

function openMedia(item: AdminMediaItem) {
  Object.assign(command, { kind: 'media', id: item.id, title: item.title, status: item.scanStatus === 'pending' ? 'review' : item.scanStatus, reason: '', confirmed: false })
}

function openReport(item: AdminGovernanceReport) {
  Object.assign(command, { kind: 'report', id: item.id, title: item.resourceTitle, outcome: 'no_action', reason: '', confirmed: false })
}

function openAppeal(item: AdminGovernanceAppeal) {
  Object.assign(command, { kind: 'appeal', id: item.id, title: item.resourceTitle, decision: 'denied', reason: '', confirmed: false })
}

function openProvider(item: AdminProvider) {
  Object.assign(command, { kind: 'provider', id: item.id, title: item.displayName, enabled: !item.adminEnabled, reason: '', confirmed: false })
}

function openFinance(item: AdminFinanceAccount) {
  Object.assign(command, { kind: 'finance', id: item.userId, title: item.displayName, deltaCents: 0, reason: '', confirmed: false })
}

function openRisk(item: AdminRiskSignal) {
  Object.assign(command, { kind: 'risk', id: item.id, title: item.resourceTitle, decision: 'monitor', status: String(item.version), reason: '', confirmed: false })
  void nextTick(() => {
    commandPanel.value?.scrollIntoView({ behavior: globalThis.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth', block: 'start' })
    commandPanel.value?.querySelector<InstanceType<typeof globalThis.HTMLElement>>('select, textarea, input, button')?.focus({ preventScroll: true })
  })
}

function openDataRightsHold(item: DataRightsRequest) {
  if (!item.ownerId) return
  Object.assign(command, { kind: 'dataRightsHold', id: item.ownerId, title: `@${item.ownerHandle}`, authorityReference: '', reason: '', confirmed: false })
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
    demandTypeBoost: current.demandTypeBoost, reason: '', expectedVersion: current.version, confirmed: false,
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
    reason: '', expectedVersion: current.version, confirmed: false,
  })
}

function resetModelRouteForm() {
  const current = modelRoutePolicy.value?.routes[modelRouteMode.value]
  if (!current) return
  Object.assign(modelRouteForm, { providerProfileId: current.providerProfileId, name: current.name, timeoutSeconds: current.timeoutSeconds, maxAttempts: current.maxAttempts, reason: '', expectedVersion: current.version, confirmed: false })
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
  const current = systemSettingPolicy.value?.current; if (!current) return
  Object.assign(systemSettingForm, { name: current.name, registrationsEnabled: current.registrationsEnabled, generationsEnabled: current.generationsEnabled, publishingEnabled: current.publishingEnabled, marketplaceCheckoutEnabled: current.marketplaceCheckoutEnabled, taskCreationEnabled: current.taskCreationEnabled, publicNotice: current.publicNotice, reason: '', expectedVersion: current.version, confirmed: false })
}

async function loadSystemSettingHistory(cursor = '') {
  const page = await api.adminGetSystemSettings({ cursor: cursor || undefined, limit: 20 })
  if (cursor && systemSettingPolicy.value) {
    const known = new Set(systemSettingPolicy.value.history.map(item => item.id))
    systemSettingPolicy.value = { ...page, history: [...systemSettingPolicy.value.history, ...page.history.filter(item => !known.has(item.id))] }
  } else {
    systemSettingPolicy.value = page
  }
  systemSettingNextCursor.value = page.nextCursor || null
}

async function loadMoreSystemSettingHistory() {
  if (!systemSettingNextCursor.value || systemSettingLoadingMore.value) return
  systemSettingLoadingMore.value = true; error.value = ''
  try { await loadSystemSettingHistory(systemSettingNextCursor.value) }
  catch (reason) { error.value = messageFrom(reason) }
  finally { systemSettingLoadingMore.value = false }
}

async function submitSystemSettings() {
  actionLoading.value = true; error.value = ''; success.value = ''
  try {
    systemSettingPolicy.value = await api.adminUpdateSystemSettings({ ...systemSettingForm })
    systemSettingNextCursor.value = systemSettingPolicy.value.nextCursor || null
    resetSystemSettingForm(); success.value = t('admin.systemSettingsUpdated')
  }
  catch (reason) { error.value = messageFrom(reason) } finally { actionLoading.value = false }
}

function resetDeveloperControlForm() {
  const current = developerAdminAccess.value?.control; if (!current) return
  Object.assign(developerControlForm, { enabled: current.enabled, maxServiceAccounts: current.maxServiceAccounts, maxActiveKeys: current.maxActiveKeys, defaultTtlDays: current.defaultTtlDays, reason: '', expectedVersion: current.version, confirmed: false })
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
  if (!developerEmergencyForm.confirmed) return
  actionLoading.value = true; error.value = ''; success.value = ''
  try {
    await api.adminRevokeDeveloperServiceAccount(id, { expectedVersion, reason: developerEmergencyForm.reason, confirmed: true })
    developerAdminAccess.value = await api.adminGetDeveloperAccess()
    Object.assign(developerEmergencyForm, { reason: '', confirmed: false }); success.value = t('admin.developerAccountRevoked')
  } catch (reason) { error.value = messageFrom(reason) } finally { actionLoading.value = false }
}

async function adminRevokeDeveloperKey(id: string, expectedVersion: number) {
  if (!developerEmergencyForm.confirmed) return
  actionLoading.value = true; error.value = ''; success.value = ''
  try {
    await api.adminRevokeDeveloperAPIKey(id, { expectedVersion, reason: developerEmergencyForm.reason, confirmed: true })
    developerAdminAccess.value = await api.adminGetDeveloperAccess()
    Object.assign(developerEmergencyForm, { reason: '', confirmed: false }); success.value = t('admin.developerKeyRevoked')
  } catch (reason) { error.value = messageFrom(reason) } finally { actionLoading.value = false }
}

async function adminReplayWebhook(delivery: DeveloperWebhookDelivery) {
  if (!webhookReplayForm.confirmed) return
  actionLoading.value = true; error.value = ''; success.value = ''
  try {
    await api.adminReplayWebhookDelivery(delivery.id, { expectedVersion: delivery.version, reason: webhookReplayForm.reason, confirmed: true })
		await loadWebhookRecoveryDirectory()
    Object.assign(webhookReplayForm, { reason: '', confirmed: false }); success.value = t('admin.webhookReplayQueued')
  } catch (reason) { error.value = messageFrom(reason) } finally { actionLoading.value = false }
}

async function adminRecoverEmailAction(item: IdentityEmailAction, operation: 'retry' | 'cancel') {
  if (!emailRecoveryForm.confirmed) return
  actionLoading.value = true; error.value = ''; success.value = ''
  try {
    const input = { expectedVersion: item.version, reason: emailRecoveryForm.reason, confirmed: true as const }
    if (operation === 'retry') await api.adminRetryEmailAction(item.id, input)
    else await api.adminCancelEmailAction(item.id, input)
		await loadEmailRecoveryDirectory()
    Object.assign(emailRecoveryForm, { reason: '', confirmed: false })
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
  if (!evaluationForm.confirmed) return
  actionLoading.value = true
  error.value = ''
  success.value = ''
  try {
    await api.adminRunRankingEvaluation(evaluationForm.reason)
    await loadDiscoveryHistories()
    Object.assign(evaluationForm, { reason: '', confirmed: false })
    success.value = t('admin.rankingEvaluationComplete')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionLoading.value = false
  }
}

async function updateRankingRollout() {
  if (!rankingPolicy.value || !rolloutForm.confirmed) return
  actionLoading.value = true
  error.value = ''
  success.value = ''
  try {
    rankingPolicy.value = await api.adminUpdateRankingRollout({
      percent: rolloutForm.percent as 0 | 5 | 10 | 25 | 50 | 100,
      expectedVersion: rankingPolicy.value.rollout.version,
      reason: rolloutForm.reason,
      confirmed: true,
    })
    rankingNextCursor.value = rankingPolicy.value.nextCursor || null
    Object.assign(rolloutForm, { percent: 25, reason: '', confirmed: false })
    success.value = t('admin.rankingRolloutUpdated')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionLoading.value = false
  }
}

async function analyzeDiscoveryIndex() {
  if (!indexForm.confirmed) return
  actionLoading.value = true
  error.value = ''
  success.value = ''
  try {
    await api.adminAnalyzeDiscoveryIndex(indexForm.reason)
    await loadDiscoveryHistories()
    Object.assign(indexForm, { reason: '', confirmed: false })
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
  Object.assign(command, { kind: 'holdRelease', id: item.id, title: `@${item.ownerHandle}`, reason: '', confirmed: false })
}

async function openSupport(item: SupportCase) {
  actionLoading.value = true
  error.value = ''
  try {
    selectedSupport.value = await api.adminGetSupportCase(item.id)
    Object.assign(supportReply, { body: '', reason: '', confirmed: false })
    Object.assign(supportDecision, { status: selectedSupport.value.status === 'open' ? 'in_review' : 'waiting_for_requester', resolutionCode: '', reason: '', confirmed: false })
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
    const updated = await api.adminReplySupportCase(selectedSupport.value.id, { ...supportReply, expectedVersion: selectedSupport.value.version, confirmed: supportReply.confirmed as true })
	await loadSupportDirectory()
	storeSupport(updated)
    Object.assign(supportReply, { body: '', reason: '', confirmed: false })
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
      reason: supportDecision.reason, expectedVersion: selectedSupport.value.version, confirmed: supportDecision.confirmed as true,
    })
	await loadSupportDirectory()
	storeSupport(updated)
    Object.assign(supportDecision, { status: updated.status === 'resolved' ? 'closed' : 'waiting_for_requester', resolutionCode: '', reason: '', confirmed: false })
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
      const updated = await api.adminUpdateUser(command.id, { role: command.role as AdminUser['role'], status: command.status as AdminUser['status'], reason: command.reason, confirmed: command.confirmed })
      if (updated.id) await loadUserDirectory()
    } else if (command.kind === 'content') {
      await api.adminUpdateContent(command.id, { status: command.status as 'published' | 'hidden' | 'removed', reason: command.reason, confirmed: command.confirmed })
      await loadContentDirectory()
    } else if (command.kind === 'media') {
      await api.adminReviewMedia(command.id, { status: command.status as 'clean' | 'review' | 'rejected', reason: command.reason, confirmed: command.confirmed })
      await loadMediaDirectory()
    } else if (command.kind === 'report') {
      await api.adminResolveGovernanceReport(command.id, { outcome: command.outcome as 'no_action' | 'hidden' | 'removed', reason: command.reason, confirmed: command.confirmed })
      await loadReportDirectory()
    } else if (command.kind === 'appeal') {
      await api.adminResolveGovernanceAppeal(command.id, { decision: command.decision as 'upheld' | 'denied', reason: command.reason, confirmed: command.confirmed })
      await loadAppealDirectory()
    } else if (command.kind === 'generation') {
      if (!command.confirmed) throw new Error(t('admin.confirmRequired'))
      await api.adminCancelGeneration(command.id, command.reason)
      await loadGenerationDirectory()
    } else if (command.kind === 'task') {
      await api.adminResolveTaskDispute(command.id, {
        decision: command.decision as 'release_creator' | 'cancel_without_settlement', reason: command.reason,
        expectedVersion: Number(command.status), confirmed: command.confirmed,
      })
      await loadTaskDirectory()
    } else if (command.kind === 'provider') {
      const updated = await api.adminUpdateProvider(command.id, { enabled: command.enabled, reason: command.reason, confirmed: command.confirmed })
      providers.value = providers.value.map((item) => item.id === updated.id ? updated : item)
    } else if (command.kind === 'finance') {
      await api.adminAdjustFinance(command.id, { deltaCents: command.deltaCents, currency: 'USD', reason: command.reason, confirmed: command.confirmed })
      await loadFinanceDirectory()
    } else if (command.kind === 'risk') {
	  await api.adminReviewRiskSignal(command.id, { decision: command.decision as 'monitor' | 'no_action' | 'escalated', reason: command.reason, expectedVersion: Number(command.status), confirmed: command.confirmed })
	  await loadRiskDirectory()
    } else if (command.kind === 'dataRightsHold') {
		await api.adminCreateDataRightsHold({ userId: command.id, reason: command.reason, authorityReference: command.authorityReference, confirmed: command.confirmed as true })
		await loadDataRightsDirectories()
    } else if (command.kind === 'holdRelease') {
		await api.adminReleaseDataRightsHold(command.id, command.reason)
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

onMounted(() => void load())
</script>

<template>
  <section class="admin-page content-width">
    <header class="admin-header">
      <div><span class="status-label"><ShieldAlert :size="14" />{{ t('admin.operationsLabel') }}</span><h1>{{ t('admin.title') }}</h1><p>{{ t('admin.summary') }}</p></div>
      <button v-if="hasAdminAccess" class="icon-button" type="button" :aria-label="t('actions.retry')" :title="t('actions.retry')" @click="load">
        <RefreshCw :size="18" />
      </button>
    </header>

    <div v-if="!loading && !hasAdminAccess" class="admin-access-state">
      <ShieldAlert :size="28" /><h2>{{ t('admin.accessRequired') }}</h2><p>{{ t('admin.accessRequiredDetail') }}</p>
      <button class="command-button primary" type="button" @click="useAdminDemo">
        <ShieldCheck :size="17" />{{ t('admin.useAdminDemo') }}
      </button>
    </div>

    <template v-else-if="hasAdminAccess">
      <nav ref="tabsNav" class="section-tabs admin-tabs" :aria-label="t('admin.sections')">
        <button v-for="tab in tabs" :key="tab" type="button" :data-tab="tab" :class="{ active: activeTab === tab }" @click="selectTab(tab)">
          <component :is="tabIcons[tab]" :size="16" />{{ t(`admin.tabs.${tab}`) }}
        </button>
      </nav>

      <div v-if="success" class="task-feedback success" role="status">
        <FileCheck2 :size="18" />{{ success }}
      </div>
      <div v-if="error" class="task-feedback error" role="alert">
        <ShieldAlert :size="18" />{{ error }}
      </div>

      <form v-if="command.kind" ref="commandPanel" class="admin-command-panel" @submit.prevent="submitCommand">
        <header>
          <div><span>{{ t('admin.controlledAction') }}</span><h2>{{ command.title }}</h2></div><button class="icon-button" type="button" :aria-label="t('tasks.cancel')" @click="closeCommand">
            <X :size="17" />
          </button>
        </header>
        <div v-if="command.kind === 'user'" class="admin-command-fields">
          <label>{{ t('admin.role') }}<select v-model="command.role"><option v-for="role in ['member','creator','publisher','moderator','admin']" :key="role" :value="role">{{ localizedLabel(roleKeys, role) }}</option></select></label>
          <label>{{ t('admin.status') }}<select v-model="command.status"><option v-for="status in ['active','suspended','deleted']" :key="status" :value="status">{{ t(`admin.states.${status}`) }}</option></select></label>
        </div>
        <label v-if="command.kind === 'content'">{{ t('admin.status') }}<select v-model="command.status"><option v-for="status in ['published','hidden','removed']" :key="status" :value="status">{{ t(`admin.states.${status}`) }}</option></select></label>
        <label v-if="command.kind === 'media'">{{ t('admin.scanDecision') }}<select v-model="command.status"><option v-for="status in ['clean','review','rejected']" :key="status" :value="status">{{ t(`workspace.scanStatus.${status}`) }}</option></select></label>
        <label v-if="command.kind === 'report'">{{ t('admin.reportOutcome') }}<select v-model="command.outcome"><option v-for="outcome in ['no_action','hidden','removed']" :key="outcome" :value="outcome">{{ t(`admin.outcomes.${outcome}`) }}</option></select></label>
        <label v-if="command.kind === 'appeal'">{{ t('admin.appealDecision') }}<select v-model="command.decision"><option v-for="decision in ['denied','upheld']" :key="decision" :value="decision">{{ t(`admin.appealDecisions.${decision}`) }}</option></select></label>
        <label v-if="command.kind === 'provider'" class="admin-checkbox"><input v-model="command.enabled" type="checkbox" />{{ command.enabled ? t('admin.enableProvider') : t('admin.disableProvider') }}</label>
        <label v-if="command.kind === 'finance'">{{ t('admin.adjustmentCents') }}<input v-model.number="command.deltaCents" type="number" min="-1000000" max="1000000" step="1" required /></label>
        <label v-if="command.kind === 'risk'">{{ t('admin.riskDecision') }}<select v-model="command.decision"><option v-for="decision in ['monitor','no_action','escalated']" :key="decision" :value="decision">{{ t(`admin.riskDecisions.${decision}`) }}</option></select></label>
        <label v-if="command.kind === 'task'">{{ t('admin.taskDecision') }}<select v-model="command.decision"><option v-for="decision in ['cancel_without_settlement','release_creator']" :key="decision" :value="decision">{{ t(`admin.taskDecisions.${decision}`) }}</option></select></label>
        <label v-if="command.kind === 'dataRightsHold'">{{ t('admin.authorityReference') }}<input v-model.trim="command.authorityReference" minlength="6" maxlength="200" required :placeholder="t('admin.authorityReferencePlaceholder')" /></label>
        <label>{{ t('admin.reason') }}<textarea v-model="command.reason" rows="3" minlength="10" maxlength="500" required :placeholder="t('admin.reasonPlaceholder')"></textarea></label>
        <label class="admin-checkbox"><input v-model="command.confirmed" type="checkbox" required />{{ t('admin.confirmAction') }}</label>
        <button class="command-button primary" type="submit" :disabled="actionLoading">
          <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><ShieldCheck v-else :size="17" />{{ t('admin.applyAction') }}
        </button>
      </form>

      <div v-if="loading" class="page-state" aria-live="polite">
        {{ t('admin.loading') }}
      </div>

      <div v-else-if="activeTab === 'overview' && overview" class="admin-overview-grid">
        <article v-for="key in ['users','works','generations','orders','tasks','risks','providers'] as const" :key="key">
          <span>{{ t(`admin.metrics.${key}`) }}</span><strong>{{ overview[key].total }}</strong><div><small v-for="(count, status) in overview[key].byStatus" :key="status">{{ overviewStatusLabel(key, status) }} {{ count }}</small></div>
        </article>
      </div>

      <div v-else-if="activeTab === 'users'" class="admin-user-directory">
        <form class="admin-user-filters" @submit.prevent="applyUserFilters">
          <label>{{ t('admin.userSearch') }}<input v-model="userQuery" type="search" maxlength="120" :placeholder="t('admin.userSearchPlaceholder')" /></label>
          <label>{{ t('admin.role') }}<select v-model="userRole"><option value="">{{ t('admin.allRoles') }}</option><option v-for="role in ['member','creator','publisher','moderator','admin']" :key="role" :value="role">{{ localizedLabel(roleKeys, role) }}</option></select></label>
          <label>{{ t('admin.status') }}<select v-model="userStatus"><option value="">{{ t('admin.allStatuses') }}</option><option v-for="status in ['active','suspended','deleted']" :key="status" :value="status">{{ t(`admin.states.${status}`) }}</option></select></label>
          <button class="command-button secondary" type="submit">
            <ListFilter :size="16" />{{ t('actions.applyFilters') }}
          </button>
          <button class="icon-button" type="button" :aria-label="t('actions.clearFilters')" :title="t('actions.clearFilters')" @click="clearUserFilters">
            <X :size="16" />
          </button>
        </form>
        <div class="admin-list">
          <article v-for="item in users" :key="item.id">
            <div><strong>{{ item.displayName }}</strong><span>@{{ item.handle }} · {{ item.email }}</span></div><span>{{ localizedLabel(roleKeys, item.role) }}</span><span :data-status="item.status">{{ t(`admin.states.${item.status}`) }}</span><small>{{ date(item.lastSeenAt) }}</small><button class="command-button secondary" type="button" @click="openUser(item)">
              <Settings2 :size="16" />{{ t('admin.manage') }}
            </button>
          </article>
        </div>
        <p v-if="!users.length" class="inline-empty">
          {{ t('admin.noUsers') }}
        </p>
        <button v-if="userNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="userLoadingMore" @click="loadMoreUsers">
          <LoaderCircle v-if="userLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
        </button>
      </div>

      <div v-else-if="activeTab === 'content'" class="admin-content-directory">
        <form class="admin-user-filters" @submit.prevent="applyContentFilters">
          <label>{{ t('admin.contentSearch') }}<input v-model="contentQuery" type="search" maxlength="120" :placeholder="t('admin.contentSearchPlaceholder')" /></label>
          <label>{{ t('admin.resourceType') }}<select v-model="contentType"><option value="">{{ t('admin.allContentTypes') }}</option><option value="work">{{ localizedLabel(resourceTypeKeys, 'work') }}</option></select></label>
          <label>{{ t('admin.status') }}<select v-model="contentStatus"><option value="">{{ t('admin.allStatuses') }}</option><option v-for="status in ['draft','published','hidden','removed']" :key="status" :value="status">{{ t(`admin.states.${status}`) }}</option></select></label>
          <button class="command-button secondary" type="submit">
            <ListFilter :size="16" />{{ t('actions.applyFilters') }}
          </button>
          <button class="icon-button" type="button" :aria-label="t('actions.clearFilters')" :title="t('actions.clearFilters')" @click="clearContentFilters">
            <X :size="16" />
          </button>
        </form>
        <div class="admin-list">
          <article v-for="item in content" :key="item.id">
            <div><strong>{{ item.title }}</strong><span>@{{ item.authorHandle }} · {{ item.aiDisclosure }}</span></div><span>{{ localizedLabel(resourceTypeKeys, item.resourceType) }}</span><span :data-status="item.status">{{ t(`admin.states.${item.status}`) }}</span><small>{{ date(item.updatedAt) }}</small><button class="command-button secondary" type="button" @click="openContent(item)">
              <ShieldCheck :size="16" />{{ t('admin.review') }}
            </button>
          </article>
        </div>
        <p v-if="!content.length" class="inline-empty">
          {{ t('admin.noContent') }}
        </p>
        <button v-if="contentNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="contentLoadingMore" @click="loadMoreContent">
          <LoaderCircle v-if="contentLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
        </button>
      </div>

      <div v-else-if="activeTab === 'media'" class="admin-media-directory">
        <form class="admin-user-filters" @submit.prevent="applyMediaFilters">
          <label>{{ t('admin.mediaSearch') }}<input v-model="mediaQuery" type="search" maxlength="120" :placeholder="t('admin.mediaSearchPlaceholder')" /></label>
          <label>{{ t('admin.mediaType') }}<select v-model="mediaKind"><option value="">{{ t('admin.allMediaTypes') }}</option><option v-for="kind in ['image','video','audio','document','prompt','workflow']" :key="kind" :value="kind">{{ localizedLabel(mediaKindKeys, kind) }}</option></select></label>
          <label>{{ t('admin.scanDecision') }}<select v-model="mediaStatus"><option value="">{{ t('admin.allScanStatuses') }}</option><option v-for="status in ['pending','clean','review','rejected']" :key="status" :value="status">{{ t(`workspace.scanStatus.${status}`) }}</option></select></label>
          <button class="command-button secondary" type="submit">
            <ListFilter :size="16" />{{ t('actions.applyFilters') }}
          </button>
          <button class="icon-button" type="button" :aria-label="t('actions.clearFilters')" :title="t('actions.clearFilters')" @click="clearMediaFilters">
            <X :size="16" />
          </button>
        </form>
        <div class="admin-list media-admin-list">
          <article v-for="item in mediaItems" :key="item.id">
            <div><strong>{{ item.title }}</strong><span>@{{ item.ownerHandle }} · {{ item.uploadedFilename || item.mimeType }}</span><small>{{ item.scanReason || t('workspace.scanPendingDetail') }}</small></div><span>{{ localizedLabel(mediaKindKeys, item.kind) }}</span><span :data-status="item.scanStatus">{{ t(`workspace.scanStatus.${item.scanStatus}`) }}</span><small>{{ date(item.scannedAt || item.createdAt) }}</small><button class="command-button secondary" type="button" @click="openMedia(item)">
              <ShieldCheck :size="16" />{{ t('admin.review') }}
            </button>
          </article>
        </div>
        <p v-if="!mediaItems.length" class="inline-empty">
          {{ t('admin.noMedia') }}
        </p>
        <button v-if="mediaNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="mediaLoadingMore" @click="loadMoreMedia">
          <LoaderCircle v-if="mediaLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
        </button>
      </div>

      <div v-else-if="activeTab === 'governance'" class="admin-governance">
        <section>
          <header><div><h2>{{ t('admin.reportQueue') }}</h2><p>{{ t('admin.reportQueueSummary') }}</p></div><span>{{ governanceReports.filter(item => ['open','reviewing'].includes(item.status)).length }}</span></header>
          <form class="admin-user-filters admin-governance-filters" @submit.prevent="applyReportFilters">
            <label>{{ t('admin.reportSearch') }}<input v-model="reportQuery" type="search" maxlength="120" :placeholder="t('admin.reportSearchPlaceholder')" /></label>
            <label>{{ t('admin.resourceType') }}<select v-model="reportType"><option value="">{{ t('admin.allResourceTypes') }}</option><option v-for="type in ['work','post','comment']" :key="type" :value="type">{{ localizedLabel(resourceTypeKeys, type) }}</option></select></label>
            <label>{{ t('admin.reportCategory') }}<select v-model="reportCategory"><option value="">{{ t('admin.allReportCategories') }}</option><option v-for="category in ['spam','harassment','copyright','sexual','violence','misleading','other']" :key="category" :value="category">{{ t(`community.reportCategories.${category}`) }}</option></select></label>
            <label>{{ t('admin.status') }}<select v-model="reportStatus"><option value="">{{ t('admin.allStatuses') }}</option><option v-for="status in ['open','reviewing','resolved','dismissed']" :key="status" :value="status">{{ t(`community.reportStates.${status}`) }}</option></select></label>
            <button class="command-button secondary" type="submit">
              <ListFilter :size="16" />{{ t('actions.applyFilters') }}
            </button>
            <button class="icon-button" type="button" :aria-label="t('actions.clearFilters')" :title="t('actions.clearFilters')" @click="clearReportFilters">
              <X :size="16" />
            </button>
          </form>
          <div v-if="governanceReports.length" class="admin-list governance-admin-list">
            <article v-for="item in governanceReports" :key="item.id">
              <div><strong>{{ item.resourceTitle }}</strong><span>@{{ item.reporterHandle }} → @{{ item.subjectHandle }} · {{ t(`community.reportCategories.${item.category}`) }}</span><small>{{ item.details }}</small></div><span>{{ localizedLabel(resourceTypeKeys, item.resourceType) }}</span><span :data-status="item.status">{{ t(`community.reportStates.${item.status}`) }}</span><small>{{ date(item.createdAt) }}</small><button v-if="['open','reviewing'].includes(item.status)" class="command-button secondary" type="button" @click="openReport(item)">
                <ShieldCheck :size="16" />{{ t('admin.resolve') }}
              </button><span v-else>{{ item.outcome ? t(`admin.outcomes.${item.outcome}`) : '' }}</span>
            </article>
          </div>
          <p v-else class="inline-empty">
            {{ t('admin.noReports') }}
          </p>
          <button v-if="reportNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="reportLoadingMore" @click="loadMoreReports">
            <LoaderCircle v-if="reportLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
          </button>
        </section>
        <section>
          <header><div><h2>{{ t('admin.appealQueue') }}</h2><p>{{ t('admin.appealQueueSummary') }}</p></div><span>{{ governanceAppeals.filter(item => item.status === 'pending').length }}</span></header>
          <form class="admin-user-filters" @submit.prevent="applyAppealFilters">
            <label>{{ t('admin.appealSearch') }}<input v-model="appealQuery" type="search" maxlength="120" :placeholder="t('admin.appealSearchPlaceholder')" /></label>
            <label>{{ t('admin.resourceType') }}<select v-model="appealType"><option value="">{{ t('admin.allResourceTypes') }}</option><option v-for="type in ['work','post','comment']" :key="type" :value="type">{{ localizedLabel(resourceTypeKeys, type) }}</option></select></label>
            <label>{{ t('admin.status') }}<select v-model="appealStatus"><option value="">{{ t('admin.allStatuses') }}</option><option v-for="status in ['pending','upheld','denied']" :key="status" :value="status">{{ t(`community.appealStates.${status}`) }}</option></select></label>
            <button class="command-button secondary" type="submit">
              <ListFilter :size="16" />{{ t('actions.applyFilters') }}
            </button>
            <button class="icon-button" type="button" :aria-label="t('actions.clearFilters')" :title="t('actions.clearFilters')" @click="clearAppealFilters">
              <X :size="16" />
            </button>
          </form>
          <div v-if="governanceAppeals.length" class="admin-list governance-admin-list">
            <article v-for="item in governanceAppeals" :key="item.id">
              <div><strong>{{ item.resourceTitle }}</strong><span>@{{ item.appellantHandle }} · {{ localizedLabel(resourceTypeKeys, item.resourceType) }}</span><small>{{ item.reason }}</small></div><span>{{ localizedLabel(resourceTypeKeys, item.resourceType) }}</span><span :data-status="item.status">{{ t(`community.appealStates.${item.status}`) }}</span><small>{{ date(item.createdAt) }}</small><button v-if="item.status === 'pending'" class="command-button secondary" type="button" @click="openAppeal(item)">
                <Undo2 :size="16" />{{ t('admin.resolve') }}
              </button><span v-else>{{ t(`admin.appealDecisions.${item.status}`) }}</span>
            </article>
          </div>
          <p v-else class="inline-empty">
            {{ t('admin.noAppeals') }}
          </p>
          <button v-if="appealNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="appealLoadingMore" @click="loadMoreAppeals">
            <LoaderCircle v-if="appealLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
          </button>
        </section>
      </div>

      <div v-else-if="activeTab === 'support'" class="admin-user-directory">
        <form class="admin-user-filters admin-operations-filters" @submit.prevent="applySupportFilters">
          <label>{{ t('admin.supportSearch') }}<input v-model="supportQuery" type="search" maxlength="120" :placeholder="t('admin.supportSearchPlaceholder')" /></label>
          <label>{{ t('admin.status') }}<select v-model="supportStatus"><option value="">{{ t('admin.allSupportStatuses') }}</option><option v-for="status in ['open','in_review','waiting_for_requester','resolved','closed']" :key="status" :value="status">{{ t(`support.statuses.${status}`) }}</option></select></label>
          <label>{{ t('admin.supportCategory') }}<select v-model="supportCategory"><option value="">{{ t('admin.allSupportCategories') }}</option><option v-for="category in ['general_support','billing','account','task_or_order','copyright']" :key="category" :value="category">{{ t(`support.categories.${category}`) }}</option></select></label>
          <button class="command-button primary" type="submit">
            <ListFilter :size="16" />{{ t('actions.applyFilters') }}
          </button>
          <button class="icon-button" type="button" :aria-label="t('actions.clearFilters')" :title="t('actions.clearFilters')" @click="clearSupportFilters">
            <Undo2 :size="16" />
          </button>
        </form>
        <div class="admin-support-layout">
          <section class="admin-support-queue">
            <header><div><h2>{{ t('admin.supportQueue') }}</h2><p>{{ t('admin.supportQueueSummary') }}</p></div><span>{{ supportCases.filter(item => !['resolved','closed'].includes(item.status)).length }}</span></header>
            <button v-for="item in supportCases" :key="item.id" type="button" :class="{ active: item.id === selectedSupport?.id }" @click="openSupport(item)">
              <span><strong>{{ item.subject }}</strong><small>@{{ item.requesterHandle }} · {{ t(`support.categories.${item.category}`) }}</small></span><span><em :data-status="item.status">{{ t(`support.statuses.${item.status}`) }}</em><small>{{ date(item.updatedAt) }}</small></span>
            </button>
            <p v-if="!supportCases.length" class="inline-empty">
              {{ t('admin.noSupportCases') }}
            </p>
            <button v-if="supportNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="supportLoadingMore" @click="loadMoreSupport">
              <LoaderCircle v-if="supportLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
            </button>
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
                <label>{{ t('admin.replyBody') }}<textarea v-model.trim="supportReply.body" rows="4" minlength="2" maxlength="4000" required></textarea></label>
                <label>{{ t('admin.reason') }}<textarea v-model.trim="supportReply.reason" rows="2" minlength="10" maxlength="500" required :placeholder="t('admin.reasonPlaceholder')"></textarea></label>
                <label class="admin-checkbox"><input v-model="supportReply.confirmed" type="checkbox" required />{{ t('admin.confirmAction') }}</label>
                <button class="command-button secondary" type="submit" :disabled="actionLoading">
                  <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><Send v-else :size="17" />{{ t('admin.sendReply') }}
                </button>
              </form>
              <form @submit.prevent="submitSupportDecision">
                <h3><ShieldCheck :size="17" />{{ t('admin.updateCase') }}</h3>
                <label>{{ t('admin.status') }}<select v-model="supportDecision.status"><option v-for="status in supportStatusOptions" :key="status" :value="status">{{ t(`support.statuses.${status}`) }}</option></select></label>
                <label v-if="['resolved','closed'].includes(supportDecision.status)">{{ t('admin.resolutionCode') }}<select v-model="supportDecision.resolutionCode" required><option value="" disabled>{{ t('admin.chooseResolution') }}</option><option v-for="code in ['answered','fixed','refund_guidance','content_restricted','no_action','duplicate','withdrawn']" :key="code" :value="code">{{ t(`support.resolutions.${code}`) }}</option></select></label>
                <label>{{ t('admin.reason') }}<textarea v-model.trim="supportDecision.reason" rows="3" minlength="10" maxlength="1000" required :placeholder="t('admin.reasonPlaceholder')"></textarea></label>
                <label class="admin-checkbox"><input v-model="supportDecision.confirmed" type="checkbox" required />{{ t('admin.confirmAction') }}</label>
                <button class="command-button primary" type="submit" :disabled="actionLoading">
                  <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><ShieldCheck v-else :size="17" />{{ t('admin.applyAction') }}
                </button>
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
          <label>{{ t('admin.generationSearch') }}<input v-model="generationQuery" type="search" maxlength="120" :placeholder="t('admin.generationSearchPlaceholder')" /></label>
          <label>{{ t('admin.creationMode') }}<select v-model="generationMode"><option value="">{{ t('admin.allGenerationModes') }}</option><option v-for="mode in ['chat','image','video','music']" :key="mode" :value="mode">{{ t(`create.modes.${mode}`) }}</option></select></label>
          <label>{{ t('admin.generationStatus') }}<select v-model="generationStatus"><option value="">{{ t('admin.allGenerationStatuses') }}</option><option v-for="status in ['queued','running','succeeded','failed','cancelled']" :key="status" :value="status">{{ t(`generation.status.${status}`) }}</option></select></label>
          <button class="command-button primary" type="submit">
            <ListFilter :size="16" />{{ t('actions.applyFilters') }}
          </button>
          <button class="icon-button" type="button" :aria-label="t('actions.clearFilters')" :title="t('actions.clearFilters')" @click="clearGenerationFilters">
            <Undo2 :size="16" />
          </button>
        </form>
        <div v-if="generations.length" class="admin-list">
          <article v-for="item in generations" :key="item.id">
            <div><strong>{{ item.prompt }}</strong><span>@{{ item.ownerHandle }} · {{ item.modelName }}</span></div><span>{{ formatCurrency(item.chargedCostCents || item.estimatedCostCents, 'USD', locale) }}</span><span :data-status="item.status">{{ t(`generation.status.${item.status}`) }}</span><small>{{ date(item.createdAt) }}</small><button v-if="['queued','running'].includes(item.status)" class="command-button secondary" type="button" @click="openGeneration(item)">
              <Ban :size="16" />{{ t('actions.cancel') }}
            </button><span v-else></span>
          </article>
        </div>
        <div v-else class="workspace-empty">
          <WandSparkles :size="22" /><p>{{ t('admin.noGenerationOperations') }}</p>
        </div>
        <button v-if="generationNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="generationLoadingMore" @click="loadMoreGenerations">
          <LoaderCircle v-if="generationLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
        </button>
      </div>

      <div v-else-if="activeTab === 'tasks'" class="admin-governance task-operations-admin">
        <section>
          <header><div><h2>{{ t('admin.taskOperationsQueue') }}</h2><p>{{ t('admin.taskOperationsSummary') }}</p></div><span>{{ taskOperations.filter(item => item.disputeStatus === 'open').length }}</span></header>
          <form class="admin-user-filters admin-task-filters" @submit.prevent="applyTaskFilters">
            <label>{{ t('admin.taskSearch') }}<input v-model="taskQuery" type="search" maxlength="120" :placeholder="t('admin.taskSearchPlaceholder')" /></label>
            <label>{{ t('admin.taskStatus') }}<select v-model="taskStatus"><option value="">{{ t('admin.allTaskStatuses') }}</option><option v-for="status in ['draft','open','assigned','submitted','revision','accepted','disputed','cancelled']" :key="status" :value="status">{{ t(`admin.taskStates.${status}`) }}</option></select></label>
            <label>{{ t('admin.taskDisputeStatus') }}<select v-model="taskDisputeStatus"><option value="">{{ t('admin.allTaskDisputeStatuses') }}</option><option value="none">{{ t('admin.noTaskDispute') }}</option><option v-for="status in ['open','resolved_creator','resolved_client']" :key="status" :value="status">{{ t(`admin.taskDisputeStates.${status}`) }}</option></select></label>
            <button class="command-button primary" type="submit">
              <ListFilter :size="16" />{{ t('actions.applyFilters') }}
            </button>
            <button class="icon-button" type="button" :aria-label="t('actions.clearFilters')" :title="t('actions.clearFilters')" @click="clearTaskFilters">
              <Undo2 :size="16" />
            </button>
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
              <button v-if="item.disputeStatus === 'open'" class="command-button secondary" type="button" @click="openTaskOperation(item)">
                <ShieldAlert :size="16" />{{ t('admin.resolveTask') }}
              </button><span v-else-if="item.disputeStatus">{{ t(`admin.taskDisputeStates.${item.disputeStatus}`) }}</span><span v-else></span>
            </article>
          </div>
          <div v-else class="workspace-empty">
            <BriefcaseBusiness :size="22" /><p>{{ t('admin.noTaskOperations') }}</p>
          </div>
          <button v-if="taskNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="taskLoadingMore" @click="loadMoreTasks">
            <LoaderCircle v-if="taskLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
          </button>
        </section>
      </div>

      <div v-else-if="activeTab === 'providers'" class="admin-list provider-admin-list">
        <article v-for="item in providers" :key="item.id">
          <div><strong>{{ item.displayName }}</strong><span>{{ localizedLabel(providerModeKeys, item.mode) }} · {{ item.provider }} · {{ item.modelName }}</span></div><span>{{ formatCurrency(item.estimatedCostCents, item.currency, locale) }}</span><span :data-status="item.effectiveEnabled ? 'active' : 'suspended'">{{ item.effectiveEnabled ? t('admin.available') : t('admin.unavailable') }}</span><small>{{ item.runtimeAvailable ? t('admin.runtimeReady') : t('admin.externalConfig') }}</small><button class="command-button secondary" type="button" @click="openProvider(item)">
            <Settings2 :size="16" />{{ item.adminEnabled ? t('admin.disable') : t('admin.enable') }}
          </button>
        </article>
      </div>

      <div v-else-if="activeTab === 'models' && modelRoutePolicy" class="admin-governance model-routes-admin">
        <section>
          <header><div><h2>{{ t('admin.modelRoutesTitle') }}</h2><p>{{ t('admin.modelRoutesSummary') }}</p></div><span>v{{ modelRoutePolicy.routes[modelRouteMode]?.version }}</span></header>
          <form class="admin-command-panel ranking-policy-form" @submit.prevent="submitModelRoute">
            <label>{{ t('admin.creationMode') }}<select v-model="modelRouteMode" @change="resetModelRouteForm"><option v-for="mode in ['chat','image','video','music']" :key="mode" :value="mode">{{ t(`create.modes.${mode}`) }}</option></select></label>
            <label>{{ t('admin.providerProfile') }}<select v-model="modelRouteForm.providerProfileId" required><option v-for="item in providers.filter(item => item.mode === modelRouteMode)" :key="item.id" :value="item.id">{{ item.displayName }} · {{ item.modelName }} · {{ item.runtimeAvailable ? t('admin.runtimeReady') : t('admin.externalConfig') }}</option></select></label>
            <label>{{ t('admin.modelRouteName') }}<input v-model.trim="modelRouteForm.name" minlength="3" maxlength="80" required /></label>
            <div class="ranking-weight-grid">
              <label>{{ t('admin.timeoutSeconds') }}<input v-model.number="modelRouteForm.timeoutSeconds" type="number" min="5" max="600" required /></label>
              <label>{{ t('admin.maxAttempts') }}<input v-model.number="modelRouteForm.maxAttempts" type="number" min="1" max="5" required /></label>
            </div>
            <label>{{ t('admin.reason') }}<textarea v-model.trim="modelRouteForm.reason" rows="3" minlength="10" maxlength="500" required :placeholder="t('admin.modelRouteReasonPlaceholder')"></textarea></label>
            <label class="admin-checkbox"><input v-model="modelRouteForm.confirmed" type="checkbox" required />{{ t('admin.confirmModelRoute') }}</label>
            <button class="command-button primary" type="submit" :disabled="actionLoading">
              <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><ShieldCheck v-else :size="17" />{{ t('admin.activateRevision') }}
            </button>
          </form>
        </section>
        <section>
          <header><div><h2>{{ t('admin.modelRouteHistory') }}</h2><p>{{ t('admin.modelRouteHistorySummary') }}</p></div><span>{{ modelRoutePolicy.history[modelRouteMode]?.length || 0 }}</span></header>
          <div class="admin-list ranking-history-list">
            <article v-for="revision in modelRoutePolicy.history[modelRouteMode] || []" :key="revision.id" :data-model-route-id="revision.id">
              <div><strong>{{ revision.name }}</strong><span>{{ revision.reason }}</span></div><span>v{{ revision.version }}</span><span>{{ revision.providerDisplayName }} · {{ revision.modelName }}</span><small>{{ date(revision.createdAt) }}</small><span :data-status="revision.id === modelRoutePolicy.routes[modelRouteMode]?.id ? 'active' : ''">{{ revision.id === modelRoutePolicy.routes[modelRouteMode]?.id ? t('admin.activeRevision') : t('admin.supersededRevision') }}</span>
            </article>
          </div>
          <button v-if="modelRoutePolicy.nextCursors?.[modelRouteMode]" class="command-button secondary admin-history-load-more" type="button" :disabled="modelRouteLoadingMore[modelRouteMode]" @click="loadMoreModelRoutes">
            <LoaderCircle v-if="modelRouteLoadingMore[modelRouteMode]" class="spin" :size="16" />{{ t('actions.loadMore') }}
          </button>
        </section>
      </div>

      <div v-else-if="activeTab === 'settings' && systemSettingPolicy" class="admin-governance system-settings-admin">
        <section>
          <header><div><h2>{{ t('admin.systemSettingsTitle') }}</h2><p>{{ t('admin.systemSettingsSummary') }}</p></div><span>v{{ systemSettingPolicy.current.version }}</span></header>
          <form class="admin-command-panel ranking-policy-form" @submit.prevent="submitSystemSettings">
            <label>{{ t('admin.settingsRevisionName') }}<input v-model.trim="systemSettingForm.name" minlength="3" maxlength="80" required /></label>
            <fieldset>
              <legend>{{ t('admin.writeAvailability') }}</legend><div class="system-setting-toggles">
                <label class="admin-checkbox"><input v-model="systemSettingForm.registrationsEnabled" type="checkbox" />{{ t('admin.registrationsEnabled') }}</label>
                <label class="admin-checkbox"><input v-model="systemSettingForm.generationsEnabled" type="checkbox" />{{ t('admin.generationsEnabled') }}</label>
                <label class="admin-checkbox"><input v-model="systemSettingForm.publishingEnabled" type="checkbox" />{{ t('admin.publishingEnabled') }}</label>
                <label class="admin-checkbox"><input v-model="systemSettingForm.marketplaceCheckoutEnabled" type="checkbox" />{{ t('admin.marketplaceCheckoutEnabled') }}</label>
                <label class="admin-checkbox"><input v-model="systemSettingForm.taskCreationEnabled" type="checkbox" />{{ t('admin.taskCreationEnabled') }}</label>
              </div>
            </fieldset>
            <label>{{ t('admin.publicNotice') }}<textarea v-model.trim="systemSettingForm.publicNotice" rows="2" maxlength="240" :placeholder="t('admin.publicNoticePlaceholder')"></textarea></label>
            <label>{{ t('admin.reason') }}<textarea v-model.trim="systemSettingForm.reason" rows="3" minlength="10" maxlength="500" required :placeholder="t('admin.systemSettingsReasonPlaceholder')"></textarea></label>
            <label class="admin-checkbox"><input v-model="systemSettingForm.confirmed" type="checkbox" required />{{ t('admin.confirmSystemSettings') }}</label>
            <button class="command-button primary" type="submit" :disabled="actionLoading">
              <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><ShieldCheck v-else :size="17" />{{ t('admin.activateRevision') }}
            </button>
          </form>
        </section>
        <section>
          <header><div><h2>{{ t('admin.systemSettingsHistory') }}</h2><p>{{ t('admin.systemSettingsHistorySummary') }}</p></div><span>{{ systemSettingPolicy.history.length }}</span></header>
          <div class="admin-list ranking-history-list">
            <article v-for="revision in systemSettingPolicy.history" :key="revision.id">
              <div><strong>{{ revision.name }}</strong><span>{{ revision.reason }}</span></div><span>v{{ revision.version }}</span><span>{{ t('admin.enabledGateCount', { count: [revision.registrationsEnabled,revision.generationsEnabled,revision.publishingEnabled,revision.marketplaceCheckoutEnabled,revision.taskCreationEnabled].filter(Boolean).length }) }}</span><small>{{ date(revision.createdAt) }} · {{ revision.createdByHandle ? `@${revision.createdByHandle}` : t('admin.systemActor') }}</small><span :data-status="revision.id === systemSettingPolicy.current.id ? 'active' : ''">{{ revision.id === systemSettingPolicy.current.id ? t('admin.activeRevision') : t('admin.supersededRevision') }}</span>
            </article>
          </div>
          <button v-if="systemSettingNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="systemSettingLoadingMore" @click="loadMoreSystemSettingHistory">
            <LoaderCircle v-if="systemSettingLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
          </button>
        </section>
      </div>

      <div v-else-if="activeTab === 'developer' && developerAdminAccess" class="admin-governance system-settings-admin">
        <section>
          <header><div><h2>{{ t('admin.developerControlTitle') }}</h2><p>{{ t('admin.developerControlSummary') }}</p></div><span>v{{ developerAdminAccess.control.version }}</span></header>
          <form class="admin-command-panel ranking-policy-form" @submit.prevent="submitDeveloperControl">
            <label class="admin-checkbox"><input v-model="developerControlForm.enabled" type="checkbox" />{{ t('admin.developerEnabled') }}</label>
            <div class="ranking-weight-grid">
              <label>{{ t('admin.maxServiceAccounts') }}<input v-model.number="developerControlForm.maxServiceAccounts" type="number" min="1" max="20" required /></label>
              <label>{{ t('admin.maxActiveKeys') }}<input v-model.number="developerControlForm.maxActiveKeys" type="number" min="1" max="10" required /></label>
              <label>{{ t('admin.defaultTtlDays') }}<input v-model.number="developerControlForm.defaultTtlDays" type="number" min="1" max="365" required /></label>
            </div>
            <label>{{ t('admin.reason') }}<textarea v-model.trim="developerControlForm.reason" rows="3" minlength="10" maxlength="500" required :placeholder="t('admin.developerReasonPlaceholder')"></textarea></label>
            <label class="admin-checkbox"><input v-model="developerControlForm.confirmed" type="checkbox" required />{{ t('admin.confirmDeveloperControl') }}</label>
            <button class="command-button primary" type="submit" :disabled="actionLoading">
              <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><ShieldCheck v-else :size="17" />{{ t('admin.applyDeveloperControl') }}
            </button>
          </form>
        </section>
        <section>
          <header><div><h2>{{ t('admin.developerInventory') }}</h2><p>{{ t('admin.developerInventorySummary') }}</p></div><span>{{ developerAdminAccess.accounts.length }}</span></header>
          <form v-if="developerAdminAccess.accounts.some(account => account.status === 'active' || account.keys.some(key => key.status === 'active'))" class="admin-command-panel ranking-policy-form developer-emergency-form" @submit.prevent>
            <fieldset>
              <legend>{{ t('admin.developerEmergencyTitle') }}</legend>
              <p>{{ t('admin.developerEmergencySummary') }}</p>
              <label>{{ t('admin.reason') }}<textarea v-model.trim="developerEmergencyForm.reason" rows="3" minlength="10" maxlength="500" required :placeholder="t('admin.developerEmergencyReasonPlaceholder')"></textarea></label>
              <label class="admin-checkbox"><input v-model="developerEmergencyForm.confirmed" type="checkbox" required />{{ t('admin.confirmDeveloperEmergency') }}</label>
            </fieldset>
          </form>
          <div v-if="developerAdminAccess.accounts.length" class="developer-admin-accounts">
            <article v-for="account in developerAdminAccess.accounts" :key="account.id" class="developer-admin-account">
              <header>
                <div><strong>{{ account.name }}</strong><span>@{{ account.ownerHandle }} · v{{ account.version }}</span></div>
                <div class="developer-admin-actions">
                  <span :data-status="account.status === 'active' ? 'active' : 'suspended'">{{ t(`account.developerStatuses.${account.status}`) }}</span>
                  <button v-if="account.status === 'active'" class="command-button danger" type="button" :disabled="actionLoading || !developerEmergencyForm.confirmed || developerEmergencyForm.reason.trim().length < 10" :aria-label="t('admin.revokeDeveloperAccountNamed', { name: account.name })" @click="adminRevokeDeveloperAccount(account.id, account.version)">
                    <Ban :size="16" />{{ t('admin.revokeDeveloperAccount') }}
                  </button>
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
                    <button v-if="key.status === 'active'" class="command-button danger" type="button" :disabled="actionLoading || !developerEmergencyForm.confirmed || developerEmergencyForm.reason.trim().length < 10" :aria-label="t('admin.revokeDeveloperKeyNamed', { prefix: key.publicPrefix })" @click="adminRevokeDeveloperKey(key.id, key.version)">
                      <KeyRound :size="16" />{{ t('admin.revokeDeveloperKey') }}
                    </button>
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
            <label>{{ t('admin.webhookRecoverySearch') }}<input v-model="webhookQuery" type="search" maxlength="120" :placeholder="t('admin.webhookRecoverySearchPlaceholder')" /></label>
            <label>{{ t('admin.webhookEventType') }}<select v-model="webhookEventType"><option value="">{{ t('admin.allWebhookEventTypes') }}</option><option v-for="eventType in ['developer.webhook.test','generation.completed','work.published','marketplace.order.fulfilled','marketplace.order.refunded']" :key="eventType" :value="eventType">{{ webhookEventLabel(eventType) }}</option></select></label>
            <button class="command-button primary" type="submit">
              <ListFilter :size="16" />{{ t('actions.applyFilters') }}
            </button>
            <button class="icon-button" type="button" :aria-label="t('actions.clearFilters')" :title="t('actions.clearFilters')" @click="clearWebhookRecoveryFilters">
              <Undo2 :size="16" />
            </button>
          </form>
          <form v-if="webhookDeadLetters.length" class="admin-command-panel ranking-policy-form" @submit.prevent>
            <label>{{ t('admin.reason') }}<textarea v-model.trim="webhookReplayForm.reason" rows="3" minlength="10" maxlength="500" required :placeholder="t('admin.webhookReplayReasonPlaceholder')"></textarea></label>
            <label class="admin-checkbox"><input v-model="webhookReplayForm.confirmed" type="checkbox" required />{{ t('admin.confirmWebhookReplay') }}</label>
          </form>
          <div v-if="webhookDeadLetters.length" class="admin-list webhook-dead-letter-list">
            <article v-for="delivery in webhookDeadLetters" :key="delivery.id">
              <div><strong>{{ delivery.endpointName }}</strong><span>@{{ delivery.ownerHandle }} · {{ delivery.endpointHost }}</span></div>
              <span>{{ webhookEventLabel(delivery.eventType) }}</span>
              <span>{{ t('admin.webhookAttempts', { count: delivery.attemptCount }) }}</span>
              <small>{{ delivery.lastStatusCode ? t('account.webhookHttpStatus', { code: delivery.lastStatusCode }) : delivery.lastErrorCode }} · {{ date(delivery.updatedAt) }}</small>
              <button class="command-button secondary" type="button" :disabled="actionLoading || !webhookReplayForm.confirmed || webhookReplayForm.reason.trim().length < 10" :aria-label="t('admin.replayWebhookNamed', { name: delivery.endpointName })" @click="adminReplayWebhook(delivery)">
                <RefreshCw :size="16" />{{ t('admin.replayWebhook') }}
              </button>
            </article>
          </div>
          <p v-else class="inline-empty">
            {{ t('admin.noWebhookDeadLetters') }}
          </p>
          <button v-if="webhookNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="webhookLoadingMore" @click="loadMoreWebhookRecovery">
            <LoaderCircle v-if="webhookLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
          </button>
        </section>
        <section>
          <header><div><h2>{{ t('admin.emailDeadLetters') }}</h2><p>{{ t('admin.emailDeadLettersSummary') }}</p></div><span>{{ emailActionDeadLetters.length }}</span></header>
          <form class="admin-user-filters admin-finance-filters" @submit.prevent="applyDeveloperRecoveryFilters">
            <label>{{ t('admin.emailRecoverySearch') }}<input v-model="emailQuery" type="search" maxlength="120" :placeholder="t('admin.emailRecoverySearchPlaceholder')" /></label>
            <label>{{ t('admin.emailActionKind') }}<select v-model="emailKind"><option value="">{{ t('admin.allEmailActionKinds') }}</option><option v-for="kind in ['verify_email','password_reset']" :key="kind" :value="kind">{{ t(`account.emailActionKinds.${kind}`) }}</option></select></label>
            <button class="command-button primary" type="submit">
              <ListFilter :size="16" />{{ t('actions.applyFilters') }}
            </button>
            <button class="icon-button" type="button" :aria-label="t('actions.clearFilters')" :title="t('actions.clearFilters')" @click="clearEmailRecoveryFilters">
              <Undo2 :size="16" />
            </button>
          </form>
          <form v-if="emailActionDeadLetters.length" class="admin-command-panel ranking-policy-form" @submit.prevent>
            <label>{{ t('admin.reason') }}<textarea v-model.trim="emailRecoveryForm.reason" rows="3" minlength="10" maxlength="500" required :placeholder="t('admin.emailRecoveryReasonPlaceholder')"></textarea></label>
            <label class="admin-checkbox"><input v-model="emailRecoveryForm.confirmed" type="checkbox" required />{{ t('admin.confirmEmailRecovery') }}</label>
          </form>
          <div v-if="emailActionDeadLetters.length" class="admin-list webhook-dead-letter-list">
            <article v-for="item in emailActionDeadLetters" :key="item.id">
              <div><strong>{{ t(`account.emailActionKinds.${item.kind}`) }}</strong><span>@{{ item.ownerHandle }} · {{ item.emailHint }}</span></div>
              <span>{{ t(`account.emailActionStatuses.${item.status}`) }}</span>
              <span>{{ t('admin.emailAttempts', { count: item.attemptCount }) }}</span>
              <small>{{ item.attempts.at(-1)?.errorCode || t('admin.unknownState') }} · {{ date(item.updatedAt) }}</small>
              <div class="developer-admin-actions">
                <button class="command-button secondary" type="button" :disabled="actionLoading || !emailRecoveryForm.confirmed || emailRecoveryForm.reason.trim().length < 10" @click="adminRecoverEmailAction(item, 'retry')">
                  <RefreshCw :size="16" />{{ t('admin.retryEmail') }}
                </button>
                <button class="command-button danger" type="button" :disabled="actionLoading || !emailRecoveryForm.confirmed || emailRecoveryForm.reason.trim().length < 10" @click="adminRecoverEmailAction(item, 'cancel')">
                  <Ban :size="16" />{{ t('admin.cancelEmailAction') }}
                </button>
              </div>
            </article>
          </div>
          <p v-else class="inline-empty">
            {{ t('admin.noEmailDeadLetters') }}
          </p>
          <button v-if="emailNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="emailLoadingMore" @click="loadMoreEmailRecovery">
            <LoaderCircle v-if="emailLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
          </button>
        </section>
      </div>

      <div v-else-if="activeTab === 'finance'" class="admin-user-directory">
        <form class="admin-user-filters admin-finance-filters" @submit.prevent="applyFinanceFilters">
          <label>{{ t('admin.financeSearch') }}<input v-model="financeQuery" type="search" maxlength="120" :placeholder="t('admin.financeSearchPlaceholder')" /></label>
          <label>{{ t('admin.financeState') }}<select v-model="financeState"><option value="">{{ t('admin.allFinanceStates') }}</option><option v-for="state in ['available','reserved','depleted']" :key="state" :value="state">{{ t(`admin.financeStates.${state}`) }}</option></select></label>
          <button class="command-button primary" type="submit">
            <ListFilter :size="16" />{{ t('actions.applyFilters') }}
          </button>
          <button class="icon-button" type="button" :aria-label="t('actions.clearFilters')" :title="t('actions.clearFilters')" @click="clearFinanceFilters">
            <Undo2 :size="16" />
          </button>
        </form>
        <div v-if="finance.length" class="admin-list finance-admin-list">
          <article v-for="item in finance" :key="`${item.userId}-${item.currency}`">
            <div><strong>{{ item.displayName }}</strong><span>@{{ item.handle }} · {{ item.email }}</span></div><span>{{ formatCurrency(item.availableCents, item.currency, locale) }}</span><span>{{ t('admin.reserved', { amount: formatCurrency(item.reservedCents, item.currency, locale) }) }}</span><small>{{ date(item.updatedAt) }}</small><button class="command-button secondary" type="button" @click="openFinance(item)">
              <CircleDollarSign :size="16" />{{ t('admin.adjust') }}
            </button>
          </article>
        </div>
        <div v-else class="workspace-empty">
          <CircleDollarSign :size="22" /><p>{{ t('admin.noFinanceAccounts') }}</p>
        </div>
        <button v-if="financeNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="financeLoadingMore" @click="loadMoreFinance">
          <LoaderCircle v-if="financeLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
        </button>
      </div>

      <div v-else-if="activeTab === 'ranking' && rankingPolicy" class="admin-governance ranking-admin">
        <section>
          <header><div><h2>{{ t('admin.rankingTitle') }}</h2><p>{{ t('admin.rankingSummary') }}</p></div><span>v{{ rankingPolicy.current.version }}</span></header>
          <form class="admin-command-panel ranking-policy-form" @submit.prevent="submitRankingPolicy">
            <fieldset>
              <legend>{{ t('admin.rankingActivationMode') }}</legend>
              <div class="ranking-mode-control">
                <label><input v-model="rankingActivationMode" type="radio" value="candidate" /><FlaskConical :size="16" /><span><strong>{{ t('admin.rankingCandidateMode') }}</strong><small>{{ t('admin.rankingCandidateModeSummary') }}</small></span></label>
                <label><input v-model="rankingActivationMode" type="radio" value="immediate" /><Activity :size="16" /><span><strong>{{ t('admin.rankingImmediateMode') }}</strong><small>{{ t('admin.rankingImmediateModeSummary') }}</small></span></label>
              </div>
            </fieldset>
            <label>{{ t('admin.rankingName') }}<input v-model.trim="rankingForm.name" minlength="3" maxlength="80" required /></label>
            <fieldset>
              <legend>{{ t('admin.textRelevance') }}</legend>
              <div class="ranking-weight-grid">
                <label>{{ t('admin.titleExactWeight') }}<input v-model.number="rankingForm.titleExactWeight" type="number" min="0" max="200" required /></label>
                <label>{{ t('admin.titlePrefixWeight') }}<input v-model.number="rankingForm.titlePrefixWeight" type="number" min="0" max="200" required /></label>
                <label>{{ t('admin.titleContainsWeight') }}<input v-model.number="rankingForm.titleContainsWeight" type="number" min="0" max="200" required /></label>
                <label>{{ t('admin.creatorExactWeight') }}<input v-model.number="rankingForm.creatorExactWeight" type="number" min="0" max="200" required /></label>
                <label>{{ t('admin.creatorMatchWeight') }}<input v-model.number="rankingForm.creatorMatchWeight" type="number" min="0" max="200" required /></label>
                <label>{{ t('admin.bodyMatchWeight') }}<input v-model.number="rankingForm.bodyMatchWeight" type="number" min="0" max="200" required /></label>
                <label>{{ t('admin.secondaryMatchWeight') }}<input v-model.number="rankingForm.secondaryMatchWeight" type="number" min="0" max="200" required /></label>
              </div>
            </fieldset>
            <fieldset>
              <legend>{{ t('admin.qualitySignals') }}</legend>
              <div class="ranking-weight-grid">
                <label>{{ t('admin.recencyWeight') }}<input v-model.number="rankingForm.recencyWeight" type="number" min="0" max="50" required /></label>
                <label>{{ t('admin.creatorActivityWeight') }}<input v-model.number="rankingForm.creatorActivityWeight" type="number" min="0" max="50" required /></label>
              </div>
            </fieldset>
            <fieldset>
              <legend>{{ t('admin.resultTypeBoosts') }}</legend>
              <div class="ranking-weight-grid">
                <label>{{ t('admin.workTypeBoost') }}<input v-model.number="rankingForm.workTypeBoost" type="number" min="-50" max="50" required /></label>
                <label>{{ t('admin.creatorTypeBoost') }}<input v-model.number="rankingForm.creatorTypeBoost" type="number" min="-50" max="50" required /></label>
                <label>{{ t('admin.productTypeBoost') }}<input v-model.number="rankingForm.productTypeBoost" type="number" min="-50" max="50" required /></label>
                <label>{{ t('admin.demandTypeBoost') }}<input v-model.number="rankingForm.demandTypeBoost" type="number" min="-50" max="50" required /></label>
              </div>
            </fieldset>
            <label>{{ t('admin.reason') }}<textarea v-model.trim="rankingForm.reason" rows="3" minlength="10" maxlength="500" required :placeholder="t('admin.rankingReasonPlaceholder')"></textarea></label>
            <label class="admin-checkbox"><input v-model="rankingForm.confirmed" type="checkbox" required />{{ t('admin.confirmRanking') }}</label>
            <button class="command-button primary" type="submit" :disabled="actionLoading">
              <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><ShieldCheck v-else :size="17" />{{ rankingActivationMode === 'candidate' ? t('admin.createCandidateRevision') : t('admin.activateRevision') }}
            </button>
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
              <label>{{ t('admin.reason') }}<textarea v-model.trim="evaluationForm.reason" rows="3" minlength="10" maxlength="500" required :placeholder="t('admin.rankingEvaluationReasonPlaceholder')"></textarea></label>
              <label class="admin-checkbox"><input v-model="evaluationForm.confirmed" type="checkbox" required />{{ t('admin.confirmEvaluation') }}</label>
              <button class="command-button secondary" type="submit" :disabled="actionLoading">
                <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><FlaskConical v-else :size="17" />{{ t('admin.runEvaluation') }}
              </button>
            </form>
            <form class="admin-command-panel compact-operation-form" @submit.prevent="updateRankingRollout">
              <h3><Activity :size="17" />{{ t('admin.configureRollout') }}</h3>
              <p>{{ t('admin.configureRolloutSummary') }}</p>
              <label>{{ t('admin.rolloutPercent') }}<select v-model.number="rolloutForm.percent"><option v-for="percent in [0,5,10,25,50,100]" :key="percent" :value="percent">{{ percent === 100 ? t('admin.promoteCandidate') : `${percent}%` }}</option></select></label>
              <label>{{ t('admin.reason') }}<textarea v-model.trim="rolloutForm.reason" rows="3" minlength="10" maxlength="500" required :placeholder="t('admin.rolloutReasonPlaceholder')"></textarea></label>
              <label class="admin-checkbox"><input v-model="rolloutForm.confirmed" type="checkbox" required />{{ t('admin.confirmRollout') }}</label>
              <button class="command-button primary" type="submit" :disabled="actionLoading">
                <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><Activity v-else :size="17" />{{ t('admin.applyRollout') }}
              </button>
            </form>
          </div>
          <p v-else class="inline-empty">
            {{ t('admin.noRankingCandidateSummary') }}
          </p>
          <div v-if="discoveryOperations.evaluations.length" class="admin-list ranking-evaluation-list">
            <article v-for="evaluation in discoveryOperations.evaluations" :key="evaluation.id">
              <div><strong>{{ t('admin.evaluationVersions', { candidate: evaluation.candidateVersion, baseline: evaluation.baselineVersion }) }}</strong><span>{{ evaluation.reason }}</span></div><span :data-status="evaluation.status === 'passed' ? 'active' : 'suspended'">{{ t(`admin.evaluationStates.${evaluation.status}`) }}</span><span>{{ t('admin.evaluationMrr', { candidate: decimal(evaluation.candidateMrr), baseline: decimal(evaluation.baselineMrr) }) }}</span><small>{{ date(evaluation.createdAt) }}</small><span>{{ t('admin.evaluationCases', { count: evaluation.caseCount }) }}</span>
            </article>
          </div>
          <button v-if="evaluationNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="evaluationLoadingMore" @click="loadMoreEvaluations">
            <LoaderCircle v-if="evaluationLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
          </button>
        </section>
        <section class="ranking-index-section">
          <header><div><h2>{{ t('admin.discoveryIndexTitle') }}</h2><p>{{ t('admin.discoveryIndexSummary') }}</p></div><Database :size="20" /></header>
          <form class="admin-command-panel compact-operation-form" @submit.prevent="analyzeDiscoveryIndex">
            <label>{{ t('admin.reason') }}<textarea v-model.trim="indexForm.reason" rows="2" minlength="10" maxlength="500" required :placeholder="t('admin.indexReasonPlaceholder')"></textarea></label>
            <label class="admin-checkbox"><input v-model="indexForm.confirmed" type="checkbox" required />{{ t('admin.confirmIndexAnalyze') }}</label>
            <button class="command-button secondary" type="submit" :disabled="actionLoading">
              <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><Database v-else :size="17" />{{ t('admin.analyzeIndex') }}
            </button>
          </form>
          <div v-if="discoveryOperations.indexRuns.length" class="admin-list ranking-index-list">
            <article v-for="run in discoveryOperations.indexRuns" :key="run.id">
              <div><strong>{{ t('admin.indexRunTitle', { count: Object.values(run.documentCounts).reduce((total, count) => total + count, 0) }) }}</strong><span>{{ run.reason }}</span></div><span :data-status="run.status === 'succeeded' ? 'active' : 'suspended'">{{ t(`admin.indexRunStates.${run.status}`) }}</span><span>{{ t('admin.indexSize', { size: bytes(Object.values(run.indexSizes).reduce((total, size) => total + size, 0)) }) }}</span><small>{{ date(run.completedAt) }}</small><span>{{ t('admin.indexTypes', { count: Object.keys(run.documentCounts).length }) }}</span>
            </article>
          </div>
          <button v-if="indexRunNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="indexRunLoadingMore" @click="loadMoreIndexRuns">
            <LoaderCircle v-if="indexRunLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
          </button>
          <p v-if="!discoveryOperations.indexRuns.length" class="inline-empty">
            {{ t('admin.noIndexRuns') }}
          </p>
        </section>
        <section>
          <header><div><h2>{{ t('admin.rankingHistory') }}</h2><p>{{ t('admin.rankingHistorySummary') }}</p></div><span>{{ rankingPolicy.history.length }}</span></header>
          <div class="admin-list ranking-history-list">
            <article v-for="revision in rankingPolicy.history" :key="revision.id">
              <div><strong>{{ revision.name }}</strong><span>{{ revision.reason }}</span></div>
              <span>v{{ revision.version }}</span>
              <span>{{ revision.createdByHandle ? `@${revision.createdByHandle}` : t('admin.systemActor') }}</span>
              <small>{{ date(revision.createdAt) }}</small>
              <span :data-status="revision.id === rankingPolicy.current.id ? 'active' : ''">{{ revision.id === rankingPolicy.current.id ? t('admin.activeRevision') : t('admin.supersededRevision') }}</span>
            </article>
          </div>
          <button v-if="rankingNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="rankingLoadingMore" @click="loadMoreRankingHistory">
            <LoaderCircle v-if="rankingLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
          </button>
        </section>
      </div>

      <div v-else-if="activeTab === 'risk'" class="admin-governance">
        <section>
          <header><div><h2>{{ t('admin.riskQueue') }}</h2><p>{{ t('admin.riskQueueSummary') }}</p></div><span>{{ riskSignals.length }}</span></header>
          <form class="admin-user-filters admin-operations-filters" @submit.prevent="applyRiskFilters">
            <label>{{ t('admin.riskSearch') }}<input v-model="riskQuery" type="search" maxlength="120" :placeholder="t('admin.riskSearchPlaceholder')" /></label>
            <label>{{ t('admin.status') }}<select v-model="riskStatus"><option value="">{{ t('admin.allRiskStatuses') }}</option><option v-for="status in ['open','reviewing','resolved','dismissed']" :key="status" :value="status">{{ t(`admin.riskStatuses.${status}`) }}</option></select></label>
            <label>{{ t('admin.riskSeverity') }}<select v-model="riskSeverity"><option value="">{{ t('admin.allRiskSeverities') }}</option><option v-for="severity in ['low','medium','high','critical']" :key="severity" :value="severity">{{ t(`admin.riskSeverities.${severity}`) }}</option></select></label>
            <button class="command-button primary" type="submit">
              <ListFilter :size="16" />{{ t('actions.applyFilters') }}
            </button>
            <button class="icon-button" type="button" :aria-label="t('actions.clearFilters')" :title="t('actions.clearFilters')" @click="clearRiskFilters">
              <Undo2 :size="16" />
            </button>
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
              <button v-if="item.status === 'open' || item.status === 'reviewing'" class="command-button secondary" type="button" @click="openRisk(item)">
                <Activity :size="16" />{{ t('admin.reviewRisk') }}
              </button><RouterLink v-else class="command-button secondary" :to="item.targetPath">
                {{ t('admin.openResource') }}
              </RouterLink>
            </article>
          </div>
          <div v-if="!riskSignals.length" class="workspace-empty">
            <Activity :size="22" /><p>
              {{ t('admin.noRiskSignals') }}
            </p>
          </div>
          <button v-if="riskNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="riskLoadingMore" @click="loadMoreRisk">
            <LoaderCircle v-if="riskLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
          </button>
        </section>
      </div>

      <div v-else-if="activeTab === 'riskRules' && riskRulePolicy" class="admin-governance risk-rules-admin">
        <section>
          <header><div><h2>{{ t('admin.riskRulesTitle') }}</h2><p>{{ t('admin.riskRulesSummary') }}</p></div><span>v{{ riskRulePolicy.current.version }}</span></header>
          <form class="admin-command-panel ranking-policy-form" @submit.prevent="submitRiskRulePolicy">
            <label>{{ t('admin.riskRuleName') }}<input v-model.trim="riskRuleForm.name" minlength="3" maxlength="80" required /></label>
            <fieldset>
              <legend>{{ t('admin.riskSignalScores') }}</legend>
              <div class="ranking-weight-grid">
                <label>{{ t('admin.taskDisputeScore') }}<input v-model.number="riskRuleForm.taskDisputeScore" type="number" min="0" max="100" required /></label>
                <label>{{ t('admin.transactionRefundScore') }}<input v-model.number="riskRuleForm.transactionRefundScore" type="number" min="0" max="100" required /></label>
                <label>{{ t('admin.communityReportScore') }}<input v-model.number="riskRuleForm.communityReportScore" type="number" min="0" max="100" required /></label>
                <label>{{ t('admin.mediaRejectionScore') }}<input v-model.number="riskRuleForm.mediaRejectionScore" type="number" min="0" max="100" required /></label>
                <label>{{ t('admin.accountLinkScore') }}<input v-model.number="riskRuleForm.accountLinkScore" type="number" min="0" max="100" required /></label>
              </div>
            </fieldset>
            <fieldset>
              <legend>{{ t('admin.accountLinkBoundary') }}</legend>
              <div class="ranking-weight-grid">
                <label>{{ t('admin.accountLinkMinAccounts') }}<input v-model.number="riskRuleForm.accountLinkMinAccounts" type="number" min="2" max="20" required /></label>
                <label>{{ t('admin.accountLinkWindowHours') }}<input v-model.number="riskRuleForm.accountLinkWindowHours" type="number" min="1" max="168" required /></label>
              </div>
              <small>{{ t('admin.accountLinkPrivacy') }}</small>
            </fieldset>
            <fieldset>
              <legend>{{ t('admin.severityThresholds') }}</legend>
              <div class="ranking-weight-grid">
                <label>{{ t('admin.mediumThreshold') }}<input v-model.number="riskRuleForm.mediumThreshold" type="number" min="1" max="98" required /></label>
                <label>{{ t('admin.highThreshold') }}<input v-model.number="riskRuleForm.highThreshold" type="number" min="2" max="99" required /></label>
                <label>{{ t('admin.criticalThreshold') }}<input v-model.number="riskRuleForm.criticalThreshold" type="number" min="3" max="100" required /></label>
              </div>
              <small>{{ t('admin.thresholdOrder') }}</small>
            </fieldset>
            <label>{{ t('admin.reason') }}<textarea v-model.trim="riskRuleForm.reason" rows="3" minlength="10" maxlength="500" required :placeholder="t('admin.riskRulesReasonPlaceholder')"></textarea></label>
            <label class="admin-checkbox"><input v-model="riskRuleForm.confirmed" type="checkbox" required />{{ t('admin.confirmRiskRules') }}</label>
            <button class="command-button primary" type="submit" :disabled="actionLoading">
              <LoaderCircle v-if="actionLoading" class="spin" :size="17" /><ShieldCheck v-else :size="17" />{{ t('admin.activateRiskRules') }}
            </button>
          </form>
        </section>
        <section>
          <header><div><h2>{{ t('admin.riskRulesHistory') }}</h2><p>{{ t('admin.riskRulesHistorySummary') }}</p></div><span>{{ riskRulePolicy.history.length }}</span></header>
          <div class="admin-list ranking-history-list">
            <article v-for="revision in riskRulePolicy.history" :key="revision.id">
              <div><strong>{{ revision.name }}</strong><span>{{ revision.reason }}</span></div>
              <span>v{{ revision.version }}</span>
              <span>{{ t('admin.riskRuleScoreSummary', { dispute: revision.taskDisputeScore, refund: revision.transactionRefundScore, report: revision.communityReportScore, media: revision.mediaRejectionScore, link: revision.accountLinkScore }) }}</span>
              <small>{{ date(revision.createdAt) }} · {{ revision.createdByHandle ? `@${revision.createdByHandle}` : t('admin.systemActor') }}</small>
              <span :data-status="revision.id === riskRulePolicy.current.id ? 'active' : ''">{{ revision.id === riskRulePolicy.current.id ? t('admin.activeRevision') : t('admin.supersededRevision') }}</span>
            </article>
          </div>
          <button v-if="riskRuleNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="riskRuleLoadingMore" @click="loadMoreRiskRuleHistory">
            <LoaderCircle v-if="riskRuleLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
          </button>
        </section>
      </div>

      <div v-else-if="activeTab === 'dataRights'" class="admin-governance data-rights-admin">
        <section>
          <header><div><h2>{{ t('admin.dataRightsQueue') }}</h2><p>{{ t('admin.dataRightsQueueSummary') }}</p></div><span>{{ dataRightsItems.filter(item => !['completed','cancelled','failed'].includes(item.status)).length }}</span></header>
          <div v-if="dataRightsItems.length" class="admin-list">
            <article v-for="item in dataRightsItems" :key="item.id">
              <div><strong>@{{ item.ownerHandle }}</strong><span>{{ t(`account.rightsTypes.${item.requestType}`) }} · {{ item.subjectRef }}</span><small v-if="item.export">SHA-256 {{ item.export.checksumSha256.slice(0, 16) }}…</small><small v-else>{{ t('account.cancelUntil', { date: date(item.cancelUntil || item.executeAfter) }) }}</small></div>
              <span>{{ t(`account.rightsTypes.${item.requestType}`) }}</span><span :data-status="item.status">{{ t(`account.rightsStatuses.${item.status}`) }}</span><small>{{ date(item.createdAt) }}</small><button v-if="item.requestType === 'account_deletion' && item.status === 'scheduled'" class="command-button secondary" type="button" @click="openDataRightsHold(item)">
                <ShieldAlert :size="16" />{{ t('admin.placeLegalHold') }}
              </button><span v-else></span>
            </article>
          </div>
          <p v-else class="inline-empty">
            {{ t('admin.noDataRights') }}
          </p>
          <button v-if="dataRightsNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="dataRightsLoadingMore" @click="loadMoreDataRights">
            <LoaderCircle v-if="dataRightsLoadingMore" class="spin" :size="16" />{{ t('actions.loadMore') }}
          </button>
        </section>
        <section>
          <header><div><h2>{{ t('admin.legalHolds') }}</h2><p>{{ t('admin.legalHoldsSummary') }}</p></div><span>{{ legalHolds.filter(item => item.status === 'active').length }}</span></header>
          <div v-if="legalHolds.length" class="admin-list">
            <article v-for="item in legalHolds" :key="item.id">
              <div><strong>@{{ item.ownerHandle }}</strong><span>{{ item.reason }}</span><small>SHA-256 {{ item.authorityReferenceHash.slice(0, 16) }}…</small></div><span>{{ t('admin.reviewDue', { date: date(item.reviewAt) }) }}</span><span :data-status="item.status">{{ t(`admin.holdStates.${item.status}`) }}</span><small>{{ t('admin.expiresAt', { date: date(item.expiresAt) }) }}</small><button v-if="item.status === 'active'" class="command-button secondary" type="button" @click="openHoldRelease(item)">
                <Undo2 :size="16" />{{ t('admin.releaseHold') }}
              </button><span v-else></span>
            </article>
          </div>
          <p v-else class="inline-empty">
            {{ t('admin.noLegalHolds') }}
          </p>
          <button v-if="legalHoldsNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="legalHoldsLoadingMore" @click="loadMoreLegalHolds">
            <LoaderCircle v-if="legalHoldsLoadingMore" class="spin" :size="16" />{{ t('actions.loadMore') }}
          </button>
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
          <header><div><h2>{{ t('admin.auditIntegrity') }}</h2><p>{{ t('admin.auditIntegritySummary') }}</p></div><span :data-status="operationalDiagnostics.audit.valid ? 'active' : 'failed'">{{ operationalDiagnostics.audit.valid ? t('admin.verified') : t('admin.integrityFailure') }}</span></header>
          <div class="admin-overview-grid diagnostics-grid">
            <article><span>{{ t('admin.auditEvents') }}</span><strong>{{ operationalDiagnostics.audit.eventCount }}</strong><small>{{ t('admin.chainSequence', { sequence: operationalDiagnostics.audit.headSequence }) }}</small></article>
            <article><span>{{ t('admin.chainHead') }}</span><strong class="hash-evidence">{{ operationalDiagnostics.audit.headHash ? `${operationalDiagnostics.audit.headHash.slice(0, 16)}…` : t('admin.emptyChain') }}</strong><small v-if="operationalDiagnostics.audit.firstInvalidSequence">{{ t('admin.firstInvalidSequence', { sequence: operationalDiagnostics.audit.firstInvalidSequence }) }}</small></article>
            <article><span>{{ t('admin.databaseReadiness') }}</span><strong>{{ operationalDiagnostics.databaseReady ? t('admin.ready') : t('admin.unavailable') }}</strong><small>{{ t('admin.observedAt', { date: date(operationalDiagnostics.asOf) }) }}</small></article>
          </div>
        </section>
      </div>

      <div v-else-if="activeTab === 'audit'" class="admin-user-directory">
        <form class="admin-user-filters admin-audit-filters" @submit.prevent="applyAuditFilters">
          <label>{{ t('admin.auditSearch') }}<input v-model="auditQuery" type="search" maxlength="120" :placeholder="t('admin.auditSearchPlaceholder')" /></label>
          <label>{{ t('admin.auditAction') }}<input v-model="auditAction" type="text" maxlength="120" :placeholder="t('admin.auditActionPlaceholder')" /></label>
          <label>{{ t('admin.auditResourceType') }}<input v-model="auditResourceType" type="text" maxlength="80" :placeholder="t('admin.auditResourceTypePlaceholder')" /></label>
          <button class="command-button primary" type="submit">
            <ListFilter :size="16" />{{ t('actions.applyFilters') }}
          </button>
          <button class="icon-button" type="button" :aria-label="t('actions.clearFilters')" :title="t('actions.clearFilters')" @click="clearAuditFilters">
            <Undo2 :size="16" />
          </button>
        </form>
        <div v-if="auditEvents.length" class="audit-list">
          <article v-for="item in auditEvents" :key="item.id">
            <Activity :size="17" /><div>
              <strong><span class="audit-sequence">#{{ item.sequence }} · </span><span>{{ item.action }}</span></strong><span>{{ item.actorHandle ? `@${item.actorHandle}` : t('admin.systemActor') }} · {{ item.resourceType }}<template v-if="item.resourceId"> · {{ item.resourceId }}</template></span><small class="hash-evidence">SHA-256 {{ item.eventHash.slice(0, 16) }}…</small><p v-if="item.reason">
                {{ item.reason }}
              </p>
            </div><small>{{ date(item.createdAt) }}<br />{{ item.requestId }}</small>
          </article>
        </div>
        <div v-else class="workspace-empty">
          <ShieldCheck :size="22" /><p>{{ t('admin.noAuditEvents') }}</p>
        </div>
        <button v-if="auditNextCursor" class="command-button secondary admin-load-more" type="button" :disabled="auditLoadingMore" @click="loadMoreAudit">
          <LoaderCircle v-if="auditLoadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
        </button>
      </div>
    </template>
  </section>
</template>
