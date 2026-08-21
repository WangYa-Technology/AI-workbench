<script setup lang="ts">
import { ArrowRight, Ban, Bell, Check, CheckCheck, Clock3, LoaderCircle, RefreshCw, Settings2 } from 'lucide-vue-next'
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api, messageFrom, type Notification, type NotificationDeliveryEvidence, type NotificationPreference } from '../api/client'
import { formatDateTime } from '../lib/format'
import { useNotificationsStore } from '../stores/notifications'
import { useSessionStore } from '../stores/session'
import UiButton from '../components/ui/UiButton.vue'
import UiIconButton from '../components/ui/UiIconButton.vue'
import UiSelect from '../components/ui/UiSelect.vue'

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

const view = computed(() => route.query.view === 'preferences' ? 'preferences' : 'inbox')
const readState = computed(() => ['all', 'unread', 'read'].includes(String(route.query.readState)) ? String(route.query.readState) as 'all' | 'unread' | 'read' : 'all')
const kind = computed(() => String(route.query.kind || ''))

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

onMounted(async () => {
  const user = await session.ensure()
  if (!user) return
  await Promise.all([load(), loadPreferences()])
})
</script>

<template>
  <section class="notifications-page content-width">
    <header class="notifications-header">
      <div><span class="status-label">{{ t('notifications.activityLabel') }}</span><h1>{{ t('notifications.title') }}</h1><p>{{ t('notifications.summary') }}</p></div>
      <UiButton v-if="session.user && view === 'inbox' && notifications.unreadCount" class="command-button secondary" variant="secondary" @click="notifications.markAllRead"><template #start><CheckCheck :size="17" /></template>{{ t('notifications.markAllRead') }}</UiButton>
    </header>

    <div v-if="session.initialized && !session.user" class="notification-auth-state">
      <Bell :size="24" /><h2>{{ t('notifications.signInTitle') }}</h2><p>{{ t('notifications.signInSummary') }}</p><UiButton as="RouterLink" class="command-button primary" variant="primary" to="/settings">{{ t('account.signIn') }}</UiButton>
    </div>

    <template v-else>
      <nav class="notification-view-tabs" :aria-label="t('notifications.views')">
        <RouterLink to="/notifications" :class="{ active: view === 'inbox' }">
          <Bell :size="17" />{{ t('notifications.inbox') }}<span v-if="notifications.unreadCount">{{ notifications.unreadCount }}</span>
        </RouterLink>
        <RouterLink to="/notifications?view=preferences" :class="{ active: view === 'preferences' }">
          <Settings2 :size="17" />{{ t('notifications.preferences') }}
        </RouterLink>
      </nav>

      <template v-if="view === 'inbox'">
        <div class="notification-filters">
          <label><span class="sr-only">{{ t('notifications.readState') }}</span><UiSelect :model-value="readState" @update:model-value="router.push({ path: '/notifications', query: { ...route.query, readState: $event, view: undefined } })"><option value="all">{{ t('notifications.all') }}</option><option value="unread">{{ t('notifications.unread') }}</option><option value="read">{{ t('notifications.read') }}</option></UiSelect></label>
          <label><span class="sr-only">{{ t('notifications.type') }}</span><UiSelect :model-value="kind" @update:model-value="router.push({ path: '/notifications', query: { ...route.query, kind: $event || undefined, view: undefined } })"><option value="">{{ t('notifications.allTypes') }}</option><option v-for="item in preferences" :key="item.kind" :value="item.kind">{{ t(`notifications.kinds.${item.kind}`) }}</option></UiSelect></label>
          <span>{{ t('notifications.unreadCount', { count: notifications.unreadCount }) }}</span>
        </div>

        <div v-if="notifications.loading" class="page-state" aria-live="polite">
          {{ t('notifications.loading') }}
        </div>
        <div v-else-if="notifications.error" class="page-state" role="alert">
          <p>{{ notifications.error }}</p><UiButton class="command-button secondary" variant="secondary" @click="load"><template #start><RefreshCw :size="17" /></template>{{ t('actions.retry') }}</UiButton>
        </div>
        <div v-else-if="!notifications.items.length" class="notification-empty">
          <Bell :size="22" /><h2>{{ t('notifications.emptyTitle') }}</h2><p>{{ t('notifications.emptySummary') }}</p>
        </div>
        <div v-else class="notification-list">
          <article v-for="item in notifications.items" :key="item.id" :class="{ unread: !item.readAt }">
            <span class="notification-signal" aria-hidden="true"></span>
            <button class="notification-copy" type="button" @click="open(item)">
              <span>{{ category(item.kind) }} · <time :datetime="item.createdAt">{{ date(item.createdAt) }}</time></span><strong>{{ item.title }}</strong><p>{{ item.body }}</p>
            </button>
            <div class="notification-actions">
              <UiIconButton v-if="!item.readAt" class="icon-button" :label="t('notifications.markRead')" @click="notifications.markRead(item)">
                <Check :size="17" />
              </UiIconButton><UiIconButton class="icon-button" :label="t('notifications.open')" @click="open(item)">
                <ArrowRight :size="17" />
              </UiIconButton>
            </div>
          </article>
        </div>
      </template>

      <div v-else class="notification-preferences-layout">
        <aside><h2>{{ t('notifications.preferences') }}</h2><p>{{ t('notifications.preferencesSummary') }}</p></aside>
        <section class="preference-list">
          <p v-if="preferenceError" class="form-error" role="alert">
            {{ preferenceError }}
          </p>
          <label v-for="item in preferences" :key="item.kind"><span><strong>{{ t(`notifications.kinds.${item.kind}`) }}</strong><small>{{ t(`notifications.kindDescriptions.${item.kind}`) }}</small></span><input type="checkbox" :checked="item.inAppEnabled" :disabled="savingKind === item.kind" @change="toggle(item)" /></label>
          <div class="delivery-evidence">
            <header><h3>{{ t('notifications.deliveryEvidence') }}</h3><p>{{ t('notifications.deliveryEvidenceSummary') }}</p></header>
            <p v-if="!deliveries.length" class="delivery-evidence-empty">
              {{ t('notifications.noDeliveryEvidence') }}
            </p>
            <article v-for="item in deliveries" v-else :key="item.id" :data-delivery-id="item.id">
              <component :is="item.status === 'delivered' ? Check : item.status === 'suppressed' ? Ban : Clock3" :size="17" aria-hidden="true" />
              <span><strong>{{ t(`notifications.kinds.${item.kind}`) }}</strong><small>{{ t(`notifications.deliveryStatuses.${item.status}`) }} · {{ t('notifications.deliveryAttempts', { count: item.attempts }) }}<template v-if="item.errorCode"> · {{ t(`notifications.deliveryErrors.${item.errorCode}`) }}</template></small></span>
              <time :datetime="item.completedAt || item.createdAt">{{ date(item.completedAt || item.createdAt) }}</time>
            </article>
            <button v-if="deliveryNextCursor" class="command-button secondary delivery-evidence-load-more" type="button" :disabled="deliveryLoadingMore" @click="loadMoreDeliveries">
              <LoaderCircle v-if="deliveryLoadingMore" class="spin" :size="16" />{{ t('actions.loadMore') }}
            </button>
          </div>
        </section>
      </div>
    </template>
  </section>
</template>
