<script setup lang="ts">
import UiEmptyState from '../components/ui/UiEmptyState.vue'
import UiForm from '../components/ui/UiForm.vue'
import { ArrowLeft, CheckCircle2, Clock3, Copyright, CreditCard, FileWarning, Headphones, MessageSquare, Plus, ReceiptText, Send, ShieldCheck, UserRound } from 'lucide-vue-next'
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { api, messageFrom, type SupportCase, type SupportCaseCreate } from '../api/client'
import { formatDateTime } from '../lib/format'
import { useSessionStore } from '../stores/session'
import UiBadge from '../components/ui/UiBadge.vue'
import UiButton from '../components/ui/UiButton.vue'
import UiCatalog from '../components/ui/UiCatalog.vue'
import UiActionCard from '../components/ui/UiActionCard.vue'
import UiIconButton from '../components/ui/UiIconButton.vue'
import UiInput from '../components/ui/UiInput.vue'
import PageHeader from '../components/ui/PageHeader.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiTextarea from '../components/ui/UiTextarea.vue'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const loading = ref(true)
const actionLoading = ref(false)
const cases = ref<SupportCase[]>([])
const nextCursor = ref<string | null>(null)
const loadingMore = ref(false)
const activeCase = ref<SupportCase | null>(null)
const showCreate = ref(false)
const error = ref('')
const success = ref('')
const replyBody = ref('')
const form = reactive<SupportCaseCreate>({
  category: 'general_support', subject: '', details: '', relatedResourceType: '', locale: 'en-US', claimantRelationship: '', rightsStatement: '',
})

const terminal = computed(() => activeCase.value ? ['resolved', 'closed'].includes(activeCase.value.status) : false)
const isCopyright = computed(() => form.category === 'copyright')
const selectedID = computed(() => String(route.params.caseId || ''))
const openCaseCount = computed(() => cases.value.filter(item => item.status === 'open').length)
const resolvedCaseCount = computed(() => cases.value.filter(item => item.status === 'resolved').length)
const supportHeroStats = computed(() => [
  { value: cases.value.length, label: t('support.myCases'), icon: MessageSquare, tone: 'blue' as const },
  { value: openCaseCount.value, label: t('support.statuses.open'), icon: Clock3, tone: 'violet' as const },
  { value: resolvedCaseCount.value, label: t('support.statuses.resolved'), icon: CheckCircle2, tone: 'green' as const },
])
const supportRoutes = computed(() => [
  { category: 'general_support' as const, icon: Headphones, label: t('support.categories.general_support'), summary: t('support.categorySummaries.general_support') },
  { category: 'account' as const, icon: UserRound, label: t('support.categories.account'), summary: t('support.categorySummaries.account') },
  { category: 'billing' as const, icon: CreditCard, label: t('support.categories.billing'), summary: t('support.categorySummaries.billing') },
  { category: 'task_or_order' as const, icon: ReceiptText, label: t('support.categories.task_or_order'), summary: t('support.categorySummaries.task_or_order') },
  { category: 'copyright' as const, icon: Copyright, label: t('support.categories.copyright'), summary: t('support.categorySummaries.copyright') },
])

function statusVariant(status: SupportCase['status']) {
  if (status === 'resolved') return 'success'
  if (status === 'waiting_for_requester') return 'warning'
  if (status === 'closed') return 'neutral'
  return 'primary'
}

function date(value: string) {
  return formatDateTime(value, locale.value, session.user?.timezone || 'UTC')
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const user = await session.ensure()
    if (!user) return
    form.locale = user.locale as 'en-US' | 'zh-CN'
    const page = await api.listSupportCases({ limit: 20 })
    cases.value = page.items
    nextCursor.value = page.nextCursor || null
    if (selectedID.value) {
      activeCase.value = await api.getSupportCase(selectedID.value)
    } else {
      activeCase.value = null
    }
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    loading.value = false
  }
}

