<script setup lang="ts">
import { onClickOutside, onKeyStroke, useIntervalFn } from '@vueuse/core'
import {
  AlertCircle, Ban, Bookmark, BriefcaseBusiness, Check, Cloud, Download, Eraser, FileText, FolderOpen, History,
  Image as ImageIcon, Images, LoaderCircle, MessageSquare, MessageSquareText,
  Music, Paperclip, Plus, RefreshCw, RotateCcw, Send, Share2, SlidersHorizontal, SquarePlus,
  Sparkles, Upload, Video, WalletCards, X,
} from 'lucide-vue-next'
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import {
  api, messageFrom, type Asset, type BillingStatement, type CreationCapabilities, type Generation, type TaskDetail, type Work,
} from '../../api/client'
import { formatCurrency, formatDateTime } from '../../lib/format'
import {
  useCreationDraftStore, type CreationDraftMode, type CreationDraftView, type CreationOutputSettings,
} from '../../stores/creationDraft'
import { usePreferencesStore } from '../../stores/preferences'
import { useSessionStore } from '../../stores/session'
import { isCreationCapabilityComplete } from '../../lib/creationCapabilities'
import AssetMedia from '../domain/AssetMedia.vue'
import AuthRequiredState from '../domain/AuthRequiredState.vue'

type CreationMode = CreationDraftMode
const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const props = defineProps<{ mode: CreationMode }>()
const session = useSessionStore()
const preferences = usePreferencesStore()
const draftStore = useCreationDraftStore()
const restoredDraft = draftStore.restore(props.mode)
const view = ref<CreationDraftView>(restoredDraft?.view || (props.mode === 'chat' ? 'guide' : 'gallery'))
const prompt = ref(restoredDraft?.prompt || '')
const generations = ref<Generation[]>([])
const billing = ref<BillingStatement | null>(null)
const assets = ref<Asset[]>([])
const capabilities = ref<CreationCapabilities | null>(null)
const sourceAssets = ref<Asset[]>([])
const maskAsset = ref<Asset | null>(null)
const sourceWork = ref<Work | null>(null)
const sourceTask = ref<TaskDetail | null>(null)
const selectedGeneration = ref<Generation | null>(null)
const activeChatGenerationId = ref<string | null>(null)
const chatSelectionReady = ref(false)
const loading = ref(true)
const assetsLoading = ref(false)
const submitting = ref(false)
const actionLoading = ref('')
const uploadLoading = ref(false)
const assetPickerOpen = ref(false)
const referencePickerMode = ref<'references' | 'mask'>('references')
const modeMenuOpen = ref(false)
const modeMenu = ref<InstanceType<typeof globalThis.HTMLElement> | null>(null)
const modeMenuTrigger = ref<InstanceType<typeof globalThis.HTMLButtonElement> | null>(null)
const controlsOpen = ref(false)
const controlsPanel = ref<InstanceType<typeof globalThis.HTMLElement> | null>(null)
const controlsTrigger = ref<InstanceType<typeof globalThis.HTMLButtonElement> | null>(null)
const error = ref('')
const feedback = ref('')
const fileInput = ref<InstanceType<typeof globalThis.HTMLInputElement> | null>(null)
const settings = ref<CreationOutputSettings>(restoredDraft?.settings || defaultModeSettings(props.mode))
const modes = computed(() => [
  { id: 'chat' as const, icon: MessageSquare, label: t('create.modes.chat') },
  { id: 'image' as const, icon: ImageIcon, label: t('create.modes.image') },
  { id: 'video' as const, icon: Video, label: t('create.modes.video') },
  { id: 'music' as const, icon: Music, label: t('create.modes.music') },
])
const conversationGenerations = computed(() => [...generations.value].reverse())
const activeCapability = computed(() => capabilities.value?.items.find(item => item.mode === props.mode) || null)
const capabilityProjectionComplete = computed(() => isCreationCapabilityComplete(activeCapability.value))
const modeIcon = computed(() => ({ chat: MessageSquare, image: ImageIcon, video: Video, music: Music })[props.mode])
const formatOptions = computed(() => ({
  chat: ['txt'], image: ['jpeg'], video: ['mp4'], music: ['wav'],
})[props.mode] as string[])
const capabilityFormatOptions = computed(() => {
  if (!capabilities.value) return formatOptions.value
  return capabilityProjectionComplete.value ? activeCapability.value!.outputFormats : []
})
const referenceKinds = computed(() => {
  if (!capabilities.value) return ({
  chat: ['document'], image: ['image'], video: ['image'], music: ['audio'],
  })[props.mode]
  return capabilityProjectionComplete.value ? activeCapability.value!.referenceKinds : []
})
const ratioOptions = computed(() => {
  if (!capabilities.value) return ['auto', '1:1', '4:5', '16:9']
  return capabilityProjectionComplete.value ? activeCapability.value!.aspectRatios : []
})
const qualityOptions = computed(() => {
  if (!capabilities.value) return ['auto', 'standard', 'high']
  return capabilityProjectionComplete.value ? activeCapability.value!.qualities : []
})
const durationOptions = computed(() => {
  if (!capabilities.value) return props.mode === 'music' ? [5, 10, 30, 60] : [5, 10, 30]
  return capabilityProjectionComplete.value ? activeCapability.value!.durationSeconds : []
})
const supportsMask = computed(() => {
  if (!capabilities.value) return props.mode === 'image'
  return capabilityProjectionComplete.value && activeCapability.value!.supportsMask
})
const capabilityUnavailable = computed(() => Boolean(capabilities.value && (!capabilityProjectionComplete.value || !activeCapability.value?.available)))
const referenceAccept = computed(() => ({
  chat: 'text/plain,.txt,.md',
  image: 'image/jpeg,image/png',
  video: 'image/jpeg,image/png',
  music: 'audio/wav,audio/x-wav,audio/wave,audio/mpeg',
})[props.mode])
const running = computed(() => generations.value.some(item => ['queued', 'running'].includes(item.status)))
const activeChatGeneration = computed(() => activeChatGenerationId.value
  ? generations.value.find(item => item.id === activeChatGenerationId.value) || null
  : null)
const canSubmit = computed(() => {
  const chatReady = props.mode !== 'chat' || !activeChatGeneration.value || activeChatGeneration.value.status === 'succeeded'
  const maskReady = !maskAsset.value || (props.mode === 'image' && sourceAssets.value.length > 0)
  return prompt.value.trim().length >= 3 && !submitting.value && Boolean(session.user) && !capabilityUnavailable.value && chatReady && maskReady
})
const generateLabel = computed(() => props.mode === 'image'
  ? t('actions.generateImage')
  : t('actions.generateMode', { mode: t(`create.modes.${props.mode}`) }))

