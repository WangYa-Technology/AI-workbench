<script setup lang="ts">
import { onClickOutside, onKeyStroke, useIntervalFn } from '@vueuse/core'
import {
  AlertCircle, ArrowUp, Check, ChevronRight, Clock3, Cloud, FileText, FolderOpen, History, Image as ImageIcon,
  Images, LoaderCircle, MessageSquare, Music, Paperclip, Plus, RefreshCw, Settings2, Sparkles,
  Upload, Video, X,
} from 'lucide-vue-next'
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { api, messageFrom, type Asset, type Conversation, type CreationCapabilities, type CreationCapability, type Generation } from '../../api/client'
import { formatDateTime } from '../../lib/format'
import { isCreationCapabilityComplete } from '../../lib/creationCapabilities'
import { useCreationDraftStore, type CreationDraftMode, type CreationDraftView, type CreationOutputSettings } from '../../stores/creationDraft'
import { usePreferencesStore } from '../../stores/preferences'
import { useSessionStore } from '../../stores/session'
import BrandLogo from '../brand/BrandLogo.vue'
import AssetMedia from '../domain/AssetMedia.vue'
import MotionFavoriteIcon from '../ui/MotionFavoriteIcon.vue'
import AuthRequiredState from '../domain/AuthRequiredState.vue'

type CreationMode = CreationDraftMode

const props = defineProps<{ mode: CreationMode }>()
const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const preferences = usePreferencesStore()
const drafts = useCreationDraftStore()

const restored = drafts.restore(props.mode)
const activeMode = ref<CreationMode>(props.mode)
const prompt = ref(restored?.prompt || '')
const view = ref<CreationDraftView>('guide')
const settings = ref<CreationOutputSettings>(restored?.settings || defaultSettings(props.mode))
const generations = ref<Generation[]>([])
const conversations = ref<Conversation[]>([])
const currentConversationId = ref('')
const capabilities = ref<CreationCapabilities | null>(null)
const capabilitiesLoaded = ref(false)
const assets = ref<Asset[]>([])
const sourceAssets = ref<Asset[]>([])
const maskAsset = ref<Asset | null>(null)
const selectedGeneration = ref<Generation | null>(null)
const selectedModelId = ref('')
const loading = ref(true)
const assetsLoading = ref(false)
const submitting = ref(false)
const uploadLoading = ref(false)
const actionLoading = ref('')
const error = ref('')
const feedback = ref('')
const historyOpen = ref(false)
const historyLoading = ref(false)
const conversationCreating = ref(false)
const modeMenuOpen = ref(false)
const controlsOpen = ref(false)
const assetPickerOpen = ref(false)
const referencePickerMode = ref<'references' | 'mask'>('references')
const modeMenu = ref<InstanceType<typeof globalThis.HTMLElement> | null>(null)
const modeMenuTrigger = ref<InstanceType<typeof globalThis.HTMLButtonElement> | null>(null)
const controlsPanel = ref<InstanceType<typeof globalThis.HTMLElement> | null>(null)
const controlsTrigger = ref<InstanceType<typeof globalThis.HTMLButtonElement> | null>(null)
const fileInput = ref<InstanceType<typeof globalThis.HTMLInputElement> | null>(null)

const modes = computed(() => [
  { id: 'chat' as const, icon: MessageSquare, label: t('create.modes.chat'), menuLabel: t('create.studio.menuItems.chat') },
  { id: 'image' as const, icon: ImageIcon, label: t('create.modes.image'), menuLabel: t('create.studio.menuItems.image') },
  { id: 'video' as const, icon: Video, label: t('create.modes.video'), menuLabel: t('create.studio.menuItems.video') },
  { id: 'music' as const, icon: Music, label: t('create.modes.music'), menuLabel: t('create.studio.menuItems.music') },
])
const activeCapability = computed(() => capabilities.value?.items.find(item => item.mode === activeMode.value) || null)
const capabilityComplete = computed(() => isCreationCapabilityComplete(activeCapability.value))
const modelOptions = computed(() => {
  const capability = activeCapability.value
  if (!capabilityComplete.value) return []
  if (capability?.models?.length) {
    return capability.models.map(item => ({ value: item.id, label: item.displayName || item.modelName, detail: `${item.providerName} · ${item.modelName}`, provider: item.provider || '' , available: item.available })).filter(item => item.available)
  }
  if (!capability?.available || !capability.modelName) return []
  return [{ value: '', label: capability.modelName, detail: capability.provider || '', provider: capability.provider || '', available: true }]
})
const capabilityUnavailable = computed(() => Boolean(capabilitiesLoaded.value && (!capabilityComplete.value || !modelOptions.value.length)))
const modeIcon = computed(() => modes.value.find(item => item.id === activeMode.value)?.icon || MessageSquare)
const modeLabel = computed(() => modes.value.find(item => item.id === activeMode.value)?.label || t('create.modes.chat'))
const formatOptions = computed(() => activeMode.value === 'chat' ? ['txt'] : activeMode.value === 'image' ? ['jpeg', 'png'] : activeMode.value === 'video' ? ['mp4'] : ['wav'])
const capabilityFormatOptions = computed(() => capabilityComplete.value ? activeCapability.value?.outputFormats || formatOptions.value : formatOptions.value)
const ratioOptions = computed(() => capabilityComplete.value ? activeCapability.value?.aspectRatios || [] : ['auto', '1:1', '4:5', '16:9'])
const qualityOptions = computed(() => capabilityComplete.value ? activeCapability.value?.qualities || [] : ['auto', 'standard', 'high'])
const durationOptions = computed(() => capabilityComplete.value ? activeCapability.value?.durationSeconds || [] : activeMode.value === 'music' ? [5, 10, 30, 60] : [5, 10, 30])
const referenceKinds = computed(() => capabilityComplete.value ? activeCapability.value?.referenceKinds || [] : ({ chat: ['document'], image: ['image'], video: ['image'], music: ['audio'] }[activeMode.value]))
const referenceAccept = computed(() => ({ chat: 'text/plain,.txt,.md', image: 'image/jpeg,image/png', video: 'image/jpeg,image/png', music: 'audio/wav,audio/x-wav,audio/wave,audio/mpeg' }[activeMode.value]))
const running = computed(() => generations.value.some(item => ['queued', 'running'].includes(item.status)))
const currentConversation = computed(() => conversations.value.find(item => item.id === currentConversationId.value) || null)
const canSubmit = computed(() => prompt.value.trim().length >= 3 && Boolean(session.user) && capabilitiesLoaded.value && !submitting.value && !capabilityUnavailable.value && (!maskAsset.value || sourceAssets.value.length > 0))
const generationLabel = computed(() => activeMode.value === 'chat' ? t('create.studio.continueChat') : t('actions.generateMode', { mode: modeLabel.value }))

function defaultSettings(mode: CreationMode): CreationOutputSettings {
  return { ratio: 'auto', quality: 'auto', format: ({ chat: 'txt', image: 'jpeg', video: 'mp4', music: 'wav' } as const)[mode], count: 1, duration: mode === 'music' ? 30 : 10, responseLength: 'balanced' }
}

function generationMediaKind(item: Generation) {
  return ({ chat: 'document', image: 'image', video: 'video', music: 'audio' } as const)[item.mode] || 'document'
}

