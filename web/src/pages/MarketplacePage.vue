<script setup lang="ts">
import UiEmptyState from '../components/ui/UiEmptyState.vue'
import UiActionBanner from '../components/ui/UiActionBanner.vue'
import UiCardContent from '../components/ui/UiCardContent.vue'
import UiCardActions from '../components/ui/UiCardActions.vue'
import UiCardTag from '../components/ui/UiCardTag.vue'
import UiFilterSearch from '../components/ui/UiFilterSearch.vue'
import UiFilterBar from '../components/ui/UiFilterBar.vue'
import DetailToolbar from '../components/ui/DetailToolbar.vue'
import UiLayoutSwitcher from '../components/ui/UiLayoutSwitcher.vue'
import UiCatalog from '../components/ui/UiCatalog.vue'
import UiContentCard from '../components/ui/UiContentCard.vue'
import {
  ArrowLeft, BadgeCheck, Check, CircleDollarSign,
  Filter, LoaderCircle, LogIn, PackageCheck, RefreshCw, Search, ShieldCheck, ShoppingBag,
  UserPlus, X, Info, Layers, WandSparkles,
} from 'lucide-vue-next'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api, APIError, messageFrom, type Asset, type Product } from '../api/client'
import { formatCurrency } from '../lib/format'
import { contentListReturn } from '../lib/contentPresentation'
import { openCheckoutWindow } from '../lib/checkout'
import { useSessionStore } from '../stores/session'
import CategoryBrowser from '../components/domain/CategoryBrowser.vue'
import AssetMedia from '../components/domain/AssetMedia.vue'
import { validProductReturnID } from '../lib/productPayment'
import type { TaskType } from '../api/client'
import PageHeader from '../components/ui/PageHeader.vue'
import UiButton from '../components/ui/UiButton.vue'
import UiCheckbox from '../components/ui/UiCheckbox.vue'
import UiInput from '../components/ui/UiInput.vue'
import UiForm from '../components/ui/UiForm.vue'
import UiSelect from '../components/ui/UiSelect.vue'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const products = ref<Product[]>([])
const total = ref(0)
const nextCursor = ref<string>()
const loadingMore = ref(false)
const moreError = ref('')
const categoryCounts = ref<Record<string, number>>()
const detail = ref<Product | null>(null)
const previewEditing = ref(false)
const previewAssets = ref<Asset[]>([])
const previewCursor = ref<string>()
const previewSelection = ref('')
const previewLoading = ref(false)
const previewSaving = ref(false)
const previewError = ref('')
const loading = ref(true)
const purchasing = ref(false)
const error = ref('')
const checkoutNeedsAttention = ref(false)
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
const catalogFilters = () => ({ q: String(route.query.q || ''), category: categoryFromRoute(), sort: String(route.query.sort || 'newest') })
const categoryName = (code?: string) => { const item = types.value.find(i => i.code === code); return item ? (locale.value.startsWith('zh') ? item.nameZh : item.nameEn) : code || '' }
async function selectCategory(value: string) { category.value = value; await applyFilters() }

function money(cents: number, currency = 'USD') {
  return formatCurrency(cents, currency, locale.value)
}

let loadVersion = 0
let previewVersion = 0
function closePreview() {
  previewVersion++
  previewEditing.value = false
  previewAssets.value = []
  previewCursor.value = undefined
  previewSelection.value = ''
  previewLoading.value = false
  previewSaving.value = false
  previewError.value = ''
}
async function load() {
  const version = ++loadVersion
  if (route.query.payment === 'cancelled' && typeof route.query.orderId === 'string' && typeof route.query.paymentId === 'string'
    && validProductReturnID(route.query.orderId) && validProductReturnID(route.query.paymentId)) {
    await router.replace({ path: '/workspace/orders', query: { payment: 'cancelled', orderId: route.query.orderId, paymentId: route.query.paymentId } })
    return
  }
  const id = productID.value
  const filters = catalogFilters()
  detail.value = null
  closePreview()
  accepted.value = false
  purchasing.value = false
  paymentEnabled.value = false
  paymentLiveMode.value = false
  products.value = []
  total.value = 0
  nextCursor.value = undefined
  categoryCounts.value = undefined
  loadingMore.value = false
  moreError.value = ''
  loading.value = true
  error.value = ''
  checkoutNeedsAttention.value = false
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
      total.value = response.total
      nextCursor.value = response.nextCursor
      categoryCounts.value = response.categoryCounts
      detail.value = null
    }
  } catch (reason) {
    if (version === loadVersion) error.value = messageFrom(reason)
  } finally {
    if (version === loadVersion) loading.value = false
  }
}

