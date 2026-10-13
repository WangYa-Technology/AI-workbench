<script setup lang="ts">
import UiEmptyState from '../components/ui/UiEmptyState.vue'
import UiActionBanner from '../components/ui/UiActionBanner.vue'
import UiCardTag from '../components/ui/UiCardTag.vue'
import CatalogToolbar from '../components/catalog/CatalogToolbar.vue'
import CatalogViews from '../components/catalog/CatalogViews.vue'
import CatalogResults from '../components/catalog/CatalogResults.vue'
import CatalogKeyword from '../components/catalog/CatalogKeyword.vue'
import DetailToolbar from '../components/ui/DetailToolbar.vue'
import UiLayoutSwitcher from '../components/ui/UiLayoutSwitcher.vue'
import UiCatalog from '../components/ui/UiCatalog.vue'
import UiCategorySidebar from '../components/ui/UiCategorySidebar.vue'
import UiCardMedia from '../components/ui/UiCardMedia.vue'
import UiContentCard from '../components/ui/UiContentCard.vue'
import {
  AlertTriangle, ArrowDownWideNarrow, ArrowLeft, BriefcaseBusiness,  Check, ChevronRight,
  CircleDollarSign, Clock3, FileCheck2, Filter, Image as ImageIcon, Lightbulb, LoaderCircle, LogIn,
  MessageSquareText, Music2, Play, Plus, RefreshCw, Search, Shapes, ShieldCheck, UserPlus,
  UserRound, Video, WandSparkles, Workflow, X,
} from 'lucide-vue-next'
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import {
  api, APIError, messageFrom, type Asset, type TaskCreate, type TaskDetail, type TaskSummary, type TaskType,
} from '../api/client'
import { formatCurrency, formatDateTime } from '../lib/format'
import { contentListReturn, creationPath } from '../lib/contentPresentation'
import AssetMedia from '../components/domain/AssetMedia.vue'
import { openCheckoutWindow } from '../lib/checkout'
import { useSessionStore } from '../stores/session'
import PageHeader from '../components/ui/PageHeader.vue'
import { taskExampleCovers } from '../dev/taskExampleCovers'
import UiButton from '../components/ui/UiButton.vue'
import UiCheckbox from '../components/ui/UiCheckbox.vue'
import UiIconButton from '../components/ui/UiIconButton.vue'
import UiInput from '../components/ui/UiInput.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiTextarea from '../components/ui/UiTextarea.vue'

import { taskDeadlineISO, taskLocalMinute } from '../lib/taskDateTime'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const tasks = ref<TaskSummary[]>([])
const taskTypeCounts = ref<Record<string, number>>({})
const taskTotal = ref(0)
const catalogTotal = computed(() => Object.values(taskTypeCounts.value).reduce((sum, count) => sum + count, 0))
const nextTaskCursor = ref<string>()
const loadingMore = ref(false)
const loadMoreError = ref('')
const detail = ref<TaskDetail | null>(null)
const assets = ref<Asset[]>([])
const assetSearch = ref('')
const assetQuery = ref('')
const assetCursor = ref<string>()
const assetsLoading = ref(false)
const assetsError = ref('')
let assetVersion = 0
let assetRetry = { more: false, pinID: '' }
const loading = ref(true)
const actionLoading = ref(false)
const error = ref('')
const success = ref('')
const createOpen = ref(false)
const taskTitleInput = ref<InstanceType<typeof UiInput> | null>(null)
const taskCreateModal = ref<InstanceType<typeof globalThis.HTMLElement> | null>(null)
const proposalOpen = ref(false)
const disputeOpen = ref(false)
const cancelOpen = ref(false)
const taskPaymentEnabled = ref(false)
const paymentLiveMode = ref(false)
const payoutReady = ref(false)
const fundingRefreshLoading = ref(false)
let disposed = false
let fundingPollTimer: number | undefined

const search = ref(String(route.query.q || ''))
const deliverableType = ref(String(route.query.type || ''))
const view = ref(route.query.view === 'mine' ? 'mine' : 'available')
const status = ref(String(route.query.status ?? (view.value === 'mine' ? '' : 'open')))
const sort = ref(String(route.query.sort || 'newest'))
const layoutMode = ref<'list' | 'grid'>('list')
const taskToolbarHeight = ref(56)
const viewTabs = computed(() => [
  { value: 'available', label: t('catalog.views.available'), icon: BriefcaseBusiness },
  { value: 'mine', label: t('catalog.views.mine'), icon: Clock3 },
])
const hasTaskFilters = computed(() => Boolean(search.value.trim() || deliverableType.value || status.value !== (view.value === 'mine' ? '' : 'open')))
const taskID = computed(() => String(route.params.id || ''))
const isDetail = computed(() => Boolean(taskID.value))
const canPublishBrief = computed(() => Boolean(session.user))
const canPropose = computed(() => Boolean(session.user) && detail.value?.viewerRole === 'viewer' && detail.value.status === 'open' && new Date(detail.value.deadline).getTime() > Date.now() && !detail.value.proposals.length)
const fundingConfirmed = computed(() => Boolean(detail.value?.funding && ['paid', 'transfer_pending', 'transferred'].includes(detail.value.funding.status)))
const directFundingConfirmed = computed(() => fundingConfirmed.value && !detail.value?.funding?.proposalId)
const canClaim = computed(() => Boolean(session.user) && detail.value?.viewerRole === 'viewer' && detail.value.status === 'open' && new Date(detail.value.deadline).getTime() > Date.now() && detail.value?.allowDirectAccept && (directFundingConfirmed.value || !taskPaymentEnabled.value))
const canFundDirect = computed(() => Boolean(
  detail.value?.viewerRole === 'client' && detail.value.status === 'open' && detail.value.allowDirectAccept
  && canStartFundingFor(),
))
const canDeliver = computed(() => Boolean(session.user) && detail.value?.viewerRole === 'assignee' && ['assigned', 'revision'].includes(detail.value.status))
const canReview = computed(() => Boolean(session.user) && detail.value?.viewerRole === 'client' && detail.value.status === 'submitted')
const canDispute = computed(() => Boolean(session.user) && detail.value && ['assigned', 'submitted', 'revision'].includes(detail.value.status) && ['client', 'assignee'].includes(detail.value.viewerRole))
const canCancel = computed(() => Boolean(session.user) && detail.value?.viewerRole === 'client' && detail.value.status === 'open')
const taskActionLabel = computed(() => {
  if (!session.user) return t('tasks.signInToRespond')
  if (detail.value?.viewerRole === 'client') return t('tasks.manageTask')
  if (detail.value?.viewerRole === 'assignee') return t('tasks.continueTask')
  return t('tasks.reviewOpportunity')
})
const minimumDeadline = computed(() => taskLocalMinute(new Date(Date.now() + 60_000), draft.timezone))

const proposal = reactive({ approach: '', deliverables: '', amount: '', timelineDays: '7' })
const delivery = reactive({ assetId: '', note: '', extraAssetIds: [] as string[], rightsEvidence: '', aiDisclosure: '', rightsConfirmed: false })
const reviewNote = ref('')
const disputeReason = ref('')
const cancelReason = ref('')
const deadlineRequest = reactive({ deadline: '', reason: '' })
const deadlineFormOpen = ref(false)
const freshDraft = () => ({
  title: '', summary: '', brief: '', deliverableType: 'image', budget: '', deadline: '', timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC',
  deliverables: '', acceptanceRules: '', rightsTerms: '', aiDisclosureRequirement: '', allowDirectAccept: false, allowDerivativeReuse: false,
})
const draft = reactive(freshDraft())

const taskTypes = ref<TaskType[]>([])
const types = computed(() => taskTypes.value.map((item) => item.code))
const statuses = ['open', 'assigned', 'submitted', 'revision', 'accepted', 'disputed', 'cancelled']
const timezoneOptions = Array.from(new Set([
  Intl.DateTimeFormat().resolvedOptions().timeZone,
  'UTC', 'America/Los_Angeles', 'America/New_York', 'Europe/London', 'Europe/Berlin', 'Asia/Shanghai', 'Asia/Tokyo', 'Australia/Sydney',
])).filter(Boolean)
let createTrigger: InstanceType<typeof globalThis.HTMLElement> | null = null

function money(cents: number, currency = 'USD') {
  return formatCurrency(cents, currency, locale.value)
}

function date(value: string, timeZone = 'UTC') {
  try { return formatDateTime(value, locale.value, timeZone) } catch { return formatDateTime(value, locale.value, 'UTC') }
}

function deadlineDate(value: string, timeZone = 'UTC') {
  const options: Intl.DateTimeFormatOptions = { dateStyle: 'medium', timeZone }
  try { return new Intl.DateTimeFormat(locale.value, options).format(new Date(value)) }
  catch { return new Intl.DateTimeFormat(locale.value, { ...options, timeZone: 'UTC' }).format(new Date(value)) }
}

function lines(value: string) {
  return value.split('\n').map((item) => item.trim()).filter(Boolean)
}

function eventLabel(kind: string) {
  return t(`tasks.events.${kind}`)
}

function taskTypeIcon(kind: string) {
  if (kind === 'image') return ImageIcon
  if (kind === 'video') return Video
  if (kind === 'audio') return Music2
  if (kind === 'prompt') return MessageSquareText
  if (kind === 'workflow') return Workflow
  return Shapes
}

