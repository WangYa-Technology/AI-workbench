<script setup lang="ts">
import { Bell, CircleUserRound, ClipboardList, Compass, Headphones, Images, Languages, ListChecks, Moon, PanelLeftClose, PanelLeftOpen, ReceiptText, Search, ShieldAlert, ShoppingBag, Store, Sun, Upload, UsersRound, WalletCards, WandSparkles } from 'lucide-vue-next'
import { computed, nextTick, onMounted, ref, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { usePreferencesStore } from '../../stores/preferences'
import { useNotificationsStore } from '../../stores/notifications'
import { useSessionStore } from '../../stores/session'
import BrandLogo from '../brand/BrandLogo.vue'
import UiButton from '../ui/UiButton.vue'
import UiIconButton from '../ui/UiIconButton.vue'
import UiInput from '../ui/UiInput.vue'

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
const isGuestHome = computed(() => route.name === 'home')
const brandTarget = computed(() => session.user ? '/discover' : '/')

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
  { key: 'purchases', label: t('workspace.purchases'), to: '/workspace/purchases', icon: ShoppingBag },
  { key: 'orders', label: t('workspace.orders'), to: '/workspace/orders', icon: ReceiptText },
  { key: 'taskDesk', label: t('workspace.tasks'), to: '/workspace/tasks', icon: ListChecks },
  { key: 'billing', label: t('workspace.billing'), to: '/workspace/billing', icon: WalletCards },
  { key: 'publish', label: t('actions.publishWork'), to: '/publish', icon: Upload },
  { key: 'support', label: t('nav.support'), to: '/support', icon: Headphones },
])

const adminNav = computed(() => session.user?.permissions.includes('admin:access') ? [
  { key: 'admin', label: t('nav.operations'), to: '/admin', icon: ShieldAlert },
] : [])

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
  if (key === 'purchases') return name === 'workspace' && route.params.section === 'purchases'
  if (key === 'orders') return name === 'workspace' && route.params.section === 'orders'
  if (key === 'taskDesk') return name === 'workspace' && route.params.section === 'tasks'
  if (key === 'billing') return name === 'workspace' && route.params.section === 'billing'
  return name === key
}

watch(() => route.query.q, (value) => {
  searchQuery.value = String(value || '')
})