async function loadMore() {
  if (loading.value || loadingMore.value || isDetail.value || !nextCursor.value) return
  const version = loadVersion
  const cursor = nextCursor.value
  loadingMore.value = true
  moreError.value = ''
  try {
    const response = await api.listProducts({ ...catalogFilters(), cursor })
    if (version !== loadVersion) return
    const seen = new Set(products.value.map(item => item.id))
    products.value.push(...response.items.filter(item => !seen.has(item.id)))
    total.value = response.total
    categoryCounts.value = response.categoryCounts
    nextCursor.value = response.nextCursor
  } catch (reason) {
    if (version === loadVersion) moreError.value = messageFrom(reason)
  } finally {
    if (version === loadVersion) loadingMore.value = false
  }
}

async function applyFilters() {
  await router.push({ path: '/market', query: {
    ...(search.value.trim() ? { q: search.value.trim() } : {}),
    ...(category.value ? { category: category.value } : {}),
    ...(sort.value !== 'newest' ? { sort: sort.value } : {}),
  } })
}

async function editPreview() {
  if (loading.value || !detail.value || detail.value.listingManaged || detail.value.seller.id !== session.user?.id) return
  closePreview()
  previewSelection.value = detail.value.previewAssetId || ''
  previewEditing.value = true
  await loadPreviewAssets()
}

async function loadPreviewAssets() {
  if (previewLoading.value || !previewEditing.value || !detail.value || detail.value.listingManaged || detail.value.seller.id !== session.user?.id) return
  const version = loadVersion
  const editor = previewVersion
  previewLoading.value = true
  previewError.value = ''
  try {
    const response = await api.listAssets({ purpose: 'product_preview', limit: 50, cursor: previewCursor.value })
    if (version !== loadVersion || editor !== previewVersion) return
    const seen = new Set(previewAssets.value.map(item => item.id))
    previewAssets.value.push(...response.items.filter(item => !seen.has(item.id)))
    previewCursor.value = response.nextCursor
  } catch (reason) {
    if (version === loadVersion && editor === previewVersion) previewError.value = messageFrom(reason)
  } finally {
    if (version === loadVersion && editor === previewVersion) previewLoading.value = false
  }
}

async function reloadOfferWithError(reason: unknown, id: string) {
  const refresh = load()
  const version = loadVersion
  await refresh
  // Even revisiting the same product is a new confirmation context.
  if (version === loadVersion && detail.value?.id === id) error.value = messageFrom(reason)
}

async function savePreview() {
  if (previewSaving.value || previewLoading.value || !previewEditing.value || !detail.value || detail.value.listingManaged || detail.value.seller.id !== session.user?.id) return
  const version = loadVersion
  const editor = previewVersion
  const id = detail.value.id
  previewSaving.value = true
  previewError.value = ''
  try {
    await api.setProductPreview(detail.value.id, previewSelection.value || null, detail.value.offerVersion)
    if (version !== loadVersion || editor !== previewVersion) return
    await load()
  } catch (reason) {
    if (version !== loadVersion || editor !== previewVersion) return
    if (reason instanceof APIError && reason.code === 'product_offer_changed') {
      await reloadOfferWithError(reason, id)
    } else previewError.value = messageFrom(reason)
  } finally {
    if (version === loadVersion && editor === previewVersion) previewSaving.value = false
  }
}

