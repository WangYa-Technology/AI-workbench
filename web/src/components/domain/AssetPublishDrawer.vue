<script setup lang="ts">
import { Save, Send, ShieldCheck, Trash2, Upload, X } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { api, messageFrom, type Asset, type ContentDraft, type ContentDraftSave, type TaskType } from '../../api/client'
import AssetMedia from './AssetMedia.vue'
import UiBadge from '../ui/UiBadge.vue'
import UiButton from '../ui/UiButton.vue'
import UiDrawer from '../ui/UiDrawer.vue'
import UiIconButton from '../ui/UiIconButton.vue'
import UiInput from '../ui/UiInput.vue'
import UiSelect from '../ui/UiSelect.vue'
import UiTextarea from '../ui/UiTextarea.vue'

const props = withDefaults(defineProps<{
  open?: boolean
  assetId?: string
}>(), { open: false, assetId: '' })
const emit = defineEmits<{ 'update:open': [value: boolean] }>()

const { t, locale } = useI18n()
const categories = ref<TaskType[]>([])
const category = ref('')
const route = useRoute()
const router = useRouter()
const asset = ref<Asset | null>(null)
const drafts = ref<ContentDraft[]>([])
const draftNextCursor = ref<string | null>(null)
const draftId = ref('')
const title = ref('')
const summary = ref('')
const prompt = ref('')
const promptVisibility = ref<'public' | 'partial' | 'private'>('public')
const disclosure = ref('')
const body = ref('')
const loading = ref(false)
const draftsLoadingMore = ref(false)
const saving = ref(false)
const discarding = ref(false)
const submitting = ref(false)
const error = ref('')
const success = ref('')
let loadSequence = 0

const assetDrafts = computed(() => drafts.value.filter((item) => item.assetId === props.assetId))
const activeDraft = computed(() => assetDrafts.value.find((item) => item.id === draftId.value))
const canPublish = computed(() => asset.value?.sourceType !== 'purchase' && asset.value?.scanStatus === 'clean')

function resetForm() {
  draftId.value = ''
  title.value = asset.value?.title || ''
  summary.value = ''
  prompt.value = String(route.query.prompt || '')
  promptVisibility.value = 'public'
  disclosure.value = t('publish.defaultDisclosure')
  body.value = ''
  category.value = categories.value[0]?.code || ''
  error.value = ''
  success.value = ''
}

function applyDraft(draft?: ContentDraft) {
  if (!draft || draft.assetId !== props.assetId) return
  draftId.value = draft.id
  title.value = draft.title
  summary.value = draft.summary
  prompt.value = draft.prompt
  promptVisibility.value = draft.promptVisibility
  disclosure.value = draft.aiDisclosure
  body.value = draft.body
  category.value = draft.category || categories.value[0]?.code || ''
}

async function load() {
  if (!props.open || !props.assetId) return
  const sequence = ++loadSequence
  loading.value = true
  asset.value = null
  drafts.value = []
  resetForm()
  try {
    const requestedDraftId = String(route.query.draftId || '')
    categories.value = (await api.listTaskTypes('community')).items
    category.value = categories.value[0]?.code || ''
    const [selectedAsset, draftPage, requestedDraft] = await Promise.all([
      api.getAsset(props.assetId),
      api.listContentDrafts(),
      requestedDraftId ? api.getContentDraft(requestedDraftId) : Promise.resolve(null),
    ])
    if (sequence !== loadSequence) return
    asset.value = selectedAsset
    drafts.value = requestedDraft && !draftPage.items.some((item) => item.id === requestedDraft.id)
      ? [requestedDraft, ...draftPage.items]
      : draftPage.items
    draftNextCursor.value = draftPage.nextCursor || null
    resetForm()
    if (!canPublish.value) {
      error.value = t('publish.assetUnavailable')
      return
    }
    if (requestedDraft) {
      if (requestedDraft.assetId !== props.assetId) error.value = t('publish.draftAssetMismatch')
      else applyDraft(requestedDraft)
    }
  } catch (reason) {
    if (sequence === loadSequence) error.value = messageFrom(reason)
  } finally {
    if (sequence === loadSequence) loading.value = false
  }
}

