<script setup lang="ts">
import { ArrowLeft, LoaderCircle, Save, Send, ShieldCheck, Trash2 } from 'lucide-vue-next'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api, messageFrom, type Asset, type ContentDraft, type ContentDraftSave } from '../api/client'
import { useSessionStore } from '../stores/session'
import AssetMedia from '../components/domain/AssetMedia.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const assets = ref<Asset[]>([])
const drafts = ref<ContentDraft[]>([])
const draftNextCursor = ref<string | null>(null)
const draftsLoadingMore = ref(false)
const draftId = ref(String(route.query.draftId || ''))
const assetId = ref(String(route.query.assetId || ''))
const title = ref('')
const summary = ref('')
const prompt = ref(String(route.query.prompt || ''))
const promptVisibility = ref<'public' | 'partial' | 'private'>('public')
const disclosure = ref(t('publish.defaultDisclosure'))
const body = ref('')
const loading = ref(true)
const submitting = ref(false)
const error = ref('')
const success = ref('')
const saving = ref(false)
const discarding = ref(false)
const selectedAsset = computed(() => assets.value.find((asset) => asset.id === assetId.value))
const activeDraft = computed(() => drafts.value.find((draft) => draft.id === draftId.value))

async function load() {
  loading.value = true
  try {
    const user = await session.ensure()
    if (!user) throw new Error(session.error || t('status.authenticationFailed'))
    const [assetResponse, draftResponse] = await Promise.all([api.listAssets(), api.listContentDrafts()])
    assets.value = assetResponse.items
    drafts.value = draftResponse.items
	draftNextCursor.value = draftResponse.nextCursor || null
    if (draftId.value) applyDraft(drafts.value.find((draft) => draft.id === draftId.value))
    if (!assetId.value && assets.value[0]) assetId.value = assets.value[0].id
    if (selectedAsset.value && !title.value) title.value = selectedAsset.value.title
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    loading.value = false
  }
}

async function loadMoreDrafts() {
  if (!draftNextCursor.value || draftsLoadingMore.value) return
  draftsLoadingMore.value = true
  error.value = ''
  try {
    const page = await api.listContentDrafts({ limit: 20, cursor: draftNextCursor.value })
    const known = new Set(drafts.value.map(item => item.id))
    drafts.value = [...drafts.value, ...page.items.filter(item => !known.has(item.id))]
    draftNextCursor.value = page.nextCursor || null
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    draftsLoadingMore.value = false
  }
}

function applyDraft(draft?: ContentDraft) {
  if (!draft) return
  assetId.value = draft.assetId
  title.value = draft.title
  summary.value = draft.summary
  prompt.value = draft.prompt
  promptVisibility.value = draft.promptVisibility
  disclosure.value = draft.aiDisclosure
  body.value = draft.body
}

function selectDraft() {
  if (!draftId.value) {
    newDraft()
    return
  }
  applyDraft(activeDraft.value)
}

function newDraft() {
  draftId.value = ''
  title.value = selectedAsset.value?.title || ''
  summary.value = ''
  prompt.value = String(route.query.prompt || '')
  promptVisibility.value = 'public'
  disclosure.value = t('publish.defaultDisclosure')
  body.value = ''
  void router.replace({ query: { ...route.query, draftId: undefined } })
}

function draftPayload(expectedVersion?: number): ContentDraftSave {
  return {
    assetId: assetId.value,
    title: title.value,
    summary: summary.value,
    prompt: prompt.value,
    promptVisibility: promptVisibility.value,
    aiDisclosure: disclosure.value,
    body: body.value,
    ...(expectedVersion ? { expectedVersion } : {}),
  }
}

async function saveDraft() {
  error.value = ''
  success.value = ''
  if (!assetId.value) {
    error.value = t('publish.assetRequired')
    return null
  }
  saving.value = true
  try {
    const saved = activeDraft.value
      ? await api.updateContentDraft(activeDraft.value.id, draftPayload(activeDraft.value.version))
      : await api.createContentDraft(draftPayload())
    drafts.value = [saved, ...drafts.value.filter((item) => item.id !== saved.id)]
    draftId.value = saved.id
    success.value = t('publish.draftSaved')
    await router.replace({ query: { ...route.query, draftId: saved.id } })
    return saved
  } catch (reason) {
    error.value = messageFrom(reason)
    return null
  } finally {
    saving.value = false
  }
}

async function discardDraft() {
  if (!activeDraft.value) return
  discarding.value = true
  error.value = ''
  try {
    await api.discardContentDraft(activeDraft.value.id, activeDraft.value.version)
    drafts.value = drafts.value.filter((item) => item.id !== draftId.value)
    newDraft()
    success.value = t('publish.draftDiscarded')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    discarding.value = false
  }
}

function syncTitle() {
  if (selectedAsset.value && !title.value) title.value = selectedAsset.value.title
}

async function submit() {
  error.value = ''
  if (!assetId.value) {
    error.value = t('publish.assetRequired')
    return
  }
  submitting.value = true
  try {
    const saved = activeDraft.value ? await saveDraft() : null
    if (activeDraft.value && !saved) return
    const publication = saved
      ? await api.publishContentDraft(saved.id, saved.version)
      : await api.publish(draftPayload() as Parameters<typeof api.publish>[0])
    await router.push(`/works/${publication.workId}`)
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    submitting.value = false
  }
}

