<script setup lang="ts">
import { Clock3, Eye, Heart, LayoutGrid, LoaderCircle, LogIn, MessageCircle, MessageSquareText, MoreHorizontal, Plus, Search, ShieldCheck, Sparkles, UserPlus, X } from 'lucide-vue-next'
import { computed, onMounted, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { onClickOutside } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api, messageFrom, type CommunityPost, type CommunityReport } from '../api/client'
import CategoryBrowser from '../components/domain/CategoryBrowser.vue'
import type { TaskType } from '../api/client'
import AssetMedia from '../components/domain/AssetMedia.vue'
import CommunityPostDrawer from '../components/domain/CommunityPostDrawer.vue'
import { useSessionStore } from '../stores/session'
import PageHero from '../components/ui/PageHero.vue'
import UiButton from '../components/ui/UiButton.vue'
import UiIconButton from '../components/ui/UiIconButton.vue'
import UiInput from '../components/ui/UiInput.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiTabs from '../components/ui/UiTabs.vue'
import UiTextarea from '../components/ui/UiTextarea.vue'

const { locale, t } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const posts = ref<CommunityPost[]>([])
const postNextCursor = ref<string | null>(null)
const postLoadingMore = ref(false)
const reports = ref<CommunityReport[]>([])
const reportNextCursor = ref<string | null>(null)
const reportLoadingMore = ref(false)
const loading = ref(true)
const actionLoading = ref('')
const error = ref('')
const feedback = ref('')
const sortMode = computed(() => route.query.sort === 'discussed' ? 'discussed' : 'latest')
function selectSort(value: string) { void router.push({ query: { ...route.query, q: search.value.trim() || undefined, sort: value === 'discussed' ? value : undefined } }) }
const sortTabs = computed(() => [
  { value: 'latest', label: t('community.latestDiscussions'), icon: Clock3 },
  { value: 'discussed', label: t('community.mostDiscussed'), icon: MessageCircle },
])
const search = ref(String(route.query.q || ''))
const categoryCounts = ref<Record<string, number>>()
let searchTimer: ReturnType<typeof globalThis.setTimeout>
watch(search, value => { globalThis.clearTimeout(searchTimer); searchTimer = globalThis.setTimeout(() => { void router.replace({ query: { ...route.query, q: value.trim() || undefined } }) }, 250) })
onBeforeUnmount(() => globalThis.clearTimeout(searchTimer))
const categories = ref<TaskType[]>([])
const category = ref(String(route.query.category || ''))
const categoryName = (code?: string) => { const item = categories.value.find(i => i.code === code); return item ? (locale.value.startsWith('zh') ? item.nameZh : item.nameEn) : code || '' }
async function selectCategory(value: string) { await router.push({ query: { ...route.query, q: search.value.trim() || undefined, category: value || undefined } }) }
watch(() => [route.query.category, route.query.view, route.query.q, route.query.sort], () => { category.value = String(route.query.category || ''); search.value = String(route.query.q || ''); mineOnly.value = route.query.view === 'mine' && Boolean(session.user); postNextCursor.value = null; void load() })
const mineOnly = ref(false)
const createOpen = ref(false)
const showCases = ref(false)
const toolsOpen = ref(false)
const toolsRoot = ref<globalThis.HTMLElement | null>(null)
const toolsTrigger = ref<InstanceType<typeof UiIconButton> | null>(null)
onClickOutside(toolsRoot, () => { toolsOpen.value = false })
function closeTools() {
  toolsOpen.value = false
  const root = toolsTrigger.value?.$el as globalThis.HTMLElement | undefined
  root?.focus()
}
async function openCasesFromTools() {
  closeTools()
  await toggleCases()
}
const appealForm = reactive({ reportId: '', reason: '' })
const communityHeroStats = computed(() => [
  { value: posts.value.length, label: t('content.loadedPosts'), icon: LayoutGrid, tone: 'blue' as const },
  { value: totalReplyCount.value, label: t('content.loadedReplies'), icon: MessageCircle, tone: 'violet' as const },
  { value: totalLikeCount.value, label: t('content.loadedLikes'), icon: Heart, tone: 'green' as const },
])