function taskThumbnail(item: { id: string; deliverableType: string }) {
  if (import.meta.env.DEV && taskExampleCovers[item.id]) return taskExampleCovers[item.id]
  const kind = item.deliverableType
  if (kind === 'video') return '/tasks/task-video.webp'
  if (kind === 'audio') return '/tasks/task-audio.webp'
  if (kind === 'image') return '/tasks/task-image.webp'
  return '/tasks/task-hero-transparent.webp'
}
function taskTypeLabel(code: string) {
  const item = taskTypes.value.find((type) => type.code === code)
  return locale.value.startsWith('zh') ? (item?.nameZh || code) : (item?.nameEn || code)
}

let loadVersion = 0
let commandVersion = 0

function resetTaskContext() {
  commandVersion++
  assetVersion++
  detail.value = null
  assets.value = []; assetCursor.value = undefined; assetSearch.value = ''; assetQuery.value = ''; assetsError.value = ''; assetsLoading.value = false
  proposalOpen.value = false; disputeOpen.value = false; cancelOpen.value = false; deadlineFormOpen.value = false
  actionLoading.value = false; fundingRefreshLoading.value = false; payoutReady.value = false
  Object.assign(proposal, { approach: '', deliverables: '', amount: '', timelineDays: '7' })
  Object.assign(delivery, { assetId: '', note: '', extraAssetIds: [], rightsEvidence: '', aiDisclosure: '', rightsConfirmed: false })
  reviewNote.value = ''; disputeReason.value = ''; cancelReason.value = ''
  Object.assign(deadlineRequest, { deadline: '', reason: '' })
  if (fundingPollTimer !== undefined) globalThis.window.clearTimeout(fundingPollTimer)
  fundingPollTimer = undefined
}
function failTask(reason: unknown) {
  if (reason instanceof APIError && [401, 403, 404].includes(reason.status)) {
    loadVersion++
    resetTaskContext()
    createOpen.value = false; Object.assign(draft, freshDraft())
    tasks.value = []; nextTaskCursor.value = undefined; taskTotal.value = 0; taskTypeCounts.value = {}
    loading.value = false; loadingMore.value = false
  }
  error.value = messageFrom(reason)
}
function eligibleAsset(asset: Asset) {
  return asset.scanStatus === 'clean' && asset.sourceType !== 'purchase' && asset.licenseCode !== 'task-contract'
}
async function loadDeliveryAssets(more = false, pinID = '') {
  if (!canDeliver.value || (more && (!assetCursor.value || assetsLoading.value))) return
  const context = loadVersion
  const version = ++assetVersion
  const query = more ? assetQuery.value : assetSearch.value.trim()
  const cursor = more ? assetCursor.value : undefined
  assetRetry = { more, pinID }
  assetsLoading.value = true; assetsError.value = ''
  try {
    const page = await api.listAssets({ purpose: 'task_delivery', q: query || undefined, limit: 20, cursor })
    if (context !== loadVersion || version !== assetVersion) return
    // Keep explicitly selected assets available while searching another page.
    const keep = new Set([delivery.assetId, ...delivery.extraAssetIds])
    const retained = more ? assets.value : assets.value.filter(item => keep.has(item.id))
    const merged = new Map(retained.map(item => [item.id, item]))
    for (const item of page.items.filter(eligibleAsset)) merged.set(item.id, item)
    assets.value = [...merged.values()]
    assetCursor.value = page.nextCursor || undefined
    assetQuery.value = query
    if (pinID && !merged.has(pinID)) {
      const selected = await api.getAsset(pinID)
      if (context !== loadVersion || version !== assetVersion) return
      if (!selected.taskDeliveryEligible || !eligibleAsset(selected)) {
        assetsError.value = t('tasks.assetUnavailable')
      } else { assets.value.unshift(selected); delivery.assetId = selected.id }
    } else if (pinID) delivery.assetId = pinID
    if (!delivery.assetId && assets.value.length) delivery.assetId = assets.value[0]!.id
  } catch (reason) {
    if (context === loadVersion && version === assetVersion) {
      if (reason instanceof APIError && reason.status === 401) failTask(reason)
      else assetsError.value = messageFrom(reason)
    }
  } finally {
    if (context === loadVersion && version === assetVersion) assetsLoading.value = false
  }
}
async function load() {
  const version = ++loadVersion
  const id = taskID.value
  actionLoading.value = false; fundingRefreshLoading.value = false; loadingMore.value = false; loadMoreError.value = ''
  if (detail.value?.id !== id) detail.value = null
  const criteria = { q: search.value, type: deliverableType.value, status: status.value, sort: sort.value, mine: view.value === 'mine' }
  // Replacing a long list with skeletons must not clamp the scroll position
  // and replay the sticky toolbar's expanded/compact transition.
  loading.value = true
  error.value = ''
  success.value = ''
  try {
    await session.ensure()
    if (version !== loadVersion) return
    if (!id && route.query.publish === '1') {
      if (!session.user) {
        await router.replace({ path: '/auth', query: { auth: 'login', returnTo: route.fullPath } })
        return
      }
      createOpen.value = true
    }
    const [runtime, configuredTypes] = await Promise.all([api.meta(), api.listTaskTypes()])
    if (version !== loadVersion) return
    taskTypes.value = configuredTypes.items
    taskPaymentEnabled.value = runtime.taskPaymentProvider.enabled
    paymentLiveMode.value = runtime.taskPaymentProvider.liveMode
    if (id) {
      const task = await api.getTask(id)
      if (version !== loadVersion) return
      detail.value = task
      if (canDeliver.value) {
        await loadDeliveryAssets(false, typeof route.query.assetId === 'string' ? route.query.assetId : '')
        if (version !== loadVersion) return
      }
      if (session.user && taskPaymentEnabled.value && ['viewer', 'assignee'].includes(task.viewerRole)) {
        const payout = await api.getPayoutStatus()
        if (version !== loadVersion) return
        payoutReady.value = payout.status === 'verified' && payout.chargesEnabled && payout.payoutsEnabled
      }
      applyPaymentReturnState()
      scheduleFundingRefresh()
    } else {
      const response = await api.listTasks(criteria)
      if (version !== loadVersion) return
      tasks.value = response.items
      taskTotal.value = response.total
      taskTypeCounts.value = response.typeCounts
      nextTaskCursor.value = response.nextCursor
      detail.value = null
    }
  } catch (reason) {
    if (version === loadVersion) failTask(reason)
  } finally {
    if (version === loadVersion) loading.value = false
  }
}

async function loadMoreTasks() {
  if (loading.value || !nextTaskCursor.value || loadingMore.value) return
  const version = loadVersion
  loadingMore.value = true
  loadMoreError.value = ''
  try {
    const response = await api.listTasks({ q: search.value, type: deliverableType.value, status: status.value, sort: sort.value, mine: view.value === 'mine', cursor: nextTaskCursor.value })
    if (version !== loadVersion) return
    const known = new Set(tasks.value.map(item => item.id))
    tasks.value.push(...response.items.filter(item => !known.has(item.id)))
    taskTotal.value = response.total
    taskTypeCounts.value = response.typeCounts
    nextTaskCursor.value = response.nextCursor
  } catch (reason) { if (version === loadVersion) {
    if (reason instanceof APIError && [401, 403, 404].includes(reason.status)) failTask(reason)
    else loadMoreError.value = messageFrom(reason)
  } }
  finally { if (version === loadVersion) loadingMore.value = false }
}

function fundingMatches(proposalId?: string) {
  if (!detail.value?.funding) return false
  return proposalId ? detail.value.funding.proposalId === proposalId : !detail.value.funding.proposalId
}

function canStartFundingFor(proposalId?: string) {
  const funding = detail.value?.funding
  if (!funding || ['payment_failed', 'cancelled', 'refunded'].includes(funding.status)) return true
  return fundingMatches(proposalId) && ['checkout_pending', 'checkout_open'].includes(funding.status)
}

function canFundProposal(proposalId: string) {
  return Boolean(detail.value?.viewerRole === 'client' && detail.value.status === 'open' && canStartFundingFor(proposalId))
}

function canAcceptProposal(proposalId: string) {
  if (detail.value?.viewerRole !== 'client' || detail.value.status !== 'open') return false
  return fundingConfirmed.value && fundingMatches(proposalId)
}

function fundingActionLabel(proposalId?: string) {
  if (fundingMatches(proposalId) && detail.value?.funding?.status === 'checkout_open') return t('tasks.resumeFunding')
  if (fundingMatches(proposalId) && detail.value?.funding?.status === 'checkout_pending') return t('tasks.retryFunding')
  return proposalId ? t('tasks.fundProposal') : t('tasks.fundTask')
}

function fundingRequestKey(proposalId?: string) {
  const base = `task-funding-${detail.value?.id}-${proposalId || 'direct'}`
  if (detail.value?.funding && ['payment_failed', 'cancelled', 'refunded'].includes(detail.value.funding.status)) {
    return `${base}-${new Date(detail.value.funding.updatedAt).getTime()}`
  }
  return base
}

