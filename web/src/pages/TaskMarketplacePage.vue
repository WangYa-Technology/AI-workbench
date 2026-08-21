<script setup lang="ts">
import {
  AlertTriangle, ArrowLeft, BriefcaseBusiness, CalendarDays, Check, ChevronDown, ChevronRight,
  CircleDollarSign, Clock3, FileCheck2, Filter, Grid2X2, Image as ImageIcon, List, LoaderCircle, LogIn,
  MessageSquareText, Music2, Play, Plus, RefreshCw, Search, Shapes, ShieldCheck, Sparkles, UserPlus,
  UserRound, Video, WandSparkles, Workflow, X,
} from 'lucide-vue-next'
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import {
  api, messageFrom, type Asset, type TaskCreate, type TaskDetail, type TaskSummary,
} from '../api/client'
import { formatCurrency, formatDateTime } from '../lib/format'
import { useSessionStore } from '../stores/session'
import UiButton from '../components/ui/UiButton.vue'
import UiCheckbox from '../components/ui/UiCheckbox.vue'
import UiIconButton from '../components/ui/UiIconButton.vue'
import UiInput from '../components/ui/UiInput.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiTextarea from '../components/ui/UiTextarea.vue'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const tasks = ref<TaskSummary[]>([])
const catalogTasks = ref<TaskSummary[]>([])
const detail = ref<TaskDetail | null>(null)
const assets = ref<Asset[]>([])
const loading = ref(true)
const actionLoading = ref(false)
const error = ref('')
const success = ref('')
const createOpen = ref(false)
const taskTitleInput = ref<InstanceType<typeof globalThis.HTMLInputElement> | null>(null)
const taskCreateModal = ref<InstanceType<typeof globalThis.HTMLElement> | null>(null)
const proposalOpen = ref(false)
const disputeOpen = ref(false)
const cancelOpen = ref(false)
const paymentEnabled = ref(false)
const paymentLiveMode = ref(false)
const fundingPollAttempts = ref(0)
let fundingPollTimer: number | undefined

const search = ref(String(route.query.q || ''))
const deliverableType = ref(String(route.query.type || ''))
const view = ref(route.query.view === 'mine' ? 'mine' : 'available')
const status = ref(String(route.query.status ?? (view.value === 'mine' ? '' : 'open')))
const sort = ref(String(route.query.sort || 'newest'))
const layoutMode = ref<'list' | 'grid'>('list')
const taskID = computed(() => String(route.params.id || ''))
const isDetail = computed(() => Boolean(taskID.value))
const canPublishBrief = computed(() => Boolean(session.user && ['publisher', 'admin'].includes(session.user.role)))
const canPropose = computed(() => Boolean(session.user) && detail.value?.viewerRole === 'viewer' && detail.value.status === 'open' && !detail.value.proposals.length)
const fundingConfirmed = computed(() => Boolean(detail.value?.funding && ['paid', 'transfer_pending', 'transferred'].includes(detail.value.funding.status)))
const directFundingConfirmed = computed(() => fundingConfirmed.value && !detail.value?.funding?.proposalId)
const canClaim = computed(() => canPropose.value && detail.value?.allowDirectAccept && (!paymentEnabled.value || directFundingConfirmed.value))
const canFundDirect = computed(() => Boolean(
  paymentEnabled.value && detail.value?.viewerRole === 'client' && detail.value.status === 'open' && detail.value.allowDirectAccept
  && canStartFundingFor(),
))
const canDeliver = computed(() => detail.value?.viewerRole === 'assignee' && ['assigned', 'revision'].includes(detail.value.status))
const canReview = computed(() => detail.value?.viewerRole === 'client' && detail.value.status === 'submitted')
const canDispute = computed(() => detail.value && ['submitted', 'revision'].includes(detail.value.status) && ['client', 'assignee'].includes(detail.value.viewerRole))
const canCancel = computed(() => detail.value?.viewerRole === 'client' && detail.value.status === 'open')
const taskActionLabel = computed(() => {
  if (!session.user) return t('tasks.signInToRespond')
  if (detail.value?.viewerRole === 'client') return t('tasks.manageTask')
  if (detail.value?.viewerRole === 'assignee') return t('tasks.continueTask')
  return t('tasks.reviewOpportunity')
})
const minimumDeadline = computed(() => {
  const date = new Date(Date.now() + 60 * 60 * 1000)
  date.setMinutes(date.getMinutes() - date.getTimezoneOffset())
  return date.toISOString().slice(0, 16)
})

const proposal = reactive({ approach: '', deliverables: '', amount: '', timelineDays: '7' })
const delivery = reactive({ assetId: String(route.query.assetId || ''), note: '' })
const reviewNote = ref('')
const disputeReason = ref('')
const cancelReason = ref('')
const draft = reactive({
  title: '', summary: '', brief: '', deliverableType: 'image', budget: '', deadline: '', timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC',
  deliverables: '', acceptanceRules: '', rightsTerms: '', aiDisclosureRequirement: '', allowDirectAccept: false,
})

