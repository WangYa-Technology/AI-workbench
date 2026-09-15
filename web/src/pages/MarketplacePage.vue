<script setup lang="ts">
import {
  ArrowLeft, BadgeCheck, Check, ChevronRight, CircleDollarSign,
  Filter, Grid2X2, List, Layers3, LoaderCircle, LogIn, PackageCheck, RefreshCw, Search, ShieldCheck, ShoppingBag,
  UserPlus, Users, X, Info,
} from 'lucide-vue-next'
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api, messageFrom, type Product } from '../api/client'
import { formatCurrency } from '../lib/format'
import { contentListReturn } from '../lib/contentPresentation'
import { openCheckoutWindow } from '../lib/checkout'
import { useSessionStore } from '../stores/session'
import CategoryBrowser from '../components/domain/CategoryBrowser.vue'
import AssetMedia from '../components/domain/AssetMedia.vue'
import type { TaskType } from '../api/client'
import PageHero from '../components/ui/PageHero.vue'
import UiButton from '../components/ui/UiButton.vue'
import UiCheckbox from '../components/ui/UiCheckbox.vue'
import UiIconButton from '../components/ui/UiIconButton.vue'
import UiInput from '../components/ui/UiInput.vue'
import UiSelect from '../components/ui/UiSelect.vue'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const products = ref<Product[]>([])
const categoryCounts = ref<Record<string, number>>()
const detail = ref<Product | null>(null)
const loading = ref(true)
const purchasing = ref(false)
const error = ref('')
const accepted = ref(false)
const paymentEnabled = ref(false)
const paymentLiveMode = ref(false)
const search = ref(String(route.query.q || ''))
const sort = ref(String(route.query.sort || 'newest'))
const layoutMode = ref<'list' | 'grid'>('list')
const hasFilters = computed(() => Boolean(search.value.trim() || category.value || sort.value !== 'newest'))
async function clearFilters() {
  search.value = ''; category.value = ''; sort.value = 'newest'
  await applyFilters()
}

const productID = computed(() => String(route.params.id || ''))
const isDetail = computed(() => Boolean(productID.value))
const types = ref<TaskType[]>([])
const categoryFromRoute = () => String(route.query.category || (route.query.type ? `market_${route.query.type}` : ''))
const category = ref(categoryFromRoute())
const categoryName = (code?: string) => { const item = types.value.find(i => i.code === code); return item ? (locale.value.startsWith('zh') ? item.nameZh : item.nameEn) : code || '' }
async function selectCategory(value: string) { category.value = value; await applyFilters() }
const marketStats = computed(() => ({
  products: products.value.length,
  creators: new Set(products.value.map(item => item.seller.handle)).size,
  types: new Set(products.value.map(item => item.productType)).size,
}))
const marketplaceHeroStats = computed(() => [
  { value: marketStats.value.products, label: t('content.loadedProducts'), icon: ShoppingBag, tone: 'blue' as const },
  { value: marketStats.value.creators, label: t('content.loadedCreators'), icon: Users, tone: 'violet' as const },
  { value: marketStats.value.types, label: t('content.loadedTypes'), icon: Layers3, tone: 'green' as const },
])

function money(cents: number, currency = 'USD') {
  return formatCurrency(cents, currency, locale.value)
}

let loadVersion = 0
async function load() {
  const version = ++loadVersion
  const id = productID.value
  const filters = { q: search.value, category: category.value, sort: sort.value }
  loading.value = true
  error.value = ''
  try {
    const [, runtime, directory] = await Promise.all([session.ensure(), api.meta(), api.listTaskTypes('marketplace')])
    if (version !== loadVersion) return
    types.value = directory.items
    paymentEnabled.value = runtime.paymentProvider.enabled
    paymentLiveMode.value = runtime.paymentProvider.liveMode
    if (id) {
      const result = await api.getProduct(id)
      if (version !== loadVersion) return
      detail.value = result
    } else {
      const response = await api.listProducts(filters)
      if (version !== loadVersion) return
      products.value = response.items
      categoryCounts.value = response.categoryCounts
      detail.value = null
    }
  } catch (reason) {
    if (version === loadVersion) error.value = messageFrom(reason)
  } finally {
    if (version === loadVersion) loading.value = false
  }
}

