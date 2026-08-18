<script setup lang="ts">
import { ArrowRight, Bookmark, Flag, Heart, LoaderCircle, MessageCircle, RefreshCw, Sparkles, Upload, UserPlus, X } from 'lucide-vue-next'
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRouter } from 'vue-router'
import { api, messageFrom, type CommunityComment, type CommunityPost, type CommunityReport } from '../api/client'
import AssetMedia from '../components/domain/AssetMedia.vue'
import { useSessionStore } from '../stores/session'

const { t } = useI18n()
const router = useRouter()
const session = useSessionStore()
const posts = ref<CommunityPost[]>([])
const postNextCursor = ref<string | null>(null)
const postLoadingMore = ref(false)
const comments = ref<Record<string, CommunityComment[]>>({})
const commentNextCursors = ref<Record<string, string | null>>({})
const commentLoadingMore = ref<Record<string, boolean>>({})
const reports = ref<CommunityReport[]>([])
const reportNextCursor = ref<string | null>(null)
const reportLoadingMore = ref(false)
const commentDrafts = ref<Record<string, string>>({})
const loading = ref(true)
const actionLoading = ref('')
const error = ref('')
const feedback = ref('')
const expandedPost = ref('')
const showCases = ref(false)
const reportForm = reactive({ postId: '', category: 'misleading' as CommunityReport['category'], details: '' })
const appealForm = reactive({ reportId: '', reason: '' })

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

function updatePost(postId: string, update: Partial<CommunityPost>) {
  posts.value = posts.value.map(post => post.id === postId ? { ...post, ...update } : post)
}

function mergeComments(postId: string, incoming: CommunityComment[]) {
  const merged = new Map((comments.value[postId] || []).map(item => [item.id, item]))
  incoming.forEach(item => merged.set(item.id, item))
  comments.value = {
    ...comments.value,
    [postId]: [...merged.values()].sort((left, right) => left.createdAt.localeCompare(right.createdAt) || left.id.localeCompare(right.id)),
  }
}

async function react(post: CommunityPost, kind: 'like' | 'bookmark') {
  if (!await requireAccount()) return
  actionLoading.value = `${post.id}:${kind}`
  feedback.value = ''
  try {
    const active = kind === 'like' ? !post.viewerLiked : !post.viewerBookmarked
    const state = await api.setCommunityReaction(post.id, kind, active)
    updatePost(post.id, state)
  } catch (reason) {
    feedback.value = messageFrom(reason)
  } finally {
    actionLoading.value = ''
  }
}

async function follow(post: CommunityPost) {
  if (!await requireAccount()) return
  actionLoading.value = `${post.id}:follow`
  feedback.value = ''
  try {
    const state = await api.setCommunityFollow(post.authorId, !post.viewerFollowing)
    posts.value = posts.value.map(item => item.authorId === state.authorId ? { ...item, viewerFollowing: state.following } : item)
  } catch (reason) {
    feedback.value = messageFrom(reason)
  } finally {
    actionLoading.value = ''
  }
}

async function toggleComments(post: CommunityPost) {
  if (expandedPost.value === post.id) {
    expandedPost.value = ''
    return
  }
  expandedPost.value = post.id
  if (comments.value[post.id]) return
  actionLoading.value = `${post.id}:comments`
  try {
	const page = await api.listCommunityComments(post.id, { limit: 20 })
	comments.value = { ...comments.value, [post.id]: page.items }
	commentNextCursors.value = { ...commentNextCursors.value, [post.id]: page.nextCursor || null }
  } catch (reason) {
    feedback.value = messageFrom(reason)
  } finally {
    actionLoading.value = ''
  }
}

