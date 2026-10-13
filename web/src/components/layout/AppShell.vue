<script setup lang="ts">
import UiSidebarGroup from '../ui/UiSidebarGroup.vue'
import UiSidebarItem from '../ui/UiSidebarItem.vue'
import { ArrowLeft, Bell, CircleUserRound, ClipboardList, Compass, Globe2, Headphones, Images, Languages, ListChecks, Moon, PanelLeftClose, PanelLeftOpen, ReceiptText, Search, ShieldAlert, ShoppingBag, Store, Sun, UsersRound, WalletCards, WandSparkles, X } from 'lucide-vue-next'
import { computed, nextTick, onMounted, ref, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { usePreferencesStore } from '../../stores/preferences'
import { useNotificationsStore } from '../../stores/notifications'
import { useSessionStore } from '../../stores/session'
import { useSiteConfigStore } from '../../stores/siteConfig'
import PageContextBar from './PageContextBar.vue'
import { adminNavigationItems } from '../../lib/admin-navigation'
import BrandLogo from '../brand/BrandLogo.vue'
import MarkdownContent from '../ui/MarkdownContent.vue'
import UiAnnouncement from '../ui/UiAnnouncement.vue'
import UiIconButton from '../ui/UiIconButton.vue'
import UiInput from '../ui/UiInput.vue'
import UiSelect from '../ui/UiSelect.vue'
import { textLength } from '../../lib/unicodeText'
import { catalogSearchScopes } from '../catalog/searchScopes'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const preferences = usePreferencesStore()
const session = useSessionStore()
const siteConfig = useSiteConfigStore()
const notifications = useNotificationsStore()
const searchQuery = ref(String(route.query.q || ''))
const searchContext = computed(() => catalogSearchScopes.find(scope => scope.route === route.name && (!scope.matches || scope.matches(route))))
const localSearchScope = computed(() => searchContext.value?.value || 'all')
const searchScope = ref(localSearchScope.value)
const mobileSearchOpen = ref(false)
const searchInvalid = ref(false)
const searchInput = useTemplateRef('searchInput')
const searchesCatalog = computed(() => Boolean(searchContext.value) && searchScope.value === localSearchScope.value)
const searchLabel = computed(() => searchesCatalog.value ? searchContext.value!.label : 'search.fullSite')
const searchPlaceholder = computed(() => searchesCatalog.value ? searchContext.value!.placeholder : 'search.sitePlaceholder')
const searchInputLabel = computed(() => searchesCatalog.value ? searchContext.value!.inputLabel : 'actions.search')
const searchError = computed(() => searchesCatalog.value ? 'inspiration.queryTooLong' : 'inspiration.searchLength')

watch(localSearchScope, value => { searchScope.value = value })
watch([searchQuery, searchScope], () => { searchInvalid.value = false })
watch(() => route.fullPath, () => {
  searchQuery.value = String(route.query.q || '')
  searchInvalid.value = false
})

async function toggleMobileSearch() {
  mobileSearchOpen.value = !mobileSearchOpen.value
  if (mobileSearchOpen.value) {
    await nextTick()
    searchInput.value?.focus()
  }
}

function closeMobileSearch() {
  if (!mobileSearchOpen.value) return
  mobileSearchOpen.value = false
  globalThis.document.getElementById('mobile-search-toggle')?.focus()
}
const mainContent = useTemplateRef('mainContent')
const routeAnnouncement = ref('')
const authAnnouncementDismissed = ref(false)
const authAnnouncementStorageKey = 'hcai-account-announcement-dismissed'
let routeFocusReady = false
const isGuestHome = computed(() => route.name === 'home')
const isAuthPage = computed(() => route.name === 'auth' || (route.name === 'settings' && !session.user))
const isAdminRoute = computed(() => route.name === 'admin')
const brandTarget = computed(() => session.user ? '/discover' : '/')
const showAuthAnnouncement = computed(() => session.initialized && !session.user && !authAnnouncementDismissed.value)

onMounted(async () => {
  try {
    authAnnouncementDismissed.value = globalThis.localStorage.getItem(authAnnouncementStorageKey) === '1'
  } catch {
    authAnnouncementDismissed.value = false
  }
  const [user] = await Promise.all([session.ensure(), siteConfig.ensure()])
  if (user) await notifications.refreshCount()
})

function dismissAuthAnnouncement() {
  authAnnouncementDismissed.value = true
  try {
    globalThis.localStorage.setItem(authAnnouncementStorageKey, '1')
  } catch {
    // Private browsing can deny storage access; the in-memory dismissal still applies.
  }
}

function openAuthFromAnnouncement() {
  void router.push({ path: '/auth', query: { auth: 'login', returnTo: route.fullPath } })
}

watch(() => [siteConfig.current.siteName, siteConfig.current.siteIconUrl] as const, ([siteName, siteIconUrl]) => {
  const document = globalThis.document
  document.title = siteName
  document.querySelector('link[rel="icon"]')?.setAttribute('href', siteIconUrl)
  document.querySelector('link[rel="apple-touch-icon"]')?.setAttribute('href', siteIconUrl)
}, { immediate: true })

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
  { key: 'support', label: t('nav.support'), to: '/support', icon: Headphones },
])