function formatPublishedAt(value: string) {
  return new Intl.DateTimeFormat(locale.value, { year: 'numeric', month: 'short', day: 'numeric' }).format(new Date(value))
}

function formatMediaKind(value?: string) {
  if (!value) return t('community.discussionType')
  const normalized = normalizeMediaKind(value)
  if (normalized === 'video') return t('create.modes.video')
  if (normalized === 'music') return t('create.modes.music')
  if (normalized === 'image') return t('create.modes.image')
  return t('community.workTopic')
}

function normalizeMediaKind(value?: string): 'image' | 'video' | 'music' | 'other' {
  if (!value) return 'other'
  const normalized = value.toLowerCase()
  if (normalized.includes('video')) return 'video'
  if (normalized.includes('audio') || normalized.includes('music')) return 'music'
  if (normalized.includes('image')) return 'image'
  return 'other'
}

function formatActivity(value: string) {
  const elapsed = new Date(value).getTime() - Date.now()
  const days = Math.round(elapsed / 86_400_000)
  if (Math.abs(days) >= 1) return new Intl.RelativeTimeFormat(locale.value, { numeric: 'auto' }).format(days, 'day')
  const hours = Math.round(elapsed / 3_600_000)
  if (Math.abs(hours) >= 1) return new Intl.RelativeTimeFormat(locale.value, { numeric: 'auto' }).format(hours, 'hour')
  const minutes = Math.round(elapsed / 60_000)
  return new Intl.RelativeTimeFormat(locale.value, { numeric: 'auto' }).format(minutes, 'minute')
}

const displayedPosts = computed(() => posts.value)

const hasActiveFilters = computed(() => Boolean(search.value.trim()) || Boolean(category.value))
const totalReplyCount = computed(() => posts.value.reduce((total, post) => total + post.commentCount, 0))
const totalLikeCount = computed(() => posts.value.reduce((total, post) => total + post.likeCount, 0))

function clearFilters() {
  globalThis.clearTimeout(searchTimer)
  search.value = ''
  void router.replace({ query: { ...route.query, q: undefined, category: undefined } })
}

let loadVersion = 0
async function load() {
  const version = ++loadVersion
  loading.value = true
  postLoadingMore.value = false
  error.value = ''
  try {
    const [directory, page] = await Promise.all([api.listTaskTypes('community'), api.listCommunityPosts({ sort: sortMode.value, q: String(route.query.q || ''), category: category.value, limit: 20, ...(mineOnly.value ? { mine: true } : {}) })])
    if (version !== loadVersion) return
    categories.value = directory.items
    posts.value = page.items
    categoryCounts.value = page.categoryCounts
    postNextCursor.value = page.nextCursor || null
  } catch (reason) {
    if (version === loadVersion) error.value = messageFrom(reason)
  } finally {
    if (version === loadVersion) loading.value = false
  }
}

async function loadMorePosts() {
  if (!postNextCursor.value || postLoadingMore.value) return
  postLoadingMore.value = true
  const version = loadVersion
  feedback.value = ''
  try {
    const page = await api.listCommunityPosts({ sort: sortMode.value, q: String(route.query.q || ''), category: category.value, limit: 20, cursor: postNextCursor.value, ...(mineOnly.value ? { mine: true } : {}) })
    if (version !== loadVersion) return
    const known = new Set(posts.value.map(item => item.id))
    posts.value = [...posts.value, ...page.items.filter(item => !known.has(item.id))]
    postNextCursor.value = page.nextCursor || null
  } catch (reason) {
    if (version === loadVersion) feedback.value = messageFrom(reason)
  } finally {
    if (version === loadVersion) postLoadingMore.value = false
  }
}

async function requireAccount() {
  const user = await session.ensure()
  if (user) return true
  await router.push({ path: '/auth', query: { auth: 'login', returnTo: route.fullPath } })
  return false
}

async function selectMyPosts() {
  if (!await requireAccount()) return
  mineOnly.value = !mineOnly.value
  const query = { ...route.query }
  if (mineOnly.value) query.view = 'mine'
  else delete query.view
  await router.replace({ path: route.path, query })
}

