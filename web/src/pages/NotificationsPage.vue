<script setup lang="ts">
import UiEmptyState from '../components/ui/UiEmptyState.vue'
import NotificationList from '../components/domain/NotificationList.vue'
import UiTabs from '../components/ui/UiTabs.vue'
import UiFilterBar from '../components/ui/UiFilterBar.vue'
import { Ban, Bell, BriefcaseBusiness, Check, CheckCheck, Clock3, FilterX, Inbox, RefreshCw, Settings2, WandSparkles, MessageCircle, ShieldCheck, ShoppingBag } from 'lucide-vue-next'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { api, APIError, messageFrom, type Notification, type NotificationDeliveryEvidence, type NotificationPreference } from '../api/client'
import { createScopedApi } from '../lib/scopedApi'
import { formatDateTime } from '../lib/format'
import { useNotificationsStore } from '../stores/notifications'
import { useSessionStore } from '../stores/session'
import UiButton from '../components/ui/UiButton.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiSwitch from '../components/ui/UiSwitch.vue'
import PageHeader from '../components/ui/PageHeader.vue'
import UiCollapsible from '../components/ui/UiCollapsible.vue'

const { t, te, locale } = useI18n()
const notificationKind = (kind: string) => te(`notifications.kinds.${kind}`) ? t(`notifications.kinds.${kind}`) : t('notifications.type')
const notificationDescription = (kind: string) => te(`notifications.kindDescriptions.${kind}`) ? t(`notifications.kindDescriptions.${kind}`) : ''
const notificationTitle = (item: Notification) => {
  if (locale.value !== 'zh-CN') return item.title
  const titles: Record<string, string> = {
    'Conversation ready': '对话已就绪',
    'Generation ready': '生成已完成',
    'Proposal accepted': '提案已接受',
  }
  return titles[item.title] || (te(`notifications.kinds.${item.kind}`) ? notificationKind(item.kind) : item.title)
}
const notificationBody = (item: Notification) => {
  if (locale.value !== 'zh-CN') return item.body
  if (item.kind.startsWith('marketplace.')) return notificationDescription(item.kind)
  if (item.title === 'Conversation ready') return '你的对话回复已准备好，可前往创作工作区查看。'
  if (item.kind === 'generation.completed') {
    if (item.body.includes('chat')) return '你的对话生成已完成，并已保存到资产库。'
    if (item.body.includes('image')) return '你的图像生成已完成，并已保存到资产库。'
    if (item.body.includes('video')) return '你的视频生成已完成，并已保存到资产库。'
    if (item.body.includes('music')) return '你的音乐生成已完成，并已保存到资产库。'
    return '你的生成结果已完成，并已保存到资产库。'
  }
  return item.body
}
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const notifications = useNotificationsStore()
const preferences = ref<NotificationPreference[]>([])
const deliveries = ref<NotificationDeliveryEvidence[]>([])
const deliveryNextCursor = ref<string | null>(null)
const deliveryLoadingMore = ref(false)
const preferenceError = ref('')
const savingKind = ref('')