async function loadMoreComments(postId: string) {
  const cursor = commentNextCursors.value[postId]
  if (!cursor || commentLoadingMore.value[postId]) return
  commentLoadingMore.value = { ...commentLoadingMore.value, [postId]: true }
  feedback.value = ''
  try {
    const page = await api.listCommunityComments(postId, { limit: 20, cursor })
    mergeComments(postId, page.items)
    commentNextCursors.value = { ...commentNextCursors.value, [postId]: page.nextCursor || null }
  } catch (reason) {
    feedback.value = messageFrom(reason)
  } finally {
    commentLoadingMore.value = { ...commentLoadingMore.value, [postId]: false }
  }
}

async function addComment(post: CommunityPost) {
  if (!await requireAccount()) return
  const body = (commentDrafts.value[post.id] || '').trim()
  if (!body) return
  actionLoading.value = `${post.id}:comment`
  feedback.value = ''
  try {
    const item = await api.createCommunityComment(post.id, body)
	mergeComments(post.id, [item])
    updatePost(post.id, { commentCount: post.commentCount + 1 })
    commentDrafts.value = { ...commentDrafts.value, [post.id]: '' }
  } catch (reason) {
    feedback.value = messageFrom(reason)
  } finally {
    actionLoading.value = ''
  }
}

async function openReport(post: CommunityPost) {
  if (!await requireAccount()) return
  Object.assign(reportForm, { postId: post.id, category: 'misleading', details: '' })
}