async function fundTask(proposalId?: string) {
  if (!detail.value || !taskPaymentEnabled.value || actionLoading.value) return
  commandVersion++
  const current = detail.value.funding
  if (fundingMatches(proposalId) && current?.status === 'checkout_open' && current.checkoutUrl) {
    const checkoutWindow = openCheckoutWindow(current.checkoutUrl)
    if (!checkoutWindow) {
      error.value = t('tasks.checkoutPopupBlocked')
    }
    return
  }
  const version = loadVersion
  const id = detail.value.id
  actionLoading.value = true
  error.value = ''
  let checkoutWindow = null as ReturnType<typeof globalThis.open>
  try {
    checkoutWindow = openCheckoutWindow()
    if (!checkoutWindow) {
      error.value = t('errors.codes.checkout_popup_blocked')
      return
    }
    const checkout = await api.checkoutTask(
      id,
      proposalId ? { proposalId } : {},
      fundingRequestKey(proposalId),
    )
    if (disposed || version !== loadVersion) { checkoutWindow.close(); return }
    checkoutWindow.location.href = checkout.checkoutUrl
    const task = await api.getTask(id)
    if (disposed || version !== loadVersion) return
    detail.value = task
    scheduleFundingRefresh()
  } catch (reason) {
    checkoutWindow?.close()
    if (version === loadVersion) failTask(reason)
    scheduleFundingRefresh()
  } finally {
    if (version === loadVersion) actionLoading.value = false
  }
}

function applyPaymentReturnState() {
  if (!detail.value || !taskPaymentEnabled.value) return
  if (route.query.payment === 'cancelled') success.value = t('tasks.fundingCheckoutCancelled')
  if (route.query.payment === 'success') success.value = t(fundingConfirmed.value ? 'tasks.fundingConfirmed' : 'tasks.fundingAwaitingConfirmation')
}

function scheduleFundingRefresh() {
  if (disposed || fundingPollTimer !== undefined || !detail.value || !taskPaymentEnabled.value) return
  if (!['checkout_pending', 'checkout_open', 'transfer_pending', 'refund_pending'].includes(detail.value.funding?.status || '')) return
  fundingPollTimer = globalThis.window.setTimeout(() => {
    fundingPollTimer = undefined
    if (globalThis.document.visibilityState === 'hidden') { scheduleFundingRefresh(); return }
    void refreshFunding()
  }, 5000)
}

async function refreshFunding() {
  if (disposed || !taskID.value || fundingRefreshLoading.value || actionLoading.value) { scheduleFundingRefresh(); return }
  const version = loadVersion
  const mutation = commandVersion
  const id = taskID.value
  fundingRefreshLoading.value = true
  try {
    const task = await api.getTask(id)
    if (disposed || version !== loadVersion || mutation !== commandVersion) return
    detail.value = task
    if (session.user && taskPaymentEnabled.value && ['viewer', 'assignee'].includes(task.viewerRole)) {
      const payout = await api.getPayoutStatus()
      if (disposed || version !== loadVersion || mutation !== commandVersion) return
      payoutReady.value = payout.status === 'verified' && payout.chargesEnabled && payout.payoutsEnabled
    }
    applyPaymentReturnState()
  } catch (reason) { if (!disposed && version === loadVersion && mutation === commandVersion) failTask(reason) }
  finally { if (version === loadVersion) { fundingRefreshLoading.value = false; scheduleFundingRefresh() } }
}

async function refreshOnReturn() {
  if (globalThis.document.visibilityState === 'hidden') return
  await session.ensure(true)
  if (!disposed && !loading.value) await refreshFunding()
}

async function applyFilters() {
  await router.push({ path: '/market/demands', query: {
    ...(view.value === 'mine' ? { view: 'mine' } : {}),
    ...(search.value.trim() ? { q: search.value.trim() } : {}),
    ...(deliverableType.value ? { type: deliverableType.value } : {}),
    status: status.value,
    ...(sort.value !== 'newest' ? { sort: sort.value } : {}),
  } })
}

async function selectView(nextView: 'available' | 'mine') {
  if (view.value === nextView) return
  view.value = nextView
  status.value = nextView === 'mine' ? '' : 'open'
  await applyFilters()
}

async function selectTaskType(nextType: string) {
  if (deliverableType.value === nextType) return
  deliverableType.value = nextType
  await applyFilters()
}

async function clearSearch() {
  await router.push({ path: route.path, query: { ...route.query, q: undefined } })
}

async function clearFilters() {
  search.value = ''
  deliverableType.value = ''
  status.value = view.value === 'mine' ? '' : 'open'
  sort.value = 'newest'
  await applyFilters()
}

async function mutate(action: () => Promise<TaskDetail>, message: string) {
  if (actionLoading.value) return
  commandVersion++
  const version = loadVersion
  actionLoading.value = true
  error.value = ''
  success.value = ''
  try {
    const result = await action()
    if (version !== loadVersion) return
    detail.value = result
    scheduleFundingRefresh()
    success.value = message
    proposalOpen.value = false
    disputeOpen.value = false
    cancelOpen.value = false
    if (canDeliver.value) await loadDeliveryAssets()
  } catch (reason) {
    if (version === loadVersion) failTask(reason)
  } finally {
    if (version === loadVersion) actionLoading.value = false
  }
}

async function submitProposal() {
  if (!detail.value) return
  await mutate(() => api.proposeTask(detail.value!.id, {
    approach: proposal.approach.trim(), deliverables: proposal.deliverables.trim(),
    amountCents: Math.round(Number(proposal.amount) * 100), timelineDays: Number(proposal.timelineDays),
  }), t('tasks.proposalSent'))
}

function showProposalForm() {
  if (!proposal.amount && detail.value) proposal.amount = String(detail.value.budgetCents / 100)
  proposalOpen.value = true
}

async function claimTask() {
  if (!detail.value || !taskPaymentEnabled.value) return
  await mutate(() => api.claimTask(detail.value!.id), t('tasks.assigned'))
}

async function acceptProposal(proposalId: string) {
  if (!detail.value || !taskPaymentEnabled.value) return
  await mutate(() => api.acceptTaskProposal(detail.value!.id, proposalId), t('tasks.assigned'))
}

async function submitDelivery() {
  if (!canDeliver.value || !detail.value) return
  if (new Set([delivery.assetId, ...delivery.extraAssetIds]).size > 20) { error.value = t('tasks.deliveryLimit'); return }
  await mutate(() => api.deliverTask(detail.value!.id, { assetIds: [delivery.assetId, ...delivery.extraAssetIds.filter(id => id !== delivery.assetId)], note: delivery.note.trim(), rightsEvidence: delivery.rightsEvidence.trim(), aiDisclosure: delivery.aiDisclosure.trim(), rightsConfirmed: delivery.rightsConfirmed }), t('tasks.delivered'))
}

async function review(decision: 'accept' | 'request_revision') {
  if (!detail.value) return
  await mutate(
    () => api.reviewTask(detail.value!.id, { decision, note: reviewNote.value.trim() }),
    decision === 'accept' ? t('tasks.acceptedProvider') : t('tasks.revisionSent'),
  )
}

async function openDispute() {
  if (!detail.value) return
  await mutate(() => api.disputeTask(detail.value!.id, disputeReason.value.trim()), t('tasks.disputed'))
}

async function cancelTask() {
  if (!detail.value) return
  await mutate(() => api.cancelTask(detail.value!.id, cancelReason.value.trim()), t('tasks.cancelled'))
}

async function changeDeadline(decision: 'propose' | 'accept' | 'reject') {
  if (!detail.value) return
  let deadline: string | undefined
  if (decision === 'propose') {
    try { deadline = taskDeadlineISO(deadlineRequest.deadline, detail.value.clientTimezone) }
    catch { error.value = t('tasks.invalidDeadline'); return }
  }
  await mutate(() => api.changeTaskDeadline(detail.value!.id, decision === 'propose'
    ? { decision, deadline, reason: deadlineRequest.reason.trim() }
    : { decision, changeId: detail.value!.deadlineChange?.id }), t('tasks.deadlineUpdated'))
}

async function publishTask() {
  if (actionLoading.value || !session.user) return
  const version = loadVersion
  let deadline: string
  try { deadline = taskDeadlineISO(draft.deadline, draft.timezone) } catch { error.value = t('tasks.invalidDeadline'); return }
  const input: TaskCreate = {
    title: draft.title.trim(), summary: draft.summary.trim(), brief: draft.brief.trim(),
    deliverableType: draft.deliverableType as TaskCreate['deliverableType'], deliverables: lines(draft.deliverables),
    acceptanceRules: lines(draft.acceptanceRules), rightsTerms: draft.rightsTerms.trim(),
    aiDisclosureRequirement: draft.aiDisclosureRequirement.trim(), budgetCents: Math.round(Number(draft.budget) * 100),
    currency: 'USD', deadline, clientTimezone: draft.timezone.trim(),
    allowDirectAccept: draft.allowDirectAccept, allowDerivativeReuse: draft.allowDerivativeReuse,
  }
  actionLoading.value = true
  error.value = ''
  try {
    const created = await api.createTask(input)
    if (version !== loadVersion || disposed) return
    createOpen.value = false
    await router.push(`/market/demands/${created.id}`)
  } catch (reason) {
    if (version === loadVersion) failTask(reason)
  } finally {
    if (version === loadVersion) actionLoading.value = false
  }
}