function generationDate(value: string) {
  return formatDateTime(value, locale.value, Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC')
}

function generationCost(item: Generation) {
  return formatCurrency(item.chargedCostCents || item.estimatedCostCents, 'USD', locale.value)
}

function generationMediaKind(item: Generation) {
  return ({ chat: 'document', image: 'image', video: 'video', music: 'audio' } as const)[item.mode] || 'document'
}

async function selectCreationMode(mode: CreationMode) {
  modeMenuOpen.value = false
  assetPickerOpen.value = false
  controlsOpen.value = false
  if (mode === props.mode) return
  await router.replace(`/create/${mode}`)
}

function toggleModeMenu() {
  modeMenuOpen.value = !modeMenuOpen.value
  if (modeMenuOpen.value) {
    assetPickerOpen.value = false
    controlsOpen.value = false
  }
}

onClickOutside(modeMenu, () => {
  modeMenuOpen.value = false
}, { ignore: [modeMenuTrigger] })

onClickOutside(controlsPanel, () => {
  controlsOpen.value = false
}, { ignore: [controlsTrigger] })

onKeyStroke('Escape', () => {
  modeMenuOpen.value = false
  controlsOpen.value = false
})

async function loadCapabilities() {
  try {
    capabilities.value = await api.creationCapabilities()
  } catch {
    // The fallback control sets keep the local builder usable during a brief
    // capability request outage; submission remains server-authorized.
  }
}

async function loadBilling() {
  if (!session.user) {
    billing.value = null
    return
  }
  try {
    billing.value = await api.billingStatement({ limit: 1 })
  } catch {
    billing.value = null
  }
}

function assetMeta(asset: Asset) {
  const dimensions = asset.width && asset.height ? `${asset.width}\u00d7${asset.height}` : asset.mimeType
  return `${dimensions} \u00b7 v${asset.versionNumber}`
}

function defaultModeSettings(mode: CreationMode): CreationOutputSettings {
  return {
    ratio: 'auto',
    quality: 'auto',
    format: ({ chat: 'txt', image: 'jpeg', video: 'mp4', music: 'wav' } as const)[mode],
    count: 1,
    duration: mode === 'music' ? 30 : 10,
    responseLength: 'balanced',
  }
}

function generationParameters(): Generation['parameters'] {
  if (props.mode === 'chat') {
    return { responseLength: settings.value.responseLength, outputFormat: settings.value.format as 'txt' }
  }
  if (props.mode === 'image') {
    return { aspectRatio: settings.value.ratio, quality: settings.value.quality, outputFormat: settings.value.format as 'jpeg' }
  }
  if (props.mode === 'video') {
    return { aspectRatio: settings.value.ratio, quality: settings.value.quality, outputFormat: settings.value.format as 'mp4', durationSeconds: settings.value.duration as 5 | 10 | 30 }
  }
  return { quality: settings.value.quality, outputFormat: settings.value.format as 'wav', durationSeconds: settings.value.duration as 5 | 10 | 30 | 60 }
}

function parameterSummary(item: Generation) {
  const parameters = item.parameters
  if (item.mode === 'chat') return [parameters.responseLength, parameters.outputFormat?.toUpperCase()].filter(Boolean).join(' · ')
  const values: Array<string | undefined> = [parameters.aspectRatio, parameters.quality]
  if (parameters.durationSeconds) values.push(t('create.studio.durationValue', { value: parameters.durationSeconds }))
  values.push(parameters.outputFormat?.toUpperCase())
  return values.filter(Boolean).join(' · ')
}

async function loadGenerations(silent = false) {
  if (!session.user) {
    loading.value = false
    generations.value = []
    return
  }
  if (!silent) loading.value = true
  try {
    const page = await api.listGenerations({ limit: 50 })
    generations.value = page.items
    if (props.mode === 'chat' && !chatSelectionReady.value) {
      activeChatGenerationId.value = page.items.find(item => item.mode === 'chat')?.id || null
      chatSelectionReady.value = true
    }
    if (selectedGeneration.value) {
      selectedGeneration.value = page.items.find(item => item.id === selectedGeneration.value?.id) || selectedGeneration.value
    }
  } catch (reason) {
    if (!silent) error.value = messageFrom(reason)
  } finally {
    if (!silent) loading.value = false
  }
}

async function loadAssets() {
  if (!session.user || assetsLoading.value) return
  assetsLoading.value = true
  try {
    const page = await api.listAssets({ limit: 50 })
    assets.value = page.items.filter(item => item.scanStatus === 'clean' && referenceKinds.value.includes(item.kind))
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    assetsLoading.value = false
  }
}

async function loadSource() {
  sourceWork.value = null
  sourceTask.value = null
  sourceAssets.value = []
  maskAsset.value = null
  try {
    const sourceWorkId = String(route.query.sourceWorkId || '')
    const sourceTaskId = String(route.query.taskId || '')
    const sourceAssetId = String(route.query.sourceAssetId || '')
    if (sourceWorkId) {
      sourceWork.value = await api.getWork(sourceWorkId)
      prompt.value = sourceWork.value.prompt || ''
    } else if (sourceTaskId) {
      sourceTask.value = await api.getTask(sourceTaskId)
      prompt.value = sourceTask.value.brief
    } else if (sourceAssetId) {
      const asset = await api.getAsset(sourceAssetId)
      sourceAssets.value = [asset]
      prompt.value = t('create.sourceAssetPrompt', { title: asset.title })
    }
  } catch (reason) {
    error.value = messageFrom(reason)
  }
}

async function chooseAsset(asset: Asset) {
  if (!referenceKinds.value.includes(asset.kind)) {
    error.value = t('create.studio.referenceUnavailable')
    return
  }
  if (referencePickerMode.value === 'mask') {
    maskAsset.value = maskAsset.value?.id === asset.id ? null : asset
    feedback.value = maskAsset.value ? t('create.studio.maskAttached', { title: asset.title }) : ''
    return
  }
  const existing = sourceAssets.value.findIndex(item => item.id === asset.id)
  if (existing >= 0) {
    sourceAssets.value = sourceAssets.value.filter(item => item.id !== asset.id)
    return
  }
  if (sourceAssets.value.length >= 8) {
    error.value = t('create.studio.referenceLimit')
    return
  }
  sourceAssets.value = [...sourceAssets.value, asset]
  sourceWork.value = null
  feedback.value = t('create.studio.referenceAttached', { title: asset.title })
}

function removeSourceAsset(assetID: string) {
  sourceAssets.value = sourceAssets.value.filter(item => item.id !== assetID)
}

function openAssetPicker(mode: 'references' | 'mask') {
  modeMenuOpen.value = false
  referencePickerMode.value = mode
  assetPickerOpen.value = true
  void loadAssets()
}

async function waitForCleanAsset(asset: Asset) {
  let current = asset
  for (let attempt = 0; attempt < 20; attempt += 1) {
    if (current.scanStatus === 'clean') return current
    if (['review', 'rejected'].includes(current.scanStatus)) throw new Error(t('create.studio.referenceUnavailable'))
    await new Promise(resolve => globalThis.setTimeout(resolve, 800))
    current = await api.getAsset(current.id)
  }
  throw new Error(t('create.studio.referenceProcessing'))
}

async function uploadReference(event: globalThis.Event) {
  const input = event.target as InstanceType<typeof globalThis.HTMLInputElement>
  const file = input.files?.[0]
  if (!file) return
  uploadLoading.value = true
  error.value = ''
  feedback.value = t('create.studio.referenceUploading')
  try {
    const form = new globalThis.FormData()
    form.append('title', file.name.replace(/\.[^.]+$/, '') || t('create.studio.referenceImage'))
    form.append('file', file)
    const uploaded = await api.uploadAsset(form)
    const clean = await waitForCleanAsset(uploaded)
    assets.value = [clean, ...assets.value.filter(item => item.id !== clean.id)]
    await chooseAsset(clean)
  } catch (reason) {
    error.value = messageFrom(reason)
    feedback.value = ''
  } finally {
    uploadLoading.value = false
    input.value = ''
  }
}

async function submit() {
  if (!canSubmit.value) return
  submitting.value = true
  error.value = ''
  feedback.value = ''
  // Close transient pickers before the new result enters the gallery so they
  // cannot cover the result's first available actions.
  assetPickerOpen.value = false
  controlsOpen.value = false
  try {
    const submittedPrompt = prompt.value.trim()
    const input = {
      mode: props.mode,
      prompt: submittedPrompt,
      parameters: generationParameters(),
      sourceWorkId: sourceWork.value?.id || null,
      sourceAssetId: sourceAssets.value[0]?.id || null,
      sourceAssetIds: sourceAssets.value.map(asset => asset.id),
      maskAssetId: maskAsset.value?.id || null,
      sourceTaskId: sourceTask.value?.id || null,
      parentGenerationId: props.mode === 'chat' ? activeChatGenerationId.value : null,
    }
    const results = await Promise.allSettled(
      Array.from({ length: props.mode === 'chat' ? 1 : settings.value.count }, () => api.createGeneration(input)),
    )
    const created = results.flatMap(result => result.status === 'fulfilled' ? [result.value] : [])
    const failed = results.length - created.length
    generations.value = [...created, ...generations.value]
    if (props.mode === 'chat' && created[0]) {
      activeChatGenerationId.value = created[0].id
      chatSelectionReady.value = true
      view.value = 'guide'
      prompt.value = ''
    } else {
      view.value = 'guide'
      prompt.value = ''
    }
    if (failed) error.value = t('create.studio.partialFailure', { count: failed })
    else feedback.value = t('create.studio.queued', { count: created.length })
    resume()
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    submitting.value = false
  }
}

async function changeGeneration(item: Generation, action: 'cancel' | 'retry') {
  actionLoading.value = `${item.id}:${action}`
  error.value = ''
  try {
    const updated = action === 'cancel'
      ? await api.cancelGeneration(item.id, `Cancelled from the ${props.mode} creation studio.`)
      : await api.retryGeneration(item.id)
    generations.value = action === 'retry'
      ? [updated, ...generations.value]
      : generations.value.map(current => current.id === item.id ? updated : current)
    selectedGeneration.value = updated
    resume()
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionLoading.value = ''
  }
}

async function toggleFavorite(item: Generation) {
  actionLoading.value = `${item.id}:favorite`
  error.value = ''
  try {
    const updated = await api.favoriteGeneration(item.id, !item.isFavorite)
    generations.value = generations.value.map(current => current.id === updated.id ? updated : current)
    if (selectedGeneration.value?.id === updated.id) selectedGeneration.value = updated
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionLoading.value = ''
  }
}

function reuseGeneration(item: Generation) {
  prompt.value = item.prompt
  if (item.parameters.aspectRatio) settings.value.ratio = item.parameters.aspectRatio
  if (item.parameters.quality) settings.value.quality = item.parameters.quality
  if (item.parameters.outputFormat) settings.value.format = item.parameters.outputFormat
  if (item.parameters.durationSeconds) settings.value.duration = item.parameters.durationSeconds
  if (item.parameters.responseLength) settings.value.responseLength = item.parameters.responseLength
  if (item.outputAssetId) {
    void api.getAsset(item.outputAssetId).then(asset => { sourceAssets.value = [asset] }).catch(() => undefined)
  }
  if (item.maskAssetId) {
    void api.getAsset(item.maskAssetId).then(asset => { maskAsset.value = asset }).catch(() => undefined)
  } else {
    maskAsset.value = null
  }
  selectedGeneration.value = null
  feedback.value = t('create.studio.reused')
}

function continueChat(item: Generation) {
  if (item.mode !== 'chat' || item.status !== 'succeeded') return
  activeChatGenerationId.value = item.id
  chatSelectionReady.value = true
  prompt.value = ''
  selectedGeneration.value = null
  view.value = 'guide'
}

function resetModeSettings(mode: CreationMode) {
  settings.value = defaultModeSettings(mode)
}

const { pause, resume } = useIntervalFn(async () => {
  if (!running.value) {
    pause()
    return
  }
  await loadGenerations(true)
  if (!running.value) await loadBilling()
}, 1400, { immediate: false })

onMounted(async () => {
  await session.ensure()
  await loadCapabilities()
  await Promise.all([loadGenerations(), loadAssets(), loadSource(), loadBilling()])
  if (running.value) resume()
})

watch([prompt, view, settings], () => {
  draftStore.save(props.mode, { prompt: prompt.value, view: view.value, settings: settings.value })
}, { deep: true, flush: 'sync' })

watch(activeCapability, (capability) => {
  if (!capability) return
  if (capability.durationSeconds.length && !capability.durationSeconds.includes(settings.value.duration as 5 | 10 | 30 | 60)) {
    settings.value.duration = capability.durationSeconds[0]
  }
  if (capability.outputFormats.length && !capability.outputFormats.includes(settings.value.format)) {
    settings.value.format = capability.outputFormats[0] as CreationOutputSettings['format']
  }
  if (capability.aspectRatios.length && !capability.aspectRatios.includes(settings.value.ratio)) {
    settings.value.ratio = capability.aspectRatios[0] as CreationOutputSettings['ratio']
  }
  if (capability.qualities.length && !capability.qualities.includes(settings.value.quality)) {
    settings.value.quality = capability.qualities[0] as CreationOutputSettings['quality']
  }
}, { immediate: true })

watch(() => [route.query.sourceWorkId, route.query.taskId, route.query.sourceAssetId].join('|'), async () => {
  await loadSource()
})

watch(() => props.mode, async (mode, previousMode) => {
  draftStore.save(previousMode, { prompt: prompt.value, view: view.value, settings: settings.value })
  pause()
  const restored = draftStore.restore(mode)
  prompt.value = restored?.prompt || ''
  view.value = restored?.view || (mode === 'chat' ? 'guide' : 'gallery')
  if (restored) settings.value = restored.settings
  else resetModeSettings(mode)
  sourceAssets.value = sourceAssets.value.filter(asset => referenceKinds.value.includes(asset.kind))
  if (mode !== 'image') maskAsset.value = null
  activeChatGenerationId.value = null
  chatSelectionReady.value = false
  selectedGeneration.value = null
  error.value = ''
  feedback.value = ''
  await Promise.all([loadGenerations(), loadAssets(), loadSource()])
  if (running.value) resume()
})
</script>

<template>
  <section
    class="creation-studio"
    :class="{ 'is-guest': session.initialized && !session.user, 'is-light': preferences.resolvedTheme === 'light' }"
    :data-mode="mode"
  >
    <div class="studio-scroll">
      <header class="studio-topbar studio-unified-header">
        <div class="studio-identity">
          <span><Sparkles :size="18" /></span>
          <div><h1>{{ t('create.studio.unifiedTitle') }}</h1><p>{{ t('create.studio.unifiedSummary') }}</p></div>
        </div>
        <div class="studio-toolbar-actions">
          <RouterLink v-if="billing" class="studio-balance" to="/workspace/billing" :aria-label="t('workspace.viewStatement')">
            <WalletCards :size="15" /><span>{{ t('workspace.availableCredits') }}</span><strong>{{ formatCurrency(billing.account.availableCents, billing.account.currency, locale) }}</strong>
          </RouterLink>
          <RouterLink class="icon-button" to="/workspace/generations" :aria-label="t('create.recentGenerations')" :title="t('create.recentGenerations')">
            <History :size="17" />
          </RouterLink>
        </div>
      </header>

      <div v-if="capabilityUnavailable" class="studio-notice error" role="status">
        <AlertCircle :size="16" /><span>{{ t('create.modeUnavailable') }}</span>
      </div>

      <AuthRequiredState
        v-if="session.initialized && !session.user"
        class="studio-auth"
        :title="t('authRequired.createTitle')"
        :summary="t('authRequired.createSummary')"
        :return-to="route.fullPath"
      />

      <div v-if="error" class="studio-notice error" role="alert">
        <AlertCircle :size="16" /><span>{{ error }}</span><button type="button" :aria-label="t('actions.close')" @click="error = ''">
          <X :size="15" />
        </button>
      </div>
      <div v-if="feedback" class="studio-notice success" role="status">
        <Check :size="16" /><span>{{ feedback }}</span><button type="button" :aria-label="t('actions.close')" @click="feedback = ''">
          <X :size="15" />
        </button>
      </div>

      <section
        v-if="!session.initialized || session.user"
        class="studio-conversation"
        :aria-label="t('create.studio.conversationLabel')"
      >
        <div v-if="loading" class="conversation-skeleton" aria-live="polite">
          <span></span><span></span><span></span>
        </div>
        <div v-else-if="!conversationGenerations.length" class="conversation-welcome">
          <span><Sparkles :size="24" /></span>
          <h2>{{ t('create.studio.welcomeTitle') }}</h2>
          <p>{{ t('create.studio.welcomeSummary') }}</p>
        </div>
        <div v-else class="conversation-feed">
          <article v-for="item in conversationGenerations" :key="item.id" class="studio-task conversation-turn" :data-status="item.status">
            <div class="conversation-user">
              <div><span class="conversation-mode"><component :is="modes.find(option => option.id === item.mode)?.icon" :size="14" />{{ t(`create.modes.${item.mode}`) }}</span><time :datetime="item.createdAt">{{ generationDate(item.createdAt) }}</time></div>
              <p>{{ item.prompt }}</p>
            </div>
            <div class="conversation-assistant">
              <span class="assistant-mark">{{ t('create.studio.assistantMark') }}</span>
              <div class="conversation-result" @click="selectedGeneration = item">
                <header><strong>{{ t(`generation.status.${item.status}`) }}</strong><small>{{ item.modelName }}</small></header>
                <AssetMedia v-if="item.outputMediaUrl" :src="item.outputMediaUrl || ''" :kind="generationMediaKind(item)" :alt="item.prompt || ''" :text="item.outputText || ''" :width="960" :height="720" :controls="generationMediaKind(item) === 'video' || generationMediaKind(item) === 'audio'" />
                <div v-else class="conversation-progress">
                  <LoaderCircle v-if="['queued', 'running'].includes(item.status)" class="spin" :size="20" />
                  <AlertCircle v-else-if="item.status === 'failed'" :size="20" />
                  <component :is="modes.find(option => option.id === item.mode)?.icon" v-else :size="20" />
                  <span>{{ item.errorMessage || `${item.progress}%` }}</span>
                </div>
                <footer>
                  <span>{{ parameterSummary(item) }}</span>
                  <button type="button" :aria-label="item.isFavorite ? t('workspace.unfavoriteGeneration') : t('workspace.favoriteGeneration')" :title="item.isFavorite ? t('workspace.unfavoriteGeneration') : t('workspace.favoriteGeneration')" @click.stop="toggleFavorite(item)">
                    <Bookmark :size="15" :fill="item.isFavorite ? 'currentColor' : 'none'" />
                  </button>
                </footer>
              </div>
            </div>
          </article>
        </div>
      </section>
    </div>

    <form class="studio-composer" @submit.prevent="submit">
      <div v-if="sourceAssets.length || maskAsset || sourceWork || sourceTask" class="studio-context">
        <div v-for="asset in sourceAssets" :key="asset.id" class="studio-context-item">
          <AssetMedia :src="asset.mediaUrl" :kind="asset.kind" :alt="asset.title" :width="64" :height="64" :controls="false" />
          <span class="source-reference"><small>{{ t('create.studio.referenceImage') }}</small><strong>{{ asset.title }}</strong></span>
          <button type="button" :aria-label="t('actions.close')" @click="removeSourceAsset(asset.id)">
            <X :size="15" />
          </button>
        </div>
        <div v-if="maskAsset" class="studio-context-item mask-context-item">
          <AssetMedia :src="maskAsset.mediaUrl" :kind="maskAsset.kind" :alt="maskAsset.title" :width="64" :height="64" :controls="false" />
          <span class="source-reference"><small>{{ t('create.studio.mask') }}</small><strong>{{ maskAsset.title }}</strong></span>
          <button type="button" :aria-label="t('actions.close')" @click="maskAsset = null">
            <X :size="15" />
          </button>
        </div>
        <div v-if="!sourceAssets.length && (sourceWork || sourceTask)" class="studio-context-item">
          <BriefcaseBusiness v-if="sourceTask" :size="18" />
          <Sparkles v-else :size="18" />
          <span class="source-reference"><small>{{ t('create.studio.referenceImage') }}</small><strong>{{ sourceWork?.title || sourceTask?.title }}</strong></span>
          <button type="button" :aria-label="t('actions.close')" @click="sourceWork = null; sourceTask = null">
            <X :size="15" />
          </button>
        </div>
      </div>
      <textarea v-model="prompt" rows="2" maxlength="1800" :placeholder="t(`create.builder.modePlaceholder.${mode}`)" @keydown.enter.exact.prevent="canSubmit && submit()"></textarea>
      <div class="studio-composer-row">
        <div class="studio-composer-tools">
          <button ref="modeMenuTrigger" type="button" :class="{ active: modeMenuOpen }" :aria-label="t('create.studio.chooseCreationType')" :title="t('create.studio.chooseCreationType')" aria-haspopup="menu" :aria-expanded="modeMenuOpen" @click="toggleModeMenu">
            <Plus :size="18" />
          </button>
          <span v-if="mode !== 'chat'" class="creation-mode-chip"><component :is="modeIcon" :size="15" />{{ t(`create.modes.${mode}`) }}<button type="button" :aria-label="t('create.studio.removeCreationType')" @click="selectCreationMode('chat')"><X :size="12" /></button></span>
          <div class="studio-settings-control">
            <button ref="controlsTrigger" class="studio-settings-button" type="button" :class="{ active: controlsOpen }" :aria-label="t('create.studio.outputSettings')" :title="t('create.studio.outputSettings')" :aria-expanded="controlsOpen" @click="controlsOpen = !controlsOpen; modeMenuOpen = false; assetPickerOpen = false">
              <SlidersHorizontal :size="16" />
            </button>
            <Transition name="mode-menu">
              <section v-if="controlsOpen" ref="controlsPanel" class="studio-mode-menu output-controls" role="dialog" :aria-label="t('create.studio.outputSettings')">
                <label v-if="['image', 'video'].includes(mode)"><span>{{ t('create.studio.ratio') }}</span><select v-model="settings.ratio"><option v-for="ratio in ratioOptions" :key="ratio" :value="ratio">{{ ratio === 'auto' ? t('create.studio.autoRatio') : ratio }}</option></select></label>
                <label v-if="mode !== 'chat'"><span>{{ t('create.studio.quality') }}</span><select v-model="settings.quality"><option v-for="quality in qualityOptions" :key="quality" :value="quality">{{ t(`create.studio.qualities.${quality}`) }}</option></select></label>
                <label v-if="['video', 'music'].includes(mode)"><span>{{ t('create.studio.duration') }}</span><select v-model.number="settings.duration"><option v-for="duration in durationOptions" :key="duration" :value="duration">{{ t('create.studio.durationValue', { value: duration }) }}</option></select></label>
                <label v-if="mode === 'chat'"><span>{{ t('create.studio.responseLength') }}</span><select v-model="settings.responseLength"><option value="short">{{ t('create.studio.responseLengths.short') }}</option><option value="balanced">{{ t('create.studio.responseLengths.balanced') }}</option><option value="detailed">{{ t('create.studio.responseLengths.detailed') }}</option></select></label>
                <label v-if="capabilityFormatOptions.length > 1 || mode === 'chat'"><span>{{ t('create.studio.format') }}</span><select v-model="settings.format"><option v-for="item in capabilityFormatOptions" :key="item" :value="item">{{ item.toUpperCase() }}</option></select></label>
                <label v-if="mode !== 'chat'"><span>{{ t('create.studio.count') }}</span><input v-model.number="settings.count" type="number" min="1" max="4" /></label>
              </section>
            </Transition>
          </div>
        </div>
        <button class="studio-submit" type="submit" :disabled="!canSubmit" :aria-label="submitting ? t('actions.generating') : generateLabel">
          <LoaderCircle v-if="submitting" class="spin" :size="17" /><Send v-else :size="17" /><span>{{ submitting ? t('actions.generating') : generateLabel }}</span>
        </button>
      </div>

      <Transition name="mode-menu">
        <section v-if="modeMenuOpen" ref="modeMenu" class="studio-mode-menu" role="menu" :aria-label="t('create.studio.chooseCreationType')">
          <div class="mode-menu-section">
            <button type="button" role="menuitem" class="mode-menu-row" @click="openAssetPicker('references')">
              <span class="mode-menu-icon"><FileText :size="21" /></span><span class="mode-menu-label">{{ t('create.studio.menuItems.file') }}</span>
            </button>
            <button type="button" class="mode-menu-row" @click="openAssetPicker('references')">
              <span class="mode-menu-icon"><Cloud :size="21" /></span><span class="mode-menu-label">{{ t('create.studio.menuItems.library') }}</span>
            </button>
            <button type="button" class="mode-menu-row" @click="openAssetPicker('references')">
              <span class="mode-menu-icon"><Images :size="21" /></span><span class="mode-menu-label">{{ t('create.studio.menuItems.album') }}</span>
            </button>
            <button type="button" class="mode-menu-row" @click="router.push('/market/demands')">
              <span class="mode-menu-icon"><FolderOpen :size="21" /></span><span class="mode-menu-label">{{ t('create.studio.menuItems.taskContext') }}</span>
            </button>
          </div>
          <div class="mode-menu-divider" aria-hidden="true"></div>
          <div class="mode-menu-section">
            <button type="button" class="mode-menu-row" @click="openAssetPicker('references')">
              <span class="mode-menu-icon"><Share2 :size="21" /></span><span class="mode-menu-label">{{ t('create.studio.menuItems.referenceWindow') }}</span>
            </button>
            <button v-if="mode === 'image' && supportsMask" type="button" class="mode-menu-row" @click="openAssetPicker('mask')">
              <span class="mode-menu-icon"><Eraser :size="21" /></span><span class="mode-menu-label">{{ t('create.studio.menuItems.mask') }}</span>
            </button>
            <button v-else type="button" class="mode-menu-row" @click="openAssetPicker('references')">
              <span class="mode-menu-icon"><Paperclip :size="21" /></span><span class="mode-menu-label">{{ t('create.studio.menuItems.reference') }}</span>
            </button>
          </div>
          <div class="mode-menu-divider" aria-hidden="true"></div>
          <div class="mode-menu-section mode-menu-generation">
            <button v-for="item in modes" :key="item.id" type="button" role="menuitem" class="mode-menu-row" :class="{ active: mode === item.id }" @click="selectCreationMode(item.id)">
              <span class="mode-menu-icon"><component :is="item.icon" :size="21" /></span><span class="mode-menu-label">{{ t(`create.studio.menuItems.${item.id}`) }}</span><Check v-if="mode === item.id" class="mode-menu-check" :size="18" />
            </button>
            <button type="button" role="menuitem" class="mode-menu-row mode-menu-disabled" disabled :title="t('create.studio.menuItems.canvasSoon')">
              <span class="mode-menu-icon"><SquarePlus :size="21" /></span><span class="mode-menu-label">{{ t('create.studio.menuItems.canvas') }}</span>
            </button>
          </div>
        </section>
      </Transition>

      <section v-if="assetPickerOpen" class="studio-popover reference-picker">
        <header>
          <div><strong>{{ t(referencePickerMode === 'mask' ? 'create.studio.maskAsset' : 'create.studio.references') }}</strong><small>{{ t(referencePickerMode === 'mask' ? 'create.studio.maskSummary' : 'create.studio.referenceSummary') }}</small></div><button type="button" :aria-label="t('actions.close')" @click="assetPickerOpen = false">
            <X :size="15" />
          </button>
        </header>
        <div v-if="mode === 'image' && supportsMask" class="reference-mode-switch" role="group" :aria-label="t('create.studio.referenceType')">
          <button type="button" :class="{ active: referencePickerMode === 'references' }" @click="referencePickerMode = 'references'">
            <Paperclip :size="14" />{{ t('create.studio.references') }}
          </button>
          <button type="button" :class="{ active: referencePickerMode === 'mask' }" @click="referencePickerMode = 'mask'">
            <Eraser :size="14" />{{ t('create.studio.mask') }}
          </button>
        </div>
        <div class="reference-actions">
          <button type="button" :disabled="uploadLoading" @click="fileInput?.click()">
            <LoaderCircle v-if="uploadLoading" class="spin" :size="16" /><Upload v-else :size="16" />{{ t('create.studio.uploadReference') }}
          </button>
          <input ref="fileInput" class="sr-only" type="file" :accept="referenceAccept" @change="uploadReference" />
        </div>
        <div v-if="assetsLoading" class="reference-loading">
          <LoaderCircle class="spin" :size="18" />{{ t('status.loadingAssets') }}
        </div>
        <div v-else-if="assets.length" class="reference-list">
          <button v-for="asset in assets" :key="asset.id" type="button" :class="{ selected: referencePickerMode === 'mask' ? maskAsset?.id === asset.id : sourceAssets.some(item => item.id === asset.id) }" @click="chooseAsset(asset)">
            <AssetMedia :src="asset.mediaUrl" :kind="asset.kind" :alt="asset.title" :width="80" :height="80" :controls="false" /><span><strong>{{ asset.title }}</strong><small>{{ assetMeta(asset) }}</small></span><Check v-if="referencePickerMode === 'mask' ? maskAsset?.id === asset.id : sourceAssets.some(item => item.id === asset.id)" :size="14" />
          </button>
        </div>
        <p v-else>
          {{ t('create.studio.noReferences') }}
        </p>
      </section>
    </form>

    <div v-if="selectedGeneration" class="studio-detail-backdrop" @mousedown.self="selectedGeneration = null">
      <aside class="studio-detail" aria-modal="true" role="dialog" :aria-label="t('create.studio.detailTitle')">
        <header>
          <div><span>{{ t(`generation.status.${selectedGeneration.status}`) }}</span><h2>{{ t('create.studio.detailTitle') }}</h2></div><button class="icon-button" type="button" :aria-label="t('actions.close')" @click="selectedGeneration = null">
            <X :size="17" />
          </button>
        </header>
        <div class="studio-detail-media">
          <AssetMedia v-if="selectedGeneration.outputMediaUrl" :src="selectedGeneration.outputMediaUrl || ''" :kind="generationMediaKind(selectedGeneration)" :alt="selectedGeneration.prompt || ''" :text="selectedGeneration.outputText || ''" :width="1200" :height="1200" :controls="generationMediaKind(selectedGeneration) === 'video' || generationMediaKind(selectedGeneration) === 'audio'" /><div v-else>
            <LoaderCircle v-if="['queued', 'running'].includes(selectedGeneration.status)" class="spin" :size="24" /><component :is="modes.find(item => item.id === selectedGeneration?.mode)?.icon" v-else :size="24" /><strong>{{ selectedGeneration.progress }}%</strong>
          </div>
        </div>
        <p>{{ selectedGeneration.prompt }}</p>
        <dl><div><dt>{{ t('create.modelLabel') }}</dt><dd>{{ selectedGeneration.modelName }}</dd></div><div><dt>{{ t('create.studio.outputSettings') }}</dt><dd>{{ parameterSummary(selectedGeneration) }}</dd></div><div><dt>{{ t('workspace.cost') }}</dt><dd>{{ generationCost(selectedGeneration) }}</dd></div><div><dt>{{ t('workspace.created') }}</dt><dd>{{ generationDate(selectedGeneration.createdAt) }}</dd></div></dl>
        <p v-if="selectedGeneration.errorMessage" class="studio-detail-error">
          <AlertCircle :size="15" />{{ selectedGeneration.errorMessage }}
        </p>
        <footer>
          <button type="button" :disabled="actionLoading === `${selectedGeneration.id}:favorite`" @click="toggleFavorite(selectedGeneration)">
            <Bookmark :size="16" :fill="selectedGeneration.isFavorite ? 'currentColor' : 'none'" />{{ selectedGeneration.isFavorite ? t('workspace.unfavoriteGeneration') : t('workspace.favoriteGeneration') }}
          </button>
          <button v-if="selectedGeneration.mode === 'chat' && selectedGeneration.status === 'succeeded'" type="button" @click="continueChat(selectedGeneration)">
            <MessageSquareText :size="16" />{{ t('create.studio.continueChat') }}
          </button>
          <button v-if="selectedGeneration.actions.canReuse" type="button" @click="reuseGeneration(selectedGeneration)">
            <RotateCcw :size="16" />{{ t('actions.remix') }}
          </button>
          <a v-if="selectedGeneration.actions.canDownload && selectedGeneration.actions.downloadPath" :href="selectedGeneration.actions.downloadPath"><Download :size="16" />{{ t('actions.download') }}</a>
          <RouterLink v-if="selectedGeneration.outputAssetId" :to="`/workspace/assets/${selectedGeneration.outputAssetId}`">
            <History :size="16" />{{ t('actions.inspectProvenance') }}
          </RouterLink>
          <button v-if="selectedGeneration.actions.canCancel" type="button" :disabled="actionLoading === `${selectedGeneration.id}:cancel`" @click="changeGeneration(selectedGeneration, 'cancel')">
            <Ban :size="16" />{{ t('actions.cancel') }}
          </button>
          <button v-if="selectedGeneration.actions.canRetry" type="button" :disabled="actionLoading === `${selectedGeneration.id}:retry`" @click="changeGeneration(selectedGeneration, 'retry')">
            <RefreshCw :size="16" />{{ t('actions.retry') }}
          </button>
          <RouterLink v-if="selectedGeneration.outputAssetId" class="primary" :to="{ path: '/publish', query: { assetId: selectedGeneration.outputAssetId, prompt: selectedGeneration.prompt } }">
            <Sparkles :size="16" />{{ t('actions.publishWork') }}
          </RouterLink>
          <RouterLink v-if="sourceTask" class="primary" :to="`/market/demands/${sourceTask.id}`">
            <BriefcaseBusiness :size="16" />{{ t('tasks.submitDelivery') }}
          </RouterLink>
        </footer>
      </aside>
    </div>
  </section>
</template>

<style scoped lang="scss">
.creation-studio {
  position: relative;
  height: calc(100dvh - 96px);
  min-height: 620px;
  overflow: hidden;
  background: var(--canvas);
}

.studio-scroll {
  height: 100%;
  overflow: auto;
  padding: 16px 26px 190px;
  overscroll-behavior: contain;
}

.studio-topbar {
  position: sticky;
  top: 0;
  z-index: 8;
  display: grid;
  grid-template-columns: auto auto minmax(180px, 1fr) auto;
  gap: 10px;
  align-items: center;
  max-width: 1420px;
  margin: 0 auto 20px;
  padding: 9px;
  border: 1px solid color-mix(in srgb, var(--border) 82%, transparent);
  border-radius: 12px;
  background: color-mix(in srgb, var(--surface) 88%, transparent);
  box-shadow: var(--shadow-xs);
  backdrop-filter: blur(18px);
}

.studio-modes {
  display: flex;
  align-items: center;
  gap: 2px;

  a {
    min-height: 36px;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 0 9px;
    border-radius: 8px;
    color: var(--text-secondary);
    font-size: 11px;
    font-weight: 600;
    text-decoration: none;
  }

  a:hover { background: var(--surface-muted); color: var(--text); }
  a.active { background: var(--accent-soft); color: var(--accent-readable); }
}

.studio-view-switch {
  display: flex;
  gap: 3px;
  padding: 3px;
  border-radius: 9px;
  background: var(--surface-muted);

  button {
    min-height: 34px;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 0 10px;
    border-radius: 7px;
    color: var(--text);
    font-size: 12px;
    font-weight: 600;
  }

  button.active { background: var(--surface); color: var(--text); box-shadow: var(--shadow-xs); }
}

.studio-search {
  min-width: 0;
  height: 38px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 12px;
  border: 1px solid var(--border);
  border-radius: 9px;
  background: var(--surface);
  color: var(--text-tertiary);

  input { width: 100%; min-width: 0; border: 0; outline: 0; background: transparent; }
  &:focus-within { border-color: var(--focus); box-shadow: 0 0 0 3px color-mix(in srgb, var(--focus) 14%, transparent); }
}

.studio-toolbar-actions { display: flex; align-items: center; gap: 6px; }
.studio-balance { min-height: 38px; display: inline-flex; align-items: center; gap: 6px; padding: 0 10px; border-radius: 8px; color: var(--text-secondary); font-size: 10px; text-decoration: none; }
.studio-balance:hover { background: var(--surface-muted); color: var(--text); }
.studio-balance strong { color: var(--text); font-family: var(--font-mono); font-size: 10px; }
.studio-filter {
  position: relative;
  height: 38px;
  display: flex;
  align-items: center;
  border: 1px solid var(--border);
  border-radius: 9px;
  background: var(--surface);
  color: var(--text-secondary);

  select { height: 100%; border: 0; outline: 0; appearance: none; background: transparent; padding: 0 30px 0 10px; font-size: 12px; }
  svg { position: absolute; right: 9px; pointer-events: none; }
}

.studio-auth, .studio-notice, .studio-gallery, .studio-guide { width: min(100%, 1420px); margin-inline: auto; }
.studio-heading { width: min(100%, 1420px); display: flex; justify-content: space-between; gap: 20px; align-items: baseline; margin: 0 auto 18px; }
.studio-heading > div { display: flex; align-items: center; gap: 8px; }
.studio-heading h1 { margin: 0; font-size: 19px; font-weight: 650; }
.studio-heading p { max-width: 680px; margin: 0; color: var(--text-secondary); font-size: 11px; text-align: right; }
.studio-notice { min-height: 40px; display: grid; grid-template-columns: 18px 1fr 28px; gap: 8px; align-items: center; margin-bottom: 10px; padding: 7px 9px 7px 12px; border-radius: 9px; font-size: 12px; }
.studio-notice.error { background: color-mix(in srgb, var(--danger) 10%, var(--surface)); color: var(--danger); }
.studio-notice.success { background: color-mix(in srgb, var(--success) 10%, var(--surface)); color: var(--success); }
.studio-notice button { width: 28px; height: 28px; display: grid; place-items: center; border-radius: 50%; color: currentColor; }

.studio-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 18px 12px; }
.studio-skeleton span { aspect-ratio: 1 / 1; border-radius: 10px; background: var(--surface-muted); animation: studio-pulse 1.1s ease-in-out infinite alternate; }
@keyframes studio-pulse { to { opacity: .52; } }