onMounted(() => void load())
</script>

<template>
  <section class="publish-page content-width">
    <header class="publish-header">
      <div><span class="status-label">{{ t('publish.workflowLabel') }}</span><h1>{{ t('publish.title') }}</h1><p>{{ t('publish.summary') }}</p></div>
      <RouterLink class="command-button secondary" to="/workspace/assets">
        <ArrowLeft :size="17" />{{ t('workspace.assets') }}
      </RouterLink>
    </header>
    <div v-if="loading" class="page-state">
      {{ t('status.loadingAssets') }}
    </div>
    <form v-else class="publish-layout" @submit.prevent="submit">
      <div class="publish-preview">
        <AssetMedia v-if="selectedAsset" :src="selectedAsset.mediaUrl" :kind="selectedAsset.kind" :alt="selectedAsset.title" :width="selectedAsset.width || 1200" :height="selectedAsset.height || 1200" />
        <div v-else class="canvas-empty">
          <p>{{ t('publish.noAssetSelected') }}</p><RouterLink class="text-link" to="/create/image">
            {{ t('actions.createAsset') }}
          </RouterLink>
        </div>
      </div>
      <div class="publish-form">
        <div class="trust-note">
          <ShieldCheck :size="18" /><span>{{ t('publish.trustNote') }}</span>
        </div>
        <fieldset class="publish-form-section">
          <legend>{{ t('publish.draftSection') }}</legend>
          <label for="publish-draft">{{ t('publish.savedDrafts') }}</label>
          <select id="publish-draft" v-model="draftId" @change="selectDraft">
            <option value="">
              {{ t('publish.newDraft') }}
            </option>
            <option v-for="draft in drafts" :key="draft.id" :value="draft.id">
              {{ draft.title || draft.assetTitle }} · v{{ draft.version }}
            </option>
          </select>
          <button v-if="draftNextCursor" class="command-button secondary" type="button" :disabled="draftsLoadingMore" @click="loadMoreDrafts">
            <LoaderCircle v-if="draftsLoadingMore" class="spin" :size="16" />{{ t('actions.loadMore') }}
          </button>
          <button v-if="activeDraft" class="command-button secondary" type="button" :disabled="discarding" @click="discardDraft">
            <Trash2 :size="16" />{{ t('publish.discardDraft') }}
          </button>
        </fieldset>
        <fieldset class="publish-form-section">
          <legend>{{ t('publish.assetSection') }}</legend>
          <label for="publish-asset">{{ t('publish.asset') }}</label>
          <select id="publish-asset" v-model="assetId" required @change="syncTitle">
            <option value="" disabled>
              {{ t('publish.chooseAsset') }}
            </option>
            <option v-for="asset in assets" :key="asset.id" :value="asset.id">
              {{ asset.title }} · {{ asset.sourceType }}
            </option>
          </select>
        </fieldset>
        <fieldset class="publish-form-section">
          <legend>{{ t('publish.detailsSection') }}</legend>
          <label for="publish-title">{{ t('publish.titleLabel') }}</label>
          <input id="publish-title" v-model="title" required minlength="3" maxlength="120" />
          <label for="publish-summary">{{ t('publish.summaryLabel') }}</label>
          <textarea id="publish-summary" v-model="summary" rows="3" maxlength="500"></textarea>
          <label for="publish-body">{{ t('publish.postLabel') }}</label>
          <textarea id="publish-body" v-model="body" rows="3" maxlength="2000"></textarea>
        </fieldset>
        <fieldset class="publish-form-section">
          <legend>{{ t('publish.disclosureSection') }}</legend>
          <label for="publish-prompt">{{ t('publish.promptLabel') }}</label>
          <textarea id="publish-prompt" v-model="prompt" rows="4" maxlength="2000"></textarea>
          <label for="prompt-visibility">{{ t('publish.visibilityLabel') }}</label>
          <select id="prompt-visibility" v-model="promptVisibility">
            <option value="public">
              {{ t('publish.visibility.public') }}
            </option>
            <option value="partial">
              {{ t('publish.visibility.partial') }}
            </option>
            <option value="private">
              {{ t('publish.visibility.private') }}
            </option>
          </select>
          <label for="publish-disclosure">{{ t('publish.disclosureLabel') }}</label>
          <textarea id="publish-disclosure" v-model="disclosure" required minlength="10" rows="3" maxlength="500"></textarea>
        </fieldset>
        <p v-if="error" class="form-error" role="alert">
          {{ error }}
        </p>
        <p v-if="success" class="task-feedback success" role="status">
          {{ success }}
        </p>
        <div class="publish-command-row">
          <button class="command-button secondary" type="button" :disabled="saving || !assets.length" @click="saveDraft">
            <LoaderCircle v-if="saving" class="spin" :size="17" /><Save v-else :size="17" />{{ t('publish.saveDraft') }}
          </button>
          <button class="command-button primary" type="submit" :disabled="submitting || saving || !assets.length">
            <Send :size="17" />{{ submitting ? t('actions.publishing') : t('actions.publishWork') }}
          </button>
        </div>
      </div>
    </form>
  </section>
</template>