watch([() => route.fullPath, () => session.user?.id], (current, previous) => {
  const identityChanged = current[1] !== previous[1]
  const taskChanged = detail.value?.id !== taskID.value
  if (identityChanged || taskChanged) resetTaskContext()
  if (identityChanged) {
    tasks.value = []; taskTypeCounts.value = {}; taskTotal.value = 0; nextTaskCursor.value = undefined
    createOpen.value = false; Object.assign(draft, freshDraft())
  }
  if (fundingPollTimer !== undefined) globalThis.window.clearTimeout(fundingPollTimer)
  fundingPollTimer = undefined
  search.value = String(route.query.q || '')
  deliverableType.value = String(route.query.type || '')
  view.value = route.query.view === 'mine' ? 'mine' : 'available'
  status.value = String(route.query.status ?? (view.value === 'mine' ? '' : 'open'))
  sort.value = String(route.query.sort || 'newest')
  void load()
}, { flush: 'sync' })

watch(createOpen, async (open) => {
  if (open) {
    createTrigger = globalThis.document.activeElement instanceof globalThis.HTMLElement ? globalThis.document.activeElement : null
    await nextTick()
    taskTitleInput.value?.focus()
    return
  }
  if (route.query.publish === '1') {
    const query = { ...route.query }
    delete query.publish
    void router.replace({ path: route.path, query })
  }
  createTrigger?.focus()
  createTrigger = null
})

function handleWindowKeydown(event: InstanceType<typeof globalThis.KeyboardEvent>) {
  if (!createOpen.value) return
  if (event.key === 'Escape') {
    createOpen.value = false
    return
  }
  if (event.key !== 'Tab' || !taskCreateModal.value) return

  const focusable = Array.from(taskCreateModal.value.querySelectorAll('button, input, select, textarea, [href], [tabindex]'))
    .filter((element): element is InstanceType<typeof globalThis.HTMLElement> => (
      element instanceof globalThis.HTMLElement
      && !element.hasAttribute('disabled')
      && element.getAttribute('tabindex') !== '-1'
    ))
  const first = focusable[0]
  const last = focusable.at(-1)
  const active = globalThis.document.activeElement
  if (!first || !last) return
  if (event.shiftKey && (active === first || !taskCreateModal.value.contains(active))) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && active === last) {
    event.preventDefault()
    first.focus()
  }
}

onMounted(() => {
  globalThis.window.addEventListener('keydown', handleWindowKeydown)
  globalThis.window.addEventListener('focus', refreshOnReturn)
  globalThis.document.addEventListener('visibilitychange', refreshOnReturn)
  void load()
})
onBeforeUnmount(() => {
  disposed = true
  globalThis.window.removeEventListener('focus', refreshOnReturn)
  globalThis.document.removeEventListener('visibilitychange', refreshOnReturn)
  loadVersion++
  globalThis.window.removeEventListener('keydown', handleWindowKeydown)
  if (fundingPollTimer !== undefined) globalThis.window.clearTimeout(fundingPollTimer)
})
</script>

