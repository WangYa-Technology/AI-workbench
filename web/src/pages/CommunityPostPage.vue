<script setup lang="ts">
import { ArrowLeft, ArrowRight, Flag, LoaderCircle, MessageCircle, RefreshCw, Sparkles, UserPlus, X } from 'lucide-vue-next'
import { reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api, messageFrom, type CommunityComment, type CommunityPost, type CommunityReport } from '../api/client'
import AssetMedia from '../components/domain/AssetMedia.vue'
import MotionFavoriteIcon from '../components/ui/MotionFavoriteIcon.vue'
import { useSessionStore } from '../stores/session'
import UiButton from '../components/ui/UiButton.vue'
import UiIconButton from '../components/ui/UiIconButton.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiTextarea from '../components/ui/UiTextarea.vue'

const { locale, t } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const post = ref<CommunityPost | null>(null)
const comments = ref<CommunityComment[]>([])
const commentNextCursor = ref<string | null>(null)
const commentDraft = ref('')
const loading = ref(true)
const commentLoadingMore = ref(false)
const actionLoading = ref('')
const error = ref('')
const feedback = ref('')
const reportForm = reactive({ open: false, category: 'misleading' as CommunityReport['category'], details: '' })

function formatPublishedAt(value: string) {
  return new Intl.DateTimeFormat(locale.value, { year: 'numeric', month: 'long', day: 'numeric' }).format(new Date(value))
}

function formatCommentTime(value: string) {
  return new Intl.DateTimeFormat(locale.value, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }).format(new Date(value))
}

function formatMediaKind(value: string) {
  const normalized = value.toLowerCase()
  if (normalized.includes('video')) return t('create.modes.video')
  if (normalized.includes('audio') || normalized.includes('music')) return t('create.modes.music')
  if (normalized.includes('image')) return t('create.modes.image')
  return t('community.workTopic')
}

async function requireAccount() {
  await session.ensure()
  if (session.user) return true
  await router.push({ path: '/settings', query: { auth: 'login', redirect: route.fullPath } })
  return false
}

async function load() {
  loading.value = true
  error.value = ''
  feedback.value = ''
  reportForm.open = false
  try {
    await session.ensure()
    const postId = String(route.params.id)
    const [postItem, commentPage] = await Promise.all([
      api.getCommunityPost(postId),
      api.listCommunityComments(postId, { limit: 20 }),
    ])
    post.value = postItem
    comments.value = commentPage.items
    commentNextCursor.value = commentPage.nextCursor || null
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    loading.value = false
  }
}

async function react(kind: 'like' | 'bookmark') {
  if (!post.value || !await requireAccount()) return
  actionLoading.value = kind
  feedback.value = ''
  try {
    const active = kind === 'like' ? !post.value.viewerLiked : !post.value.viewerBookmarked
    const state = await api.setCommunityReaction(post.value.id, kind, active)
    post.value = { ...post.value, ...state }
  } catch (reason) {
    feedback.value = messageFrom(reason)
  } finally {
    actionLoading.value = ''
  }
}

async function follow() {
  if (!post.value || !await requireAccount()) return
  actionLoading.value = 'follow'
  feedback.value = ''
  try {
    const state = await api.setCommunityFollow(post.value.authorId, !post.value.viewerFollowing)
    post.value = { ...post.value, viewerFollowing: state.following }
  } catch (reason) {
    feedback.value = messageFrom(reason)
  } finally {
    actionLoading.value = ''
  }
}

async function addComment() {
  if (!post.value || !await requireAccount()) return
  const body = commentDraft.value.trim()
  if (!body) return
  actionLoading.value = 'comment'
  feedback.value = ''
  try {
    const item = await api.createCommunityComment(post.value.id, body)
    comments.value = [...comments.value, item]
    post.value = { ...post.value, commentCount: post.value.commentCount + 1 }
    commentDraft.value = ''
  } catch (reason) {
    feedback.value = messageFrom(reason)
  } finally {
    actionLoading.value = ''
  }
}

async function loadMoreComments() {
  if (!post.value || !commentNextCursor.value || commentLoadingMore.value) return
  commentLoadingMore.value = true
  try {
    const page = await api.listCommunityComments(post.value.id, { limit: 20, cursor: commentNextCursor.value })
    const known = new Set(comments.value.map(item => item.id))
    comments.value = [...comments.value, ...page.items.filter(item => !known.has(item.id))]
    commentNextCursor.value = page.nextCursor || null
  } catch (reason) {
    feedback.value = messageFrom(reason)
  } finally {
    commentLoadingMore.value = false
  }
}

async function openReport() {
  if (!await requireAccount()) return
  Object.assign(reportForm, { open: true, category: 'misleading', details: '' })
}

async function submitReport() {
  if (!post.value) return
  actionLoading.value = 'report'
  feedback.value = ''
  try {
    await api.reportCommunityPost(post.value.id, { category: reportForm.category, details: reportForm.details })
    reportForm.open = false
    feedback.value = t('community.reportSubmitted')
  } catch (reason) {
    feedback.value = messageFrom(reason)
  } finally {
    actionLoading.value = ''
  }
}

watch(() => route.params.id, () => void load(), { immediate: true })
</script>

<template>
  <section class="community-post-page content-width">
    <RouterLink class="text-link community-post-back" to="/community">
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
    <div v-else-if="post" class="community-post-layout">
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
              <span>{{ formatMediaKind(post.mediaKind) }}</span>
            </div>
            <time :datetime="post.publishedAt">{{ formatPublishedAt(post.publishedAt) }}</time>
          </header>

          <h1>{{ post.workTitle }}</h1>
          <p class="community-post-body">
            {{ post.body }}
          </p>

          <div class="community-post-actions" :aria-label="t('community.postActions')">
            <button type="button" :class="{ active: post.viewerLiked }" :disabled="actionLoading === 'like'" @click="react('like')">
              <MotionFavoriteIcon :active="post.viewerLiked" kind="heart" :size="16" />{{ t('community.like') }} <span>{{ post.likeCount }}</span>
            </button>
            <button type="button" :class="{ active: post.viewerBookmarked }" :disabled="actionLoading === 'bookmark'" @click="react('bookmark')">
              <MotionFavoriteIcon :active="post.viewerBookmarked" kind="bookmark" :size="16" />{{ t('community.bookmark') }} <span>{{ post.bookmarkCount }}</span>
            </button>
            <button v-if="session.user?.id !== post.authorId" type="button" :disabled="actionLoading === 'follow'" @click="follow">
              <UserPlus :size="16" />{{ post.viewerFollowing ? t('community.following') : t('community.follow') }}
            </button>
            <button v-if="session.user?.id !== post.authorId" type="button" @click="openReport">
              <Flag :size="16" />{{ t('community.report') }}
            </button>
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

      <aside class="community-post-context" :aria-label="t('community.relatedWork')">
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
          <RouterLink class="text-link" :to="`/create/image?sourceWorkId=${post.workId}`">
            <Sparkles :size="14" :stroke-width="1.75" />{{ t('actions.useInCreate') }}
          </RouterLink>
        </nav>
      </aside>

      <section class="community-comments" :aria-label="t('community.comments')">
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
          <label for="community-comment">{{ t('community.addComment') }}</label>
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
