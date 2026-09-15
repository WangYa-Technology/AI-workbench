<script setup lang="ts">
import { ArrowLeft, ArrowRight, Flag, LoaderCircle, MessageCircle, MoreHorizontal, RefreshCw, Sparkles, UserPlus, X } from 'lucide-vue-next'
import { onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api, messageFrom, type CommunityComment, type CommunityPost, type CommunityReport, type TaskType } from '../api/client'
import AssetMedia from '../components/domain/AssetMedia.vue'
import MotionFavoriteIcon from '../components/ui/MotionFavoriteIcon.vue'
import { useSessionStore } from '../stores/session'
import UiButton from '../components/ui/UiButton.vue'
import UiIconButton from '../components/ui/UiIconButton.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiTextarea from '../components/ui/UiTextarea.vue'
import UiDropdownMenu from '../components/ui/UiDropdownMenu.vue'
import MarkdownContent from '../components/ui/MarkdownContent.vue'
import { contentListReturn, creationPath } from '../lib/contentPresentation'

const { locale, t } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const post = ref<CommunityPost | null>(null)
const categories = ref<TaskType[]>([])
const categoryName = (code?: string) => { const item = categories.value.find(i => i.code === code); return item ? (locale.value.startsWith('zh') ? item.nameZh : item.nameEn) : code || '' }
const comments = ref<CommunityComment[]>([])
const commentNextCursor = ref<string | null>(null)
const commentDraft = ref('')
const loading = ref(true)
const commentLoadingMore = ref(false)
const actionLoading = ref('')
const error = ref('')
const feedback = ref('')
const commentError = ref('')
const reportForm = reactive({ open: false, category: 'misleading' as CommunityReport['category'], details: '' })

function formatPublishedAt(value: string) {
  return new Intl.DateTimeFormat(locale.value, { year: 'numeric', month: 'long', day: 'numeric' }).format(new Date(value))
}

function formatCommentTime(value: string) {
  return new Intl.DateTimeFormat(locale.value, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }).format(new Date(value))
}

function formatMediaKind(value?: string) {
  if (!value) return t('community.discussionType')
  const normalized = value.toLowerCase()
  if (normalized.includes('video')) return t('create.modes.video')
  if (normalized.includes('audio') || normalized.includes('music')) return t('create.modes.music')
  if (normalized.includes('image')) return t('create.modes.image')
  return t('community.workTopic')
}

async function requireAccount() {
  await session.ensure()
  if (session.user) return true
  await router.push({ path: '/auth', query: { auth: 'login', returnTo: route.fullPath } })
  return false
}

let loadVersion = 0
onBeforeUnmount(() => { loadVersion++ })
async function load() {
  const version = ++loadVersion
  const postId = String(route.params.id)
  commentLoadingMore.value = false
  loading.value = true
  error.value = ''
  feedback.value = ''
  actionLoading.value = ''
  reportForm.open = false
  commentError.value = ''
  try {
    await session.ensure()
    if (version !== loadVersion) return
    const [postItem, commentPage, directory] = await Promise.all([
      api.getCommunityPost(postId),
      api.listCommunityComments(postId, { limit: 20 }),
      api.listTaskTypes('community'),
    ])
    if (version !== loadVersion) return
    post.value = postItem
    categories.value = directory.items
    comments.value = commentPage.items
    commentNextCursor.value = commentPage.nextCursor || null
  } catch (reason) {
    if (version === loadVersion) error.value = messageFrom(reason)
  } finally {
    if (version === loadVersion) loading.value = false
  }
}

async function react(kind: 'like' | 'bookmark') {
  if (!post.value || !await requireAccount()) return
  const version = loadVersion
  actionLoading.value = kind
  feedback.value = ''
  try {
    const active = kind === 'like' ? !post.value.viewerLiked : !post.value.viewerBookmarked
    const state = await api.setCommunityReaction(post.value.id, kind, active)
    if (version !== loadVersion) return
    post.value = { ...post.value, ...state }
  } catch (reason) {
    if (version === loadVersion) feedback.value = messageFrom(reason)
  } finally {
    if (version === loadVersion) actionLoading.value = ''
  }
}