async function buy() {
  if (purchasing.value || loading.value || !session.user || !detail.value || detail.value.id !== productID.value || !accepted.value || !paymentEnabled.value) return
  const offerID = detail.value.id
  const requestVersion = loadVersion
  purchasing.value = true
  error.value = ''
  checkoutNeedsAttention.value = false
  let checkoutWindow = null as ReturnType<typeof globalThis.open>
  try {
    checkoutWindow = openCheckoutWindow()
    if (!checkoutWindow) {
      error.value = t('errors.codes.checkout_popup_blocked')
      return
    }
    const checkout = await api.checkoutProduct(detail.value.id, accepted.value, detail.value.offerVersion)
    if (requestVersion !== loadVersion || productID.value !== offerID) { checkoutWindow.close(); return }
    checkoutWindow.location.href = checkout.checkoutUrl
  } catch (reason) {
    checkoutWindow?.close()
    if (requestVersion !== loadVersion || productID.value !== offerID) return
    if (reason instanceof APIError && ['product_offer_changed', 'payment_checkout_closed', 'payment_checkout_expired'].includes(reason.code)) {
      await reloadOfferWithError(reason, offerID)
    } else {
      error.value = messageFrom(reason)
      checkoutNeedsAttention.value = reason instanceof APIError && ['payment_checkout_preparation_failed', 'payment_reconciliation_required'].includes(reason.code)
    }
  } finally {
    if (requestVersion === loadVersion) purchasing.value = false
  }
}

watch([() => route.fullPath, () => session.user?.id], () => {
  category.value = categoryFromRoute()
  search.value = String(route.query.q || '')
  sort.value = String(route.query.sort || 'newest')
  accepted.value = false
  void load()
})

onMounted(() => void load())
onBeforeUnmount(() => { loadVersion++ })
</script>