<template>
  <section class="task-market content-width" :class="{ 'is-detail': isDetail, 'catalog-page': !isDetail }" :style="{ '--catalog-toolbar-height': `${taskToolbarHeight}px` }">
    <template v-if="!isDetail">
      <PageHeader
        :title="t('tasks.title')"
        :summary="t('tasks.summary')"
        artwork-src="/illustrations/headers/tasks.webp"
      >
        <template #actions>
          <UiButton v-if="session.user" as="RouterLink" class="command-button secondary" variant="secondary" to="/workspace/tasks">
            <template #start>
              <BriefcaseBusiness :size="17" />
            </template>{{ t('tasks.myTasks') }}
          </UiButton>
          <UiButton v-if="canPublishBrief" class="command-button primary" variant="primary" @click="createOpen = true">
            <template #start>
              <Plus :size="17" />
            </template>{{ t('tasks.publishBrief') }}
          </UiButton>
          <UiButton v-if="!session.user" as="RouterLink" class="command-button secondary" variant="secondary" :to="{ path: '/auth', query: { auth: 'login', returnTo: route.fullPath } }">
            <template #start>
              <LogIn :size="17" />
            </template>{{ t('account.signIn') }}
          </UiButton>
          <UiButton v-if="!session.user" as="RouterLink" class="command-button primary" variant="primary" :to="{ path: '/auth', query: { auth: 'register', returnTo: route.fullPath } }">
            <template #start>
              <UserPlus :size="17" />
            </template>{{ t('account.createAccount') }}
          </UiButton>
        </template>
      </PageHeader>

      <CatalogToolbar class="task-catalog-bar" filter-class="task-toolbar" :title="t('tasks.title')" @resize="taskToolbarHeight = $event">
        <CatalogViews class="task-view-tabs" :model-value="view" :items="session.user ? viewTabs : viewTabs.slice(0, 1)" :label="t('tasks.views')" :count="taskTotal" :pending="loading || Boolean(error)" :status="loading ? t('tasks.loading') : error ? t('tasks.unavailable') : `${taskTotal} ${t('tasks.results')}`" @update:model-value="selectView($event as 'available' | 'mine')" />
        <div class="task-filters catalog-controls" role="group" :aria-label="t('search.taskFilters')">
          <UiSelect v-model="deliverableType" class="task-toolbar__type" :aria-label="t('tasks.allTypes')" :align-item-with-trigger="false" @change="applyFilters">
            <template #start>
              <Filter :size="15" aria-hidden="true" />
            </template>
            <option value="">
              {{ t('tasks.allTypes') }}
            </option>
            <option v-for="item in types" :key="item" :value="item">
              {{ taskTypeLabel(item) }}
            </option>
          </UiSelect>
          <UiSelect v-model="status" class="task-toolbar__status" :align-item-with-trigger="false" :aria-label="t('tasks.allStatuses')" @change="applyFilters">
            <option value="">
              {{ t('tasks.allStatuses') }}
            </option>
            <option v-for="item in statuses" :key="item" :value="item">
              {{ t(`tasks.status.${item}`) }}
            </option>
          </UiSelect>
          <UiButton v-if="hasTaskFilters || sort !== 'newest'" class="task-toolbar__reset catalog-reset" variant="secondary" @click="clearFilters">
            {{ t('tasks.clearFilters') }}
          </UiButton>
          <div class="catalog-display">
            <UiSelect v-model="sort" class="task-toolbar__sort" :align-item-with-trigger="false" :aria-label="t('tasks.sortNewest')" @change="applyFilters">
              <template #start>
                <ArrowDownWideNarrow :size="15" aria-hidden="true" />
              </template>
              <option value="newest">
                {{ t('tasks.sortNewest') }}
              </option>
              <option value="deadline">
                {{ t('tasks.sortDeadline') }}
              </option>
              <option value="budget_desc">
                {{ t('tasks.sortBudget') }}
              </option>
            </UiSelect>
            <UiLayoutSwitcher v-model="layoutMode" :label="t('tasks.layout')" :list-label="t('tasks.listView')" :grid-label="t('tasks.gridView')" />
          </div>
        </div>
        <CatalogKeyword :query="search" @clear="clearSearch" />
      </CatalogToolbar>

      <div class="task-browser-layout">
        <UiCategorySidebar :title="t('tasks.categories')" :model-value="deliverableType" :items="[{ value: '', label: t('catalog.views.all'), icon: BriefcaseBusiness, count: catalogTotal }, ...types.map(type => ({ value: type, label: taskTypeLabel(type), icon: taskTypeIcon(type), count: taskTypeCounts[type] || 0 }))]" @update:model-value="selectTaskType" />

        <CatalogResults class="task-results" :loading="loading">
          <div v-if="loading" class="task-skeleton" aria-live="polite">
            <span v-for="item in 4" :key="item"></span><p>{{ t('tasks.loading') }}</p>
          </div>
          <div v-else-if="error" class="task-market-state task-market-error" role="alert">
            <span><AlertTriangle :size="20" /></span><strong>{{ t('tasks.unavailable') }}</strong><p>{{ error }}</p><UiButton class="command-button secondary" variant="secondary" @click="load">
              <template #start>
                <RefreshCw :size="17" />
              </template>{{ t('actions.retry') }}
            </UiButton>
          </div>
          <template v-else>
            <UiCatalog class="ui-catalog task-result-list" :class="{ 'is-grid': layoutMode === 'grid' }">
              <UiContentCard v-for="item in tasks" :key="item.id" class="task-row" :data-type="item.deliverableType" :to="`/market/demands/${item.id}`">
                <UiCardMedia class="task-row-media">
                  <img :src="taskThumbnail(item)" alt="" loading="lazy" />
                </UiCardMedia>
                <h2 class="task-card-title">
                  {{ item.title }}
                </h2>
                <p class="task-card-summary">
                  {{ item.summary }}
                </p>
                <div class="task-card-tags">
                  <span class="task-card-type"><component :is="taskTypeIcon(item.deliverableType)" :size="14" aria-hidden="true" />{{ taskTypeLabel(item.deliverableType) }}</span>
                  <UiCardTag :variant="item.status === 'open' || item.status === 'accepted' ? 'success' : item.status === 'disputed' ? 'danger' : item.status === 'revision' ? 'warning' : 'neutral'">
                    {{ t(`tasks.status.${item.status}`) }}
                  </UiCardTag>
                  <span v-if="item.allowDirectAccept && item.status === 'open'">{{ t('tasks.direct') }}</span>
                </div>
                <div class="task-card-meta">
                  <span>@{{ item.client.handle }}</span><span>{{ item.proposalCount }} {{ t('tasks.proposalCount') }}</span>
                </div>
                <div class="task-card-budget">
                  <span>{{ t('content.budget') }}</span><strong>{{ money(item.budgetCents, item.currency) }}</strong>
                </div>
                <div class="task-card-footer">
                  <span class="task-card-deadline" :title="date(item.deadline, item.clientTimezone)"><Clock3 :size="13" aria-hidden="true" />{{ t('tasks.deadline') }} · <time :datetime="item.deadline">{{ deadlineDate(item.deadline, item.clientTimezone) }}</time></span>
                  <span class="task-card-open">{{ t('tasks.reviewBrief') }}<ChevronRight :size="15" aria-hidden="true" /></span>
                </div>
              </UiContentCard>
              <UiEmptyState
                v-if="!tasks.length" class="task-market-empty"
                :title="t(hasTaskFilters ? 'tasks.noResults' : view === 'mine' ? 'tasks.noMyActivity' : 'tasks.emptyTitle')"
                :message="t(hasTaskFilters ? 'tasks.emptyFilteredSummary' : view === 'mine' ? 'tasks.noMyActivitySummary' : 'tasks.emptySummary')"
              >
                <template #icon>
                  <component :is="hasTaskFilters ? Search : BriefcaseBusiness" :size="24" :stroke-width="1.75" />
                </template>
                <template #actions>
                  <UiButton v-if="hasTaskFilters" variant="secondary" @click="clearFilters">
                    {{ t('tasks.clearFilters') }}
                  </UiButton>
                  <UiButton v-else-if="view === 'mine'" variant="secondary" @click="selectView('available')">
                    {{ t('tasks.browseTasks') }}
                  </UiButton>
                  <UiButton v-else as="RouterLink" variant="secondary" to="/create/image">
                    {{ t('tasks.createInstead') }}
                  </UiButton>
                </template>
              </UiEmptyState>
            </UiCatalog>
            <div v-if="loadMoreError" class="task-feedback error" role="alert">
              {{ loadMoreError }}
            </div>
            <UiButton v-if="nextTaskCursor" variant="secondary" :loading="loadingMore" @click="loadMoreTasks">
              {{ t(loadMoreError ? 'actions.retry' : 'actions.loadMore') }}
            </UiButton>
            <UiActionBanner v-if="canPublishBrief" :title="t('tasks.publishIdea')" :summary="t('tasks.publishIdeaSummary')">
              <template #icon>
                <Lightbulb :size="24" :stroke-width="1.75" />
              </template>
              <template #actions>
                <UiButton variant="secondary" @click="createOpen = true">
                  <template #start>
                    <Plus :size="17" :stroke-width="1.75" />
                  </template>
                  {{ t('tasks.publishBrief') }}
                </UiButton>
              </template>
            </UiActionBanner>
          </template>
        </CatalogResults>
      </div>
    </template>

    <template v-else>
      <DetailToolbar class="task-detail-toolbar">
        <RouterLink class="text-link task-back" :to="contentListReturn('/market/demands')">
          <ArrowLeft :size="17" />{{ t('tasks.back') }}
        </RouterLink>
        <div v-if="detail" class="task-detail-toolbar-actions">
          <UiButton v-if="canDeliver" as="RouterLink" class="command-button primary" variant="primary" :to="{ path: creationPath(detail.deliveryKind || detail.deliverableType), query: { taskId: detail.id } }">
            <template #start>
              <WandSparkles :size="17" />
            </template>{{ t('tasks.startCreating') }}
          </UiButton>
        </div>
      </DetailToolbar>
      <div v-if="loading" class="page-state" aria-live="polite">
        <LoaderCircle class="spin" :size="20" />{{ t('tasks.loading') }}
      </div>
      <div v-else-if="error && !detail" class="page-state" role="alert">
        <p>{{ error }}</p><UiButton class="command-button secondary" variant="secondary" @click="load">
          <template #start>
            <RefreshCw :size="17" />
          </template>{{ t('actions.retry') }}
        </UiButton>
      </div>
      <div v-else-if="detail" class="task-detail-layout">
        <header class="task-brief-header">
          <div class="task-brief-signals">
            <span class="task-status" :data-status="detail.status">{{ t(`tasks.status.${detail.status}`) }}</span><span v-if="taskPaymentEnabled && detail.allowDirectAccept && detail.status === 'open' && (directFundingConfirmed || detail.viewerRole === 'client')" class="task-direct-signal"><BriefcaseBusiness :size="14" />{{ t(!directFundingConfirmed ? 'tasks.directFundingRequired' : 'tasks.direct') }}</span>
          </div><h1>{{ detail.title }}</h1><p>{{ detail.summary }}</p>
          <dl class="task-key-facts">
            <div><dt>{{ t('content.budget') }}</dt><dd>{{ money(detail.budgetCents, detail.currency) }}</dd></div>
            <div><dt>{{ t('tasks.deadline') }}</dt><dd>{{ date(detail.deadline, detail.clientTimezone) }}<small>{{ detail.clientTimezone }}</small></dd></div>
            <div><dt>{{ t('tasks.categories') }}</dt><dd>{{ taskTypeLabel(detail.deliverableType) }}</dd></div>
          </dl>
        </header>
        <article class="task-brief">
          <section class="task-brief-overview">
            <div class="task-section-heading">
              <FileCheck2 :size="19" /><h2>{{ t('tasks.brief') }}</h2>
            </div><p>{{ detail.brief }}</p>
          </section>
          <div class="task-rule-grid">
            <section class="task-rule-card" data-tone="blue">
              <header><span><Play v-if="detail.deliverableType === 'video'" :size="13" fill="currentColor" /><component :is="taskTypeIcon(detail.deliverableType)" v-else :size="15" /></span><h2>{{ t('tasks.deliverables') }}</h2></header><ul>
                <li v-for="item in detail.deliverables" :key="item">
                  <Check :size="16" />{{ item }}
                </li>
              </ul>
            </section>
            <section class="task-rule-card" data-tone="green">
              <header><span><ShieldCheck :size="16" /></span><h2>{{ t('tasks.acceptance') }}</h2></header><ul>
                <li v-for="item in detail.acceptanceRules" :key="item">
                  <Check :size="16" />{{ item }}
                </li>
              </ul>
            </section>
          </div>
          <section class="task-trust-block">
            <div><ShieldCheck :size="18" /><h2>{{ t('tasks.rights') }}</h2></div><p>{{ detail.rightsTerms }}</p><p v-if="detail.deliveryKind">
              {{ t('tasks.deliveryKindLabel') }} · {{ t(`tasks.deliveryKinds.${detail.deliveryKind}`) }}
            </p>
          </section>
          <section class="task-trust-block">
            <div><WandSparkles :size="18" /><h2>{{ t('tasks.disclosure') }}</h2></div><p>{{ detail.aiDisclosureRequirement }}</p>
          </section>
          <a class="command-button primary task-mobile-action" href="#task-actions">
            {{ taskActionLabel }}<ChevronRight :size="17" />
          </a>

          <section v-if="canPropose || canDeliver || canReview || canDispute || canCancel" id="task-participation" class="task-participation">
            <h2>{{ taskActionLabel }}</h2>
            <UiButton v-if="canPropose && !proposalOpen" class="command-button primary wide" variant="primary" @click="showProposalForm">
              <template #start>
                <Plus :size="17" />
              </template>{{ t('tasks.submitProposal') }}
            </UiButton>
            <form v-if="proposalOpen" class="task-action-form" @submit.prevent="submitProposal">
              <label>{{ t('tasks.proposalApproach') }}<UiTextarea v-model="proposal.approach" rows="5" minlength="20" required /></label><label>{{ t('tasks.proposalDeliverables') }}<UiTextarea v-model="proposal.deliverables" rows="3" minlength="10" required /></label><div class="form-pair">
                <label>{{ t('tasks.proposedAmount') }}<UiInput v-model="proposal.amount" type="number" min="0.50" max="999999.99" step="0.01" required /></label><label>{{ t('tasks.timelineDays') }}<UiInput v-model="proposal.timelineDays" type="number" min="1" required /></label>
              </div><UiButton class="command-button primary wide" variant="primary" type="submit" :loading="actionLoading">
                {{ t('tasks.submitProposal') }}
              </UiButton>
            </form>

            <template v-if="canDeliver">
              <UiButton as="RouterLink" class="command-button primary wide" variant="primary" :to="{ path: creationPath(detail.deliveryKind || detail.deliverableType), query: { taskId: detail.id } }">
                <template #start>
                  <WandSparkles :size="17" />
                </template>{{ t('tasks.createForTask') }}
              </UiButton>
              <form class="task-action-form delivery-asset-search" @submit.prevent="loadDeliveryAssets()">
                <label>{{ t('tasks.searchDeliveryAssets') }}<UiInput v-model="assetSearch" type="search" maxlength="120" /></label>
                <UiButton type="submit" :loading="assetsLoading">
                  {{ t('actions.search') }}
                </UiButton>
              </form>
              <p v-if="assetsError" class="task-feedback error" role="alert">
                {{ assetsError }} <UiButton type="button" :disabled="assetsLoading" @click="loadDeliveryAssets(assetRetry.more, assetRetry.pinID)">
                  {{ t('actions.retry') }}
                </UiButton>
              </p>
              <UiButton v-if="assetCursor" class="delivery-assets-more" type="button" :loading="assetsLoading" @click="loadDeliveryAssets(true)">
                {{ t('tasks.moreDeliveryAssets') }}
              </UiButton>
              <p class="task-inline-help">
                {{ t('tasks.deliveryLimit') }}
              </p>
              <form class="task-action-form delivery-submit-form" @submit.prevent="submitDelivery">
                <label>{{ t('tasks.asset') }}<UiSelect v-model="delivery.assetId" required><option v-for="asset in assets" :key="asset.id" :value="asset.id">{{ asset.title }}</option></UiSelect></label><label>{{ t('tasks.deliveryNote') }}<UiTextarea v-model="delivery.note" rows="4" minlength="5" required /></label><fieldset v-if="assets.length > 1">
                  <legend>{{ t('tasks.extraDeliveryAssets') }}</legend>
                  <label v-for="asset in assets.filter(item => item.id !== delivery.assetId)" :key="asset.id" class="check-label"><UiCheckbox :model-value="delivery.extraAssetIds.includes(asset.id)" :disabled="!delivery.extraAssetIds.includes(asset.id) && new Set([delivery.assetId, ...delivery.extraAssetIds]).size >= 20" @update:model-value="checked => delivery.extraAssetIds = checked ? [...delivery.extraAssetIds, asset.id] : delivery.extraAssetIds.filter(id => id !== asset.id)" />{{ asset.title }}</label>
                </fieldset>
                <label>{{ t('tasks.rightsEvidence') }}<UiTextarea v-model="delivery.rightsEvidence" rows="3" minlength="10" required /></label>
                <label>{{ t('tasks.aiDisclosure') }}<UiTextarea v-model="delivery.aiDisclosure" rows="3" minlength="5" required /></label>
                <label class="check-label"><UiCheckbox v-model="delivery.rightsConfirmed" required />{{ t('tasks.rightsConfirmed') }}</label>
                <UiButton class="command-button secondary wide" variant="secondary" type="submit" :loading="actionLoading" :disabled="!delivery.assetId || assetsLoading || new Set([delivery.assetId, ...delivery.extraAssetIds]).size > 20">
                  <template #start>
                    <FileCheck2 v-if="!actionLoading" :size="17" />
                  </template>{{ t('tasks.submitDelivery') }}
                </UiButton>
              </form><p v-if="!assets.length && !assetsLoading && !assetsError" class="task-inline-help">
                {{ t('tasks.noDeliveryAssets') }}
              </p>
            </template>

            <form v-if="canReview" class="task-action-form" @submit.prevent>
              <label>{{ t('tasks.reviewNote') }}<UiTextarea v-model="reviewNote" rows="4" /></label><UiButton class="command-button primary wide" variant="primary" :loading="actionLoading" @click="review('accept')">
                <template #start>
                  <Check v-if="!actionLoading" :size="17" />
                </template>{{ t('tasks.acceptDelivery') }}
              </UiButton><UiButton class="command-button secondary wide" variant="secondary" :loading="actionLoading" @click="review('request_revision')">
                <template #start>
                  <RefreshCw v-if="!actionLoading" :size="17" />
                </template>{{ t('tasks.requestRevision') }}
              </UiButton>
            </form>

            <UiButton v-if="canDispute && !disputeOpen" class="text-link danger-link" variant="ghost" size="sm" :disabled="actionLoading" @click="disputeOpen = true">
              {{ t('tasks.openDispute') }}
            </UiButton>
            <form v-if="disputeOpen" class="task-action-form" @submit.prevent="openDispute">
              <label>{{ t('tasks.disputeReason') }}<UiTextarea v-model="disputeReason" rows="4" minlength="20" required /></label><UiButton class="command-button secondary wide" variant="secondary" type="submit" :loading="actionLoading">
                {{ t('tasks.openDispute') }}
              </UiButton>
            </form>
            <UiButton v-if="canCancel && !cancelOpen" class="text-link danger-link" variant="ghost" size="sm" :disabled="actionLoading" @click="cancelOpen = true">
              {{ t('tasks.cancelTask') }}
            </UiButton>
            <form v-if="cancelOpen" class="task-action-form" @submit.prevent="cancelTask">
              <p class="task-form-warning">
                <AlertTriangle :size="16" />{{ t('tasks.cancelWarning') }}
              </p><label>{{ t('tasks.cancelReason') }}<UiTextarea v-model="cancelReason" rows="3" minlength="10" required /></label><div class="task-confirm-actions">
                <UiButton class="command-button secondary" variant="secondary" type="button" @click="cancelOpen = false">
                  {{ t('tasks.keepTask') }}
                </UiButton><UiButton class="command-button destructive" variant="destructive" type="submit" :loading="actionLoading">
                  {{ t('tasks.confirmCancel') }}
                </UiButton>
              </div>
            </form>
          </section>

          <section v-if="detail.proposals.length" class="task-proposals">
            <h2>{{ detail.viewerRole === 'client' ? t('tasks.proposals') : t('tasks.yourProposal') }}</h2><article v-for="item in detail.proposals" :key="item.id">
              <div><strong>{{ item.creator.displayName }}</strong><span>@{{ item.creator.handle }}</span></div><p>{{ item.approach }}</p><p>{{ item.deliverables }}</p><dl><div><dt>{{ t('tasks.proposedAmount') }}</dt><dd>{{ money(item.amountCents) }}</dd></div><div><dt>{{ t('tasks.timelineDays') }}</dt><dd>{{ item.timelineDays }}</dd></div></dl><div v-if="item.status === 'submitted' && detail.status === 'open' && detail.viewerRole === 'client'" class="task-proposal-actions">
                <UiButton v-if="canFundProposal(item.id)" class="command-button secondary" variant="secondary" :loading="actionLoading" :disabled="!taskPaymentEnabled" :aria-describedby="!taskPaymentEnabled ? 'proposal-payment-' + item.id : undefined" @click="fundTask(item.id)">
                  <template #start>
                    <CircleDollarSign v-if="!actionLoading" :size="17" />
                  </template>{{ fundingActionLabel(item.id) }}
                </UiButton><UiButton v-if="canAcceptProposal(item.id)" class="command-button primary" variant="primary" :loading="actionLoading" :disabled="!taskPaymentEnabled" :aria-describedby="!taskPaymentEnabled ? 'proposal-payment-' + item.id : undefined" @click="acceptProposal(item.id)">
                  {{ t('tasks.acceptProposal') }}
                </UiButton>
                <p v-if="!taskPaymentEnabled && (canFundProposal(item.id) || canAcceptProposal(item.id))" :id="'proposal-payment-' + item.id" class="task-payment-note">
                  {{ t('tasks.paymentUnavailable') }}
                </p>
              </div><span v-else class="task-status" :data-status="item.status">{{ t(`tasks.proposalStatus.${item.status}`) }}</span>
            </article>
          </section>

          <section v-if="['client', 'assignee'].includes(detail.viewerRole) && ['assigned', 'revision'].includes(detail.status)" class="task-participation">
            <p class="task-inline-help">
              {{ t('tasks.deadlinePolicy') }}
            </p>
            <template v-if="detail.deadlineChange">
              <strong>{{ t('tasks.deadlinePending') }} · {{ date(detail.deadlineChange.deadline, detail.clientTimezone) }}</strong>
              <p>{{ detail.deadlineChange.reason }}</p>
              <UiButton v-if="detail.deadlineChange.proposedBy !== session.user?.id" variant="primary" :loading="actionLoading" @click="changeDeadline('accept')">
                {{ t('tasks.confirmDeadline') }}
              </UiButton>
              <UiButton variant="secondary" :loading="actionLoading" @click="changeDeadline('reject')">
                {{ t('tasks.rejectDeadline') }}
              </UiButton>
            </template>
            <template v-else>
              <UiButton variant="secondary" :disabled="actionLoading" @click="deadlineFormOpen = !deadlineFormOpen">
                {{ t('tasks.requestExtension') }}
              </UiButton>
              <form v-if="deadlineFormOpen" class="task-action-form" @submit.prevent="changeDeadline('propose')">
                <label>{{ t('tasks.newDeadline') }} · {{ detail.clientTimezone }}<UiInput v-model="deadlineRequest.deadline" type="datetime-local" required /></label>
                <label>{{ t('tasks.extensionReason') }}<UiTextarea v-model="deadlineRequest.reason" minlength="10" maxlength="2000" required /></label>
                <UiButton variant="primary" type="submit" :loading="actionLoading">
                  {{ t('tasks.requestExtension') }}
                </UiButton>
              </form>
            </template>
          </section>

          <section v-if="detail.deliveries.length" class="task-deliveries">
            <h2>{{ t('tasks.submitDelivery') }}</h2><article v-for="item in detail.deliveries" :key="item.id">
              <AssetMedia :src="item.mediaUrl" :kind="item.mediaKind || 'document'" :alt="item.assetTitle" :width="320" :height="240" /><div>
                <span>{{ item.assetTitle }} / v{{ item.version }}</span>
                <ul v-if="item.assets?.length">
                  <li v-for="asset in item.assets" :key="asset.id">
                    <a :href="asset.mediaUrl" target="_blank" rel="noopener">{{ asset.title }}</a>
                  </li>
                </ul>
                <p v-if="item.rightsEvidence">
                  {{ t('tasks.rightsEvidence') }} · {{ item.rightsEvidence }}
                </p>
                <p v-if="item.aiDisclosure">
                  {{ t('tasks.aiDisclosure') }} · {{ item.aiDisclosure }}
                </p>
                <strong>{{ item.note }}</strong><p v-if="item.reviewNote">
                  {{ item.reviewNote }}
                </p><small><template v-if="item.creator">{{ item.creator.displayName }} · </template>{{ date(item.createdAt) }}</small>
              </div><span class="task-status" :data-status="item.status">{{ t(`tasks.deliveryStatus.${item.status}`) }}</span>
            </article>
          </section>

          <section v-if="detail.events.length" class="task-history">
            <div class="task-section-heading">
              <Clock3 :size="19" /><h2>{{ t('tasks.history') }}</h2>
            </div><ol>
              <li v-for="event in detail.events" :key="event.id">
                <span aria-hidden="true"></span><div>
                  <strong>{{ eventLabel(event.kind) }}</strong><p v-if="event.note">
                    {{ event.note }}
                  </p><small>{{ date(event.createdAt) }}<template v-if="event.actor"> / {{ event.actor.displayName }}</template></small>
                </div>
              </li>
            </ol>
          </section>
        </article>

        <aside id="task-actions" class="task-action-rail">
          <p v-if="session.user && ['viewer', 'assignee'].includes(detail.viewerRole) && taskPaymentEnabled && !payoutReady" class="task-inline-help">
            {{ t('tasks.payoutSetupHint') }} <RouterLink to="/settings?section=payouts">
              {{ t('tasks.setupPayout') }}
            </RouterLink>
          </p>
          <UiButton v-if="detail.funding" variant="secondary" size="sm" :loading="fundingRefreshLoading" @click="refreshFunding">
            {{ t('tasks.refreshFunding') }}
          </UiButton>
          <h2 v-if="!(canPropose || canDeliver || canReview || canDispute || canCancel)" class="task-rail-heading">
            {{ taskActionLabel }}
          </h2>
          <a v-if="canPropose || canDeliver || canReview || canDispute || canCancel" class="command-button secondary task-summary-action" href="#task-participation">{{ taskActionLabel }}<ChevronRight :size="16" /></a>
          <div v-if="taskPaymentEnabled" class="task-reward">
            <p><ShieldCheck :size="16" /><span>{{ taskPaymentEnabled ? t(paymentLiveMode ? 'tasks.providerLive' : 'tasks.providerTest') : t('tasks.paymentUnavailable') }}</span></p>
          </div>
          <dl class="task-facts">
            <div><dt><UserRound :size="16" />{{ t('tasks.commissioner') }}</dt><dd>{{ detail.client.displayName }}<small>@{{ detail.client.handle }}</small></dd></div><div v-if="detail.assignee">
              <dt><BriefcaseBusiness :size="16" />{{ t('tasks.selectedCreator') }}</dt><dd>{{ detail.assignee.displayName }}<small>@{{ detail.assignee.handle }}</small></dd>
            </div>
          </dl>

          <div v-if="!session.user" class="market-auth-prompt">
            <div><h2>{{ t('tasks.guestTitle') }}</h2><p>{{ t('tasks.guestSummary') }}</p></div>
            <UiButton as="RouterLink" class="command-button primary wide" variant="primary" :to="{ path: '/auth', query: { auth: 'login', returnTo: route.fullPath } }">
              <template #start>
                <LogIn :size="17" />
              </template>{{ t('account.signIn') }}
            </UiButton>
            <UiButton as="RouterLink" class="text-link" variant="ghost" size="sm" :to="{ path: '/auth', query: { auth: 'register', returnTo: route.fullPath } }">
              {{ t('account.createAccount') }}
            </UiButton>
          </div>

          <div v-if="success" class="task-feedback success" role="status">
            <Check :size="17" />{{ success }}
          </div>
          <div v-if="error" class="task-feedback error" role="alert">
            <AlertTriangle :size="17" />{{ error }}
          </div>

          <div v-if="detail.funding" class="task-settlement task-funding">
            <CircleDollarSign :size="19" /><div><strong>{{ t(`tasks.fundingStatus.${detail.funding.status}`) }}</strong><span>{{ money(detail.funding.amountCents, detail.funding.currency) }} / {{ t(detail.funding.liveMode ? 'tasks.providerLiveMode' : 'tasks.providerTestMode') }}</span></div>
          </div>

          <UiButton v-if="canFundDirect" class="command-button primary wide" variant="primary" :loading="actionLoading" :disabled="!taskPaymentEnabled" :aria-describedby="!taskPaymentEnabled ? 'task-payment-disabled' : undefined" @click="fundTask()">
            <template #start>
              <CircleDollarSign v-if="!actionLoading" :size="17" />
            </template>{{ fundingActionLabel() }}
          </UiButton>

          <UiButton v-if="canClaim" class="command-button primary wide" variant="primary" :loading="actionLoading" :disabled="!taskPaymentEnabled" :aria-describedby="!taskPaymentEnabled ? 'task-payment-disabled' : undefined" @click="claimTask">
            <template #start>
              <BriefcaseBusiness v-if="!actionLoading" :size="17" />
            </template>{{ t('tasks.acceptTask') }}
          </UiButton>
          <p v-if="!taskPaymentEnabled && (canFundDirect || canClaim)" id="task-payment-disabled" class="task-payment-note">
            {{ t('tasks.paymentUnavailable') }}
          </p>
          <div v-if="detail.settlement" class="task-settlement">
            <CircleDollarSign :size="19" /><div><strong>{{ t('tasks.settlement') }}</strong><span>{{ money(detail.settlement.amountCents, detail.settlement.currency) }} / {{ t(`tasks.settlementMode.${detail.settlement.mode}`) }}</span></div>
          </div><p v-else-if="taskPaymentEnabled" class="task-payment-note">
            <ShieldCheck :size="17" /><span>{{ t(taskPaymentEnabled ? 'tasks.providerUnsettled' : 'tasks.paymentUnavailable') }}</span>
          </p>
        </aside>
      </div>
    </template>

    <div v-if="createOpen" class="modal-backdrop" @mousedown.self="createOpen = false">
      <section ref="taskCreateModal" class="task-create-modal" role="dialog" aria-modal="true" :aria-labelledby="'task-create-title'">
        <header>
          <div>
            <h2 id="task-create-title">
              {{ t('tasks.createTitle') }}
            </h2><p>{{ t('tasks.createSummary') }}</p>
          </div><UiIconButton class="icon-button" :label="t('tasks.cancel')" @click="createOpen = false">
            <X :size="19" />
          </UiIconButton>
        </header><form @submit.prevent="publishTask">
          <fieldset class="task-form-section">
            <legend>{{ t('tasks.scopeSection') }}</legend><label>{{ t('tasks.titleLabel') }}<UiInput ref="taskTitleInput" v-model="draft.title" required minlength="5" /></label><label>{{ t('tasks.summaryLabel') }}<UiTextarea v-model="draft.summary" rows="2" required minlength="10" /></label><label>{{ t('tasks.brief') }}<UiTextarea v-model="draft.brief" rows="5" required minlength="30" /></label>
          </fieldset>
          <fieldset class="task-form-section">
            <legend>{{ t('tasks.scheduleSection') }}</legend><div class="form-pair">
              <label>{{ t('tasks.typeLabel') }}<UiSelect v-model="draft.deliverableType"><option v-for="item in types" :key="item" :value="item">{{ taskTypeLabel(item) }}</option></UiSelect><small v-if="taskTypes.find(item => item.code === draft.deliverableType)?.deliveryKind">{{ t('tasks.deliveryKindLabel') }} · {{ t(`tasks.deliveryKinds.${taskTypes.find(item => item.code === draft.deliverableType)?.deliveryKind}`) }}</small></label><label>{{ t('tasks.budgetLabel') }}<UiInput v-model="draft.budget" type="number" min="0.50" max="999999.99" step="0.01" required /></label>
            </div><div class="form-pair">
              <label>{{ t('tasks.deadlineLabel') }}<UiInput v-model="draft.deadline" type="datetime-local" :min="minimumDeadline" required /></label><label>{{ t('tasks.timezoneLabel') }}<UiSelect v-model="draft.timezone" :aria-label="t('tasks.timezoneLabel')" required><option v-for="item in timezoneOptions" :key="item" :value="item">{{ item }}</option></UiSelect>
              </label>
            </div><label class="check-label"><UiCheckbox v-model="draft.allowDirectAccept" />{{ t('tasks.directLabel') }}</label>
          </fieldset>
          <fieldset class="task-form-section">
            <legend>{{ t('tasks.deliverySection') }}</legend><label>{{ t('tasks.deliverablesLabel') }}<UiTextarea v-model="draft.deliverables" rows="3" required /></label><label>{{ t('tasks.acceptanceLabel') }}<UiTextarea v-model="draft.acceptanceRules" rows="3" required /></label><label>{{ t('tasks.rightsLabel') }}<UiTextarea v-model="draft.rightsTerms" rows="3" required /></label><label>{{ t('tasks.disclosureLabel') }}<UiTextarea v-model="draft.aiDisclosureRequirement" rows="3" required /></label><label class="check-label"><UiCheckbox v-model="draft.allowDerivativeReuse" />{{ t('tasks.allowDerivativeReuse') }}</label>
          </fieldset><div class="modal-actions">
            <UiButton class="command-button secondary" variant="secondary" type="button" @click="createOpen = false">
              {{ t('tasks.cancel') }}
            </UiButton><UiButton class="command-button primary" variant="primary" type="submit" :loading="actionLoading">
              {{ t('tasks.publish') }}
            </UiButton>
          </div>
        </form>
      </section>
    </div>
  </section>