function generationDate(value: string) {
  return formatDateTime(value, locale.value, Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC')
}

function conversationTitle(conversation: Conversation) {
  const title = conversation.title?.trim()
  return !title || title === 'New conversation' || title === '新建对话' ? t('create.studio.newChat') : title
}

function generationCost(item: Generation) {
  return `${(item.chargedPoints || item.estimatedPoints).toLocaleString(locale.value)} ${t('workspace.pointsUnit')}`
}

function parameterSummary(item: Generation) {
  const p = item.parameters
  if (item.mode === 'chat') return [p.responseLength, p.outputFormat?.toUpperCase()].filter(Boolean).join(' · ')
  return [p.aspectRatio, p.quality, p.durationSeconds ? t('create.studio.durationValue', { value: p.durationSeconds }) : '', p.outputFormat?.toUpperCase()].filter(Boolean).join(' · ')
}

function statusLabel(item: Generation) {
  if (item.mode === 'chat' && item.status === 'succeeded') return t('create.studio.chatCompleted')
  return t(`generation.status.${item.status}`)
}

function generationParameters(): Generation['parameters'] {
  if (activeMode.value === 'chat') return { responseLength: settings.value.responseLength, outputFormat: 'txt' }
  if (activeMode.value === 'image') return { aspectRatio: settings.value.ratio, quality: settings.value.quality, outputFormat: settings.value.format as 'jpeg' | 'png' }
  if (activeMode.value === 'video') return { aspectRatio: settings.value.ratio, quality: settings.value.quality, outputFormat: 'mp4', durationSeconds: settings.value.duration as 5 | 10 | 30 }
  return { quality: settings.value.quality, outputFormat: 'wav', durationSeconds: settings.value.duration as 5 | 10 | 30 | 60 }
}

async function loadCapabilities() {
  try { capabilities.value = await api.creationCapabilities() } catch { capabilities.value = null } finally { capabilitiesLoaded.value = true }
}

async function loadGenerations(silent = false) {
  if (!session.user) { generations.value = []; loading.value = false; return }
  if (!currentConversationId.value) { generations.value = []; loading.value = false; return }
  if (!silent) loading.value = true
  try { generations.value = (await api.listGenerations({ conversationId: currentConversationId.value, limit: 50 })).items.sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt)) } catch (reason) { if (!silent) error.value = messageFrom(reason) } finally { if (!silent) loading.value = false }
}

async function loadConversations() {
  if (!session.user) return
  historyLoading.value = true
  try {
    conversations.value = (await api.listConversations()).items
    const requested = String(route.query.conversationId || '')
    const next = conversations.value.find(item => item.id === requested) || conversations.value[0]
    if (next) {
      currentConversationId.value = next.id
      if (requested !== next.id) await router.replace({ query: { ...route.query, conversationId: next.id } })
    } else {
      currentConversationId.value = ''
    }
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    historyLoading.value = false
  }
}

async function selectConversation(conversation: Conversation) {
  currentConversationId.value = conversation.id
  historyOpen.value = false
  selectedGeneration.value = null
  prompt.value = ''
  feedback.value = ''
  await router.replace({ query: { ...route.query, conversationId: conversation.id } })
  await loadGenerations()
}

async function newConversation() {
  if (!session.user || conversationCreating.value) return
  conversationCreating.value = true
  error.value = ''
  try {
    const conversation = await api.createConversation({ title: t('create.studio.newChat') })
    conversations.value = [conversation, ...conversations.value]
    currentConversationId.value = conversation.id
    generations.value = []
    prompt.value = ''
    sourceAssets.value = []
    maskAsset.value = null
    selectedGeneration.value = null
    feedback.value = ''
    historyOpen.value = false
    await router.replace({ query: { ...route.query, conversationId: conversation.id } })
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    conversationCreating.value = false
  }
}

async function loadAssets() {
  if (!session.user || assetsLoading.value) return
  assetsLoading.value = true
  try { assets.value = (await api.listAssets({ limit: 50 })).items.filter(item => item.scanStatus === 'clean' && referenceKinds.value.includes(item.kind)) } catch (reason) { error.value = messageFrom(reason) } finally { assetsLoading.value = false }
}

function selectMode(mode: CreationMode) {
  if (!modeAvailable(mode)) return
  activeMode.value = mode
  settings.value = defaultSettings(mode)
  sourceAssets.value = sourceAssets.value.filter(asset => referenceKinds.value.includes(asset.kind))
  if (mode !== 'image') maskAsset.value = null
  modeMenuOpen.value = false
  assetPickerOpen.value = false
  controlsOpen.value = false
  void loadAssets()
}

function modeAvailable(mode: CreationMode) {
  if (!capabilitiesLoaded.value) return true
  if (!capabilities.value) return false
  const capability = capabilities.value.items.find(item => item.mode === mode)
  return Boolean(capability && isCreationCapabilityComplete(capability) && modelOptionsForMode(mode, capability).length)
}

function modelOptionsForMode(_mode: CreationMode, capability: CreationCapability) {
  if (capability.models?.length) return capability.models.filter(item => item.available)
  return capability.available && capability.modelName ? [{ id: '', available: true }] : []
}

function toggleModeMenu() {
  modeMenuOpen.value = !modeMenuOpen.value
  if (modeMenuOpen.value) { controlsOpen.value = false; assetPickerOpen.value = false }
}

function openAssetPicker(mode: 'references' | 'mask') {
  referencePickerMode.value = mode
  modeMenuOpen.value = false
  assetPickerOpen.value = true
  void loadAssets()
}

async function chooseAsset(asset: Asset) {
  if (referencePickerMode.value === 'mask') { maskAsset.value = maskAsset.value?.id === asset.id ? null : asset; feedback.value = maskAsset.value ? t('create.studio.maskAttached', { title: asset.title }) : ''; return }
  if (sourceAssets.value.some(item => item.id === asset.id)) { sourceAssets.value = sourceAssets.value.filter(item => item.id !== asset.id); return }
  if (sourceAssets.value.length >= 8) { error.value = t('create.studio.referenceLimit'); return }
  sourceAssets.value = [...sourceAssets.value, asset]
  feedback.value = t('create.studio.referenceAttached', { title: asset.title })
}

function removeSourceAsset(id: string) { sourceAssets.value = sourceAssets.value.filter(asset => asset.id !== id) }

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
  try {
    const form = new globalThis.FormData()
    form.append('title', file.name.replace(/\.[^.]+$/, '') || t('create.studio.referenceImage'))
    form.append('file', file)
    const uploaded = await waitForCleanAsset(await api.uploadAsset(form))
    assets.value = [uploaded, ...assets.value.filter(item => item.id !== uploaded.id)]
    await chooseAsset(uploaded)
  } catch (reason) { error.value = messageFrom(reason) } finally { uploadLoading.value = false; input.value = '' }
}

async function submit() {
  if (!canSubmit.value) return
  submitting.value = true
  error.value = ''
  feedback.value = ''
  try {
    if (!currentConversationId.value) {
      const conversation = await api.createConversation({ title: t('create.studio.newChat') })
      conversations.value = [conversation, ...conversations.value]
      currentConversationId.value = conversation.id
      await router.replace({ query: { ...route.query, conversationId: conversation.id } })
    }
    const input = {
      conversationId: currentConversationId.value,
      mode: activeMode.value,
      prompt: prompt.value.trim(),
      modelId: selectedModelId.value || null,
      parameters: generationParameters(),
      sourceAssetId: sourceAssets.value[0]?.id || null,
      sourceAssetIds: sourceAssets.value.map(asset => asset.id),
      maskAssetId: maskAsset.value?.id || null,
      parentGenerationId: activeMode.value === 'chat' ? generations.value.filter(item => item.mode === 'chat' && item.status === 'succeeded').at(-1)?.id || null : null,
    }
    const created = await Promise.all(Array.from({ length: activeMode.value === 'chat' ? 1 : settings.value.count }, () => api.createGeneration(input)))
    generations.value = [...generations.value, ...created].sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt))
    await loadConversations()
    prompt.value = ''
    feedback.value = t('create.studio.queued', { count: created.length })
    resume()
  } catch (reason) { error.value = messageFrom(reason) } finally { submitting.value = false }
}

