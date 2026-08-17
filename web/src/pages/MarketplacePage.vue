<script setup lang="ts">
import {
  ArrowLeft, ArrowRight, BadgeCheck, Check, ChevronRight, CircleDollarSign, FileCheck2,
  Filter, LoaderCircle, LogIn, PackageCheck, RefreshCw, Search, ShieldCheck, ShoppingBag, UserPlus,
} from 'lucide-vue-next'
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api, messageFrom, type Product, type Purchase } from '../api/client'
import { formatCurrency } from '../lib/format'
import { useSessionStore } from '../stores/session'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const products = ref<Product[]>([])
const detail = ref<Product | null>(null)
const purchase = ref<Purchase | null>(null)
const loading = ref(true)
const purchasing = ref(false)
const error = ref('')
const accepted = ref(false)
const search = ref(String(route.query.q || ''))
const productType = ref(String(route.query.type || ''))
const sort = ref(String(route.query.sort || 'newest'))

const productID = computed(() => String(route.params.id || ''))
const isDetail = computed(() => Boolean(productID.value))
const types = ['prompt', 'workflow', 'asset', 'work']

function money(cents: number, currency = 'USD') {
  return formatCurrency(cents, currency, locale.value)
}

async function load() {
  loading.value = true
  error.value = ''
  purchase.value = null
  try {
    await session.ensure()
    if (productID.value) {
      detail.value = await api.getProduct(productID.value)
    } else {
      const response = await api.listProducts({ q: search.value, type: productType.value, sort: sort.value })
      products.value = response.items
      detail.value = null
    }
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    loading.value = false
  }
}

async function applyFilters() {
  await router.push({ path: '/market', query: {
    ...(search.value.trim() ? { q: search.value.trim() } : {}),
    ...(productType.value ? { type: productType.value } : {}),
    ...(sort.value !== 'newest' ? { sort: sort.value } : {}),
  } })
}

async function buy() {
  if (!session.user || !detail.value || !accepted.value) return
  purchasing.value = true
  error.value = ''
  try {
    purchase.value = await api.purchaseProduct(detail.value.id, accepted.value)
    detail.value = await api.getProduct(detail.value.id)
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    purchasing.value = false
  }
}

watch(() => route.fullPath, () => {
  search.value = String(route.query.q || '')
  productType.value = String(route.query.type || '')
  sort.value = String(route.query.sort || 'newest')
  accepted.value = false
  void load()
})

onMounted(() => void load())
</script>