async function loadMore() {
  if (!nextCursor.value || loadingMore.value) return
  loadingMore.value = true
  error.value = ''
  try {
    const page = await api.listSupportCases({ limit: 20, cursor: nextCursor.value })
    const known = new Set(cases.value.map(item => item.id))
    cases.value = [...cases.value, ...page.items.filter(item => !known.has(item.id))]
    nextCursor.value = page.nextCursor || null
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    loadingMore.value = false
  }
}

async function selectCase(item: SupportCase) {
  showCreate.value = false
  await router.push(`/support/${item.id}`)
}

function startCreate(category: SupportCaseCreate['category'] = 'general_support') {
  activeCase.value = null
  form.category = category
  resetCopyrightFields()
  showCreate.value = true
  success.value = ''
  error.value = ''
  void nextTick(() => globalThis.document.getElementById('main-content')?.scrollTo({ top: 0 }))
}

function resetCopyrightFields() {
  if (!isCopyright.value) {
    form.claimantRelationship = ''
    form.rightsStatement = ''
  }
}

function backToSupport() {
  showCreate.value = false
  void router.push('/support')
}

async function createCase() {
  actionLoading.value = true
  error.value = ''
  success.value = ''
  try {
    const input: SupportCaseCreate = { ...form }
    if (!input.relatedResourceId) delete input.relatedResourceId
    const created = await api.createSupportCase(input)
    cases.value = [created, ...cases.value]
    showCreate.value = false
    success.value = t('support.created')
    Object.assign(form, { category: 'general_support', subject: '', details: '', relatedResourceType: '', relatedResourceId: undefined, claimantRelationship: '', rightsStatement: '' })
    await router.push(`/support/${created.id}`)
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionLoading.value = false
  }
}

async function submitReply() {
  if (!activeCase.value) return
  actionLoading.value = true
  error.value = ''
  success.value = ''
  try {
    const updated = await api.replySupportCase(activeCase.value.id, { body: replyBody.value, expectedVersion: activeCase.value.version })
    activeCase.value = updated
    cases.value = cases.value.map(item => item.id === updated.id ? updated : item)
    replyBody.value = ''
    success.value = t('support.replyAdded')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionLoading.value = false
  }
}

watch(() => route.params.caseId, () => void load())
onMounted(() => void load())
</script>