<template>
  <section class="market-page content-width" :class="{ 'is-detail': isDetail, 'has-category-sidebar': types.length > 0 }">
    <template v-if="!isDetail">
      <PageHeader
        :title="t('marketplace.title')"
        :summary="t('marketplace.summary')"
        artwork-src="/illustrations/headers/tasks.webp"
      >
        <template #actions>
          <UiButton v-if="session.user" as="RouterLink" variant="secondary" to="/workspace/products">
            {{ t('sellerProducts.title') }}
          </UiButton>
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
      </PageHeader>

      <UiFilterBar class="ui-filter-bar market-filters" role="search" @submit.prevent="applyFilters">
        <UiFilterSearch class="market-search" :label="t('actions.search')">
          <UiInput v-model="search" type="search" :placeholder="t('marketplace.searchPlaceholder')" />
        </UiFilterSearch>
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
      </UiFilterBar>

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
        <UiEmptyState v-else-if="!products.length" :title="t(hasFilters ? 'marketplace.noResults' : 'marketplace.emptyTitle')" :message="t(hasFilters ? 'marketplace.emptyFilteredSummary' : 'marketplace.emptySummary')">
          <template #icon>
            <component :is="hasFilters ? Search : ShoppingBag" :size="24" :stroke-width="1.75" />
          </template>
          <template v-if="hasFilters" #actions>
            <UiButton variant="secondary" @click="clearFilters">
              {{ t('marketplace.clearFilters') }}
            </UiButton>
          </template>
        </UiEmptyState>
        <template v-else>
          <div class="task-results-meta market-results-summary">
            <div><strong>{{ total }} {{ t('marketplace.results') }}</strong><span>{{ t('marketplace.browseSummary') }}</span></div>
            <div class="task-results-actions">
              <UiButton v-if="hasFilters" class="text-link" variant="ghost" size="sm" @click="clearFilters">
                {{ t('marketplace.clearFilters') }}
              </UiButton>
              <UiLayoutSwitcher v-model="layoutMode" :label="t('marketplace.layout')" :list-label="t('marketplace.listView')" :grid-label="t('marketplace.gridView')" />
            </div>
          </div>
          <UiCatalog class="ui-catalog market-catalog" :class="{ 'is-grid': layoutMode === 'grid' }">
            <UiContentCard v-for="item in products" :key="item.id" class="ui-content-card product-card" :to="`/market/assets/${item.id}`">
              <div class="ui-content-card__media product-media">
                <AssetMedia :src="item.mediaUrl" :kind="item.mediaKind" :alt="item.title" :width="item.width || 1200" :height="item.height || 900" :controls="false" />
              </div>
              <UiCardContent :title="item.title" :summary="item.description">
                <template #tags>
                  <UiCardTag>{{ categoryName(item.category) }}</UiCardTag><UiCardTag v-if="item.ownedAssetId" variant="success">
                    <BadgeCheck :size="13" aria-hidden="true" />{{ t('marketplace.owned') }}
                  </UiCardTag>
                </template>
                <template #meta>
                  <span>@{{ item.seller.handle }}</span><span>{{ item.license.name }}</span>
                </template>
              </UiCardContent>
              <UiCardActions class="market-product-offer" :label="t('marketplace.productPrice')" :value="money(item.priceCents, item.currency)" numeric :action-label="t('marketplace.viewProduct')">
                <template #description>
                  <ShieldCheck :size="14" :stroke-width="1.75" aria-hidden="true" />{{ t(item.license.allowsCommercial ? 'marketplace.commercialYes' : 'marketplace.commercialNo') }}
                </template>
              </UiCardActions>
            </UiContentCard>
          </UiCatalog>
          <div v-if="nextCursor" class="catalog-pagination">
            <p v-if="moreError" class="form-error" role="alert">
              {{ moreError }}
            </p>
            <UiButton variant="secondary" :loading="loadingMore" @click="loadMore">
              {{ t(moreError ? 'actions.retry' : 'actions.loadMore') }}
            </UiButton>
          </div>
        </template>
        <UiActionBanner :title="t('marketplace.reuseTitle')" :summary="t('marketplace.reuseSummary')">
          <template #icon>
            <Layers :size="24" :stroke-width="1.75" />
          </template>
          <template #actions>
            <UiButton as="RouterLink" variant="primary" to="/create/image">
              <template #start>
                <WandSparkles :size="17" :stroke-width="1.75" />
              </template>
              {{ t('actions.startCreating') }}
            </UiButton>
          </template>
        </UiActionBanner>
      </CategoryBrowser>
    </template>

    <template v-else>
      <DetailToolbar class="product-detail-toolbar">
        <RouterLink class="text-link market-back" :to="contentListReturn('/market')">
          <ArrowLeft :size="17" />{{ t('marketplace.back') }}
        </RouterLink>
      </DetailToolbar>
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
          <section v-if="detail.seller.id === session.user?.id" class="product-description">
            <h2>{{ t('marketplace.previewTitle') }}</h2>
            <p>{{ t('marketplace.previewHelp') }}</p>
            <UiButton as="RouterLink" variant="secondary" :to="`/workspace/products/${detail.id}`">
              {{ t('sellerProducts.manage') }}
            </UiButton>
            <UiButton v-if="!previewEditing && !detail.listingManaged" variant="secondary" @click="editPreview">
              {{ t('marketplace.editPreview') }}
            </UiButton>
            <UiForm v-else-if="previewEditing && !detail.listingManaged" :disabled="previewSaving" @submit="savePreview">
              <UiSelect v-model="previewSelection" :aria-label="t('marketplace.previewTitle')" :disabled="previewSaving">
                <option value="">
                  {{ t('marketplace.noPreview') }}
                </option>
                <option v-if="detail.previewAssetId && !previewAssets.some(item => item.id === detail?.previewAssetId)" :value="detail.previewAssetId">
                  {{ t('marketplace.currentPreview') }}
                </option>
                <option v-for="asset in previewAssets" :key="asset.id" :value="asset.id">
                  {{ asset.title }}
                </option>
              </UiSelect>
              <p v-if="previewError" role="alert">
                {{ previewError }}
              </p>
              <UiCardActions :value="t('marketplace.previewTitle')">
                <UiButton v-if="previewCursor || previewError" variant="secondary" :loading="previewLoading" :disabled="previewSaving" @click="loadPreviewAssets">
                  {{ t('actions.loadMore') }}
                </UiButton>
                <UiButton type="submit" variant="primary" :loading="previewSaving" :disabled="previewLoading">
                  {{ t('marketplace.savePreview') }}
                </UiButton>
                <UiButton variant="ghost" :disabled="previewSaving" @click="closePreview">
                  {{ t('actions.cancel') }}
                </UiButton>
              </UiCardActions>
            </UiForm>
          </section>
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
          <span>{{ t('marketplace.providerPrice') }}</span><strong>{{ money(detail.priceCents, detail.currency) }}</strong><p v-if="paymentEnabled">
            {{ t(paymentLiveMode ? 'marketplace.liveCharge' : 'marketplace.testCharge') }}
          </p>
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
            <UiButton v-if="checkoutNeedsAttention" as="RouterLink" variant="secondary" to="/workspace/orders">
              {{ t('marketplace.viewOrder') }}
            </UiButton>
            <UiButton class="command-button primary wide" variant="primary" type="submit" :loading="purchasing" :disabled="!paymentEnabled || !accepted" :aria-describedby="!paymentEnabled ? 'product-payment-disabled' : undefined">
              <template #start>
                <CircleDollarSign v-if="!purchasing" :size="17" />
              </template>{{ purchasing ? t('marketplace.openingCheckout') : t('marketplace.openCheckout') }}
            </UiButton>
            <p v-if="!paymentEnabled" id="product-payment-disabled" class="task-payment-note">
              {{ t('marketplace.paymentUnavailable') }}
            </p>
          </form>
          <small v-if="paymentEnabled">{{ t('marketplace.providerCheckoutEvidence') }}</small>
        </aside>
      </div>
    </template>
  </section>
