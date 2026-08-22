<script setup lang="ts">
import { Clock3, Eye, Heart, LayoutGrid, LoaderCircle, MessageCircle, MoreHorizontal, RefreshCw, Search, ShieldCheck, Sparkles, Upload, UserRound, X } from 'lucide-vue-next'
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRouter } from 'vue-router'
import { api, messageFrom, type CommunityPost, type CommunityReport } from '../api/client'
import AssetMedia from '../components/domain/AssetMedia.vue'
import { useSessionStore } from '../stores/session'
import UiButton from '../components/ui/UiButton.vue'
import UiIconButton from '../components/ui/UiIconButton.vue'
import UiInput from '../components/ui/UiInput.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiTabs from '../components/ui/UiTabs.vue'
import UiTextarea from '../components/ui/UiTextarea.vue'

const { locale, t } = useI18n()
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
const sortMode = ref<'latest' | 'discussed'>('latest')
const sortTabs = computed(() => [
  { value: 'latest', label: t('community.latestDiscussions'), icon: Clock3 },
  { value: 'discussed', label: t('community.mostDiscussed'), icon: MessageCircle },
])
const search = ref('')
const mediaFilter = ref<'all' | 'image' | 'video' | 'music' | 'other'>('all')
const showCases = ref(false)
const appealForm = reactive({ reportId: '', reason: '' })

function formatPublishedAt(value: string) {
  return new Intl.DateTimeFormat(locale.value, { year: 'numeric', month: 'short', day: 'numeric' }).format(new Date(value))
}

function formatMediaKind(value: string) {
  const normalized = normalizeMediaKind(value)
  if (normalized === 'video') return t('create.modes.video')
  if (normalized === 'music') return t('create.modes.music')
  if (normalized === 'image') return t('create.modes.image')
  return t('community.workTopic')
}

function normalizeMediaKind(value: string): 'image' | 'video' | 'music' | 'other' {
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

const displayedPosts = computed(() => {
  const query = search.value.trim().toLocaleLowerCase(locale.value)
  const items = posts.value.filter((post) => {
    const matchesType = mediaFilter.value === 'all' || normalizeMediaKind(post.mediaKind) === mediaFilter.value
    const matchesSearch = !query || [post.workTitle, post.body, post.authorName, post.authorHandle]
      .some(value => value.toLocaleLowerCase(locale.value).includes(query))
    return matchesType && matchesSearch
  })
  if (sortMode.value === 'discussed') {
    return [...items].sort((left, right) => right.commentCount - left.commentCount || right.likeCount - left.likeCount || right.publishedAt.localeCompare(left.publishedAt))
  }
  return [...items].sort((left, right) => right.publishedAt.localeCompare(left.publishedAt))
})

const hasActiveFilters = computed(() => Boolean(search.value.trim()) || mediaFilter.value !== 'all')
const totalReplyCount = computed(() => posts.value.reduce((total, post) => total + post.commentCount, 0))
const totalLikeCount = computed(() => posts.value.reduce((total, post) => total + post.likeCount, 0))

function clearFilters() {
  search.value = ''
  mediaFilter.value = 'all'
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const page = await api.listCommunityPosts({ limit: 20 })
    posts.value = page.items
    postNextCursor.value = page.nextCursor || null
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    loading.value = false
  }
}

async function loadMorePosts() {
  if (!postNextCursor.value || postLoadingMore.value) return
  postLoadingMore.value = true
  feedback.value = ''
  try {
    const page = await api.listCommunityPosts({ limit: 20, cursor: postNextCursor.value })
    const known = new Set(posts.value.map(item => item.id))
    posts.value = [...posts.value, ...page.items.filter(item => !known.has(item.id))]
    postNextCursor.value = page.nextCursor || null
  } catch (reason) {
    feedback.value = messageFrom(reason)
  } finally {
    postLoadingMore.value = false
  }
}