watch(() => route.path, async () => {
  if (!routeFocusReady) return
  await nextTick()
  await new Promise<void>((resolve) => globalThis.requestAnimationFrame(() => resolve()))
  const heading = mainContent.value?.querySelector('h1')?.textContent?.trim()
  routeAnnouncement.value = heading || t('accessibility.pageChanged')
  if (mainContent.value) mainContent.value.scrollTop = 0
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
  <div v-if="isGuestHome" class="guest-shell">
    <a class="skip-link" href="#main-content">{{ t('accessibility.skipToContent') }}</a>
    <p class="sr-only" aria-live="polite" aria-atomic="true">
      {{ routeAnnouncement }}
    </p>
    <main id="main-content" ref="mainContent" tabindex="-1">
      <RouterView />
    </main>
  </div>

  <div v-else class="app-shell" :class="{ 'is-sidebar-collapsed': preferences.sidebarCollapsed }">
    <a class="skip-link" href="#main-content">{{ t('accessibility.skipToContent') }}</a>
    <p class="sr-only" aria-live="polite" aria-atomic="true">
      {{ routeAnnouncement }}
    </p>
    <aside class="site-sidebar">
      <RouterLink class="brand" :to="brandTarget" :aria-label="t('brand')">
        <BrandLogo class="brand-mark" />
        <strong>{{ t('brand') }}</strong>
      </RouterLink>

      <nav id="primary-navigation" class="primary-nav" :aria-label="t('accessibility.primaryNavigation')">
        <section class="nav-section">
          <span class="nav-section-label">{{ t('nav.communityArea') }}</span>
          <RouterLink v-for="item in communityNav" :key="item.key" :to="item.to" :class="{ active: active(item.key) }" :aria-current="active(item.key) ? 'page' : undefined" :aria-label="preferences.sidebarCollapsed ? item.label : undefined" :title="preferences.sidebarCollapsed ? item.label : undefined">
            <component :is="item.icon" :size="18" :stroke-width="1.75" />
            <span>{{ item.label }}</span>
          </RouterLink>
        </section>
        <section class="nav-section">
          <span class="nav-section-label">{{ t('nav.workbench') }}</span>
          <RouterLink v-for="item in workbenchNav" :key="item.key" :to="item.to" :class="{ active: active(item.key) }" :aria-current="active(item.key) ? 'page' : undefined" :aria-label="preferences.sidebarCollapsed ? item.label : undefined" :title="preferences.sidebarCollapsed ? item.label : undefined">
            <component :is="item.icon" :size="18" :stroke-width="1.75" />
            <span>{{ item.label }}</span>
          </RouterLink>
        </section>
        <section v-if="adminNav.length" class="nav-section nav-section-admin">
          <span class="nav-section-label">{{ t('nav.operationsArea') }}</span>
          <RouterLink v-for="item in adminNav" :key="item.key" :to="item.to" :class="{ active: active(item.key) }" :aria-current="active(item.key) ? 'page' : undefined" :aria-label="preferences.sidebarCollapsed ? item.label : undefined" :title="preferences.sidebarCollapsed ? item.label : undefined">
            <component :is="item.icon" :size="18" :stroke-width="1.75" />
            <span>{{ item.label }}</span>
          </RouterLink>
        </section>
      </nav>

      <UiIconButton
        class="sidebar-collapse-control"
        :label="t(preferences.sidebarCollapsed ? 'actions.expandSidebar' : 'actions.collapseSidebar')"
        aria-controls="primary-navigation"
        :aria-expanded="!preferences.sidebarCollapsed"
        @click="preferences.toggleSidebar"
      >
        <span class="t-icon-swap" :data-state="preferences.sidebarCollapsed ? 'a' : 'b'" aria-hidden="true">
          <span class="t-icon" data-icon="a"><PanelLeftOpen :size="18" :stroke-width="1.75" /></span>
          <span class="t-icon" data-icon="b"><PanelLeftClose :size="18" :stroke-width="1.75" /></span>
        </span>
      </UiIconButton>
    </aside>

    <div class="app-main" :class="{ 'is-create-route': route.name === 'create' }">
      <header class="site-header">
        <RouterLink class="mobile-brand" :to="brandTarget" :aria-label="t('brand')">
          <BrandLogo class="brand-mark" />
          <strong>{{ t('brand') }}</strong>
        </RouterLink>

        <div class="search-stack">
          <form class="global-search" role="search" @submit.prevent="submitSearch">
            <UiInput v-model="searchQuery" class="global-search-input" type="search" :placeholder="t('actions.searchPlaceholder')" :aria-label="t('actions.search')" />
            <UiIconButton class="global-search-submit" type="submit" :label="t('actions.search')">
              <Search :size="18" :stroke-width="1.75" />
            </UiIconButton>
          </form>
          <p class="quick-links-context">
            {{ t('actions.explore') }}
          </p>
        </div>

        <div class="header-actions">
          <UiButton as="RouterLink" class="command-button secondary" variant="secondary" to="/publish">
            <template #start><Upload :size="17" :stroke-width="1.75" /></template>
            {{ t('actions.publish') }}
          </UiButton>
          <UiIconButton as="RouterLink" class="icon-button notification-action" to="/notifications" :label="t('actions.notifications')">
            <Bell :size="19" :stroke-width="1.75" />
            <span class="t-badge" :data-open="String(Boolean(notifications.unreadCount))">
              <span class="t-badge-dot notification-badge" :aria-label="notifications.unreadCount ? t('notifications.unreadCount', { count: notifications.unreadCount }) : undefined">{{ notifications.unreadCount ? (notifications.unreadCount > 99 ? '99+' : notifications.unreadCount) : '' }}</span>
            </span>
          </UiIconButton>
          <UiIconButton class="icon-button desktop-utility" :label="t('actions.language')" @click="preferences.toggleLocale">
            <Languages :size="18" :stroke-width="1.75" />
          </UiIconButton>
          <UiIconButton class="icon-button desktop-utility" :label="t('actions.theme')" @click="preferences.toggleTheme">
            <Sun v-if="preferences.resolvedTheme === 'dark'" :size="18" :stroke-width="1.75" />
            <Moon v-else :size="18" :stroke-width="1.75" />
          </UiIconButton>
          <UiIconButton as="RouterLink" class="icon-button account-action" to="/settings" :label="session.user?.displayName || t('actions.account')">
            <CircleUserRound :size="19" :stroke-width="1.75" />
          </UiIconButton>
        </div>
      </header>

      <main id="main-content" ref="mainContent" tabindex="-1">
        <RouterView />
      </main>

      <footer v-if="route.name === 'create'" class="site-footer creation-site-footer">
        <p>{{ t('create.footerDisclaimer') }}</p>
      </footer>

      <footer v-else class="site-footer">
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
