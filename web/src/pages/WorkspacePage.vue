<script setup lang="ts">
import {
  ArrowLeft, ArrowRight, Boxes, ClipboardList, Clock3, FileCheck2, FolderOpen, Maximize2, PackageCheck, Plus,
  Ban, Bookmark, BookmarkX, Coins, Download, GitBranch, ListFilter, ReceiptText, RefreshCw, RotateCcw, ShieldCheck, ShoppingBag, Store, TrendingUp, Upload, UsersRound, WalletCards, WandSparkles, X,
} from 'lucide-vue-next'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api, messageFrom, type Asset, type BillingStatement, type Generation, type Meta, type Order, type PointOverview, type SavedWork, type TaskSummary } from '../api/client'
import { contentListReturn, creationPath, licenseLabel } from '../lib/contentPresentation'
import { formatCurrency, formatDateTime } from '../lib/format'
import { openCheckoutWindow } from '../lib/checkout'
import { useSessionStore } from '../stores/session'
import AssetPublishDrawer from '../components/domain/AssetPublishDrawer.vue'
import AssetMedia from '../components/domain/AssetMedia.vue'
import AuthRequiredState from '../components/domain/AuthRequiredState.vue'
import MotionFavoriteIcon from '../components/ui/MotionFavoriteIcon.vue'
import PageHero from '../components/ui/PageHero.vue'
import UiButton from '../components/ui/UiButton.vue'
import UiBadge from '../components/ui/UiBadge.vue'
import UiCard from '../components/ui/UiCard.vue'
import UiCheckbox from '../components/ui/UiCheckbox.vue'
import UiIconButton from '../components/ui/UiIconButton.vue'
import UiFileInput from '../components/ui/UiFileInput.vue'
import UiInput from '../components/ui/UiInput.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiTabs from '../components/ui/UiTabs.vue'
import UiTextarea from '../components/ui/UiTextarea.vue'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const assets = ref<Asset[]>([])
const savedWorks = ref<SavedWork[]>([])
const generations = ref<Generation[]>([])
const tasks = ref<TaskSummary[]>([])
const orders = ref<Order[]>([])
const orderNextCursor = ref<string | null>(null)
const ordersLoadingMore = ref(false)
const billing = ref<BillingStatement | null>(null)
const points = ref<PointOverview | null>(null)
const subscriptionAction = ref('')
const selectedAsset = ref<Asset | null>(null)
const loading = ref(true)
const refunding = ref('')
const generationAction = ref('')
const error = ref('')
const success = ref('')
const refundReasons = ref<Record<string, string>>({})
const uploadOpen = ref(false)
const uploading = ref(false)
const uploadTitle = ref('')
const uploadFile = ref<globalThis.File | null>(null)
const versionOpen = ref(false)
const versionUploading = ref(false)
const versionTitle = ref('')
const versionNote = ref('')
const versionFile = ref<globalThis.File | null>(null)
const assetNextCursor = ref<string | null>(null)
const assetLoadingMore = ref(false)
const savedWorkNextCursor = ref<string | null>(null)
const savedWorkLoadingMore = ref(false)
const assetUsageLoadingMore = ref(false)
const assetMediaElement = ref<globalThis.HTMLElement | null>(null)
const generationNextCursor = ref<string | null>(null)
const generationLoadingMore = ref(false)
const generationMode = ref('')
const generationStatus = ref('')
const generationDateFrom = ref('')
const generationDateTo = ref('')
const selectedGenerationIDs = ref<string[]>([])
const billingNextCursor = ref<string | null>(null)
const billingLoadingMore = ref(false)
const billingDirection = ref('')
const billingEntryType = ref('')
const billingDateFrom = ref('')
const billingDateTo = ref('')
const billingEntryTypes = ['subscription_purchase', 'wallet_topup', 'product_purchase', 'product_sale', 'product_refund', 'task_payment', 'task_earning', 'admin_adjustment', 'initial_credit'] as const
const paymentProvider = ref<Meta['paymentProvider'] | null>(null)
const topupAmount = ref(20)
const topupAction = ref(false)
let paymentConfirmationTimer: ReturnType<typeof globalThis.setTimeout> | undefined

const assetID = computed(() => String(route.params.assetId || ''))
const needsAuthentication = computed(() => session.initialized && !session.user && !session.error)
const section = computed(() => ['generations', 'purchases', 'orders', 'tasks', 'billing'].includes(String(route.params.section)) ? String(route.params.section) : 'assets')
const publishAssetID = computed(() => String(route.query.publish || ''))
const publishRequested = computed(() => section.value === 'assets' && Boolean(publishAssetID.value))
const publishOpen = computed(() => publishRequested.value && Boolean(session.user))
const assetView = computed(() => section.value === 'assets' && route.query.view === 'saved' ? 'saved' : 'owned')
const generationFocus = computed(() => String(route.query.generationId || ''))
const visibleAssets = computed(() => section.value === 'purchases' ? assets.value.filter((item) => item.sourceType === 'purchase') : assets.value)
const cleanAssetCount = computed(() => assets.value.filter(item => item.scanStatus === 'clean').length)
const purchasedCleanCount = computed(() => visibleAssets.value.filter(item => item.scanStatus === 'clean').length)
const purchasedKindCount = computed(() => new Set(visibleAssets.value.map(item => item.kind)).size)
const fulfilledOrderCount = computed(() => orders.value.filter(item => item.status === 'fulfilled').length)
const refundedOrderCount = computed(() => orders.value.filter(item => ['test_refunded', 'refunded'].includes(item.status)).length)
const openTaskCount = computed(() => tasks.value.filter(item => item.status === 'open').length)
const taskProposalCount = computed(() => tasks.value.reduce((total, item) => total + item.proposalCount, 0))
const taskRewardTotal = computed(() => tasks.value.reduce((total, item) => total + item.budgetCents, 0))
const assetViewTabs = computed(() => [
  { value: 'owned', label: t('workspace.ownedAssets'), icon: Boxes },
  { value: 'saved', label: t('workspace.savedWorks'), icon: Bookmark },
])
const selectedGenerations = computed(() => generations.value.filter(item => selectedGenerationIDs.value.includes(item.id)))
const allGenerationsSelected = computed(() => generations.value.length > 0 && selectedGenerations.value.length === generations.value.length)
const billingPaymentModeLabel = computed(() => {
  if (!paymentProvider.value?.enabled) return t('workspace.paymentUnavailable')
  if (paymentProvider.value.provider === 'waffo_pancake') return t(paymentProvider.value.liveMode ? 'workspace.waffoLiveMode' : 'workspace.waffoTestMode')
  if (paymentProvider.value.provider === 'epay') return t('workspace.epayMode')
  return t(paymentProvider.value.liveMode ? 'workspace.stripeLiveMode' : 'workspace.stripeTestMode')
})
const billingPaymentSummary = computed(() => {
  if (!paymentProvider.value?.enabled) return t('workspace.externalPaymentUnavailable')
  return t(paymentProvider.value.liveMode ? 'workspace.externalPaymentLiveSummary' : 'workspace.externalPaymentTestSummary')
})
const pointUsage = computed(() => points.value?.account.lifetimeSpentPoints || 0)
const pointBalanceTrend = computed(() => {
  if (!points.value) return []
  const entries = [...points.value.entries].sort((left, right) => Date.parse(right.createdAt) - Date.parse(left.createdAt))
  const today = new Date()
  let balance = points.value.account.balancePoints
  let entryIndex = 0
  const snapshots: Array<{ label: string; value: number }> = []
  for (let offset = 0; offset < 7; offset += 1) {
    const dayStart = new Date(today)
    dayStart.setHours(0, 0, 0, 0)
    dayStart.setDate(dayStart.getDate() - offset)
    const nextDay = new Date(dayStart)
    nextDay.setDate(nextDay.getDate() + 1)
    snapshots.push({
      label: new Intl.DateTimeFormat(locale.value, { month: 'numeric', day: 'numeric' }).format(dayStart),
      value: Math.max(0, balance),
    })
    while (entryIndex < entries.length) {
      const entry = entries[entryIndex]
      if (!entry) break
      const entryTime = Date.parse(entry.createdAt)
      if (entryTime < dayStart.getTime()) break
      if (entryTime < nextDay.getTime()) balance += entry.direction === 'debit' ? entry.amountPoints : -entry.amountPoints
      entryIndex += 1
    }
  }
  return snapshots.reverse()
})
const pointTrendGeometry = computed(() => {
  const values = pointBalanceTrend.value.map(item => item.value)
  if (!values.length) return { line: '', area: '', dots: [] as Array<{ x: number; y: number }>, grid: [] as Array<{ y: number; value: number }> }
  const rawMin = Math.min(...values)
  const rawMax = Math.max(...values)
  const range = Math.max(rawMax - rawMin, Math.max(rawMax * 0.08, 100))
  const min = Math.max(0, rawMin - range * 0.18)
  const max = Math.max(min + 1, rawMax + range * 0.18)
  const left = 54
  const right = 676
  const top = 20
  const bottom = 166
  const coordinates = values.map((value, index) => {
    const x = values.length === 1 ? (left + right) / 2 : left + (index / (values.length - 1)) * (right - left)
    const y = bottom - ((value - min) / (max - min)) * (bottom - top)
    return { x, y }
  })
  const line = coordinates.map(point => `${point.x.toFixed(2)},${point.y.toFixed(2)}`).join(' ')
  const area = coordinates.length ? `M ${left} ${bottom} L ${line.replaceAll(',', ' ')} L ${right} ${bottom} Z` : ''
  return {
    line,
    area,
    dots: coordinates,
    grid: [max, (max + min) / 2, min].map((value, index) => ({ y: top + index * ((bottom - top) / 2), value: Math.round(value) })),
  }
})
const sectionMeta = computed(() => ({
  assets: { title: t('workspace.assets'), summary: assetView.value === 'saved' ? t('workspace.savedSummary') : t('workspace.assetsSummary'), count: assetView.value === 'saved' ? savedWorks.value.length : visibleAssets.value.length, icon: Boxes, actionIcon: Upload, actionLabel: t('workspace.uploadAsset'), actionTo: '' },
  generations: { title: t('workspace.generations'), summary: t('workspace.generationsSummary'), count: generations.value.length, icon: Clock3, actionIcon: Plus, actionLabel: t('actions.newCreation'), actionTo: '/create/image' },
  purchases: { title: t('workspace.purchases'), summary: t('workspace.purchasesSummary'), count: visibleAssets.value.length, icon: ShoppingBag, actionIcon: Store, actionLabel: t('workspace.browseMarket'), actionTo: '/market' },
  orders: { title: t('workspace.orders'), summary: t('workspace.ordersSummary'), count: orders.value.length, icon: ReceiptText, actionIcon: Store, actionLabel: t('workspace.browseMarket'), actionTo: '/market' },
  tasks: { title: t('workspace.tasks'), summary: t('workspace.tasksSummary'), count: tasks.value.length, icon: ClipboardList, actionIcon: ArrowRight, actionLabel: t('workspace.browseTasks'), actionTo: '/market/demands' },
  billing: { title: t('workspace.billing'), summary: t('workspace.billingSummary'), count: billing.value?.entries.length || 0, icon: WalletCards, actionIcon: Plus, actionLabel: t('actions.newCreation'), actionTo: '/create/image' },
})[section.value]!)
const workspaceHero = computed(() => {
  const eyebrow = `${t('workspace.workbenchLabel')} · ${t('workspace.itemCount', { count: sectionMeta.value.count })}`
  if (section.value === 'assets') {
    return {
      eyebrow,
      eyebrowIcon: Boxes,
      statsLabel: t('workspace.assetViewsLabel'),
      stats: [
        { value: visibleAssets.value.length, label: t('workspace.ownedAssets'), icon: Boxes, tone: 'blue' as const },
        { value: savedWorks.value.length, label: t('workspace.savedWorks'), icon: Bookmark, tone: 'violet' as const },
        { value: cleanAssetCount.value, label: t('workspace.scanStatus.clean'), icon: ShieldCheck, tone: 'green' as const },
      ],
      artworkSrc: '/tasks/task-hero-transparent.webp',
    }
  }
  if (section.value === 'purchases') {
    return {
      eyebrow,
      eyebrowIcon: ShoppingBag,
      statsLabel: t('workspace.purchases'),
      stats: [
        { value: visibleAssets.value.length, label: t('workspace.purchases'), icon: ShoppingBag, tone: 'blue' as const },
        { value: purchasedKindCount.value, label: t('workspace.assets'), icon: Boxes, tone: 'violet' as const },
        { value: purchasedCleanCount.value, label: t('workspace.scanStatus.clean'), icon: ShieldCheck, tone: 'green' as const },
      ],
      artworkSrc: '/assets/purchase-hero-light.webp',
    }
  }
  if (section.value === 'orders') {
    return {
      eyebrow,
      eyebrowIcon: ReceiptText,
      statsLabel: t('workspace.orders'),
      stats: [
        { value: orders.value.length, label: t('workspace.orderRecords'), icon: ReceiptText, tone: 'blue' as const },
        { value: refundedOrderCount.value, label: t('workspace.refundedOrders'), icon: RotateCcw, tone: 'violet' as const },
        { value: fulfilledOrderCount.value, label: t('workspace.activeEntitlements'), icon: ShieldCheck, tone: 'green' as const },
      ],
      artworkSrc: '/assets/order-hero-light.webp',
    }
  }
  if (section.value === 'tasks') {
    return {
      eyebrow,
      eyebrowIcon: ClipboardList,
      statsLabel: t('workspace.tasks'),
      stats: [
        { value: openTaskCount.value, label: t('workspace.openBriefs'), icon: ClipboardList, tone: 'blue' as const },
        { value: formatCurrency(taskRewardTotal.value, 'USD', locale.value), label: t('workspace.taskRewardTotal'), icon: Coins, tone: 'violet' as const },
        { value: taskProposalCount.value, label: t('tasks.proposals'), icon: UsersRound, tone: 'green' as const },
      ],
      artworkSrc: '/tasks/task-hero-transparent.webp',
    }
  }
  return null
})
const versionAccept = computed(() => ({ image: 'image/jpeg,image/png', video: 'video/mp4', audio: 'audio/wav,audio/mpeg', document: 'text/plain' }[selectedAsset.value?.kind || ''] || ''))