async function openCreatePost() {
  if (!await requireAccount()) return
  createOpen.value = true
}

function handlePostCreated(post: CommunityPost) {
  if (!category.value || category.value === post.category) posts.value = [post, ...posts.value.filter(item => item.id !== post.id)]
  feedback.value = t('community.postPublished')
}

async function toggleCases() {
  if (!await requireAccount()) return
  showCases.value = !showCases.value
  if (!showCases.value) return
  actionLoading.value = 'cases'
  feedback.value = ''
  try {
    const page = await api.listMyCommunityReports({ limit: 20 })
    reports.value = page.items
    reportNextCursor.value = page.nextCursor || null
  } catch (reason) {
    feedback.value = messageFrom(reason)
  } finally {
    actionLoading.value = ''
  }
}

async function loadMoreCases() {
  if (!reportNextCursor.value || reportLoadingMore.value) return
  reportLoadingMore.value = true
  feedback.value = ''
  try {
    const page = await api.listMyCommunityReports({ limit: 20, cursor: reportNextCursor.value })
    const known = new Set(reports.value.map(item => item.id))
    reports.value = [...reports.value, ...page.items.filter(item => !known.has(item.id))]
    reportNextCursor.value = page.nextCursor || null
  } catch (reason) {
    feedback.value = messageFrom(reason)
  } finally {
    reportLoadingMore.value = false
  }
}

async function submitAppeal() {
  actionLoading.value = `${appealForm.reportId}:appeal`
  feedback.value = ''
  try {
    const appeal = await api.createCommunityAppeal(appealForm.reportId, appealForm.reason)
    reports.value = reports.value.map(item => item.id === appeal.reportId ? { ...item, appeal, viewerCanAppeal: false } : item)
    Object.assign(appealForm, { reportId: '', reason: '' })
    feedback.value = t('community.appealSubmitted')
  } catch (reason) {
    feedback.value = messageFrom(reason)
  } finally {
    actionLoading.value = ''
  }
}

onMounted(async () => {
  await session.ensure()
  mineOnly.value = route.query.view === 'mine' && Boolean(session.user)
  await load()
})
</script>