const view = computed(() => route.query.view === 'preferences' ? 'preferences' : 'inbox')
const viewTabs = computed(() => [
  { value: 'inbox', label: notifications.unreadCount ? `${t('notifications.inbox')} · ${notifications.unreadCount}` : t('notifications.inbox'), icon: Inbox },
  { value: 'preferences', label: t('notifications.preferences'), icon: Settings2 },
])
const readState = computed(() => ['all', 'unread', 'read'].includes(String(route.query.readState)) ? String(route.query.readState) as 'all' | 'unread' | 'read' : 'all')
const kind = computed(() => String(route.query.kind || ''))
const hasInboxFilters = computed(() => readState.value !== 'all' || Boolean(kind.value))
const currentViewLabel = computed(() => {
  if (view.value === 'preferences') return t('notifications.preferences')
  if (notifications.loading) return t('notifications.loading')
  if (notifications.error) return t('notifications.inbox')
  return t(notifications.nextCursor ? 'notifications.loadedCount' : 'notifications.resultCount', { count: notifications.items.length })
})
const currentViewSummary = computed(() => view.value === 'inbox' ? t('notifications.inboxUnreadCount', { count: notifications.unreadCount }) : t('notifications.preferencesSummary'))
const preferenceGroups = computed(() => {
  const groups = [
    { key: 'creation', icon: WandSparkles, prefixes: ['generation', 'asset'] },
    { key: 'community', icon: MessageCircle, prefixes: ['community'] },
    { key: 'tasks', icon: BriefcaseBusiness, prefixes: ['task'] },
    { key: 'purchases', icon: ShoppingBag, prefixes: ['marketplace', 'billing'] },
    { key: 'account', icon: ShieldCheck, prefixes: ['account', 'security', 'support'] },
  ]
  const known = new Set(groups.flatMap(group => group.prefixes))
  return groups.map(group => ({
    ...group,
    items: preferences.value.filter(item => group.prefixes.includes(item.kind.split('.')[0]!)
      || (group.key === 'account' && !known.has(item.kind.split('.')[0]!))),
  })).filter(group => group.items.length)
})
const preferencesLoading = ref(true)
const preferenceSaved = ref(false)
const openPreferenceGroups = ref(new Set(['creation']))
let generation = 0
function clearPreferences() {
  generation++; preferences.value = []; deliveries.value = []; deliveryNextCursor.value = null
  preferenceError.value = ''; preferenceSaved.value = false; savingKind.value = ''
  preferencesLoading.value = false; deliveryLoadingMore.value = false
}
function scope(current: number) {
  return createScopedApi(api, () => current === generation && !!session.user && session.user.status === 'active' && !notifications.accessDenied, cause => {
    if (cause instanceof APIError && [401, 403].includes(cause.status)) notifications.denyAccess(cause)
  })
}

function date(value: string) {
  return formatDateTime(value, locale.value, session.user?.timezone || 'UTC')
}

function category(value: string) {
  return t(`notifications.categories.${value.split('.')[0]}`)
}

function isPreferenceGroupOpen(key: string) {
  return openPreferenceGroups.value.has(key)
}

function setPreferenceGroupOpen(key: string, open: boolean) {
  const next = new Set(openPreferenceGroups.value)
  if (open) next.add(key)
  else next.delete(key)
  openPreferenceGroups.value = next
}

async function load() {
  if (!session.user) return
  await notifications.load({ readState: readState.value, kind: kind.value || undefined })
}

async function loadPreferences() {
  if (!session.user || session.user.status !== 'active' || notifications.accessDenied) return
  const current = generation
  const client = scope(current)
  preferencesLoading.value = true
  preferenceError.value = ''
  try {
    const [preferenceResult, deliveryResult] = await Promise.all([
      client.listNotificationPreferences(),
      client.listNotificationDeliveries({ limit: 20 }),
    ])
    preferences.value = preferenceResult.items
    deliveries.value = deliveryResult.items
    deliveryNextCursor.value = deliveryResult.nextCursor || null
  } catch (reason) {
    if (current === generation) preferenceError.value = messageFrom(reason)
  } finally {
    if (current === generation) preferencesLoading.value = false
  }
}

function retryPreferences() {
  clearPreferences(); notifications.clear(); void load(); void loadPreferences()
}

async function loadMoreDeliveries() {
  if (!deliveryNextCursor.value || deliveryLoadingMore.value || preferencesLoading.value || notifications.accessDenied) return
  const current = generation
  deliveryLoadingMore.value = true
  preferenceError.value = ''
  try {
    const page = await scope(current).listNotificationDeliveries({ limit: 20, cursor: deliveryNextCursor.value })
    const known = new Set(deliveries.value.map(item => item.id))
    deliveries.value = [...deliveries.value, ...page.items.filter(item => !known.has(item.id))]
    deliveryNextCursor.value = page.nextCursor || null
  } catch (reason) {
    if (current === generation) preferenceError.value = messageFrom(reason)
  } finally {
    if (current === generation) deliveryLoadingMore.value = false
  }
}

