<script setup lang="ts">
import { ArrowRight, ArrowUpRight, BriefcaseBusiness, FileCheck2, Languages, Layers3, Moon, PackageOpen, PenTool, Sun, Users } from 'lucide-vue-next'
import { computed, onMounted, onUnmounted, ref, type Directive } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRouter } from 'vue-router'
import { api, type Product, type TaskSummary, type Work } from '../api/client'
import AssetMedia from '../components/domain/AssetMedia.vue'
import BrandLogo from '../components/brand/BrandLogo.vue'
import { formatCurrency } from '../lib/format'
import { usePreferencesStore } from '../stores/preferences'
import { useSessionStore } from '../stores/session'
import { useSiteConfigStore } from '../stores/siteConfig'
import UiButton from '../components/ui/UiButton.vue'
import UiCollapsible from '../components/ui/UiCollapsible.vue'
import UiIconButton from '../components/ui/UiIconButton.vue'

// Reveal motion never hides content or reduces text contrast, including during loading.
const observers = new WeakMap<globalThis.HTMLElement, globalThis.IntersectionObserver>()
const vReveal: Directive<globalThis.HTMLElement> = {
  mounted(element) {
    if (!('IntersectionObserver' in globalThis) || globalThis.matchMedia('(prefers-reduced-motion: reduce)').matches) return
    const observer = new globalThis.IntersectionObserver(entries => {
      if (!entries.some(entry => entry.isIntersecting)) return
      element.classList.add('home-entered')
      observer.disconnect()
      observers.delete(element)
    }, { threshold: 0.1 })
    observers.set(element, observer)
    observer.observe(element)
  },
  unmounted(element) { observers.get(element)?.disconnect(); observers.delete(element) },
}
const { t, locale } = useI18n()
const router = useRouter()
const session = useSessionStore()
const preferences = usePreferencesStore()
const siteConfig = useSiteConfigStore()
const works = ref<Work[]>([])
const tasks = ref<TaskSummary[]>([])
const products = ref<Product[]>([])
const catalogs = ref<'loading' | 'ready'>('ready')
const failedCatalogs = ref<string[]>([])
const headerScrolled = ref(false)
let headerScrollFrame = 0
const syncHeaderState = (event?: globalThis.Event) => {
  const scrollY = event instanceof globalThis.CustomEvent ? event.detail.top as number : globalThis.scrollY
  if (!headerScrolled.value && scrollY > 48) headerScrolled.value = true
  else if (headerScrolled.value && scrollY < 12) headerScrolled.value = false
}
const updateHeaderState = () => {
  if (headerScrollFrame) return
  headerScrollFrame = globalThis.requestAnimationFrame(() => {
    headerScrollFrame = 0
    syncHeaderState()
  })
}
const publishDestination = '/market/demands?publish=1'
const publishTarget = computed(() => session.user ? publishDestination : {
  path: '/auth', query: { auth: 'login', returnTo: publishDestination },
})
const openTasks = computed(() => tasks.value.filter(task => task.status === 'open').slice(0, 3))
const audiences = [
  { key: 'commission', icon: BriefcaseBusiness },
  { key: 'projects', icon: Users },
  { key: 'resources', icon: PackageOpen },
]
const faqOpen = ref<Record<string, boolean>>({})
const processSteps = ['brief', 'collaborate', 'deliver', 'accept']
function deadline(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '' : new Intl.DateTimeFormat(locale.value, { month: 'short', day: 'numeric' }).format(date)
}
async function loadCatalogs() {
  if (catalogs.value === 'loading') return
  catalogs.value = 'loading'
  failedCatalogs.value = []
  const results = await Promise.allSettled([api.listTasks({ status: 'open', limit: 3 }), api.listProducts({ limit: 4 }), api.listWorks()])
  const [taskPage, productPage, workPage] = results
  if (taskPage.status === 'fulfilled') tasks.value = taskPage.value.items
  else failedCatalogs.value.push('tasks')
  if (productPage.status === 'fulfilled') products.value = productPage.value.items.slice(0, 4)
  else failedCatalogs.value.push('products')
  if (workPage.status === 'fulfilled') works.value = workPage.value.items.slice(0, 3)
  else failedCatalogs.value.push('works')
  catalogs.value = 'ready'
}
onMounted(async () => {
  updateHeaderState()
  globalThis.addEventListener('scroll', updateHeaderState, { passive: true })
  globalThis.addEventListener('public-page-snapshot', syncHeaderState)
  if (await session.ensure()) { await router.replace('/discover'); return }
  await loadCatalogs()
})
onUnmounted(() => {
  globalThis.removeEventListener('scroll', updateHeaderState)
  globalThis.removeEventListener('public-page-snapshot', syncHeaderState)
  if (headerScrollFrame) globalThis.cancelAnimationFrame(headerScrollFrame)
})
</script>