async function toggleFavorite(item: Generation) {
  actionLoading.value = `${item.id}:favorite`
  try { const updated = await api.favoriteGeneration(item.id, !item.isFavorite); generations.value = generations.value.map(current => current.id === updated.id ? updated : current); if (selectedGeneration.value?.id === updated.id) selectedGeneration.value = updated } catch (reason) { error.value = messageFrom(reason) } finally { actionLoading.value = '' }
}

async function changeGeneration(item: Generation, action: 'cancel' | 'retry') {
  actionLoading.value = `${item.id}:${action}`
  try {
    const updated = action === 'cancel' ? await api.cancelGeneration(item.id, 'Cancelled from the creation studio.') : await api.retryGeneration(item.id)
    generations.value = action === 'retry' ? [...generations.value, updated] : generations.value.map(current => current.id === item.id ? updated : current)
    selectedGeneration.value = updated
    resume()
  } catch (reason) { error.value = messageFrom(reason) } finally { actionLoading.value = '' }
}

function reuseGeneration(item: Generation) {
  prompt.value = item.prompt
  activeMode.value = item.mode as CreationMode
  if (item.parameters.aspectRatio) settings.value.ratio = item.parameters.aspectRatio
  if (item.parameters.quality) settings.value.quality = item.parameters.quality
  if (item.parameters.outputFormat) settings.value.format = item.parameters.outputFormat
  if (item.parameters.durationSeconds) settings.value.duration = item.parameters.durationSeconds
  if (item.parameters.responseLength) settings.value.responseLength = item.parameters.responseLength
  selectedGeneration.value = null
}

const { pause, resume } = useIntervalFn(async () => {
  if (!running.value) { pause(); return }
  await loadGenerations(true)
}, 1400, { immediate: false })

onClickOutside(modeMenu, () => { modeMenuOpen.value = false }, { ignore: [modeMenuTrigger] })
onClickOutside(controlsPanel, () => { controlsOpen.value = false }, { ignore: [controlsTrigger] })
onKeyStroke('Escape', () => { modeMenuOpen.value = false; controlsOpen.value = false; assetPickerOpen.value = false })

watch([prompt, settings, activeMode], () => { drafts.save(props.mode, { prompt: prompt.value, view: view.value, settings: settings.value }) }, { deep: true })
watch(() => route.query.conversationId, async value => {
  const conversationID = String(value || '')
  if (!conversationID || conversationID === currentConversationId.value) return
  if (!conversations.value.some(item => item.id === conversationID)) return
  currentConversationId.value = conversationID
  await loadGenerations()
})
watch(activeCapability, capability => {
  selectedModelId.value = capability?.models?.find(item => item.available)?.id || ''
  if (!capability) return
  if (capability.outputFormats.length && !capability.outputFormats.includes(settings.value.format)) settings.value.format = capability.outputFormats[0]
  if (capability.aspectRatios.length && !capability.aspectRatios.includes(settings.value.ratio)) settings.value.ratio = capability.aspectRatios[0] as CreationOutputSettings['ratio']
  if (capability.qualities.length && !capability.qualities.includes(settings.value.quality)) settings.value.quality = capability.qualities[0] as CreationOutputSettings['quality']
}, { immediate: true })

onMounted(async () => {
  await session.ensure()
  await loadCapabilities()
  await loadConversations()
  await Promise.all([loadGenerations(), loadAssets()])
  if (running.value) resume()
})
</script>

