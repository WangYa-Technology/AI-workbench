<script setup lang="ts">
import {
  AlertTriangle, ArrowLeft, BriefcaseBusiness, CalendarDays, Check, ChevronRight,
  CircleDollarSign, Clock3, FileCheck2, Filter, LoaderCircle, LogIn, Plus, RefreshCw, Search, ShieldCheck, UserPlus, UserRound, WandSparkles, X,
} from 'lucide-vue-next'
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import {
  api, messageFrom, type Asset, type TaskCreate, type TaskDetail, type TaskSummary,
} from '../api/client'
import { formatCurrency, formatDateTime } from '../lib/format'
import { useSessionStore } from '../stores/session'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const tasks = ref<TaskSummary[]>([])
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

const search = ref(String(route.query.q || ''))
const deliverableType = ref(String(route.query.type || ''))
const status = ref(String(route.query.status ?? 'open'))
const sort = ref(String(route.query.sort || 'newest'))
const taskID = computed(() => String(route.params.id || ''))
const isDetail = computed(() => Boolean(taskID.value))
const canPropose = computed(() => Boolean(session.user) && detail.value?.viewerRole === 'viewer' && detail.value.status === 'open' && !detail.value.proposals.length)
const canClaim = computed(() => canPropose.value && detail.value?.allowDirectAccept)
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

async function load() {
  loading.value = true
  error.value = ''
  success.value = ''
  try {
    await session.ensure()
    if (taskID.value) {
      detail.value = await api.getTask(taskID.value)
      if (detail.value.viewerRole === 'assignee') {
        const response = await api.listAssets()
        assets.value = response.items.filter((item) => item.scanStatus === 'clean')
        if (!delivery.assetId && assets.value.length) delivery.assetId = assets.value[0].id
      }
    } else {
      const response = await api.listTasks({ q: search.value, type: deliverableType.value, status: status.value, sort: sort.value })
      tasks.value = response.items
      detail.value = null
    }
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    loading.value = false
  }
}

async function applyFilters() {
  await router.push({ path: '/market/demands', query: {
    ...(search.value.trim() ? { q: search.value.trim() } : {}),
    ...(deliverableType.value ? { type: deliverableType.value } : {}),
    ...(status.value ? { status: status.value } : {}),
    ...(sort.value !== 'newest' ? { sort: sort.value } : {}),
  } })
}