<template>
  <section class="community-page content-width">
    <PageHero
      :eyebrow="t('community.networkLabel')"
      :eyebrow-icon="Sparkles"
      :title="t('community.title')"
      :summary="t('community.summary')"
      :stats="communityHeroStats"
      :stats-label="t('community.statsLabel')"
      artwork-src="/community/community-hero.webp"
    >
      <template #actions>
        <UiButton v-if="session.user" class="command-button secondary" variant="secondary" :class="{ active: mineOnly }" :aria-pressed="mineOnly" @click="selectMyPosts">
          <template #start>
            <MessageSquareText :size="17" />
          </template>{{ t('community.myPosts') }}
        </UiButton>
        <UiButton v-if="session.user" class="command-button primary" variant="primary" @click="openCreatePost">
          <template #start>
            <Plus :size="17" />
          </template>{{ t('community.publishPost') }}
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
    <div :class="{ 'has-category-sidebar': categories.length > 0 }">
      <div class="controls-with-switcher community-controls-row has-switcher">
        <div class="view-switcher-bar">
          <UiTabs class="view-switcher" :model-value="sortMode" :items="sortTabs" :label="t('community.sortLabel')" @update:model-value="selectSort(String($event))" />
        </div>
        <section class="community-toolbar" :aria-label="t('community.filtersLabel')">
          <label class="community-search-control">
            <span class="sr-only">{{ t('community.searchLabel') }}</span>
            <Search :size="18" :stroke-width="1.75" aria-hidden="true" />
            <UiInput v-model="search" type="search" maxlength="120" :placeholder="t('community.searchPlaceholder')" />
          </label>
          <UiSelect :model-value="category" class="community-type-control category-filter" :aria-label="t('community.typeLabel')" @update:model-value="selectCategory(String($event))">
            <option value="">
              {{ t('community.allTypes') }}
            </option>
            <option v-for="item in categories" :key="item.code" :value="item.code">
              {{ categoryName(item.code) }}
            </option>
          </UiSelect>
          <div ref="toolsRoot" class="community-tools" @keydown.esc.stop.prevent="closeTools" @focusout="(event) => { if (!toolsRoot?.contains(event.relatedTarget as Node)) toolsOpen = false }">
            <UiIconButton ref="toolsTrigger" variant="outline" class="ui-select__trigger" :label="t('community.communityActions')" :aria-expanded="toolsOpen" aria-controls="community-tools-panel" @click="toolsOpen = !toolsOpen">
              <MoreHorizontal :size="19" aria-hidden="true" />
            </UiIconButton>
            <Transition name="ui-select-menu">
              <nav v-if="toolsOpen" id="community-tools-panel" class="community-tools-panel ui-menu-surface" :aria-label="t('community.communityActions')">
                <UiButton variant="ghost" :class="{ active: showCases }" :aria-pressed="showCases" @click="openCasesFromTools">
                  <template #start>
                    <ShieldCheck :size="17" :stroke-width="1.75" aria-hidden="true" />
                  </template>
                  {{ t('community.myCases') }}
                </UiButton>
                <UiButton as="RouterLink" variant="ghost" to="/discover" @click="toolsOpen = false">
                  <template #start>
                    <Eye :size="17" :stroke-width="1.75" aria-hidden="true" />
                  </template>
                  {{ t('actions.browseWorks') }}
                </UiButton>
              </nav>
            </Transition>
          </div>
        </section>
      </div>
      <p v-if="feedback" class="task-feedback" :class="[t('community.reportSubmitted'), t('community.appealSubmitted'), t('community.postPublished')].includes(feedback) ? 'success' : 'error'" role="status">
        {{ feedback }}
      </p>
      <section v-if="showCases" class="community-cases" :aria-label="t('community.myCases')">
        <header>
          <div><span class="status-label">{{ t('community.governanceLabel') }}</span><h2>{{ t('community.myCases') }}</h2></div><UiIconButton class="icon-button" type="button" :label="t('actions.close')" @click="showCases = false">
            <X :size="17" />
          </UiIconButton>
        </header>
        <div v-if="actionLoading === 'cases'" class="page-state">
          <LoaderCircle class="spin" :size="18" />{{ t('community.loadingCases') }}
        </div>
        <div v-else-if="reports.length" class="community-case-list">
          <article v-for="item in reports" :key="item.id">
            <div><strong>{{ item.resourceTitle }}</strong><span>{{ t(`community.reportCategories.${item.category}`) }} · {{ t(`community.reportStates.${item.status}`) }}</span><p>{{ item.details }}</p><small v-if="item.resolutionReason">{{ item.resolutionReason }}</small></div>
            <UiButton v-if="item.viewerCanAppeal" class="command-button secondary" type="button" variant="secondary" @click="Object.assign(appealForm, { reportId: item.id, reason: '' })">
              {{ t('community.appeal') }}
            </UiButton>
            <span v-else-if="item.appeal" :data-status="item.appeal.status">{{ t(`community.appealStates.${item.appeal.status}`) }}</span>
          </article>
        </div>
        <p v-else class="inline-empty">
          {{ t('community.noCases') }}
        </p>
        <UiButton v-if="reportNextCursor" class="command-button secondary community-cases-load-more" type="button" :disabled="reportLoadingMore" variant="secondary" @click="loadMoreCases">
          <LoaderCircle v-if="reportLoadingMore" class="spin" :size="16" />{{ t('actions.loadMore') }}
        </UiButton>
        <form v-if="appealForm.reportId" class="community-governance-form" @submit.prevent="submitAppeal">
          <label>{{ t('community.appealReason') }}<UiTextarea v-model="appealForm.reason" rows="3" minlength="10" maxlength="1000" required /></label>
          <div>
            <UiButton class="command-button primary" type="submit" :disabled="actionLoading.endsWith(':appeal')" variant="primary">
              {{ t('community.submitAppeal') }}
            </UiButton><UiButton class="command-button secondary" type="button" variant="secondary" @click="appealForm.reportId = ''">
              {{ t('actions.cancel') }}
            </UiButton>
          </div>
        </form>
      </section>
      <CategoryBrowser :items="categories" :model-value="category" :counts="categoryCounts" @update:model-value="selectCategory">
        <div v-if="loading" class="page-state" aria-live="polite">
          <LoaderCircle class="spin" :size="20" />{{ t('status.loadingCommunity') }}
        </div>
        <div v-else-if="error" class="page-state" role="alert">
          <p>{{ error }}</p><UiButton variant="secondary" @click="load">
            {{ t('actions.retry') }}
          </UiButton>
        </div>
        <div v-else-if="posts.length" class="community-topics">
          <div class="community-results-meta">
            <div><strong>{{ t('community.discussionCount', { count: displayedPosts.length }) }}</strong><span>{{ t(mineOnly ? 'community.myDiscussionsSummary' : 'community.discussionsSummary') }}</span></div>
            <UiButton v-if="hasActiveFilters" class="text-link" variant="ghost" size="sm" type="button" @click="clearFilters">
              {{ t('community.clearFilters') }}
            </UiButton>
          </div>
          <div v-if="displayedPosts.length" class="community-feed">
            <article v-for="post in displayedPosts" :key="post.id" class="community-post-row" :data-post-id="post.id">
              <div class="community-post-topic">
                <RouterLink class="community-post-thumbnail" :class="{ 'is-discussion': !post.mediaUrl }" :to="`/community/posts/${post.id}`" :aria-label="post.title">
                  <AssetMedia v-if="post.mediaUrl && post.mediaKind" :src="post.mediaUrl" :kind="post.mediaKind" :alt="post.title" :width="720" :height="540" :controls="false" />
                  <span v-else class="community-discussion-thumbnail"><MessageSquareText :size="28" :stroke-width="1.45" aria-hidden="true" /></span>
                </RouterLink>
                <RouterLink class="community-author-mark" :to="`/creators/${post.authorHandle}`" :aria-label="post.authorName">
                  {{ post.authorName.slice(0, 1) }}
                </RouterLink>
                <div class="community-post-copy">
                  <div class="community-post-title-row">
                    <RouterLink class="community-post-title-link" :to="`/community/posts/${post.id}`">
                      <h2>{{ post.title }}</h2>
                    </RouterLink>
                    <span :data-kind="normalizeMediaKind(post.mediaKind)">{{ categoryName(post.category) }} · {{ formatMediaKind(post.mediaKind) }}</span>
                  </div>
                  <p>{{ post.body }}</p>
                  <div class="community-post-meta">
                    <RouterLink :to="`/creators/${post.authorHandle}`">
                      <strong>{{ post.authorName }}</strong>
                      <small>@{{ post.authorHandle }}</small>
                    </RouterLink>
                    <time :datetime="post.publishedAt">{{ formatActivity(post.publishedAt) }}</time>
                  </div>
                </div>
              </div>
              <RouterLink class="community-post-stat" :to="`/community/posts/${post.id}`" :aria-label="`${post.commentCount} ${t('community.repliesColumn')}`">
                <MessageCircle :size="17" :stroke-width="1.75" aria-hidden="true" /><span>{{ post.commentCount }}</span>
              </RouterLink>
              <RouterLink class="community-post-stat" :to="`/community/posts/${post.id}`" :class="{ active: post.viewerLiked }" :aria-label="`${post.likeCount} ${t('community.likesColumn')}`">
                <Heart :size="17" :stroke-width="1.75" :fill="post.viewerLiked ? 'currentColor' : 'none'" aria-hidden="true" /><span>{{ post.likeCount }}</span>
              </RouterLink>
              <RouterLink class="community-post-time" :to="`/community/posts/${post.id}`" :aria-label="formatPublishedAt(post.publishedAt)">
                <time :datetime="post.publishedAt" :title="formatPublishedAt(post.publishedAt)">{{ formatActivity(post.publishedAt) }}</time>
              </RouterLink>
              <RouterLink class="community-post-more" :to="`/community/posts/${post.id}`" :aria-label="t('community.openDiscussion')">
                <MoreHorizontal :size="19" :stroke-width="1.75" aria-hidden="true" />
              </RouterLink>
            </article>
          </div>
          <section v-else class="community-filter-empty" :aria-label="t('community.noFilteredResults')">
            <Search :size="24" :stroke-width="1.5" aria-hidden="true" />
            <h2>{{ t('community.noFilteredResults') }}</h2>
            <p>{{ t('community.noFilteredResultsSummary') }}</p>
            <UiButton class="command-button secondary" type="button" variant="secondary" @click="clearFilters">
              {{ t('community.clearFilters') }}
            </UiButton>
          </section>
        </div>
        <UiButton v-if="postNextCursor" class="command-button secondary community-feed-load-more" type="button" :disabled="postLoadingMore" variant="secondary" @click="loadMorePosts">
          <LoaderCircle v-if="postLoadingMore" class="spin" :size="16" />{{ t('actions.loadMore') }}
        </UiButton>
        <section v-if="!posts.length" class="community-empty" :class="{ 'is-mine-empty': mineOnly }" :aria-label="mineOnly ? t('community.myPosts') : t('community.emptyTitle')">
          <div class="community-empty-main">
            <span class="community-empty-mark" aria-hidden="true"><MessageSquareText :size="21" :stroke-width="1.7" /></span>
            <div class="community-empty-copy">
              <span class="status-label">{{ mineOnly ? t('community.myPosts') : t('community.emptyTitle') }}</span>
              <h2>{{ mineOnly ? t('community.noMyPosts') : t('community.empty') }}</h2>
              <p>{{ mineOnly ? t('community.noMyPostsSummary') : t('community.emptySummary') }}</p>
            </div>
          </div>
          <nav class="community-empty-actions" :aria-label="t('community.emptyActionsLabel')">
            <UiButton v-if="session.user" class="command-button primary" variant="primary" @click="openCreatePost">
              <Plus :size="17" :stroke-width="1.75" />{{ t('community.publishPost') }}
            </UiButton>
            <UiButton v-if="mineOnly" class="command-button secondary" variant="secondary" @click="selectMyPosts">
              <LayoutGrid :size="17" :stroke-width="1.75" />{{ t('community.allPosts') }}
            </UiButton>
            <UiButton v-else as="RouterLink" class="command-button secondary" variant="secondary" to="/discover">
              <Sparkles :size="17" :stroke-width="1.75" />{{ t('actions.browseWorks') }}
            </UiButton>
          </nav>
        </section>
      </CategoryBrowser>
    </div>
  </section>
  <CommunityPostDrawer v-model:open="createOpen" @created="handlePostCreated" />