async function applyFilters() {
  await router.push({ path: '/market', query: {
    ...(search.value.trim() ? { q: search.value.trim() } : {}),
    ...(category.value ? { category: category.value } : {}),
    ...(sort.value !== 'newest' ? { sort: sort.value } : {}),
  } })
}

async function buy() {
  if (!session.user || !detail.value || !accepted.value || !paymentEnabled.value) return
  purchasing.value = true
  error.value = ''
  let checkoutWindow = null as ReturnType<typeof globalThis.open>
  try {
    checkoutWindow = openCheckoutWindow()
    if (!checkoutWindow) {
      error.value = t('errors.codes.checkout_popup_blocked')
      return
    }
    const checkout = await api.checkoutProduct(detail.value.id, accepted.value)
    checkoutWindow.location.href = checkout.checkoutUrl
  } catch (reason) {
    checkoutWindow?.close()
    error.value = messageFrom(reason)
  } finally {
    purchasing.value = false
  }
}

watch(() => route.fullPath, () => {
  category.value = categoryFromRoute()
  search.value = String(route.query.q || '')
  sort.value = String(route.query.sort || 'newest')
  accepted.value = false
  void load()
})

onMounted(() => void load())
</script>

<template>
  <section class="market-page content-width" :class="{ 'is-detail': isDetail, 'has-category-sidebar': types.length > 0 }">
    <template v-if="!isDetail">
      <PageHero
        :eyebrow="paymentEnabled ? t(paymentLiveMode ? 'marketplace.providerLiveShort' : 'marketplace.providerTestShort') : t('marketplace.paymentUnavailable')"
        :eyebrow-icon="ShieldCheck"
        :title="t('marketplace.title')"
        :summary="t('marketplace.summary')"
        :stats="marketplaceHeroStats"
        :stats-label="t('marketplace.statsLabel')"
        artwork-src="/tasks/task-hero-transparent.webp"
      >
        <template #actions>
          <UiButton v-if="session.user" as="RouterLink" class="command-button secondary" variant="secondary" to="/workspace/orders">
            <template #start>
              <ShoppingBag :size="17" />
            </template>{{ t('marketplace.myOrders') }}
          </UiButton>
          <UiButton v-if="!session.user" as="RouterLink" class="command-button secondary" variant="secondary" :to="{ path: '/auth', query: { auth: 'login', returnTo: route.fullPath } }">
            <template #start>
              <LogIn :size="17" />
            </template>{{ t('account.signIn') }}
          </UiButton>
          <UiButton v-if="!session.user" as="RouterLink" class="command-button primary" variant="primary" :to="{ path: '/auth', query: { auth: 'register', returnTo: route.fullPath } }">
            <template #start>
              <UserPlus :size="17" />
            </template>{{ t('account.createAccount') }}
          </UiButton>
        </template>
      </PageHero>

      <form class="market-filters" role="search" @submit.prevent="applyFilters">
        <label class="market-search"><span class="sr-only">{{ t('actions.search') }}</span><Search :size="17" /><UiInput v-model="search" type="search" :placeholder="t('marketplace.searchPlaceholder')" /></label>
        <UiSelect v-model="category" class="market-filter-control category-filter" :aria-label="t('marketplace.allTypes')" :align-item-with-trigger="false" @change="applyFilters">
          <template #start>
            <Filter :size="16" aria-hidden="true" />
          </template>
          <option value="">
            {{ t('marketplace.allTypes') }}
          </option>
          <option v-for="item in types" :key="item.code" :value="item.code">
            {{ categoryName(item.code) }}
          </option>
        </UiSelect>
        <UiSelect v-model="sort" class="market-filter-control" :aria-label="t('marketplace.sortNewest')" @change="applyFilters">
          <option value="newest">
            {{ t('marketplace.sortNewest') }}
          </option>
          <option value="price_asc">
            {{ t('marketplace.sortLow') }}
          </option>
          <option value="price_desc">
            {{ t('marketplace.sortHigh') }}
          </option>
        </UiSelect>
        <UiButton class="market-search-submit" variant="primary" type="submit">
          <template #start>
            <Search :size="17" />
          </template>{{ t('actions.search') }}
        </UiButton>
      </form>

      <CategoryBrowser :items="types" :model-value="category" :counts="categoryCounts" @update:model-value="selectCategory">
        <div v-if="loading" class="page-state" aria-live="polite">
          <LoaderCircle class="spin" :size="20" />{{ t('marketplace.loading') }}
        </div>
        <div v-else-if="error" class="page-state" role="alert">
          <p>{{ error }}</p><UiButton class="command-button secondary" variant="secondary" @click="load">
            <template #start>
              <RefreshCw :size="17" />
            </template>{{ t('actions.retry') }}
          </UiButton>
        </div>
        <div v-else-if="!products.length" class="page-state">
          <p>{{ t('marketplace.noResults') }}</p>
          <UiButton v-if="hasFilters" variant="secondary" @click="clearFilters">
            {{ t('marketplace.clearFilters') }}
          </UiButton>
        </div>
        <template v-else>
          <div class="task-results-meta market-results-summary">
            <div><strong>{{ products.length }} {{ t('marketplace.results') }}</strong><span>{{ t('marketplace.browseSummary') }}</span></div>
            <div class="task-results-actions">
              <UiButton v-if="hasFilters" class="text-link" variant="ghost" size="sm" @click="clearFilters">
                {{ t('marketplace.clearFilters') }}
              </UiButton>
              <div class="task-layout-switcher" :aria-label="t('marketplace.layout')">
                <UiIconButton size="sm" class="icon-button" variant="ghost" :class="{ active: layoutMode === 'list' }" :aria-pressed="layoutMode === 'list'" :label="t('marketplace.listView')" @click="layoutMode = 'list'">
                  <List :size="17" />
                </UiIconButton>
                <UiIconButton size="sm" class="icon-button" variant="ghost" :class="{ active: layoutMode === 'grid' }" :aria-pressed="layoutMode === 'grid'" :label="t('marketplace.gridView')" @click="layoutMode = 'grid'">
                  <Grid2X2 :size="16" />
                </UiIconButton>
              </div>
            </div>
          </div>
          <div class="market-catalog" :class="{ 'is-grid': layoutMode === 'grid' }">
            <RouterLink v-for="item in products" :key="item.id" class="product-card" :to="`/market/assets/${item.id}`">
              <div class="product-media">
                <img :src="item.mediaUrl" :alt="item.title" :width="item.width || 1200" :height="item.height || 900" loading="lazy" />
              </div>
              <div class="product-card-copy">
                <div class="market-product-tags">
                  <span>{{ categoryName(item.category) }}</span><span v-if="item.ownedAssetId" class="market-owned"><BadgeCheck :size="13" />{{ t('marketplace.owned') }}</span>
                </div>
                <h2>{{ item.title }}</h2>
                <p>{{ item.description }}</p>
                <div class="market-product-meta">
                  <span>@{{ item.seller.handle }}</span><span>{{ item.license.name }}</span>
                </div>
              </div>
              <div class="market-product-offer">
                <span>{{ t('marketplace.productPrice') }}</span><strong>{{ money(item.priceCents, item.currency) }}</strong><small><ShieldCheck :size="14" />{{ t(item.license.allowsCommercial ? 'marketplace.commercialYes' : 'marketplace.commercialNo') }}</small><span class="market-product-action">{{ t('marketplace.viewProduct') }}<ChevronRight :size="16" /></span>
              </div>
            </RouterLink>
          </div>
        </template>
      </CategoryBrowser>
    </template>

    <template v-else>
      <RouterLink class="text-link market-back" :to="contentListReturn('/market')">
        <ArrowLeft :size="17" />{{ t('marketplace.back') }}
      </RouterLink>
      <div v-if="loading" class="page-state" aria-live="polite">
        <LoaderCircle class="spin" :size="20" />{{ t('marketplace.loadingProduct') }}
      </div>
      <div v-else-if="error && !detail" class="page-state" role="alert">
        <p>{{ error }}</p><UiButton class="command-button secondary" variant="secondary" @click="load">
          <template #start>
            <RefreshCw :size="17" />
          </template>{{ t('actions.retry') }}
        </UiButton>
      </div>
      <div v-else-if="detail" class="product-detail-layout">
        <header class="product-detail-heading">
          <span>{{ categoryName(detail.category) }}</span><h1>{{ detail.title }}</h1><p>{{ detail.description }}</p>
        </header>
        <div class="product-detail-main">
          <div class="product-detail-media">
            <AssetMedia :src="detail.mediaUrl" :kind="detail.mediaKind" :alt="detail.title" :width="detail.width || 1600" :height="detail.height || 1200" />
            <a v-if="detail.mediaUrl" :href="detail.mediaUrl" target="_blank" rel="noopener noreferrer" class="product-full-preview">{{ t('content.preview') }}</a>
          </div>
          <section class="product-description">
            <div class="product-seller">
              <BadgeCheck :size="18" /><span>{{ t('marketplace.soldBy') }}</span><RouterLink :to="`/creators/${detail.seller.handle}`">
                <strong>{{ detail.seller.displayName }}</strong><small>@{{ detail.seller.handle }}</small>
              </RouterLink>
            </div>
          </section>
          <section class="product-evidence">
            <div>
              <h2>{{ t('marketplace.included') }}</h2><ul>
                <li v-for="item in detail.includedFiles" :key="item">
                  <PackageCheck :size="17" />{{ item }}
                </li>
              </ul><p><strong>{{ t('marketplace.compatibility') }}</strong>{{ detail.compatibility }}</p>
            </div>
            <div><h2>{{ t('marketplace.aiDisclosure') }}</h2><p>{{ detail.aiDisclosure }}</p></div>
          </section>
          <section class="license-panel">
            <header><div><span>{{ t('marketplace.licenseVersion', { version: detail.license.version }) }}</span><h2>{{ detail.license.name }}</h2><p>{{ detail.license.summary }}</p></div><ShieldCheck :size="26" /></header>
            <div class="license-rights">
              <span><component :is="detail.license.allowsCommercial ? Check : X" :size="16" />{{ detail.license.allowsCommercial ? t('marketplace.commercialYes') : t('marketplace.commercialNo') }}</span><span><component :is="detail.license.allowsDerivatives ? Check : X" :size="16" />{{ detail.license.allowsDerivatives ? t('marketplace.derivativesYes') : t('marketplace.derivativesNo') }}</span><span><Info :size="16" />{{ detail.license.attributionRequired ? t('marketplace.attributionYes') : t('marketplace.attributionNo') }}</span><span><component :is="detail.license.allowsRedistribution ? Check : X" :size="16" />{{ detail.license.allowsRedistribution ? t('marketplace.redistributionYes') : t('marketplace.redistributionNo') }}</span>
            </div>
            <p class="license-terms">
              {{ detail.license.terms }}
            </p>
          </section>
        </div>

        <aside class="product-purchase-rail">
          <span>{{ t('marketplace.providerPrice') }}</span><strong>{{ money(detail.priceCents, detail.currency) }}</strong><p>{{ paymentEnabled ? t(paymentLiveMode ? 'marketplace.liveCharge' : 'marketplace.testCharge') : t('marketplace.paymentUnavailable') }}</p>
          <dl><div><dt>{{ t('marketplace.license') }}</dt><dd>{{ detail.license.name }}</dd></div><div><dt>{{ t('marketplace.refundWindow') }}</dt><dd>{{ t('marketplace.refundDays', { count: detail.license.refundWindowDays }) }}</dd></div></dl>
          <div v-if="!session.user" class="market-auth-prompt">
            <div><h2>{{ t('marketplace.guestTitle') }}</h2><p>{{ t('marketplace.guestSummary') }}</p></div>
            <UiButton as="RouterLink" class="command-button primary wide" variant="primary" :to="{ path: '/auth', query: { auth: 'login', returnTo: route.fullPath } }">
              <template #start>
                <LogIn :size="17" />
              </template>{{ t('account.signIn') }}
            </UiButton>
            <UiButton as="RouterLink" class="text-link" variant="ghost" size="sm" :to="{ path: '/auth', query: { auth: 'register', returnTo: route.fullPath } }">
              {{ t('account.createAccount') }}
            </UiButton>
          </div>
          <template v-else-if="detail.ownedAssetId">
            <UiButton as="RouterLink" class="command-button primary wide" variant="primary" :to="`/workspace/assets/${detail.ownedAssetId}`">
              <template #start>
                <PackageCheck :size="17" />
              </template>{{ t('marketplace.openAsset') }}
            </UiButton>
            <UiButton as="RouterLink" class="command-button secondary wide" variant="secondary" to="/workspace/orders">
              {{ t('marketplace.viewOrder') }}
            </UiButton>
          </template>
          <form v-else @submit.prevent="buy">
            <label class="license-accept"><UiCheckbox v-model="accepted" /><span>{{ t('marketplace.acceptLicense', { name: detail.license.name, version: detail.license.version }) }}</span></label>
            <p v-if="error" class="form-error" role="alert">
              {{ error }}
            </p>
            <UiButton class="command-button primary wide" variant="primary" type="submit" :loading="purchasing" :disabled="!paymentEnabled || !accepted">
              <template #start>
                <CircleDollarSign v-if="!purchasing" :size="17" />
              </template>{{ purchasing ? t('marketplace.openingCheckout') : t('marketplace.openCheckout') }}
            </UiButton>
          </form>
          <small v-if="paymentEnabled">{{ t('marketplace.providerCheckoutEvidence') }}</small>
        </aside>
      </div>
    </template>
  </section>