async function submitReport() {
  actionLoading.value = `${reportForm.postId}:report`
  feedback.value = ''
  try {
    const item = await api.reportCommunityPost(reportForm.postId, { category: reportForm.category, details: reportForm.details })
    reports.value = [item, ...reports.value]
    reportForm.postId = ''
    feedback.value = t('community.reportSubmitted')
  } catch (reason) {
    feedback.value = messageFrom(reason)
  } finally {
    actionLoading.value = ''
  }
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
    <header class="community-header">
      <div>
        <span class="status-label">{{ t('community.networkLabel') }}</span>
        <h1>{{ t('community.title') }}</h1>
        <p>{{ t('community.summary') }}</p>
      </div>
      <RouterLink class="command-button primary" to="/publish">
        <Upload :size="17" :stroke-width="1.75" aria-hidden="true" />
        {{ t('actions.publishWork') }}
      </RouterLink>
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
      <p>{{ error }}</p><button class="command-button secondary" type="button" @click="load">
        <RefreshCw :size="17" :stroke-width="1.75" />{{ t('actions.retry') }}
      </button>
    </div>
    <div v-else>
      <div class="community-feed-toolbar">
        <strong>{{ t('discover.recent') }}</strong>
        <span class="community-toolbar-actions"><button class="text-link" type="button" @click="toggleCases">
          {{ t('community.myCases') }}
        </button><RouterLink class="text-link" to="/discover">
          {{ t('actions.browseWorks') }}<ArrowRight :size="16" :stroke-width="1.75" />
        </RouterLink></span>
      </div>
      <p v-if="feedback" class="task-feedback" :class="feedback === t('community.reportSubmitted') || feedback === t('community.appealSubmitted') ? 'success' : 'error'" role="status">
        {{ feedback }}
      </p>
      <section v-if="showCases" class="community-cases" :aria-label="t('community.myCases')">
        <header>
          <div><span class="status-label">{{ t('community.governanceLabel') }}</span><h2>{{ t('community.myCases') }}</h2></div><button class="icon-button" type="button" :aria-label="t('actions.close')" @click="showCases = false">
            <X :size="17" />
          </button>
        </header>
        <div v-if="actionLoading === 'cases'" class="page-state">
          <LoaderCircle class="spin" :size="18" />{{ t('community.loadingCases') }}
        </div>
        <div v-else-if="reports.length" class="community-case-list">
          <article v-for="item in reports" :key="item.id">
            <div><strong>{{ item.resourceTitle }}</strong><span>{{ t(`community.reportCategories.${item.category}`) }} · {{ t(`community.reportStates.${item.status}`) }}</span><p>{{ item.details }}</p><small v-if="item.resolutionReason">{{ item.resolutionReason }}</small></div>
            <button v-if="item.viewerCanAppeal" class="command-button secondary" type="button" @click="Object.assign(appealForm, { reportId: item.id, reason: '' })">
              {{ t('community.appeal') }}
            </button>
            <span v-else-if="item.appeal" :data-status="item.appeal.status">{{ t(`community.appealStates.${item.appeal.status}`) }}</span>
          </article>
        </div>
        <p v-else class="inline-empty">
          {{ t('community.noCases') }}
        </p>
        <button v-if="reportNextCursor" class="command-button secondary community-cases-load-more" type="button" :disabled="reportLoadingMore" @click="loadMoreCases">
          <LoaderCircle v-if="reportLoadingMore" class="spin" :size="16" />{{ t('actions.loadMore') }}
        </button>
        <form v-if="appealForm.reportId" class="community-governance-form" @submit.prevent="submitAppeal">
          <label>{{ t('community.appealReason') }}<textarea v-model="appealForm.reason" rows="3" minlength="10" maxlength="1000" required></textarea></label>
          <div>
            <button class="command-button primary" type="submit" :disabled="actionLoading.endsWith(':appeal')">
              {{ t('community.submitAppeal') }}
            </button><button class="command-button secondary" type="button" @click="appealForm.reportId = ''">
              {{ t('actions.cancel') }}
            </button>
          </div>
        </form>
      </section>
      <div v-if="posts.length" class="community-feed">
        <article v-for="post in posts" :key="post.id" class="post-row" :data-post-id="post.id">
          <RouterLink class="post-media" :to="`/works/${post.workId}`" :aria-label="post.workTitle">
            <AssetMedia :src="post.mediaUrl" :kind="post.mediaKind" :alt="post.workTitle" :width="1200" :height="900" :controls="false" />
          </RouterLink>
          <div class="post-copy">
            <div class="post-author">
              <span class="post-author-avatar" aria-hidden="true">{{ post.authorName.slice(0, 1) }}</span>
              <RouterLink class="post-author-identity" :to="`/creators/${post.authorHandle}`">
                <strong>{{ post.authorName }}</strong>
                <small>@{{ post.authorHandle }}</small>
              </RouterLink>
              <button v-if="session.user?.id !== post.authorId" class="post-follow" type="button" :disabled="actionLoading === `${post.id}:follow`" @click="follow(post)">
                <UserPlus :size="14" />{{ post.viewerFollowing ? t('community.following') : t('community.follow') }}
              </button>
            </div>
            <RouterLink class="post-title-link" :to="`/works/${post.workId}`">
              <h2>{{ post.workTitle }}</h2>
            </RouterLink>
            <p class="post-body">
              {{ post.body }}
            </p>
            <div class="post-actions">
              <button type="button" :class="{ active: post.viewerLiked }" :aria-label="t('community.like')" :title="t('community.like')" @click="react(post, 'like')">
                <Heart :size="17" :fill="post.viewerLiked ? 'currentColor' : 'none'" /><span>{{ post.likeCount }}</span>
              </button>
              <button type="button" :class="{ active: expandedPost === post.id }" :aria-label="t('community.comments')" :title="t('community.comments')" @click="toggleComments(post)">
                <MessageCircle :size="17" /><span>{{ post.commentCount }}</span>
              </button>
              <button type="button" :class="{ active: post.viewerBookmarked }" :aria-label="t('community.bookmark')" :title="t('community.bookmark')" @click="react(post, 'bookmark')">
                <Bookmark :size="17" :fill="post.viewerBookmarked ? 'currentColor' : 'none'" /><span>{{ post.bookmarkCount }}</span>
              </button>
              <button v-if="session.user?.id !== post.authorId" type="button" :aria-label="t('community.report')" :title="t('community.report')" @click="openReport(post)">
                <Flag :size="17" />
              </button>
            </div>
            <section v-if="expandedPost === post.id" class="post-comments">
              <p v-if="actionLoading === `${post.id}:comments`">
                {{ t('community.loadingComments') }}
              </p>
              <div v-else-if="comments[post.id]?.length">
                <article v-for="comment in comments[post.id]" :key="comment.id">
                  <strong>{{ comment.authorName }}</strong><span>@{{ comment.authorHandle }}</span><p>{{ comment.body }}</p>
                </article>
                <button v-if="commentNextCursors[post.id]" class="command-button secondary" type="button" :disabled="commentLoadingMore[post.id]" @click="loadMoreComments(post.id)">
                  <LoaderCircle v-if="commentLoadingMore[post.id]" class="spin" :size="16" />{{ t('actions.loadMore') }}
                </button>
              </div>
              <p v-else>
                {{ t('community.noComments') }}
              </p>
              <form @submit.prevent="addComment(post)">
                <input v-model="commentDrafts[post.id]" name="body" minlength="2" maxlength="1000" required :placeholder="t('community.commentPlaceholder')" /><button class="icon-button" type="submit" :aria-label="t('community.addComment')">
                  <ArrowRight :size="17" />
                </button>
              </form>
            </section>
            <form v-if="reportForm.postId === post.id" class="community-governance-form post-report-form" @submit.prevent="submitReport">
              <header>
                <strong>{{ t('community.reportPost') }}</strong><button class="icon-button" type="button" :aria-label="t('actions.close')" @click="reportForm.postId = ''">
                  <X :size="16" />
                </button>
              </header>
              <label>{{ t('community.reportCategory') }}<select v-model="reportForm.category"><option v-for="category in ['spam','harassment','copyright','sexual','violence','misleading','other']" :key="category" :value="category">{{ t(`community.reportCategories.${category}`) }}</option></select></label>
              <label>{{ t('community.reportDetails') }}<textarea v-model="reportForm.details" rows="3" minlength="10" maxlength="1000" required></textarea></label>
              <button class="command-button secondary" type="submit" :disabled="actionLoading === `${post.id}:report`">
                {{ t('community.submitReport') }}
              </button>
            </form>
            <footer class="post-footer">
              <p class="post-disclosure">
                <Sparkles :size="15" :stroke-width="1.75" aria-hidden="true" />
                <span>{{ post.aiDisclosure }}</span>
              </p>
              <div class="post-footer-actions">
                <RouterLink class="text-link" :to="`/create/image?sourceWorkId=${post.workId}`">
                  <Sparkles :size="15" :stroke-width="1.75" />{{ t('actions.useInCreate') }}
                </RouterLink>
                <RouterLink class="text-link" :to="`/works/${post.workId}`">
                  {{ t('actions.viewWork') }}<ArrowRight :size="16" :stroke-width="1.75" />
                </RouterLink>
              </div>
            </footer>
          </div>
        </article>
      </div>
      <button v-if="postNextCursor" class="command-button secondary community-feed-load-more" type="button" :disabled="postLoadingMore" @click="loadMorePosts">
        <LoaderCircle v-if="postLoadingMore" class="spin" :size="16" />{{ t('actions.loadMore') }}
      </button>
      <section v-if="!posts.length" class="community-empty" :aria-label="t('community.emptyTitle')">
        <div>
          <span class="status-label">{{ t('community.emptyTitle') }}</span>
          <h2>{{ t('community.empty') }}</h2>
          <p>{{ t('community.emptySummary') }}</p>
        </div>
        <nav class="community-empty-actions" :aria-label="t('community.emptyActionsLabel')">
          <RouterLink class="command-button primary" to="/publish">
            <Upload :size="17" :stroke-width="1.75" />{{ t('actions.publishWork') }}
          </RouterLink>
          <RouterLink class="command-button secondary" to="/discover">
            <Sparkles :size="17" :stroke-width="1.75" />{{ t('actions.browseWorks') }}
          </RouterLink>
        </nav>
      </section>
    </div>
  </section>
</template>