</template>

<style scoped>
.community-controls-row.controls-with-switcher { overflow: visible; }
.community-tools { position: relative; justify-self: end; }
.community-tools > .ui-icon-button { width: var(--community-toolbar-control-height); height: var(--community-toolbar-control-height); min-height: var(--community-toolbar-control-height); justify-content: center; padding: 0; border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--surface); color: var(--text-secondary); }
.community-tools > .ui-icon-button:hover:not(:disabled) { border-color: var(--border-strong); background: var(--surface); transform: none; }
.community-tools-panel { position: absolute; top: calc(100% + 8px); right: 0; width: max-content; min-width: 200px; max-width: calc(100vw - 48px); transform-origin: top right; }
.community-tools-panel .ui-button { justify-content: flex-start; gap: 10px; width: 100%; height: auto; min-height: 36px; padding: 8px 12px; border: 0; border-radius: 8px; color: var(--text); font-size: 13px; font-weight: 500; line-height: 20px; box-shadow: none; }
.community-tools-panel .ui-button:hover { background: var(--surface-muted); transform: none; }
.community-tools-panel .ui-button :deep(svg) { color: var(--text-secondary); }
@media (max-width: 1080px) {
  .community-controls-row .community-toolbar { grid-template-columns: minmax(0, 1fr) 150px 40px; }
}
@media (max-width: 560px) {
  .community-controls-row .community-toolbar { grid-template-columns: minmax(0, 1fr) 40px; }
  .community-search-control { grid-column: 1 / -1; }
}
</style>
