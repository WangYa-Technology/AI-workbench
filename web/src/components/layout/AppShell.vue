<script setup lang="ts">
import { Bell, CircleUserRound, ClipboardList, Compass, Headphones, History, Images, Languages, ListChecks, Moon, ReceiptText, Search, ShoppingBag, Store, Sun, Upload, UsersRound, WalletCards, WandSparkles } from 'lucide-vue-next'
import { computed, nextTick, onMounted, ref, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { usePreferencesStore } from '../../stores/preferences'
import { useNotificationsStore } from '../../stores/notifications'
import { useSessionStore } from '../../stores/session'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const preferences = usePreferencesStore()
const session = useSessionStore()
const notifications = useNotificationsStore()
const searchQuery = ref(String(route.query.q || ''))
const mainContent = useTemplateRef('mainContent')
const routeAnnouncement = ref('')
let routeFocusReady = false

onMounted(async () => {
  const user = await session.ensure()
  if (user) await notifications.refreshCount()
})

onMounted(async () => {
  await router.isReady()
  await nextTick()
  routeFocusReady = true
})

const communityNav = computed(() => [
  { key: 'tasks', label: t('nav.tasks'), to: '/market/demands', icon: ClipboardList },
  { key: 'community', label: t('nav.community'), to: '/community', icon: UsersRound },
  { key: 'inspiration', label: t('nav.inspiration'), to: '/discover', icon: Compass },
  { key: 'marketplace', label: t('nav.resourceMarket'), to: '/market', icon: Store },
])

const workbenchNav = computed(() => [
  { key: 'create', label: t('nav.aiCreate'), to: '/create/image', icon: WandSparkles },
  { key: 'assets', label: t('workspace.assets'), to: '/workspace/assets', icon: Images },
  { key: 'generations', label: t('workspace.generations'), to: '/workspace/generations', icon: History },
  { key: 'purchases', label: t('workspace.purchases'), to: '/workspace/purchases', icon: ShoppingBag },
  { key: 'orders', label: t('workspace.orders'), to: '/workspace/orders', icon: ReceiptText },
  { key: 'taskDesk', label: t('workspace.tasks'), to: '/workspace/tasks', icon: ListChecks },
  { key: 'billing', label: t('workspace.billing'), to: '/workspace/billing', icon: WalletCards },
  { key: 'publish', label: t('actions.publishWork'), to: '/publish', icon: Upload },
  { key: 'support', label: t('nav.support'), to: '/support', icon: Headphones },
])

const mobileNav = computed(() => [
  { key: 'inspiration', label: t('nav.inspirationShort'), to: '/discover', icon: Compass },
  { key: 'create', label: t('nav.create'), to: '/create/image', icon: WandSparkles },
  { key: 'marketplace', label: t('nav.marketShort'), to: '/market', icon: Store },
  { key: 'community', label: t('nav.community'), to: '/community', icon: UsersRound },
])

const policyTopics = ['terms', 'privacy', 'cookies', 'acceptable', 'ai', 'licensing', 'refunds', 'copyright'] as const
const trustLinks = computed(() => [
  ...policyTopics.map((topic) => ({
    label: t(`legal.topics.${topic}.title`),
    to: `/policies/${topic}`,
  })),
  { label: t('nav.support'), to: '/support' },
])

const active = (key: string) => {
  const name = String(route.name)
  if (key === 'inspiration') return ['discover', 'work'].includes(name)
  if (key === 'tasks') return ['demands', 'demand'].includes(name)
  if (key === 'marketplace') return ['marketplace', 'product'].includes(name)
  if (key === 'assets') return (name === 'workspace' && (!route.params.section || route.params.section === 'assets')) || name === 'asset'
  if (key === 'generations') return name === 'workspace' && route.params.section === 'generations'
  if (key === 'purchases') return name === 'workspace' && route.params.section === 'purchases'
  if (key === 'orders') return name === 'workspace' && route.params.section === 'orders'
  if (key === 'taskDesk') return name === 'workspace' && route.params.section === 'tasks'
  if (key === 'billing') return name === 'workspace' && route.params.section === 'billing'
  return name === key
}

const quickLinks = computed(() => [
  { label: t('create.modes.image'), to: '/create/image' },
  { label: t('discover.promptLibrary'), to: '/market' },
  { label: t('workspace.assets'), to: '/workspace/assets' },
  { label: t('nav.community'), to: '/community' },
])

watch(() => route.query.q, (value) => {
  searchQuery.value = String(value || '')
})

watch(() => route.path, async () => {
  if (!routeFocusReady) return
  await nextTick()
  await new Promise<void>((resolve) => globalThis.requestAnimationFrame(() => resolve()))
  const heading = mainContent.value?.querySelector('h1')?.textContent?.trim()
  routeAnnouncement.value = heading || t('accessibility.pageChanged')
  mainContent.value?.focus({ preventScroll: true })
})

watch(() => session.user?.id, (userID) => {
  if (userID) void notifications.refreshCount()
  else notifications.clear()
})

const submitSearch = () => {
  const q = searchQuery.value.trim()
  if (q.length >= 2) void router.push({ path: '/search', query: { q } })
}
</script>

<template>
  <div class="app-shell">
    <a class="skip-link" href="#main-content">{{ t('accessibility.skipToContent') }}</a>
    <p class="sr-only" aria-live="polite" aria-atomic="true">
      {{ routeAnnouncement }}
    </p>
    <aside class="site-sidebar">
      <RouterLink class="brand" to="/discover" :aria-label="t('brand')">
        <span class="brand-mark"><WandSparkles :size="18" :stroke-width="1.75" /></span>
        <strong>{{ t('brand') }}</strong>
      </RouterLink>

      <nav class="primary-nav" :aria-label="t('accessibility.primaryNavigation')">
        <section class="nav-section">
          <span class="nav-section-label">{{ t('nav.communityArea') }}</span>
          <RouterLink v-for="item in communityNav" :key="item.key" :to="item.to" :class="{ active: active(item.key) }" :aria-current="active(item.key) ? 'page' : undefined">
            <component :is="item.icon" :size="18" :stroke-width="1.75" />
            <span>{{ item.label }}</span>
          </RouterLink>
        </section>
        <section class="nav-section">
          <span class="nav-section-label">{{ t('nav.workbench') }}</span>
          <RouterLink v-for="item in workbenchNav" :key="item.key" :to="item.to" :class="{ active: active(item.key) }" :aria-current="active(item.key) ? 'page' : undefined">
            <component :is="item.icon" :size="18" :stroke-width="1.75" />
            <span>{{ item.label }}</span>
          </RouterLink>
        </section>
      </nav>
    </aside>

    <div class="app-main">
      <header class="site-header">
        <RouterLink class="mobile-brand" to="/discover" :aria-label="t('brand')">
          <span class="brand-mark"><WandSparkles :size="18" :stroke-width="1.75" /></span>
          <strong>{{ t('brand') }}</strong>
        </RouterLink>

        <div class="search-stack">
          <form class="global-search" role="search" @submit.prevent="submitSearch">
            <input v-model="searchQuery" type="search" :placeholder="t('actions.searchPlaceholder')" :aria-label="t('actions.search')" />
            <button type="submit" :aria-label="t('actions.search')" :title="t('actions.search')">
              <Search :size="18" :stroke-width="1.75" />
            </button>
          </form>
          <nav class="quick-links" :aria-label="t('actions.quickLinks')">
            <span>{{ t('actions.explore') }}</span>
            <RouterLink v-for="item in quickLinks" :key="item.to" :to="item.to">
              {{ item.label }}
            </RouterLink>
          </nav>
        </div>

        <div class="header-actions">
          <RouterLink class="command-button secondary" to="/publish">
            <Upload :size="17" :stroke-width="1.75" />
            <span>{{ t('actions.publish') }}</span>
          </RouterLink>
          <RouterLink class="icon-button notification-action" to="/notifications" :aria-label="t('actions.notifications')" :title="t('actions.notifications')">
            <Bell :size="19" :stroke-width="1.75" />
            <span v-if="notifications.unreadCount" class="notification-badge" :aria-label="t('notifications.unreadCount', { count: notifications.unreadCount })">{{ notifications.unreadCount > 99 ? '99+' : notifications.unreadCount }}</span>
          </RouterLink>
          <button class="icon-button desktop-utility" type="button" :aria-label="t('actions.language')" :title="t('actions.language')" @click="preferences.toggleLocale">
            <Languages :size="18" :stroke-width="1.75" />
          </button>
          <button class="icon-button desktop-utility" type="button" :aria-label="t('actions.theme')" :title="t('actions.theme')" @click="preferences.toggleTheme">
            <Sun v-if="preferences.resolvedTheme === 'dark'" :size="18" :stroke-width="1.75" />
            <Moon v-else :size="18" :stroke-width="1.75" />
          </button>
          <RouterLink class="icon-button account-action" to="/settings" :aria-label="t('actions.account')" :title="session.user?.displayName || t('actions.account')">
            <CircleUserRound :size="19" :stroke-width="1.75" />
          </RouterLink>
        </div>
      </header>

      <main id="main-content" ref="mainContent" tabindex="-1">
        <RouterView v-slot="{ Component }">
          <Transition name="page" mode="out-in">
            <component :is="Component" />
          </Transition>
        </RouterView>
      </main>

      <footer class="site-footer">
        <nav :aria-label="t('legal.footerLabel')">
          <RouterLink v-for="item in trustLinks" :key="item.to" :to="item.to">
            {{ item.label }}
          </RouterLink>
        </nav>
        <p>{{ t('legal.footerBoundary') }}</p>
      </footer>
    </div>

    <nav class="mobile-nav" :aria-label="t('accessibility.mobileNavigation')">
      <RouterLink v-for="item in mobileNav" :key="item.key" :to="item.to" :class="{ active: active(item.key) }" :aria-current="active(item.key) ? 'page' : undefined">
        <component :is="item.icon" :size="18" :stroke-width="1.75" />
        <span>{{ item.label }}</span>
      </RouterLink>
      <RouterLink to="/settings" :class="{ active: ['settings', 'notifications'].includes(String(route.name)) }" :aria-current="['settings', 'notifications'].includes(String(route.name)) ? 'page' : undefined">
        <CircleUserRound :size="18" :stroke-width="1.75" />
        <span>{{ t('nav.account') }}</span>
      </RouterLink>
    </nav>
  </div>
</template>
