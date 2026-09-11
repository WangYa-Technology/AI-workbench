<script setup lang="ts">
import { ArrowRight, Ban, Bell, BriefcaseBusiness, Check, CheckCheck, Clock3, FilterX, Inbox, RefreshCw, Settings2, WandSparkles } from 'lucide-vue-next'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api, messageFrom, type Notification, type NotificationDeliveryEvidence, type NotificationPreference } from '../api/client'
import { formatDateTime } from '../lib/format'
import { useNotificationsStore } from '../stores/notifications'
import { useSessionStore } from '../stores/session'
import UiButton from '../components/ui/UiButton.vue'
import UiIconButton from '../components/ui/UiIconButton.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiSwitch from '../components/ui/UiSwitch.vue'
import PageHero from '../components/ui/PageHero.vue'

const { t, locale } = useI18n()
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
const notificationTabsRoot = ref<globalThis.HTMLElement | null>(null)
const notificationTabsReady = ref(false)
const notificationTabIndicatorStyle = ref({ width: '0px', transform: 'translate(0px, 0px)' })
let notificationTabsObserver: globalThis.ResizeObserver | undefined

const view = computed(() => route.query.view === 'preferences' ? 'preferences' : 'inbox')
const readState = computed(() => ['all', 'unread', 'read'].includes(String(route.query.readState)) ? String(route.query.readState) as 'all' | 'unread' | 'read' : 'all')
const kind = computed(() => String(route.query.kind || ''))
const hasInboxFilters = computed(() => readState.value !== 'all' || Boolean(kind.value))
const enabledPreferenceCount = computed(() => preferences.value.filter(item => item.inAppEnabled).length)
const currentViewLabel = computed(() => view.value === 'inbox' ? t('notifications.inbox') : t('notifications.preferences'))
const currentViewSummary = computed(() => view.value === 'inbox' ? t('notifications.summary') : t('notifications.preferencesSummary'))
const notificationHeroStats = computed(() => [
  { value: notifications.unreadCount, label: t('notifications.unread'), icon: Inbox, tone: 'blue' as const },
  { value: enabledPreferenceCount.value, label: t('notifications.enabledCategories'), icon: Settings2, tone: 'violet' as const },
  { value: deliveries.value.length, label: t('notifications.deliveryEvidence'), icon: CheckCheck, tone: 'green' as const },
])

function date(value: string) {
  return formatDateTime(value, locale.value, session.user?.timezone || 'UTC')
}

function category(value: string) {
  return t(`notifications.categories.${value.split('.')[0]}`)
}

async function load() {
  if (!session.user) return
  await notifications.load({ readState: readState.value, kind: kind.value || undefined })
}

async function loadPreferences() {
  if (!session.user) return
  preferenceError.value = ''
  try {
    const [preferenceResult, deliveryResult] = await Promise.all([
      api.listNotificationPreferences(),
      api.listNotificationDeliveries({ limit: 20 }),
    ])
    preferences.value = preferenceResult.items
    deliveries.value = deliveryResult.items
    deliveryNextCursor.value = deliveryResult.nextCursor || null
  } catch (reason) {
    preferenceError.value = messageFrom(reason)
  }
}

async function loadMoreDeliveries() {
  if (!deliveryNextCursor.value || deliveryLoadingMore.value) return
  deliveryLoadingMore.value = true
  preferenceError.value = ''
  try {
    const page = await api.listNotificationDeliveries({ limit: 20, cursor: deliveryNextCursor.value })
    const known = new Set(deliveries.value.map(item => item.id))
    deliveries.value = [...deliveries.value, ...page.items.filter(item => !known.has(item.id))]
    deliveryNextCursor.value = page.nextCursor || null
  } catch (reason) {
    preferenceError.value = messageFrom(reason)
  } finally {
    deliveryLoadingMore.value = false
  }
}

async function open(item: Notification) {
  await router.push(item.targetPath)
}

function clearInboxFilters() {
  void router.push({ path: '/notifications' })
}

function updateNotificationTabIndicator() {
  const tab = notificationTabsRoot.value?.querySelector<globalThis.HTMLElement>(`[data-notification-view="${view.value}"]`)
  if (!tab) return
  notificationTabIndicatorStyle.value = {
    width: `${tab.offsetWidth}px`,
    transform: `translate(${tab.offsetLeft}px, ${tab.offsetTop}px)`,
  }
}

async function toggle(item: NotificationPreference) {
  savingKind.value = item.kind
  preferenceError.value = ''
  try {
    const updated = await api.updateNotificationPreference(item.kind, !item.inAppEnabled, item.version)
    preferences.value = preferences.value.map(candidate => candidate.kind === item.kind ? updated : candidate)
  } catch (reason) {
    preferenceError.value = messageFrom(reason)
    await loadPreferences()
  } finally {
    savingKind.value = ''
  }
}