async function clearFilters() {
  search.value = ''
  deliverableType.value = ''
  status.value = 'open'
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
  await mutate(() => api.reviewTask(detail.value!.id, { decision, note: reviewNote.value.trim() }), decision === 'accept' ? t('tasks.accepted') : t('tasks.revisionSent'))
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
  search.value = String(route.query.q || '')
  deliverableType.value = String(route.query.type || '')
  status.value = String(route.query.status ?? 'open')
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
onBeforeUnmount(() => globalThis.window.removeEventListener('keydown', handleWindowKeydown))
</script>

<template>
  <section class="task-market content-width">
    <template v-if="!isDetail">
      <header class="task-market-header">
        <div>
          <span class="status-label"><ShieldCheck :size="14" />{{ t('tasks.localTestShort') }}</span>
          <h1>{{ t('tasks.title') }}</h1>
          <p>{{ t('tasks.summary') }}</p>
        </div>
        <div class="task-header-actions">
          <RouterLink v-if="session.user" class="command-button secondary" to="/workspace/tasks">
            <BriefcaseBusiness :size="17" />{{ t('tasks.myTasks') }}
          </RouterLink>
          <button v-if="session.user" class="command-button primary" type="button" @click="createOpen = true">
            <Plus :size="17" />{{ t('tasks.publishBrief') }}
          </button>
          <RouterLink v-if="!session.user" class="command-button secondary" :to="{ path: '/settings', query: { auth: 'login', returnTo: route.fullPath } }">
            <LogIn :size="17" />{{ t('account.signIn') }}
          </RouterLink>
          <RouterLink v-if="!session.user" class="command-button primary" :to="{ path: '/settings', query: { auth: 'register', returnTo: route.fullPath } }">
            <UserPlus :size="17" />{{ t('account.createAccount') }}
          </RouterLink>
        </div>
      </header>

      <form class="task-filters" role="search" @submit.prevent="applyFilters">
        <label class="task-search"><span class="sr-only">{{ t('actions.search') }}</span><Search :size="17" /><input v-model="search" type="search" :placeholder="t('tasks.searchPlaceholder')" /></label>
        <label><span class="sr-only">{{ t('tasks.allTypes') }}</span><Filter :size="16" /><select v-model="deliverableType" @change="applyFilters"><option value="">{{ t('tasks.allTypes') }}</option><option v-for="item in types" :key="item" :value="item">{{ t(`tasks.types.${item}`) }}</option></select></label>
        <label><span class="sr-only">{{ t('tasks.allStatuses') }}</span><select v-model="status" @change="applyFilters"><option value="">{{ t('tasks.allStatuses') }}</option><option v-for="item in statuses" :key="item" :value="item">{{ t(`tasks.status.${item}`) }}</option></select></label>
        <label><span class="sr-only">{{ t('tasks.sortNewest') }}</span><select v-model="sort" @change="applyFilters"><option value="newest">{{ t('tasks.sortNewest') }}</option><option value="deadline">{{ t('tasks.sortDeadline') }}</option><option value="budget_desc">{{ t('tasks.sortBudget') }}</option></select></label>
        <button class="icon-button" type="submit" :aria-label="t('actions.search')">
          <Search :size="17" />
        </button>
      </form>

      <div v-if="loading" class="task-skeleton" aria-live="polite">
        <span v-for="item in 4" :key="item"></span><p>{{ t('tasks.loading') }}</p>
      </div>
      <div v-else-if="error" class="task-market-state task-market-error" role="alert">
        <span><AlertTriangle :size="20" /></span><strong>{{ t('tasks.unavailable') }}</strong><p>{{ error }}</p><button class="command-button secondary" type="button" @click="load">
          <RefreshCw :size="17" />{{ t('actions.retry') }}
        </button>
      </div>
      <div v-else class="task-results">
        <div class="task-results-meta">
          <strong>{{ tasks.length }} {{ t('tasks.results') }}</strong>
        </div>
        <RouterLink v-for="item in tasks" :key="item.id" class="task-row" :to="`/market/demands/${item.id}`">
          <span class="task-type"><component :is="item.deliverableType === 'image' ? WandSparkles : FileCheck2" :size="17" />{{ t(`tasks.types.${item.deliverableType}`) }}</span>
          <span class="task-row-copy"><strong>{{ item.title }}</strong><small>{{ item.summary }}</small><span>@{{ item.client.handle }}<template v-if="item.allowDirectAccept && item.status === 'open'"> / {{ t('tasks.direct') }}</template></span></span>
          <span class="task-row-data"><small>{{ t('tasks.reward') }}</small><strong>{{ money(item.budgetCents, item.currency) }}</strong></span>
          <span class="task-row-data"><small>{{ t('tasks.deadline') }}</small><strong>{{ date(item.deadline, item.clientTimezone) }}</strong></span>
          <span class="task-row-data"><small>{{ t('tasks.proposalCount') }}</small><strong>{{ item.proposalCount }}</strong></span>
          <span class="task-status" :data-status="item.status">{{ t(`tasks.status.${item.status}`) }}</span><ChevronRight :size="18" />
        </RouterLink>
        <div v-if="!tasks.length" class="task-market-state task-market-empty">
          <span><Search :size="20" /></span><strong>{{ t('tasks.noResults') }}</strong><button class="text-link" type="button" @click="clearFilters">
            {{ t('tasks.clearFilters') }}
          </button>
        </div>
      </div>
    </template>

    <template v-else>
      <RouterLink class="text-link task-back" to="/market/demands">
        <ArrowLeft :size="17" />{{ t('tasks.back') }}
      </RouterLink>
      <div v-if="loading" class="page-state" aria-live="polite">
        <LoaderCircle class="spin" :size="20" />{{ t('tasks.loading') }}
      </div>
      <div v-else-if="error && !detail" class="page-state" role="alert">
        <p>{{ error }}</p><button class="command-button secondary" type="button" @click="load">
          <RefreshCw :size="17" />{{ t('actions.retry') }}
        </button>
      </div>
      <div v-else-if="detail" class="task-detail-layout">
        <article class="task-brief">
          <header class="task-brief-header">
            <div class="task-brief-signals">
              <span class="task-status" :data-status="detail.status">{{ t(`tasks.status.${detail.status}`) }}</span><span v-if="detail.allowDirectAccept && detail.status === 'open'" class="task-direct-signal"><BriefcaseBusiness :size="14" />{{ t('tasks.direct') }}</span>
            </div><h1>{{ detail.title }}</h1><p>{{ detail.summary }}</p>
          </header>
          <section><h2>{{ t('tasks.brief') }}</h2><p>{{ detail.brief }}</p></section>
          <div class="task-rule-grid">
            <section>
              <h2>{{ t('tasks.deliverables') }}</h2><ul>
                <li v-for="item in detail.deliverables" :key="item">
                  <Check :size="16" />{{ item }}
                </li>
              </ul>
            </section>
            <section>
              <h2>{{ t('tasks.acceptance') }}</h2><ul>
                <li v-for="item in detail.acceptanceRules" :key="item">
                  <FileCheck2 :size="16" />{{ item }}
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
              <div><strong>{{ item.creator.displayName }}</strong><span>@{{ item.creator.handle }}</span></div><p>{{ item.approach }}</p><p>{{ item.deliverables }}</p><dl><div><dt>{{ t('tasks.reward') }}</dt><dd>{{ money(item.amountCents) }}</dd></div><div><dt>{{ t('tasks.timelineDays') }}</dt><dd>{{ item.timelineDays }}</dd></div></dl><button v-if="item.status === 'submitted' && detail.status === 'open'" class="command-button primary" type="button" :disabled="actionLoading" @click="acceptProposal(item.id)">
                {{ t('tasks.acceptProposal') }}
              </button><span v-else class="task-status" :data-status="item.status">{{ t(`tasks.proposalStatus.${item.status}`) }}</span>
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
            <h2>{{ t('tasks.history') }}</h2><ol>
              <li v-for="event in detail.events" :key="event.id">
                <span><Clock3 :size="15" /></span><div>
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
            <span>{{ t('tasks.reward') }}</span><strong>{{ money(detail.budgetCents, detail.currency) }}</strong><small>{{ t('tasks.localTest') }}</small>
          </div>
          <dl class="task-facts">
            <div><dt><UserRound :size="16" />{{ t('tasks.commissioner') }}</dt><dd>{{ detail.client.displayName }}<small>@{{ detail.client.handle }}</small></dd></div><div><dt><CalendarDays :size="16" />{{ t('tasks.deadline') }}</dt><dd>{{ date(detail.deadline, detail.clientTimezone) }}<small>{{ detail.clientTimezone }}</small></dd></div><div v-if="detail.assignee">
              <dt><BriefcaseBusiness :size="16" />{{ t('tasks.selectedCreator') }}</dt><dd>{{ detail.assignee.displayName }}<small>@{{ detail.assignee.handle }}</small></dd>
            </div>
          </dl>

          <div v-if="!session.user" class="market-auth-prompt">
            <div><h2>{{ t('tasks.guestTitle') }}</h2><p>{{ t('tasks.guestSummary') }}</p></div>
            <RouterLink class="command-button primary wide" :to="{ path: '/settings', query: { auth: 'login', returnTo: route.fullPath } }">
              <LogIn :size="17" />{{ t('account.signIn') }}
            </RouterLink>
            <RouterLink class="text-link" :to="{ path: '/settings', query: { auth: 'register', returnTo: route.fullPath } }">
              {{ t('account.createAccount') }}
            </RouterLink>
          </div>

          <div v-if="success" class="task-feedback success" role="status">
            <Check :size="17" />{{ success }}
          </div>
          <div v-if="error" class="task-feedback error" role="alert">
            <AlertTriangle :size="17" />{{ error }}
          </div>

          <button v-if="canClaim" class="command-button primary wide" type="button" :disabled="actionLoading" @click="claimTask">
            <BriefcaseBusiness :size="17" />{{ t('tasks.acceptTask') }}
          </button>
          <button v-if="canPropose && !proposalOpen" class="command-button secondary wide" type="button" @click="showProposalForm">
            <Plus :size="17" />{{ t('tasks.submitProposal') }}
          </button>
          <form v-if="proposalOpen" class="task-action-form" @submit.prevent="submitProposal">
            <label>{{ t('tasks.proposalApproach') }}<textarea v-model="proposal.approach" rows="5" minlength="20" required></textarea></label><label>{{ t('tasks.proposalDeliverables') }}<textarea v-model="proposal.deliverables" rows="3" minlength="10" required></textarea></label><div class="form-pair">
              <label>{{ t('tasks.proposedAmount') }}<input v-model="proposal.amount" type="number" min="1" step="1" required /></label><label>{{ t('tasks.timelineDays') }}<input v-model="proposal.timelineDays" type="number" min="1" required /></label>
            </div><button class="command-button primary wide" type="submit" :disabled="actionLoading">
              {{ t('tasks.submitProposal') }}
            </button>
          </form>

          <template v-if="canDeliver">
            <RouterLink class="command-button primary wide" :to="`/create/image?taskId=${detail.id}`">
              <WandSparkles :size="17" />{{ t('tasks.createForTask') }}
            </RouterLink><form class="task-action-form" @submit.prevent="submitDelivery">
              <label>{{ t('tasks.asset') }}<select v-model="delivery.assetId" required><option v-for="asset in assets" :key="asset.id" :value="asset.id">{{ asset.title }}</option></select></label><label>{{ t('tasks.deliveryNote') }}<textarea v-model="delivery.note" rows="4" minlength="5" required></textarea></label><button class="command-button secondary wide" type="submit" :disabled="actionLoading || !assets.length">
                <FileCheck2 :size="17" />{{ t('tasks.submitDelivery') }}
              </button>
            </form><p v-if="!assets.length" class="task-inline-help">
              {{ t('tasks.noDeliveryAssets') }}
            </p>
          </template>

          <form v-if="canReview" class="task-action-form" @submit.prevent>
            <label>{{ t('tasks.reviewNote') }}<textarea v-model="reviewNote" rows="4"></textarea></label><button class="command-button primary wide" type="button" :disabled="actionLoading" @click="review('accept')">
              <Check :size="17" />{{ t('tasks.acceptDelivery') }}
            </button><button class="command-button secondary wide" type="button" :disabled="actionLoading" @click="review('request_revision')">
              <RefreshCw :size="17" />{{ t('tasks.requestRevision') }}
            </button>
          </form>

          <button v-if="canDispute && !disputeOpen" class="text-link danger-link" type="button" @click="disputeOpen = true">
            {{ t('tasks.openDispute') }}
          </button>
          <form v-if="disputeOpen" class="task-action-form" @submit.prevent="openDispute">
            <label>{{ t('tasks.disputeReason') }}<textarea v-model="disputeReason" rows="4" minlength="20" required></textarea></label><button class="command-button secondary wide" type="submit" :disabled="actionLoading">
              {{ t('tasks.openDispute') }}
            </button>
          </form>
          <button v-if="canCancel && !cancelOpen" class="text-link danger-link" type="button" @click="cancelOpen = true">
            {{ t('tasks.cancelTask') }}
          </button>
          <form v-if="cancelOpen" class="task-action-form" @submit.prevent="cancelTask">
            <p class="task-form-warning">
              <AlertTriangle :size="16" />{{ t('tasks.cancelWarning') }}
            </p><label>{{ t('tasks.cancelReason') }}<textarea v-model="cancelReason" rows="3" minlength="10" required></textarea></label><div class="task-confirm-actions">
              <button class="command-button secondary" type="button" @click="cancelOpen = false">
                {{ t('tasks.keepTask') }}
              </button><button class="command-button danger" type="submit" :disabled="actionLoading">
                {{ t('tasks.confirmCancel') }}
              </button>
            </div>
          </form>
          <div v-if="detail.settlement" class="task-settlement">
            <CircleDollarSign :size="19" /><div><strong>{{ t('tasks.settlement') }}</strong><span>{{ money(detail.settlement.amountCents, detail.settlement.currency) }} / {{ t('tasks.localSettlementMode') }}</span></div>
          </div><p v-else class="task-payment-note">
            {{ t('tasks.unsettled') }}
          </p>

          <div v-if="session.user" class="demo-actor-switch">
            <span>{{ t('tasks.accountMode') }}: <strong>{{ session.user?.displayName }}</strong></span><button type="button" :disabled="actionLoading" @click="switchActor(detail.viewerRole === 'client' ? 'creator' : 'publisher')">
              {{ detail.viewerRole === 'client' ? t('tasks.switchCreator') : t('tasks.switchPublisher') }}
            </button>
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
          </div><button class="icon-button" type="button" :aria-label="t('tasks.cancel')" @click="createOpen = false">
            <X :size="19" />
          </button>
        </header><form @submit.prevent="publishTask">
          <fieldset class="task-form-section">
            <legend>{{ t('tasks.scopeSection') }}</legend><label>{{ t('tasks.titleLabel') }}<input ref="taskTitleInput" v-model="draft.title" required minlength="5" /></label><label>{{ t('tasks.summaryLabel') }}<textarea v-model="draft.summary" rows="2" required minlength="10"></textarea></label><label>{{ t('tasks.brief') }}<textarea v-model="draft.brief" rows="5" required minlength="30"></textarea></label>
          </fieldset>
          <fieldset class="task-form-section">
            <legend>{{ t('tasks.scheduleSection') }}</legend><div class="form-pair">
              <label>{{ t('tasks.typeLabel') }}<select v-model="draft.deliverableType"><option v-for="item in types" :key="item" :value="item">{{ t(`tasks.types.${item}`) }}</option></select></label><label>{{ t('tasks.budgetLabel') }}<input v-model="draft.budget" type="number" min="1" step="1" required /></label>
            </div><div class="form-pair">
              <label>{{ t('tasks.deadlineLabel') }}<input v-model="draft.deadline" type="datetime-local" :min="minimumDeadline" required /></label><label>{{ t('tasks.timezoneLabel') }}<select v-model="draft.timezone" :aria-label="t('tasks.timezoneLabel')" required><option v-for="item in timezoneOptions" :key="item" :value="item">{{ item }}</option></select></label>
            </div><label class="check-label"><input v-model="draft.allowDirectAccept" type="checkbox" />{{ t('tasks.directLabel') }}</label>
          </fieldset>
          <fieldset class="task-form-section">
            <legend>{{ t('tasks.deliverySection') }}</legend><label>{{ t('tasks.deliverablesLabel') }}<textarea v-model="draft.deliverables" rows="3" required></textarea></label><label>{{ t('tasks.acceptanceLabel') }}<textarea v-model="draft.acceptanceRules" rows="3" required></textarea></label><label>{{ t('tasks.rightsLabel') }}<textarea v-model="draft.rightsTerms" rows="3" required></textarea></label><label>{{ t('tasks.disclosureLabel') }}<textarea v-model="draft.aiDisclosureRequirement" rows="3" required></textarea></label>
          </fieldset><div class="modal-actions">
            <button class="command-button secondary" type="button" @click="createOpen = false">
              {{ t('tasks.cancel') }}
            </button><button class="command-button primary" type="submit" :disabled="actionLoading">
              {{ t('tasks.publish') }}
            </button>
          </div>
        </form>
      </section>
    </div>
  </section>
</template>