</template>

<style scoped>
.market-page .market-filters {  grid-template-columns: minmax(160px, 1fr) 150px 150px 84px; }
.market-page.has-category-sidebar .market-filters { grid-template-columns: minmax(160px, 1fr) 150px 150px 84px; }
.market-search-submit { height: 40px; padding-inline: 12px; }
.market-catalog { display: grid; gap: 12px; }
.market-catalog .product-card { display: grid; grid-template-columns: 160px minmax(0, 1fr) var(--catalog-actions-width); grid-template-rows: auto; align-items: stretch; gap: 16px; }
.market-catalog .product-media { aspect-ratio: auto; min-height: 138px; }
.market-catalog .product-media img { position: absolute; inset: 0; transform: none; }

@media (min-width: 1321px) { .market-page.has-category-sidebar .market-filters { grid-template-columns: minmax(0, 1fr) 150px 84px; } }
@media (max-width: 767px) {
  .market-page .market-filters, .market-page.has-category-sidebar .market-filters { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) 84px; }
  .market-filters .market-search { grid-column: 1 / -1; }
  .market-catalog:not(.is-grid) .product-card { grid-template-columns: 100px minmax(0, 1fr); gap: 12px; }
  .market-results-summary { flex-wrap: wrap; }
}
</style>

<style scoped>
.market-page.is-detail { --content-max: 1400px; padding-top: 0; }
.product-detail-heading { padding: 24px; border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface); }
.product-detail-main { display: grid; gap: 24px; }
.product-description, .product-evidence, .license-panel { padding: 24px; border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface); }
.product-evidence { gap: 24px; font-size: 14px; line-height: 1.7; }
.product-seller { flex-wrap: wrap; }
.product-detail-media { border-radius: var(--radius-surface); border: 1px solid var(--border); }
.product-purchase-rail { position: static; }
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
@media(max-width: 600px) { .product-detail-heading, .product-description, .product-evidence, .license-panel { padding: 18px; } .product-detail-layout, .product-detail-main { gap: 16px; } .product-evidence { grid-template-columns: 1fr; } }
</style>