watch(() => route.fullPath, () => {
  if (view.value === 'inbox') void load()
  else void loadPreferences()
})
watch([view, locale], () => void nextTick(updateNotificationTabIndicator))

onMounted(async () => {
  notificationTabsObserver = new globalThis.ResizeObserver(updateNotificationTabIndicator)
  if (notificationTabsRoot.value) notificationTabsObserver.observe(notificationTabsRoot.value)
  updateNotificationTabIndicator()
  globalThis.requestAnimationFrame(() => { notificationTabsReady.value = true })
  const user = await session.ensure()
  if (!user) return
  await Promise.all([load(), loadPreferences()])
})
onBeforeUnmount(() => notificationTabsObserver?.disconnect())
</script>

<template>
  <section class="notifications-page content-width">
    <PageHero
      class="notification-hero"
      :eyebrow="t('notifications.activityLabel')"
      :eyebrow-icon="Bell"
      :title="t('notifications.title')"
      :summary="t('notifications.summary')"
      :stats="notificationHeroStats"
      :stats-label="t('notifications.views')"
      artwork-src="/notifications/notification-hero.png"
      :artwork-width="1717"
      :artwork-height="916"
      adapt-artwork-for-dark
    >
      <template #actions>
        <UiButton v-if="session.user && view === 'inbox' && notifications.unreadCount" class="command-button secondary" variant="secondary" @click="notifications.markAllRead">
          <template #start><CheckCheck :size="17" /></template>{{ t('notifications.markAllRead') }}
        </UiButton>
      </template>
    </PageHero>

    <div v-if="session.initialized && !session.user" class="notification-auth-state">
      <Bell :size="24" /><h2>{{ t('notifications.signInTitle') }}</h2><p>{{ t('notifications.signInSummary') }}</p><UiButton as="RouterLink" class="command-button primary" variant="primary" :to="{ path: '/auth', query: { returnTo: route.fullPath } }">
        {{ t('account.signIn') }}
      </UiButton>
    </div>

    <div v-else class="notification-workspace">
      <aside class="notification-section-nav task-category-panel">
        <nav ref="notificationTabsRoot" class="notification-view-tabs t-tabs" :data-ready="notificationTabsReady ? 'true' : 'false'" :aria-label="t('notifications.views')">
          <span class="notification-view-pill t-tabs-pill" :style="notificationTabIndicatorStyle" aria-hidden="true"></span>
          <RouterLink to="/notifications" class="t-tab" data-notification-view="inbox" :aria-current="view === 'inbox' ? 'page' : undefined" :aria-label="t('notifications.inbox')" :class="{ active: view === 'inbox' }">
            <Inbox :size="18" />
            <span><strong>{{ t('notifications.inbox') }}</strong><small>{{ t('notifications.summary') }}</small></span>
            <em v-if="notifications.unreadCount">{{ notifications.unreadCount }}</em>
          </RouterLink>
          <RouterLink to="/notifications?view=preferences" class="t-tab" data-notification-view="preferences" :aria-current="view === 'preferences' ? 'page' : undefined" :aria-label="t('notifications.preferences')" :class="{ active: view === 'preferences' }">
            <Settings2 :size="18" />
            <span><strong>{{ t('notifications.preferences') }}</strong><small>{{ t('notifications.preferencesSummary') }}</small></span>
          </RouterLink>
        </nav>
      </aside>

      <div class="notification-results task-results">
        <div class="notification-results-meta task-results-meta">
          <div><h2>{{ currentViewLabel }}</h2><span>{{ currentViewSummary }}</span></div>
        </div>

        <template v-if="view === 'inbox'">
          <div class="notification-filters">
            <label><span>{{ t('notifications.readState') }}</span><UiSelect :model-value="readState" @update:model-value="router.push({ path: '/notifications', query: { ...route.query, readState: $event, view: undefined } })"><option value="all">{{ t('notifications.all') }}</option><option value="unread">{{ t('notifications.unread') }}</option><option value="read">{{ t('notifications.read') }}</option></UiSelect></label>
            <label><span>{{ t('notifications.type') }}</span><UiSelect :model-value="kind" @update:model-value="router.push({ path: '/notifications', query: { ...route.query, kind: $event || undefined, view: undefined } })"><option value="">{{ t('notifications.allTypes') }}</option><option v-for="item in preferences" :key="item.kind" :value="item.kind">{{ t(`notifications.kinds.${item.kind}`) }}</option></UiSelect></label>
            <span>{{ t('notifications.unreadCount', { count: notifications.unreadCount }) }}</span>
          </div>

          <section class="notification-list-shell" :class="{ 'is-empty': !notifications.loading && !notifications.error && !notifications.items.length }">
            <div v-if="notifications.loading" class="page-state" aria-live="polite">{{ t('notifications.loading') }}</div>
            <div v-else-if="notifications.error" class="page-state" role="alert">
              <p>{{ notifications.error }}</p><UiButton class="command-button secondary" variant="secondary" @click="load"><template #start><RefreshCw :size="17" /></template>{{ t('actions.retry') }}</UiButton>
            </div>
            <div v-else-if="!notifications.items.length" class="notification-empty task-market-state" :class="{ 'is-filtered': hasInboxFilters }">
              <span><component :is="hasInboxFilters ? FilterX : CheckCheck" :size="20" /></span>
              <strong>{{ t(hasInboxFilters ? 'notifications.emptyFilteredTitle' : 'notifications.emptyTitle') }}</strong>
              <p>{{ t(hasInboxFilters ? 'notifications.emptyFilteredSummary' : 'notifications.emptySummary') }}</p>
              <UiButton v-if="hasInboxFilters" class="text-link" variant="ghost" size="sm" @click="clearInboxFilters">
                <RefreshCw :size="15" />{{ t('actions.clearFilters') }}
              </UiButton>
              <div v-else class="task-empty-actions notification-empty-actions" :aria-label="t('notifications.emptyActionsLabel')">
                <UiButton as="RouterLink" class="command-button primary" variant="primary" to="/create/image">
                  <template #start><WandSparkles :size="16" /></template>{{ t('notifications.emptyCreateAction') }}
                </UiButton>
                <UiButton as="RouterLink" class="command-button secondary" variant="secondary" to="/market/demands">
                  <template #start><BriefcaseBusiness :size="16" /></template>{{ t('notifications.emptyTaskAction') }}
                </UiButton>
              </div>
            </div>
            <div v-else class="notification-list">
              <article v-for="item in notifications.items" :key="item.id" :class="{ unread: !item.readAt }">
                <span class="notification-signal" aria-hidden="true"></span>
                <UiButton class="notification-copy" variant="ghost" :content-wrapper="false" @click="open(item)">
                  <span>{{ category(item.kind) }} · <time :datetime="item.createdAt">{{ date(item.createdAt) }}</time></span><strong>{{ item.title }}</strong><p>{{ item.body }}</p>
                </UiButton>
                <div class="notification-actions">
                  <UiIconButton v-if="!item.readAt" class="icon-button" :label="t('notifications.markRead')" @click="notifications.markRead(item)"><Check :size="17" /></UiIconButton>
                  <UiIconButton class="icon-button" :label="t('notifications.open')" @click="open(item)"><ArrowRight :size="17" /></UiIconButton>
                </div>
              </article>
            </div>
          </section>
        </template>

        <template v-else>
          <p v-if="preferenceError" class="form-error notification-preference-error" role="alert">{{ preferenceError }}</p>
          <section class="notification-preference-panel preference-list" :aria-label="t('notifications.preferences')">
            <article v-for="item in preferences" :key="item.kind">
              <span><strong>{{ t(`notifications.kinds.${item.kind}`) }}</strong><small>{{ t(`notifications.kindDescriptions.${item.kind}`) }}</small></span>
              <UiSwitch :model-value="item.inAppEnabled" :label="t(`notifications.kinds.${item.kind}`)" :disabled="savingKind === item.kind" @update:model-value="toggle(item)" />
            </article>
          </section>

          <section class="notification-delivery-panel delivery-evidence">
            <header><h3>{{ t('notifications.deliveryEvidence') }}</h3><p>{{ t('notifications.deliveryEvidenceSummary') }}</p></header>
            <p v-if="!deliveries.length" class="delivery-evidence-empty">{{ t('notifications.noDeliveryEvidence') }}</p>
            <article v-for="item in deliveries" v-else :key="item.id" :data-delivery-id="item.id">
              <component :is="item.status === 'delivered' ? Check : item.status === 'suppressed' ? Ban : Clock3" :size="17" aria-hidden="true" />
              <span><strong>{{ t(`notifications.kinds.${item.kind}`) }}</strong><small>{{ t(`notifications.deliveryStatuses.${item.status}`) }} · {{ t('notifications.deliveryAttempts', { count: item.attempts }) }}<template v-if="item.errorCode"> · {{ t(`notifications.deliveryErrors.${item.errorCode}`) }}</template></small></span>
              <time :datetime="item.completedAt || item.createdAt">{{ date(item.completedAt || item.createdAt) }}</time>
            </article>
            <UiButton v-if="deliveryNextCursor" class="command-button secondary delivery-evidence-load-more" variant="secondary" :loading="deliveryLoadingMore" @click="loadMoreDeliveries">{{ t('actions.loadMore') }}</UiButton>
          </section>
        </template>
      </div>
    </div>
  </section>
</template>