<template>
  <section class="creation-studio-new" :class="{ 'is-light': preferences.resolvedTheme === 'light', 'is-guest': session.initialized && !session.user }" :data-mode="activeMode">
    <header class="creation-header">
      <div class="creation-heading">
        <span class="creation-mark"><Sparkles :size="16" /></span>
        <span class="creation-title">{{ t('create.studio.unifiedTitle') }}</span>
        <span class="creation-heading-divider" aria-hidden="true">/</span>
        <span class="creation-current-mode"><component :is="modeIcon" :size="14" />{{ modeLabel }}</span>
      </div>
      <div class="creation-header-actions">
        <span v-if="currentConversation" class="creation-current-conversation" :title="conversationTitle(currentConversation)">
          <MessageSquare :size="14" aria-hidden="true" />
          <span class="creation-current-conversation-label">{{ conversationTitle(currentConversation) }}</span>
        </span>
        <button class="creation-icon-link" :class="{ active: historyOpen }" type="button" :aria-label="t('create.studio.openHistory')" :aria-expanded="historyOpen" :title="t('create.studio.openHistory')" @click="historyOpen = !historyOpen">
          <History :size="17" />
        </button>
      </div>
    </header>

    <Transition name="creation-popover">
      <aside v-if="historyOpen" class="creation-history-panel" :aria-label="t('create.studio.historyTitle')">
        <header class="creation-history-header">
          <div>
            <span>{{ t('create.studio.historyLabel') }}</span>
            <strong>{{ t('create.studio.historyTitle') }}</strong>
          </div>
          <button class="creation-icon-link" type="button" :aria-label="t('actions.close')" :title="t('actions.close')" @click="historyOpen = false">
            <X :size="16" />
          </button>
        </header>
        <button class="creation-history-new" type="button" :disabled="conversationCreating" @click="newConversation">
          <LoaderCircle v-if="conversationCreating" class="spin" :size="16" />
          <Plus v-else :size="16" />
          <span>{{ t('create.studio.newChat') }}</span>
        </button>
        <div v-if="historyLoading" class="creation-history-empty">
          <LoaderCircle class="spin" :size="17" />{{ t('status.loadingWorkspace') }}
        </div>
        <div v-else-if="conversations.length" class="creation-history-list">
          <button v-for="conversation in conversations" :key="conversation.id" type="button" class="creation-history-item" :class="{ active: conversation.id === currentConversationId }" @click="selectConversation(conversation)">
            <span class="creation-history-item-main">
              <strong>{{ conversationTitle(conversation) }}</strong>
              <small>{{ conversation.modes.map(mode => t(`create.modes.${mode}`)).join(' · ') || t('create.studio.emptyConversation') }} · {{ conversation.generationCount }} {{ t('create.studio.historyItems') }}</small>
            </span>
            <ChevronRight :size="15" />
          </button>
        </div>
        <div v-else class="creation-history-empty">
          <Clock3 :size="20" />
          <span>{{ t('create.studio.noConversations') }}</span>
        </div>
      </aside>
    </Transition>

    <main class="creation-main">
      <div class="creation-main-content">
        <div v-if="capabilityUnavailable || error || feedback" class="creation-notices">
          <div v-if="capabilityUnavailable" class="creation-notice error" role="status"><AlertCircle :size="16" /><span>{{ t('create.modeUnavailable') }}</span></div>
          <div v-if="error" class="creation-notice error" role="alert"><AlertCircle :size="16" /><span>{{ error }}</span><button type="button" :aria-label="t('actions.close')" @click="error = ''"><X :size="15" /></button></div>
          <div v-if="feedback" class="creation-notice success" role="status"><Check :size="16" /><span>{{ feedback }}</span><button type="button" :aria-label="t('actions.close')" @click="feedback = ''"><X :size="15" /></button></div>
        </div>
        <AuthRequiredState v-if="session.initialized && !session.user" class="creation-auth" :title="t('authRequired.createTitle')" :summary="t('authRequired.createSummary')" :return-to="route.fullPath" />
        <section v-else class="creation-conversation" :aria-label="t('create.studio.conversationLabel')">
          <div v-if="loading" class="creation-loading" aria-live="polite"><span></span><span></span><span></span></div>
          <div v-else-if="!generations.length" class="creation-empty">
            <span class="creation-empty-mark"><component :is="modeIcon" :size="28" /></span>
            <strong>{{ t('create.studio.welcomeTitle') }}</strong>
            <p>{{ t('create.studio.welcomeSummary') }}</p>
            <small>{{ t(`create.modeMeta.${activeMode}.summary`) }}</small>
          </div>
          <div v-else class="creation-feed">
            <article v-for="item in generations" :key="item.id" class="creation-turn" :data-status="item.status">
              <div class="creation-prompt">
                <div><span class="creation-mode-label"><component :is="modes.find(option => option.id === item.mode)?.icon" :size="13" />{{ t(`create.modes.${item.mode}`) }}</span><time :datetime="item.createdAt">{{ generationDate(item.createdAt) }}</time></div>
                <p>{{ item.prompt }}</p>
              </div>
              <div class="creation-result" @click="selectedGeneration = item">
                <div class="creation-assistant">
                  <BrandLogo class="creation-assistant-mark" />
                  <div class="creation-assistant-body">
                    <div class="creation-result-head"><span class="creation-status" :data-status="item.status">{{ statusLabel(item) }}</span><span>{{ item.modelName }}</span><button type="button" :aria-label="item.isFavorite ? t('workspace.unfavoriteGeneration') : t('workspace.favoriteGeneration')" @click.stop="toggleFavorite(item)"><MotionFavoriteIcon :active="item.isFavorite" kind="bookmark" :size="15" /></button></div>
                    <div v-if="item.mode === 'chat' && item.outputText" class="creation-output-box"><div class="creation-text">{{ item.outputText }}</div></div>
                    <div v-else-if="item.outputMediaUrl" class="creation-media"><AssetMedia :src="item.outputMediaUrl" :kind="generationMediaKind(item)" :alt="item.prompt" :text="item.outputText || ''" :width="1200" :height="900" :controls="generationMediaKind(item) === 'video' || generationMediaKind(item) === 'audio'" /></div>
                    <div v-else class="creation-progress"><LoaderCircle v-if="['queued', 'running'].includes(item.status)" class="spin" :size="22" /><AlertCircle v-else-if="item.status === 'failed'" :size="22" /><component :is="modes.find(option => option.id === item.mode)?.icon" v-else :size="22" /><span>{{ item.errorMessage || `${item.progress}%` }}</span></div>
                    <footer><span>{{ parameterSummary(item) }}</span><span>{{ generationCost(item) }}</span></footer>
                  </div>
                </div>
              </div>
            </article>
          </div>
        </section>
      </div>
    </main>

    <form class="creation-composer" @submit.prevent="submit">
      <div v-if="sourceAssets.length || maskAsset" class="creation-context">
        <span v-for="asset in sourceAssets" :key="asset.id" class="creation-context-chip"><Paperclip :size="13" /><strong>{{ asset.title }}</strong><button type="button" :aria-label="t('actions.close')" @click="removeSourceAsset(asset.id)"><X :size="13" /></button></span>
        <span v-if="maskAsset" class="creation-context-chip mask"><ImageIcon :size="13" /><strong>{{ maskAsset.title }}</strong><button type="button" :aria-label="t('actions.close')" @click="maskAsset = null"><X :size="13" /></button></span>
      </div>
      <textarea v-model="prompt" rows="2" maxlength="1800" :placeholder="t(`create.builder.modePlaceholder.${activeMode}`)" @keydown.enter.exact.prevent="canSubmit && submit()"></textarea>
      <div class="creation-composer-row">
        <div class="creation-composer-tools">
          <button ref="modeMenuTrigger" class="creation-tool-button" type="button" :class="{ active: modeMenuOpen }" :aria-label="t('create.studio.chooseCreationType')" :aria-expanded="modeMenuOpen" @click="toggleModeMenu"><Plus :size="19" /></button>
          <span v-if="activeMode !== 'chat'" class="creation-mode-chip"><component :is="modeIcon" :size="14" /><span class="creation-mode-chip-label">{{ modeLabel }}</span><button type="button" :aria-label="t('create.studio.removeCreationType')" @click="selectMode('chat')"><X :size="12" /></button></span>
          <button ref="controlsTrigger" class="creation-tool-button" type="button" :class="{ active: controlsOpen }" :aria-label="t('create.studio.outputSettings')" :aria-expanded="controlsOpen" @click="controlsOpen = !controlsOpen; modeMenuOpen = false; assetPickerOpen = false"><Settings2 :size="17" /></button>
        </div>
        <div class="creation-composer-actions">
          <label v-if="capabilitiesLoaded" class="creation-model-picker">
            <span>{{ t('create.studio.modelSelector') }}</span>
            <select
              v-model="selectedModelId"
              :disabled="!modelOptions.length"
              :aria-label="t('create.studio.modelSelector')"
            >
              <option v-if="!modelOptions.length" value="">
                {{ t('create.studio.noModelAvailable') }}
              </option>
              <option v-for="item in modelOptions" :key="item.value" :value="item.value">
                {{ item.label }}
              </option>
            </select>
          </label>
          <button class="creation-submit" type="submit" :disabled="!canSubmit"><LoaderCircle v-if="submitting" class="spin" :size="17" /><ArrowUp v-else :size="18" /><span>{{ submitting ? t('actions.generating') : generationLabel }}</span></button>
        </div>
      </div>

      <Transition name="creation-popover">
        <section v-if="modeMenuOpen" ref="modeMenu" class="creation-popover creation-tools-menu t-dropdown is-open" data-origin="bottom-left" role="menu" :aria-label="t('create.studio.chooseCreationType')">
          <div class="creation-menu-group">
            <button type="button" class="creation-menu-item" @click="openAssetPicker('references')"><FileText :size="18" /><span><strong>{{ t('create.studio.menuItems.file') }}</strong><small>{{ t('create.studio.referenceSummary') }}</small></span></button>
            <button type="button" class="creation-menu-item" @click="openAssetPicker('references')"><Cloud :size="18" /><span><strong>{{ t('create.studio.menuItems.library') }}</strong><small>{{ t('create.studio.menuItems.referenceWindow') }}</small></span></button>
            <button type="button" class="creation-menu-item" @click="openAssetPicker('references')"><Images :size="18" /><span><strong>{{ t('create.studio.menuItems.album') }}</strong><small>{{ t('create.studio.menuItems.reference') }}</small></span></button>
          </div>
          <div class="creation-menu-divider"></div>
          <div class="creation-menu-heading">{{ t('create.studio.menuItems.generationHeading') }}</div>
          <div class="creation-menu-group">
            <button v-for="item in modes" :key="item.id" type="button" class="creation-menu-item generation" :class="{ selected: activeMode === item.id, unavailable: !modeAvailable(item.id) }" :disabled="!modeAvailable(item.id)" :aria-disabled="!modeAvailable(item.id)" :title="!modeAvailable(item.id) ? t('create.studio.noModelAvailable') : undefined" @click="selectMode(item.id)"><span class="creation-menu-icon"><component :is="item.icon" :size="18" /></span><span><strong>{{ item.menuLabel }}</strong><small>{{ t(`create.modeMeta.${item.id}.summary`) }}</small></span><Check v-if="activeMode === item.id" class="creation-menu-check" :size="16" /></button>
          </div>
          <button type="button" class="creation-menu-item" @click="router.push('/market/demands')"><FolderOpen :size="18" /><span><strong>{{ t('create.studio.menuItems.taskContext') }}</strong><small>{{ t('create.studio.menuItems.referenceWindow') }}</small></span></button>
        </section>
      </Transition>

      <Transition name="creation-popover">
        <section v-if="controlsOpen" ref="controlsPanel" class="creation-popover creation-settings t-dropdown is-open" data-origin="bottom-right" role="dialog" :aria-label="t('create.studio.outputSettings')">
          <label v-if="['image', 'video'].includes(activeMode)"><span>{{ t('create.studio.ratio') }}</span><select v-model="settings.ratio"><option v-for="item in ratioOptions" :key="item" :value="item">{{ item }}</option></select></label>
          <label v-if="activeMode !== 'chat'"><span>{{ t('create.studio.quality') }}</span><select v-model="settings.quality"><option v-for="item in qualityOptions" :key="item" :value="item">{{ t(`create.studio.qualities.${item}`) }}</option></select></label>
          <label v-if="['video', 'music'].includes(activeMode)"><span>{{ t('create.studio.duration') }}</span><select v-model.number="settings.duration"><option v-for="item in durationOptions" :key="item" :value="item">{{ t('create.studio.durationValue', { value: item }) }}</option></select></label>
          <label v-if="activeMode === 'chat'"><span>{{ t('create.studio.responseLength') }}</span><select v-model="settings.responseLength"><option value="short">{{ t('create.studio.responseLengths.short') }}</option><option value="balanced">{{ t('create.studio.responseLengths.balanced') }}</option><option value="detailed">{{ t('create.studio.responseLengths.detailed') }}</option></select></label>
          <label v-if="capabilityFormatOptions.length > 1"><span>{{ t('create.studio.format') }}</span><select v-model="settings.format"><option v-for="item in capabilityFormatOptions" :key="item" :value="item">{{ item.toUpperCase() }}</option></select></label>
          <label v-if="activeMode !== 'chat'"><span>{{ t('create.studio.count') }}</span><input v-model.number="settings.count" type="number" min="1" max="4" /></label>
        </section>
      </Transition>

      <section v-if="assetPickerOpen" class="creation-popover creation-assets t-dropdown is-open" data-origin="bottom-left" role="dialog" :aria-label="t('create.studio.references')">
        <header><div><strong>{{ t(referencePickerMode === 'mask' ? 'create.studio.maskAsset' : 'create.studio.references') }}</strong><small>{{ t('create.studio.referenceSummary') }}</small></div><button type="button" :aria-label="t('actions.close')" @click="assetPickerOpen = false"><X :size="15" /></button></header>
        <div class="creation-asset-actions"><button type="button" :disabled="uploadLoading" @click="fileInput?.click()"><LoaderCircle v-if="uploadLoading" class="spin" :size="15" /><Upload v-else :size="15" />{{ t('create.studio.uploadReference') }}</button><input ref="fileInput" class="sr-only" type="file" :accept="referenceAccept" @change="uploadReference" /></div>
        <div v-if="assetsLoading" class="creation-asset-empty"><LoaderCircle class="spin" :size="17" />{{ t('status.loadingAssets') }}</div>
        <div v-else-if="assets.length" class="creation-asset-list"><button v-for="asset in assets" :key="asset.id" type="button" :class="{ selected: referencePickerMode === 'mask' ? maskAsset?.id === asset.id : sourceAssets.some(item => item.id === asset.id) }" @click="chooseAsset(asset)"><AssetMedia :src="asset.mediaUrl" :kind="asset.kind" :alt="asset.title" :width="48" :height="48" :controls="false" /><span><strong>{{ asset.title }}</strong><small>{{ asset.mimeType }}</small></span><Check v-if="referencePickerMode === 'mask' ? maskAsset?.id === asset.id : sourceAssets.some(item => item.id === asset.id)" :size="14" /></button></div>
        <p v-else class="creation-asset-empty">{{ t('create.studio.noReferences') }}</p>
      </section>
    </form>

    <div v-if="selectedGeneration" class="creation-detail-backdrop" @mousedown.self="selectedGeneration = null">
      <aside class="creation-detail" role="dialog" aria-modal="true" :aria-label="t('create.studio.detailTitle')">
        <header><div><span>{{ statusLabel(selectedGeneration) }}</span><h2>{{ t('create.studio.detailTitle') }}</h2></div><button class="creation-icon-link" type="button" :aria-label="t('actions.close')" @click="selectedGeneration = null"><X :size="17" /></button></header>
        <div class="creation-detail-media"><div v-if="selectedGeneration.mode === 'chat' && selectedGeneration.outputText" class="creation-detail-text">{{ selectedGeneration.outputText }}</div><AssetMedia v-else-if="selectedGeneration.outputMediaUrl" :src="selectedGeneration.outputMediaUrl" :kind="generationMediaKind(selectedGeneration)" :alt="selectedGeneration.prompt" :text="selectedGeneration.outputText || ''" :width="1200" :height="900" :controls="generationMediaKind(selectedGeneration) === 'video' || generationMediaKind(selectedGeneration) === 'audio'" /><div v-else><LoaderCircle v-if="['queued', 'running'].includes(selectedGeneration.status)" class="spin" :size="24" /><strong>{{ selectedGeneration.progress }}%</strong></div></div>
        <p class="creation-detail-prompt">{{ selectedGeneration.prompt }}</p>
        <dl><div><dt>{{ t('create.modelLabel') }}</dt><dd>{{ selectedGeneration.modelName }}</dd></div><div><dt>{{ t('create.studio.outputSettings') }}</dt><dd>{{ parameterSummary(selectedGeneration) }}</dd></div><div><dt>{{ t('workspace.cost') }}</dt><dd>{{ generationCost(selectedGeneration) }}</dd></div><div><dt>{{ t('workspace.created') }}</dt><dd>{{ generationDate(selectedGeneration.createdAt) }}</dd></div></dl>
        <p v-if="selectedGeneration.errorMessage" class="creation-detail-error"><AlertCircle :size="15" />{{ selectedGeneration.errorMessage }}</p>
        <footer><button type="button" @click="toggleFavorite(selectedGeneration)"><MotionFavoriteIcon :active="selectedGeneration.isFavorite" kind="bookmark" :size="15" />{{ selectedGeneration.isFavorite ? t('workspace.unfavoriteGeneration') : t('workspace.favoriteGeneration') }}</button><button v-if="selectedGeneration.actions.canReuse" type="button" @click="reuseGeneration(selectedGeneration)"><RefreshCw :size="15" />{{ t('actions.remix') }}</button><a v-if="selectedGeneration.actions.canDownload && selectedGeneration.actions.downloadPath" :href="selectedGeneration.actions.downloadPath"><ArrowUp :size="15" />{{ t('actions.download') }}</a><button v-if="selectedGeneration.actions.canCancel" type="button" @click="changeGeneration(selectedGeneration, 'cancel')"><X :size="15" />{{ t('actions.cancel') }}</button><button v-if="selectedGeneration.actions.canRetry" type="button" @click="changeGeneration(selectedGeneration, 'retry')"><RefreshCw :size="15" />{{ t('actions.retry') }}</button></footer>
      </aside>
    </div>
  </section>