async function follow() {
  if (!post.value || !await requireAccount()) return
  const version = loadVersion
  actionLoading.value = 'follow'
  feedback.value = ''
  try {
    const state = await api.setCommunityFollow(post.value.authorId, !post.value.viewerFollowing)
    if (version !== loadVersion) return
    post.value = { ...post.value, viewerFollowing: state.following }
  } catch (reason) {
    if (version === loadVersion) feedback.value = messageFrom(reason)
  } finally {
    if (version === loadVersion) actionLoading.value = ''
  }
}

async function addComment() {
  if (!post.value || !await requireAccount()) return
  const body = commentDraft.value.trim()
  if (!body) return
  const version = loadVersion
  actionLoading.value = 'comment'
  commentError.value = ''
  try {
    const item = await api.createCommunityComment(post.value.id, body)
    if (version !== loadVersion) return
    comments.value = [...comments.value, item]
    post.value = { ...post.value, commentCount: post.value.commentCount + 1 }
    commentDraft.value = ''
  } catch (reason) {
    if (version === loadVersion) commentError.value = messageFrom(reason)
  } finally {
    if (version === loadVersion) actionLoading.value = ''
  }
}

async function loadMoreComments() {
  if (!post.value || !commentNextCursor.value || commentLoadingMore.value) return
  const version = loadVersion
  commentLoadingMore.value = true
  commentError.value = ''
  try {
    const page = await api.listCommunityComments(post.value.id, { limit: 20, cursor: commentNextCursor.value })
    if (version !== loadVersion) return
    const known = new Set(comments.value.map(item => item.id))
    comments.value = [...comments.value, ...page.items.filter(item => !known.has(item.id))]
    commentNextCursor.value = page.nextCursor || null
  } catch (reason) {
    if (version === loadVersion) commentError.value = messageFrom(reason)
  } finally {
    if (version === loadVersion) commentLoadingMore.value = false
  }
}

async function openReport() {
  if (!await requireAccount()) return
  Object.assign(reportForm, { open: true, category: 'misleading', details: '' })
}

async function submitReport() {
  if (!post.value) return
  const version = loadVersion
  actionLoading.value = 'report'
  feedback.value = ''
  try {
    await api.reportCommunityPost(post.value.id, { category: reportForm.category, details: reportForm.details })
    if (version !== loadVersion) return
    reportForm.open = false
    feedback.value = t('community.reportSubmitted')
  } catch (reason) {
    if (version === loadVersion) feedback.value = messageFrom(reason)
  } finally {
    if (version === loadVersion) actionLoading.value = ''
  }
}

watch(() => route.params.id, () => {
  try { commentDraft.value = globalThis.sessionStorage.getItem(`community-draft:${route.params.id}`) || '' } catch { commentDraft.value = '' }
  void load()
}, { immediate: true })
watch(commentDraft, value => {
  try {
    const key = `community-draft:${route.params.id}`
    if (value) globalThis.sessionStorage.setItem(key, value)
    else globalThis.sessionStorage.removeItem(key)
  } catch { /* Storage may be disabled; editing remains available. */ }
})
</script>