function date(value: string) {
  return formatDateTime(value, locale.value, session.user?.timezone || 'UTC')
}

async function selectAssetView(value: string) {
  await router.push(value === 'saved' ? { path: '/workspace/assets', query: { view: 'saved' } } : '/workspace/assets')
}

async function openPublish(assetId: string) {
  const query = Object.fromEntries(Object.entries(route.query).filter(([key]) => key !== 'draftId'))
  await router.push({ path: route.path, query: { ...query, publish: assetId } })
}

async function closePublish() {
  const query = { ...route.query }
  delete query.publish
  delete query.draftId
  await router.replace({ path: route.path, query })
}

function updatePublishOpen(open: boolean) {
  if (!open) void closePublish()
}

function orderEventLabel(status: string) {
  return t(`marketplace.orderEvents.${status}`)
}

function orderPaymentModeLabel(order: Order) {
  if (order.paymentMode === 'test') return t('workspace.localTestMode')
  if (order.paymentMode === 'waffo_pancake') return t(order.realCharge ? 'workspace.waffoLiveMode' : 'workspace.waffoTestMode')
  if (order.paymentMode === 'epay') return t('workspace.epayMode')
  return t(order.realCharge ? 'workspace.stripeLiveMode' : 'workspace.stripeTestMode')
}

function orderStatusTone(status: Order['status']) {
  if (status === 'fulfilled') return 'accepted'
  if (['test_pending', 'payment_pending', 'test_paid', 'payment_paid', 'refund_requested'].includes(status)) return 'submitted'
  return 'disputed'
}

function taskRoleLabel(item: TaskSummary) {
  if (item.client.id === session.user?.id) return t('workspace.taskRoleCommissioner')
  if (item.assignee?.id === session.user?.id) return t('workspace.taskRoleCreator')
  return t('workspace.taskRoleParticipant')
}

function taskNextAction(item: TaskSummary) {
  if (item.status === 'open') return t('tasks.reviewBrief')
  if (item.status === 'accepted') return t('tasks.reviewBrief')
  return t('tasks.continueTask')
}

function generationStatusLabel(item: Generation) {
  if (item.mode === 'chat' && item.status === 'succeeded') return t('create.studio.chatCompleted')
  return t(`generation.status.${item.status}`)
}

function generationListQuery(cursor = '') {
  const dateFrom = String(route.query.dateFrom || '')
  const dateTo = String(route.query.dateTo || '')
  return {
    mode: String(route.query.mode || ''),
    status: String(route.query.status || ''),
    dateFrom: dateFrom ? `${dateFrom}T00:00:00Z` : '',
    dateTo: dateTo ? `${dateTo}T23:59:59Z` : '',
    cursor,
    limit: Number(route.query.limit) || undefined,
  }
}

function syncGenerationFilters() {
  generationMode.value = String(route.query.mode || '')
  generationStatus.value = String(route.query.status || '')
  generationDateFrom.value = String(route.query.dateFrom || '')
  generationDateTo.value = String(route.query.dateTo || '')
}

function billingListQuery(cursor = '') {
  const dateFrom = String(route.query.dateFrom || '')
  const dateTo = String(route.query.dateTo || '')
  return {
    direction: String(route.query.direction || ''),
    entryType: String(route.query.entryType || ''),
    dateFrom: dateFrom ? `${dateFrom}T00:00:00Z` : '',
    dateTo: dateTo ? `${dateTo}T23:59:59Z` : '',
    cursor,
    limit: Number(route.query.limit) || undefined,
  }
}

function syncBillingFilters() {
  billingDirection.value = String(route.query.direction || '')
  billingEntryType.value = String(route.query.entryType || '')
  billingDateFrom.value = String(route.query.dateFrom || '')
  billingDateTo.value = String(route.query.dateTo || '')
}

async function loadBillingStatement(cursor = '') {
  const page = await api.billingStatement(billingListQuery(cursor))
  if (cursor && billing.value) {
    const known = new Set(billing.value.entries.map(item => item.id))
    billing.value = { ...page, entries: [...billing.value.entries, ...page.entries.filter(item => !known.has(item.id))] }
  } else {
    billing.value = page
  }
  billingNextCursor.value = page.nextCursor || null
}

function hasPaymentEvidence(paymentId: string) {
  return Boolean(
    billing.value?.entries.some(entry => entry.operationId === paymentId)
    || points.value?.entries.some(entry => entry.operationId === paymentId),
  )
}

async function pollPaymentConfirmation(paymentId: string, attempt = 0) {
  if (section.value !== 'billing' || String(route.query.paymentId || '') !== paymentId) return
  try {
    const [, pointOverview] = await Promise.all([loadBillingStatement(), api.pointOverview()])
    points.value = pointOverview
    if (hasPaymentEvidence(paymentId)) {
      success.value = t('workspace.paymentConfirmed')
      return
    }
  } catch {
    // The normal page error state remains authoritative; a transient poll can retry.
  }
  if (attempt < 9) {
    paymentConfirmationTimer = globalThis.setTimeout(() => void pollPaymentConfirmation(paymentId, attempt + 1), 1200)
  }
}

async function purchasePlan(planId: string) {
  if (subscriptionAction.value || !paymentProvider.value?.enabled) return
  subscriptionAction.value = planId
  error.value = ''
  success.value = ''
  try {
    const checkoutWindow = openCheckoutWindow()
    if (!checkoutWindow) {
      error.value = t('errors.codes.checkout_popup_blocked')
      return
    }
    try {
      const checkout = await api.checkoutSubscription(planId)
      checkoutWindow.location.href = checkout.checkoutUrl
      success.value = t('workspace.paymentCheckoutStarted')
    } catch (reason) {
      checkoutWindow.close()
      throw reason
    }
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    subscriptionAction.value = ''
  }
}

async function topUpWallet() {
  if (topupAction.value || !Number.isFinite(topupAmount.value) || topupAmount.value < 0.5) return
  topupAction.value = true
  error.value = ''
  success.value = ''
  try {
    if (!paymentProvider.value?.enabled) {
      error.value = t('workspace.externalPaymentUnavailable')
      return
    }
    const checkoutWindow = openCheckoutWindow()
    if (!checkoutWindow) {
      error.value = t('errors.codes.checkout_popup_blocked')
      return
    }
    try {
      const checkout = await api.checkoutWalletTopup(Math.round(topupAmount.value * 100))
      checkoutWindow.location.href = checkout.checkoutUrl
      success.value = t('workspace.paymentCheckoutStarted')
    } catch (reason) {
      checkoutWindow.close()
      throw reason
    }
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    topupAction.value = false
  }
}

async function applyBillingFilters() {
  const query: Record<string, string> = {}
  if (billingDirection.value) query.direction = billingDirection.value
  if (billingEntryType.value) query.entryType = billingEntryType.value
  if (billingDateFrom.value) query.dateFrom = billingDateFrom.value
  if (billingDateTo.value) query.dateTo = billingDateTo.value
  await router.push({ path: '/workspace/billing', query })
}

async function clearBillingFilters() {
  billingDirection.value = ''
  billingEntryType.value = ''
  billingDateFrom.value = ''
  billingDateTo.value = ''
  await router.push('/workspace/billing')
}

async function loadMoreBilling() {
  if (!billingNextCursor.value || billingLoadingMore.value) return
  billingLoadingMore.value = true
  error.value = ''
  try {
    await loadBillingStatement(billingNextCursor.value)
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    billingLoadingMore.value = false
  }
}

async function loadGenerationList() {
  syncGenerationFilters()
  const page = await api.listGenerations(generationListQuery())
  generations.value = page.items
  generationNextCursor.value = page.nextCursor || null
  if (generationFocus.value && !generations.value.some(item => item.id === generationFocus.value)) {
    const focused = await api.getGeneration(generationFocus.value)
    generations.value = [focused, ...generations.value]
  }
}

async function applyGenerationFilters() {
  const query: Record<string, string> = {}
  if (generationMode.value) query.mode = generationMode.value
  if (generationStatus.value) query.status = generationStatus.value
  if (generationDateFrom.value) query.dateFrom = generationDateFrom.value
  if (generationDateTo.value) query.dateTo = generationDateTo.value
  await router.push({ path: '/create/image', query })
}

