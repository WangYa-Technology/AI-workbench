<script setup lang="ts">
import { ArrowRight, BriefcaseBusiness, Compass, Grid2X2, LayoutGrid, List, LoaderCircle, RefreshCw, Search, ShoppingBag, Sparkles, UsersRound } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api, messageFrom, type Work } from '../api/client'
import AssetMedia from '../components/domain/AssetMedia.vue'
import CategoryBrowser from '../components/domain/CategoryBrowser.vue'
import UiButton from '../components/ui/UiButton.vue'
import UiIconButton from '../components/ui/UiIconButton.vue'
import UiInput from '../components/ui/UiInput.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import PageHero from '../components/ui/PageHero.vue'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const works = ref<Work[]>([])
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
const discoverHeroStats = computed(() => [
  { value: works.value.length, label: t('discover.loadedWorks'), icon: LayoutGrid, tone: 'blue' as const },
  { value: new Set(works.value.map(work => work.author.handle)).size, label: t('discover.loadedCreators'), icon: UsersRound, tone: 'violet' as const },
  { value: new Set(works.value.map(work => work.modelName)).size, label: t('discover.modelsInUse'), icon: Sparkles, tone: 'green' as const },
])
const publishedDate = (value: string) => new Intl.DateTimeFormat(locale.value, { year: 'numeric', month: 'short', day: 'numeric' }).format(new Date(value))
let version = 0
async function load(more = false) {
  if (more && (!nextCursor.value || loadingMore.value || loading.value)) return
  const requestVersion = more ? version : ++version
  if (more) { loadingMore.value = true; moreError.value = '' }
  else { loading.value = true; error.value = ''; moreError.value = ''; nextCursor.value = null }
  try {
    const page = await api.browseWorks({ q: String(route.query.q || ''), kind: String(route.query.kind || ''), promptVisibility: String(route.query.promptVisibility || ''), cursor: more ? nextCursor.value || undefined : undefined })
    if (requestVersion !== version) return
    const known = new Set(works.value.map(work => work.id))
    works.value = more ? [...works.value, ...page.items.filter(work => !known.has(work.id))] : page.items
    nextCursor.value = page.nextCursor
    categoryCounts.value = page.categoryCounts
  } catch (reason) {
    if (requestVersion !== version) return
    if (more) moreError.value = messageFrom(reason)
    else error.value = messageFrom(reason)
  } finally {
    if (more) loadingMore.value = false
    else if (requestVersion === version) loading.value = false
  }
}
async function applyFilters() {
  await router.push({ path: '/discover', query: { ...(search.value.trim() ? { q: search.value.trim() } : {}), ...(kind.value ? { kind: kind.value } : {}), ...(visibility.value ? { promptVisibility: visibility.value } : {}) } })
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
    <PageHero :eyebrow="t('discover.eyebrow')" :eyebrow-icon="Sparkles" :title="t('discover.libraryTitle')" :summary="t('discover.librarySummary')" :stats="discoverHeroStats" :stats-label="t('discover.statsLabel')" artwork-src="/discover/inspiration-hero.png" :artwork-width="1254" :artwork-height="1254">
      <template #actions>
        <UiButton as="RouterLink" class="command-button secondary" variant="secondary" to="/market/demands">
          <template #start>
            <BriefcaseBusiness :size="17" />
          </template>{{ t('actions.findBrief') }}
        </UiButton>
        <UiButton as="RouterLink" class="command-button primary" variant="primary" to="/create/image">
          <template #start>
            <Sparkles :size="17" />
          </template>{{ t('actions.startCreating') }}
        </UiButton>
      </template>
    </PageHero>
    <form class="inspiration-filters" role="search" :aria-label="t('discover.filtersLabel')" @submit.prevent="applyFilters">
      <label class="inspiration-search"><span class="sr-only">{{ t('discover.searchLabel') }}</span><Search :size="17" /><UiInput v-model="search" type="search" maxlength="120" :placeholder="t('discover.searchPlaceholder')" /></label>
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
    </form>
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
          <div><strong>{{ t('discover.workCount', { count: works.length }) }}</strong><span>{{ t('discover.resultsSummary') }}</span></div>
          <div class="task-results-actions">
            <UiButton v-if="hasFilters" class="text-link" size="sm" variant="ghost" @click="clearFilters">
              {{ t('discover.clearFilters') }}
            </UiButton>
            <div class="task-layout-switcher" :aria-label="t('discover.layout')">
              <UiIconButton size="sm" class="icon-button" variant="ghost" :class="{ active: layout === 'list' }" :aria-pressed="layout === 'list'" :label="t('discover.listView')" @click="layout = 'list'">
                <List :size="17" />
              </UiIconButton>
              <UiIconButton size="sm" class="icon-button" variant="ghost" :class="{ active: layout === 'grid' }" :aria-pressed="layout === 'grid'" :label="t('discover.gridView')" @click="layout = 'grid'">
                <Grid2X2 :size="16" />
              </UiIconButton>
            </div>
          </div>
        </div>
        <div v-if="works.length" class="inspiration-catalog" :class="{ 'is-grid': layout === 'grid' }">
          <article v-for="work in works" :key="work.id" class="inspiration-card">
            <RouterLink class="inspiration-media" :to="'/works/' + work.id" :aria-label="work.title">
              <AssetMedia :src="work.mediaUrl" :kind="work.mediaKind" :alt="work.title" :width="work.width || 800" :height="work.height || 600" :controls="false" />
            </RouterLink>
            <div class="inspiration-copy">
              <div class="inspiration-tags">
                <span>{{ kindName(work.mediaKind) }}</span><span v-if="work.licenseCode.startsWith('demo')">{{ t('status.demo') }}</span>
              </div>
              <RouterLink :to="'/works/' + work.id">
                <h2>{{ work.title }}</h2>
              </RouterLink>
              <p>{{ work.summary }}</p>
              <div class="inspiration-byline">
                <RouterLink :to="'/creators/' + work.author.handle">
                  {{ work.author.displayName }}
                </RouterLink><span>{{ work.modelName }}</span><time :datetime="work.publishedAt">{{ publishedDate(work.publishedAt) }}</time>
              </div>
            </div>
            <div class="inspiration-actions">
              <strong>{{ t(`discover.promptStates.${work.promptVisibility}`) }}</strong><small>{{ t(work.licenseCode.startsWith('demo') ? 'discover.demoLicense' : 'discover.reviewLicense') }}</small><UiButton as="RouterLink" variant="primary" :to="'/works/' + work.id">
                {{ t('actions.viewWork') }}<template #end>
                  <ArrowRight :size="16" />
                </template>
              </UiButton><UiButton v-if="work.mediaKind === 'image'" as="RouterLink" variant="ghost" :to="'/create/image?sourceWorkId=' + work.id">
                <template #start>
                  <Sparkles :size="15" />
                </template>{{ t('actions.remix') }}
              </UiButton>
            </div>
          </article>
        </div>
        <section v-else class="inspiration-empty">
          <Search :size="26" /><h2>{{ t(hasFilters ? 'discover.noSearchResults' : 'discover.emptyTitle') }}</h2><p>{{ t(hasFilters ? 'discover.tryAnotherSearch' : 'discover.emptySummary') }}</p><UiButton v-if="hasFilters" variant="secondary" @click="clearFilters">
            {{ t('discover.clearFilters') }}
          </UiButton><UiButton v-else as="RouterLink" variant="primary" to="/create/image">
            {{ t('actions.startCreating') }}
          </UiButton>
        </section>
        <div v-if="nextCursor" class="inspiration-pagination">
          <p v-if="moreError" role="alert" class="form-error">
            {{ moreError }}
          </p><UiButton variant="secondary" :loading="loadingMore" @click="load(true)">
            {{ t(moreError ? 'actions.retry' : 'actions.loadMore') }}
          </UiButton>
        </div>
      </template>
    </CategoryBrowser>
    <footer class="inspiration-footer">
      <span class="inspiration-footer-title"><Compass :size="18" aria-hidden="true" />{{ t('discover.exploreMore') }}</span>
      <nav :aria-label="t('discover.exploreMore')">
        <RouterLink to="/community">
          <UsersRound :size="16" aria-hidden="true" /><span>{{ t('nav.community') }}</span><ArrowRight :size="14" aria-hidden="true" />
        </RouterLink><RouterLink to="/market">
          <ShoppingBag :size="16" aria-hidden="true" /><span>{{ t('nav.resourceMarket') }}</span><ArrowRight :size="14" aria-hidden="true" />
        </RouterLink>
      </nav>
    </footer>
  </section>
</template>

<style scoped>
.inspiration-filters { display: grid; grid-template-columns: minmax(160px, 1fr) 150px 180px 84px; gap: 8px; align-items: center; min-height: 54px; margin-bottom: 16px; padding: 6px 8px; border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface-muted); }
.inspiration-filters :deep(.ui-select__trigger), .inspiration-filters > .ui-button { height: 40px; min-height: 40px; }
.inspiration-filters :deep(.ui-select__trigger) { border-color: var(--border); background: var(--surface); font-size: 12px; }
.inspiration-search { display: flex; gap: 9px; align-items: center; min-width: 0; height: 40px; padding-inline: 12px; border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--surface); color: var(--text-secondary); }
.inspiration-search :deep(input) { min-width: 0; width: 100%; height: 100%; padding: 0; border: 0; box-shadow: none; background: transparent; }
.inspiration-search:focus-within { border-color: var(--focus); }
.inspiration-catalog { display: grid; gap: 12px; }
.inspiration-card { min-width: 0; display: grid; grid-template-columns: 168px minmax(0, 1fr) 180px; gap: 18px; padding: 12px; border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface); }
.inspiration-card:hover { border-color: var(--border-strong); }
.inspiration-media { min-width: 0; height: 144px; overflow: hidden; border-radius: var(--radius-control); background: var(--surface-muted); }
.inspiration-media :deep(.asset-renderer) { height: 100%; width: 100%; }
.inspiration-media :deep(img), .inspiration-media :deep(video) { width: 100%; height: 100%; object-fit: cover; }
.inspiration-media :deep(.asset-document), .inspiration-media :deep(.asset-audio) { min-height: 140px; max-height: 180px; overflow: hidden; }
.inspiration-copy { min-width: 0; display: flex; flex-direction: column; justify-content: center; gap: 8px; padding-block: 4px; }
.inspiration-copy h2 { margin: 0; color: var(--text); font-size: 16px; line-height: 1.45; overflow-wrap: anywhere; }
.inspiration-copy p { margin: 0; color: var(--text-secondary); font-size: 12px; line-height: 1.6; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.inspiration-tags { display: flex; flex-wrap: wrap; gap: 6px; }
.inspiration-tags span { padding: 4px 7px; border-radius: 6px; background: var(--accent-soft); color: var(--accent-readable); font-size: 10px; font-weight: 600; }
.inspiration-tags span + span { background: var(--surface-muted); color: var(--text-secondary); font-weight: 500; }
.inspiration-byline { display: flex; flex-wrap: wrap; gap: 6px 12px; color: var(--text-secondary); font-size: 11px; }
.inspiration-byline a { color: var(--text); font-weight: 560; }
.inspiration-byline time { color: var(--text-tertiary); }
.inspiration-actions { display: flex; flex-direction: column; gap: 6px; padding: 4px 0 4px 16px; border-left: 1px solid var(--border); }
.inspiration-actions > span, .inspiration-actions small { color: var(--text-secondary); font-size: 11px; overflow-wrap: anywhere; }
.inspiration-actions strong { color: var(--text); font-size: 12px; }
.inspiration-actions .ui-button { min-height: 34px; font-size: 12px; }
.inspiration-actions small { margin-bottom: auto; }
.inspiration-catalog.is-grid { grid-template-columns: repeat(auto-fill, minmax(min(100%, 260px), 1fr)); }
.is-grid .inspiration-card { grid-template-columns: minmax(0, 1fr); grid-template-rows: auto 1fr auto; gap: 12px; }
.is-grid .inspiration-media { aspect-ratio: 16 / 10; height: auto; min-height: 0; }
.is-grid .inspiration-copy { justify-content: flex-start; }
.is-grid .inspiration-byline { margin-top: auto; }
.is-grid .inspiration-actions { padding: 12px 0 0; border-left: 0; border-top: 1px solid var(--border); }
.is-grid .inspiration-actions { display: grid; grid-template-columns: 1fr 1fr; align-items: center; }
.is-grid .inspiration-actions strong, .is-grid .inspiration-actions small { grid-column: 1 / -1; }
.inspiration-empty { min-height: 230px; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px; padding: 24px; border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface); text-align: center; }
.inspiration-empty h2 { margin: 0; font-size: 18px; }
.inspiration-empty p { margin: 0; color: var(--text-secondary); font-size: 13px; }
.inspiration-pagination { display: grid; justify-items: center; gap: 8px; margin-top: 20px; }
.inspiration-footer { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 10px 20px; margin-top: 18px; padding: 10px 14px; border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface-muted); color: var(--text-secondary); font-size: 13px; }
.inspiration-footer-title { display: inline-flex; align-items: center; gap: 9px; font-weight: 560; }
.inspiration-footer-title svg { color: var(--text-tertiary); }
.inspiration-footer nav { display: flex; flex-wrap: wrap; gap: 8px; }
.inspiration-footer a { display: inline-flex; align-items: center; gap: 8px; min-height: 36px; padding: 0 11px; border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--surface); color: var(--text-secondary); transition: background-color var(--duration-quick) var(--ease-smooth-out), color var(--duration-quick) var(--ease-smooth-out); }
.inspiration-footer a:hover { background: var(--accent-soft); color: var(--accent-readable); }
.inspiration-footer a:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
@media (min-width: 1321px) { .inspiration-footer { margin-left: 234px; } }
@media (min-width: 1321px) { .inspiration-filters { grid-template-columns: minmax(0, 1fr) 180px 84px; } }
@media (max-width: 760px) {
  .inspiration-filters { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) 84px; }
  .inspiration-search { grid-column: 1 / -1; }
  .inspiration-card { grid-template-columns: 100px minmax(0, 1fr); gap: 12px; }
  .inspiration-actions { grid-column: 1 / -1; display: grid; grid-template-columns: 1fr 1fr; padding: 10px 0 0; border-left: 0; border-top: 1px solid var(--border); }
  .inspiration-actions small { grid-column: 1 / -1; }
  .inspiration-actions strong { grid-column: 1 / -1; }
  .inspiration-actions > span, .inspiration-actions strong { align-self: center; }
  .inspiration-results-meta { flex-wrap: wrap; }
}
</style>
