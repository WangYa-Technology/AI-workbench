<script setup lang="ts">
import {
  ArrowLeft, ArrowRight, BadgeCheck, Check, ChevronRight, CircleDollarSign,
  Filter, Layers3, LoaderCircle, LogIn, PackageCheck, RefreshCw, Search, ShieldCheck, ShoppingBag,
  UserPlus, Users,
} from 'lucide-vue-next'
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api, messageFrom, type Product } from '../api/client'
import { formatCurrency } from '../lib/format'
import { openCheckoutWindow } from '../lib/checkout'
import { useSessionStore } from '../stores/session'
import CategoryBrowser from '../components/domain/CategoryBrowser.vue'
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
const detail = ref<Product | null>(null)
const loading = ref(true)
const purchasing = ref(false)
const error = ref('')
const accepted = ref(false)
const paymentEnabled = ref(false)
const paymentLiveMode = ref(false)
const search = ref(String(route.query.q || ''))
const sort = ref(String(route.query.sort || 'newest'))

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
  { value: marketStats.value.products, label: t('marketplace.listedProducts'), icon: ShoppingBag, tone: 'blue' as const },
  { value: marketStats.value.creators, label: t('marketplace.activeCreators'), icon: Users, tone: 'violet' as const },
  { value: marketStats.value.types, label: t('marketplace.licenseTypes'), icon: Layers3, tone: 'green' as const },
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
        <UiIconButton class="icon-button" :label="t('actions.search')" type="submit">
          <ArrowRight :size="17" />
        </UiIconButton>
      </form>

      <CategoryBrowser :items="types" :model-value="category" @update:model-value="selectCategory">
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
        </div>
        <template v-else>
          <div class="market-results-meta">
            <strong>{{ products.length }} {{ t('marketplace.results') }}</strong>
          </div>
          <div class="product-grid">
            <RouterLink v-for="item in products" :key="item.id" class="product-card" :to="`/market/assets/${item.id}`">
              <div class="product-media">
                <img :src="item.mediaUrl" :alt="item.title" :width="item.width || 1200" :height="item.height || 900" /><span>{{ categoryName(item.category) }}</span><strong v-if="item.ownedAssetId"><BadgeCheck :size="15" />{{ t('marketplace.owned') }}</strong>
              </div>
              <div class="product-card-copy">
                <div><span>@{{ item.seller.handle }}</span><span>{{ item.license.name }}</span></div>
                <h2>{{ item.title }}</h2>
                <p>{{ item.description }}</p>
                <footer><strong>{{ money(item.priceCents, item.currency) }}</strong><ChevronRight :size="17" /></footer>
              </div>
            </RouterLink>
          </div>
        </template>
      </CategoryBrowser>
    </template>

    <template v-else>
      <RouterLink class="text-link market-back" to="/market">
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
        <main class="product-detail-main">
          <div class="product-detail-media">
            <img :src="detail.mediaUrl" :alt="detail.title" :width="detail.width || 1600" :height="detail.height || 1200" /><span>{{ t('status.demo') }}</span>
          </div>
          <section class="product-description">
            <span>{{ categoryName(detail.category) }}</span><h1>{{ detail.title }}</h1><p>{{ detail.description }}</p><div class="product-seller">
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
              <span><Check :size="16" />{{ detail.license.allowsCommercial ? t('marketplace.commercialYes') : t('marketplace.commercialNo') }}</span><span><Check :size="16" />{{ detail.license.allowsDerivatives ? t('marketplace.derivativesYes') : t('marketplace.derivativesNo') }}</span><span><Check :size="16" />{{ detail.license.attributionRequired ? t('marketplace.attributionYes') : t('marketplace.attributionNo') }}</span><span><Check :size="16" />{{ detail.license.allowsRedistribution ? t('marketplace.redistributionYes') : t('marketplace.redistributionNo') }}</span>
            </div>
            <p class="license-terms">
              {{ detail.license.terms }}
            </p>
          </section>
        </main>

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
              </template>{{ purchasing ? t('marketplace.openingCheckout') : paymentEnabled ? t('marketplace.openCheckout') : t('marketplace.paymentUnavailable') }}
            </UiButton>
          </form>
          <small>{{ t(paymentEnabled ? 'marketplace.providerCheckoutEvidence' : 'marketplace.paymentUnavailable') }}</small>
        </aside>
      </div>
    </template>
  </section>
</template>