async function requireAccount() {
  const user = await session.ensure()
  if (user) return true
  await router.push('/settings')
  return false
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
  await load()
})
</script>

<template>
  <section class="community-page content-width">
    <header class="page-hero-header page-hero-banner community-header">
      <div class="page-hero-copy">
        <span class="page-hero-eyebrow"><Sparkles :size="14" :stroke-width="1.75" aria-hidden="true" />{{ t('community.networkLabel') }}</span>
        <h1>{{ t('community.title') }}</h1>
        <p>{{ t('community.summary') }}</p>
        <div class="page-hero-stats" :aria-label="t('community.statsLabel')">
          <article><span class="page-hero-stat-icon" data-tone="blue"><LayoutGrid :size="23" :stroke-width="1.75" aria-hidden="true" /></span><div><strong>{{ posts.length }}</strong><span>{{ t('community.discussionsLabel') }}</span></div></article>
          <article><span class="page-hero-stat-icon" data-tone="violet"><MessageCircle :size="23" :stroke-width="1.75" aria-hidden="true" /></span><div><strong>{{ totalReplyCount }}</strong><span>{{ t('community.repliesColumn') }}</span></div></article>
          <article><span class="page-hero-stat-icon" data-tone="green"><Heart :size="23" :stroke-width="1.75" aria-hidden="true" /></span><div><strong>{{ totalLikeCount }}</strong><span>{{ t('community.likesColumn') }}</span></div></article>
        </div>
      </div>
      <div class="page-hero-actions">
        <UiButton as="RouterLink" class="command-button primary" variant="primary" to="/publish">
          <template #start>
            <Upload :size="17" :stroke-width="1.75" aria-hidden="true" />
          </template>
          {{ t('actions.publishWork') }}
        </UiButton>
      </div>
      <img class="page-hero-art community-hero-art" src="/community/community-hero.webp" alt="" width="768" height="714" aria-hidden="true" />
      <div v-if="session.user" class="page-hero-account">
        <span class="page-hero-avatar"><UserRound :size="22" :stroke-width="1.75" aria-hidden="true" /></span><span><strong>{{ session.user.displayName }}</strong><small>@{{ session.user.handle }}</small></span><ShieldCheck :size="15" :stroke-width="1.75" aria-hidden="true" />
      </div>
    </header>
    <div v-if="loading" class="community-skeleton" aria-live="polite">
      <span class="sr-only">{{ t('status.loadingCommunity') }}</span>
      <article v-for="index in 5" :key="index">
        <span class="community-skeleton-media"></span>
        <span class="community-skeleton-line short"></span>
        <span class="community-skeleton-line"></span>
        <span class="community-skeleton-line medium"></span>
      </article>
    </div>
    <div v-else-if="error" class="page-state" role="alert">
      <p>{{ error }}</p><UiButton class="command-button secondary" variant="secondary" @click="load">
        <template #start>
          <RefreshCw :size="17" :stroke-width="1.75" />
        </template>{{ t('actions.retry') }}
      </UiButton>
    </div>
    <div v-else>
      <div class="view-switcher-bar">
        <UiTabs class="view-switcher" :model-value="sortMode" :items="sortTabs" :label="t('community.sortLabel')" @update:model-value="sortMode = $event as 'latest' | 'discussed'" />
      </div>
      <section class="community-toolbar" :aria-label="t('community.filtersLabel')">
        <label class="community-search-control">
          <span class="sr-only">{{ t('community.searchLabel') }}</span>
          <Search :size="18" :stroke-width="1.75" aria-hidden="true" />
          <UiInput v-model="search" type="search" maxlength="120" :placeholder="t('community.searchPlaceholder')" />
        </label>
        <UiSelect v-model="mediaFilter" class="community-type-control" :aria-label="t('community.typeLabel')">
          <template #start>
            <LayoutGrid :size="17" :stroke-width="1.75" aria-hidden="true" />
          </template>
          <option value="all">
            {{ t('community.allTypes') }}
          </option>
          <option value="image">
            {{ t('create.modes.image') }}
          </option>
          <option value="video">
            {{ t('create.modes.video') }}
          </option>
          <option value="music">
            {{ t('create.modes.music') }}
          </option>
          <option value="other">
            {{ t('community.workTopic') }}
          </option>
        </UiSelect>
        <nav class="community-toolbar-actions" :aria-label="t('community.communityActions')">
          <UiButton variant="ghost" :class="{ active: showCases }" :aria-pressed="showCases" @click="toggleCases">
            <template #start>
              <ShieldCheck :size="17" :stroke-width="1.75" aria-hidden="true" />
            </template>
            {{ t('community.myCases') }}
          </UiButton>
          <UiButton as="RouterLink" variant="ghost" to="/discover">
            <template #start>
              <Eye :size="17" :stroke-width="1.75" aria-hidden="true" />
            </template>
            {{ t('actions.browseWorks') }}
          </UiButton>
        </nav>
      </section>
      <p v-if="feedback" class="task-feedback" :class="feedback === t('community.reportSubmitted') || feedback === t('community.appealSubmitted') ? 'success' : 'error'" role="status">
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
      <div v-if="posts.length" class="community-topics">
        <div class="community-results-meta">
          <span>{{ t('community.resultCount', { count: displayedPosts.length }) }}</span>
          <UiButton v-if="hasActiveFilters" variant="ghost" type="button" @click="clearFilters">
            {{ t('community.clearFilters') }}
          </UiButton>
        </div>
        <div class="community-list-head" aria-hidden="true">
          <span>{{ t('community.topicColumn') }}</span>
          <span>{{ t('community.repliesColumn') }}</span>
          <span>{{ t('community.likesColumn') }}</span>
          <span>{{ t('community.updatedColumn') }}</span>
          <span></span>
        </div>
        <div v-if="displayedPosts.length" class="community-feed">
          <article v-for="post in displayedPosts" :key="post.id" class="community-post-row" :data-post-id="post.id">
            <div class="community-post-topic">
              <RouterLink class="community-post-thumbnail" :to="`/community/posts/${post.id}`" :aria-label="post.workTitle">
                <AssetMedia :src="post.mediaUrl" :kind="post.mediaKind" :alt="post.workTitle" :width="720" :height="540" :controls="false" />
              </RouterLink>
              <RouterLink class="community-author-mark" :to="`/creators/${post.authorHandle}`" :aria-label="post.authorName">
                {{ post.authorName.slice(0, 1) }}
              </RouterLink>
              <div class="community-post-copy">
                <div class="community-post-title-row">
                  <RouterLink class="community-post-title-link" :to="`/community/posts/${post.id}`">
                    <h2>{{ post.workTitle }}</h2>
                  </RouterLink>
                  <span :data-kind="normalizeMediaKind(post.mediaKind)">{{ formatMediaKind(post.mediaKind) }}</span>
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
      <section v-if="!posts.length" class="community-empty" :aria-label="t('community.emptyTitle')">
        <div>
          <span class="status-label">{{ t('community.emptyTitle') }}</span>
          <h2>{{ t('community.empty') }}</h2>
          <p>{{ t('community.emptySummary') }}</p>
        </div>
        <nav class="community-empty-actions" :aria-label="t('community.emptyActionsLabel')">
          <UiButton as="RouterLink" class="command-button primary" variant="primary" to="/publish">
            <Upload :size="17" :stroke-width="1.75" />{{ t('actions.publishWork') }}
          </UiButton>
          <UiButton as="RouterLink" class="command-button secondary" variant="secondary" to="/discover">
            <Sparkles :size="17" :stroke-width="1.75" />{{ t('actions.browseWorks') }}
          </UiButton>
        </nav>
      </section>
    </div>
  </section>
</template>