const types = ['image', 'video', 'audio', 'prompt', 'workflow', 'mixed']
const statuses = ['open', 'assigned', 'submitted', 'revision', 'accepted', 'disputed', 'cancelled']
const openTaskCount = computed(() => catalogTasks.value.filter((item) => item.status === 'open').length)
const totalTaskReward = computed(() => catalogTasks.value.reduce((total, item) => total + item.budgetCents, 0))
const totalProposalCount = computed(() => catalogTasks.value.reduce((total, item) => total + item.proposalCount, 0))
const taskTypeCounts = computed(() => Object.fromEntries(types.map((type) => [
  type,
  catalogTasks.value.filter((item) => item.deliverableType === type).length,
])))
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

function taskThumbnail(kind: string) {
  if (kind === 'video') return '/tasks/task-video.webp'
  if (kind === 'audio') return '/tasks/task-audio.webp'
  if (kind === 'image') return '/tasks/task-image.webp'
  return '/tasks/task-hero-transparent.webp'
}

async function load() {
  loading.value = true
  error.value = ''
  success.value = ''
  try {
    const [, runtime] = await Promise.all([session.ensure(), api.meta()])
    paymentEnabled.value = runtime.paymentProvider.enabled
    paymentLiveMode.value = runtime.paymentProvider.liveMode
    if (taskID.value) {
      detail.value = await api.getTask(taskID.value)
      if (detail.value.viewerRole === 'assignee') {
        const response = await api.listAssets()
        assets.value = response.items.filter((item) => item.scanStatus === 'clean')
        if (!delivery.assetId && assets.value.length) delivery.assetId = assets.value[0].id
      }
      applyPaymentReturnState()
    } else {
      const [response, catalog] = await Promise.all([
        api.listTasks({
          q: search.value,
          type: deliverableType.value,
          status: status.value,
          sort: sort.value,
          mine: view.value === 'mine',
        }),
        api.listTasks({
          status: view.value === 'mine' ? '' : 'open',
          sort: 'newest',
          mine: view.value === 'mine',
        }),
      ])
      tasks.value = response.items
      catalogTasks.value = catalog.items
      detail.value = null
    }
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    loading.value = false
  }
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
  return Boolean(paymentEnabled.value && detail.value?.viewerRole === 'client' && detail.value.status === 'open' && canStartFundingFor(proposalId))
}

function canAcceptProposal(proposalId: string) {
  if (detail.value?.viewerRole !== 'client' || detail.value.status !== 'open') return false
  return !paymentEnabled.value || (fundingConfirmed.value && fundingMatches(proposalId))
}

function fundingActionLabel(proposalId?: string) {
  if (fundingMatches(proposalId) && detail.value?.funding?.status === 'checkout_open') return t('tasks.resumeFunding')
  if (fundingMatches(proposalId) && detail.value?.funding?.status === 'checkout_pending') return t('tasks.retryFunding')
  return proposalId ? t('tasks.fundProposal') : t('tasks.fundTask')
}

function fundingRequestKey(proposalId?: string) {
  const base = `task-funding-${detail.value?.id}-${proposalId || 'direct'}`
  if (detail.value?.funding && ['payment_failed', 'cancelled', 'refunded'].includes(detail.value.funding.status)) {
    return `${base}-${globalThis.crypto.randomUUID().slice(0, 8)}`
  }
  return base
}

async function fundTask(proposalId?: string) {
  if (!detail.value) return
  const current = detail.value.funding
  if (fundingMatches(proposalId) && current?.status === 'checkout_open' && current.checkoutUrl) {
    globalThis.location.assign(current.checkoutUrl)
    return
  }
  actionLoading.value = true
  error.value = ''
  try {
    const checkout = await api.checkoutTask(
      detail.value.id,
      proposalId ? { proposalId } : {},
      fundingRequestKey(proposalId),
    )
    globalThis.location.assign(checkout.checkoutUrl)
  } catch (reason) {
    error.value = messageFrom(reason)
    detail.value = await api.getTask(detail.value.id)
  } finally {
    actionLoading.value = false
  }
}

function applyPaymentReturnState() {
  if (!detail.value || !paymentEnabled.value) return
  const paymentReturn = String(route.query.payment || '')
  if (paymentReturn === 'cancelled') {
    success.value = t('tasks.fundingCheckoutCancelled')
    return
  }
  if (paymentReturn !== 'success') return
  if (fundingConfirmed.value) {
    success.value = t('tasks.fundingConfirmed')
    return
  }
  success.value = t('tasks.fundingAwaitingConfirmation')
  if (fundingPollAttempts.value >= 5 || fundingPollTimer !== undefined) return
  fundingPollTimer = globalThis.window.setTimeout(async () => {
    fundingPollTimer = undefined
    fundingPollAttempts.value += 1
    try {
      detail.value = await api.getTask(taskID.value)
      applyPaymentReturnState()
    } catch (reason) {
      error.value = messageFrom(reason)
    }
  }, 1500)
}