async function clearGenerationFilters() {
  generationMode.value = ''
  generationStatus.value = ''
  generationDateFrom.value = ''
  generationDateTo.value = ''
  await router.push('/create/image')
}

let workspaceLoadVersion = 0
async function load() {
  const version = ++workspaceLoadVersion
  const id = assetID.value
  const currentSection = section.value
  if (selectedAsset.value?.id !== id) selectedAsset.value = null
	if (paymentConfirmationTimer !== undefined) {
		globalThis.clearTimeout(paymentConfirmationTimer)
		paymentConfirmationTimer = undefined
	}
  loading.value = true
  error.value = ''
  success.value = ''
  try {
    const user = await session.ensure()
    if (version !== workspaceLoadVersion) return
    if (!user) {
      if (session.error) throw new Error(session.error)
      return
    }
    if (id) {
      const item = await api.getAsset(id)
      if (version !== workspaceLoadVersion) return
      selectedAsset.value = item
      versionTitle.value = selectedAsset.value.title
      versionOpen.value = false
      return
    }
    if (currentSection === 'assets') {
      const [assetResponse, savedResponse] = await Promise.all([api.listAssets(), api.listSavedWorks()])
      if (version !== workspaceLoadVersion) return
      assets.value = assetResponse.items
      assetNextCursor.value = assetResponse.nextCursor || null
      savedWorks.value = savedResponse.items
      savedWorkNextCursor.value = savedResponse.nextCursor || null
    } else if (currentSection === 'generations') {
      await loadGenerationList()
    } else if (currentSection === 'purchases') {
      const page = await api.listAssets()
      if (version !== workspaceLoadVersion) return
      assets.value = page.items
      assetNextCursor.value = page.nextCursor || null
    } else if (currentSection === 'orders') {
  const page = await api.listOrders({ limit: 20 })
  if (version !== workspaceLoadVersion) return
  orders.value = page.items
  orderNextCursor.value = page.nextCursor || null
    } else if (currentSection === 'tasks') {
      const page = await api.listTasks({ mine: true })
      if (version !== workspaceLoadVersion) return
      tasks.value = page.items
    } else if (currentSection === 'billing') {
      syncBillingFilters()
      const [, pointOverview, runtime] = await Promise.all([loadBillingStatement(), api.pointOverview(), api.meta()])
      if (version !== workspaceLoadVersion) return
      points.value = pointOverview
      paymentProvider.value = runtime.paymentProvider
      if (route.query.payment === 'success') {
        const paymentId = String(route.query.paymentId || '')
        success.value = paymentId && hasPaymentEvidence(paymentId) ? t('workspace.paymentConfirmed') : t('workspace.paymentPendingConfirmation')
        if (paymentId && !hasPaymentEvidence(paymentId)) void pollPaymentConfirmation(paymentId)
      }
      if (route.query.payment === 'cancelled') success.value = t('workspace.paymentCancelled')
    }
    if (version === workspaceLoadVersion) selectedAsset.value = null
  } catch (reason) {
    if (version === workspaceLoadVersion) error.value = messageFrom(reason)
  } finally {
    if (version === workspaceLoadVersion) loading.value = false
  }
}

async function loadMoreOrders() {
  if (!orderNextCursor.value || ordersLoadingMore.value) return
  ordersLoadingMore.value = true
  error.value = ''
  try {
    const page = await api.listOrders({ limit: 20, cursor: orderNextCursor.value })
    const known = new Set(orders.value.map(item => item.id))
    orders.value = [...orders.value, ...page.items.filter(item => !known.has(item.id))]
    orderNextCursor.value = page.nextCursor || null
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    ordersLoadingMore.value = false
  }
}

async function loadMoreAssets() {
  if (!assetNextCursor.value || assetLoadingMore.value) return
  assetLoadingMore.value = true
  error.value = ''
  try {
    const page = await api.listAssets({ cursor: assetNextCursor.value })
    const known = new Set(assets.value.map(item => item.id))
    assets.value = [...assets.value, ...page.items.filter(item => !known.has(item.id))]
    assetNextCursor.value = page.nextCursor || null
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    assetLoadingMore.value = false
  }
}

async function loadMoreSavedWorks() {
  if (!savedWorkNextCursor.value || savedWorkLoadingMore.value) return
  savedWorkLoadingMore.value = true
  error.value = ''
  try {
    const page = await api.listSavedWorks({ cursor: savedWorkNextCursor.value })
    const known = new Set(savedWorks.value.map(item => item.postId))
    savedWorks.value = [...savedWorks.value, ...page.items.filter(item => !known.has(item.postId))]
    savedWorkNextCursor.value = page.nextCursor || null
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    savedWorkLoadingMore.value = false
  }
}

async function loadMoreAssetUsages() {
  const item = selectedAsset.value
  if (!item?.usageNextCursor || assetUsageLoadingMore.value) return
  assetUsageLoadingMore.value = true
  error.value = ''
  try {
    const page = await api.listAssetUsages(item.id, { cursor: item.usageNextCursor })
    const known = new Set((item.usages || []).map(usage => `${usage.kind}:${usage.resourceId}`))
    selectedAsset.value = {
      ...item,
      usages: [...(item.usages || []), ...page.items.filter(usage => !known.has(`${usage.kind}:${usage.resourceId}`))],
      usageNextCursor: page.nextCursor,
    }
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    assetUsageLoadingMore.value = false
  }
}

async function toggleAssetFullscreen() {
  const element = assetMediaElement.value
  if (!element) return
  try {
    if (globalThis.document.fullscreenElement) await globalThis.document.exitFullscreen()
    else await element.requestFullscreen()
  } catch {
    // Fullscreen is optional and can be unavailable in embedded browsers.
  }
}

async function loadMoreGenerations() {
  if (!generationNextCursor.value || generationLoadingMore.value) return
  generationLoadingMore.value = true
  error.value = ''
  try {
    const page = await api.listGenerations(generationListQuery(generationNextCursor.value))
    const known = new Set(generations.value.map(item => item.id))
    generations.value = [...generations.value, ...page.items.filter(item => !known.has(item.id))]
    generationNextCursor.value = page.nextCursor || null
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    generationLoadingMore.value = false
  }
}

async function removeSavedWork(item: SavedWork) {
  error.value = ''
  success.value = ''
  try {
    await api.setCommunityReaction(item.postId, 'bookmark', false)
    savedWorks.value = savedWorks.value.filter(saved => saved.postId !== item.postId)
    success.value = t('workspace.savedRemoved')
  } catch (reason) {
    error.value = messageFrom(reason)
  }
}

async function changeGeneration(item: Generation, action: 'cancel' | 'retry') {
  generationAction.value = item.id
  error.value = ''
  success.value = ''
  try {
    await (action === 'cancel'
      ? api.cancelGeneration(item.id, 'Cancelled from personal generation history.')
      : api.retryGeneration(item.id))
    const [, statement, pointOverview] = await Promise.all([loadGenerationList(), api.billingStatement(), api.pointOverview()])
    billing.value = statement
    points.value = pointOverview
    success.value = action === 'cancel' ? t('workspace.generationCancelled') : t('workspace.generationRetried')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    generationAction.value = ''
  }
}

function toggleGenerationSelection(item: Generation) {
  selectedGenerationIDs.value = selectedGenerationIDs.value.includes(item.id)
    ? selectedGenerationIDs.value.filter(id => id !== item.id)
    : [...selectedGenerationIDs.value, item.id]
}

function toggleAllGenerations() {
  selectedGenerationIDs.value = allGenerationsSelected.value ? [] : generations.value.map(item => item.id)
}

async function toggleGenerationFavorite(item: Generation) {
  generationAction.value = `${item.id}:favorite`
  error.value = ''
  success.value = ''
  try {
    const updated = await api.favoriteGeneration(item.id, !item.isFavorite)
    generations.value = generations.value.map(current => current.id === updated.id ? updated : current)
    success.value = t(updated.isFavorite ? 'workspace.generationFavorited' : 'workspace.generationUnfavorited')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    generationAction.value = ''
  }
}

async function applyGenerationBatch(action: 'favorite' | 'unfavorite' | 'cancel') {
  const ids = [...selectedGenerationIDs.value]
  if (!ids.length) return
  generationAction.value = 'batch'
  error.value = ''
  success.value = ''
  try {
    const result = await api.batchGenerations({ generationIds: ids, action, reason: action === 'cancel' ? 'Cancelled from personal generation history.' : undefined })
    const updates = new Map(result.items.map(item => [item.id, item]))
    generations.value = generations.value.map(item => updates.get(item.id) || item)
    selectedGenerationIDs.value = selectedGenerationIDs.value.filter(id => !updates.has(id))
    success.value = result.failures.length
      ? t('workspace.generationBatchPartial', { succeeded: result.items.length, failed: result.failures.length })
      : t('workspace.generationBatchComplete', { count: result.items.length })
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    generationAction.value = ''
  }
}

async function requestRefund(order: Order) {
  refunding.value = order.id
  error.value = ''
  success.value = ''
  try {
    const updated = await api.refundOrder(order.id, refundReasons.value[order.id] || '')
    orders.value = orders.value.map((item) => item.id === updated.id ? updated : item)
    const page = await api.listAssets()
    assets.value = page.items
    assetNextCursor.value = page.nextCursor || null
    success.value = t(updated.status === 'refund_requested' ? 'workspace.refundPending' : 'workspace.refundComplete')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    refunding.value = ''
  }
}

function chooseUpload(event: globalThis.Event) {
  const input = event.target as globalThis.HTMLInputElement
  uploadFile.value = input.files?.[0] || null
  if (uploadFile.value && !uploadTitle.value) uploadTitle.value = uploadFile.value.name.replace(/\.[^.]+$/, '').slice(0, 120)
}

function chooseVersion(event: globalThis.Event) {
  const input = event.target as globalThis.HTMLInputElement
  versionFile.value = input.files?.[0] || null
}

async function uploadVersion() {
  if (!selectedAsset.value || !versionFile.value) return
  versionUploading.value = true
  error.value = ''
  try {
    const form = new globalThis.FormData()
    form.append('title', versionTitle.value || selectedAsset.value.title)
    form.append('note', versionNote.value)
    form.append('file', versionFile.value)
    const item = await api.uploadAssetVersion(selectedAsset.value.id, form)
    versionOpen.value = false
    versionFile.value = null
    versionNote.value = ''
    await router.push(`/workspace/assets/${item.id}`)
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    versionUploading.value = false
  }
}

async function uploadAsset() {
  if (!uploadFile.value) return
  uploading.value = true
  error.value = ''
  success.value = ''
  try {
    const form = new globalThis.FormData()
    form.append('title', uploadTitle.value)
    form.append('file', uploadFile.value)
    const item = await api.uploadAsset(form)
    assets.value = [item, ...assets.value]
    success.value = t('workspace.uploadQueued')
    uploadTitle.value = ''
    uploadFile.value = null
    uploadOpen.value = false
    void pollUpload(item.id)
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    uploading.value = false
  }
}