function clearInboxFilters() {
  void router.push({ path: '/notifications' })
}

async function toggle(item: NotificationPreference) {
  if (savingKind.value || notifications.accessDenied || !preferences.value.includes(item)) return
  const current = generation
  preferenceSaved.value = false
  savingKind.value = item.kind
  preferenceError.value = ''
  try {
    const updated = await scope(current).updateNotificationPreference(item.kind, !item.inAppEnabled, item.version)
    preferences.value = preferences.value.map(candidate => candidate.kind === item.kind ? updated : candidate)
    preferenceSaved.value = true
  } catch (reason) {
    if (current !== generation) return
    await loadPreferences()
    if (current === generation) preferenceError.value = messageFrom(reason)
  } finally {
    if (current === generation) savingKind.value = ''
  }
}

watch([() => session.user, () => session.initialized, () => route.fullPath], () => {
  clearPreferences(); notifications.clear()
  if (session.initialized && session.user?.status === 'active') { void load(); void loadPreferences() }
}, { immediate: true, flush: 'sync', deep: true })
watch(() => notifications.accessDenied, denied => { if (denied) { clearPreferences(); preferenceError.value = notifications.error } }, { flush: 'sync' })
onBeforeUnmount(() => { clearPreferences(); notifications.clearView() })
</script>