.studio-task {
  min-width: 0;
  overflow: hidden;
  border-radius: 10px;
  background: var(--surface);
  box-shadow: var(--shadow-xs);
  cursor: pointer;
  transition: transform 150ms ease, box-shadow 150ms ease;
  &:hover { transform: translateY(-2px); box-shadow: var(--shadow-sm); }
}

.studio-task-media { position: relative; aspect-ratio: 1 / 1; overflow: hidden; background: var(--surface-muted); }
.studio-task-media :deep(.asset-renderer), .studio-task-media :deep(img) { width: 100%; height: 100%; object-fit: cover; }
.studio-task-progress { height: 100%; display: grid; place-content: center; justify-items: center; gap: 7px; color: var(--text-tertiary); }
.studio-task-progress strong { color: var(--text-secondary); font-size: 12px; }
.studio-task-progress span { font-family: var(--font-mono); font-size: 11px; }
.studio-task[data-status='failed'] .studio-task-progress { color: var(--danger); }
.studio-task-status { position: absolute; top: 9px; left: 9px; min-height: 26px; display: inline-flex; align-items: center; gap: 6px; padding: 0 9px; border-radius: 999px; background: color-mix(in srgb, var(--surface) 86%, transparent); color: var(--text-secondary); backdrop-filter: blur(10px); font-size: 10px; font-weight: 650; }
.studio-task-status i { width: 6px; height: 6px; border-radius: 50%; background: var(--warning); }
.studio-task-favorite { position: absolute; top: 9px; right: 9px; width: 28px; height: 28px; display: grid; place-items: center; border-radius: 50%; background: color-mix(in srgb, var(--surface) 86%, transparent); color: var(--text-secondary); backdrop-filter: blur(10px); }
.studio-task-favorite:hover { color: var(--accent-readable); }
.studio-task[data-status='succeeded'] .studio-task-status i { background: var(--success); }
.studio-task[data-status='failed'] .studio-task-status i { background: var(--danger); }
.studio-task-copy { padding: 11px 12px 12px; }
.studio-task-copy p { display: -webkit-box; min-height: 36px; overflow: hidden; margin: 0 0 8px; color: var(--text); font-size: 12px; line-height: 1.5; -webkit-box-orient: vertical; -webkit-line-clamp: 2; }
.studio-task-copy > small { display: block; overflow: hidden; margin: -2px 0 7px; color: var(--text-secondary); font-size: 9px; text-overflow: ellipsis; white-space: nowrap; }
.studio-task-copy > div { display: flex; justify-content: space-between; gap: 8px; color: var(--text-tertiary); font-size: 10px; }
.studio-task-copy span, .studio-task-copy time { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

.creation-studio[data-mode='chat'] .studio-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
.creation-studio[data-mode='chat'] .studio-task-media { aspect-ratio: 16 / 7; }
.creation-studio[data-mode='chat'] .studio-task-media :deep(.asset-document) { align-content: start; justify-items: start; padding: 22px; text-align: left; }
.creation-studio[data-mode='chat'] .studio-task-media :deep(.asset-document svg) { display: none; }
.creation-studio[data-mode='chat'] .studio-task-media :deep(.asset-document pre) { max-height: 100%; overflow: hidden; white-space: pre-wrap; }
.creation-studio[data-mode='video'] .studio-grid { grid-template-columns: repeat(3, minmax(0, 1fr)); }
.creation-studio[data-mode='video'] .studio-task-media { aspect-ratio: 16 / 9; }
.creation-studio[data-mode='music'] .studio-grid { grid-template-columns: repeat(3, minmax(0, 1fr)); }
.creation-studio[data-mode='music'] .studio-task-media { aspect-ratio: 16 / 9; }
.creation-studio[data-mode='music'] .studio-task-media :deep(.asset-audio) { padding: 18px; }
.creation-studio[data-mode='music'] .studio-task-media :deep(audio) { width: 100%; }

.studio-empty { min-height: 420px; display: grid; place-content: center; justify-items: center; gap: 9px; color: var(--text-secondary); text-align: center; }
.studio-empty > span { width: 50px; height: 50px; display: grid; place-items: center; margin-bottom: 5px; border-radius: 14px; background: var(--accent-soft); color: var(--accent-readable); }
.studio-empty h2 { margin: 0; color: var(--text); font-size: 20px; }
.studio-empty p { max-width: 470px; margin: 0; font-size: 13px; line-height: 1.5; }

.studio-guide { display: grid; grid-template-columns: minmax(0, 1fr) minmax(280px, .72fr); grid-template-rows: auto 1fr; gap: 14px 26px; }
.studio-guide > header { grid-column: 1; display: flex; gap: 12px; align-items: flex-start; padding: 14px 0; }
.studio-guide > header > span { width: 38px; height: 38px; display: grid; place-items: center; flex: 0 0 auto; border-radius: 11px; background: var(--accent-soft); color: var(--accent-readable); }
.studio-new-chat { min-height: 34px; display: inline-flex; align-items: center; gap: 6px; margin-left: auto; padding: 0 11px; border-radius: 999px; background: var(--surface-muted); color: var(--text-secondary); font-size: 11px; font-weight: 650; }
.studio-new-chat:hover { color: var(--text); }
.studio-guide h1 { margin: 0 0 4px; font-size: 20px; }
.studio-guide header p { margin: 0; color: var(--text-secondary); font-size: 13px; }
.studio-guide-messages { grid-column: 1; display: grid; align-content: start; gap: 15px; }
.studio-guide-messages article { display: grid; grid-template-columns: 32px minmax(0, 1fr); gap: 10px; align-items: start; }
.studio-guide-messages article > span { width: 32px; height: 32px; display: grid; place-items: center; border-radius: 9px; background: var(--surface-muted); color: var(--text-secondary); font-size: 11px; font-weight: 700; }
.studio-guide-messages article[data-role='user'] { grid-template-columns: minmax(0, 1fr) 32px; }
.studio-guide-messages article[data-role='user'] > span { grid-column: 2; }
.studio-guide-messages article[data-role='user'] > p { grid-column: 1; grid-row: 1; justify-self: end; background: var(--accent-soft); }
.studio-guide-messages p { width: fit-content; max-width: 720px; margin: 0; padding: 10px 12px; border-radius: 10px; background: var(--surface); color: var(--text-secondary); font-size: 13px; line-height: 1.55; }
.studio-guide-latest { grid-column: 2; grid-row: 1 / 3; min-height: 460px; overflow: hidden; border-radius: 12px; background: var(--surface-muted); }
.studio-guide-latest :deep(.asset-renderer), .studio-guide-latest :deep(img) { width: 100%; height: 100%; object-fit: cover; }
.studio-guide-latest > div { height: 100%; display: grid; place-content: center; justify-items: center; gap: 8px; color: var(--text-tertiary); }
.creation-studio[data-mode='chat'] .studio-guide { grid-template-columns: minmax(0, 900px); justify-content: center; }
.creation-studio[data-mode='chat'] .studio-guide > header,
.creation-studio[data-mode='chat'] .studio-guide-messages { grid-column: 1; }
.creation-studio[data-mode='chat'] .studio-guide-messages { padding-bottom: 24px; }

.studio-composer {
  position: absolute;
  z-index: 30;
  right: 46px;
  bottom: 16px;
  left: 26px;
  max-width: 1180px;
  margin-inline: auto;
  padding: 12px;
  border: 1px solid color-mix(in srgb, var(--border) 78%, transparent);
  border-radius: 16px;
  background: color-mix(in srgb, var(--surface) 88%, transparent);
  box-shadow: 0 14px 44px rgb(25 31 45 / 13%);
  backdrop-filter: blur(22px) saturate(1.2);
}

.studio-composer textarea { width: 100%; min-height: 56px; max-height: 150px; resize: none; border: 0; outline: 0; background: transparent; padding: 5px 7px 8px; color: var(--text); font-size: 14px; line-height: 1.5; }
.studio-composer-row { display: flex; justify-content: space-between; align-items: center; gap: 10px; }
.studio-composer-tools { min-width: 0; display: flex; align-items: center; gap: 5px; }
.studio-composer-tools > button { width: 36px; height: 36px; display: grid; place-items: center; flex: 0 0 auto; border-radius: 50%; color: var(--text-secondary); }
.studio-composer-tools > button:hover, .studio-composer-tools > button.active { background: var(--surface-muted); color: var(--accent-readable); }
.studio-submit { min-height: 38px; display: inline-flex; align-items: center; gap: 7px; flex: 0 0 auto; padding: 0 14px; border-radius: 999px; background: var(--text); color: var(--surface); font-size: 12px; font-weight: 650; }
.studio-submit:hover:not(:disabled) { background: color-mix(in srgb, var(--text) 84%, var(--accent)); transform: translateY(-1px); }
.studio-submit:disabled { opacity: .38; cursor: not-allowed; }

.studio-context { display: flex; gap: 6px; overflow-x: auto; margin-bottom: 6px; padding: 5px; border-radius: 10px; background: var(--surface-muted); }
.studio-context-item { min-width: 190px; max-width: 250px; display: grid; grid-template-columns: 42px minmax(0, 1fr) 28px; gap: 9px; align-items: center; flex: 0 0 auto; }
.mask-context-item { outline: 1px solid color-mix(in srgb, var(--accent) 38%, transparent); outline-offset: -1px; border-radius: 8px; }
.studio-context :deep(.asset-renderer), .studio-context :deep(img) { width: 42px; height: 42px; object-fit: cover; border-radius: 8px; }
.studio-context-item > svg:first-child { margin: 12px; }
.studio-context-item > span { min-width: 0; display: grid; gap: 2px; }
.studio-context small { color: var(--text-tertiary); font-size: 9px; }
.studio-context strong { overflow: hidden; font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
.studio-context-item > button { width: 28px; height: 28px; display: grid; place-items: center; border-radius: 50%; color: var(--text-tertiary); }

.studio-popover { position: absolute; right: 0; bottom: calc(100% + 8px); left: 0; padding: 12px; border: 1px solid var(--border); border-radius: 13px; background: color-mix(in srgb, var(--surface) 95%, transparent); box-shadow: var(--shadow-sm); backdrop-filter: blur(20px); }
.studio-popover > header { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; margin-bottom: 10px; }
.studio-popover > header div { display: grid; gap: 2px; }
.studio-popover > header strong { font-size: 12px; }
.studio-popover > header small, .studio-popover > p { color: var(--text-secondary); font-size: 10px; }
.studio-popover > header button { width: 28px; height: 28px; display: grid; place-items: center; border-radius: 50%; }
.reference-actions { margin-bottom: 10px; }
.reference-mode-switch { display: inline-flex; gap: 3px; margin-bottom: 10px; padding: 3px; border-radius: 9px; background: var(--surface-muted); }
.reference-mode-switch button { min-height: 30px; display: inline-flex; align-items: center; gap: 5px; padding: 0 9px; border-radius: 7px; color: var(--text-secondary); font-size: 10px; }
.reference-mode-switch button.active { background: var(--surface); color: var(--text); box-shadow: var(--shadow-xs); }
.reference-actions button { min-height: 34px; display: inline-flex; align-items: center; gap: 6px; padding: 0 10px; border-radius: 8px; background: var(--surface-muted); color: var(--text-secondary); font-size: 11px; font-weight: 600; }
.reference-list { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 7px; max-height: 220px; overflow: auto; }
.reference-list button { min-width: 0; display: grid; grid-template-columns: 42px minmax(0, 1fr) 18px; gap: 8px; align-items: center; padding: 5px; border-radius: 9px; color: var(--text); text-align: left; }
.reference-list button:hover { background: var(--surface-muted); }
.reference-list button.selected { background: var(--accent-soft); color: var(--accent-readable); }
.reference-list :deep(.asset-renderer), .reference-list :deep(img) { width: 42px; height: 42px; object-fit: cover; border-radius: 7px; }
.reference-list span { min-width: 0; display: grid; gap: 3px; }
.reference-list strong, .reference-list small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.reference-list strong { font-size: 10px; }
.reference-list small { color: var(--text-tertiary); font-size: 9px; }
.reference-loading { min-height: 90px; display: grid; place-content: center; justify-items: center; gap: 7px; color: var(--text-secondary); font-size: 11px; }
.output-controls { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 8px; }
.output-controls label { min-width: 0; display: grid; gap: 5px; color: var(--text-secondary); font-size: 10px; }
.output-controls select, .output-controls input { width: 100%; min-width: 0; height: 36px; border: 1px solid var(--border); border-radius: 8px; outline: 0; background: var(--surface); padding: 0 9px; color: var(--text); }

.studio-detail-backdrop { position: fixed; z-index: 90; inset: 0; display: flex; justify-content: flex-end; background: rgb(10 13 19 / 38%); backdrop-filter: blur(3px); }
.studio-detail { width: min(480px, 100%); height: 100%; display: grid; grid-template-rows: auto minmax(260px, .9fr) auto auto auto; gap: 14px; overflow-y: auto; padding: 18px; background: var(--surface); box-shadow: -18px 0 50px rgb(10 13 19 / 18%); }
.studio-detail > header { display: flex; justify-content: space-between; align-items: flex-start; }
.studio-detail > header span { color: var(--accent-readable); font-size: 10px; font-weight: 700; }
.studio-detail h2 { margin: 4px 0 0; font-size: 18px; }
.studio-detail-media { min-height: 300px; display: grid; place-items: center; overflow: hidden; border-radius: 10px; background: var(--surface-muted); }
.studio-detail-media :deep(.asset-renderer), .studio-detail-media :deep(img) { width: 100%; height: 100%; object-fit: contain; }
.studio-detail-media > div { display: grid; place-items: center; gap: 8px; color: var(--text-tertiary); }
.studio-detail > p { margin: 0; color: var(--text-secondary); font-size: 12px; line-height: 1.55; white-space: pre-wrap; }
.studio-detail dl { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 7px; margin: 0; }
.studio-detail dl div { min-width: 0; display: grid; gap: 3px; padding: 9px; border-radius: 8px; background: var(--surface-muted); }
.studio-detail dt { color: var(--text-tertiary); font-size: 9px; }
.studio-detail dd { overflow: hidden; margin: 0; font-size: 10px; text-overflow: ellipsis; white-space: nowrap; }
.studio-detail-error { display: flex; gap: 7px; color: var(--danger) !important; }
.studio-detail footer { display: flex; flex-wrap: wrap; gap: 7px; align-self: end; }
.studio-detail footer :is(button, a) { min-height: 36px; display: inline-flex; align-items: center; gap: 6px; padding: 0 11px; border-radius: 8px; background: var(--surface-muted); color: var(--text-secondary); font-size: 11px; font-weight: 600; }
.studio-detail footer .primary { background: var(--accent); color: var(--accent-contrast); }

@media (max-width: 1100px) {
  .studio-topbar { grid-template-columns: auto minmax(150px, 1fr) auto; }
  .studio-view-switch { display: none; }
  .studio-grid { grid-template-columns: repeat(3, minmax(0, 1fr)); }
  .creation-studio[data-mode='chat'] .studio-grid,
  .creation-studio[data-mode='video'] .studio-grid,
  .creation-studio[data-mode='music'] .studio-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .studio-guide { grid-template-columns: 1fr; }
  .studio-guide-latest { grid-column: 1; grid-row: auto; min-height: 340px; }
}

@media (max-width: 767px) {
  .creation-studio { height: calc(100dvh - 124px); min-height: 520px; }
  .studio-scroll { padding: 10px 12px 176px; }
  .studio-topbar { grid-template-columns: minmax(0, 1fr) auto; padding: 7px; }
  .studio-modes { min-width: 0; overflow-x: auto; }
  .studio-modes a { flex: 0 0 auto; }
  .studio-modes a { padding-inline: 7px; }
  .studio-view-switch { min-width: 0; }
  .studio-view-switch button { flex: 1; }
  .studio-search { grid-column: 1 / -1; grid-row: 2; }
  .studio-filter { display: none; }
  .studio-balance span { display: none; }
  .studio-heading { display: grid; gap: 4px; margin-bottom: 12px; }
  .studio-heading p { text-align: left; }
  .studio-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px 7px; }
  .creation-studio[data-mode='chat'] .studio-grid,
  .creation-studio[data-mode='video'] .studio-grid,
  .creation-studio[data-mode='music'] .studio-grid { grid-template-columns: 1fr; }
  .studio-task-copy { padding: 9px; }
  .studio-task-copy > div { display: grid; }
  .studio-composer { right: 10px; bottom: 10px; left: 10px; padding: 10px; border-radius: 14px; }
  .studio-submit span { display: none; }
  .studio-submit { width: 38px; padding: 0; justify-content: center; }
  .reference-list { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .output-controls { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .studio-guide-latest { min-height: 280px; }
  .studio-detail { padding-bottom: 78px; }
}

/* Gemini-inspired creation surface: the prompt is the primary action and the
   surrounding UI recedes until the user asks for history or settings. */
.creation-studio {
  --studio-bg: #0b0d11;
  --studio-panel: #11141a;
  --studio-panel-strong: #050608;
  --studio-border: rgb(255 255 255 / 11%);
  --studio-muted: #9ca3af;
  --studio-text: #f4f6f8;
  --studio-blue: #4f8cff;
  --studio-blue-soft: rgb(79 140 255 / 18%);
  color-scheme: dark;
  height: calc(100dvh - 96px);
  min-height: 620px;
  background:
    radial-gradient(circle at 50% 18%, rgb(37 54 84 / 18%), transparent 38%),
    var(--studio-bg);
  color: var(--studio-text);
}

.studio-scroll {
  padding: 10px 24px 208px;
  scrollbar-color: rgb(255 255 255 / 18%) transparent;
}

.studio-topbar {
  grid-template-columns: auto minmax(0, 1fr) auto;
  max-width: 1240px;
  margin-bottom: 22px;
  padding: 4px 0;
  border: 0;
  border-radius: 0;
  background: transparent;
  box-shadow: none;
  backdrop-filter: none;
}

.studio-modes {
  gap: 3px;
  padding: 4px;
  border: 1px solid var(--studio-border);
  border-radius: 999px;
  background: rgb(255 255 255 / 4%);

  a {
    min-height: 34px;
    padding: 0 12px;
    border-radius: 999px;
    color: var(--studio-muted);
    font-size: 11px;
    transition: color 160ms ease, background 160ms ease, transform 160ms ease;
  }

  a:hover { background: rgb(255 255 255 / 8%); color: var(--studio-text); transform: translateY(-1px); }
  a.active { background: rgb(255 255 255 / 14%); color: #fff; box-shadow: inset 0 0 0 1px rgb(255 255 255 / 8%); }
}

.studio-view-switch {
  justify-self: center;
  padding: 3px;
  border: 1px solid var(--studio-border);
  border-radius: 999px;
  background: rgb(255 255 255 / 3%);

  button {
    min-height: 30px;
    padding: 0 12px;
    border-radius: 999px;
    background: #171b23;
    color: #d8e0ec;
    font-size: 10px;
  }

  button.active { background: #2a3344; color: #fff; box-shadow: none; }
}

.studio-search {
  width: 188px;
  height: 34px;
  border: 1px solid var(--studio-border);
  border-radius: 999px;
  background: rgb(255 255 255 / 4%);
  color: var(--studio-muted);

  input { color: var(--studio-text); font-size: 11px; }
  input::placeholder { color: #707785; }
  &:focus-within { border-color: rgb(79 140 255 / 65%); box-shadow: 0 0 0 3px rgb(79 140 255 / 12%); }
}

.studio-toolbar-actions { gap: 3px; }
.studio-balance { min-height: 34px; color: var(--studio-muted); }
.studio-balance strong { color: var(--studio-text); }
.studio-balance:hover { background: rgb(255 255 255 / 7%); color: var(--studio-text); }
.studio-filter { height: 34px; border-color: var(--studio-border); background: rgb(255 255 255 / 4%); color: var(--studio-muted); }
.studio-filter select { color: var(--studio-text); font-size: 10px; }
.studio-toolbar-actions > .icon-button { color: var(--studio-muted); }
.studio-toolbar-actions > .icon-button:hover { background: rgb(255 255 255 / 8%); color: var(--studio-text); }

.studio-heading {
  width: min(100%, 980px);
  margin: 42px auto 12px;
  opacity: .9;
}
.studio-heading h1 { color: var(--studio-text); font-size: 16px; font-weight: 560; }
.studio-heading > div { gap: 7px; }
.studio-heading > div > svg { color: var(--studio-blue); }
.studio-heading p { color: #7f8795; font-size: 11px; }

.studio-auth, .studio-notice, .studio-gallery, .studio-guide { width: min(100%, 980px); }
.studio-notice { border: 1px solid var(--studio-border); background: var(--studio-panel); }
.studio-notice.error { color: #ff8e9c; }
.studio-notice.success { color: #7ce2b0; }

/* Guest and signed-in empty states are mutually exclusive. The guest prompt
   owns the available stage so it stays clear of the fixed composer. */
.creation-studio.is-guest .studio-scroll {
  display: flex;
  flex-direction: column;
}

.creation-studio.is-guest .studio-auth {
  min-height: 0;
  flex: 1 1 auto;
  place-content: center;
  justify-items: center;
  gap: 15px;
  padding: 30px 0 60px;
  text-align: center;
}

.creation-studio.is-guest .studio-auth :deep(.auth-required-icon) {
  width: 48px;
  height: 48px;
  border: 1px solid var(--studio-border);
  border-radius: 50%;
  background: var(--studio-blue-soft);
  color: #b7ceff;
}

.creation-studio.is-guest .studio-auth :deep(.auth-required-copy) {
  justify-items: center;
  gap: 8px;
}

.creation-studio.is-guest .studio-auth :deep(.auth-required-copy h2) {
  color: var(--studio-text);
  font-size: 23px;
  font-weight: 560;
}

.creation-studio.is-guest .studio-auth :deep(.auth-required-copy p) {
  max-width: 430px;
  color: #8993a3;
  font-size: 13px;
}

.creation-studio.is-guest .studio-auth :deep(.auth-required-actions) {
  justify-content: center;
  margin-top: 2px;
}

.studio-grid { grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 16px; }
.creation-studio[data-mode='chat'] .studio-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
.creation-studio[data-mode='video'] .studio-grid,
.creation-studio[data-mode='music'] .studio-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }

.studio-task {
  border: 1px solid var(--studio-border);
  border-radius: 16px;
  background: var(--studio-panel);
  box-shadow: none;
  transition: border-color 160ms ease, transform 160ms ease, background 160ms ease;
}
.studio-task:hover { border-color: rgb(255 255 255 / 22%); background: #151922; box-shadow: none; }
.studio-task-media { background: #090b0f; }
.studio-task-copy { background: var(--studio-panel); }
.studio-task-copy p { color: #e8ebef; }
.studio-task-copy > small { color: #858d9d; }
.studio-task-copy > div { color: #687181; }
.studio-task-status, .studio-task-favorite { background: rgb(5 6 8 / 78%); color: #c4cad4; }
.studio-task-favorite:hover { color: var(--studio-blue); }

.studio-empty {
  min-height: 430px;
  color: #8e97a6;
}
.studio-empty > span { width: 54px; height: 54px; border: 1px solid var(--studio-border); border-radius: 50%; background: rgb(255 255 255 / 5%); color: var(--studio-blue); }
.studio-empty h2 { color: var(--studio-text); font-size: 19px; font-weight: 560; }
.studio-empty p { color: #858e9d; }

.studio-guide { grid-template-columns: minmax(0, 1fr) minmax(280px, .72fr); }
.studio-guide > header { padding: 12px 0 18px; }
.studio-guide > header > span { width: 36px; height: 36px; border: 1px solid var(--studio-border); border-radius: 50%; background: rgb(255 255 255 / 5%); color: var(--studio-blue); }
.studio-guide h1 { color: var(--studio-text); font-size: 18px; font-weight: 560; }
.studio-guide header p { color: #858e9d; }
.studio-guide-messages article > span { border: 1px solid var(--studio-border); border-radius: 50%; background: rgb(255 255 255 / 5%); color: #b4bcc9; }
.studio-guide-messages p { border: 1px solid var(--studio-border); border-radius: 16px; background: var(--studio-panel); color: #d4d8df; }
.studio-guide-messages article[data-role='user'] > p { border-color: rgb(79 140 255 / 34%); background: rgb(79 140 255 / 14%); color: #eaf1ff; }
.studio-guide-latest { border: 1px solid var(--studio-border); border-radius: 16px; background: var(--studio-panel); }

.studio-composer {
  right: 24px;
  bottom: 18px;
  left: 24px;
  width: min(760px, calc(100% - 48px));
  max-width: none;
  padding: 10px 12px 9px;
  border: 1px solid rgb(255 255 255 / 16%);
  border-radius: 24px;
  background: var(--studio-panel-strong);
  box-shadow: 0 18px 60px rgb(0 0 0 / 42%), 0 0 0 1px rgb(255 255 255 / 3%);
  backdrop-filter: blur(24px) saturate(1.15);
}

.studio-composer textarea {
  min-height: 62px;
  padding: 8px 10px 6px;
  color: var(--studio-text);
  font-size: 14px;
  line-height: 1.55;
}
.studio-composer textarea::placeholder { color: #737b89; }
.studio-composer-row { gap: 8px; }
.studio-composer-tools { gap: 7px; }
.studio-composer-tools > button,
.studio-settings-button {
  width: 34px;
  height: 34px;
  padding: 0;
  border: 1px solid rgb(255 255 255 / 8%);
  background: rgb(255 255 255 / 5%);
  color: #9ca5b2;
}
.studio-composer-tools > button:hover,
.studio-composer-tools > button.active,
.studio-settings-button:hover,
.studio-settings-button.active { border-color: rgb(79 140 255 / 36%); background: var(--studio-blue-soft); color: #c8dcff; }
.studio-submit {
  width: 38px;
  min-height: 38px;
  justify-content: center;
  padding: 0;
  border-radius: 50%;
  background: var(--studio-blue);
  color: #fff;
  box-shadow: 0 5px 18px rgb(79 140 255 / 35%);
  transition: transform 160ms ease, background 160ms ease, opacity 160ms ease;
}
.studio-submit span { display: none; }
.studio-submit:hover:not(:disabled) { background: #6b9fff; transform: translateY(-1px); }
.studio-submit:disabled { background: #3a4456; color: #8993a3; opacity: .72; box-shadow: none; }

.studio-context {
  gap: 6px;
  margin: 0 2px 4px;
  padding: 0;
  background: transparent;
}
.studio-context-item {
  min-width: 0;
  max-width: 220px;
  grid-template-columns: 22px minmax(0, 1fr) 18px;
  gap: 6px;
  padding: 5px 8px 5px 5px;
  border: 1px solid rgb(79 140 255 / 38%);
  border-radius: 999px;
  background: var(--studio-blue-soft);
  color: #d8e6ff;
}
.mask-context-item { outline: 0; border-color: rgb(190 117 255 / 42%); background: rgb(156 93 225 / 16%); }
.studio-context :deep(.asset-renderer), .studio-context :deep(img) { width: 22px; height: 22px; border-radius: 50%; }
.studio-context-item > svg:first-child { margin: 2px; color: #9fbfff; }
.studio-context small { display: none; }
.studio-context strong { color: inherit; font-size: 10px; }
.studio-context-item > button { width: 18px; height: 18px; color: #a9c3f4; }
.studio-context-item > button:hover { background: rgb(255 255 255 / 10%); color: #fff; }

.studio-popover {
  border-color: var(--studio-border);
  border-radius: 18px;
  background: #12161e;
  box-shadow: 0 18px 50px rgb(0 0 0 / 45%);
}
.studio-popover > header strong { color: var(--studio-text); }
.studio-popover > header small, .studio-popover > p { color: #8992a2; }
.reference-mode-switch, .reference-actions button { background: rgb(255 255 255 / 7%); }
.reference-mode-switch button { color: #a5adba; }
.reference-mode-switch button.active { background: rgb(255 255 255 / 12%); color: #fff; box-shadow: none; }
.reference-actions button { color: #c1cad7; }
.reference-list button:hover { background: rgb(255 255 255 / 7%); }
.reference-list button.selected { background: var(--studio-blue-soft); color: #dce8ff; }
.output-controls select, .output-controls input { border-color: var(--studio-border); background: #090b0f; color: var(--studio-text); }

.studio-detail-backdrop { background: rgb(0 0 0 / 64%); }
.studio-detail { border-left: 1px solid var(--studio-border); background: #11141a; box-shadow: -18px 0 50px rgb(0 0 0 / 35%); }
.studio-detail-media, .studio-detail dl div { background: #090b0f; }
.studio-detail > p, .studio-detail dt { color: #8a93a2; }
.studio-detail dd { color: #e9edf3; }

/* The creation surface is one conversation. Mode selection lives inside the
   composer so the user starts with an outcome, not a navigation decision. */
.studio-unified-header { display: flex; align-items: center; justify-content: space-between; }
.studio-identity { display: flex; align-items: center; gap: 11px; }
.studio-identity > span { width: 34px; height: 34px; display: grid; place-items: center; border: 1px solid var(--studio-border); border-radius: 11px; background: var(--studio-blue-soft); color: #a9c5ff; }
.studio-identity h1 { margin: 0; color: var(--studio-text); font-size: 15px; font-weight: 600; }
.studio-identity p { margin: 3px 0 0; color: #7f8998; font-size: 10px; }
.studio-conversation {
  width: min(100%, 920px);
  min-height: clamp(300px, calc(100dvh - 390px), 500px);
  margin: clamp(30px, 6vh, 56px) auto 0;
}
.conversation-feed { display: grid; gap: 28px; }
.conversation-turn { display: grid; gap: 12px; }
.conversation-user { width: min(84%, 700px); margin-left: auto; }
.conversation-user > div { display: flex; align-items: center; justify-content: flex-end; gap: 10px; margin-bottom: 7px; color: #737d8e; font-size: 10px; }
.conversation-mode { display: inline-flex; align-items: center; gap: 5px; color: #a9c5ff; }
.conversation-user p { margin: 0; padding: 12px 15px; border: 1px solid rgb(79 140 255 / 28%); border-radius: 18px 18px 5px 18px; background: rgb(79 140 255 / 11%); color: #e9f0ff; font-size: 14px; line-height: 1.55; }
.conversation-assistant { display: grid; grid-template-columns: 28px minmax(0, 1fr); gap: 10px; align-items: start; }
.assistant-mark { width: 28px; height: 28px; display: grid; place-items: center; border: 1px solid rgb(255 255 255 / 16%); border-radius: 50%; background: rgb(255 255 255 / 7%); color: #c7d7ff; font-size: 11px; font-weight: 700; }
.conversation-result { min-width: 0; overflow: hidden; border: 1px solid var(--studio-border); border-radius: 17px; background: var(--studio-panel); cursor: pointer; transition: border-color 160ms ease, transform 160ms ease; }
.conversation-result:hover { border-color: rgb(255 255 255 / 25%); transform: translateY(-1px); }
.conversation-result > header { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 11px 14px 9px; color: #dbe5f8; font-size: 11px; }
.conversation-result > header small { color: #7f8998; font-size: 10px; }
.conversation-result :deep(.asset-renderer), .conversation-result :deep(img), .conversation-result :deep(video) { display: block; width: 100%; max-height: 480px; object-fit: contain; background: #080a0e; }
.conversation-result :deep(audio) { display: block; width: calc(100% - 28px); margin: 12px 14px; }
.conversation-progress { min-height: 150px; display: grid; place-items: center; align-content: center; gap: 9px; color: #98a5b8; font-size: 12px; }
.conversation-result > footer { display: flex; align-items: center; justify-content: space-between; gap: 10px; padding: 9px 14px 11px; color: #768194; font-size: 10px; }
.conversation-result > footer button { width: 27px; height: 27px; display: grid; place-items: center; border-radius: 50%; color: inherit; }
.conversation-result > footer button:hover { background: rgb(255 255 255 / 8%); color: #c3d5ff; }
.conversation-welcome { min-height: inherit; display: grid; place-content: center; justify-items: center; gap: 10px; text-align: center; }
.conversation-welcome > span { width: 54px; height: 54px; display: grid; place-items: center; border: 1px solid var(--studio-border); border-radius: 50%; background: var(--studio-blue-soft); color: #a9c5ff; }
.conversation-welcome h2 { margin: 4px 0 0; color: var(--studio-text); font-size: 22px; font-weight: 560; }
.conversation-welcome p { max-width: 460px; margin: 0; color: #8993a3; font-size: 13px; line-height: 1.55; }
.conversation-skeleton { display: grid; gap: 20px; }
.conversation-skeleton span { display: block; height: 100px; border-radius: 17px; background: linear-gradient(100deg, rgb(255 255 255 / 5%), rgb(255 255 255 / 10%), rgb(255 255 255 / 5%)); background-size: 200% 100%; animation: studio-shimmer 1.4s linear infinite; }
.conversation-skeleton span:first-child { width: 68%; margin-left: auto; height: 70px; }
.conversation-skeleton span:last-child { width: 82%; }
.creation-mode-chip { min-height: 34px; display: inline-flex; align-items: center; gap: 7px; padding: 0 8px 0 10px; border: 1px solid rgb(79 140 255 / 38%); border-radius: 999px; background: var(--studio-blue-soft); color: #d8e6ff; font-size: 11px; font-weight: 600; line-height: 1; white-space: nowrap; }
.creation-mode-chip button { width: 18px; min-width: 18px; height: 18px; min-height: 18px; display: inline-grid; place-items: center; flex: 0 0 18px; padding: 0; border: 0; border-radius: 50%; background: transparent; color: #a9c3f4; line-height: 0; cursor: pointer; }
.creation-mode-chip button:hover { background: rgb(255 255 255 / 11%); color: #fff; }
.studio-settings-control { position: relative; display: grid; place-items: center; flex: 0 0 auto; }
.studio-settings-button { display: grid; place-items: center; border-radius: 50%; }
.studio-mode-menu { position: absolute; right: auto; bottom: calc(100% + 8px); left: 0; z-index: 20; width: min(220px, calc(100vw - 32px)); max-height: min(320px, calc(100dvh - 250px)); overflow-y: auto; overscroll-behavior: contain; padding: 5px; border: 1px solid rgb(255 255 255 / 21%); border-radius: 22px; background: rgb(35 37 43 / 97%); box-shadow: 0 14px 34px rgb(0 0 0 / 42%), inset 0 1px 0 rgb(255 255 255 / 9%); backdrop-filter: blur(22px) saturate(1.08); scrollbar-width: none; -ms-overflow-style: none; }
.studio-mode-menu::-webkit-scrollbar { display: none; width: 0; height: 0; }
.output-controls.studio-mode-menu { right: auto; bottom: calc(100% + 12px); left: 0; width: min(220px, calc(100vw - 32px)); max-height: min(320px, calc(100dvh - 250px)); display: grid; grid-template-columns: 1fr; gap: 9px; padding: 12px; }
.output-controls.studio-mode-menu label { display: grid; gap: 5px; color: #aab2bf; font-size: 11px; font-weight: 560; }
.output-controls.studio-mode-menu :is(select, input) { height: 34px; border-radius: 10px; padding: 0 9px; font-size: 12px; }
.mode-menu-section { display: grid; gap: 1px; }
.mode-menu-row { min-height: 35px; display: grid; grid-template-columns: 22px minmax(0, 1fr) 13px; gap: 6px; align-items: center; padding: 0 7px; border: 0; border-radius: 12px; background: transparent; color: #e7e9ed; text-align: left; transition: background 140ms ease, color 140ms ease, transform 140ms ease; }
.mode-menu-row:hover { background: rgb(255 255 255 / 9%); color: #fff; transform: translateX(1px); }
.mode-menu-row.active { background: #087cf2; color: #fff; box-shadow: inset 0 1px 0 rgb(255 255 255 / 18%); }
.mode-menu-row.active:hover { background: #1685f4; }
.mode-menu-icon { width: 22px; height: 22px; display: grid; place-items: center; color: #e4e8ee; }
.mode-menu-icon svg { width: 15px; height: 15px; }
.mode-menu-row.active .mode-menu-icon { color: #fff; }
.mode-menu-label { overflow: hidden; font-size: 12px; font-weight: 560; letter-spacing: 0; text-overflow: ellipsis; white-space: nowrap; }
.mode-menu-check { width: 12px; height: 12px; }
.mode-menu-check { justify-self: end; color: #dcecff; }
.mode-menu-divider { height: 1px; margin: 3px 8px; background: rgb(255 255 255 / 20%); }
.mode-menu-disabled { color: #a1a5ac; cursor: not-allowed; opacity: .72; }
.mode-menu-disabled:hover { background: transparent; color: #a1a5ac; transform: none; }
.mode-menu-enter-active, .mode-menu-leave-active { transition: opacity 140ms ease, transform 140ms ease; }
.mode-menu-enter-from, .mode-menu-leave-to { opacity: 0; transform: translateY(5px); }
@keyframes studio-shimmer { to { background-position: -200% 0; } }

/* The global theme owns the creation surface. Dark mode keeps the immersive
   studio treatment above; light mode translates the same hierarchy into the
   site's canvas, surface, text and hairline tokens. */
.creation-studio.is-light {
& {
  --studio-bg: var(--canvas);
  --studio-panel: var(--surface);
  --studio-panel-strong: var(--surface);
  --studio-border: var(--border);
  --studio-muted: var(--text-secondary);
  --studio-text: var(--text);
  --studio-blue: var(--accent);
  --studio-blue-soft: var(--accent-soft);
  color-scheme: light;
  background:
    radial-gradient(circle at 50% 18%, color-mix(in srgb, var(--accent) 8%, transparent), transparent 38%),
    var(--studio-bg);
}

.studio-scroll { scrollbar-color: var(--border-strong) transparent; }
.studio-modes,
.studio-view-switch,
.studio-search,
.studio-filter { background: color-mix(in srgb, var(--surface-muted) 72%, transparent); }
.studio-modes a { color: var(--text-secondary); }
.studio-modes a:hover,
.studio-modes a.active { background: var(--surface); color: var(--text); box-shadow: inset 0 0 0 1px var(--border); }
.studio-view-switch button { background: var(--surface-muted); color: var(--text-secondary); }
.studio-view-switch button.active { background: var(--surface); color: var(--text); box-shadow: var(--shadow-xs); }
.studio-search input,
.studio-filter select { color: var(--text); }
.studio-search input::placeholder { color: var(--text-tertiary); }
.studio-balance,
.studio-toolbar-actions > .icon-button { color: var(--text-secondary); }
.studio-balance strong { color: var(--text); }
.studio-balance:hover,
.studio-toolbar-actions > .icon-button:hover { background: var(--surface-muted); color: var(--text); }
.studio-heading p,
.studio-identity p,
.studio-empty,
.studio-empty p,
.studio-guide header p,
.studio-guide-latest > div,
.studio-popover > header small,
.studio-popover > p,
.conversation-user > div,
.conversation-result > header small,
.conversation-result > footer,
.conversation-welcome p { color: var(--text-secondary); }
.studio-notice.error { color: var(--danger); }
.studio-notice.success { color: var(--success); }
.creation-studio.is-guest .studio-auth :deep(.auth-required-icon) { color: var(--accent-readable); }
.creation-studio.is-guest .studio-auth :deep(.auth-required-copy p) { color: var(--text-secondary); }

.studio-task,
.studio-task-copy,
.studio-guide-messages p,
.studio-guide-latest,
.conversation-result { background: var(--surface); }
.studio-task:hover,
.conversation-result:hover { border-color: var(--border-strong); background: var(--surface-raised); }
.studio-task-media,
.conversation-result :deep(.asset-renderer),
.conversation-result :deep(img),
.conversation-result :deep(video) { background: var(--surface-muted); }
.studio-task-copy p,
.studio-guide-messages p,
.conversation-result > header { color: var(--text); }
.studio-task-copy > small,
.studio-task-copy > div { color: var(--text-secondary); }
.studio-task-status,
.studio-task-favorite { background: rgb(255 255 255 / 88%); color: var(--text-secondary); }
.studio-empty > span,
.studio-guide > header > span,
.studio-guide-messages article > span,
.assistant-mark { background: var(--surface-muted); color: var(--accent-readable); }
.studio-guide-messages article[data-role='user'] > p,
.conversation-user p { border-color: color-mix(in srgb, var(--accent) 26%, var(--border)); background: var(--accent-soft); color: var(--text); }

.studio-composer {
  border-color: var(--border-strong);
  background: color-mix(in srgb, var(--surface) 96%, transparent);
  box-shadow: 0 18px 52px rgb(27 35 52 / 12%), 0 0 0 1px rgb(255 255 255 / 70%);
}
.studio-composer textarea { color: var(--text); }
.studio-composer textarea::placeholder { color: var(--text-tertiary); }
.studio-composer-tools > button,
.studio-settings-button { border-color: var(--border); background: var(--surface-muted); color: var(--text-secondary); }
.studio-composer-tools > button:hover,
.studio-composer-tools > button.active,
.studio-settings-button:hover,
.studio-settings-button.active { border-color: color-mix(in srgb, var(--accent) 34%, var(--border)); background: var(--accent-soft); color: var(--accent-readable); }
.studio-submit { background: var(--accent); color: var(--accent-contrast); box-shadow: 0 5px 18px color-mix(in srgb, var(--accent) 30%, transparent); }
.studio-submit:hover:not(:disabled) { background: var(--accent-hover); }
.studio-submit:disabled { background: var(--surface-muted); color: var(--text-tertiary); }
.studio-context-item,
.creation-mode-chip { border-color: color-mix(in srgb, var(--accent) 32%, var(--border)); color: var(--accent-readable); }
.studio-context-item > svg:first-child,
.studio-context-item > button,
.creation-mode-chip button,
.conversation-mode { color: var(--accent-readable); }
.studio-context-item > button:hover,
.creation-mode-chip button:hover { background: color-mix(in srgb, var(--accent) 12%, transparent); color: var(--accent-readable); }

.studio-popover {
  border-color: var(--border-strong);
  background: color-mix(in srgb, var(--surface-raised) 97%, transparent);
  box-shadow: 0 18px 50px rgb(27 35 52 / 16%);
}
.reference-mode-switch,
.reference-actions button { background: var(--surface-muted); }
.reference-mode-switch button,
.reference-actions button { color: var(--text-secondary); }
.reference-mode-switch button.active { background: var(--surface); color: var(--text); box-shadow: var(--shadow-xs); }
.reference-list button:hover { background: var(--surface-muted); }
.reference-list button.selected { background: var(--accent-soft); color: var(--accent-readable); }
.output-controls select,
.output-controls input { border-color: var(--border); background: var(--surface); color: var(--text); }

.studio-detail-backdrop { background: rgb(27 35 52 / 22%); }
.studio-detail { border-left-color: var(--border); background: var(--surface); box-shadow: -18px 0 50px rgb(27 35 52 / 14%); }
.studio-detail-media,
.studio-detail dl div { background: var(--surface-muted); }
.studio-detail > p,
.studio-detail dt { color: var(--text-secondary); }
.studio-detail dd { color: var(--text); }

.studio-identity > span,
.conversation-welcome > span { color: var(--accent-readable); }
.conversation-progress { color: var(--text-secondary); }
.conversation-result > footer button:hover { background: var(--surface-muted); color: var(--accent-readable); }
.conversation-skeleton span { background: linear-gradient(100deg, var(--surface-muted), var(--surface), var(--surface-muted)); background-size: 200% 100%; }

.studio-mode-menu {
  border-color: var(--border-strong);
  background: color-mix(in srgb, var(--surface-raised) 97%, transparent);
  box-shadow: 0 22px 65px rgb(27 35 52 / 18%), inset 0 1px 0 rgb(255 255 255 / 70%);
}
.output-controls.studio-mode-menu label { color: var(--text-secondary); }
.output-controls.studio-mode-menu :is(select, input) { border-color: var(--border); background: var(--surface); color: var(--text); }
.mode-menu-row { color: var(--text); }
.mode-menu-row:hover { background: var(--surface-muted); color: var(--text); }
.mode-menu-row.active { background: var(--accent); color: var(--accent-contrast); }
.mode-menu-row.active:hover { background: var(--accent-hover); }
.mode-menu-icon { color: var(--text-secondary); }
.mode-menu-row.active .mode-menu-icon,
.mode-menu-check { color: var(--accent-contrast); }
.mode-menu-divider { background: var(--border); }
.mode-menu-disabled,
.mode-menu-disabled:hover { color: var(--text-tertiary); }
}

@media (max-width: 1100px) {
  .studio-topbar { grid-template-columns: auto minmax(0, 1fr) auto; }
  .studio-search { width: 150px; }
}

@media (max-width: 767px) {
  .creation-studio { height: calc(100dvh - 124px); min-height: 520px; }
  .studio-scroll { padding: 8px 12px 180px; }
  .studio-topbar { grid-template-columns: minmax(0, 1fr) auto; margin-bottom: 8px; }
  .studio-toolbar-actions { justify-content: flex-end; }
  .studio-balance { display: none; }
  .studio-composer { right: 10px; bottom: 10px; left: 10px; width: auto; padding: 9px 10px 8px; border-radius: 22px; }
  .studio-composer textarea { min-height: 58px; }
  .studio-identity p { display: none; }
  .studio-conversation { min-height: clamp(240px, calc(100dvh - 350px), 420px); margin-top: 28px; }
  .creation-studio.is-guest .studio-auth { padding: 20px 12px 46px; }
  .creation-studio.is-guest .studio-auth :deep(.auth-required-copy h2) { font-size: 20px; }
  .creation-studio.is-guest .studio-auth :deep(.auth-required-actions) { width: 100%; }
  .creation-studio.is-guest .studio-auth :deep(.command-button) { flex: 1 1 132px; justify-content: center; }
  .conversation-user { width: 92%; }
  .studio-mode-menu { width: min(224px, calc(100vw - 28px)); max-height: min(320px, calc(100dvh - 430px)); }
  .mode-menu-row { min-height: 36px; }
}
</style>