async function pollUpload(id: string) {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    await new Promise(resolve => globalThis.setTimeout(resolve, 750))
    try {
      const updated = await api.getAsset(id)
      assets.value = assets.value.map(item => item.id === updated.id ? updated : item)
      if (updated.scanStatus !== 'pending') {
        success.value = updated.scanStatus === 'clean' ? t('workspace.uploadReady') : t('workspace.uploadReview')
        return
      }
    } catch {
      return
    }
  }
}

watch(() => route.fullPath, () => void load())
onMounted(() => void load())
onBeforeUnmount(() => {
  workspaceLoadVersion++
  if (paymentConfirmationTimer !== undefined) globalThis.clearTimeout(paymentConfirmationTimer)
})
</script>

<template>
  <section class="workspace-page content-width" :class="{ 'assets-task-page': section === 'assets' && !assetID, 'asset-detail-workspace-page': Boolean(assetID) }">
    <AuthRequiredState
      v-if="!loading && needsAuthentication"
      :title="t(publishRequested ? 'authRequired.publishTitle' : 'authRequired.workspaceTitle')"
      :summary="t(publishRequested ? 'authRequired.publishSummary' : 'authRequired.workspaceSummary')"
      :return-to="route.fullPath"
    />

    <template v-else-if="assetID">
      <RouterLink class="text-link asset-back" :to="contentListReturn('/workspace/assets')">
        <ArrowLeft :size="17" />{{ t(contentListReturn('/workspace/assets').startsWith('/workspace/purchases') ? 'content.backPurchases' : 'workspace.assetBack') }}
      </RouterLink>
      <div v-if="loading" class="page-state" aria-live="polite">
        {{ t('status.loadingAssets') }}
      </div>
      <div v-else-if="error || !selectedAsset" class="page-state" role="alert">
        <p>{{ error }}</p><UiButton class="command-button secondary" variant="secondary" @click="load">
          <template #start>
            <RefreshCw :size="17" />
          </template>{{ t('actions.retry') }}
        </UiButton>
      </div>
      <div v-else class="asset-detail-layout">
        <header class="asset-content-heading">
          <h1>{{ selectedAsset.title }}</h1>
          <div class="asset-content-actions">
            <UiButton v-if="selectedAsset.provenance?.purchase?.orderStatus === 'fulfilled' && selectedAsset.scanStatus === 'clean'" as="RouterLink" class="command-button primary" variant="primary" :to="{ path: creationPath(selectedAsset.kind), query: { sourceAssetId: selectedAsset.id } }">
              <template #start>
                <WandSparkles :size="16" />
              </template>{{ t('actions.useInCreate') }}
            </UiButton>
            <UiButton v-if="selectedAsset.sourceType !== 'purchase' && selectedAsset.scanStatus === 'clean'" class="command-button primary" variant="primary" @click="openPublish(selectedAsset.id)">
              <template #start>
                <Upload :size="16" />
              </template>{{ t('actions.publishAsset') }}
            </UiButton>
          </div>
        </header>
        <div class="asset-detail-main">
          <section ref="assetMediaElement" class="asset-detail-media-panel">
            <div class="asset-detail-media-toolbar">
              <div class="asset-detail-media-badges">
                <UiBadge variant="primary">
                  <Boxes :size="14" />{{ t(`workspace.sourceTypes.${selectedAsset.sourceType}`) }}
                </UiBadge>
                <UiBadge :variant="selectedAsset.scanStatus === 'clean' ? 'success' : selectedAsset.scanStatus === 'rejected' ? 'danger' : 'warning'">
                  <ShieldCheck :size="14" />{{ t(`workspace.scanStatus.${selectedAsset.scanStatus}`) }}
                </UiBadge>
              </div>
              <UiIconButton :label="t('actions.expandPreview')" variant="soft" size="md" @click="toggleAssetFullscreen">
                <Maximize2 :size="17" />
              </UiIconButton>
            </div>
            <div v-if="selectedAsset.scanStatus === 'clean'" class="asset-detail-media">
              <AssetMedia :src="selectedAsset.mediaUrl" :kind="selectedAsset.kind" :alt="selectedAsset.title" :width="selectedAsset.width || 1600" :height="selectedAsset.height || 1200" eager />
            </div>
            <div v-else class="asset-detail-media asset-scan-state" :data-status="selectedAsset.scanStatus">
              <ShieldCheck :size="28" /><strong>{{ t(`workspace.scanStatus.${selectedAsset.scanStatus}`) }}</strong><p>{{ selectedAsset.scanReason || t('workspace.scanPendingDetail') }}</p>
            </div>
          </section>

          <UiCard v-if="selectedAsset.provenance?.generation" class="asset-detail-card asset-generation-card">
            <header class="asset-detail-card-heading">
              <span class="asset-detail-card-icon"><WandSparkles :size="18" /></span>
              <div><span class="asset-card-eyebrow">{{ t('workspace.generatedWith') }}</span><h2>{{ selectedAsset.provenance.generation.modelName }}</h2></div>
            </header>
            <div class="asset-generation-meta">
              <div><span>{{ t('create.modelLabel') }}</span><strong>{{ selectedAsset.provenance.generation.modelName }}</strong></div>
              <div><span>{{ t('admin.providerName') }}</span><strong>{{ selectedAsset.provenance.generation.provider === 'local_test' ? t('status.localProvider') : selectedAsset.provenance.generation.provider }}</strong></div>
            </div>
            <div class="asset-prompt-block">
              <span>{{ t('work.prompt') }}</span><p>{{ selectedAsset.provenance.generation.prompt }}</p>
            </div>
            <div v-if="selectedAsset.provenance.generation.sourceAsset" class="source-lineage asset-source-lineage">
              <span>{{ t('workspace.sourceAsset') }}</span><RouterLink :to="`/workspace/assets/${selectedAsset.provenance.generation.sourceAsset.id}`">
                {{ selectedAsset.provenance.generation.sourceAsset.title }}<ArrowRight :size="15" />
              </RouterLink><small>{{ selectedAsset.provenance.generation.sourceAsset.purchase?.licenseName || licenseLabel(selectedAsset.provenance.generation.sourceAsset.licenseCode) }}</small>
            </div>
          </UiCard>

          <UiCard v-if="selectedAsset.provenance?.purchase" class="asset-detail-card">
            <header class="asset-detail-card-heading">
              <span class="asset-detail-card-icon"><ShoppingBag :size="18" /></span><div><span class="asset-card-eyebrow">{{ t('workspace.purchasedFrom') }}</span><h2>{{ selectedAsset.provenance.purchase.productTitle }}</h2></div>
            </header>
            <strong>{{ selectedAsset.provenance.purchase.sellerName }} · @{{ selectedAsset.provenance.purchase.sellerHandle }}</strong>
            <p class="asset-detail-muted">
              {{ selectedAsset.provenance.purchase.licenseName }}
            </p>
            <small class="asset-detail-muted">{{ orderPaymentModeLabel({ paymentMode: selectedAsset.provenance.purchase.paymentMode, realCharge: selectedAsset.provenance.purchase.realCharge } as Order) }} · {{ t(`marketplace.orderStatus.${selectedAsset.provenance.purchase.orderStatus}`) }}</small>
            <div class="asset-detail-card-actions">
              <UiButton v-if="selectedAsset.provenance.purchase.orderStatus === 'fulfilled'" as="a" class="command-button secondary" variant="secondary" :href="selectedAsset.mediaUrl" :download="selectedAsset.title">
                <template #start>
                  <Download :size="16" />
                </template>{{ t('actions.downloadLicensed') }}
              </UiButton>
              <UiButton as="RouterLink" class="command-button secondary" variant="secondary" to="/workspace/orders">
                <template #start>
                  <ReceiptText :size="16" />
                </template>{{ t('marketplace.viewOrder') }}
              </UiButton>
            </div>
          </UiCard>

          <UiCard v-if="selectedAsset.sourceType === 'upload'" class="asset-detail-card">
            <header class="asset-detail-card-heading">
              <span class="asset-detail-card-icon"><Upload :size="18" /></span><div><span class="asset-card-eyebrow">{{ t('workspace.uploadEvidence') }}</span><h2>{{ selectedAsset.uploadedFilename }}</h2></div>
            </header>
            <span class="asset-detail-muted">{{ selectedAsset.mimeType }} · {{ selectedAsset.sizeBytes ? t('workspace.fileSize', { size: selectedAsset.sizeBytes }) : '' }}</span>
            <p class="asset-detail-muted">
              {{ selectedAsset.scanReason || t('workspace.scanPendingDetail') }}
            </p>
          </UiCard>
        </div>

        <aside class="asset-inspector">
          <UiCard class="asset-detail-card asset-provenance-card">
            <span class="asset-provenance-heading"><ShieldCheck :size="17" /><span class="status-label">{{ t('workspace.provenance') }}</span></span>
            <dl class="asset-facts">
              <div><dt>{{ t('marketplace.license') }}</dt><dd>{{ licenseLabel(selectedAsset.licenseCode) }}</dd></div><div><dt>{{ t('workspace.sourceAsset') }}</dt><dd>{{ t(`workspace.sourceTypes.${selectedAsset.sourceType}`) }}</dd></div><div><dt>{{ t('workspace.assetVersion') }}</dt><dd>v{{ selectedAsset.versionNumber }}</dd></div><div><dt>{{ t('workspace.granted') }}</dt><dd>{{ date(selectedAsset.createdAt) }}</dd></div>
            </dl>
          </UiCard>

          <UiCard class="asset-detail-card asset-version-block">
            <header class="asset-detail-card-heading">
              <span class="asset-detail-card-icon"><GitBranch :size="18" /></span><div><span class="asset-card-eyebrow">{{ t('workspace.assetVersion') }}</span><h2>{{ t('workspace.versionHistory') }}</h2></div>
            </header>
            <div class="asset-version-list">
              <RouterLink v-for="version in selectedAsset.versions" :key="version.id" class="asset-version-row" :class="{ 'is-active': version.id === selectedAsset.id }" :to="`/workspace/assets/${version.id}`">
                <div class="asset-version-row-top">
                  <strong>v{{ version.versionNumber }}</strong><UiBadge :variant="version.scanStatus === 'clean' ? 'success' : version.scanStatus === 'rejected' ? 'danger' : 'warning'">
                    <ShieldCheck :size="13" />{{ t(`workspace.scanStatus.${version.scanStatus}`) }}
                  </UiBadge>
                </div>
                <span>{{ version.title }}</span><small>{{ version.versionNote || date(version.createdAt) }}</small>
              </RouterLink>
            </div>
            <UiButton v-if="selectedAsset.isLatestVersion && selectedAsset.sourceType !== 'purchase'" class="command-button secondary wide" variant="secondary" @click="versionOpen = !versionOpen">
              <template #start>
                <Upload :size="16" />
              </template>{{ t('workspace.uploadVersion') }}
            </UiButton>
            <form v-if="versionOpen" class="asset-version-form" @submit.prevent="uploadVersion">
              <label>{{ t('workspace.versionTitle') }}<UiInput v-model="versionTitle" type="text" minlength="3" maxlength="120" :placeholder="selectedAsset.title" /></label>
              <label>{{ t('workspace.versionNote') }}<UiTextarea v-model="versionNote" rows="2" minlength="3" maxlength="500" required /></label>
              <label>{{ t('workspace.versionFile') }}<UiFileInput :accept="versionAccept" required @change="chooseVersion" /></label>
              <small>{{ t('workspace.versionScanBoundary') }}</small>
              <UiButton class="command-button primary wide" variant="primary" type="submit" :loading="versionUploading" :disabled="!versionFile">
                <template #start>
                  <Upload v-if="!versionUploading" :size="16" />
                </template>{{ t('workspace.queueVersion') }}
              </UiButton>
            </form>
          </UiCard>

          <UiCard class="asset-detail-card asset-usage-block">
            <header class="asset-detail-card-heading">
              <span class="asset-detail-card-icon"><GitBranch :size="18" /></span><div><span class="asset-card-eyebrow">{{ t('workspace.assetUsage') }}</span><h2>{{ t('workspace.assetUsage') }}</h2></div>
            </header>
            <p class="asset-detail-muted">
              {{ t('workspace.assetUsageSummary') }}
            </p>
            <template v-for="usage in selectedAsset.usages" :key="`${usage.kind}-${usage.resourceId}`">
              <RouterLink v-if="usage.targetPath" class="source-lineage usage-lineage" :to="usage.targetPath">
                <span>{{ t(`workspace.usageKinds.${usage.kind}`) }} · {{ t('workspace.assetUsageVersion', { version: usage.assetVersion }) }}</span><strong>{{ usage.title }}</strong><small>{{ t(`workspace.usageStatuses.${usage.status}`) }} · {{ date(usage.createdAt) }}</small>
              </RouterLink>
              <div v-else class="source-lineage usage-lineage unavailable">
                <span>{{ t(`workspace.usageKinds.${usage.kind}`) }} · {{ t('workspace.assetUsageVersion', { version: usage.assetVersion }) }}</span><strong>{{ usage.title }}</strong><small>{{ t(`workspace.usageStatuses.${usage.status}`) }} · {{ date(usage.createdAt) }}</small>
              </div>
            </template>
            <div v-if="!selectedAsset.usages?.length" class="asset-usage-empty">
              <span class="asset-usage-illustration"><FolderOpen :size="38" /><GitBranch :size="18" /></span><strong>{{ t('workspace.noAssetUsage') }}</strong>
            </div>
            <UiButton v-if="selectedAsset.usageNextCursor" class="command-button secondary wide" variant="secondary" :loading="assetUsageLoadingMore" @click="loadMoreAssetUsages">
              {{ t('actions.loadMore') }}
            </UiButton>
          </UiCard>
        </aside>
      </div>
    </template>

    <template v-else>
      <PageHero
        v-if="workspaceHero"
        :eyebrow="workspaceHero.eyebrow"
        :eyebrow-icon="workspaceHero.eyebrowIcon"
        :title="sectionMeta.title"
        :summary="sectionMeta.summary"
        :stats="workspaceHero.stats"
        :stats-label="workspaceHero.statsLabel"
        :artwork-src="workspaceHero.artworkSrc"
        :artwork-width="1536"
        :artwork-height="1024"
        adapt-artwork-for-dark
      >
        <template #actions>
          <UiButton v-if="section === 'assets' && assetView === 'owned'" class="command-button primary" variant="primary" @click="uploadOpen = !uploadOpen">
            <template #start>
              <Upload :size="17" />
            </template>{{ t('workspace.uploadAsset') }}
          </UiButton>
          <UiButton v-else-if="section === 'assets'" as="RouterLink" class="command-button secondary" variant="secondary" to="/community">
            <template #start>
              <Bookmark :size="17" />
            </template>{{ t('workspace.browseCommunity') }}
          </UiButton>
          <UiButton v-if="section === 'purchases' || section === 'orders'" as="RouterLink" class="command-button primary" variant="primary" to="/market">
            <template #start>
              <Store :size="17" />
            </template>{{ t('workspace.browseMarket') }}
          </UiButton>
          <UiButton v-if="section === 'purchases'" as="RouterLink" class="command-button secondary" variant="secondary" to="/workspace/orders">
            <template #start>
              <ReceiptText :size="17" />
            </template>{{ t('workspace.orders') }}
          </UiButton>
          <UiButton v-if="section === 'orders'" as="RouterLink" class="command-button secondary" variant="secondary" to="/workspace/purchases">
            <template #start>
              <ShoppingBag :size="17" />
            </template>{{ t('workspace.purchases') }}
          </UiButton>
          <template v-if="section === 'tasks'">
            <UiButton as="RouterLink" class="command-button primary" variant="primary" to="/market/demands">
              <template #start>
                <ClipboardList :size="17" />
              </template>{{ t('workspace.browseTasks') }}
            </UiButton>
            <UiButton as="RouterLink" class="command-button secondary" variant="secondary" to="/market/demands?view=mine">
              <template #start>
                <ArrowRight :size="17" />
              </template>{{ t('tasks.myActivity') }}
            </UiButton>
          </template>
        </template>
      </PageHero>
      <header v-else-if="section !== 'billing'" class="workspace-header">
        <div>
          <span class="status-label"><component :is="sectionMeta.icon" :size="14" />{{ t('workspace.workbenchLabel') }} · {{ t('workspace.itemCount', { count: sectionMeta.count }) }}</span>
          <h1>{{ sectionMeta.title }}</h1><p>{{ sectionMeta.summary }}</p>
        </div>
        <UiButton v-if="section === 'assets' && assetView === 'owned'" class="command-button primary" variant="primary" @click="uploadOpen = !uploadOpen">
          <template #start>
            <Upload :size="17" />
          </template>{{ sectionMeta.actionLabel }}
        </UiButton><UiButton v-else-if="section !== 'assets'" as="RouterLink" class="command-button primary" variant="primary" :to="sectionMeta.actionTo">
          <template #start>
            <component :is="sectionMeta.actionIcon" :size="17" />
          </template>{{ sectionMeta.actionLabel }}
        </UiButton>
      </header>
      <nav v-if="section !== 'assets'" class="section-tabs workspace-switcher" :aria-label="t('workspace.sectionsLabel')">
        <RouterLink to="/workspace/assets" :class="{ active: section === 'assets' }">
          <Boxes :size="17" />{{ t('workspace.assets') }}
        </RouterLink>
        <RouterLink to="/workspace/purchases" :class="{ active: section === 'purchases' }">
          <ShoppingBag :size="17" />{{ t('workspace.purchases') }}
        </RouterLink>
        <RouterLink to="/workspace/orders" :class="{ active: section === 'orders' }">
          <ReceiptText :size="17" />{{ t('workspace.orders') }}
        </RouterLink>
        <RouterLink to="/workspace/tasks" :class="{ active: section === 'tasks' }">
          <ClipboardList :size="17" />{{ t('workspace.tasks') }}
        </RouterLink>
        <RouterLink to="/workspace/billing" :class="{ active: section === 'billing' }">
          <WalletCards :size="17" />{{ t('workspace.billing') }}
        </RouterLink>
      </nav>

      <div v-if="section === 'assets'" class="view-switcher-bar assets-view-switcher-bar">
        <UiTabs class="view-switcher" :model-value="assetView" :items="assetViewTabs" :label="t('workspace.assetViewsLabel')" @update:model-value="selectAssetView" />
      </div>

      <form v-if="uploadOpen && section === 'assets' && assetView === 'owned'" class="asset-upload-panel" @submit.prevent="uploadAsset">
        <header>
          <div><span class="status-label">{{ t('workspace.localScanLabel') }}</span><h2>{{ t('workspace.uploadAsset') }}</h2><p>{{ t('workspace.uploadSummary') }}</p></div><UiIconButton class="icon-button" :label="t('actions.close')" @click="uploadOpen = false">
            <X :size="17" />
          </UiIconButton>
        </header>
        <div><label>{{ t('workspace.uploadTitle') }}<UiInput v-model="uploadTitle" type="text" minlength="3" maxlength="120" required /></label><label>{{ t('workspace.uploadFile') }}<UiFileInput accept="image/jpeg,image/png,video/mp4,audio/wav,audio/mpeg,text/plain" required @change="chooseUpload" /></label></div>
        <small>{{ t('workspace.uploadLimits') }}</small><UiButton class="command-button primary" variant="primary" type="submit" :loading="uploading" :disabled="!uploadFile">
          <template #start>
            <Upload v-if="!uploading" :size="17" />
          </template>{{ uploading ? t('workspace.uploading') : t('workspace.queueUpload') }}
        </UiButton>
      </form>

      <div v-if="success && section === 'assets'" class="task-feedback success" role="status">
        <FileCheck2 :size="18" />{{ success }}
      </div>

      <div v-if="loading" class="page-state" aria-live="polite">
        {{ t('status.loadingWorkspace') }}
      </div>
      <div v-else-if="error && section !== 'orders'" class="page-state" role="alert">
        <p>{{ error }}</p><UiButton class="command-button secondary" variant="secondary" @click="load">
          <template #start>
            <RefreshCw :size="17" />
          </template>{{ t('actions.retry') }}
        </UiButton>
      </div>

      <template v-else-if="section === 'assets' || section === 'purchases'">
        <div class="task-browser-layout asset-browser-layout">
          <aside class="task-category-panel asset-category-panel" :aria-label="t('workspace.sectionsLabel')">
            <h2>{{ t('workspace.assets') }}</h2>
            <nav>
              <RouterLink class="asset-category-link" to="/workspace/assets" :class="{ active: section === 'assets' && assetView === 'owned' }">
                <Boxes :size="17" /><span>{{ t('workspace.ownedAssets') }}</span><small>{{ assets.length }}</small>
              </RouterLink>
              <RouterLink class="asset-category-link" to="/workspace/assets?view=saved" :class="{ active: section === 'assets' && assetView === 'saved' }">
                <Bookmark :size="17" /><span>{{ t('workspace.savedWorks') }}</span><small>{{ savedWorks.length }}</small>
              </RouterLink>
              <RouterLink class="asset-category-link" to="/workspace/purchases" :class="{ active: section === 'purchases' }">
                <ShoppingBag :size="17" /><span>{{ t('workspace.purchases') }}</span><small>{{ section === 'purchases' ? visibleAssets.length : assets.filter(item => item.sourceType === 'purchase').length }}</small>
              </RouterLink>
            </nav>
            <section class="asset-category-note">
              <span><ShieldCheck :size="18" /></span><div><strong>{{ t('workspace.localScanLabel') }}</strong><p>{{ t('workspace.assetsSummary') }}</p></div>
            </section>
          </aside>

          <div class="task-results asset-results">
            <div v-if="section === 'assets'" class="task-results-meta">
              <div><strong>{{ sectionMeta.count }} {{ t('workspace.assets') }}</strong><span>{{ sectionMeta.summary }}</span></div>
            </div>

            <div v-if="assetView === 'saved'" class="saved-rights-notice" role="note">
              <ShieldCheck :size="19" /><div><strong>{{ t('workspace.savedRightsTitle') }}</strong><p>{{ t('workspace.savedRightsSummary') }}</p></div>
            </div>

            <div class="task-result-list asset-result-list">
              <template v-if="assetView === 'saved'">
                <article v-for="item in savedWorks" :key="item.postId" class="task-row asset-row" data-type="saved">
                  <RouterLink class="task-row-media asset-row-media" :to="`/works/${item.workId}`">
                    <AssetMedia :src="item.mediaUrl" :kind="item.mediaKind" :alt="item.title" :width="item.width || 800" :height="item.height || 800" :controls="false" />
                  </RouterLink>
                  <div class="task-row-copy">
                    <div class="task-row-heading">
                      <span class="task-type-badge asset-type-badge"><Bookmark :size="13" />{{ t('workspace.referenceOnly') }}</span>
                    </div>
                    <strong>{{ item.title }}</strong><small>{{ licenseLabel(item.licenseCode) }}</small>
                    <div class="task-row-byline">
                      <span>@{{ item.authorHandle }}</span><span>{{ date(item.savedAt) }}</span>
                    </div>
                  </div>
                  <div class="task-row-commercial">
                    <span class="task-row-data"><small>{{ t('workspace.referenceOnly') }}</small><strong>{{ t('workspace.savedWorks') }}</strong></span>
                    <div class="asset-row-action-stack">
                      <UiButton as="RouterLink" class="command-button primary task-row-action" variant="primary" :to="`/works/${item.workId}`">
                        {{ t('actions.viewDetails') }}<template #end>
                          <ArrowRight :size="16" />
                        </template>
                      </UiButton>
                      <UiIconButton class="icon-button" :label="t('workspace.removeSaved')" @click="removeSavedWork(item)">
                        <BookmarkX :size="16" />
                      </UiIconButton>
                    </div>
                  </div>
                </article>
              </template>
              <template v-else>
                <article v-for="asset in visibleAssets" :key="asset.id" class="task-row asset-row" :data-type="asset.sourceType">
                  <RouterLink class="task-row-media asset-row-media" :to="`/workspace/assets/${asset.id}`">
                    <AssetMedia v-if="asset.scanStatus === 'clean'" :src="asset.mediaUrl" :kind="asset.kind" :alt="asset.title" :width="asset.width || 800" :height="asset.height || 800" :controls="false" />
                    <span v-else class="asset-row-scan-state"><ShieldCheck :size="24" /><strong>{{ t(`workspace.scanStatus.${asset.scanStatus}`) }}</strong></span>
                  </RouterLink>
                  <div class="task-row-copy">
                    <div class="task-row-heading">
                      <span class="task-type-badge asset-type-badge"><Boxes :size="13" />{{ t(`workspace.sourceTypes.${asset.sourceType}`) }}</span><span class="task-status" :data-status="asset.scanStatus">{{ t(`workspace.scanStatus.${asset.scanStatus}`) }}</span>
                    </div>
                    <strong>{{ asset.title }}</strong>
                    <div class="task-row-byline">
                      <span>{{ date(asset.createdAt) }}</span><span>{{ t(`create.modes.${asset.kind === 'audio' ? 'music' : asset.kind === 'document' ? 'chat' : asset.kind}`) }}</span>
                    </div>
                  </div>
                  <div class="task-row-commercial">
                    <span class="task-row-data"><small>{{ t('workspace.assetVersion') }}</small><strong>v{{ asset.versionNumber }}</strong></span>
                    <span class="task-row-data"><small>{{ t('marketplace.license') }}</small><strong>{{ licenseLabel(asset.licenseCode) }}</strong></span>
                    <div class="asset-row-action-stack">
                      <UiButton as="RouterLink" class="command-button primary task-row-action" variant="primary" :to="`/workspace/assets/${asset.id}`">
                        {{ t('actions.viewDetails') }}<template #end>
                          <ArrowRight :size="16" />
                        </template>
                      </UiButton>
                      <UiIconButton v-if="asset.sourceType !== 'purchase' && asset.scanStatus === 'clean'" class="icon-button asset-publish-action" :label="t('actions.publishAsset')" @click="openPublish(asset.id)">
                        <Upload :size="16" />
                      </UiIconButton>
                      <UiIconButton v-if="asset.sourceType === 'purchase' && asset.scanStatus === 'clean' && asset.provenance?.purchase?.orderStatus === 'fulfilled'" as="RouterLink" class="icon-button" :to="{ path: creationPath(asset.kind), query: { sourceAssetId: asset.id } }" :label="t('actions.useInCreate')">
                        <WandSparkles :size="16" />
                      </UiIconButton>
                    </div>
                  </div>
                </article>
              </template>

              <div v-if="(assetView === 'saved' ? !savedWorks.length : !visibleAssets.length)" class="task-market-state task-market-empty">
                <span><Bookmark v-if="assetView === 'saved'" :size="20" /><ShoppingBag v-else-if="section === 'purchases'" :size="20" /><Boxes v-else :size="20" /></span>
                <strong>{{ assetView === 'saved' ? t('workspace.noSavedTitle') : section === 'purchases' ? t('workspace.noPurchasesTitle') : t('workspace.noAssetsTitle') }}</strong>
                <p>{{ assetView === 'saved' ? t('workspace.noSaved') : section === 'purchases' ? t('workspace.noPurchases') : t('workspace.noAssets') }}</p>
                <UiButton v-if="assetView === 'saved'" as="RouterLink" class="command-button secondary" variant="secondary" to="/community">
                  {{ t('workspace.browseCommunity') }}<ArrowRight :size="16" />
                </UiButton>
                <UiButton v-else-if="section !== 'purchases'" as="RouterLink" class="command-button secondary" variant="secondary" to="/create/image">
                  {{ t('actions.startCreating') }}<ArrowRight :size="16" />
                </UiButton>
              </div>
            </div>
            <UiButton v-if="assetView === 'saved' ? savedWorkNextCursor : assetNextCursor" class="command-button secondary generation-load-more" variant="secondary" :loading="assetView === 'saved' ? savedWorkLoadingMore : assetLoadingMore" @click="assetView === 'saved' ? loadMoreSavedWorks() : loadMoreAssets()">
              {{ t('actions.loadMore') }}
            </UiButton>
          </div>
        </div>
      </template>

      <div v-else-if="section === 'generations'" class="generation-list">
        <form class="generation-filters" @submit.prevent="applyGenerationFilters">
          <label><span>{{ t('workspace.generationModeFilter') }}</span><UiSelect v-model="generationMode">
            <option value="">{{ t('workspace.allModes') }}</option><option value="chat">{{ t('create.modes.chat') }}</option><option value="image">{{ t('create.modes.image') }}</option><option value="video">{{ t('create.modes.video') }}</option><option value="music">{{ t('create.modes.music') }}</option>
          </UiSelect></label>
          <label><span>{{ t('workspace.generationStatusFilter') }}</span><UiSelect v-model="generationStatus">
            <option value="">{{ t('workspace.allStatuses') }}</option><option value="queued">{{ t('generation.status.queued') }}</option><option value="running">{{ t('generation.status.running') }}</option><option value="succeeded">{{ t('generation.status.succeeded') }}</option><option value="failed">{{ t('generation.status.failed') }}</option><option value="cancelled">{{ t('generation.status.cancelled') }}</option>
          </UiSelect></label>
          <label><span>{{ t('workspace.dateFrom') }}</span><UiInput v-model="generationDateFrom" type="date" /></label>
          <label><span>{{ t('workspace.dateTo') }}</span><UiInput v-model="generationDateTo" type="date" /></label>
          <UiButton class="command-button secondary" variant="secondary" type="submit">
            <template #start>
              <ListFilter :size="16" />
            </template>{{ t('actions.applyFilters') }}
          </UiButton>
          <UiIconButton class="icon-button" :label="t('actions.clearFilters')" @click="clearGenerationFilters">
            <X :size="16" />
          </UiIconButton>
        </form>
        <div v-if="success" class="task-feedback success" role="status">
          <FileCheck2 :size="18" />{{ success }}
        </div>
        <div v-if="error" class="task-feedback error" role="alert">
          <RefreshCw :size="18" />{{ error }}
        </div>
        <div v-if="generations.length" class="generation-bulk-toolbar">
          <label class="generation-select-all"><UiCheckbox :model-value="allGenerationsSelected" @update:model-value="toggleAllGenerations" /><span>{{ t('workspace.selectGenerations', { count: selectedGenerations.length }) }}</span></label>
          <div v-if="selectedGenerations.length" class="generation-bulk-actions">
            <UiButton class="command-button secondary" variant="secondary" :loading="generationAction === 'batch'" @click="applyGenerationBatch('favorite')">
              <template #start>
                <Bookmark v-if="generationAction !== 'batch'" :size="15" />
              </template>{{ t('workspace.favoriteSelected') }}
            </UiButton>
            <UiButton class="command-button secondary" variant="secondary" :loading="generationAction === 'batch'" @click="applyGenerationBatch('unfavorite')">
              <template #start>
                <BookmarkX v-if="generationAction !== 'batch'" :size="15" />
              </template>{{ t('workspace.unfavoriteSelected') }}
            </UiButton>
            <UiButton class="command-button secondary" variant="secondary" :loading="generationAction === 'batch'" @click="applyGenerationBatch('cancel')">
              <template #start>
                <Ban v-if="generationAction !== 'batch'" :size="15" />
              </template>{{ t('workspace.cancelSelected') }}
            </UiButton>
          </div>
        </div>
        <article v-for="item in generations" :key="item.id" class="generation-row" :class="{ 'usage-focus': generationFocus === item.id }">
          <label class="generation-row-select"><UiCheckbox :model-value="selectedGenerationIDs.includes(item.id)" :aria-label="t('workspace.selectGeneration', { prompt: item.prompt })" @click.stop @update:model-value="toggleGenerationSelection(item)" /></label>
          <div v-if="item.outputMediaUrl" class="generation-thumb">
            <AssetMedia :src="item.outputMediaUrl" :kind="item.mode === 'music' ? 'audio' : item.mode === 'chat' ? 'document' : item.mode" :alt="item.prompt" :text="item.outputText" :width="100" :height="100" :controls="false" />
          </div><div v-else class="generation-thumb placeholder">
            <Clock3 :size="20" />
          </div><div class="generation-row-copy">
            <strong>{{ item.prompt }}</strong><span>{{ t(`create.modes.${item.mode}`) }} · {{ item.provider === 'local_test' ? t('status.localProvider') : item.provider }}</span>
          </div><span class="generation-row-data model-cell"><small>{{ t('workspace.model') }}</small><strong>{{ item.modelName }}</strong></span><span class="generation-row-data created-cell"><small>{{ t('workspace.created') }}</small><strong>{{ date(item.createdAt) }}</strong></span><span class="generation-row-data cost-cell"><small>{{ t('workspace.pointsUsed') }}</small><strong>{{ (item.chargedPoints || item.estimatedPoints).toLocaleString(locale) }} {{ t('workspace.pointsUnit') }}</strong></span><div class="generation-row-status" :data-status="item.status">
            <strong>{{ generationStatusLabel(item) }}</strong><span>{{ item.progress }}%</span>
          </div><div class="generation-actions">
            <UiIconButton class="icon-button" :label="item.isFavorite ? t('workspace.unfavoriteGeneration') : t('workspace.favoriteGeneration')" :disabled="generationAction === `${item.id}:favorite`" @click="toggleGenerationFavorite(item)">
              <MotionFavoriteIcon :active="item.isFavorite" kind="bookmark" :size="16" />
            </UiIconButton>
            <UiIconButton v-if="item.actions.canCancel" class="icon-button" :label="t('actions.cancel')" :disabled="generationAction === item.id" @click="changeGeneration(item, 'cancel')">
              <Ban :size="16" />
            </UiIconButton>
            <UiIconButton v-if="item.actions.canRetry" class="icon-button" :label="t('actions.retry')" :disabled="generationAction === item.id" @click="changeGeneration(item, 'retry')">
              <RotateCcw :size="16" />
            </UiIconButton>
            <UiIconButton v-if="item.actions.canDownload && item.actions.downloadPath" as="a" class="icon-button" :href="item.actions.downloadPath" :download="item.prompt" :label="t('actions.download')">
              <Download :size="16" />
            </UiIconButton>
            <UiIconButton v-if="item.actions.canReuse && item.actions.reusePath" as="RouterLink" class="icon-button" :to="item.actions.reusePath" :label="t('actions.useInCreate')">
              <WandSparkles :size="16" />
            </UiIconButton>
            <UiIconButton v-if="item.actions.canView" as="RouterLink" class="icon-button" :to="item.actions.viewPath" :label="t('actions.viewDetails')">
              <ArrowRight :size="16" />
            </UiIconButton>
          </div>
          <div v-if="generationFocus === item.id" class="generation-focus-detail">
            <span>{{ t('workspace.generationId') }} <strong>{{ item.id }}</strong></span><span v-if="item.retryOfGenerationId">{{ t('workspace.retryOf') }} <strong>{{ item.retryOfGenerationId }}</strong></span><p v-if="item.errorMessage">
              {{ item.errorMessage }}
            </p><p v-else-if="item.cancelReason">
              {{ item.cancelReason }}
            </p>
            <template v-if="item.providerUsage">
              <p v-if="item.providerUsage.status === 'reported'">
                {{ t('workspace.providerUsageReported', { input: item.providerUsage.inputTokens, cached: item.providerUsage.cachedInputTokens, output: item.providerUsage.outputTokens, reasoning: item.providerUsage.reasoningTokens, total: item.providerUsage.totalTokens }) }}
              </p>
              <p v-else>
                {{ t('workspace.providerUsageNotReported') }}
              </p>
              <small>{{ t('workspace.providerUsageBoundary') }}</small>
            </template>
          </div>
        </article>
        <div v-if="!generations.length" class="workspace-empty">
          <Clock3 :size="22" /><h2>{{ t('workspace.noGenerationsTitle') }}</h2><p>{{ t('workspace.noGenerations') }}</p><UiButton as="RouterLink" class="command-button secondary" variant="secondary" to="/create/image">
            {{ t('actions.startCreating') }}<ArrowRight :size="16" />
          </UiButton>
        </div>
        <UiButton v-if="generationNextCursor" class="command-button secondary generation-load-more" variant="secondary" :loading="generationLoadingMore" @click="loadMoreGenerations">
          {{ t('actions.loadMore') }}
        </UiButton>
      </div>

      <div v-else-if="section === 'orders'" class="orders-workspace">
        <div v-if="success" class="task-feedback success" role="status">
          <FileCheck2 :size="18" />{{ success }}
        </div>
        <div v-if="error" class="task-feedback error" role="alert">
          <RefreshCw :size="18" />{{ error }}
        </div>
        <div class="order-list">
          <article v-for="order in orders" :key="order.id" class="order-row" :data-order-status="order.status">
            <header class="order-card-header">
              <div class="order-card-identity">
                <span class="task-status" :data-status="orderStatusTone(order.status)">{{ t(`marketplace.orderStatus.${order.status}`) }}</span>
                <RouterLink :to="`/market/assets/${order.productId}`">
                  <h2>{{ order.productTitle }}</h2>
                </RouterLink>
                <small>{{ t('workspace.orderReference') }} · {{ order.id }}</small>
              </div>
              <div class="order-card-amount">
                <strong>{{ formatCurrency(order.amountCents, order.currency, locale) }}</strong><span>{{ orderPaymentModeLabel(order) }}</span>
              </div>
            </header>
            <dl class="order-summary-grid">
              <div><dt>{{ t('marketplace.license') }}</dt><dd>{{ order.licenseName }} · v{{ order.licenseVersion }}</dd></div>
              <div><dt>{{ t('workspace.paymentMode') }}</dt><dd>{{ orderPaymentModeLabel(order) }}</dd></div>
              <div><dt>{{ t('workspace.placedAt') }}</dt><dd>{{ date(order.createdAt) }}</dd></div>
              <div><dt>{{ t('marketplace.refundWindow') }}</dt><dd>{{ t('marketplace.refundDays', { count: order.refundWindowDays }) }}</dd></div>
            </dl>
            <div class="order-evidence">
              <section class="order-license-panel">
                <h3><ShieldCheck :size="17" />{{ t('workspace.licenseSnapshot') }}</h3>
                <p>{{ order.licenseTerms }}</p>
                <small><FileCheck2 :size="14" />{{ licenseLabel(order.licenseCode) }} · v{{ order.licenseVersion }}</small>
              </section>
              <section class="order-history-panel">
                <h3><ReceiptText :size="17" />{{ t('workspace.eventHistory') }}</h3>
                <ol>
                  <li v-for="event in order.events" :key="`${event.toStatus}-${event.createdAt}`">
                    <span><PackageCheck :size="15" /></span><div>
                      <strong>{{ orderEventLabel(event.toStatus) }}</strong><small>{{ date(event.createdAt) }}</small><p v-if="event.toStatus === 'refund_requested'">
                        {{ event.reason }}
                      </p>
                    </div>
                  </li>
                </ol>
              </section>
            </div>
            <form v-if="order.status === 'fulfilled'" class="refund-form" @submit.prevent="requestRefund(order)">
              <label>{{ t('workspace.refundReason') }}<UiTextarea v-model="refundReasons[order.id]" rows="2" minlength="10" maxlength="500" required :placeholder="t('workspace.refundPlaceholder')" /></label><UiButton class="command-button secondary" variant="secondary" type="submit" :loading="refunding === order.id">
                <template #start>
                  <RotateCcw v-if="refunding !== order.id" :size="17" />
                </template>{{ t(order.paymentMode === 'test' ? 'workspace.requestRefund' : 'workspace.requestProviderRefund') }}
              </UiButton>
            </form>
          </article>
          <div v-if="!orders.length" class="task-market-state task-market-empty">
            <span><ReceiptText :size="24" /></span><strong>{{ t('workspace.noOrdersTitle') }}</strong><p>{{ t('workspace.noOrders') }}</p>
          </div>
          <UiButton v-if="orderNextCursor" class="command-button secondary generation-load-more" variant="secondary" :loading="ordersLoadingMore" @click="loadMoreOrders">
            {{ t('actions.loadMore') }}
          </UiButton>
        </div>
      </div>

      <div v-else-if="section === 'billing' && billing && points" class="billing-dashboard">
        <header class="billing-account-overview" aria-labelledby="billing-dashboard-title">
          <div class="billing-account-heading">
            <div>
              <span class="wallet-kicker">{{ t('workspace.billing') }}</span>
              <h1 id="billing-dashboard-title">
                <span class="billing-greeting-mark" aria-hidden="true">👋</span>{{ t('workspace.billingGreeting', { name: session.user?.displayName || points.currentSubscription?.planName || t('workspace.noActiveSubscription') }) }}
              </h1>
              <p><ShieldCheck :size="15" />{{ t('workspace.billingAccountStatus') }}</p>
            </div>
          </div>
          <img class="billing-account-art" src="/billing/account-overview.png" alt="" aria-hidden="true" />
          <div class="billing-account-stats">
            <article>
              <span class="billing-stat-icon" data-tone="primary"><Coins :size="22" /></span>
              <div><span>{{ t('workspace.currentBalance') }}</span><strong>{{ points.account.availablePoints.toLocaleString(locale) }}</strong><small>{{ t('workspace.availablePoints') }}</small></div>
            </article>
            <article>
              <span class="billing-stat-icon" data-tone="success"><TrendingUp :size="22" /></span>
              <div><span>{{ t('workspace.totalUsage') }}</span><strong>{{ pointUsage.toLocaleString(locale) }}</strong><small>{{ t('workspace.lifetimeUsage') }}</small></div>
            </article>
            <article>
              <span class="billing-stat-icon" data-tone="wallet"><WalletCards :size="22" /></span>
              <div><span>{{ t('workspace.walletBalance') }}</span><strong>{{ formatCurrency(billing.account.availableCents, billing.account.currency, locale) }}</strong><small>{{ t('workspace.subscriptionPaymentBalance') }}</small></div>
            </article>
          </div>
        </header>

        <div v-if="success" class="task-feedback success" role="status">
          <FileCheck2 :size="18" />{{ success }}
        </div>

        <div class="billing-dashboard-columns">
          <div class="billing-dashboard-column">
            <section class="billing-dashboard-panel billing-usage-panel" aria-labelledby="billing-trend-title">
              <header class="billing-panel-heading">
                <div>
                  <h2 id="billing-trend-title">
                    {{ t('workspace.pointsBalanceTrend') }}
                  </h2><p>{{ t('workspace.trendBasedOnRecentEntries') }}</p>
                </div>
                <span class="billing-range-chip">{{ t('workspace.recentSevenDays') }}</span>
              </header>
              <div class="billing-trend-chart" role="img" :aria-label="t('workspace.pointsBalanceTrend')">
                <svg viewBox="0 0 700 184" preserveAspectRatio="none" aria-hidden="true">
                  <defs><linearGradient id="billing-chart-fill" x1="0" x2="0" y1="0" y2="1"><stop offset="0%" stop-color="currentColor" stop-opacity=".2" /><stop offset="100%" stop-color="currentColor" stop-opacity="0" /></linearGradient></defs>
                  <g class="billing-chart-grid">
                    <template v-for="line in pointTrendGeometry.grid" :key="line.y">
                      <line x1="54" x2="676" :y1="line.y" :y2="line.y" />
                      <text x="0" :y="line.y + 4">{{ line.value.toLocaleString(locale) }}</text>
                    </template>
                  </g>
                  <path class="billing-chart-area" :d="pointTrendGeometry.area" />
                  <polyline class="billing-chart-line" :points="pointTrendGeometry.line" />
                  <circle v-for="point in pointTrendGeometry.dots" :key="`${point.x}-${point.y}`" class="billing-chart-dot" :cx="point.x" :cy="point.y" r="4" />
                </svg>
                <div class="billing-chart-labels">
                  <span v-for="item in pointBalanceTrend" :key="item.label">{{ item.label }}</span>
                </div>
                <div class="billing-chart-current">
                  <strong>{{ points.account.balancePoints.toLocaleString(locale) }}</strong><span>{{ t('workspace.pointsUnit') }}</span>
                </div>
              </div>
              <div class="billing-usage-facts">
                <div><Coins :size="18" /><span>{{ t('workspace.pointsGranted', { count: (points.currentSubscription?.grantedPoints || 0).toLocaleString(locale) }) }}</span></div>
                <div><Clock3 :size="18" /><span>{{ t('workspace.reservedCredits') }} · {{ formatCurrency(billing.account.reservedCents, billing.account.currency, locale) }}</span></div>
                <div><ShieldCheck :size="18" /><span>{{ billingPaymentModeLabel }}</span></div>
              </div>
              <div class="billing-environment-note">
                <ShieldCheck :size="17" /><p>{{ billingPaymentSummary }}</p>
              </div>
            </section>

            <section class="billing-dashboard-panel billing-wallet-panel" aria-labelledby="billing-wallet-title">
              <div class="billing-wallet-visual">
                <header>
                  <span class="wallet-kicker">{{ t('workspace.walletSectionLabel') }}</span><h2 id="billing-wallet-title">
                    {{ t('workspace.addFunds') }}
                  </h2><p>{{ t('workspace.addFundsSummary') }}</p>
                </header>
                <div class="billing-wallet-balance">
                  <span>{{ t('workspace.availableCredits') }}</span><strong>{{ formatCurrency(billing.account.availableCents, billing.account.currency, locale) }}</strong><small>{{ billingPaymentModeLabel }}</small>
                </div>
              </div>
              <div class="billing-wallet-summary-details">
                <dl class="billing-wallet-details">
                  <div><dt>{{ t('workspace.totalBalance') }}</dt><dd>{{ formatCurrency(billing.account.balanceCents, billing.account.currency, locale) }}</dd></div>
                  <div><dt>{{ t('workspace.reservedCredits') }}</dt><dd>{{ formatCurrency(billing.account.reservedCents, billing.account.currency, locale) }}</dd></div>
                  <div><dt>{{ t('workspace.paymentMode') }}</dt><dd>{{ billingPaymentModeLabel }}</dd></div>
                </dl>
                <form v-if="paymentProvider?.enabled" class="billing-topup-form" @submit.prevent="topUpWallet">
                  <label><span>{{ t('workspace.topupAmount') }}</span><UiInput v-model.number="topupAmount" type="number" min="0.5" max="999999.99" step="0.01" /></label>
                  <UiButton class="command-button primary" variant="primary" type="submit" :loading="topupAction" :disabled="topupAction || topupAmount < 0.5">
                    <Coins :size="16" />{{ t('workspace.topUpWallet') }}
                  </UiButton>
                </form>

                <UiButton as="RouterLink" class="command-button secondary billing-support-link" variant="secondary" to="/support">
                  {{ t('workspace.contactSupportForCredits') }}<ArrowRight :size="16" />
                </UiButton>
              </div>
            </section>
          </div>

          <div class="billing-dashboard-column">
            <section class="billing-dashboard-panel billing-plans-panel" aria-labelledby="billing-plans-title">
              <header class="billing-panel-heading">
                <div>
                  <h2 id="billing-plans-title">
                    {{ t('workspace.subscription') }}
                  </h2><p>{{ t('workspace.subscriptionSummary') }}</p>
                </div><ShieldCheck :size="21" />
              </header>

              <div v-if="points.currentSubscription" class="billing-current-plan">
                <span class="plan-mark">{{ t('workspace.planMark') }}</span>
                <div><strong>{{ points.currentSubscription.planName }}</strong><small>{{ t('workspace.planRenewsOn', { date: date(points.currentSubscription.currentPeriodEnd) }) }}</small></div>
                <span class="billing-plan-status"><ShieldCheck :size="14" />{{ t('workspace.activePlan') }}</span>
                <ul>
                  <li><Coins :size="15" />{{ t('workspace.pointsGranted', { count: points.currentSubscription.grantedPoints.toLocaleString(locale) }) }}</li>
                  <li><ShieldCheck :size="15" />{{ t('workspace.planFeatureOne') }}</li>
                </ul>
              </div>
              <div class="billing-plan-list">
                <article v-for="plan in points.plans" :key="plan.id" :class="{ current: points.currentSubscription?.planId === plan.id }">
                  <div class="billing-plan-copy">
                    <strong>{{ plan.name }}</strong><small>{{ plan.description }}</small><span>{{ t('workspace.modelsIncluded', { count: plan.modelIds.length }) }}</span>
                  </div>
                  <div class="billing-plan-price">
                    <strong>{{ plan.includedPoints.toLocaleString(locale) }} {{ t('workspace.pointsUnit') }}</strong><small>{{ formatCurrency(plan.priceCents, plan.currency, locale) }} / {{ plan.billingPeriodDays }} {{ t('workspace.days') }}</small>
                  </div>
                  <UiButton class="command-button secondary" variant="secondary" :loading="subscriptionAction === plan.id" :disabled="!paymentProvider?.enabled || subscriptionAction !== '' || points.currentSubscription?.planId === plan.id" @click="purchasePlan(plan.id)">
                    <template #start>
                      <ShieldCheck v-if="points.currentSubscription?.planId === plan.id && subscriptionAction !== plan.id" :size="15" /><Ban v-else-if="!paymentProvider?.enabled" :size="15" /><Coins v-else-if="subscriptionAction !== plan.id" :size="15" />
                    </template>{{ points.currentSubscription?.planId === plan.id ? t('workspace.currentPlan') : paymentProvider?.enabled ? t('workspace.payAndChoosePlan') : t('workspace.paymentUnavailable') }}
                  </UiButton>
                </article>
              </div>
            </section>

            <section class="billing-dashboard-panel billing-point-ledger" aria-labelledby="billing-points-ledger-title">
              <header class="billing-ledger-heading">
                <div>
                  <h2 id="billing-points-ledger-title">
                    {{ t('workspace.pointStatement') }}
                  </h2><p>{{ t('workspace.recentPointEntries') }}</p>
                </div><span>{{ points.entries.length }} {{ t('workspace.entriesLoaded') }}</span>
              </header>
              <article v-for="entry in points.entries" :key="entry.id" class="billing-entry">
                <span :data-direction="entry.direction">{{ entry.direction === 'credit' ? '+' : '-' }}</span>
                <div><strong>{{ entry.description }}</strong><small>{{ date(entry.createdAt) }} · {{ t(`workspace.pointEntryTypes.${entry.entryType}`) }}</small></div>
                <strong>{{ entry.direction === 'credit' ? '+' : '-' }}{{ entry.amountPoints.toLocaleString(locale) }} {{ t('workspace.pointsUnit') }}</strong>
                <small>{{ entry.balanceAfterPoints.toLocaleString(locale) }}</small>
              </article>
            </section>
          </div>
        </div>

        <section class="billing-dashboard-panel billing-wallet-ledger" aria-labelledby="billing-wallet-ledger-title">
          <header class="billing-ledger-heading">
            <div>
              <span class="wallet-kicker">{{ t('workspace.walletSectionLabel') }}</span><h2 id="billing-wallet-ledger-title">
                {{ t('workspace.walletStatement') }}
              </h2>
            </div><span>{{ billing.entries.length }} {{ t('workspace.entriesLoaded') }}</span>
          </header>
          <form class="billing-filters" @submit.prevent="applyBillingFilters">
            <label><span>{{ t('workspace.billingDirection') }}</span><UiSelect v-model="billingDirection"><option value="">{{ t('workspace.allDirections') }}</option><option value="debit">{{ t('workspace.billingDirections.debit') }}</option><option value="credit">{{ t('workspace.billingDirections.credit') }}</option></UiSelect></label>
            <label><span>{{ t('workspace.billingEntryType') }}</span><UiSelect v-model="billingEntryType"><option value="">{{ t('workspace.allEntryTypes') }}</option><option v-for="entryType in billingEntryTypes" :key="entryType" :value="entryType">{{ t(`workspace.billingEntryTypes.${entryType}`) }}</option></UiSelect></label>
            <label><span>{{ t('workspace.dateFrom') }}</span><UiInput v-model="billingDateFrom" type="date" /></label>
            <label><span>{{ t('workspace.dateTo') }}</span><UiInput v-model="billingDateTo" type="date" /></label>
            <UiButton class="command-button secondary" variant="secondary" type="submit">
              <template #start>
                <ListFilter :size="16" />
              </template>{{ t('actions.applyFilters') }}
            </UiButton>
            <UiIconButton class="icon-button" :label="t('actions.clearFilters')" @click="clearBillingFilters">
              <X :size="16" />
            </UiIconButton>
          </form>
          <article v-for="entry in billing.entries" :key="entry.id" class="billing-entry">
            <span :data-direction="entry.direction">{{ entry.direction === 'credit' ? '+' : '-' }}</span>
            <div><strong>{{ entry.description }}</strong><small>{{ date(entry.createdAt) }} · {{ t(`workspace.billingEntryTypes.${entry.entryType}`) }}</small></div>
            <strong>{{ formatCurrency(entry.amountCents, entry.currency, locale) }}</strong>
            <small>{{ formatCurrency(entry.balanceAfterCents, entry.currency, locale) }}</small>
          </article>
          <div v-if="!billing.entries.length" class="workspace-empty">
            <WalletCards :size="22" /><h2>{{ t('workspace.noBillingEntries') }}</h2>
          </div>
          <UiButton v-if="billingNextCursor" class="command-button secondary billing-load-more" variant="secondary" :loading="billingLoadingMore" @click="loadMoreBilling">
            {{ t('actions.loadMore') }}
          </UiButton>
        </section>
      </div>

      <div v-else class="tasks-workspace">
        <div class="workspace-task-list">
          <RouterLink v-for="item in tasks" :key="item.id" class="workspace-task-card" :data-status="item.status" :data-type="item.deliverableType" :to="`/market/demands/${item.id}`">
            <div class="workspace-task-card-main">
              <span class="workspace-task-card-labels"><span class="task-type-badge"><ClipboardList :size="13" />{{ t(`tasks.types.${item.deliverableType}`) }}</span><span class="task-status" :data-status="item.status">{{ t(`tasks.status.${item.status}`) }}</span></span>
              <h2>{{ item.title }}</h2>
              <p>{{ item.summary }}</p>
              <span class="workspace-task-card-byline"><strong>{{ taskRoleLabel(item) }}</strong><span>@{{ item.client.handle }}</span><span v-if="item.assignee">{{ t('workspace.taskAssignee') }} · @{{ item.assignee.handle }}</span></span>
            </div>
            <dl class="workspace-task-card-facts">
              <div><dt><Coins :size="14" />{{ t('tasks.reward') }}</dt><dd>{{ formatCurrency(item.budgetCents, item.currency, locale) }}</dd></div>
              <div><dt><Clock3 :size="14" />{{ t('tasks.deadline') }}</dt><dd>{{ formatDateTime(item.deadline, locale, item.clientTimezone) }}</dd></div>
              <div><dt><ClipboardList :size="14" />{{ t('tasks.proposals') }}</dt><dd>{{ item.proposalCount }}</dd></div>
            </dl>
            <span class="workspace-task-card-action">{{ taskNextAction(item) }}<ArrowRight :size="16" /></span>
          </RouterLink>
          <div v-if="!tasks.length" class="task-market-state task-market-empty">
            <span><ClipboardList :size="24" /></span><strong>{{ t('workspace.noTasksTitle') }}</strong><p>{{ t('workspace.noTasks') }}</p>
          </div>
        </div>
      </div>
    </template>
    <AssetPublishDrawer :open="publishOpen" :asset-id="publishAssetID" @update:open="updatePublishOpen" />
  </section>
</template>
