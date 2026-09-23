<script setup lang="ts">
import UiEmptyState from '../components/ui/UiEmptyState.vue'
import UiActionBanner from '../components/ui/UiActionBanner.vue'
import UiCardContent from '../components/ui/UiCardContent.vue'
import UiCardActions from '../components/ui/UiCardActions.vue'
import UiCardTag from '../components/ui/UiCardTag.vue'
import UiFilterSearch from '../components/ui/UiFilterSearch.vue'
import UiFilterBar from '../components/ui/UiFilterBar.vue'
import UiLayoutSwitcher from '../components/ui/UiLayoutSwitcher.vue'
import UiCatalog from '../components/ui/UiCatalog.vue'
import UiContentCard from '../components/ui/UiContentCard.vue'
import { ArrowRight, BriefcaseBusiness, Compass, LoaderCircle, RefreshCw, Search, ShoppingBag, Sparkles, UsersRound } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api, messageFrom, type Work } from '../api/client'
import AssetMedia from '../components/domain/AssetMedia.vue'
import CategoryBrowser from '../components/domain/CategoryBrowser.vue'
import UiButton from '../components/ui/UiButton.vue'
import UiInput from '../components/ui/UiInput.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import { creationPath } from '../lib/contentPresentation'
import { textLength } from '../lib/unicodeText'
import PageHeader from '../components/ui/PageHeader.vue'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const works = ref<Work[]>([])
const total = ref(0)
const categoryCounts = ref<Record<string, number>>()
const loading = ref(true)
const loadingMore = ref(false)
const error = ref('')
const moreError = ref('')
const nextCursor = ref<string | null>(null)
const search = ref(String(route.query.q || ''))
const kind = ref(String(route.query.kind || ''))
const visibility = ref(String(route.query.promptVisibility || ''))
const layout = ref<'list' | 'grid'>('list')
const hasFilters = computed(() => Boolean(route.query.q || route.query.kind || route.query.promptVisibility))
const kinds = [
  { code: 'image', nameZh: '图片', nameEn: 'Images', icon: 'image', sortOrder: 1 },
  { code: 'video', nameZh: '视频', nameEn: 'Videos', icon: 'video', sortOrder: 2 },
  { code: 'audio', nameZh: '音频', nameEn: 'Audio', icon: 'audio', sortOrder: 3 },
  { code: 'document', nameZh: '文本', nameEn: 'Text', icon: 'prompt', sortOrder: 4 },
]
const kindName = (code: string) => {
  const item = kinds.find(item => item.code === code)
  return item ? (locale.value.startsWith('zh') ? item.nameZh : item.nameEn) : code
}
const publishedDate = (value: string) => new Intl.DateTimeFormat(locale.value, { year: 'numeric', month: 'short', day: 'numeric' }).format(new Date(value))
let version = 0
async function load(more = false) {
  if (more && (!nextCursor.value || loadingMore.value || loading.value)) return
  const requestVersion = more ? version : ++version
  if (more) { loadingMore.value = true; moreError.value = '' }
  else { loadingMore.value = false; loading.value = true; error.value = ''; moreError.value = ''; nextCursor.value = null }
  try {
    const page = await api.browseWorks({ q: String(route.query.q || ''), kind: String(route.query.kind || ''), promptVisibility: String(route.query.promptVisibility || ''), cursor: more ? nextCursor.value || undefined : undefined })
    if (requestVersion !== version) return
    const known = new Set(works.value.map(work => work.id))
    works.value = more ? [...works.value, ...page.items.filter(work => !known.has(work.id))] : page.items
    nextCursor.value = page.nextCursor
    categoryCounts.value = page.categoryCounts
    total.value = page.total
  } catch (reason) {
    if (requestVersion !== version) return
    if (more) moreError.value = messageFrom(reason)
    else error.value = messageFrom(reason)
  } finally {
    if (requestVersion === version) { if (more) loadingMore.value = false; else loading.value = false }
  }
}
async function applyFilters() {
  if (textLength(search.value.trim()) > 120) { error.value = t('inspiration.queryTooLong'); return }
  const previousPath = route.fullPath
  await router.push({ path: '/discover', query: { ...(search.value.trim() ? { q: search.value.trim() } : {}), ...(kind.value ? { kind: kind.value } : {}), ...(visibility.value ? { promptVisibility: visibility.value } : {}) } })
  if (route.fullPath === previousPath) await load()
}
async function selectKind(value: string) { kind.value = value; await applyFilters() }
async function clearFilters() { search.value = ''; kind.value = ''; visibility.value = ''; await applyFilters() }
watch(() => route.fullPath, () => {
  search.value = String(route.query.q || ''); kind.value = String(route.query.kind || ''); visibility.value = String(route.query.promptVisibility || '')
  void load()
}, { immediate: true })
</script>

