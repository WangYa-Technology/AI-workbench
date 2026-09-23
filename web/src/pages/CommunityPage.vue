<script setup lang="ts">
import { textWithin } from '../lib/unicodeText'
import UiEmptyState from '../components/ui/UiEmptyState.vue'
import UiActionBanner from '../components/ui/UiActionBanner.vue'
import UiAvatar from '../components/ui/UiAvatar.vue'
import UiCardContent from '../components/ui/UiCardContent.vue'
import UiCardTag from '../components/ui/UiCardTag.vue'
import UiFilterSearch from '../components/ui/UiFilterSearch.vue'
import UiFilterBar from '../components/ui/UiFilterBar.vue'
import UiLayoutSwitcher from '../components/ui/UiLayoutSwitcher.vue'
import UiCatalog from '../components/ui/UiCatalog.vue'
import UiContentCard from '../components/ui/UiContentCard.vue'
import { Clock3, Eye, Heart, LoaderCircle, LogIn, MessageCircle, MessageSquareText, MoreHorizontal, Plus, Search, ShieldCheck, UserPlus, X } from 'lucide-vue-next'
import { computed, nextTick, onMounted, onBeforeUnmount, reactive, ref, watch } from 'vue'
import UiDropdownMenu from '../components/ui/UiDropdownMenu.vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api, APIError, messageFrom, type CommunityPost, type CommunityReport } from '../api/client'
import CategoryBrowser from '../components/domain/CategoryBrowser.vue'
import type { TaskType } from '../api/client'
import AssetMedia from '../components/domain/AssetMedia.vue'
import CommunityPostDrawer from '../components/domain/CommunityPostDrawer.vue'
import { useSessionStore } from '../stores/session'
import PageHeader from '../components/ui/PageHeader.vue'
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
const layout = ref<'list' | 'grid'>('list')
const posts = ref<CommunityPost[]>([])
const total = ref(0)
const capabilities = ref<Awaited<ReturnType<typeof api.communityCapabilities>> | null>(null)
const activeView = computed(() => ['mine','saved','following','drafts'].includes(String(route.query.view)) ? String(route.query.view) as 'mine' | 'saved' | 'following' | 'drafts' : 'all')
const viewTabs = computed(() => (session.user ? ['all','mine','saved','following','drafts'] : ['all']).map(value => ({ value, label: t(`community.views.${value}`) })))
const viewBar = ref<globalThis.HTMLElement | null>(null)
let viewObserver: globalThis.ResizeObserver | undefined
function revealSelectedView() {
  const bar = viewBar.value
  const selected = bar?.querySelector<globalThis.HTMLElement>('[aria-selected="true"]')
  if (!bar || !selected) return
  const bounds = bar.getBoundingClientRect(), tab = selected.getBoundingClientRect()
  if (tab.left < bounds.left) bar.scrollLeft += tab.left - bounds.left
  else if (tab.right > bounds.right) bar.scrollLeft += tab.right - bounds.right
}
watch(activeView, () => { void nextTick(revealSelectedView) })
onBeforeUnmount(() => viewObserver?.disconnect())
function selectView(value: string) { void router.push({ path: '/community', query: { ...route.query, view: value === 'all' ? undefined : value } }) }
const editPostId = ref('')
function openEdit(post: CommunityPost) { editPostId.value = post.id; createOpen.value = true }
async function loadFocusedCase() {
  if (!route.params.reportId || !await requireAccount()) return
  showCases.value = true
  reports.value = []; reportNextCursor.value = null
  try { reports.value = [await api.getCommunityReport(String(route.params.reportId))]; reportNextCursor.value = null }
  catch (reason) { feedback.value = messageFrom(reason) }
}
watch(() => route.params.reportId, () => { void loadFocusedCase() })
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
function openRouteEditor() { if (route.query.edit && session.user) { editPostId.value = String(route.query.edit); createOpen.value = true } }
watch(() => route.query.edit, openRouteEditor)
watch(createOpen, open => { if (!open && route.query.edit) void router.replace({ query: { ...route.query, edit: undefined } }) })
const showCases = ref(false)
const appealForm = reactive({ reportId: '', reason: '' })