<template>
  <section class="market-page content-width">
    <template v-if="!isDetail">
      <header class="market-header">
        <div>
          <span class="status-label">{{ t('marketplace.localTest') }}</span>
          <h1>{{ t('marketplace.title') }}</h1>
          <p>{{ t('marketplace.summary') }}</p>
        </div>
        <div class="market-header-actions">
          <RouterLink v-if="session.user" class="command-button secondary" to="/workspace/orders">
            <ShoppingBag :size="17" />{{ t('marketplace.myOrders') }}
          </RouterLink>
          <RouterLink v-if="!session.user" class="command-button secondary" :to="{ path: '/settings', query: { auth: 'login', returnTo: route.fullPath } }">
            <LogIn :size="17" />{{ t('account.signIn') }}
          </RouterLink>
          <RouterLink v-if="!session.user" class="command-button primary" :to="{ path: '/settings', query: { auth: 'register', returnTo: route.fullPath } }">
            <UserPlus :size="17" />{{ t('account.createAccount') }}
          </RouterLink>
        </div>
      </header>

      <form class="market-filters" role="search" @submit.prevent="applyFilters">
        <label class="market-search"><span class="sr-only">{{ t('actions.search') }}</span><Search :size="17" /><input v-model="search" type="search" :placeholder="t('marketplace.searchPlaceholder')" /></label>
        <label><Filter :size="16" /><span class="sr-only">{{ t('marketplace.allTypes') }}</span><select v-model="productType" @change="applyFilters"><option value="">{{ t('marketplace.allTypes') }}</option><option v-for="item in types" :key="item" :value="item">{{ t(`marketplace.types.${item}`) }}</option></select></label>
        <label><span class="sr-only">{{ t('marketplace.sortNewest') }}</span><select v-model="sort" @change="applyFilters"><option value="newest">{{ t('marketplace.sortNewest') }}</option><option value="price_asc">{{ t('marketplace.sortLow') }}</option><option value="price_desc">{{ t('marketplace.sortHigh') }}</option></select></label>
        <button class="icon-button" type="submit" :aria-label="t('actions.search')">
          <ArrowRight :size="17" />
        </button>
      </form>

      <div v-if="loading" class="page-state" aria-live="polite">
        <LoaderCircle class="spin" :size="20" />{{ t('marketplace.loading') }}
      </div>
      <div v-else-if="error" class="page-state" role="alert">
        <p>{{ error }}</p><button class="command-button secondary" type="button" @click="load">
          <RefreshCw :size="17" />{{ t('actions.retry') }}
        </button>
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
              <img :src="item.mediaUrl" :alt="item.title" :width="item.width || 1200" :height="item.height || 900" /><span>{{ t(`marketplace.types.${item.productType}`) }}</span><strong v-if="item.ownedAssetId"><BadgeCheck :size="15" />{{ t('marketplace.owned') }}</strong>
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
    </template>

    <template v-else>
      <RouterLink class="text-link market-back" to="/market">
        <ArrowLeft :size="17" />{{ t('marketplace.back') }}
      </RouterLink>
      <div v-if="loading" class="page-state" aria-live="polite">
        <LoaderCircle class="spin" :size="20" />{{ t('marketplace.loadingProduct') }}
      </div>
      <div v-else-if="error && !detail" class="page-state" role="alert">
        <p>{{ error }}</p><button class="command-button secondary" type="button" @click="load">
          <RefreshCw :size="17" />{{ t('actions.retry') }}
        </button>
      </div>
      <div v-else-if="detail" class="product-detail-layout">
        <main class="product-detail-main">
          <div class="product-detail-media">
            <img :src="detail.mediaUrl" :alt="detail.title" :width="detail.width || 1600" :height="detail.height || 1200" /><span>{{ t('status.demo') }}</span>
          </div>
          <section class="product-description">
            <span>{{ t(`marketplace.types.${detail.productType}`) }}</span><h1>{{ detail.title }}</h1><p>{{ detail.description }}</p><div class="product-seller">
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
          <span>{{ t('marketplace.localTestPrice') }}</span><strong>{{ money(detail.priceCents, detail.currency) }}</strong><p>{{ t('marketplace.noRealCharge') }}</p>
          <dl><div><dt>{{ t('marketplace.license') }}</dt><dd>{{ detail.license.name }}</dd></div><div><dt>{{ t('marketplace.refundWindow') }}</dt><dd>{{ t('marketplace.refundDays', { count: detail.license.refundWindowDays }) }}</dd></div></dl>
          <div v-if="purchase" class="purchase-success" role="status">
            <FileCheck2 :size="20" /><div><strong>{{ t('marketplace.purchaseSuccess') }}</strong><span>{{ t('marketplace.assetGranted') }}</span></div>
          </div>
          <div v-if="!session.user" class="market-auth-prompt">
            <div><h2>{{ t('marketplace.guestTitle') }}</h2><p>{{ t('marketplace.guestSummary') }}</p></div>
            <RouterLink class="command-button primary wide" :to="{ path: '/settings', query: { auth: 'login', returnTo: route.fullPath } }">
              <LogIn :size="17" />{{ t('account.signIn') }}
            </RouterLink>
            <RouterLink class="text-link" :to="{ path: '/settings', query: { auth: 'register', returnTo: route.fullPath } }">
              {{ t('account.createAccount') }}
            </RouterLink>
          </div>
          <template v-else-if="detail.ownedAssetId">
            <RouterLink class="command-button primary wide" :to="`/workspace/assets/${detail.ownedAssetId}`">
              <PackageCheck :size="17" />{{ t('marketplace.openAsset') }}
            </RouterLink>
            <RouterLink class="command-button secondary wide" to="/workspace/orders">
              {{ t('marketplace.viewOrder') }}
            </RouterLink>
          </template>
          <form v-else @submit.prevent="buy">
            <label class="license-accept"><input v-model="accepted" type="checkbox" /><span>{{ t('marketplace.acceptLicense', { name: detail.license.name, version: detail.license.version }) }}</span></label>
            <p v-if="error" class="form-error" role="alert">
              {{ error }}
            </p>
            <button class="command-button primary wide" type="submit" :disabled="!accepted || purchasing">
              <LoaderCircle v-if="purchasing" class="spin" :size="17" /><CircleDollarSign v-else :size="17" />{{ purchasing ? t('marketplace.purchasing') : t('marketplace.purchase') }}
            </button>
          </form>
          <small>{{ t('marketplace.checkoutEvidence') }}</small>
        </aside>
      </div>
    </template>
  </section>
</template>