<template>
  <section class="notifications-page content-width">
    <PageHeader
      class="notification-hero"
      :title="t('notifications.title')"
      :summary="t('notifications.summary')"
      artwork-src="/illustrations/headers/notifications.webp"
    >
      <template #actions>
        <UiButton v-if="session.user && view === 'inbox' && notifications.unreadCount" class="command-button secondary" variant="secondary" @click="notifications.markAllRead">
          <template #start>
            <CheckCheck :size="17" />
          </template>{{ t('notifications.markAllRead') }}
        </UiButton>
      </template>
    </PageHeader>

    <div v-if="session.initialized && !session.user" class="notification-auth-state">
      <Bell :size="24" /><h2>{{ t('notifications.signInTitle') }}</h2><p>{{ t('notifications.signInSummary') }}</p><UiButton as="RouterLink" class="command-button primary" variant="primary" :to="{ path: '/auth', query: { returnTo: route.fullPath } }">
        {{ t('account.signIn') }}
      </UiButton>
    </div>

    <div v-else class="notification-workspace">
      <UiFilterBar as="div" split class="controls-with-switcher has-switcher">
        <div class="view-switcher-bar">
          <UiTabs class="view-switcher" :model-value="view" :items="viewTabs" :label="t('notifications.views')" @update:model-value="router.push({ path: '/notifications', query: { ...route.query, view: $event === 'preferences' ? 'preferences' : undefined } })" />
        </div>
        <div v-if="view === 'inbox'" class="ui-filter-bar__controls ui-filter-bar__controls--end">
          <UiSelect :aria-label="t('notifications.readState')" :model-value="readState" @update:model-value="router.push({ path: '/notifications', query: { ...route.query, readState: $event, view: undefined } })">
            <option value="all">
              {{ t('notifications.all') }}
            </option><option value="unread">
              {{ t('notifications.unread') }}
            </option><option value="read">
              {{ t('notifications.read') }}
            </option>
          </UiSelect>
          <UiSelect :aria-label="t('notifications.type')" :model-value="kind" @update:model-value="router.push({ path: '/notifications', query: { ...route.query, kind: $event || undefined, view: undefined } })">
            <option value="">
              {{ t('notifications.allTypes') }}
            </option><option v-for="item in preferences" :key="item.kind" :value="item.kind">
              {{ notificationKind(item.kind) }}
            </option>
          </UiSelect>
        </div>
      </UiFilterBar>

      <div class="notification-results task-results">
        <div class="notification-results-meta task-results-meta">
          <div><h2>{{ currentViewLabel }}</h2><span>{{ currentViewSummary }}</span></div>
          <div v-if="view === 'preferences'" class="notification-save-status" role="status">
            <CheckCheck :size="15" aria-hidden="true" />{{ t(preferenceSaved ? 'notifications.saved' : 'notifications.autoSave') }}
          </div>
        </div>

        <template v-if="view === 'inbox'">
          <section class="notification-list-shell">
            <div v-if="notifications.loading" class="page-state" aria-live="polite">
              {{ t('notifications.loading') }}
            </div>
            <div v-else-if="notifications.error" class="page-state" role="alert">
              <p>{{ notifications.error }}</p><UiButton class="command-button secondary" variant="secondary" @click="load">
                <template #start>
                  <RefreshCw :size="17" />
                </template>{{ t('actions.retry') }}
              </UiButton>
            </div>
            <UiEmptyState
              v-else-if="!notifications.items.length" class="notification-empty" :class="{ 'is-filtered': hasInboxFilters }"
              :title="t(hasInboxFilters ? 'notifications.emptyFilteredTitle' : 'notifications.emptyTitle')"
              :message="t(hasInboxFilters ? 'notifications.emptyFilteredSummary' : 'notifications.emptySummary')"
            >
              <template #icon>
                <component :is="hasInboxFilters ? FilterX : CheckCheck" :size="24" :stroke-width="1.75" />
              </template>
              <template #actions>
                <UiButton v-if="hasInboxFilters" variant="secondary" @click="clearInboxFilters">
                  {{ t('actions.clearFilters') }}
                </UiButton>
                <template v-else>
                  <UiButton as="RouterLink" variant="primary" to="/create/image">
                    {{ t('notifications.emptyCreateAction') }}
                  </UiButton>
                  <UiButton as="RouterLink" variant="secondary" to="/market/demands">
                    {{ t('notifications.emptyTaskAction') }}
                  </UiButton>
                </template>
              </template>
            </UiEmptyState>
            <template v-else>
              <NotificationList :items="notifications.items" :title-for="notificationTitle" :body-for="notificationBody" :category-for="category" :date-for="date" @read="notifications.markRead" />
              <UiButton v-if="notifications.nextCursor" class="notification-load-more" variant="secondary" :loading="notifications.loading" @click="notifications.loadMore({ readState, kind: kind || undefined })">
                {{ t('actions.loadMore') }}
              </UiButton>
            </template>
          </section>
        </template>

        <template v-else>
          <div v-if="preferenceError" class="form-error notification-preference-error" role="alert">
            <p>{{ preferenceError }}</p>
            <UiButton variant="secondary" size="sm" @click="retryPreferences">
              {{ t('actions.retry') }}
            </UiButton>
          </div>
          <p v-if="preferencesLoading" class="page-state" role="status">
            {{ t('notifications.loading') }}
          </p>
          <div v-else class="notification-preference-groups">
            <UiCollapsible v-for="group in preferenceGroups" :key="group.key" class="notification-preference-panel preference-list" :open="isPreferenceGroupOpen(group.key)" @update:open="setPreferenceGroupOpen(group.key, $event)">
              <template #trigger>
                <span class="notification-group-icon"><component :is="group.icon" :size="19" aria-hidden="true" /></span>
                <div class="notification-group-copy">
                  <h3 :id="'notification-group-' + group.key">
                    {{ t('notifications.groups.' + group.key) }}
                  </h3><p>{{ t('notifications.groupSummaries.' + group.key) }}</p>
                </div>
                <small class="notification-group-count">{{ group.items.filter(item => item.inAppEnabled).length }} / {{ group.items.length }}</small>
              </template>
              <div class="notification-group-options">
                <article v-for="item in group.items" :key="item.kind">
                  <span><strong>{{ notificationKind(item.kind) }}</strong><small :id="'notification-description-' + item.kind">{{ notificationDescription(item.kind) }}</small></span>
                  <UiSwitch :model-value="item.inAppEnabled" :label="notificationKind(item.kind)" :aria-describedby="'notification-description-' + item.kind" :disabled="!!savingKind" @update:model-value="toggle(item)" />
                </article>
              </div>
            </UiCollapsible>
          </div>

          <section class="notification-delivery-panel delivery-evidence">
            <header><h3>{{ t('notifications.deliveryEvidence') }}</h3><p>{{ t('notifications.deliveryEvidenceSummary') }}</p></header>
            <UiEmptyState v-if="!deliveries.length" density="compact" :title="t('notifications.noDeliveryEvidence')" />
            <article v-for="item in deliveries" v-else :key="item.id" :data-delivery-id="item.id">
              <component :is="item.status === 'delivered' ? Check : item.status === 'suppressed' ? Ban : Clock3" :size="17" aria-hidden="true" />
              <span><strong>{{ notificationKind(item.kind) }}</strong><small>{{ t(`notifications.deliveryStatuses.${item.status}`) }} · {{ t('notifications.deliveryAttempts', { count: item.attempts }) }}<template v-if="item.errorCode"> · {{ t(`notifications.deliveryErrors.${item.errorCode}`) }}</template></small></span>
              <time :datetime="item.completedAt || item.createdAt">{{ date(item.completedAt || item.createdAt) }}</time>
            </article>
            <UiButton v-if="deliveryNextCursor" class="command-button secondary delivery-evidence-load-more" variant="secondary" :loading="deliveryLoadingMore" @click="loadMoreDeliveries">
              {{ t('actions.loadMore') }}
            </UiButton>
          </section>
        </template>
      </div>
    </div>
  </section>