const availableAdminNavigation = computed(() => adminNavigationItems.filter(item => session.user?.permissions.includes(item.permission)))
const activeAdminTab = computed(() => {
  const requested = String(route.query.tab || 'overview')
  return availableAdminNavigation.value.some(item => item.tab === requested)
    ? requested
    : availableAdminNavigation.value[0]?.tab || 'overview'
})
const adminNav = computed(() => {
  if (!session.user?.permissions.includes('admin:access')) return []
  if (!isAdminRoute.value) return [
    { key: 'admin', label: t('nav.operations'), to: '/admin', icon: ShieldAlert },
  ]
  return availableAdminNavigation.value.map(item => ({
    key: `admin:${item.tab}`,
    label: t(`admin.tabs.${item.tab}`),
    to: { path: '/admin', query: { tab: item.tab } },
    icon: item.icon,
  }))
})

const sidebarGroups = computed(() => [
  ...(!isAdminRoute.value ? [
    { key: 'community', label: t('nav.communityArea'), items: communityNav.value },
    { key: 'workbench', label: t('nav.workbench'), items: workbenchNav.value },
  ] : []),
  { key: 'admin', label: t('nav.operationsArea'), items: adminNav.value },
].filter(group => group.items.length).map(group => ({
  ...group,
  items: group.items.map(item => ({ ...item, active: active(item.key) })),
})))

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
  if (key.startsWith('admin:')) return name === 'admin' && activeAdminTab.value === key.slice(6)
  return name === key
}