<template>
  <section class="community-post-page content-width">
    <RouterLink class="text-link community-post-back" :to="contentListReturn('/community')">
      <ArrowLeft :size="15" :stroke-width="1.75" />{{ t('community.backToCommunity') }}
    </RouterLink>

    <div v-if="loading" class="page-state" aria-live="polite">
      <LoaderCircle class="spin" :size="18" />{{ t('community.loadingPost') }}
    </div>
    <div v-else-if="error" class="page-state" role="alert">
      <p>{{ error }}</p>
      <UiButton class="command-button secondary" variant="secondary" @click="load">
        <template #start>
          <RefreshCw :size="17" />
        </template>{{ t('actions.retry') }}
      </UiButton>
    </div>
    <div v-else-if="post" class="community-post-layout" :class="{ 'is-standalone': !post.workId }">
      <div class="community-post-main">
        <article class="community-post-article">
          <header class="community-post-author">
            <RouterLink class="community-post-avatar" :to="`/creators/${post.authorHandle}`" :aria-label="post.authorName">
              {{ post.authorName.slice(0, 1) }}
            </RouterLink>
            <div>
              <RouterLink :to="`/creators/${post.authorHandle}`">
                <strong>{{ post.authorName }}</strong>
                <small>@{{ post.authorHandle }}</small>
              </RouterLink>
              <span>{{ categoryName(post.category) }} · {{ formatMediaKind(post.mediaKind) }}</span>
            </div>
            <time :datetime="post.publishedAt">{{ formatPublishedAt(post.publishedAt) }}</time>
            <UiButton v-if="session.user?.id !== post.authorId" variant="secondary" type="button" :loading="actionLoading === 'follow'" @click="follow">
              <UserPlus :size="16" />{{ post.viewerFollowing ? t('community.following') : t('community.follow') }}
            </UiButton>
          </header>

          <h1>{{ post.title }}</h1>
          <MarkdownContent class="community-post-body" :source="post.body" />

          <div class="community-post-actions" :aria-label="t('community.postActions')">
            <UiButton variant="ghost" type="button" :class="{ active: post.viewerLiked }" :disabled="actionLoading === 'like'" :loading="actionLoading === 'like'" @click="react('like')">
              <MotionFavoriteIcon :active="post.viewerLiked" kind="heart" :size="16" />{{ t('community.like') }} <span>{{ post.likeCount }}</span>
            </UiButton>
            <UiButton variant="ghost" type="button" :class="{ active: post.viewerBookmarked }" :disabled="actionLoading === 'bookmark'" :loading="actionLoading === 'bookmark'" @click="react('bookmark')">
              <MotionFavoriteIcon :active="post.viewerBookmarked" kind="bookmark" :size="16" />{{ t('community.bookmark') }} <span>{{ post.bookmarkCount }}</span>
            </UiButton>
            <a class="text-link" href="#post-comments"><MessageCircle :size="16" />{{ t('community.comments') }} {{ post.commentCount }}</a>
            <UiDropdownMenu v-if="session.user?.id !== post.authorId" class="post-more" :label="t('content.more')">
              <template #trigger>
                <MoreHorizontal :size="18" />
              </template>
              <UiButton role="menuitem" variant="ghost" type="button" @click="openReport">
                <Flag :size="16" />{{ t('community.report') }}
              </UiButton>
            </UiDropdownMenu>
          </div>

          <p v-if="feedback" class="task-feedback" :class="feedback === t('community.reportSubmitted') ? 'success' : 'error'" role="status">
            {{ feedback }}
          </p>

          <form v-if="reportForm.open" class="community-governance-form community-post-report" @submit.prevent="submitReport">
            <header>
              <strong>{{ t('community.reportPost') }}</strong>
              <UiIconButton class="icon-button" :label="t('actions.close')" @click="reportForm.open = false">
                <X :size="16" />
              </UiIconButton>
            </header>
            <label>{{ t('community.reportCategory') }}<UiSelect v-model="reportForm.category"><option v-for="category in ['spam','harassment','copyright','sexual','violence','misleading','other']" :key="category" :value="category">{{ t(`community.reportCategories.${category}`) }}</option></UiSelect></label>
            <label>{{ t('community.reportDetails') }}<UiTextarea v-model="reportForm.details" rows="3" minlength="10" maxlength="1000" required /></label>
            <UiButton class="command-button secondary" variant="secondary" type="submit" :loading="actionLoading === 'report'">
              {{ t('community.submitReport') }}
            </UiButton>
          </form>
        </article>
      </div>

      <aside v-if="post.workId && post.workTitle && post.mediaUrl && post.mediaKind" class="community-post-context" :aria-label="t('community.relatedWork')">
        <RouterLink class="community-post-media" :to="`/works/${post.workId}`" :aria-label="post.workTitle">
          <AssetMedia :src="post.mediaUrl" :kind="post.mediaKind" :alt="post.workTitle" :width="1200" :height="900" :controls="false" />
        </RouterLink>
        <div>
          <span>{{ t('community.relatedWork') }}</span>
          <h2>{{ post.workTitle }}</h2>
          <p>{{ post.aiDisclosure }}</p>
        </div>
        <nav class="community-post-context-actions" :aria-label="t('community.relatedWork')">
          <RouterLink class="text-link" :to="`/works/${post.workId}`">
            {{ t('community.attachedWork') }}<ArrowRight :size="14" :stroke-width="1.75" />
          </RouterLink>
          <RouterLink class="text-link" :to="{ path: creationPath(post.mediaKind), query: { sourceWorkId: post.workId } }">
            <Sparkles :size="14" :stroke-width="1.75" />{{ t('actions.useInCreate') }}
          </RouterLink>
        </nav>
      </aside>

      <section id="post-comments" class="community-comments" :aria-label="t('community.comments')">
        <p v-if="commentError" class="task-feedback error" role="alert">
          {{ commentError }}
        </p>
        <header>
          <h2>{{ t('community.comments') }}</h2>
          <span>{{ post.commentCount }}</span>
        </header>

        <div v-if="comments.length" class="community-comment-list">
          <article v-for="comment in comments" :key="comment.id">
            <span class="community-comment-avatar" aria-hidden="true">{{ comment.authorName.slice(0, 1) }}</span>
            <div>
              <header>
                <RouterLink :to="`/creators/${comment.authorHandle}`">
                  <strong>{{ comment.authorName }}</strong><small>@{{ comment.authorHandle }}</small>
                </RouterLink>
                <time :datetime="comment.createdAt">{{ formatCommentTime(comment.createdAt) }}</time>
              </header>
              <p>{{ comment.body }}</p>
            </div>
          </article>
        </div>
        <p v-else class="community-comments-empty">
          {{ t('community.noComments') }}
        </p>

        <UiButton v-if="commentNextCursor" class="command-button secondary community-comment-load-more" variant="secondary" :loading="commentLoadingMore" @click="loadMoreComments">
          {{ t('actions.loadMore') }}
        </UiButton>

        <form class="community-comment-composer" @submit.prevent="addComment">
          <label for="community-comment">{{ t('content.discussion') }}</label>
          <div class="community-comment-field">
            <UiTextarea id="community-comment" v-model="commentDraft" name="body" rows="3" minlength="2" maxlength="1000" required :placeholder="t('community.commentPlaceholder')" />
            <div>
              <UiButton class="command-button" variant="primary" type="submit" :loading="actionLoading === 'comment'">
                <template #start>
                  <MessageCircle v-if="actionLoading !== 'comment'" :size="15" />
                </template>{{ t('community.addComment') }}
              </UiButton>
            </div>
          </div>
        </form>
      </section>
    </div>
  </section>