function formatPublishedAt(value: string) {
  return new Intl.DateTimeFormat(locale.value, { year: 'numeric', month: 'short', day: 'numeric' }).format(new Date(value))
}

function authorInitials(name: string) {
  const parts = name.trim().split(/\s+/).filter(Boolean)
  if (!parts.length) return '?'
  if (parts.length === 1) return Array.from(parts[0]).slice(0, 2).join('').toUpperCase()
  return `${Array.from(parts[0])[0]}${Array.from(parts.at(-1) || '')[0] || ''}`.toUpperCase()
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

function clearFilters() {
  globalThis.clearTimeout(searchTimer)
  search.value = ''
  void router.replace({ query: { ...route.query, q: undefined, category: undefined } })
}

let loadVersion = 0
async function load() {
  if (activeView.value !== 'all' && !await requireAccount()) return
  const version = ++loadVersion
  loading.value = true
  postLoadingMore.value = false
  error.value = ''
  try {
    const [directory, page, ability] = await Promise.all([api.listTaskTypes('community'), api.listCommunityPosts({ sort: sortMode.value, q: String(route.query.q || ''), category: category.value, limit: 20, view: activeView.value }), api.communityCapabilities()])
    if (version !== loadVersion) return
    capabilities.value = ability
    total.value = page.total
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
    const page = await api.listCommunityPosts({ sort: sortMode.value, q: String(route.query.q || ''), category: category.value, limit: 20, cursor: postNextCursor.value, view: activeView.value })
    if (version !== loadVersion) return
    const known = new Set(posts.value.map(item => item.id))
    posts.value = [...posts.value, ...page.items.filter(item => !known.has(item.id))]
    postNextCursor.value = page.nextCursor || null
  } catch (reason) {
    if (version === loadVersion) {
      if (reason instanceof APIError && reason.status === 422) await load()
      else feedback.value = messageFrom(reason)
    }
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

async function openCreatePost() {
  if (!await requireAccount()) return
  editPostId.value = ''
  createOpen.value = true
}

async function handlePostCreated(post: CommunityPost) {
  feedback.value = t(post.status === 'draft' ? 'community.draftSaved' : 'community.postPublished')
  if (post.status === 'draft' && activeView.value !== 'drafts') await router.push({ path: '/community', query: { view: 'drafts' } })
  else if (post.status === 'published' && activeView.value === 'drafts') await router.push({ path: '/community', query: { view: 'mine' } })
  else await load()
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
  if (!textWithin(appealForm.reason, 10, 1000)) { feedback.value = t('community.textLengthError'); return }
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
  viewObserver = new globalThis.ResizeObserver(revealSelectedView)
  if (viewBar.value) viewObserver.observe(viewBar.value)
  await session.ensure()
  mineOnly.value = route.query.view === 'mine' && Boolean(session.user)
  await load()
  await loadFocusedCase()
  openRouteEditor()
  await nextTick(revealSelectedView)
})
</script>

<template>
  <section class="community-page content-width">
    <PageHeader
      :title="t('community.title')"
      :summary="t('community.summary')"
      artwork-src="/illustrations/headers/community.webp"
    >
      <template #actions>
        <UiButton v-if="capabilities?.canSaveDraft" class="command-button primary" variant="primary" @click="openCreatePost">
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
    </PageHeader>
    <div :class="{ 'has-category-sidebar': categories.length > 0 }">
      <UiFilterBar as="div" split class="ui-filter-bar controls-with-switcher community-controls-row has-switcher">
        <div ref="viewBar" class="view-switcher-bar">
          <UiTabs class="view-switcher" :model-value="activeView" :items="viewTabs" :label="t('community.browseView')" @update:model-value="selectView(String($event))" />
        </div>
        <section class="ui-filter-bar__controls community-toolbar" :aria-label="t('community.filtersLabel')">
          <UiFilterSearch class="community-search-control" :label="t('community.searchLabel')">
            <UiInput v-model="search" type="search" maxlength="120" :placeholder="t('community.searchPlaceholder')" />
          </UiFilterSearch>
          <UiSelect :model-value="sortMode" :aria-label="t('community.sortLabel')" @update:model-value="selectSort(String($event))">
            <option v-for="item in sortTabs" :key="item.value" :value="item.value">
              {{ item.label }}
            </option>
          </UiSelect>
          <UiSelect :model-value="category" class="community-type-control category-filter" :aria-label="t('community.typeLabel')" @update:model-value="selectCategory(String($event))">
            <option value="">
              {{ t('community.allTypes') }}
            </option>
            <option v-for="item in categories" :key="item.code" :value="item.code">
              {{ categoryName(item.code) }}
            </option>
          </UiSelect>
          <UiDropdownMenu class="community-tools" trigger-class="ui-select__trigger" transition-name="ui-select-menu" :label="t('community.communityActions')">
            <template #trigger>
              <MoreHorizontal :size="19" aria-hidden="true" />
            </template>
            <UiButton role="menuitem" variant="ghost" :class="{ active: showCases }" @click="toggleCases">
              <template #start>
                <ShieldCheck :size="17" :stroke-width="1.75" aria-hidden="true" />
              </template>
              {{ t('community.myCases') }}
            </UiButton>
            <UiButton as="RouterLink" role="menuitem" variant="ghost" to="/discover">
              <template #start>
                <Eye :size="17" :stroke-width="1.75" aria-hidden="true" />
              </template>
              {{ t('actions.browseWorks') }}
            </UiButton>
          </UiDropdownMenu>
        </section>
      </UiFilterBar>
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
            <div>
              <strong>{{ item.resourceTitle }}</strong><span>{{ t(`community.reportCategories.${item.category}`) }} · {{ t(`community.reportStates.${item.status}`) }}</span><p v-if="item.details">
                {{ item.details }}
              </p><small v-if="item.resolutionReason">{{ item.resolutionReason }}</small>
            </div>
            <UiButton v-if="item.subjectAuthorId === session.user?.id && item.resourceType === 'post'" as="RouterLink" variant="ghost" :to="{ path: '/community', query: { edit: item.resourceId } }">
              {{ t('community.editPost') }}
            </UiButton>
            <UiButton v-if="item.viewerCanAppeal" class="command-button secondary" type="button" variant="secondary" @click="Object.assign(appealForm, { reportId: item.id, reason: '' })">
              {{ t('community.appeal') }}
            </UiButton>
            <span v-else-if="item.appeal" :data-status="item.appeal.status">{{ t(`community.appealStates.${item.appeal.status}`) }}</span>
          </article>
        </div>
        <UiEmptyState v-else density="compact" :title="t('community.noCases')" />
        <UiButton v-if="reportNextCursor" class="command-button secondary community-cases-load-more" type="button" :disabled="reportLoadingMore" variant="secondary" @click="loadMoreCases">
          <LoaderCircle v-if="reportLoadingMore" class="spin" :size="16" />{{ t('actions.loadMore') }}
        </UiButton>
        <form v-if="appealForm.reportId" class="community-governance-form" @submit.prevent="submitAppeal">
          <label>{{ t('community.appealReason') }}<UiTextarea v-model="appealForm.reason" rows="3" required /></label>
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
            <div><strong>{{ t('community.discussionCount', { count: total }) }}</strong><span>{{ t(mineOnly ? 'community.myDiscussionsSummary' : 'community.discussionsSummary') }}</span></div>
            <div class="task-results-actions">
              <UiButton v-if="hasActiveFilters" class="text-link" variant="ghost" size="sm" type="button" @click="clearFilters">
                {{ t('community.clearFilters') }}
              </UiButton>
              <UiLayoutSwitcher v-model="layout" :label="t('community.layout')" :list-label="t('community.listView')" :grid-label="t('community.gridView')" />
            </div>
          </div>
          <UiCatalog v-if="displayedPosts.length" class="ui-catalog community-feed" :grid="layout === 'grid'">
            <UiContentCard v-for="post in displayedPosts" :key="post.id" class="ui-content-card community-post-row" :data-post-id="post.id">
              <div class="community-post-topic">
                <RouterLink class="ui-content-card__media community-post-thumbnail" :class="{ 'is-discussion': !post.mediaUrl }" :to="post.status === 'draft' ? `/community?view=drafts&edit=${post.id}` : `/community/posts/${post.id}`" :aria-label="post.title">
                  <AssetMedia v-if="post.mediaUrl && post.mediaKind" :src="post.mediaUrl" :kind="post.mediaKind" :alt="post.title" :width="720" :height="540" :controls="false" />
                  <span v-else class="community-discussion-thumbnail"><MessageSquareText :size="28" :stroke-width="1.45" aria-hidden="true" /></span>
                </RouterLink>
                <UiCardContent :title="post.title || t('community.untitledDraft')" :summary="post.body" :to="post.status === 'draft' ? `/community?view=drafts&edit=${post.id}` : `/community/posts/${post.id}`">
                  <template #tags>
                    <UiCardTag>{{ categoryName(post.category) }}</UiCardTag><UiCardTag variant="neutral">
                      {{ formatMediaKind(post.mediaKind) }}
                    </UiCardTag>
                  </template>
                  <template #meta>
                    <RouterLink class="community-author-link" :to="`/creators/${post.authorHandle}`">
                      <UiAvatar class="community-author-avatar" :initials="authorInitials(post.authorName)" aria-hidden="true" />
                      <span>{{ post.authorName }} <small>@{{ post.authorHandle }}</small></span>
                    </RouterLink><time :datetime="post.publishedAt">{{ formatActivity(post.publishedAt) }}</time>
                  </template>
                </UiCardContent>
                <UiButton v-if="post.status === 'draft'" variant="secondary" size="sm" @click="openEdit(post)">
                  {{ t('community.editPost') }}
                </UiButton>
              </div>
              <RouterLink class="community-post-stat" :to="post.status === 'draft' ? `/community?view=drafts&edit=${post.id}` : `/community/posts/${post.id}`" :aria-label="`${post.commentCount} ${t('community.repliesColumn')}`">
                <MessageCircle :size="17" :stroke-width="1.75" aria-hidden="true" /><span>{{ post.commentCount }}</span>
              </RouterLink>
              <RouterLink class="community-post-stat" :to="post.status === 'draft' ? `/community?view=drafts&edit=${post.id}` : `/community/posts/${post.id}`" :class="{ active: post.viewerLiked }" :aria-label="`${post.likeCount} ${t('community.likesColumn')}`">
                <Heart :size="17" :stroke-width="1.75" :fill="post.viewerLiked ? 'currentColor' : 'none'" aria-hidden="true" /><span>{{ post.likeCount }}</span>
              </RouterLink>
              <RouterLink class="community-post-time" :to="post.status === 'draft' ? `/community?view=drafts&edit=${post.id}` : `/community/posts/${post.id}`" :aria-label="formatPublishedAt(post.publishedAt)">
                <time :datetime="post.publishedAt" :title="formatPublishedAt(post.publishedAt)">{{ formatActivity(post.publishedAt) }}</time>
              </RouterLink>
              <RouterLink class="community-post-more" :to="post.status === 'draft' ? `/community?view=drafts&edit=${post.id}` : `/community/posts/${post.id}`" :aria-label="t('community.openDiscussion')">
                <MoreHorizontal :size="19" :stroke-width="1.75" aria-hidden="true" />
              </RouterLink>
            </UiContentCard>
          </UiCatalog>
        </div>
        <UiButton v-if="postNextCursor" class="command-button secondary community-feed-load-more" type="button" :disabled="postLoadingMore" variant="secondary" @click="loadMorePosts">
          <LoaderCircle v-if="postLoadingMore" class="spin" :size="16" />{{ t('actions.loadMore') }}
        </UiButton>
        <UiEmptyState
          v-if="!loading && !error && !posts.length" class="community-empty"
          :title="hasActiveFilters ? t('community.noFilteredResults') : t(activeView === 'all' ? 'community.empty' : activeView === 'mine' ? 'community.noMyPosts' : `community.emptyViews.${activeView}.title`)"
          :message="hasActiveFilters ? t('community.filteredEmptySummary') : t(activeView === 'all' ? 'community.emptySummary' : activeView === 'mine' ? 'community.noMyPostsSummary' : `community.emptyViews.${activeView}.summary`)"
        >
          <template #icon>
            <component :is="hasActiveFilters ? Search : MessageSquareText" :size="24" :stroke-width="1.75" />
          </template>
          <template #actions>
            <UiButton v-if="hasActiveFilters" variant="secondary" @click="clearFilters">
              {{ t('community.clearFilters') }}
            </UiButton>
            <UiButton v-else-if="activeView !== 'all'" variant="secondary" @click="selectView('all')">
              {{ t('community.allPosts') }}
            </UiButton>
            <UiButton v-else as="RouterLink" variant="secondary" to="/discover">
              {{ t('actions.browseWorks') }}
            </UiButton>
          </template>
        </UiEmptyState>
        <UiActionBanner :title="t('community.shareIdeaTitle')" :summary="t('community.shareIdeaSummary')">
          <template #icon>
            <MessageSquareText :size="24" :stroke-width="1.75" />
          </template>
          <template #actions>
            <UiButton v-if="capabilities?.canSaveDraft" variant="primary" @click="openCreatePost">
              <template #start>
                <Plus :size="17" :stroke-width="1.75" />
              </template>
              {{ t('community.publishPost') }}
            </UiButton>
            <UiButton v-else as="RouterLink" variant="primary" :to="{ path: '/auth', query: { auth: 'login', returnTo: route.fullPath } }">
              <template #start>
                <LogIn :size="17" :stroke-width="1.75" />
              </template>
              {{ t('community.signInToShare') }}
            </UiButton>
          </template>
        </UiActionBanner>
      </CategoryBrowser>
    </div>
  </section>
  <CommunityPostDrawer v-model:open="createOpen" :post-id="editPostId" @created="handlePostCreated" @deleted="load" />
</template>

<style scoped>

.community-feed.is-grid .community-post-row { grid-template-columns: auto auto minmax(0, 1fr) auto; grid-template-rows: 1fr auto; align-items: start; column-gap: 12px; }
.community-feed.is-grid .community-post-topic { grid-column: 1 / -1; display: flex; flex-direction: column; align-items: stretch; gap: 12px; padding-right: 0; }
.community-feed.is-grid .community-post-thumbnail { width: 100%; }
.community-feed.is-grid .community-post-stat, .community-feed.is-grid .community-post-time, .community-feed.is-grid .community-post-more { position: static; margin: 0; align-self: end; justify-content: center; }
.community-feed.is-grid .community-post-time { justify-content: flex-end; }

.community-controls-row.controls-with-switcher { overflow: visible; grid-template-columns: minmax(0, 1fr); }
.community-controls-row .view-switcher-bar { min-width: 0; max-width: 100%; overflow-x: auto; }
.community-controls-row :deep(.ui-tabs) { flex-shrink: 0; width: max-content; }
.community-controls-row :deep(.ui-tabs__tab) { flex: 0 0 auto; }
.community-tools { position: relative; justify-self: end; }
.community-tools :deep(> .ui-button) { width: var(--community-toolbar-control-height); height: var(--community-toolbar-control-height); min-height: var(--community-toolbar-control-height); justify-content: center; padding: 0; border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--surface); color: var(--text-secondary); }
.community-tools :deep(> .ui-button):hover:not(:disabled) { border-color: var(--border-strong); background: var(--surface); transform: none; }
.community-tools :deep(.ui-dropdown-menu__content) { left: auto; position: absolute; top: calc(100% + 8px); right: 0; width: max-content; min-width: 200px; max-width: calc(100vw - 48px); transform-origin: top right; }
.community-tools :deep(.ui-dropdown-menu__content .ui-button) { justify-content: flex-start; gap: 10px; width: 100%; height: auto; min-height: 36px; padding: 8px 12px; border: 0; border-radius: 8px; color: var(--text); font-size: 13px; font-weight: 500; line-height: 20px; box-shadow: none; }
.community-tools :deep(.ui-dropdown-menu__content .ui-button):hover { background: var(--surface-muted); transform: none; }
.community-tools :deep(.ui-dropdown-menu__content .ui-button svg) { color: var(--text-secondary); }
@media (max-width: 1080px) {
  .community-controls-row .community-toolbar { grid-template-columns: minmax(0, 1fr) 150px 40px; }
}
@media (max-width: 560px) {
  .community-controls-row .community-toolbar { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) 40px; }
  .community-search-control { grid-column: 1 / -1; }
}
</style>