watch(() => route.path, async () => {
  mobileSearchOpen.value = false
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
  if (searchesCatalog.value) {
    if (searchContext.value?.maxLength && textLength(q) > searchContext.value.maxLength) {
      searchInvalid.value = true
      searchInput.value?.focus()
      return
    }
    searchQuery.value = q
    void router.push({ path: route.path, query: { ...route.query, q: q || undefined } })
    return
  }
  if (textLength(q) < 2 || textLength(q) > 120) {
    searchInvalid.value = true
    searchInput.value?.focus()
    return
  }
  void router.push({ path: '/search', query: { q } })
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

  <div v-else-if="isAuthPage" class="auth-shell">
    <a class="skip-link" href="#main-content">{{ t('accessibility.skipToContent') }}</a>
    <p class="sr-only" aria-live="polite" aria-atomic="true">
      {{ routeAnnouncement }}
    </p>
    <main id="main-content" ref="mainContent" tabindex="-1">
      <RouterView />
    </main>
  </div>

  <div v-else class="app-shell" :class="{ 'is-sidebar-collapsed': preferences.sidebarCollapsed, 'is-admin-route': isAdminRoute }">
    <a class="skip-link" href="#main-content">{{ t('accessibility.skipToContent') }}</a>
    <p class="sr-only" aria-live="polite" aria-atomic="true">
      {{ routeAnnouncement }}
    </p>
    <aside class="site-sidebar">
      <RouterLink class="brand" :to="brandTarget" :aria-label="siteConfig.current.siteName">
        <BrandLogo class="brand-mark" />
        <strong>{{ siteConfig.current.siteName }}</strong>
      </RouterLink>

      <nav id="primary-navigation" class="primary-nav" :class="{ 'is-admin-navigation': isAdminRoute }" :aria-label="t('accessibility.primaryNavigation')">
        <UiSidebarItem v-if="isAdminRoute" class="admin-sidebar-return" variant="back" to="/discover" :icon="ArrowLeft" :label="t('nav.backToWorkspace')" :collapsed="preferences.sidebarCollapsed" />
        <UiSidebarGroup v-for="group in sidebarGroups" :key="group.key" :class="{ 'nav-section-admin': group.key === 'admin' }" :label="group.label" :items="group.items" :collapsed="preferences.sidebarCollapsed" />
      </nav>

      <UiAnnouncement
        v-if="showAuthAnnouncement"
        class="sidebar-announcement"
        :title="t('nav.accountAnnouncement.title')"
        :description="t('nav.accountAnnouncement.description')"
        :action-label="t('nav.accountAnnouncement.action')"
        :dismiss-label="t('actions.close')"
        dismissible
        @action="openAuthFromAnnouncement"
        @dismiss="dismissAuthAnnouncement"
      />

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

    <div class="app-main" :class="{ 'is-create-route': route.name === 'create', 'is-admin-route': isAdminRoute }">
      <header class="site-header">
        <RouterLink class="mobile-brand" :to="brandTarget" :aria-label="siteConfig.current.siteName">
          <BrandLogo class="brand-mark" />
          <strong>{{ siteConfig.current.siteName }}</strong>
        </RouterLink>

        <div v-if="isAdminRoute" id="admin-header-slot" class="admin-header-slot"></div>

        <div v-else id="header-search" class="search-stack" :class="{ 'is-mobile-open': mobileSearchOpen }" @keydown.esc="closeMobileSearch">
          <form class="global-search" role="search" :aria-label="t(searchLabel)" @submit.prevent="submitSearch">
            <UiSelect v-if="localSearchScope !== 'all'" v-model="searchScope" class="global-search-scope" size="sm" :align-item-with-trigger="false" :aria-label="t('search.scope')">
              <template #start>
                <component :is="searchContext?.icon" v-if="searchesCatalog" :size="15" :stroke-width="1.75" aria-hidden="true" />
                <Globe2 v-else :size="15" :stroke-width="1.75" aria-hidden="true" />
              </template>
              <option v-if="searchContext" :value="searchContext.value">
                {{ t(searchContext.label) }}
              </option>
              <option value="all">
                {{ t('search.fullSite') }}
              </option>
            </UiSelect>
            <UiInput ref="searchInput" v-model="searchQuery" class="global-search-input" size="sm" type="search" :placeholder="t(searchPlaceholder)" :aria-label="t(searchInputLabel)" :aria-invalid="searchInvalid || undefined" :aria-describedby="searchInvalid ? 'header-search-error' : undefined" />
            <UiIconButton class="global-search-submit" type="submit" :label="t('actions.search')">
              <Search :size="18" :stroke-width="1.75" />
            </UiIconButton>
          </form>
          <p v-if="searchInvalid" id="header-search-error" class="global-search-error" role="alert">
            {{ t(searchError) }}
          </p>
        </div>

        <div class="header-actions">
          <UiIconButton v-if="!isAdminRoute" id="mobile-search-toggle" class="mobile-search-toggle" size="sm" :label="t(mobileSearchOpen ? 'search.closeSearch' : 'search.openSearch')" aria-controls="header-search" :aria-expanded="mobileSearchOpen" @click="toggleMobileSearch">
            <X v-if="mobileSearchOpen" :size="19" />
            <Search v-else :size="19" />
          </UiIconButton>
          <UiIconButton as="RouterLink" class="notification-action" size="sm" to="/notifications" :label="t('actions.notifications')">
            <Bell :size="19" :stroke-width="1.75" />
            <span class="t-badge" :data-open="String(Boolean(notifications.unreadCount))">
              <span class="t-badge-dot notification-badge" :aria-label="notifications.unreadCount ? t('notifications.unreadCount', { count: notifications.unreadCount }) : undefined">{{ notifications.unreadCount ? (notifications.unreadCount > 99 ? '99+' : notifications.unreadCount) : '' }}</span>
            </span>
          </UiIconButton>
          <UiIconButton class="desktop-utility" size="sm" :label="t('actions.language')" @click="preferences.toggleLocale">
            <Languages :size="18" :stroke-width="1.75" />
          </UiIconButton>
          <UiIconButton class="desktop-utility" size="sm" :label="t('actions.theme')" @click="preferences.toggleTheme">
            <Sun v-if="preferences.resolvedTheme === 'dark'" :size="18" :stroke-width="1.75" />
            <Moon v-else :size="18" :stroke-width="1.75" />
          </UiIconButton>
          <UiIconButton as="RouterLink" class="account-action" size="sm" :to="session.user ? '/settings' : { path: '/auth', query: { returnTo: route.fullPath } }" :label="session.user?.displayName || t('actions.account')">
            <CircleUserRound :size="19" :stroke-width="1.75" />
          </UiIconButton>
        </div>
      </header>

      <main id="main-content" ref="mainContent" tabindex="-1">
        <PageContextBar :root="mainContent" :disabled="isAdminRoute || route.name === 'create'" />
        <RouterView />
      </main>

      <footer v-if="route.name === 'create'" class="site-footer creation-site-footer">
        <p>{{ t('create.footerDisclaimer') }}</p>
        <MarkdownContent class="site-footer-content" inline :source="siteConfig.footerText" />
      </footer>

      <footer v-else class="site-footer">
        <nav :aria-label="t('legal.footerLabel')">
          <RouterLink v-for="item in trustLinks" :key="item.to" :to="item.to">
            {{ item.label }}
          </RouterLink>
        </nav>
        <MarkdownContent class="site-footer-content" inline :source="siteConfig.footerText" />
      </footer>
    </div>

    <nav v-if="!isAuthPage" class="mobile-nav" :aria-label="t('accessibility.mobileNavigation')">
      <RouterLink v-for="item in mobileNav" :key="item.key" :to="item.to" :class="{ active: active(item.key) }" :aria-current="active(item.key) ? 'page' : undefined">
        <component :is="item.icon" :size="18" :stroke-width="1.75" />
        <span>{{ item.label }}</span>
      </RouterLink>
      <RouterLink :to="session.user ? '/settings' : { path: '/auth', query: { returnTo: route.fullPath } }" :class="{ active: ['settings', 'notifications', 'auth'].includes(String(route.name)) }" :aria-current="['settings', 'notifications', 'auth'].includes(String(route.name)) ? 'page' : undefined">
        <CircleUserRound :size="18" :stroke-width="1.75" />
        <span>{{ t('nav.account') }}</span>
      </RouterLink>
    </nav>
  </div>
</template>

<style scoped>
.search-stack { position: relative; }
.global-search { display: flex; align-items: center; gap: 2px; height: 40px; padding: 4px; border-radius: 12px; background: var(--surface); box-shadow: var(--shadow-xs); }
.global-search .global-search-input {
  --field-focus-background: transparent;
  --field-focus-border: transparent;
  --field-focus-shadow: none;
  flex: 1; min-width: 0; height: 32px; padding: 0 8px 0 12px; border: 0; background: transparent; box-shadow: none; color: var(--text); font-size: 14px; font-weight: 400;
}
.global-search .global-search-input:focus { outline: none; background: transparent; border-color: transparent; box-shadow: none; }
.global-search .global-search-input::placeholder { color: var(--text-secondary); opacity: 1; }
.global-search-scope { position: relative; flex: 0 0 auto; width: auto; max-width: 160px; padding-right: 4px; }
.global-search-scope::after { content: ''; position: absolute; right: 0; top: 8px; bottom: 8px; width: 1px; background: var(--border); pointer-events: none; }
.global-search-scope :deep(.ui-select__trigger) { min-height: 32px; height: 32px; gap: 4px; padding: 0 4px 0 6px; border: 0; border-radius: 6px; background: transparent; box-shadow: none; color: var(--text-secondary); font-size: 13px; font-weight: 400; transition: none; }
.global-search-scope :deep(.ui-select__start) { color: inherit; }
.global-search-scope :deep(.ui-select__trigger:hover),
.global-search-scope :deep(.ui-select__trigger[aria-expanded='true']) { color: var(--text); background: transparent; }
.global-search-scope :deep(.ui-select__trigger:focus-visible) { text-decoration: underline; text-underline-offset: 4px; }
.global-search-scope :deep(.ui-select__chevron) { width: 14px; height: 14px; margin-left: 0; color: inherit; }
.global-search .global-search-submit { --ui-size: 32px; position: static; flex: 0 0 32px; border-radius: 8px; color: var(--text-secondary); }
.global-search .global-search-submit:hover { color: var(--text); background: var(--surface-muted); }
.global-search-error { position: absolute; top: calc(100% + 6px); left: 0; right: 0; margin: 0; padding: 8px 12px; border-radius: var(--radius-control); background: var(--surface); color: var(--danger); box-shadow: var(--shadow-sm); font-size: 12px; }
.header-actions .mobile-search-toggle { display: none; }
@media (min-width: 1024px) {
  .search-stack {
    position: absolute;
    grid-column: auto;
    /* Header width follows the sidebar animation; keep the viewport center fixed. */
    left: calc(100% - 50vw);
    top: 50%;
    width: min(540px, calc(100% - 360px));
    transform: translate(-50%, -50%);
  }
}
@media (max-width: 1023px) {
  .header-actions .mobile-search-toggle { display: inline-flex; }
  .search-stack.is-mobile-open { display: grid; position: absolute; top: 100%; left: 8px; right: 8px; padding: 10px; border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--surface); box-shadow: var(--shadow-sm); }
  .global-search { height: 44px; }
  .global-search .global-search-input { height: 36px; padding-left: 8px; font-size: 16px; }
  .global-search-scope :deep(.ui-select__trigger) { height: 36px; padding-inline: 6px; }
  .global-search-error { position: static; margin-top: 6px; padding: 4px 0; box-shadow: none; }
}
</style>