</template>

<style scoped lang="scss">
.creation-studio-new {
  --studio-accent: var(--accent);
  --studio-accent-soft: var(--accent-soft);
  --studio-composer-bg: var(--surface);
  position: relative;
  height: calc(100dvh - 96px);
  min-height: 620px;
  display: grid;
  grid-template-rows: auto minmax(0, 1fr) auto;
  overflow: hidden;
  background: var(--surface);
  color: var(--text);
}
.creation-header { min-height: 48px; height: 48px; box-sizing: border-box; display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 0 clamp(18px, 4vw, 48px); border-bottom: 1px solid var(--border); background: var(--surface); }
.creation-heading { min-width: 0; display: flex; align-items: center; gap: 8px; }
.creation-mark { width: 28px; height: 28px; display: grid; place-items: center; flex: 0 0 28px; border-radius: 8px; background: var(--studio-accent-soft); color: var(--studio-accent); }
.creation-title { color: var(--text); font-size: 13px; font-weight: 650; line-height: 1; }
.creation-heading-divider { color: var(--text-tertiary); font-size: 12px; line-height: 1; }
.creation-current-mode { min-width: 0; display: inline-flex; align-items: center; gap: 5px; overflow: hidden; color: var(--text-secondary); font-size: 11px; line-height: 1; text-overflow: ellipsis; white-space: nowrap; }
.creation-header-actions { min-width: 0; display: flex; align-items: center; gap: 3px; padding: 3px; border: 1px solid var(--border); border-radius: 11px; background: var(--surface-muted); }
.creation-current-conversation { min-width: 0; max-width: min(32vw, 250px); height: 28px; box-sizing: border-box; display: inline-flex; align-items: center; gap: 7px; padding: 0 8px; overflow: hidden; border-radius: 8px; color: var(--text); font-size: 11px; font-weight: 600; line-height: 1; }
.creation-current-conversation > svg { flex: 0 0 auto; color: var(--accent); }
.creation-current-conversation-label { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.creation-history-panel { position: absolute; z-index: 20; top: 48px; right: 0; width: min(360px, calc(100% - 24px)); max-height: min(620px, calc(100dvh - 72px)); box-sizing: border-box; display: flex; flex-direction: column; padding: 14px; overflow: auto; border: 1px solid var(--border-strong); border-top: 0; border-radius: 0 0 12px 12px; background: var(--surface); box-shadow: 0 16px 36px rgb(10 23 52 / 12%); }
.creation-history-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; padding: 2px 2px 12px; }
.creation-history-header > div { min-width: 0; display: grid; gap: 4px; }
.creation-history-header span { color: var(--text-tertiary); font-size: 9px; font-weight: 700; letter-spacing: .08em; text-transform: uppercase; }
.creation-history-header strong { color: var(--text); font-size: 15px; font-weight: 650; }
.creation-history-new { min-height: 36px; display: flex; align-items: center; justify-content: center; gap: 7px; margin: 0 0 10px; padding: 0 12px; border: 1px solid var(--border-strong); border-radius: 8px; background: var(--surface-muted); color: var(--text); font-size: 12px; font-weight: 650; }
.creation-history-new:hover { border-color: var(--accent); color: var(--accent-readable); }
.creation-history-new:disabled { cursor: wait; opacity: .65; }
.creation-history-list { display: grid; gap: 4px; }
.creation-history-item { min-width: 0; display: flex; align-items: center; justify-content: space-between; gap: 10px; padding: 10px; border: 1px solid transparent; border-radius: 8px; background: transparent; color: var(--text); text-align: left; }
.creation-history-item:hover { background: var(--surface-muted); }
.creation-history-item.active { border-color: var(--accent); background: var(--accent-soft); }
.creation-history-item > svg { flex: 0 0 auto; color: var(--text-tertiary); }
.creation-history-item-main { min-width: 0; display: grid; gap: 4px; }
.creation-history-item-main strong { overflow: hidden; font-size: 12px; font-weight: 650; text-overflow: ellipsis; white-space: nowrap; }
.creation-history-item-main small { overflow: hidden; color: var(--text-tertiary); font-size: 10px; text-overflow: ellipsis; white-space: nowrap; }
.creation-history-empty { min-height: 84px; display: flex; align-items: center; justify-content: center; gap: 8px; color: var(--text-tertiary); font-size: 11px; text-align: center; }
.creation-composer-actions { min-width: 0; display: flex; align-items: center; justify-content: flex-end; gap: 8px; }
.creation-model-picker { min-width: 0; display: grid; grid-template-columns: auto minmax(120px, 1fr); align-items: center; gap: 7px; color: var(--text-tertiary); font-size: 10px; }
.creation-model-picker > span { white-space: nowrap; }
.creation-model-picker select { width: min(190px, 20vw); height: 34px; background-color: var(--surface-muted); font-size: 11px; }
.creation-icon-link:hover, .creation-icon-link.active { background: var(--surface); color: var(--text); box-shadow: var(--shadow-xs); }
.creation-icon-link { width: 30px; height: 30px; display: grid; place-items: center; flex: 0 0 30px; padding: 0; border: 0; border-radius: 8px; background: transparent; color: var(--text-secondary); line-height: 0; }
.creation-main { min-height: 0; overflow: auto; padding: 20px clamp(16px, 5vw, 72px) 20px; overscroll-behavior: contain; background: var(--surface); }
.creation-main-content { width: min(100%, 900px); min-height: 100%; margin: 0 auto; }
.creation-notices { display: grid; gap: 8px; margin: 0 0 16px; }
.creation-conversation { min-height: 100%; }
.creation-empty { min-height: min(58dvh, 520px); display: grid; place-content: center; justify-items: center; gap: 10px; padding: 32px; text-align: center; }
.creation-empty-mark { width: 60px; height: 60px; display: grid; place-items: center; border: 1px solid var(--border-strong); border-radius: 16px; background: var(--surface); color: var(--studio-accent); }
.creation-empty strong { font-size: 20px; font-weight: 600; }
.creation-empty p, .creation-empty small { max-width: 470px; margin: 0; color: var(--text-secondary); font-size: 13px; line-height: 1.5; }
.creation-empty small { color: var(--text-tertiary); font-size: 11px; }
.creation-loading { display: grid; gap: 14px; padding-top: 18px; }
.creation-loading span { display: block; height: 72px; border-radius: 12px; background: linear-gradient(90deg, var(--surface-muted), var(--surface), var(--surface-muted)); background-size: 220% 100%; animation: creation-shimmer 1.3s ease infinite; }
@keyframes creation-shimmer { to { background-position: -220% 0; } }
.creation-feed { display: grid; gap: 28px; }
.creation-turn { display: grid; gap: 12px; }
.creation-prompt { width: min(78%, 660px); margin-left: auto; }
.creation-prompt > div { display: flex; align-items: center; justify-content: flex-end; gap: 10px; margin-bottom: 6px; color: var(--text-tertiary); font-size: 10px; }
.creation-mode-label { display: inline-flex; align-items: center; gap: 5px; color: var(--studio-accent); font-weight: 650; }
.creation-prompt p { margin: 0; padding: 11px 14px; border-radius: 14px 14px 4px 14px; background: var(--studio-accent-soft); color: var(--text); font-size: 13px; line-height: 1.55; white-space: pre-wrap; }
.creation-result { min-width: 0; cursor: pointer; }
.creation-assistant { min-width: 0; display: grid; grid-template-columns: 28px minmax(0, 1fr); gap: 10px; align-items: start; }
.creation-assistant-mark { width: 28px; height: 28px; margin-top: 1px; flex: 0 0 28px; border: 1px solid var(--border); border-radius: 7px; background: var(--surface); }
.creation-assistant-body { min-width: 0; }
.creation-result-head { min-height: 26px; display: flex; align-items: center; gap: 8px; color: var(--text-tertiary); font-size: 10px; }
.creation-result-head > span:nth-child(2) { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.creation-result-head button { width: 28px; height: 28px; display: grid; place-items: center; margin-left: auto; padding: 0; border: 0; border-radius: 7px; background: transparent; color: var(--text-tertiary); line-height: 0; }
.creation-result-head button > svg, .creation-tool-button > svg { display: block; }
.creation-result-head button:hover { background: var(--surface-muted); color: var(--text); }
.creation-status { color: var(--text-secondary); font-weight: 650; }
.creation-status[data-status='succeeded'] { color: var(--success); }
.creation-status[data-status='failed'] { color: var(--danger); }
.creation-output-box { width: min(100%, 760px); box-sizing: border-box; padding: 14px 16px; border: 1px solid var(--border); border-radius: 12px; background: var(--surface-muted); }
.creation-output-box .creation-text { max-width: none; }
.creation-text { max-width: 760px; color: var(--text); font-size: 15px; line-height: 1.7; white-space: pre-wrap; overflow-wrap: anywhere; }
.creation-media { width: min(100%, 760px); overflow: hidden; border: 1px solid var(--border); border-radius: 12px; background: var(--surface-muted); }
.creation-media :deep(.asset-renderer) { display: flex; justify-content: center; width: 100%; }
.creation-media :deep(img), .creation-media :deep(video) { display: block; width: 100%; height: auto; max-height: min(62dvh, 720px); object-fit: contain; }
.creation-media :deep(.asset-audio) { width: 100%; padding: 28px; box-sizing: border-box; }
.creation-progress { min-height: 44px; display: inline-flex; align-items: center; gap: 8px; color: var(--text-secondary); font-size: 12px; }
.creation-result footer { display: flex; gap: 12px; padding-top: 8px; color: var(--text-tertiary); font-size: 10px; }
.creation-composer { position: relative; width: min(calc(100% - 32px), 900px); margin: 0 auto 18px; padding: 10px 12px 9px; border: 1px solid var(--border-strong); border-radius: 20px; background: var(--studio-composer-bg); box-shadow: 0 8px 28px rgb(20 33 61 / 9%); }
.creation-composer textarea { width: 100%; min-height: 46px; max-height: 140px; display: block; box-sizing: border-box; resize: none; border: 0; outline: 0; background: transparent; color: var(--text); padding: 6px 7px 9px; font: inherit; font-size: 14px; line-height: 1.5; }
.creation-composer textarea::placeholder { color: var(--text-tertiary); }
.creation-composer-row { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.creation-composer-tools { min-width: 0; display: flex; align-items: center; gap: 7px; }
.creation-tool-button { width: 32px; height: 32px; display: grid; place-items: center; flex: 0 0 auto; padding: 0; border: 1px solid var(--border); border-radius: 50%; background: var(--surface-muted); color: var(--text-secondary); line-height: 0; }
.creation-tool-button:hover, .creation-tool-button.active { border-color: var(--studio-accent); background: var(--studio-accent-soft); color: var(--studio-accent); }
.creation-mode-chip { height: 32px; display: inline-flex; align-items: center; gap: 5px; box-sizing: border-box; padding: 0 7px 0 9px; border: 1px solid color-mix(in srgb, var(--studio-accent) 35%, var(--border)); border-radius: 999px; background: var(--studio-accent-soft); color: var(--studio-accent); font-size: 11px; font-weight: 650; line-height: 1; }
.creation-mode-chip > svg, .creation-mode-chip-label { display: block; flex: 0 0 auto; }
.creation-mode-chip-label { line-height: 1; }
.creation-mode-chip button { width: 22px; height: 22px; display: grid; place-items: center; flex: 0 0 22px; margin: 0; padding: 0; border: 0; border-radius: 50%; background: transparent; color: inherit; }
.creation-mode-chip button:hover { background: rgb(0 0 0 / 10%); }
.creation-submit { min-height: 34px; display: inline-flex; align-items: center; justify-content: center; gap: 7px; padding: 0 13px; border: 0; border-radius: 10px; background: var(--studio-accent); color: var(--accent-contrast); font-size: 12px; font-weight: 650; }
.creation-submit:hover:not(:disabled) { background: var(--accent-hover); }
.creation-submit:disabled { background: var(--surface-muted); color: var(--text-tertiary); cursor: not-allowed; }
.creation-notice { width: 100%; min-width: 0; min-height: 42px; box-sizing: border-box; display: flex; align-items: flex-start; gap: 8px; margin: 0; padding: 10px 12px; border: 1px solid var(--border); border-radius: 9px; font-size: 11px; line-height: 1.45; overflow-wrap: anywhere; }
.creation-notice.error { color: var(--danger); }
.creation-notice.success { color: var(--success); }
.creation-notice span { min-width: 0; flex: 1; }
.creation-notice button { width: 24px; height: 24px; display: grid; place-items: center; flex: 0 0 24px; margin: -2px -4px -2px 0; padding: 0; border: 0; border-radius: 6px; background: transparent; color: inherit; line-height: 0; }
.creation-popover { position: absolute; z-index: 20; bottom: calc(100% + 10px); left: 12px; width: min(300px, calc(100vw - 32px)); box-sizing: border-box; padding: 8px; border: 1px solid var(--border-strong); border-radius: 14px; background: var(--surface-raised); box-shadow: var(--shadow-sm); }
.creation-tools-menu { max-height: min(460px, calc(100dvh - 230px)); overflow: auto; }
.creation-menu-group { display: grid; gap: 2px; }
.creation-menu-divider { height: 1px; margin: 7px 4px; background: var(--border); }
.creation-menu-heading { padding: 7px 9px 5px; color: var(--text-tertiary); font-size: 10px; font-weight: 700; letter-spacing: .04em; text-transform: uppercase; }
.creation-menu-item { width: 100%; min-height: 42px; display: flex; align-items: center; gap: 10px; padding: 7px 9px; border: 0; border-radius: 9px; background: transparent; color: var(--text-secondary); text-align: left; }
.creation-menu-item:hover, .creation-menu-item.selected { background: var(--surface-muted); color: var(--text); }
.creation-menu-item:disabled, .creation-menu-item.unavailable { cursor: not-allowed; opacity: .42; }
.creation-menu-item:disabled:hover { background: transparent; color: var(--text-tertiary); }
.creation-menu-item > span:not(.creation-menu-icon) { min-width: 0; display: grid; gap: 2px; }
.creation-menu-item strong { color: inherit; font-size: 12px; font-weight: 650; }
.creation-menu-item small { overflow: hidden; color: var(--text-tertiary); font-size: 10px; text-overflow: ellipsis; white-space: nowrap; }
.creation-menu-icon { width: 20px; display: grid; place-items: center; color: var(--studio-accent); }
.creation-menu-check { margin-left: auto; color: var(--studio-accent); }
.creation-settings { width: min(260px, calc(100vw - 32px)); display: grid; gap: 10px; padding: 13px; }
.creation-settings label { display: grid; gap: 5px; color: var(--text-secondary); font-size: 11px; }
.creation-settings select, .creation-settings input { width: 100%; height: 34px; box-sizing: border-box; border: 1px solid var(--border); border-radius: 8px; background: var(--surface); color: var(--text); padding: 0 9px; font: inherit; font-size: 12px; }
.creation-assets { width: min(360px, calc(100vw - 32px)); }
.creation-assets > header { display: flex; align-items: flex-start; justify-content: space-between; gap: 10px; padding: 5px 5px 10px; }
.creation-assets > header > div { display: grid; gap: 3px; }
.creation-assets > header strong { font-size: 13px; }
.creation-assets > header small { color: var(--text-tertiary); font-size: 10px; }
.creation-assets > header button { width: 28px; height: 28px; display: grid; place-items: center; padding: 0; border: 0; border-radius: 7px; background: transparent; color: var(--text-secondary); line-height: 0; }
.creation-asset-actions button { min-height: 32px; display: inline-flex; align-items: center; gap: 6px; padding: 0 10px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface-muted); color: var(--text-secondary); font-size: 11px; }
.creation-asset-list { max-height: 250px; display: grid; gap: 3px; overflow: auto; margin-top: 9px; }
.creation-asset-list button { min-width: 0; display: flex; align-items: center; gap: 8px; padding: 5px; border: 0; border-radius: 8px; background: transparent; color: var(--text); text-align: left; }
.creation-asset-list button:hover, .creation-asset-list button.selected { background: var(--surface-muted); }
.creation-asset-list :deep(.asset-renderer) { width: 42px; height: 42px; overflow: hidden; border-radius: 6px; background: var(--surface-muted); }
.creation-asset-list :deep(img) { width: 100%; height: 100%; object-fit: cover; }
.creation-asset-list button > span { min-width: 0; display: grid; gap: 2px; flex: 1; }
.creation-asset-list strong { overflow: hidden; font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
.creation-asset-list small { color: var(--text-tertiary); font-size: 9px; }
.creation-asset-list > button > svg { color: var(--studio-accent); }
.creation-asset-empty { min-height: 64px; display: flex; align-items: center; justify-content: center; gap: 6px; color: var(--text-tertiary); font-size: 11px; }
.creation-detail-backdrop { position: fixed; z-index: 90; inset: 0; display: flex; justify-content: flex-end; background: rgb(8 12 20 / 54%); }
.creation-detail { width: min(480px, 100%); height: 100%; display: grid; grid-template-rows: auto auto auto auto 1fr; gap: 14px; overflow: auto; box-sizing: border-box; padding: 20px; border-left: 1px solid var(--border); background: var(--surface); }
.creation-detail > header { display: flex; justify-content: space-between; align-items: flex-start; }
.creation-detail > header span { color: var(--studio-accent); font-size: 10px; font-weight: 700; }
.creation-detail h2 { margin: 4px 0 0; font-size: 18px; }
.creation-detail-media { min-height: 260px; display: grid; place-items: center; overflow: hidden; border: 1px solid var(--border); border-radius: 10px; background: var(--surface-muted); }
.creation-detail-media :deep(.asset-renderer), .creation-detail-media :deep(img), .creation-detail-media :deep(video) { width: 100%; max-height: 52dvh; object-fit: contain; }
.creation-detail-text { width: 100%; max-height: 48dvh; overflow: auto; box-sizing: border-box; padding: 18px; white-space: pre-wrap; color: var(--text); font-size: 13px; line-height: 1.6; }
.creation-detail-prompt { margin: 0; color: var(--text-secondary); font-size: 12px; line-height: 1.55; white-space: pre-wrap; }
.creation-detail dl { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 7px; margin: 0; }
.creation-detail dl div { min-width: 0; display: grid; gap: 3px; padding: 9px; border-radius: 8px; background: var(--surface-muted); }
.creation-detail dt { color: var(--text-tertiary); font-size: 9px; }
.creation-detail dd { overflow: hidden; margin: 0; color: var(--text); font-size: 10px; text-overflow: ellipsis; white-space: nowrap; }
.creation-detail-error { display: flex; gap: 7px; margin: 0; color: var(--danger); font-size: 11px; }
.creation-detail footer { display: flex; flex-wrap: wrap; align-content: flex-end; gap: 7px; }
.creation-detail footer :is(button, a) { min-height: 34px; display: inline-flex; align-items: center; gap: 6px; padding: 0 10px; border: 0; border-radius: 8px; background: var(--surface-muted); color: var(--text-secondary); font-size: 11px; font-weight: 650; }
.creation-detail footer :is(button, a):hover { color: var(--text); }
.creation-auth { max-width: 620px; margin: 36px auto; }
.creation-popover-enter-active { transition: opacity var(--dropdown-open-dur) var(--dropdown-ease), transform var(--dropdown-open-dur) var(--dropdown-ease); }
.creation-popover-leave-active { transition: opacity var(--dropdown-close-dur) var(--dropdown-ease), transform var(--dropdown-close-dur) var(--dropdown-ease); }
.creation-popover-enter-from { opacity: 0; transform: scale(var(--dropdown-pre-scale)); }
.creation-popover-leave-to { opacity: 0; transform: scale(var(--dropdown-closing-scale)); }
.spin { animation: creation-spin 1s linear infinite; }
@keyframes creation-spin { to { transform: rotate(360deg); } }

@media (max-width: 700px) {
  .creation-studio-new { height: calc(100dvh - var(--creation-mobile-shell-offset, 124px)); min-height: 490px; }
  .creation-header { min-height: 44px; height: 44px; padding: 0 14px; }
  .creation-header-actions { max-width: 48%; }
  .creation-current-conversation { max-width: min(38vw, 156px); }
  .creation-history-panel { top: 44px; width: min(360px, calc(100% - 16px)); max-height: calc(100dvh - 68px); }
  .creation-mark { width: 26px; height: 26px; flex-basis: 26px; }
  .creation-title { font-size: 12px; }
  .creation-model-picker { grid-template-columns: minmax(0, 1fr); gap: 0; }
  .creation-model-picker > span { display: none; }
  .creation-model-picker select { width: 126px; }
  .creation-main { padding: 14px 12px 16px; }
  .creation-prompt { width: 92%; }
  .creation-text { font-size: 14px; }
  .creation-composer { width: calc(100% - 20px); margin-bottom: 10px; padding: 9px 10px 8px; border-radius: 18px; }
  .creation-composer textarea { min-height: 48px; font-size: 14px; }
  .creation-submit { width: 34px; height: 34px; min-height: 34px; padding: 0; border-radius: 50%; }
  .creation-submit span { display: none; }
  .creation-popover { left: 10px; width: min(300px, calc(100vw - 28px)); }
  .creation-settings { width: min(260px, calc(100vw - 28px)); }
  .creation-assets { width: min(360px, calc(100vw - 28px)); }
  .creation-detail { padding: 14px; }
}

@media (min-width: 768px) {
  .creation-studio-new {
    margin: calc(-1 * var(--space-page-y)) calc(-1 * var(--space-page-x)) -12px;
    height: calc(100% + var(--space-page-y) + 12px);
    border-radius: var(--radius-shell);
  }
}

@media (min-width: 768px) and (max-width: 1023px) {
  .creation-studio-new {
    height: calc(100% + 24px);
    margin: -16px -18px -8px;
  }
}

@media (max-width: 767px) {
  .creation-studio-new { margin-inline: -16px; margin-bottom: 0; }
}
</style>