<template>
  <div class="guest-home" :data-home-theme="preferences.resolvedTheme">
    <header :class="['home-header home-width', { 'home-header-scrolled': headerScrolled }]">
      <RouterLink class="home-brand" to="/" :aria-label="siteConfig.current.siteName">
        <BrandLogo class="brand-mark" /><strong>{{ siteConfig.current.siteName }}</strong>
      </RouterLink>
      <nav class="home-nav" :aria-label="t('accessibility.primaryNavigation')">
        <RouterLink to="/market/demands">
          {{ t('nav.tasks') }}
        </RouterLink><RouterLink to="/market">
          {{ t('home.business.nav.resources') }}
        </RouterLink><RouterLink to="/discover">
          {{ t('home.nav.work') }}
        </RouterLink><RouterLink to="/community">
          {{ t('home.nav.community') }}
        </RouterLink>
      </nav>
      <div class="home-actions">
        <div class="home-action-tools">
          <UiIconButton class="home-action-tool" :label="t('actions.language')" @click="preferences.toggleLocale">
            <Languages :size="15" />
          </UiIconButton>
          <UiIconButton class="home-action-tool" :label="t('actions.theme')" @click="preferences.toggleTheme">
            <Sun v-if="preferences.resolvedTheme === 'dark'" :size="15" /><Moon v-else :size="15" />
          </UiIconButton>
        </div>
        <RouterLink class="home-login home-button" :to="{ path: '/auth', query: { auth: 'login', returnTo: '/' } }">
          {{ t('home.signIn') }}
        </RouterLink>
      </div>
    </header>

    <section class="home-hero home-width">
      <div class="home-hero-copy">
        <p class="home-kicker">
          <span></span>{{ t('home.business.eyebrow') }}
        </p>
        <h1>{{ t('home.business.title') }}<em>{{ t('home.business.titleEnd') }}</em></h1>
        <p class="home-hero-summary">
          {{ t('home.business.summary') }}
        </p>
        <div class="home-hero-links">
          <UiButton as="RouterLink" class="home-button home-publish" :to="publishTarget">
            {{ t('home.business.publish') }}<ArrowUpRight :size="18" aria-hidden="true" />
          </UiButton><UiButton as="RouterLink" variant="outline" class="home-button home-button-secondary" to="/market/demands">
            {{ t('home.business.find') }}<ArrowRight :size="18" aria-hidden="true" />
          </UiButton>
        </div>
        <p class="home-resource-note">
          <span>{{ t('home.business.readyMadePrompt') }}</span>
          <RouterLink class="home-resource-link" to="/market">
            <span>{{ t('home.business.readyMadeAction') }}</span><ArrowRight :size="15" aria-hidden="true" />
          </RouterLink>
        </p>
      </div>
      <div class="home-project-board" :aria-label="t('home.business.boardLabel')">
        <div class="home-board-top">
          <span>{{ t('home.business.boardLabel') }}</span><Layers3 :size="20" aria-hidden="true" />
        </div>
        <div class="home-board-body">
          <div class="home-board-title">
            <BriefcaseBusiness :size="25" aria-hidden="true" /><h2>{{ t('home.business.boardTitle') }}</h2>
          </div>
          <p>{{ t('home.business.boardSummary') }}</p>
          <div class="home-brief-fields">
            <span>{{ t('home.business.scope') }}</span><span>{{ t('home.business.budget') }}</span><span>{{ t('home.business.deadline') }}</span>
          </div>
          <ol class="home-board-flow">
            <li v-for="(step, index) in processSteps" :key="step">
              <span class="home-step-dot">{{ index + 1 }}</span><span>{{ t(`home.business.steps.${step}.short`) }}</span>
            </li>
          </ol>
        </div>
        <div class="home-board-bottom">
          <FileCheck2 :size="22" aria-hidden="true" /><div><strong>{{ t('home.business.handoff') }}</strong><span>{{ t('home.business.handoffSummary') }}</span></div>
        </div>
      </div>
    </section>

    <section v-reveal class="home-pathways home-width" :aria-label="t('home.business.pathsLabel')">
      <article v-for="(item, index) in audiences" :key="item.key" :class="['home-pathway', `home-pathway-${item.key}`]">
        <div class="home-pathway-top">
          <component :is="item.icon" :size="24" aria-hidden="true" /><span>0{{ index + 1 }}</span>
        </div><h2>{{ t(`home.business.paths.${item.key}.title`) }}</h2><p>{{ t(`home.business.paths.${item.key}.summary`) }}</p>
      </article>
    </section>

    <div v-if="catalogs === 'loading'" class="home-catalog-notice home-width" role="status">
      {{ t('home.business.loading') }}
    </div>
    <div v-else-if="failedCatalogs.length" class="home-catalog-notice home-width" role="status">
      <span>{{ t('home.business.loadError') }}</span><UiButton variant="ghost" @click="loadCatalogs">
        {{ t('home.business.retry') }}
      </UiButton>
    </div>

    <section v-if="openTasks.length" v-reveal class="home-live-tasks home-section home-width" aria-labelledby="home-projects-title">
      <header class="home-section-heading">
        <div>
          <p class="home-kicker">
            {{ t('home.business.projectsLabel') }}
          </p><h2 id="home-projects-title">
            {{ t('home.business.projectsTitle') }}
          </h2>
        </div><RouterLink class="home-text-link" to="/market/demands">
          {{ t('home.business.allProjects') }}<ArrowUpRight :size="17" aria-hidden="true" />
        </RouterLink>
      </header>
      <RouterLink v-for="task in openTasks" :key="task.id" class="home-task" :to="`/market/demands/${task.id}`">
        <div><h3>{{ task.title }}</h3><p>{{ task.summary }}</p><span>{{ task.client?.displayName }}<template v-if="task.deadline"> · {{ t('home.business.due') }} {{ deadline(task.deadline) }}</template></span></div><div class="home-task-budget">
          <span>{{ t('home.business.budget') }}</span><strong>{{ formatCurrency(task.budgetCents, task.currency, locale) }}</strong>
        </div><ArrowUpRight :size="20" aria-hidden="true" />
      </RouterLink>
    </section>

    <section v-if="products.length" v-reveal class="home-products home-section home-width" aria-labelledby="home-products-title">
      <header class="home-section-heading">
        <div>
          <p class="home-kicker">
            {{ t('home.business.resourcesLabel') }}
          </p><h2 id="home-products-title">
            {{ t('home.business.resourcesTitle') }}
          </h2>
        </div><RouterLink class="home-text-link" to="/market">
          {{ t('home.business.allResources') }}<ArrowUpRight :size="17" aria-hidden="true" />
        </RouterLink>
      </header>
      <div class="home-product-grid">
        <RouterLink v-for="product in products" :key="product.id" class="home-product" :to="`/market/assets/${product.id}`">
          <AssetMedia v-if="product.mediaUrl" :src="product.mediaUrl" :kind="product.mediaKind" :alt="product.title" :width="product.width || 1000" :height="product.height || 800" :controls="false" /><div v-else class="home-product-placeholder">
            <PackageOpen :size="38" aria-hidden="true" />
          </div><div class="home-product-copy">
            <h3>{{ product.title }}</h3><strong>{{ formatCurrency(product.priceCents, product.currency, locale) }}</strong>
          </div><p>{{ product.seller.displayName }} · {{ product.license.name }}</p>
        </RouterLink>
      </div>
    </section>

    <section v-reveal class="home-process-wrap">
      <div class="home-process home-width">
        <header class="home-section-heading">
          <div>
            <p class="home-kicker">
              {{ t('home.business.processLabel') }}
            </p><h2>{{ t('home.business.processTitle') }}</h2>
          </div><p class="home-section-summary">
            {{ t('home.business.processSummary') }}
          </p>
        </header><ol class="home-process-grid">
          <li v-for="(step, index) in processSteps" :key="step">
            <span class="home-process-number">0{{ index + 1 }}</span><h3>{{ t(`home.business.steps.${step}.title`) }}</h3><p>{{ t(`home.business.steps.${step}.summary`) }}</p>
          </li>
        </ol><RouterLink class="home-text-link" :to="publishTarget">
          {{ t('home.business.publish') }}<ArrowUpRight :size="17" aria-hidden="true" />
        </RouterLink>
      </div>
    </section>

    <section v-if="works.length" v-reveal class="home-inspiration home-section home-width" aria-labelledby="home-works-title">
      <header class="home-section-heading">
        <div>
          <p class="home-kicker">
            {{ t('home.business.worksLabel') }}
          </p><h2 id="home-works-title">
            {{ t('home.business.worksTitle') }}
          </h2>
        </div><RouterLink class="home-text-link" to="/discover">
          {{ t('home.exploreWork') }}<ArrowUpRight :size="17" aria-hidden="true" />
        </RouterLink>
      </header><div class="home-work-grid">
        <RouterLink v-for="work in works" :key="work.id" class="home-work" :to="`/works/${work.id}`">
          <AssetMedia :src="work.mediaUrl" :kind="work.mediaKind" :alt="work.title" :width="work.width || 1000" :height="work.height || 800" :controls="false" /><h3>{{ work.title }}</h3><p>{{ work.author.displayName }}</p>
        </RouterLink>
      </div>
    </section>

    <section v-reveal class="home-supporting home-section home-width">
      <RouterLink class="home-community" to="/community">
        <Users :size="26" aria-hidden="true" /><div><h2>{{ t('home.business.communityTitle') }}</h2><p>{{ t('home.business.communitySummary') }}</p></div><ArrowUpRight :size="22" aria-hidden="true" />
      </RouterLink><RouterLink class="home-tool" to="/create/image">
        <PenTool :size="26" aria-hidden="true" /><div><h2>{{ t('home.business.toolTitle') }}</h2><p>{{ t('home.business.toolSummary') }}</p></div><ArrowUpRight :size="22" aria-hidden="true" />
      </RouterLink>
    </section>

    <section v-reveal class="home-faq home-width" aria-labelledby="home-faq-title">
      <div>
        <p class="home-kicker">
          {{ t('home.business.faqLabel') }}
        </p><h2 id="home-faq-title">
          {{ t('home.business.faqTitle') }}
        </h2><RouterLink class="home-text-link" to="/support">
          {{ t('nav.support') }}<ArrowUpRight :size="16" aria-hidden="true" />
        </RouterLink>
      </div><div>
        <UiCollapsible v-for="key in ['difference', 'ai', 'rights', 'payment']" :key="key" v-model:open="faqOpen[key]" :title="t(`home.business.faq.${key}.question`)">
          <p>
            {{ t(`home.business.faq.${key}.answer`) }}<RouterLink v-if="key === 'rights'" to="/policies/licensing">
              {{ t('legal.topics.licensing.title') }}
            </RouterLink>
          </p>
        </UiCollapsible>
      </div>
    </section>

    <footer class="home-footer home-width">
      <RouterLink class="home-brand" to="/">
        <BrandLogo class="brand-mark" /><strong>{{ siteConfig.current.siteName }}</strong>
      </RouterLink><p>{{ siteConfig.footerText }}</p><nav :aria-label="t('legal.footerLabel')">
        <RouterLink to="/policies/terms">
          {{ t('legal.topics.terms.title') }}
        </RouterLink><RouterLink to="/policies/privacy">
          {{ t('legal.topics.privacy.title') }}
        </RouterLink><RouterLink to="/policies/licensing">
          {{ t('legal.topics.licensing.title') }}
        </RouterLink><RouterLink to="/support">
          {{ t('nav.support') }}
        </RouterLink>
      </nav>
    </footer>
  </div>