<template>
  <section class="discover-page content-width has-category-sidebar">
    <PageHeader :title="t('discover.libraryTitle')" :summary="t('discover.librarySummary')" artwork-src="/illustrations/headers/inspiration.webp">
      <template #actions>
        <UiButton as="RouterLink" class="command-button primary" variant="primary" to="/create/image">
          <template #start>
            <Sparkles :size="17" />
          </template>{{ t('actions.startCreating') }}
        </UiButton>
      </template>
    </PageHeader>
    <UiFilterBar class="ui-filter-bar inspiration-filters" role="search" :aria-label="t('discover.filtersLabel')" @submit.prevent="applyFilters">
      <UiFilterSearch class="inspiration-search" :label="t('discover.searchLabel')">
        <UiInput v-model="search" type="search" :aria-invalid="textLength(search.trim()) > 120" :placeholder="t('discover.searchPlaceholder')" />
      </UiFilterSearch>
      <UiSelect v-model="kind" class="category-filter" :aria-label="t('discover.mediaType')" @change="applyFilters">
        <option value="">
          {{ t('discover.allMedia') }}
        </option><option v-for="item in kinds" :key="item.code" :value="item.code">
          {{ kindName(item.code) }}
        </option>
      </UiSelect>
      <UiSelect v-model="visibility" :aria-label="t('discover.promptFilter')" @change="applyFilters">
        <option value="">
          {{ t('discover.allPrompts') }}
        </option><option v-for="value in ['public', 'partial', 'private', 'purchased']" :key="value" :value="value">
          {{ t(`discover.promptStates.${value}`) }}
        </option>
      </UiSelect>
      <UiButton variant="primary" type="submit">
        <template #start>
          <Search :size="17" />
        </template>{{ t('actions.search') }}
      </UiButton>
    </UiFilterBar>
    <CategoryBrowser :items="kinds" :model-value="kind" :counts="categoryCounts" @update:model-value="selectKind">
      <div v-if="loading" class="page-state" aria-live="polite">
        <LoaderCircle class="spin" :size="20" />{{ t('status.loadingWorks') }}
      </div>
      <div v-else-if="error" class="page-state" role="alert">
        <p>{{ error }}</p><UiButton variant="secondary" @click="load()">
          <template #start>
            <RefreshCw :size="17" />
          </template>{{ t('actions.retry') }}
        </UiButton>
      </div>
      <template v-else>
        <div class="task-results-meta inspiration-results-meta">
          <div><strong>{{ t('discover.workCount', { count: total }) }}</strong><span>{{ t('discover.resultsSummary') }}</span></div>
          <div class="task-results-actions">
            <UiButton v-if="hasFilters" class="text-link" size="sm" variant="ghost" @click="clearFilters">
              {{ t('discover.clearFilters') }}
            </UiButton>
            <UiLayoutSwitcher v-model="layout" :label="t('discover.layout')" :list-label="t('discover.listView')" :grid-label="t('discover.gridView')" />
          </div>
        </div>
        <UiCatalog v-if="works.length" class="ui-catalog inspiration-catalog" :class="{ 'is-grid': layout === 'grid' }">
          <UiContentCard v-for="work in works" :key="work.id" class="ui-content-card inspiration-card">
            <RouterLink class="ui-content-card__media inspiration-media" :to="'/works/' + work.id" :aria-label="work.title">
              <AssetMedia :src="work.mediaUrl" :kind="work.mediaKind" :alt="work.title" :width="work.width || 800" :height="work.height || 600" :controls="false" />
            </RouterLink>
            <UiCardContent :title="work.title" :summary="work.summary" :to="'/works/' + work.id">
              <template #tags>
                <UiCardTag>{{ kindName(work.mediaKind) }}</UiCardTag>
              </template>
              <template #meta>
                <RouterLink :to="'/creators/' + work.author.handle">
                  {{ work.author.displayName }}
                </RouterLink><span>{{ work.modelName }}</span><time :datetime="work.publishedAt">{{ publishedDate(work.publishedAt) }}</time>
              </template>
            </UiCardContent>
            <UiCardActions class="inspiration-actions" :value="t(`discover.promptStates.${work.promptVisibility}`)" :description="t('discover.reviewLicense')">
              <UiButton as="RouterLink" variant="secondary" size="sm" :to="'/works/' + work.id">
                {{ t('actions.viewWork') }}<template #end>
                  <ArrowRight :size="16" />
                </template>
              </UiButton><UiButton as="RouterLink" variant="soft" size="sm" :to="{ path: creationPath(work.mediaKind), query: { sourceWorkId: work.id } }" :title="t('inspiration.remixHint')">
                <template #start>
                  <Sparkles :size="15" />
                </template>{{ t('inspiration.createFromWork') }}
              </UiButton>
            </UiCardActions>
          </UiContentCard>
        </UiCatalog>
        <UiEmptyState v-else class="inspiration-empty" :title="t(hasFilters ? 'discover.noSearchResults' : 'discover.emptyTitle')" :message="t(hasFilters ? 'discover.tryAnotherSearch' : 'discover.emptySummary')">
          <template #icon>
            <component :is="hasFilters ? Search : Compass" :size="24" :stroke-width="1.75" />
          </template>
          <template #actions>
            <UiButton v-if="hasFilters" variant="secondary" @click="clearFilters">
              {{ t('discover.clearFilters') }}
            </UiButton>
            <UiButton v-else as="RouterLink" variant="primary" to="/create/image">
              {{ t('actions.startCreating') }}
            </UiButton>
          </template>
        </UiEmptyState>
        <div v-if="nextCursor" class="catalog-pagination">
          <p v-if="moreError" role="alert" class="form-error">
            {{ moreError }}
          </p><UiButton variant="secondary" :loading="loadingMore" @click="load(true)">
            {{ t(moreError ? 'actions.retry' : 'actions.loadMore') }}
          </UiButton>
        </div>
      </template>
      <UiActionBanner :title="t('discover.exploreMore')" :summary="t('discover.exploreMoreSummary')">
        <template #icon>
          <Compass :size="24" :stroke-width="1.75" />
        </template>
        <template #actions>
          <UiButton as="RouterLink" variant="secondary" to="/market/demands">
            <template #start>
              <BriefcaseBusiness :size="16" :stroke-width="1.75" />
            </template>
            {{ t('nav.tasks') }}
          </UiButton>
          <UiButton as="RouterLink" variant="secondary" to="/community">
            <template #start>
              <UsersRound :size="16" :stroke-width="1.75" />
            </template>
            {{ t('nav.community') }}
          </UiButton>
          <UiButton as="RouterLink" variant="secondary" to="/market">
            <template #start>
              <ShoppingBag :size="16" :stroke-width="1.75" />
            </template>
            {{ t('nav.resourceMarket') }}
          </UiButton>
        </template>
      </UiActionBanner>
    </CategoryBrowser>
  </section>
</template>

<style scoped>

.inspiration-catalog { display: grid; gap: 12px; }
.inspiration-card { min-width: 0; display: grid; grid-template-columns: 168px minmax(0, 1fr) var(--catalog-actions-width); gap: 18px; }
.inspiration-media { min-width: 0; height: 144px; overflow: hidden; border-radius: var(--radius-control); background: var(--surface-muted); }
.inspiration-media :deep(.asset-renderer) { height: 100%; width: 100%; }
.inspiration-media :deep(img), .inspiration-media :deep(video) { width: 100%; height: 100%; object-fit: cover; }
.inspiration-media :deep(.asset-document), .inspiration-media :deep(.asset-audio) { min-height: 140px; max-height: 180px; overflow: hidden; }

@media (max-width: 767px) {
  .inspiration-filters { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) 84px; }
  .inspiration-search { grid-column: 1 / -1; }
  .inspiration-card { grid-template-columns: 100px minmax(0, 1fr); gap: 12px; }
  .inspiration-results-meta { flex-wrap: wrap; }
}
</style>