async function loadMoreDrafts() {
  if (!draftNextCursor.value || draftsLoadingMore.value) return
  draftsLoadingMore.value = true
  error.value = ''
  try {
    const page = await api.listContentDrafts({ limit: 20, cursor: draftNextCursor.value })
    const known = new Set(drafts.value.map((item) => item.id))
    drafts.value = [...drafts.value, ...page.items.filter((item) => !known.has(item.id))]
    draftNextCursor.value = page.nextCursor || null
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    draftsLoadingMore.value = false
  }
}

function selectDraft() {
  if (draftId.value) applyDraft(activeDraft.value)
  else newDraft()
}

function newDraft() {
  resetForm()
  const query = { ...route.query }
  delete query.draftId
  void router.replace({ path: route.path, query })
}

function draftPayload(expectedVersion?: number): ContentDraftSave {
  return {
    assetId: props.assetId,
    title: title.value,
    summary: summary.value,
    prompt: prompt.value,
    promptVisibility: promptVisibility.value,
    aiDisclosure: disclosure.value,
    body: body.value,
    category: category.value,
    ...(expectedVersion !== undefined ? { expectedVersion } : {}),
  }
}

async function saveDraft() {
  if (!canPublish.value) return null
  error.value = ''
  success.value = ''
  saving.value = true
  try {
    const saved = activeDraft.value
      ? await api.updateContentDraft(activeDraft.value.id, draftPayload(activeDraft.value.version))
      : await api.createContentDraft(draftPayload())
    drafts.value = [saved, ...drafts.value.filter((item) => item.id !== saved.id)]
    draftId.value = saved.id
    success.value = t('publish.draftSaved')
    await router.replace({ path: route.path, query: { ...route.query, draftId: saved.id } })
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
  success.value = ''
  try {
    await api.discardContentDraft(activeDraft.value.id, activeDraft.value.version)
    drafts.value = drafts.value.filter((item) => item.id !== activeDraft.value?.id)
    newDraft()
    success.value = t('publish.draftDiscarded')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    discarding.value = false
  }
}

async function submit() {
  if (!canPublish.value) return
  error.value = ''
  success.value = ''
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

watch(() => [props.open, props.assetId] as const, ([open]) => {
  if (open) void load()
  else loadSequence += 1
}, { immediate: true })
</script>

<template>
  <UiDrawer :open="open" side="right" size="lg" :label="t('publish.title')" @update:open="emit('update:open', $event)">
    <form class="asset-publish-drawer" @submit.prevent="submit">
      <header class="asset-publish-drawer-header">
        <div>
          <span class="status-label"><Upload :size="14" />{{ t('publish.workflowLabel') }}</span>
          <h2>{{ t('actions.publishAsset') }}</h2>
          <p>{{ asset?.title || t('publish.summary') }}</p>
        </div>
        <UiIconButton variant="ghost" :label="t('actions.close')" @click="emit('update:open', false)">
          <X :size="19" />
        </UiIconButton>
      </header>

      <div class="asset-publish-drawer-body">
        <div v-if="loading" class="asset-publish-loading" aria-live="polite">
          {{ t('status.loadingAssets') }}
        </div>

        <template v-else>
          <section v-if="asset" class="asset-publish-source" :aria-label="t('publish.assetSection')">
            <div class="asset-publish-thumbnail">
              <AssetMedia v-if="asset.scanStatus === 'clean'" :src="asset.mediaUrl" :kind="asset.kind" :alt="asset.title" :width="asset.width || 800" :height="asset.height || 800" :controls="false" />
              <ShieldCheck v-else :size="26" />
            </div>
            <div>
              <span>{{ t('publish.assetSection') }}</span>
              <strong>{{ asset.title }}</strong>
              <small>{{ asset.kind }} · {{ asset.licenseCode }}</small>
            </div>
            <UiBadge :variant="asset.scanStatus === 'clean' ? 'success' : 'warning'">
              {{ t(`workspace.scanStatus.${asset.scanStatus}`) }}
            </UiBadge>
          </section>

          <div class="asset-publish-trust-note">
            <ShieldCheck :size="18" />
            <span>{{ t('publish.trustNote') }}</span>
          </div>

          <fieldset class="asset-publish-section">
            <legend>{{ t('publish.draftSection') }}</legend>
            <label for="asset-publish-draft">{{ t('publish.savedDrafts') }}</label>
            <UiSelect id="asset-publish-draft" v-model="draftId" @change="selectDraft">
              <option value="">
                {{ t('publish.newDraft') }}
              </option>
              <option v-for="draft in assetDrafts" :key="draft.id" :value="draft.id">
                {{ draft.title || draft.assetTitle }} · v{{ draft.version }}
              </option>
            </UiSelect>
            <div v-if="draftNextCursor || activeDraft" class="asset-publish-inline-actions">
              <UiButton v-if="draftNextCursor" size="sm" variant="secondary" :loading="draftsLoadingMore" @click="loadMoreDrafts">
                {{ t('actions.loadMore') }}
              </UiButton>
              <UiButton v-if="activeDraft" size="sm" variant="ghost" :disabled="discarding" @click="discardDraft">
                <template #start>
                  <Trash2 :size="15" />
                </template>{{ t('publish.discardDraft') }}
              </UiButton>
            </div>
          </fieldset>

          <fieldset class="asset-publish-section">
            <legend>{{ t('publish.detailsSection') }}</legend>
            <label for="asset-category">{{ t('community.typeLabel') }}</label>
            <UiSelect id="asset-category" v-model="category" required>
              <option v-for="item in categories" :key="item.code" :value="item.code">
                {{ locale.startsWith('zh') ? item.nameZh : item.nameEn }}
              </option>
            </UiSelect>
            <label for="asset-publish-title">{{ t('publish.titleLabel') }}</label>
            <UiInput id="asset-publish-title" v-model="title" required minlength="3" maxlength="120" />
            <label for="asset-publish-summary">{{ t('publish.summaryLabel') }}</label>
            <UiTextarea id="asset-publish-summary" v-model="summary" rows="3" maxlength="500" />
            <label for="asset-publish-body">{{ t('publish.postLabel') }}</label>
            <UiTextarea id="asset-publish-body" v-model="body" rows="4" maxlength="2000" />
          </fieldset>

          <fieldset class="asset-publish-section">
            <legend>{{ t('publish.disclosureSection') }}</legend>
            <label for="asset-publish-prompt">{{ t('publish.promptLabel') }}</label>
            <UiTextarea id="asset-publish-prompt" v-model="prompt" rows="4" maxlength="2000" />
            <label for="asset-publish-visibility">{{ t('publish.visibilityLabel') }}</label>
            <UiSelect id="asset-publish-visibility" v-model="promptVisibility">
              <option value="public">
                {{ t('publish.visibility.public') }}
              </option>
              <option value="partial">
                {{ t('publish.visibility.partial') }}
              </option>
              <option value="private">
                {{ t('publish.visibility.private') }}
              </option>
            </UiSelect>
            <label for="asset-publish-disclosure">{{ t('publish.disclosureLabel') }}</label>
            <UiTextarea id="asset-publish-disclosure" v-model="disclosure" required minlength="10" rows="3" maxlength="500" />
          </fieldset>

          <p v-if="error" class="form-error" role="alert">
            {{ error }}
          </p>
          <p v-if="success" class="task-feedback success" role="status">
            {{ success }}
          </p>
        </template>
      </div>

      <footer class="asset-publish-drawer-footer">
        <UiButton variant="secondary" :disabled="loading || !canPublish" :loading="saving" @click="saveDraft">
          <template #start>
            <Save v-if="!saving" :size="16" />
          </template>{{ t('publish.saveDraft') }}
        </UiButton>
        <UiButton variant="primary" type="submit" :disabled="loading || saving || !canPublish" :loading="submitting">
          <template #start>
            <Send v-if="!submitting" :size="16" />
          </template>{{ submitting ? t('actions.publishing') : t('actions.publishWork') }}
        </UiButton>
      </footer>
    </form>
  </UiDrawer>
</template>

<style scoped>
.asset-publish-drawer { height: 100%; min-height: 0; display: grid; grid-template-rows: auto minmax(0, 1fr) auto; background: var(--surface); }
.asset-publish-drawer-header { min-height: 86px; display: flex; justify-content: space-between; align-items: flex-start; gap: 20px; padding: 18px 20px 16px; border-bottom: 1px solid var(--border); }
.asset-publish-drawer-header > div { min-width: 0; }
.asset-publish-drawer-header .status-label { display: inline-flex; align-items: center; gap: 6px; color: var(--accent-readable); }
.asset-publish-drawer-header h2 { margin: 7px 0 3px; font-size: 20px; line-height: 1.2; }
.asset-publish-drawer-header p { overflow: hidden; margin: 0; color: var(--text-secondary); font-size: 13px; text-overflow: ellipsis; white-space: nowrap; }
.asset-publish-drawer-body { min-height: 0; display: grid; align-content: start; gap: 20px; padding: 20px; overflow-y: auto; scrollbar-width: thin; }
.asset-publish-loading { min-height: 240px; display: grid; place-items: center; color: var(--text-secondary); }
.asset-publish-source { min-width: 0; display: grid; grid-template-columns: 72px minmax(0, 1fr) auto; align-items: center; gap: 12px; padding: 10px; border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--surface-muted); }
.asset-publish-thumbnail { width: 72px; height: 58px; display: grid; place-items: center; overflow: hidden; border-radius: 6px; background: var(--canvas); color: var(--text-secondary); }
.asset-publish-thumbnail :deep(.asset-renderer) { width: 100%; height: 100%; }
.asset-publish-source > div:nth-child(2) { min-width: 0; display: grid; gap: 2px; }
.asset-publish-source span, .asset-publish-source small { color: var(--text-secondary); font-size: 11px; }
.asset-publish-source strong { overflow: hidden; font-size: 14px; text-overflow: ellipsis; white-space: nowrap; }
.asset-publish-source :deep(.ui-badge[data-variant='success']) { color: var(--text); font-weight: 650; }
.asset-publish-trust-note { display: flex; align-items: flex-start; gap: 9px; padding: 11px 12px; border-radius: var(--radius-control); background: var(--accent-soft); color: var(--text-secondary); font-size: 12px; line-height: 1.5; }
.asset-publish-trust-note svg { flex: 0 0 auto; color: var(--accent-readable); }
.asset-publish-section { min-width: 0; display: grid; gap: 8px; margin: 0; padding: 0 0 20px; border: 0; border-bottom: 1px solid var(--border); }
.asset-publish-section legend { width: 100%; margin-bottom: 4px; padding: 0; color: var(--accent-readable); font-size: 12px; font-weight: 650; }
.asset-publish-section label { font-size: 13px; font-weight: 600; }
.asset-publish-section label:not(:first-of-type) { margin-top: 6px; }
.asset-publish-section :deep(.ui-input), .asset-publish-section :deep(.ui-select), .asset-publish-section :deep(.ui-textarea) { width: 100%; }
.asset-publish-section :deep(textarea) { resize: vertical; }
.asset-publish-inline-actions { display: flex; justify-content: flex-end; gap: 8px; }
.asset-publish-drawer-footer { min-height: 68px; display: grid; grid-template-columns: minmax(0, .9fr) minmax(0, 1.1fr); gap: 10px; padding: 12px 20px; border-top: 1px solid var(--border); background: var(--surface); }
.asset-publish-drawer-footer :deep(.ui-button) { width: 100%; }
@media (max-width: 560px) {
  .asset-publish-drawer-header, .asset-publish-drawer-body { padding-inline: 16px; }
  .asset-publish-source { grid-template-columns: 64px minmax(0, 1fr); }
  .asset-publish-source > :last-child { grid-column: 2; justify-self: start; }
  .asset-publish-thumbnail { width: 64px; height: 54px; }
  .asset-publish-drawer-footer { padding-inline: 16px; }
}
</style>