</template>

<style scoped>
.notification-save-status { display: flex; align-items: center; gap: 6px; margin: 0; color: var(--text-secondary); font-size: 12px; }
.notification-save-status svg { color: var(--accent-readable); }
.notification-preference-groups { border-block: 1px solid var(--border); }
.notification-preference-panel.preference-list { display: block; padding: 0; border: 0; border-radius: 0; background: transparent; box-shadow: none; overflow: visible; }
.notification-preference-panel + .notification-preference-panel { border-top: 1px solid var(--border); }
.notification-preference-panel :deep(.ui-collapsible__trigger) { min-height: 0; justify-content: flex-start; padding: 16px 4px; }
.notification-group-icon { display: grid; place-items: center; width: 24px; height: 24px; flex-shrink: 0; color: var(--text-secondary); }
.notification-group-copy { flex: 1; min-width: 0; }
.notification-group-copy h3 { margin: 0; font-size: 14px; font-weight: 600; }
.notification-group-copy p { margin: 4px 0 0; font-weight: 400; color: var(--text-secondary); font-size: 12px; line-height: 1.5; }
.notification-group-count { color: var(--text-tertiary); white-space: nowrap; font-variant-numeric: tabular-nums; }
.notification-group-options { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); padding: 0 4px 14px 40px; column-gap: 32px; }
.notification-group-options article { display: flex; align-items: center; gap: 16px; padding: 12px 0; border-top: 0; min-width: 0; }
.notification-group-options article > span { flex: 1; min-width: 0; display: grid; gap: 5px; }
.notification-group-options strong { font-size: 13px; font-weight: 550; }
.notification-group-options small { color: var(--text-secondary); font-size: 12px; line-height: 1.6; }
.notification-group-options :deep(.ui-switch) { flex-shrink: 0; }
.notification-results-meta { flex-wrap: wrap; margin-bottom: 12px; }
.notification-results-meta > div:first-child { flex-wrap: wrap; }
@media (max-width: 1100px) { .notification-group-options { grid-template-columns: minmax(0, 1fr); } }
@media (max-width: 767px) { .notification-preference-panel :deep(.ui-collapsible__trigger) { padding: 14px 0; } .notification-group-options { padding-inline: 0; } }
</style>