</template>

<style scoped>
/* Share the application palette, including atmospheric surfaces. */
.guest-home { --neutral: var(--surface-neutral); --blue: var(--surface-cool); --teal: var(--surface-teal); --ink: var(--surface-ink); --home-heading: var(--ink); --motion-fast: 180ms; --motion-base: 280ms; --motion-reveal: 560ms; background: var(--canvas); color: var(--text); min-height: 100dvh; }
.guest-home[data-home-theme='dark'] { --home-heading: var(--text); }
.home-width { width: min(calc(100% - 80px), 1200px); margin-inline: auto; }
.home-header { position: sticky; top: 0; z-index: 20; min-height: 68px; display: flex; align-items: center; justify-content: space-between; gap: 20px; border: 1px solid transparent; border-bottom-color: var(--border); background: color-mix(in srgb, var(--canvas) 94%, transparent); transition: min-height var(--motion-base) cubic-bezier(.22,1,.36,1), width var(--motion-base) cubic-bezier(.22,1,.36,1), top var(--motion-base) cubic-bezier(.22,1,.36,1), border-radius var(--motion-base) ease, border-color var(--motion-fast) ease, box-shadow var(--motion-base) ease, padding-inline var(--motion-base) cubic-bezier(.22,1,.36,1); }
.home-header-scrolled { top: 12px; width: min(calc(100% - 48px), 820px); min-height: 52px; padding-inline: 14px; border-color: color-mix(in srgb, var(--border) 88%, var(--surface)); border-radius: 999px; background: color-mix(in srgb, var(--surface) 92%, transparent); box-shadow: 0 10px 26px rgb(32 44 59 / 12%); backdrop-filter: blur(16px); }
.home-brand { display: inline-flex; align-items: center; gap: 10px; color: var(--text); white-space: nowrap; font-size: 16px; }
.home-brand .brand-mark { width: 30px; height: 30px; border-radius: 7px; }
.home-nav, .home-actions { display: flex; align-items: center; gap: 21px; }
.home-header-scrolled .home-nav { gap: 18px; }
.home-nav { color: var(--text); font-size: 13px; font-weight: 500; }
.home-nav a { position: relative; transition: color var(--motion-fast) ease, transform var(--motion-fast) cubic-bezier(.22,1,.36,1); }
.home-nav a::after { content: ''; position: absolute; right: 0; bottom: -6px; left: 0; height: 1px; background: var(--accent); transform: scaleX(0); transform-origin: center; transition: transform var(--motion-fast) cubic-bezier(.22,1,.36,1); }
.home-nav a:hover { color: var(--text); transform: translateY(-1px); }
.home-nav a:hover::after { transform: scaleX(1); }
.home-actions { gap: 12px; }
.home-action-tools { display: inline-flex; align-items: center; gap: 2px; padding-right: 9px; border-right: 1px solid var(--border); }
.home-action-tool { --ui-size: 32px; border-radius: 7px; }
.home-login { min-height: 36px; padding: 8px 14px; gap: 8px; font-size: 13px; }
.home-button { display: inline-flex; align-items: center; justify-content: center; gap: 14px; min-height: 48px; padding: 12px 22px; border: 1px solid transparent; border-radius: var(--radius-button); background: var(--accent); color: var(--accent-contrast); font-size: 14px; font-weight: 650; }
.home-header .home-login { min-height: 36px; padding: 8px 14px; gap: 8px; border-radius: var(--radius-button); font-size: 13px; }
.home-header-scrolled .home-actions { gap: 8px; }
.home-header-scrolled .home-action-tools { padding-right: 6px; }
.home-header-scrolled .home-action-tool { --ui-size: 29px; }
.home-header-scrolled .home-nav { font-size: 12px; }
.home-header-scrolled .home-login { min-height: 32px; margin-right: -6px; padding: 7px 12px; border-radius: var(--radius-button); font-size: 12px; }
.home-button-secondary { border-color: var(--border); color: var(--text); background: transparent; }
.home-hero { display: grid; grid-template-columns: 1.1fr 1fr; gap: 72px; align-items: center; padding-block: 72px 64px; }
.home-hero-copy { min-width: 0; }
.home-kicker { display: flex; align-items: center; gap: 12px; margin: 0 0 20px; font-size: 12px; letter-spacing: .01em; font-weight: 550; color: var(--text-secondary); }
.home-kicker > span { width: 28px; height: 1px; border-radius: 0; background: var(--accent); }
h1 { margin: 0; font-size: clamp(36px, 4.1vw, 56px); font-weight: 560; line-height: 1.2; letter-spacing: -.025em; text-wrap: balance; }
h1 em { display: block; font-style: normal; color: var(--home-heading); }
.home-hero-summary { max-width: 490px; margin: 24px 0 28px; font-size: 16px; line-height: 1.85; color: var(--text-secondary); }
.home-hero-links { display: flex; gap: 12px; flex-wrap: wrap; }
.home-resource-note { display: flex; align-items: center; flex-wrap: wrap; gap: 0 8px; margin: 16px 0 0; color: var(--text-secondary); font-size: 14px; line-height: 1.6; }
.home-resource-link { display: inline-flex; align-items: center; gap: 6px; min-height: 32px; border-radius: 4px; color: var(--accent-readable); font-weight: 550; text-decoration: none; }
.home-resource-link > span { text-decoration: underline; text-decoration-color: transparent; text-underline-offset: 4px; transition: text-decoration-color var(--motion-fast) ease; }
.home-resource-link > svg { flex-shrink: 0; transition: transform var(--motion-fast) cubic-bezier(.22,1,.36,1); }
.home-resource-link:is(:hover, :focus-visible) > span { text-decoration-color: currentColor; }
.home-resource-link:is(:hover, :focus-visible) > svg { transform: translateX(3px); }
.home-project-board { position: relative; min-width: 0; border: 1px solid var(--border); border-radius: 24px; overflow: hidden; background: var(--surface); box-shadow: 0 8px 24px rgb(32 44 59 / 6%); }
.home-board-top { display: flex; justify-content: space-between; align-items: center; padding: 17px 24px; border-radius: 23px 23px 0 0; background: var(--ink); color: #e7edf5; font-size: 12px; }
.home-board-body { padding: 26px; } .home-board-title { display: flex; align-items: center; gap: 12px; } .home-board-title > svg { color: var(--accent-readable); }
.home-board-title h2 { text-wrap: balance; margin: 0; font-size: 23px; font-weight: 550; letter-spacing: -.025em; }
.home-board-body > p { margin: 12px 0 18px; font-size: 13px; line-height: 1.8; color: var(--text-secondary); }
.home-brief-fields { display: flex; flex-wrap: wrap; gap: 8px; } .home-brief-fields span { padding: 7px 12px; border: 1px solid var(--border); border-radius: 5px; font-size: 12px; color: var(--text-secondary); }
.home-board-flow { display: grid; grid-template-columns: repeat(4, 1fr); gap: 8px; margin: 28px 0 0; padding: 0; list-style: none; }
.home-board-flow li { position: relative; display: flex; flex-direction: column; align-items: flex-start; gap: 10px; font-size: 12px; }
.home-board-flow li:not(:last-child)::after { content: ''; position: absolute; top: 13px; left: 36px; right: 0; height: 1px; background: var(--border); transform-origin: left; animation: home-line 800ms 300ms ease-out both; }
.home-step-dot { width: 27px; height: 27px; display: grid; place-items: center; border-radius: 50%; background: var(--blue); color: var(--text); font-size: 11px; font-weight: 650; }
.home-board-flow li:last-child .home-step-dot { background: var(--teal); }
.home-board-bottom { display: flex; align-items: center; gap: 14px; padding: 19px 26px; background: var(--teal); border-radius: 0 0 23px 23px; } .home-board-bottom strong { display: block; font-size: 13px; } .home-board-bottom span { display: block; margin-top: 5px; font-size: 12px; color: var(--text-secondary); }
.home-pathways { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 18px; padding-block: 12px 64px; }
.home-pathway { display: flex; flex-direction: column; padding: 27px; border: 1px solid transparent; border-radius: 20px; color: var(--text); background: var(--neutral); }
.home-pathway-projects { background: var(--blue); } .home-pathway-resources { background: var(--teal); }
.home-pathway-top { display: flex; align-items: center; justify-content: space-between; margin-bottom: 26px; } .home-pathway-top > span { color: var(--text-secondary); font-size: 12px; font-variant-numeric: tabular-nums; }
.home-pathway h2 { margin: 0 0 12px; font-size: 22px; font-weight: 550; } .home-pathway p { margin: 0; color: var(--text-secondary); font-size: 14px; line-height: 1.8; }
.home-section { padding-block: 44px 56px; }
.home-section-heading { display: flex; justify-content: space-between; align-items: end; gap: 24px; margin-bottom: 28px; text-align: left; } .home-section-heading .home-kicker { margin-bottom: 12px; }
.home-section-heading h2, .home-faq h2 { margin: 0; font-size: clamp(25px, 2.5vw, 33px); font-weight: 550; letter-spacing: -.025em; line-height: 1.3; }
.home-text-link { display: inline-flex; align-items: center; gap: 8px; font-size: 13px; font-weight: 550; color: var(--accent-readable); }
.home-task { display: grid; grid-template-columns: 1fr auto auto; align-items: center; gap: 28px; padding: 24px 0; border-top: 1px solid var(--border); color: var(--text); }
.home-task h3 { margin: 0 0 8px; font-size: 18px; font-weight: 550; } .home-task p { margin: 0 0 12px; color: var(--text-secondary); font-size: 13px; line-height: 1.7; } .home-task span { color: var(--text-secondary); font-size: 12px; }
.home-task-budget { display: flex; flex-direction: column; gap: 7px; text-align: right; } .home-task-budget strong { font-size: 20px; font-weight: 550; white-space: nowrap; }
.home-product-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 22px; } .home-work-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 22px; }
.home-product, .home-work { min-width: 0; color: var(--text); transition: transform var(--motion-base) cubic-bezier(.22,1,.36,1); }
.home-product :deep(.asset-renderer), .home-work :deep(.asset-renderer), .home-product-placeholder { aspect-ratio: 4/3; overflow: hidden; border-radius: 16px; background: var(--blue); }
.home-product :deep(img), .home-work :deep(img), .home-product :deep(video), .home-work :deep(video) { width: 100%; height: 100%; object-fit: cover; transition: transform var(--motion-reveal) cubic-bezier(.22,1,.36,1), filter var(--motion-base) ease; }
.home-product-placeholder { display: grid; place-items: center; color: var(--text-secondary); }
.home-product-copy { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; margin-top: 16px; } .home-product h3, .home-work h3 { margin: 0; font-size: 15px; font-weight: 550; overflow-wrap: anywhere; } .home-product-copy strong { font-size: 14px; white-space: nowrap; } .home-work h3 { margin-top: 16px; }
.home-product p, .home-work p { margin: 7px 0 0; color: var(--text-secondary); font-size: 12px; overflow-wrap: anywhere; }
.home-process-wrap { padding-block: 28px; background: var(--canvas); color: #f6f8fc; } .home-process { --accent-readable: color-mix(in srgb, var(--accent) 45%, #fff); padding: 52px 40px; border-radius: 28px; background: var(--ink); } .home-process .home-kicker, .home-process p { color: #c2ccda; } .home-process .home-text-link { color: var(--accent-readable); margin-top: 32px; }
.home-section-summary { max-width: 320px; margin: 0; font-size: 14px; line-height: 1.8; }
.home-process-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 34px; padding: 0; margin: 38px 0 0; list-style: none; }
.home-process-number { display: block; padding-bottom: 16px; border-bottom: 1px solid #526078; color: var(--accent-readable); font-size: 13px; } .home-process h3 { font-size: 17px; font-weight: 550; margin: 20px 0 12px; } .home-process li p { font-size: 13px; line-height: 1.85; margin: 0; }
.home-supporting { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 24px; } .home-supporting > a { display: flex; align-items: flex-start; gap: 18px; padding: 25px; border: 1px solid var(--border); border-radius: 20px; color: var(--text); transition: background-color var(--motion-fast) ease, border-color var(--motion-fast) ease; } .home-supporting > a > div { flex: 1; min-width: 0; } .home-supporting > a > svg { flex-shrink: 0; color: var(--accent-readable); } .home-supporting > a > svg:last-child { margin-left: auto; }
.home-supporting h2 { font-size: 18px; font-weight: 550; margin: 0 0 10px; } .home-supporting p { font-size: 13px; line-height: 1.8; margin: 0; color: var(--text-secondary); }
.home-faq { display: grid; grid-template-columns: 1fr 1.7fr; gap: 64px; padding-block: 24px 64px; }
.home-faq .home-text-link { margin-top: 20px; }
.home-faq :deep(.ui-collapsible) { margin-bottom: 10px; border: 1px solid var(--border); border-radius: 14px; background: color-mix(in srgb, var(--surface) 62%, transparent); overflow: hidden; transition: border-color var(--motion-fast) ease, background-color var(--motion-fast) ease, box-shadow var(--motion-fast) ease; }
.home-faq :deep(.ui-collapsible__trigger) { padding: 18px 18px; font-size: 14px; font-weight: 550; color: var(--text); text-align: left; gap: 20px; }
.home-faq :deep(.ui-collapsible__inner) { padding: 0 16px; }
.home-faq :deep(.ui-collapsible[data-open='true']) { border-color: color-mix(in srgb, var(--accent) 42%, var(--border)); background: var(--surface); box-shadow: 0 6px 16px rgb(32 44 59 / 6%); }
.home-faq p:not(.home-kicker) { margin: 0 0 22px; color: var(--text-secondary); font-size: 14px; line-height: 1.8; }
.home-faq p a { color: var(--accent-readable); text-decoration: underline; margin-inline-start: 6px; }

.home-footer { display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 20px; border-top: 1px solid var(--border); padding-block: 28px; } .home-footer p { max-width: 340px; font-size: 11px; color: var(--text-secondary); } .home-footer nav { display: flex; flex-wrap: wrap; gap: 16px; font-size: 12px; color: var(--text-secondary); }
.home-catalog-notice { display: flex; justify-content: space-between; gap: 15px; margin-bottom: 24px; font-size: 13px; line-height: 1.7; color: var(--text-secondary); } .home-catalog-notice button { color: var(--accent-readable); text-decoration: underline; background: transparent; border: 0; cursor: pointer; }
.guest-home :is(a, button, summary):focus-visible { outline: 2px solid var(--accent-readable); outline-offset: 5px; }
@keyframes home-rise { from { transform: translateY(16px); } to { transform: translateY(0); } }
@keyframes home-line { from { transform: scaleX(0); } to { transform: scaleX(1); } }
.home-hero-copy > * { animation: home-rise var(--motion-reveal) cubic-bezier(.22,1,.36,1) both; }
.home-hero-copy > :nth-child(1) { animation-delay: 40ms; }
.home-hero-copy > :nth-child(2) { animation-delay: 100ms; }
.home-hero-copy > :nth-child(3) { animation-delay: 160ms; }
.home-hero-copy > :nth-child(4) { animation-delay: 220ms; }
.home-hero-copy > :nth-child(5) { animation-delay: 280ms; }
.home-project-board { animation: home-rise var(--motion-reveal) 120ms cubic-bezier(.22,1,.36,1) both; }
.home-entered { animation: home-rise var(--motion-reveal) ease-out both; }
.home-entered .home-pathway, .home-entered .home-task, .home-entered .home-product, .home-entered .home-work, .home-entered .home-process-grid li, .home-entered .home-supporting > a { animation: home-rise 500ms cubic-bezier(.22,1,.36,1) both; }
.home-entered .home-pathway:nth-child(2), .home-entered .home-task:nth-child(2), .home-entered .home-product:nth-child(2), .home-entered .home-work:nth-child(2), .home-entered .home-process-grid li:nth-child(2), .home-entered .home-supporting > a:nth-child(2) { animation-delay: 60ms; }
.home-entered .home-pathway:nth-child(3), .home-entered .home-task:nth-child(3), .home-entered .home-product:nth-child(3), .home-entered .home-work:nth-child(3), .home-entered .home-process-grid li:nth-child(3) { animation-delay: 120ms; }
.home-entered .home-pathway:nth-child(4), .home-entered .home-product:nth-child(4), .home-entered .home-process-grid li:nth-child(4) { animation-delay: 180ms; }
.home-button { transition: transform var(--motion-fast) cubic-bezier(.22,1,.36,1), box-shadow var(--motion-fast) ease, background-color var(--motion-fast) ease; } .home-button svg, .home-text-link svg, .home-task > svg, .home-supporting > a > svg:last-child { transition: transform var(--motion-fast) cubic-bezier(.22,1,.36,1); }
@media (hover: hover) and (pointer: fine) {
  .home-button:hover { transform: none; }
  .home-button-secondary:hover { background: var(--surface); }
  .home-supporting > a:hover, .home-faq :deep(.ui-collapsible:hover) { background: var(--surface); }
  .home-button:not(.home-button-secondary):hover { background: var(--accent-hover); }
  .home-product:hover, .home-work:hover { transform: translateY(-2px); }
  .home-product:hover :deep(img), .home-work:hover :deep(img), .home-product:hover :deep(video), .home-work:hover :deep(video) { transform: scale(1.035); filter: saturate(1.04); }
  .home-text-link:hover svg, .home-task:hover > svg, .home-supporting > a:hover > svg:last-child { transform: translate(3px, -3px); }
}
@media (hover: none) { .home-product:active, .home-work:active { transform: scale(.99); } }
@media (max-width: 1100px) { .home-nav { gap: 16px; } .home-hero { gap: 40px; } }
@media (max-width: 820px) { .home-width { width: calc(100% - 40px); } .home-nav { display: none; } .home-hero { gap: 35px; padding-block: 45px; grid-template-columns: 1fr; } .home-project-board { max-width: 600px; width: calc(100% - 12px); } .home-pathways { gap: 12px; } .home-pathway { padding: 20px; } .home-pathway h2 { font-size: 20px; } .home-product-grid, .home-process-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } .home-supporting { grid-template-columns: 1fr; } .home-faq { gap: 32px; } }
@media (max-width: 540px) { .home-width { width: calc(100% - 36px); } .home-header { min-height: 68px; gap: 8px; } .home-header-scrolled { top: 8px; width: calc(100% - 24px); min-height: 50px; padding-inline: 9px; } .home-brand { font-size: 13px; gap: 7px; } .home-brand .brand-mark { width: 27px; height: 27px; } .home-actions { gap: 6px; } .home-action-tools { padding-right: 5px; } .home-action-tool { --ui-size: 29px; } .home-login { padding-inline: 10px; font-size: 12px; } .home-hero { padding-block: 32px 42px; gap: 32px; } h1 { font-size: 38px; } .home-hero-summary { font-size: 14px; margin-block: 20px 24px; } .home-button { font-size: 13px; padding-inline: 16px; } .home-board-body { padding: 22px; } .home-board-title h2 { text-wrap: balance; font-size: 20px; } .home-board-bottom { padding: 18px 22px; } .home-board-flow { gap: 4px; } .home-board-flow li { font-size: 11px; } .home-pathways { grid-template-columns: 1fr; padding-bottom: 36px; gap: 12px; } .home-pathway { padding: 24px; } .home-pathway-top { margin-bottom: 18px; } .home-section { padding-block: 32px; } .home-section-heading { align-items: flex-start; flex-direction: column; gap: 16px; } .home-task { grid-template-columns: 1fr auto; gap: 14px; } .home-task > svg { display: none; } .home-task h3 { font-size: 16px; } .home-task-budget strong { font-size: 16px; } .home-task p { overflow-wrap: anywhere; } .home-product-grid, .home-work-grid { grid-template-columns: 1fr; gap: 28px; } .home-process { padding-block: 36px; } .home-process-grid { gap: 25px; } .home-process h3 { font-size: 15px; } .home-faq { grid-template-columns: 1fr; gap: 16px; padding-bottom: 36px; } .home-supporting > a { padding: 20px; gap: 12px; } .home-footer { align-items: flex-start; } .home-footer p { width: 100%; margin: 0; } }
@media (max-width: 540px) { .home-process { padding-inline: 24px; border-radius: 20px; } }
@media (prefers-reduced-motion: reduce) { .guest-home :deep(*), .guest-home :deep(*::before), .guest-home :deep(*::after) { animation: none !important; transition: none !important; } }
</style>