async function applyFilters() {
  await router.push({ path: '/market/demands', query: {
    ...(view.value === 'mine' ? { view: 'mine' } : {}),
    ...(search.value.trim() ? { q: search.value.trim() } : {}),
    ...(deliverableType.value ? { type: deliverableType.value } : {}),
    ...(status.value ? { status: status.value } : {}),
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

async function clearFilters() {
  search.value = ''
  deliverableType.value = ''
  status.value = view.value === 'mine' ? '' : 'open'
  sort.value = 'newest'
  await applyFilters()
}

async function mutate(action: () => Promise<TaskDetail>, message: string) {
  actionLoading.value = true
  error.value = ''
  success.value = ''
  try {
    detail.value = await action()
    success.value = message
    proposalOpen.value = false
    disputeOpen.value = false
    cancelOpen.value = false
    if (detail.value.viewerRole === 'assignee') {
      const response = await api.listAssets()
      assets.value = response.items.filter((item) => item.scanStatus === 'clean')
      if (!delivery.assetId && assets.value.length) delivery.assetId = assets.value[0].id
    }
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionLoading.value = false
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
  if (!detail.value) return
  await mutate(() => api.claimTask(detail.value!.id), t('tasks.assigned'))
}

async function acceptProposal(proposalId: string) {
  if (!detail.value) return
  await mutate(() => api.acceptTaskProposal(detail.value!.id, proposalId), t('tasks.assigned'))
}

async function submitDelivery() {
  if (!detail.value) return
  await mutate(() => api.deliverTask(detail.value!.id, { assetId: delivery.assetId, note: delivery.note.trim() }), t('tasks.delivered'))
}

async function review(decision: 'accept' | 'request_revision') {
  if (!detail.value) return
  await mutate(
    () => api.reviewTask(detail.value!.id, { decision, note: reviewNote.value.trim() }),
    decision === 'accept' ? t(paymentEnabled.value ? 'tasks.acceptedProvider' : 'tasks.accepted') : t('tasks.revisionSent'),
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

async function switchActor(actor: 'creator' | 'publisher') {
  actionLoading.value = true
  const user = await session.switchDemoActor(actor)
  actionLoading.value = false
  if (user) await load()
}

async function publishTask() {
  const input: TaskCreate = {
    title: draft.title.trim(), summary: draft.summary.trim(), brief: draft.brief.trim(),
    deliverableType: draft.deliverableType as TaskCreate['deliverableType'], deliverables: lines(draft.deliverables),
    acceptanceRules: lines(draft.acceptanceRules), rightsTerms: draft.rightsTerms.trim(),
    aiDisclosureRequirement: draft.aiDisclosureRequirement.trim(), budgetCents: Math.round(Number(draft.budget) * 100),
    currency: 'USD', deadline: new Date(draft.deadline).toISOString(), clientTimezone: draft.timezone.trim(),
    allowDirectAccept: draft.allowDirectAccept,
  }
  actionLoading.value = true
  error.value = ''
  try {
    const created = await api.createTask(input)
    createOpen.value = false
    await router.push(`/market/demands/${created.id}`)
    success.value = t('tasks.created')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionLoading.value = false
  }
}

watch(() => route.fullPath, () => {
  if (fundingPollTimer !== undefined) globalThis.window.clearTimeout(fundingPollTimer)
  fundingPollTimer = undefined
  fundingPollAttempts.value = 0
  search.value = String(route.query.q || '')
  deliverableType.value = String(route.query.type || '')
  view.value = route.query.view === 'mine' ? 'mine' : 'available'
  status.value = String(route.query.status ?? (view.value === 'mine' ? '' : 'open'))
  sort.value = String(route.query.sort || 'newest')
  delivery.assetId = String(route.query.assetId || delivery.assetId || '')
  void load()
})

watch(createOpen, async (open) => {
  if (open) {
    createTrigger = globalThis.document.activeElement instanceof globalThis.HTMLElement ? globalThis.document.activeElement : null
    await nextTick()
    taskTitleInput.value?.focus()
    return
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
  void load()
})
onBeforeUnmount(() => {
  globalThis.window.removeEventListener('keydown', handleWindowKeydown)
  if (fundingPollTimer !== undefined) globalThis.window.clearTimeout(fundingPollTimer)
})
</script>

<template>
  <section class="task-market content-width" :class="{ 'is-detail': isDetail }">
    <template v-if="!isDetail">
      <header class="page-hero-header page-hero-banner task-market-header">
        <div class="page-hero-copy">
          <span class="page-hero-eyebrow"><ShieldCheck :size="14" />{{ paymentEnabled ? t(paymentLiveMode ? 'tasks.providerLiveShort' : 'tasks.providerTestShort') : t('tasks.localTestShort') }}</span>
          <h1>{{ t('tasks.title') }}</h1>
          <p>{{ t('tasks.summary') }}</p>
          <div class="page-hero-stats" :aria-label="t('tasks.taskStats')">
            <article><span class="page-hero-stat-icon" data-tone="blue"><BriefcaseBusiness :size="23" /></span><div><strong>{{ openTaskCount }}</strong><span>{{ t('tasks.openTasks') }}</span></div></article>
            <article><span class="page-hero-stat-icon" data-tone="violet"><CircleDollarSign :size="23" /></span><div><strong>{{ money(totalTaskReward) }}</strong><span>{{ t('tasks.totalReward') }}</span></div></article>
            <article><span class="page-hero-stat-icon" data-tone="green"><UserRound :size="23" /></span><div><strong>{{ totalProposalCount }}</strong><span>{{ t('tasks.totalProposals') }}</span></div></article>
          </div>
        </div>
        <div class="page-hero-actions">
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
          <UiButton v-if="!session.user" as="RouterLink" class="command-button secondary" variant="secondary" :to="{ path: '/settings', query: { auth: 'login', returnTo: route.fullPath } }">
            <template #start>
              <LogIn :size="17" />
            </template>{{ t('account.signIn') }}
          </UiButton>
          <UiButton v-if="!session.user" as="RouterLink" class="command-button primary" variant="primary" :to="{ path: '/settings', query: { auth: 'register', returnTo: route.fullPath } }">
            <template #start>
              <UserPlus :size="17" />
            </template>{{ t('account.createAccount') }}
          </UiButton>
        </div>
        <img class="page-hero-art task-market-hero-art" src="/tasks/task-hero-transparent.webp" alt="" aria-hidden="true" />
        <div v-if="session.user" class="page-hero-account">
          <span class="page-hero-avatar"><UserRound :size="22" /></span><span><strong>{{ session.user.displayName }}</strong><small>@{{ session.user.handle }}</small></span><ShieldCheck :size="15" />
        </div>
      </header>

      <div v-if="session.user" class="view-switcher-bar">
        <div v-motion-tabs class="view-switcher t-tabs" role="tablist" :aria-label="t('tasks.views')">
          <span class="t-tabs-pill" aria-hidden="true"></span>
          <button class="t-tab" type="button" role="tab" :aria-selected="view === 'available'" :class="{ active: view === 'available' }" @click="selectView('available')">
            <Search :size="16" />{{ t('tasks.availableWork') }}
          </button>
          <button class="t-tab" type="button" role="tab" :aria-selected="view === 'mine'" :class="{ active: view === 'mine' }" @click="selectView('mine')">
            <BriefcaseBusiness :size="16" />{{ t('tasks.myActivity') }}
          </button>
        </div>
      </div>

      <form class="task-filters" role="search" @submit.prevent="applyFilters">
        <label class="task-search"><span class="sr-only">{{ t('actions.search') }}</span><Search :size="17" /><UiInput v-model="search" type="search" :placeholder="t('tasks.searchPlaceholder')" /></label>
        <label class="task-filter-control">
          <span class="task-filter-display" aria-hidden="true"><Filter :size="15" /><span>{{ deliverableType ? t(`tasks.types.${deliverableType}`) : t('tasks.allTypes') }}</span><ChevronDown :size="14" /></span>
          <UiSelect v-model="deliverableType" :aria-label="t('tasks.allTypes')" @change="applyFilters"><option value="">{{ t('tasks.allTypes') }}</option><option v-for="item in types" :key="item" :value="item">{{ t(`tasks.types.${item}`) }}</option></UiSelect>
        </label>
        <label class="task-filter-control">
          <span class="task-filter-display" aria-hidden="true"><span>{{ status ? t(`tasks.status.${status}`) : t('tasks.allStatuses') }}</span><ChevronDown :size="14" /></span>
          <UiSelect v-model="status" :aria-label="t('tasks.allStatuses')" @change="applyFilters"><option value="">{{ t('tasks.allStatuses') }}</option><option v-for="item in statuses" :key="item" :value="item">{{ t(`tasks.status.${item}`) }}</option></UiSelect>
        </label>
        <label class="task-filter-control">
          <span class="task-filter-display" aria-hidden="true"><span>{{ sort === 'deadline' ? t('tasks.sortDeadline') : sort === 'budget_desc' ? t('tasks.sortBudget') : t('tasks.sortNewest') }}</span><ChevronDown :size="14" /></span>
          <UiSelect v-model="sort" :aria-label="t('tasks.sortNewest')" @change="applyFilters"><option value="newest">{{ t('tasks.sortNewest') }}</option><option value="deadline">{{ t('tasks.sortDeadline') }}</option><option value="budget_desc">{{ t('tasks.sortBudget') }}</option></UiSelect>
        </label>
        <UiButton class="command-button primary task-filter-submit" variant="primary" type="submit">
          <template #start>
            <Search :size="17" />
          </template>{{ t('actions.search') }}
        </UiButton>
      </form>

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
      <div v-else class="task-browser-layout">
        <aside class="task-category-panel" :aria-label="t('tasks.categories')">
          <h2>{{ t('tasks.categories') }}</h2>
          <nav>
            <UiButton variant="ghost" type="button" :class="{ active: !deliverableType }" @click="selectTaskType('')">
              <BriefcaseBusiness :size="17" /><span>{{ t('tasks.allTasks') }}</span><small>{{ catalogTasks.length }}</small>
            </UiButton>
            <UiButton v-for="type in types" :key="type" variant="ghost" type="button" :class="{ active: deliverableType === type }" @click="selectTaskType(type)">
              <component :is="taskTypeIcon(type)" :size="17" /><span>{{ t(`tasks.types.${type}`) }}</span><small>{{ taskTypeCounts[type] || 0 }}</small>
            </UiButton>
          </nav>
          <section class="task-creator-program">
            <span><Sparkles :size="20" /></span><div><strong>{{ t('tasks.creatorProgram') }}</strong><p>{{ t('tasks.creatorProgramSummary') }}</p></div><RouterLink class="text-link" to="/settings">
              {{ t('tasks.learnMore') }}<ChevronRight :size="15" />
            </RouterLink>
          </section>
        </aside>

        <div class="task-results">
          <div class="task-results-meta">
            <div><strong>{{ tasks.length }} {{ t('tasks.results') }}</strong><span>{{ t(view === 'mine' ? 'tasks.myActivitySummary' : 'tasks.availableWorkSummary') }}</span></div>
            <div class="task-results-actions">
              <UiButton v-if="search || deliverableType || status !== (view === 'mine' ? '' : 'open') || sort !== 'newest'" class="text-link" variant="ghost" size="sm" @click="clearFilters">
                {{ t('tasks.clearFilters') }}
              </UiButton>
              <div class="task-layout-switcher" :aria-label="t('tasks.layout')">
                <UiIconButton size="sm" class="icon-button" variant="ghost" :class="{ active: layoutMode === 'list' }" :label="t('tasks.listView')" @click="layoutMode = 'list'">
                  <List :size="17" />
                </UiIconButton>
                <UiIconButton size="sm" class="icon-button" variant="ghost" :class="{ active: layoutMode === 'grid' }" :label="t('tasks.gridView')" @click="layoutMode = 'grid'">
                  <Grid2X2 :size="16" />
                </UiIconButton>
              </div>
            </div>
          </div>
          <div class="task-result-list" :class="{ 'is-grid': layoutMode === 'grid' }">
            <RouterLink v-for="item in tasks" :key="item.id" class="task-row" :class="{ 'is-direct': item.allowDirectAccept && item.status === 'open' }" :data-type="item.deliverableType" :to="`/market/demands/${item.id}`">
              <span class="task-row-media"><img :src="taskThumbnail(item.deliverableType)" :alt="item.title" /><span v-if="item.deliverableType === 'video'" class="task-media-play"><Play :size="18" fill="currentColor" /></span></span>
              <span class="task-row-copy">
                <span class="task-row-heading"><span class="task-type-badge"><component :is="taskTypeIcon(item.deliverableType)" :size="13" />{{ t(`tasks.types.${item.deliverableType}`) }}</span><span class="task-status" :data-status="item.status">{{ t(`tasks.status.${item.status}`) }}</span></span>
                <strong>{{ item.title }}</strong><small>{{ item.summary }}</small>
                <span class="task-row-byline"><span>@{{ item.client.handle }}</span><span>{{ item.proposalCount }} {{ t('tasks.proposalCount') }}</span><span v-if="item.allowDirectAccept && item.status === 'open'">{{ t('tasks.direct') }}</span></span>
              </span>
              <span class="task-row-commercial">
                <span class="task-row-data"><small>{{ paymentEnabled ? t('tasks.providerReward') : t('tasks.reward') }}</small><strong>{{ money(item.budgetCents, item.currency) }}</strong></span>
                <span class="task-row-data"><small><Clock3 :size="13" />{{ t('tasks.deadline') }}</small><strong>{{ date(item.deadline, item.clientTimezone) }}</strong></span>
                <span class="command-button primary task-row-action">{{ t('tasks.reviewBrief') }}<ChevronRight :size="16" /></span>
              </span>
            </RouterLink>
            <div v-if="!tasks.length" class="task-market-state task-market-empty">
              <span><component :is="view === 'mine' ? BriefcaseBusiness : Search" :size="20" /></span><strong>{{ t(view === 'mine' ? 'tasks.noMyActivity' : 'tasks.noResults') }}</strong><UiButton v-if="view === 'available'" class="text-link" variant="ghost" size="sm" @click="clearFilters">
                {{ t('tasks.clearFilters') }}
              </UiButton>
              <p>{{ t(view === 'mine' ? 'tasks.noMyActivitySummary' : 'tasks.emptySummary') }}</p>
              <div class="task-empty-actions">
                <UiButton v-if="view === 'mine'" class="command-button secondary" variant="secondary" @click="selectView('available')">
                  <template #start>
                    <Search :size="17" />
                  </template>{{ t('tasks.browseTasks') }}
                </UiButton>
                <UiButton v-else as="RouterLink" class="command-button secondary" variant="secondary" to="/create/image">
                  <template #start>
                    <WandSparkles :size="17" />
                  </template>{{ t('tasks.createInstead') }}
                </UiButton>
                <UiButton v-if="canPublishBrief" class="command-button primary" variant="primary" @click="createOpen = true">
                  <template #start>
                    <Plus :size="17" />
                  </template>{{ t('tasks.publishBrief') }}
                </UiButton>
              </div>
            </div>
          </div>
          <section v-if="canPublishBrief" class="task-publish-cta">
            <span><Sparkles :size="20" /></span><div><strong>{{ t('tasks.publishIdea') }}</strong><p>{{ t('tasks.publishIdeaSummary') }}</p></div><UiButton class="command-button primary" variant="primary" @click="createOpen = true">
              <template #start>
                <Plus :size="17" />
              </template>{{ t('tasks.publishBrief') }}
            </UiButton>
          </section>
        </div>
      </div>
    </template>

    <template v-else>
      <div class="task-detail-toolbar">
        <RouterLink class="text-link task-back" to="/market/demands">
          <ArrowLeft :size="17" />{{ t('tasks.back') }}
        </RouterLink>
        <div v-if="detail" class="task-detail-toolbar-actions">
          <UiButton as="RouterLink" class="command-button primary" variant="primary" :to="`/create/image?taskId=${detail.id}`">
            <template #start>
              <WandSparkles :size="17" />
            </template>{{ t('tasks.startCreating') }}
          </UiButton>
          <UiButton as="RouterLink" class="command-button secondary" variant="secondary" to="/market/demands">
            <template #start>
              <BriefcaseBusiness :size="17" />
            </template>{{ t('tasks.findTasks') }}
          </UiButton>
        </div>
      </div>
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
        <article class="task-brief">
          <header class="task-brief-header">
            <div class="task-brief-signals">
              <span class="task-status" :data-status="detail.status">{{ t(`tasks.status.${detail.status}`) }}</span><span v-if="detail.allowDirectAccept && detail.status === 'open' && (!paymentEnabled || directFundingConfirmed || detail.viewerRole === 'client')" class="task-direct-signal"><BriefcaseBusiness :size="14" />{{ t(paymentEnabled && !directFundingConfirmed ? 'tasks.directFundingRequired' : 'tasks.direct') }}</span>
            </div><h1>{{ detail.title }}</h1><p>{{ detail.summary }}</p>
          </header>
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
            <div><ShieldCheck :size="18" /><h2>{{ t('tasks.rights') }}</h2></div><p>{{ detail.rightsTerms }}</p>
          </section>
          <section class="task-trust-block">
            <div><WandSparkles :size="18" /><h2>{{ t('tasks.disclosure') }}</h2></div><p>{{ detail.aiDisclosureRequirement }}</p>
          </section>
          <a class="command-button primary task-mobile-action" href="#task-actions">
            {{ taskActionLabel }}<ChevronRight :size="17" />
          </a>

          <section v-if="detail.proposals.length" class="task-proposals">
            <h2>{{ detail.viewerRole === 'client' ? t('tasks.proposals') : t('tasks.yourProposal') }}</h2><article v-for="item in detail.proposals" :key="item.id">
              <div><strong>{{ item.creator.displayName }}</strong><span>@{{ item.creator.handle }}</span></div><p>{{ item.approach }}</p><p>{{ item.deliverables }}</p><dl><div><dt>{{ paymentEnabled ? t('tasks.providerReward') : t('tasks.reward') }}</dt><dd>{{ money(item.amountCents) }}</dd></div><div><dt>{{ t('tasks.timelineDays') }}</dt><dd>{{ item.timelineDays }}</dd></div></dl><div v-if="item.status === 'submitted' && detail.status === 'open' && detail.viewerRole === 'client'" class="task-proposal-actions">
                <UiButton v-if="canFundProposal(item.id)" class="command-button secondary" variant="secondary" :loading="actionLoading" @click="fundTask(item.id)">
                  <template #start>
                    <CircleDollarSign v-if="!actionLoading" :size="17" />
                  </template>{{ fundingActionLabel(item.id) }}
                </UiButton><UiButton v-if="canAcceptProposal(item.id)" class="command-button primary" variant="primary" :loading="actionLoading" @click="acceptProposal(item.id)">
                  {{ t('tasks.acceptProposal') }}
                </UiButton>
              </div><span v-else class="task-status" :data-status="item.status">{{ t(`tasks.proposalStatus.${item.status}`) }}</span>
            </article>
          </section>

          <section v-if="detail.deliveries.length" class="task-deliveries">
            <h2>{{ t('tasks.submitDelivery') }}</h2><article v-for="item in detail.deliveries" :key="item.id">
              <img :src="item.mediaUrl" :alt="item.assetTitle" width="320" height="240" /><div>
                <span>{{ item.assetTitle }} / v{{ item.version }}</span><strong>{{ item.note }}</strong><p v-if="item.reviewNote">
                  {{ item.reviewNote }}
                </p><small>{{ date(item.createdAt) }}</small>
              </div><span class="task-status" :data-status="item.status">{{ t(`tasks.deliveryStatus.${item.status}`) }}</span>
            </article>
          </section>

          <section class="task-history">
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
          <div class="task-reward">
            <div><span>{{ paymentEnabled ? t('tasks.providerReward') : t('tasks.reward') }}</span><strong>{{ money(detail.budgetCents, detail.currency) }}</strong></div>
            <img src="/tasks/task-reward.webp" alt="" aria-hidden="true" />
            <p><ShieldCheck :size="16" /><span>{{ paymentEnabled ? t(paymentLiveMode ? 'tasks.providerLive' : 'tasks.providerTest') : t('tasks.localTest') }}</span></p>
          </div>
          <dl class="task-facts">
            <div><dt><UserRound :size="16" />{{ t('tasks.commissioner') }}</dt><dd>{{ detail.client.displayName }}<small>@{{ detail.client.handle }}</small></dd></div><div><dt><CalendarDays :size="16" />{{ t('tasks.deadline') }}</dt><dd>{{ date(detail.deadline, detail.clientTimezone) }}<small>{{ detail.clientTimezone }}</small></dd></div><div v-if="detail.assignee">
              <dt><BriefcaseBusiness :size="16" />{{ t('tasks.selectedCreator') }}</dt><dd>{{ detail.assignee.displayName }}<small>@{{ detail.assignee.handle }}</small></dd>
            </div>
          </dl>

          <div v-if="!session.user" class="market-auth-prompt">
            <div><h2>{{ t('tasks.guestTitle') }}</h2><p>{{ t('tasks.guestSummary') }}</p></div>
            <UiButton as="RouterLink" class="command-button primary wide" variant="primary" :to="{ path: '/settings', query: { auth: 'login', returnTo: route.fullPath } }">
              <template #start>
                <LogIn :size="17" />
              </template>{{ t('account.signIn') }}
            </UiButton>
            <UiButton as="RouterLink" class="text-link" variant="ghost" size="sm" :to="{ path: '/settings', query: { auth: 'register', returnTo: route.fullPath } }">
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

          <UiButton v-if="canFundDirect" class="command-button primary wide" variant="primary" :loading="actionLoading" @click="fundTask()">
            <template #start>
              <CircleDollarSign v-if="!actionLoading" :size="17" />
            </template>{{ fundingActionLabel() }}
          </UiButton>

          <UiButton v-if="canClaim" class="command-button primary wide" variant="primary" :loading="actionLoading" @click="claimTask">
            <template #start>
              <BriefcaseBusiness v-if="!actionLoading" :size="17" />
            </template>{{ t('tasks.acceptTask') }}
          </UiButton>
          <UiButton v-if="canPropose && !proposalOpen" class="command-button primary wide" variant="primary" @click="showProposalForm">
            <template #start>
              <Plus :size="17" />
            </template>{{ t('tasks.submitProposal') }}
          </UiButton>
          <form v-if="proposalOpen" class="task-action-form" @submit.prevent="submitProposal">
            <label>{{ t('tasks.proposalApproach') }}<UiTextarea v-model="proposal.approach" rows="5" minlength="20" required /></label><label>{{ t('tasks.proposalDeliverables') }}<UiTextarea v-model="proposal.deliverables" rows="3" minlength="10" required /></label><div class="form-pair">
              <label>{{ t('tasks.proposedAmount') }}<UiInput v-model="proposal.amount" type="number" min="1" step="1" required /></label><label>{{ t('tasks.timelineDays') }}<UiInput v-model="proposal.timelineDays" type="number" min="1" required /></label>
            </div><UiButton class="command-button primary wide" variant="primary" type="submit" :loading="actionLoading">
              {{ t('tasks.submitProposal') }}
            </UiButton>
          </form>

          <template v-if="canDeliver">
            <UiButton as="RouterLink" class="command-button primary wide" variant="primary" :to="`/create/image?taskId=${detail.id}`">
              <template #start>
                <WandSparkles :size="17" />
              </template>{{ t('tasks.createForTask') }}
            </UiButton><form class="task-action-form" @submit.prevent="submitDelivery">
              <label>{{ t('tasks.asset') }}<UiSelect v-model="delivery.assetId" required><option v-for="asset in assets" :key="asset.id" :value="asset.id">{{ asset.title }}</option></UiSelect></label><label>{{ t('tasks.deliveryNote') }}<UiTextarea v-model="delivery.note" rows="4" minlength="5" required /></label><UiButton class="command-button secondary wide" variant="secondary" type="submit" :loading="actionLoading" :disabled="!assets.length">
                <template #start>
                  <FileCheck2 v-if="!actionLoading" :size="17" />
                </template>{{ t('tasks.submitDelivery') }}
              </UiButton>
            </form><p v-if="!assets.length" class="task-inline-help">
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

          <UiButton v-if="canDispute && !disputeOpen" class="text-link danger-link" variant="ghost" size="sm" @click="disputeOpen = true">
            {{ t('tasks.openDispute') }}
          </UiButton>
          <form v-if="disputeOpen" class="task-action-form" @submit.prevent="openDispute">
            <label>{{ t('tasks.disputeReason') }}<UiTextarea v-model="disputeReason" rows="4" minlength="20" required /></label><UiButton class="command-button secondary wide" variant="secondary" type="submit" :loading="actionLoading">
              {{ t('tasks.openDispute') }}
            </UiButton>
          </form>
          <UiButton v-if="canCancel && !cancelOpen" class="text-link danger-link" variant="ghost" size="sm" @click="cancelOpen = true">
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
          <div v-if="detail.settlement" class="task-settlement">
            <CircleDollarSign :size="19" /><div><strong>{{ t('tasks.settlement') }}</strong><span>{{ money(detail.settlement.amountCents, detail.settlement.currency) }} / {{ t(`tasks.settlementMode.${detail.settlement.mode}`) }}</span></div>
          </div><p v-else class="task-payment-note">
            <ShieldCheck :size="17" /><span>{{ t(paymentEnabled ? 'tasks.providerUnsettled' : 'tasks.unsettled') }}</span>
          </p>

          <div v-if="session.user" class="demo-actor-switch">
            <span>{{ t('tasks.accountMode') }}: <strong>{{ session.user?.displayName }}</strong></span><UiButton variant="ghost" size="sm" :loading="actionLoading" @click="switchActor(detail.viewerRole === 'client' ? 'creator' : 'publisher')">
              {{ detail.viewerRole === 'client' ? t('tasks.switchCreator') : t('tasks.switchPublisher') }}
            </UiButton>
          </div>
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
              <label>{{ t('tasks.typeLabel') }}<UiSelect v-model="draft.deliverableType"><option v-for="item in types" :key="item" :value="item">{{ t(`tasks.types.${item}`) }}</option></UiSelect></label><label>{{ t('tasks.budgetLabel') }}<UiInput v-model="draft.budget" type="number" min="1" step="1" required /></label>
            </div><div class="form-pair">
              <label>{{ t('tasks.deadlineLabel') }}<UiInput v-model="draft.deadline" type="datetime-local" :min="minimumDeadline" required /></label><label>{{ t('tasks.timezoneLabel') }}<UiSelect v-model="draft.timezone" :aria-label="t('tasks.timezoneLabel')" required><option v-for="item in timezoneOptions" :key="item" :value="item">{{ item }}</option></UiSelect>
              </label>
            </div><label class="check-label"><UiCheckbox v-model="draft.allowDirectAccept" />{{ t('tasks.directLabel') }}</label>
          </fieldset>
          <fieldset class="task-form-section">
            <legend>{{ t('tasks.deliverySection') }}</legend><label>{{ t('tasks.deliverablesLabel') }}<UiTextarea v-model="draft.deliverables" rows="3" required /></label><label>{{ t('tasks.acceptanceLabel') }}<UiTextarea v-model="draft.acceptanceRules" rows="3" required /></label><label>{{ t('tasks.rightsLabel') }}<UiTextarea v-model="draft.rightsTerms" rows="3" required /></label><label>{{ t('tasks.disclosureLabel') }}<UiTextarea v-model="draft.aiDisclosureRequirement" rows="3" required /></label>
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