</template>

<style scoped>
.community-post-layout { grid-template-columns: minmax(0, 740px) minmax(220px, 280px); gap: 0 24px; justify-content: center; }
.community-post-layout.is-standalone { grid-template-columns: minmax(0, 800px); }
.community-post-author { grid-template-columns: 36px minmax(0, 1fr) auto auto; }
.community-post-author > div { flex-wrap: wrap; }
.community-post-author > div > a { flex-wrap: wrap; max-width: 100%; overflow-wrap: anywhere; }
.community-post-author strong, .community-post-author small { min-width: 0; max-width: 100%; }
.community-post-author strong { font-size: 13px; }
.community-post-author small, .community-post-author span, .community-post-author time { font-size: 12px; }
.community-post-article h1 { font-size: clamp(25px, 2.4vw, 32px); line-height: 1.35; overflow-wrap: anywhere; }
.community-post-body { max-width: none; color: var(--text); font-size: 15px; line-height: 1.8; white-space: normal; overflow-wrap: anywhere; }
.community-post-body :deep(pre) { max-width: 100%; overflow-x: auto; }
.community-post-actions button, .community-post-actions > a { min-height: 38px; font-size: 13px; }
.post-more { position: relative; margin-left: auto; }
.post-more :deep(.ui-dropdown-menu__content) { left: auto; right: 0; }
.community-post-context { padding: 14px; border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface); }
.community-post-context p, .community-post-context > div > span, .community-post-context-actions .text-link { font-size: 12px; }
.community-post-context h2 { white-space: normal; }
.community-comment-list article p { font-size: 14px; line-height: 1.7; }
.community-comment-list article strong, .community-comment-list article small, .community-comment-list article time, .community-comments-empty { font-size: 12px; }
.community-comment-composer label { font-size: 13px; }
.community-comment-composer .command-button { min-height: 38px; font-size: 13px; background: var(--accent); color: white; border-color: transparent; }
.community-comments { scroll-margin-top: 24px; }
@media(max-width: 1000px) { .community-post-layout { grid-template-columns: minmax(0, 1fr); grid-template-areas: 'post' 'context' 'comments'; gap: 20px; } .community-post-context { position: static; } }
@media(max-width: 600px) { .community-post-author { grid-template-columns: 36px minmax(0, 1fr) auto; } .community-post-author time { grid-column: 2; grid-row: 2; } .community-post-author > button { grid-column: 3; grid-row: 1 / 3; } }
</style>