</template>

<style scoped>
.market-page .market-filters { --control-height-toolbar: 40px; min-height: 54px; padding: 6px 8px; grid-template-columns: minmax(160px, 1fr) 150px 150px 84px; }
.market-page.has-category-sidebar .market-filters { grid-template-columns: minmax(160px, 1fr) 150px 150px 84px; }
.market-search-submit { height: 40px; padding-inline: 12px; }
.market-catalog { display: grid; gap: 12px; }
.market-catalog .product-card { display: grid; grid-template-columns: 160px minmax(0, 1fr) 180px; grid-template-rows: auto; align-items: stretch; gap: 16px; padding: 12px; border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface); transition: border-color var(--duration-quick) var(--ease-smooth-out); }
.market-catalog .product-card:hover { border-color: var(--border-strong); }
.market-catalog .product-media { aspect-ratio: auto; min-height: 138px; }
.market-catalog .product-media img { position: absolute; inset: 0; transform: none; }
.market-catalog .product-card-copy { display: flex; flex-direction: column; justify-content: center; align-items: stretch; gap: 8px; padding: 4px 0; }
.market-catalog .product-card-copy h2 { min-height: 0; margin: 0; font-size: 15px; line-height: 1.45; overflow-wrap: anywhere; }
.market-catalog .product-card-copy p { min-height: 0; margin: 0; font-size: 12px; line-height: 1.6; }
.market-catalog .market-product-tags { justify-content: flex-start; align-items: center; gap: 6px; }
.market-product-tags > span { display: inline-flex; align-items: center; gap: 4px; padding: 4px 7px; border-radius: 6px; background: var(--accent-soft); color: var(--accent-readable); font-size: 10px; font-weight: 600; }
.market-catalog .market-product-meta { justify-content: flex-start; flex-wrap: wrap; gap: 6px 12px; font-size: 11px; line-height: 1.5; }
.market-product-offer { min-width: 0; display: flex; flex-direction: column; gap: 6px; padding: 4px 0 4px 16px; border-left: 1px solid var(--border); }
.market-product-offer > span:first-child { color: var(--text-secondary); font-size: 11px; }
.market-product-offer > strong { font-family: var(--font-mono); font-size: 16px; color: var(--text); }
.market-product-offer small { display: flex; align-items: center; gap: 5px; color: var(--text-secondary); font-size: 11px; }
.market-product-action { display: flex; justify-content: center; align-items: center; gap: 8px; min-height: 34px; margin-top: auto; border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--surface-muted); color: var(--text); font-size: 12px; font-weight: 600; }
.market-catalog.is-grid { grid-template-columns: repeat(auto-fill, minmax(min(100%, 260px), 1fr)); align-items: stretch; }
.market-catalog.is-grid .product-card { grid-template-columns: minmax(0, 1fr); grid-template-rows: auto 1fr auto; gap: 12px; }
.market-catalog.is-grid .product-media { aspect-ratio: 16 / 10; min-height: 0; }
.market-catalog.is-grid .product-card-copy { justify-content: flex-start; }
.market-catalog.is-grid .market-product-meta { margin-top: auto; }
.market-catalog.is-grid .market-product-offer { padding: 12px 0 0; border-left: 0; border-top: 1px solid var(--border); }
.market-catalog.is-grid .market-product-action { margin-top: 6px; }
@media (min-width: 1321px) { .market-page.has-category-sidebar .market-filters { grid-template-columns: minmax(0, 1fr) 150px 84px; } }
@media (max-width: 760px) {
  .market-page .market-filters, .market-page.has-category-sidebar .market-filters { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) 84px; }
  .market-filters .market-search { grid-column: 1 / -1; }
  .market-catalog:not(.is-grid) .product-card { grid-template-columns: 100px minmax(0, 1fr); gap: 12px; }
  .market-catalog:not(.is-grid) .market-product-offer { grid-column: 1 / -1; display: grid; grid-template-columns: minmax(0, 1fr) auto; border-left: 0; border-top: 1px solid var(--border); padding: 10px 0 0; }
  .market-catalog:not(.is-grid) .market-product-offer > span:first-child { display: none; }
  .market-catalog:not(.is-grid) .market-product-offer small { grid-column: 1; }
  .market-catalog:not(.is-grid) .market-product-action { grid-column: 2; grid-row: 1 / 3; align-self: center; padding-inline: 12px; margin: 0; }
  .market-results-summary { flex-wrap: wrap; }
}
</style>

