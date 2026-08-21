<script setup lang="ts">
import { ArrowLeft, ArrowRight, ClipboardList, FileSearch, RefreshCw, Search, ShoppingBag, Sparkles, UsersRound } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api, messageFrom, type SearchPage } from '../api/client'
import AssetMedia from '../components/domain/AssetMedia.vue'
import { formatCurrency } from '../lib/format'
import UiButton from '../components/ui/UiButton.vue'
import UiIconButton from '../components/ui/UiIconButton.vue'
import UiInput from '../components/ui/UiInput.vue'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const result = ref<SearchPage | null>(null)
const loading = ref(false)
const error = ref('')
const draft = ref('')
const supportedTypes = ['work', 'creator', 'product', 'demand'] as const
const selectedTypes = ref<string[]>([])

const query = computed(() => String(route.query.q || '').trim())
const page = computed(() => Math.max(1, Number(route.query.page || 1) || 1))
const typeIcons = { work: Sparkles, creator: UsersRound, product: ShoppingBag, demand: ClipboardList }

function readTypes() {
  const raw = String(route.query.types || '')
  return raw.split(',').filter((type) => supportedTypes.includes(type as typeof supportedTypes[number]))
}

async function load() {
  draft.value = query.value
  selectedTypes.value = readTypes()
  if (query.value.length < 2) {
    result.value = null
    error.value = ''
    return
  }
  loading.value = true
  error.value = ''
  try {
    result.value = await api.search({ q: query.value, types: selectedTypes.value, page: page.value, limit: 12 })
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    loading.value = false
  }
}

function submit() {
  const q = draft.value.trim()
  if (q.length < 2) return
  void router.push({ path: '/search', query: { q, ...(selectedTypes.value.length ? { types: selectedTypes.value.join(',') } : {}) } })
}

function toggleType(type: string) {
  selectedTypes.value = selectedTypes.value.includes(type) ? selectedTypes.value.filter((item) => item !== type) : [...selectedTypes.value, type]
  void router.push({ path: '/search', query: { q: query.value, ...(selectedTypes.value.length ? { types: selectedTypes.value.join(',') } : {}) } })
}

function changePage(nextPage: number) {
  void router.push({ path: '/search', query: { q: query.value, ...(selectedTypes.value.length ? { types: selectedTypes.value.join(',') } : {}), ...(nextPage > 1 ? { page: String(nextPage) } : {}) } })
}

function money(cents?: number, currency?: string) {
  return cents === undefined || !currency ? '' : formatCurrency(cents, currency, locale.value)
}

watch(() => route.fullPath, () => void load(), { immediate: true })
</script>

<template>
  <section class="search-page content-width">
    <header class="search-page-header">
      <span class="status-label">{{ t('search.label') }}</span>
      <h1>{{ t('search.title') }}</h1>
      <p>{{ t('search.summary') }}</p>
      <form role="search" @submit.prevent="submit">
        <Search :size="20" :stroke-width="1.75" aria-hidden="true" />
        <UiInput v-model="draft" type="search" minlength="2" maxlength="120" required :placeholder="t('search.placeholder')" :aria-label="t('actions.search')" />
        <UiButton class="command-button primary" variant="primary" type="submit">{{ t('actions.search') }}<template #end><ArrowRight :size="17" /></template></UiButton>
      </form>
    </header>

    <nav v-if="query.length >= 2" class="search-type-filter" :aria-label="t('search.filterLabel')">
      <button type="button" :class="{ active: selectedTypes.length === 0 }" @click="selectedTypes = []; submit()">
        {{ t('search.all') }}
      </button>
      <button v-for="type in supportedTypes" :key="type" type="button" :class="{ active: selectedTypes.includes(type) }" :aria-pressed="selectedTypes.includes(type)" @click="toggleType(type)">
        <component :is="typeIcons[type]" :size="15" />{{ t(`search.types.${type}`) }}
      </button>
    </nav>

    <div v-if="loading" class="page-state" aria-live="polite">
      {{ t('search.loading') }}
    </div>
    <div v-else-if="error" class="page-state" role="alert">
      <p>{{ error }}</p><UiButton class="command-button secondary" variant="secondary" @click="load"><template #start><RefreshCw :size="17" /></template>{{ t('actions.retry') }}</UiButton>
    </div>
    <div v-else-if="query.length < 2" class="search-start-state">
      <FileSearch :size="26" :stroke-width="1.5" /><h2>{{ t('search.startTitle') }}</h2><p>{{ t('search.startSummary') }}</p>
    </div>
    <template v-else-if="result">
      <div class="search-results-meta">
        <strong>{{ t('search.resultCount', { count: result.total }) }}</strong>
        <span>{{ t('search.policyVersion', { version: result.policyVersion, name: result.policyName }) }}<template v-if="result.policyVariant === 'candidate'"> · {{ t('search.stagedCandidate') }}</template></span>
      </div>
      <div v-if="result.items.length" class="search-results">
        <RouterLink v-for="item in result.items" :key="`${item.type}:${item.id}`" class="search-result" :to="item.path">
          <div class="search-result-media">
            <AssetMedia v-if="item.mediaUrl && item.mediaKind" :src="item.mediaUrl" :kind="item.mediaKind" :alt="item.title" :width="640" :height="480" :controls="false" />
            <span v-else class="search-result-avatar" aria-hidden="true">{{ item.title.slice(0, 1) }}</span>
          </div>
          <div class="search-result-copy">
            <span><component :is="typeIcons[item.type]" :size="14" />{{ t(`search.types.${item.type}`) }}<template v-if="item.creatorHandle"> · @{{ item.creatorHandle }}</template></span>
            <h2>{{ item.title }}</h2>
            <p>{{ item.summary }}</p>
            <div class="rank-signals" :aria-label="t('search.rankSignals')">
              <small v-for="signal in item.rankSignals" :key="signal">{{ t(`search.signals.${signal}`) }}</small>
            </div>
          </div>
          <strong v-if="item.priceCents !== undefined && item.currency" class="search-result-price">{{ money(item.priceCents, item.currency) }}</strong>
          <ArrowRight class="search-result-arrow" :size="18" />
        </RouterLink>
      </div>
      <div v-else class="search-start-state">
        <FileSearch :size="26" /><h2>{{ t('search.emptyTitle') }}</h2><p>{{ t('search.emptySummary') }}</p>
      </div>
      <nav v-if="result.total > result.limit" class="search-pagination" :aria-label="t('search.pagination')">
        <UiIconButton class="icon-button" :disabled="result.page <= 1" :label="t('search.previous')" @click="changePage(result.page - 1)">
          <ArrowLeft :size="18" />
        </UiIconButton>
        <span>{{ t('search.page', { page: result.page, total: Math.ceil(result.total / result.limit) }) }}</span>
        <UiIconButton class="icon-button" :disabled="!result.hasMore" :label="t('search.next')" @click="changePage(result.page + 1)">
          <ArrowRight :size="18" />
        </UiIconButton>
      </nav>
    </template>
  </section>
</template>