<template>
  <section class="support-page content-width">
    <PageHeader
      :title="t(showCreate ? 'support.createTitle' : 'support.title')"
      :summary="t(showCreate ? 'support.createIntro' : 'support.summary')"
      :stats="!showCreate && !loading && cases.length ? supportHeroStats : []"
      :stats-label="t('support.myCases')"
      artwork-src="/illustrations/headers/support.webp"
    >
      <template #actions>
        <UiButton v-if="showCreate" variant="secondary" :disabled="actionLoading" @click="backToSupport">
          <ArrowLeft :size="16" />{{ t('support.backToSupport') }}
        </UiButton>
        <UiButton v-else-if="session.user" class="command-button primary" variant="primary" @click="startCreate()">
          <template #start>
            <Plus :size="17" />
          </template>{{ t('support.newCase') }}
        </UiButton>
      </template>
    </PageHeader>

    <div v-if="!loading && !session.user" class="support-auth-state">
      <Headphones :size="28" /><h2>{{ t('support.signInTitle') }}</h2><p>{{ t('support.signInSummary') }}</p><UiButton as="RouterLink" variant="primary" :to="{ path: '/auth', query: { returnTo: route.fullPath } }">
        {{ t('account.signIn') }}
      </UiButton>
    </div>

    <template v-else-if="session.user">
      <div v-if="success" class="task-feedback success" role="status">
        <CheckCircle2 :size="18" />{{ success }}
      </div>
      <div v-if="error" class="task-feedback error" role="alert">
        <FileWarning :size="18" />{{ error }}
      </div>
      <div v-if="loading" class="page-state">
        {{ t('support.loading') }}
      </div>

      <div v-else class="support-layout" :class="{ 'detail-open': activeCase || showCreate, 'has-no-cases': !cases.length, 'is-creating': showCreate }">
        <aside v-if="!showCreate" class="support-case-index">
          <div class="support-index-heading">
            <div><h2>{{ t('support.myCases') }}</h2><span>{{ t('support.caseCount', { count: cases.length }) }}</span></div>
            <UiIconButton :label="t('support.newCase')" variant="ghost" size="sm" @click="startCreate()">
              <Plus :size="16" />
            </UiIconButton>
          </div>
          <UiButton v-for="item in cases" :key="item.id" variant="ghost" type="button" :class="{ active: item.id === activeCase?.id }" @click="selectCase(item)">
            <span><strong>{{ item.subject }}</strong><small>{{ t(`support.categories.${item.category}`) }}</small></span>
            <span><small>{{ date(item.updatedAt) }}</small><UiBadge :variant="statusVariant(item.status)" dot>{{ t(`support.statuses.${item.status}`) }}</UiBadge></span>
          </UiButton>
          <div v-if="nextCursor" class="support-index-pagination">
            <UiButton class="command-button secondary" variant="secondary" :loading="loadingMore" @click="loadMore">
              {{ t('actions.loadMore') }}
            </UiButton>
          </div>
          <UiEmptyState v-if="!cases.length" class="support-empty" density="compact" :title="t('support.emptyTitle')" :message="t('support.emptySummary')">
            <template #icon>
              <MessageSquare :size="22" />
            </template>
          </UiEmptyState>
        </aside>

        <section v-if="showCreate" class="support-detail support-create-panel">
          <UiForm surface="muted" class="support-form" :disabled="actionLoading" @submit.prevent="createCase">
            <div class="support-intake-columns">
              <section class="support-intake-section" aria-labelledby="support-problem-heading">
                <h2 id="support-problem-heading">
                  {{ t('support.problemSection') }}
                </h2>
                <label>{{ t('support.category') }}<UiSelect v-model="form.category" @change="resetCopyrightFields"><option v-for="category in ['general_support','billing','account','task_or_order','copyright']" :key="category" :value="category">{{ t(`support.categories.${category}`) }}</option></UiSelect></label>
                <label>{{ t('support.subject') }}<UiInput v-model.trim="form.subject" minlength="4" maxlength="160" required /></label>
                <label>{{ t('support.details') }}<UiTextarea v-model.trim="form.details" rows="9" minlength="20" maxlength="4000" required /></label>
              </section>
              <section class="support-intake-section support-intake-context" aria-labelledby="support-context-heading">
                <h2 id="support-context-heading">
                  {{ t('support.contextSection') }}<span v-if="!isCopyright">{{ t('support.optional') }}</span>
                </h2>
                <label>{{ t('support.relatedType') }}<UiSelect v-model="form.relatedResourceType"><option value="">{{ t('support.noResource') }}</option><option v-for="kind in ['work','product','post','asset','generation','order','task']" :key="kind" :value="kind">{{ t(`support.resourceTypes.${kind}`) }}</option></UiSelect></label>
                <label>{{ t('support.relatedId') }}<UiInput v-model.trim="form.relatedResourceId" :required="isCopyright || Boolean(form.relatedResourceType)" :placeholder="t('support.uuidPlaceholder')" /></label>
                <template v-if="isCopyright">
                  <label>{{ t('support.claimantRelationship') }}<UiSelect v-model="form.claimantRelationship" required><option value="" disabled>{{ t('support.chooseRelationship') }}</option><option value="rights_holder">{{ t('support.relationships.rights_holder') }}</option><option value="authorized_agent">{{ t('support.relationships.authorized_agent') }}</option></UiSelect></label>
                  <label>{{ t('support.rightsStatement') }}<UiTextarea v-model.trim="form.rightsStatement" rows="4" minlength="20" maxlength="1500" required /></label>
                  <p class="support-boundary">
                    <ShieldCheck :size="16" />{{ t('support.copyrightBoundary') }}
                  </p>
                </template>
              </section>
            </div>
            <footer class="support-intake-footer">
              <p class="support-intake-note">
                <FileWarning :size="16" />{{ t('support.sensitiveBoundary') }}
              </p>
              <div class="support-intake-actions">
                <UiButton variant="secondary" @click="backToSupport">
                  {{ t('actions.cancel') }}
                </UiButton>
                <UiButton class="command-button primary" variant="primary" type="submit" :loading="actionLoading">
                  <template #start>
                    <Send v-if="!actionLoading" :size="17" />
                  </template>{{ t('support.submitCase') }}
                </UiButton>
              </div>
            </footer>
          </UiForm>
        </section>

        <section v-else-if="activeCase" class="support-detail">
          <UiButton class="support-back" variant="ghost" type="button" @click="backToSupport">
            <ArrowLeft :size="16" />{{ t('support.backToSupport') }}
          </UiButton>
          <header class="support-case-header">
            <div><span>{{ t(`support.categories.${activeCase.category}`) }}</span><h2>{{ activeCase.subject }}</h2><p>{{ t('support.caseReference', { id: activeCase.id.slice(0, 8), version: activeCase.version }) }}</p></div>
            <UiBadge :variant="statusVariant(activeCase.status)" dot>
              {{ t(`support.statuses.${activeCase.status}`) }}
            </UiBadge>
          </header>
          <dl class="support-evidence">
            <div><dt>{{ t('support.opened') }}</dt><dd>{{ date(activeCase.createdAt) }}</dd></div>
            <div><dt>{{ t('support.assignedTo') }}</dt><dd>{{ activeCase.assignedOperatorHandle ? `@${activeCase.assignedOperatorHandle}` : t('support.unassigned') }}</dd></div>
            <div v-if="activeCase.relatedResourceId">
              <dt>{{ t('support.relatedResource') }}</dt><dd>{{ t(`support.resourceTypes.${activeCase.relatedResourceType}`) }} · {{ activeCase.relatedResourceId }}</dd>
            </div>
            <div v-if="activeCase.resolutionCode">
              <dt>{{ t('support.resolution') }}</dt><dd>{{ t(`support.resolutions.${activeCase.resolutionCode}`) }}</dd>
            </div>
          </dl>
          <div class="support-thread">
            <article v-for="message in activeCase.messages" :key="message.id" :class="message.authorRole">
              <header><strong>{{ message.authorRole === 'operator' ? t('support.supportTeam') : `@${message.authorHandle || activeCase.requesterHandle}` }}</strong><span>{{ date(message.createdAt) }}</span></header><p>{{ message.body }}</p>
            </article>
          </div>
          <UiForm v-if="!terminal" surface="muted" class="support-reply" @submit.prevent="submitReply">
            <label>{{ t('support.reply') }}<UiTextarea v-model.trim="replyBody" rows="4" minlength="2" maxlength="4000" required :placeholder="t('support.replyPlaceholder')" /></label>
            <UiButton class="command-button primary" variant="primary" type="submit" :loading="actionLoading">
              <template #start>
                <Send v-if="!actionLoading" :size="17" />
              </template>{{ t('support.sendReply') }}
            </UiButton>
          </UiForm>
          <div v-else class="support-closed-note">
            <Clock3 :size="16" /><span><strong>{{ t('support.caseComplete') }}</strong>{{ activeCase.resolutionReason }}</span>
          </div>
        </section>

        <section v-else class="support-detail support-welcome">
          <div class="support-welcome-heading">
            <h2>{{ t('support.selectTitle') }}</h2>
          </div>
          <UiCatalog class="support-route-list">
            <UiActionCard v-for="item in supportRoutes" :key="item.category" :title="item.label" :summary="item.summary" :icon="item.icon" @click="startCreate(item.category)" />
          </UiCatalog>
        </section>
      </div>
    </template>
  </section>
</template>