<style scoped>
.product-detail-layout { grid-template-columns: minmax(0, 1fr) 320px; gap: 24px; }
.product-detail-heading { grid-column: 1 / -1; }
.product-detail-heading h1 { margin: 8px 0; font-size: clamp(24px, 2.5vw, 32px); line-height: 1.3; overflow-wrap: anywhere; }
.product-detail-heading > span { color: var(--accent-readable); font-size: 12px; }
.product-detail-heading > p { margin: 0; max-width: 76ch; color: var(--text-secondary); font-size: 14px; line-height: 1.7; }
.product-detail-media { height: min(55dvh, 520px); aspect-ratio: auto; }
.product-detail-media :deep(.asset-renderer) { width: 100%; height: 100%; }
.product-detail-media :deep(img), .product-detail-media :deep(video) { width: 100%; height: 100%; object-fit: contain; }
.product-full-preview { position: absolute; bottom: 12px; right: 12px; padding: 10px; border-radius: var(--radius-control); color: white; background: rgb(0 0 0 / 72%); font-size: 13px; }
.product-purchase-rail { margin-inline: 0; min-width: 0; padding: 18px; border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface); }
.product-seller a { display: inline-flex; flex-wrap: wrap; gap: 6px; }
@media(max-width: 1000px) { .product-detail-layout { grid-template-columns: minmax(0, 1fr); } .product-purchase-rail { grid-row: 2; position: static; } }
</style>