</template>

<style scoped>
.task-result-list.ui-catalog { gap: 12px; }
.task-result-list.ui-catalog .task-row { display: grid; grid-template-columns: 176px minmax(0, 1fr) 180px; grid-template-areas: 'media title budget' 'media summary budget' 'media tags footer' 'media meta footer'; gap: 6px 20px; padding: 16px; align-items: start; text-decoration: none; }
.task-row .task-row-media { grid-area: media; height: auto; min-height: 0; aspect-ratio: 16 / 10; align-self: center; }
.task-card-title { grid-area: title; margin: 0; color: var(--text); font-size: 16px; font-weight: 600; line-height: 1.5; overflow-wrap: anywhere; }
.task-card-summary { grid-area: summary; margin: 0; color: var(--text-secondary); font-size: 13px; line-height: 1.65; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; overflow-wrap: anywhere; }
.task-card-tags { grid-area: tags; display: flex; flex-wrap: wrap; align-items: center; gap: 8px; font-size: 12px; color: var(--text-secondary); }
.task-card-type { display: inline-flex; align-items: center; gap: 5px; }
.task-card-meta { grid-area: meta; display: flex; flex-wrap: wrap; gap: 6px 12px; font-size: 12px; color: var(--text-secondary); overflow-wrap: anywhere; }
.task-card-budget { grid-area: budget; display: flex; flex-direction: column; align-items: flex-end; gap: 4px; }
.task-card-budget > span { color: var(--text-secondary); font-size: 12px; }
.task-card-budget strong { font-size: 18px; font-weight: 600; font-variant-numeric: tabular-nums; line-height: 1.5; color: var(--text); }
.task-card-footer { grid-area: footer; align-self: end; display: flex; flex-direction: column; align-items: flex-end; gap: 10px; }
.task-card-deadline { display: flex; flex-wrap: wrap; align-items: center; gap: 4px; font-size: 12px; color: var(--text-secondary); }
.task-card-open { display: inline-flex; align-items: center; gap: 4px; font-size: 12px; color: var(--text-secondary); }
.task-row:hover .task-card-open { color: var(--accent-readable); }
.task-result-list.ui-catalog.is-grid { grid-template-columns: repeat(auto-fit, minmax(min(100%, 300px), 1fr)); }
.task-result-list.ui-catalog.is-grid .task-row { grid-template-columns: minmax(0, 1fr); grid-template-rows: auto auto auto 1fr auto auto auto; grid-template-areas: 'media' 'title' 'budget' 'summary' 'tags' 'meta' 'footer'; gap: 10px; }
.task-result-list.ui-catalog.is-grid .task-row-media { height: auto; min-height: 0; aspect-ratio: 16 / 10; }
.task-result-list.is-grid .task-card-title { min-height: 48px; }
.task-result-list.is-grid .task-card-budget { flex-direction: row; align-items: baseline; gap: 8px; }
.task-result-list.is-grid .task-card-budget strong { order: -1; }
.task-result-list.is-grid .task-card-footer { flex-direction: row; align-items: center; justify-content: space-between; gap: 8px; padding-top: 10px; border-top: 1px solid var(--border); }
@media (min-width: 768px) and (max-width: 1100px) { .task-result-list.ui-catalog .task-row { grid-template-columns: 132px minmax(0, 1fr) 160px; gap: 6px 14px; } }
@media (max-width: 767px) {
  .task-result-list.ui-catalog .task-row, .task-result-list.ui-catalog.is-grid .task-row { grid-template-columns: minmax(0, 1fr); grid-template-rows: auto; grid-template-areas: 'media' 'title' 'budget' 'summary' 'tags' 'meta' 'footer'; padding: 14px; gap: 10px; }
  .task-result-list.ui-catalog .task-row-media { height: auto; min-height: 0; aspect-ratio: 16 / 10; }
  .task-card-budget { flex-direction: row; align-items: baseline; gap: 8px; }
  .task-card-budget strong { order: -1; }
  .task-card-footer { flex-direction: row; align-items: center; justify-content: space-between; gap: 8px; padding-top: 10px; border-top: 1px solid var(--border); }
}
.task-toolbar .task-toolbar__type, .task-toolbar .task-toolbar__status { flex: 0 0 auto; max-width: 190px; }
.task-toolbar .task-toolbar__sort { width: auto; max-width: 200px; }
.task-skeleton { padding-top: 0; }
@media (min-width: 1321px) {
  .task-toolbar .task-toolbar__type { display: none; }
}
@media (max-width: 767px) {
  .task-toolbar.ui-filter-bar { display: grid; }
  .task-toolbar .task-filters { margin-left: 0; }
  .task-keyword { justify-self: start; }
  .task-toolbar .task-filters { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
  .task-toolbar .task-toolbar__type, .task-toolbar .task-toolbar__status { width: 100%; max-width: none; }
  .task-toolbar__type :deep(.ui-select__trigger), .task-toolbar__status :deep(.ui-select__trigger) { width: 100%; }
  .task-toolbar__reset { justify-self: start; }
}

.task-market.is-detail { --content-max: 1400px; padding-top: 0; }
.task-detail-layout { display: grid; grid-template-columns: minmax(0, 1fr) 288px; gap: 24px; }
.task-brief-header { grid-column: 1 / -1; padding: 24px; border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface); }
.task-brief-header h1 { max-width: 960px; margin: 12px 0; }
.task-brief-header > p { max-width: 900px; }
.task-brief { padding: 0 24px; border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface); }
.task-brief > section { padding-block: 24px; }
.task-brief > section:last-child { border-bottom: 0; }
.task-summary-action { background: var(--surface-muted); color: var(--text); border-color: var(--border); }
.task-rail-heading { margin: 0; font-size: 16px; }
.task-brief-header h1 { font-size: clamp(25px, 2.6vw, 34px); line-height: 1.25; }
.task-brief-header > p { font-size: 14px; line-height: 1.7; }
.task-brief-overview, .task-trust-block { padding-block: 20px; }
.task-key-facts { display: flex; flex-wrap: wrap; gap: 12px; margin: 20px 0 0; padding: 0; border: 0; }
.task-key-facts > div { flex: 1 1 180px; padding: 12px 16px; border-radius: var(--radius-control); background: var(--surface-muted); }
.task-key-facts small { display: block; margin-top: 4px; font-size: 11px; font-weight: 400; color: var(--text-secondary); }
.task-key-facts dt { font-size: 12px; color: var(--text-secondary); }
.task-key-facts dd { margin: 6px 0 0; font-size: 14px; font-weight: 600; }
.task-participation { padding: 20px 0; border-bottom: 1px solid var(--border); display: grid; gap: 14px; scroll-margin-top: 64px; }
.task-participation h2 { margin: 0; font-size: 18px; }
.task-participation .task-action-form { padding: 16px; border: 1px solid var(--border); border-radius: var(--radius-control); }
.task-action-rail { scroll-margin-top: 64px; }
.task-reward > div { min-width: 0; width: 100%; }
.task-action-rail { min-height: 0; max-height: none; overflow: visible; position: static; padding: 18px; gap: 14px; box-shadow: none; background: var(--surface); }
.task-action-rail .task-reward { grid-template-columns: minmax(0, 1fr); grid-template-areas: "copy" "note"; }
.task-action-rail .task-reward strong { font-size: 26px; }
.task-rule-grid { gap: 12px; }
.task-rule-card { min-height: 0; border-color: var(--border); background: var(--surface); }
.task-rule-card header, .task-rule-card[data-tone="green"] header { background: var(--surface-muted); }
.task-rule-grid { padding-block: 24px; }
.task-reward { padding-bottom: 16px; }
.task-reward > p { margin: 0; }
.task-deliveries .asset-renderer { width: 100%; height: 140px; overflow: hidden; }
.task-deliveries :deep(img), .task-deliveries :deep(video) { width: 100%; height: 100%; object-fit: contain; }
.task-mobile-action { display: none; }
@media(max-width: 1000px) { .task-detail-layout { grid-template-columns: minmax(0, 1fr); } .task-action-rail { grid-row: 2; width: 100%; box-sizing: border-box; } }
@media(max-width: 600px) { .task-brief-header { padding: 18px; } .task-brief { padding-inline: 18px; } .task-detail-layout { display: grid; gap: 16px; } .task-rule-grid { grid-template-columns: 1fr; } .task-key-facts { gap: 14px; } .task-key-facts > div { flex: 1 1 130px; } }
</style>
