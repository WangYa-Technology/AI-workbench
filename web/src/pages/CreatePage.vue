<script setup lang="ts">
import { ArrowRight, Ban, Bot, BriefcaseBusiness, Check, ChevronRight, Eye, History, Image, LoaderCircle, MessageSquare, Music, PanelRight, Plus, RotateCcw, Send, Settings2, ShieldCheck, Sparkles, UserRound, Video, WalletCards, Wrench, X } from 'lucide-vue-next'
import { useIntervalFn } from '@vueuse/core'
import { computed, nextTick, onMounted, reactive, ref, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'
import { api, messageFrom, type Asset, type BillingStatement, type Generation, type TaskDetail, type Work } from '../api/client'
import { formatCurrency } from '../lib/format'
import { compilePrompt, MAX_COMPILED_PROMPT_LENGTH } from '../lib/promptCompiler'
import { useSessionStore } from '../stores/session'
import AssetMedia from '../components/domain/AssetMedia.vue'

type CreationMode = 'chat' | 'image' | 'video' | 'music'
type BuilderView = 'chat' | 'split' | 'preview'
type ConfigSection = 'role' | 'prompt' | 'tools'
type MessageRole = 'assistant' | 'user'

interface ChatMessage {
  id: number
  role: MessageRole
  content: string
}

interface RoleConfig {
  name: string
  objective: string
  audience: string
  tone: 'precise' | 'friendly' | 'creative'
}

interface PromptConfig {
  negativePrompt: string
  creativity: number
}

interface ToolConfig {
  sourceContext: boolean
  webResearch: boolean
  safetyReview: boolean
}

interface AgentConfig {
  role: RoleConfig
  prompt: PromptConfig
  tools: ToolConfig
}

interface ScrollableElement {
  scrollHeight: number
  scrollTo: (options: { top: number; behavior: 'smooth' }) => void
}

const { t, locale } = useI18n()
const route = useRoute()
const session = useSessionStore()
const prompt = ref('')
const sourceWork = ref<Work | null>(null)
const sourceTask = ref<TaskDetail | null>(null)
const sourceAsset = ref<Asset | null>(null)
const generation = ref<Generation | null>(null)
const billing = ref<BillingStatement | null>(null)
const submitting = ref(false)
const error = ref('')
const view = ref<BuilderView>('split')
const activeSection = ref<ConfigSection>('role')
const draft = ref('')
const isThinking = ref(false)
const contextOpen = ref(false)
const contextLoading = ref(false)
const contextAssets = ref<Asset[]>([])
const contextError = ref('')
const messageList = useTemplateRef<ScrollableElement>('messageList')
const config = reactive<AgentConfig>({
  role: { name: '', objective: '', audience: '', tone: 'precise' },
  prompt: { negativePrompt: '', creativity: 0.4 },
  tools: { sourceContext: true, webResearch: false, safetyReview: true },
})
const messages = ref<ChatMessage[]>([])

const modeNames: CreationMode[] = ['chat', 'image', 'video', 'music']
const activeMode = computed<CreationMode>(() => {
  const value = String(route.params.mode || 'image')
  return modeNames.includes(value as CreationMode) ? value as CreationMode : 'image'
})
const modes = computed(() => [
  { name: 'chat' as const, label: t('create.modes.chat'), icon: MessageSquare },
  { name: 'image' as const, label: t('create.modes.image'), icon: Image },
  { name: 'video' as const, label: t('create.modes.video'), icon: Video },
  { name: 'music' as const, label: t('create.modes.music'), icon: Music },
])
const modeMeta = computed(() => ({
  chat: { icon: MessageSquare, costCents: 2, kind: 'document', title: t('create.modeMeta.chat.title'), summary: t('create.modeMeta.chat.summary'), model: t('create.modeMeta.chat.model'), output: t('create.modeMeta.chat.output') },
  image: { icon: Image, costCents: 5, kind: 'image', title: t('create.modeMeta.image.title'), summary: t('create.modeMeta.image.summary'), model: t('create.modeMeta.image.model'), output: t('create.modeMeta.image.output') },
  video: { icon: Video, costCents: 20, kind: 'video', title: t('create.modeMeta.video.title'), summary: t('create.modeMeta.video.summary'), model: t('create.modeMeta.video.model'), output: t('create.modeMeta.video.output') },
  music: { icon: Music, costCents: 8, kind: 'audio', title: t('create.modeMeta.music.title'), summary: t('create.modeMeta.music.summary'), model: t('create.modeMeta.music.model'), output: t('create.modeMeta.music.output') },
})[activeMode.value])
const promptStarters = computed(() => ['first', 'second', 'third'].map((item) => t(`create.modeStarters.${activeMode.value}.${item}`)))
const modeWelcome = computed(() => t(`create.builder.modeWelcome.${activeMode.value}`))
const modeChatPlaceholder = computed(() => t(`create.builder.modePlaceholder.${activeMode.value}`))
const generateActionLabel = computed(() => activeMode.value === 'image'
  ? t('actions.generateImage')
  : t('actions.generateMode', { mode: t(`create.modes.${activeMode.value}`) }))

const finished = computed(() => generation.value?.status === 'succeeded')
const running = computed(() => Boolean(generation.value && ['queued', 'running'].includes(generation.value.status)))
const configSections = computed(() => [
  { id: 'role' as const, label: t('create.builder.role'), icon: UserRound },
  { id: 'prompt' as const, label: t('create.builder.prompt'), icon: Sparkles },
  { id: 'tools' as const, label: t('create.builder.tools'), icon: Wrench },
])
const activeSectionLabel = computed(() => configSections.value.find((item) => item.id === activeSection.value)?.label || '')
const completion = computed(() => {
  const required = [config.role.name, config.role.objective, prompt.value]
  return Math.round((required.filter((value) => value.trim()).length / required.length) * 100)
})
const floatingActions = computed(() => {
  if (view.value === 'chat') return [{ id: 'split', label: t('create.builder.openConfig'), icon: PanelRight }]
  if (view.value === 'preview') return [{ id: 'split', label: t('create.builder.backToBuild'), icon: Settings2 }]
  return [
    { id: 'chat', label: t('create.builder.focusChat'), icon: MessageSquare },
    { id: 'preview', label: t('create.builder.preview'), icon: Eye },
  ]
})
const activeToolCount = computed(() => Object.values(config.tools).filter(Boolean).length)
const optionalPromptSections = computed(() => [
  config.role.name ? `Agent: ${config.role.name}` : '',
  config.role.objective ? `Objective: ${config.role.objective}` : '',
  config.role.audience ? `Audience: ${config.role.audience}` : '',
  `Tone: ${config.role.tone}`,
  config.prompt.negativePrompt ? `Avoid: ${config.prompt.negativePrompt}` : '',
  `Creativity: ${config.prompt.creativity.toFixed(1)}`,
  config.tools.sourceContext ? 'Use the supplied source context when available.' : '',
  config.tools.webResearch ? 'Web research requested when supported by the provider.' : '',
  config.tools.safetyReview ? 'Apply a final safety and rights review.' : '',
])
const compiledPrompt = computed(() => compilePrompt(prompt.value, optionalPromptSections.value))

const { pause, resume } = useIntervalFn(async () => {
  if (!generation.value || !running.value) return
  try {
    generation.value = await api.getGeneration(generation.value.id)
    if (!running.value) {
      pause()
      billing.value = await api.billingStatement()
    }
  } catch (reason) {
    error.value = messageFrom(reason)
    pause()
  }
}, 800, { immediate: false })

async function scrollToLatest() {
  await nextTick()
  messageList.value?.scrollTo({ top: messageList.value.scrollHeight, behavior: 'smooth' })
}

function inferSection(content: string): ConfigSection {
  if (/tool|search|knowledge|code|工具|搜索|知识库|代码/i.test(content)) return 'tools'
  if (/prompt|instruction|style|提示词|指令|风格/i.test(content)) return 'prompt'
  return 'role'
}

function assistantReply(section: ConfigSection) {
  if (section === 'tools') return t('create.builder.toolReply')
  if (section === 'prompt') return t('create.builder.promptReply')
  return t('create.builder.roleReply')
}

async function sendGuideMessage() {
  const content = draft.value.trim()
  if (!content || isThinking.value) return
  messages.value.push({ id: Date.now(), role: 'user', content })
  draft.value = ''
  isThinking.value = true
  await scrollToLatest()

  // Local deterministic guidance keeps the chat and configuration panel synchronized.
  const section = inferSection(content)
  activeSection.value = section
  if (section === 'role' && !config.role.objective) config.role.objective = content
  if (section === 'prompt' && !prompt.value) prompt.value = content

  globalThis.window.setTimeout(async () => {
    messages.value.push({ id: Date.now() + 1, role: 'assistant', content: assistantReply(section) })
    isThinking.value = false
    await scrollToLatest()
  }, 420)
}

function continueConfiguration() {
  const order: ConfigSection[] = ['role', 'prompt', 'tools']
  const index = order.indexOf(activeSection.value)
  if (index < order.length - 1) {
    activeSection.value = order[index + 1]
    messages.value.push({ id: Date.now(), role: 'assistant', content: t(`create.builder.${activeSection.value}Step`) })
    void scrollToLatest()
  } else {
    view.value = 'preview'
  }
}

function setStarter(starter: string) {
  prompt.value = starter
  activeSection.value = 'prompt'
}

function setBuilderView(next: string) {
  if (['chat', 'split', 'preview'].includes(next)) view.value = next as BuilderView
}

async function toggleContextPicker() {
  contextOpen.value = !contextOpen.value
  if (!contextOpen.value || contextAssets.value.length) return
  contextLoading.value = true
  contextError.value = ''
  try {
    const user = await session.ensure()
    if (!user) throw new Error(t('create.builder.contextSignIn'))
    const response = await api.listAssets({ limit: 20 })
    contextAssets.value = response.items.filter((item) => item.scanStatus === 'clean')
  } catch (reason) {
    contextError.value = messageFrom(reason)
  } finally {
    contextLoading.value = false
  }
}

function attachContext(asset: Asset) {
  sourceAsset.value = asset
  sourceWork.value = null
  sourceTask.value = null
  contextOpen.value = false
  messages.value.push({ id: Date.now(), role: 'assistant', content: t('create.builder.contextAttached', { title: asset.title }) })
  void scrollToLatest()
}

async function loadSource() {
  const sourceWorkId = String(route.query.sourceWorkId || '')
  const sourceTaskId = String(route.query.taskId || '')
  const sourceAssetId = String(route.query.sourceAssetId || '')
  try {
    if (sourceWorkId) {
      sourceWork.value = await api.getWork(sourceWorkId)
      prompt.value = sourceWork.value.prompt || ''
    } else if (sourceTaskId) {
      sourceTask.value = await api.getTask(sourceTaskId)
      prompt.value = sourceTask.value.brief
    } else if (sourceAssetId) {
      sourceAsset.value = await api.getAsset(sourceAssetId)
      prompt.value = t('create.sourceAssetPrompt', { title: sourceAsset.value.title })
    }
  } catch (reason) {
    error.value = messageFrom(reason)
  }
}

async function cancelGeneration() {
  if (!generation.value) return
  error.value = ''
  try {
    generation.value = await api.cancelGeneration(generation.value.id, 'Cancelled from the AI creation workspace.')
    billing.value = await api.billingStatement()
    pause()
  } catch (reason) {
    error.value = messageFrom(reason)
  }
}

async function retryGeneration() {
  if (!generation.value) return
  error.value = ''
  submitting.value = true
  try {
    generation.value = await api.retryGeneration(generation.value.id)
    billing.value = await api.billingStatement()
    resume()
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    submitting.value = false
  }
}

async function submit() {
  error.value = ''
  if (prompt.value.trim().length < 3) {
    error.value = t('create.promptError')
    return
  }
  submitting.value = true
  try {
    const user = await session.ensure()
    if (!user) throw new Error(session.error || t('status.authenticationFailed'))
    generation.value = await api.createGeneration({
      mode: activeMode.value,
      prompt: compiledPrompt.value,
      sourceWorkId: sourceWork.value?.id || null,
      sourceTaskId: sourceTask.value?.id || null,
      sourceAssetId: sourceAsset.value?.id || null,
    })
    view.value = 'preview'
    resume()
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    submitting.value = false
  }
}

function startAgain() {
  generation.value = null
  error.value = ''
  prompt.value = ''
}

watch(activeMode, () => {
  pause()
  generation.value = null
  error.value = ''
  const nextMessage = messages.value.some((message) => message.role === 'user')
    ? t(`create.builder.modeChanged.${activeMode.value}`)
    : modeWelcome.value
  if (messages.value.some((message) => message.role === 'user')) {
    messages.value.push({ id: Date.now(), role: 'assistant', content: nextMessage })
  } else {
    messages.value = [{ id: Date.now(), role: 'assistant', content: nextMessage }]
  }
  void scrollToLatest()
})

onMounted(() => {
  messages.value = [{ id: Date.now(), role: 'assistant', content: modeWelcome.value }]
  void session.ensure()
  void loadSource()
  void api.billingStatement().then((result) => { billing.value = result }).catch(() => undefined)
})
</script>

<template>
  <section class="builder-page">
    <div class="builder-shell content-width" :data-view="view">
      <header class="builder-toolbar">
        <div class="builder-identity">
          <span><Sparkles :size="17" /></span>
          <div><strong>{{ t('create.builder.title') }}</strong><small>{{ t('create.builder.subtitle') }}</small></div>
        </div>

        <nav class="builder-modes" :aria-label="t('create.modeLabel')">
          <RouterLink v-for="mode in modes" :key="mode.name" :to="`/create/${mode.name}`" :class="{ active: activeMode === mode.name }">
            <component :is="mode.icon" :size="16" /><span>{{ mode.label }}</span>
          </RouterLink>
        </nav>

        <div class="builder-progress" :aria-label="t('create.builder.progress', { value: completion })">
          <span>{{ completion }}%</span>
        </div>
        <RouterLink class="icon-button" to="/workspace/generations" :aria-label="t('create.recentGenerations')" :title="t('create.recentGenerations')">
          <History :size="18" />
        </RouterLink>
      </header>

      <main class="builder-body">
        <Transition name="builder-column">
          <section v-if="view !== 'preview'" class="builder-chat">
            <header class="builder-panel-header">
              <div><span>{{ t('create.builder.guideLabel') }}</span><h1>{{ modeMeta.title }}</h1><p>{{ modeMeta.summary }}</p></div>
              <small><i></i>{{ t('create.builder.localGuide') }}</small>
            </header>

            <div ref="messageList" class="builder-messages">
              <TransitionGroup name="builder-message">
                <article v-for="message in messages" :key="message.id" :data-role="message.role">
                  <span class="message-avatar"><Bot v-if="message.role === 'assistant'" :size="16" /><UserRound v-else :size="16" /></span>
                  <div><small>{{ message.role === 'assistant' ? t('create.builder.guideName') : t('create.builder.you') }}</small><p>{{ message.content }}</p></div>
                </article>
              </TransitionGroup>
              <article v-if="isThinking" data-role="assistant">
                <span class="message-avatar"><Bot :size="16" /></span><span class="thinking"><i></i><i></i><i></i></span>
              </article>
            </div>

            <div class="builder-starters" :aria-label="t('create.startersLabel')">
              <button v-for="starter in promptStarters" :key="starter" type="button" @click="setStarter(starter)">
                {{ starter }}
              </button>
            </div>
            <form class="builder-composer" @submit.prevent="sendGuideMessage">
              <textarea v-model="draft" rows="2" maxlength="1000" :placeholder="modeChatPlaceholder" @keydown.enter.exact.prevent="sendGuideMessage"></textarea>
              <div>
                <button class="icon-button" type="button" :aria-label="t('create.builder.addContext')" :title="t('create.builder.addContext')" :aria-expanded="contextOpen" @click="toggleContextPicker">
                  <Plus :size="18" />
                </button><small>{{ t('create.builder.enterToSend') }}</small><button class="builder-send" type="submit" :disabled="!draft.trim() || isThinking" :aria-label="t('create.builder.send')">
                  <Send :size="17" />
                </button>
              </div>
            </form>
            <Transition name="builder-panel">
              <section v-if="contextOpen" class="builder-context-picker" aria-live="polite">
                <header>
                  <div><strong>{{ t('create.builder.contextTitle') }}</strong><small>{{ t('create.builder.contextSummary') }}</small></div><button class="icon-button" type="button" :aria-label="t('actions.close')" @click="contextOpen = false">
                    <X :size="16" />
                  </button>
                </header>
                <p v-if="contextLoading">
                  {{ t('create.builder.contextLoading') }}
                </p>
                <p v-else-if="contextError" class="form-error" role="alert">
                  {{ contextError }}
                </p>
                <div v-else-if="contextAssets.length" class="builder-context-list">
                  <button v-for="asset in contextAssets" :key="asset.id" type="button" @click="attachContext(asset)">
                    <span><component :is="asset.kind === 'video' ? Video : asset.kind === 'audio' ? Music : Image" :size="16" /></span><span><strong>{{ asset.title }}</strong><small>{{ asset.kind }} · v{{ asset.versionNumber }}</small></span><ChevronRight :size="16" />
                  </button>
                </div>
                <div v-else class="builder-context-empty">
                  <p>
                    {{ t('create.builder.contextEmpty') }}
                  </p><RouterLink class="text-link" to="/workspace/assets">
                    {{ t('create.builder.manageAssets') }}
                  </RouterLink>
                </div>
              </section>
            </Transition>
          </section>
        </Transition>

        <Transition name="builder-column">
          <aside v-if="view !== 'chat'" class="builder-config">
            <template v-if="view === 'split'">
              <header class="builder-panel-header config-heading">
                <div><span>{{ t('create.builder.configLabel') }}</span><h2>{{ activeSectionLabel }}</h2></div>
                <button class="icon-button" type="button" :aria-label="t('actions.close')" :title="t('actions.close')" @click="view = 'chat'">
                  <X :size="18" />
                </button>
              </header>

              <nav class="builder-tabs" :aria-label="t('create.builder.configLabel')">
                <button v-for="section in configSections" :key="section.id" type="button" :class="{ active: activeSection === section.id }" @click="activeSection = section.id">
                  <component :is="section.icon" :size="16" /><span>{{ section.label }}</span><Check v-if="section.id === 'role' && config.role.name || section.id === 'prompt' && prompt" class="tab-check" :size="13" />
                </button>
              </nav>

              <Transition name="builder-panel" mode="out-in">
                <form :key="activeSection" class="builder-form" @submit.prevent="submit">
                  <label class="builder-primary-prompt"><span>{{ t('create.promptLabel') }}</span><textarea v-model="prompt" rows="5" :maxlength="MAX_COMPILED_PROMPT_LENGTH" :placeholder="t('create.promptPlaceholder')" :disabled="submitting || running || finished"></textarea></label>
                  <div class="field-meta">
                    <span>{{ t('create.builder.compiledLength', { value: compiledPrompt.length, max: MAX_COMPILED_PROMPT_LENGTH }) }}</span><span>{{ t('create.costEstimate', { cost: formatCurrency(modeMeta.costCents, 'USD', locale) }) }}</span>
                  </div>
                  <template v-if="activeSection === 'role'">
                    <label><span>{{ t('create.builder.agentName') }}</span><input v-model="config.role.name" maxlength="120" :placeholder="t('create.builder.agentNamePlaceholder')" /></label>
                    <label><span>{{ t('create.builder.objective') }}</span><textarea v-model="config.role.objective" rows="4" maxlength="500" :placeholder="t('create.builder.objectivePlaceholder')"></textarea></label>
                    <div class="builder-field-pair">
                      <label><span>{{ t('create.builder.audience') }}</span><input v-model="config.role.audience" maxlength="160" :placeholder="t('create.builder.audiencePlaceholder')" /></label>
                      <label><span>{{ t('create.builder.tone') }}</span><select v-model="config.role.tone"><option value="precise">{{ t('create.builder.tones.precise') }}</option><option value="friendly">{{ t('create.builder.tones.friendly') }}</option><option value="creative">{{ t('create.builder.tones.creative') }}</option></select></label>
                    </div>
                  </template>

                  <template v-else-if="activeSection === 'prompt'">
                    <label><span>{{ t('create.builder.avoid') }}</span><textarea v-model="config.prompt.negativePrompt" rows="3" maxlength="500" :placeholder="t('create.builder.avoidPlaceholder')"></textarea></label>
                    <label class="builder-range"><span>{{ t('create.builder.creativity') }} <b>{{ config.prompt.creativity.toFixed(1) }}</b></span><input v-model.number="config.prompt.creativity" type="range" min="0" max="1" step="0.1" /></label>
                  </template>

                  <template v-else>
                    <label class="builder-tool"><span><strong>{{ t('create.builder.sourceContext') }}</strong><small>{{ t('create.builder.sourceContextSummary') }}</small></span><input v-model="config.tools.sourceContext" type="checkbox" /></label>
                    <label class="builder-tool"><span><strong>{{ t('create.builder.webResearch') }}</strong><small>{{ t('create.builder.webResearchSummary') }}</small></span><input v-model="config.tools.webResearch" type="checkbox" /></label>
                    <label class="builder-tool"><span><strong>{{ t('create.builder.safetyReview') }}</strong><small>{{ t('create.builder.safetyReviewSummary') }}</small></span><input v-model="config.tools.safetyReview" type="checkbox" /></label>
                    <dl class="builder-evidence">
                      <div><dt>{{ t('create.modelLabel') }}</dt><dd>{{ modeMeta.model }}</dd></div><div><dt>{{ t('create.outputLabel') }}</dt><dd>{{ modeMeta.output }}</dd></div><div><dt>{{ t('create.queueLabel') }}</dt><dd>{{ t('create.queueValue') }}</dd></div>
                    </dl>
                  </template>

                  <div v-if="sourceWork || sourceTask || sourceAsset" class="builder-source source-reference">
                    <BriefcaseBusiness v-if="sourceTask" :size="18" /><ShieldCheck v-else :size="18" />
                    <span><small>{{ sourceTask ? t('create.taskContext') : sourceAsset ? t('create.sourceAsset') : t('create.remixing') }}</small><strong>{{ sourceTask?.title || sourceAsset?.title || sourceWork?.title }}</strong></span>
                  </div>
                  <p v-if="error" class="form-error" role="alert">
                    {{ error }}
                  </p>

                  <div class="builder-form-actions">
                    <button v-if="activeSection !== 'tools'" class="builder-continue" type="button" @click="continueConfiguration">
                      {{ t('create.builder.continue') }}<ChevronRight :size="17" />
                    </button>
                    <button v-if="!generation || running" class="builder-continue primary" type="submit" :disabled="submitting || running || prompt.trim().length < 3">
                      <LoaderCircle v-if="submitting || running" class="spin" :size="17" /><component :is="modeMeta.icon" v-else :size="17" />{{ running ? t('actions.generating') : generateActionLabel }}
                    </button>
                  </div>
                  <button v-if="running" class="builder-continue" type="button" @click="cancelGeneration">
                    <Ban :size="17" />{{ t('actions.cancel') }}
                  </button>
                  <button v-if="generation && ['failed', 'cancelled'].includes(generation.status)" class="builder-continue" type="button" :disabled="submitting" @click="retryGeneration">
                    <RotateCcw :size="17" />{{ t('actions.retry') }}
                  </button>
                </form>
              </Transition>

              <footer class="builder-config-footer">
                <ShieldCheck :size="15" /><span>{{ t('create.providerNote') }}<strong v-if="billing"><span>{{ t('workspace.availableCredits') }}</span><span aria-hidden="true"> · </span><span>{{ formatCurrency(billing.account.availableCents, billing.account.currency, locale) }}</span></strong></span><RouterLink to="/workspace/billing" :aria-label="t('workspace.viewStatement')" :title="t('workspace.viewStatement')">
                  <WalletCards :size="16" />
                </RouterLink>
              </footer>
            </template>

            <section v-else class="builder-preview">
              <header><div><span>{{ t('create.builder.previewLabel') }}</span><h2>{{ config.role.name || t('create.builder.untitled') }}</h2></div><small>{{ activeToolCount }} {{ t('create.builder.modulesEnabled') }}</small></header>
              <div class="builder-stage">
                <AssetMedia v-if="finished && generation?.outputMediaUrl" :src="generation.outputMediaUrl" :kind="modeMeta.kind" :alt="generation.prompt" :text="generation.outputText" :width="2000" :height="2500" eager />
                <div v-else class="builder-stage-empty">
                  <span><component :is="modeMeta.icon" :size="24" /></span><h3>{{ running ? t('actions.generating') : t('create.canvasTitle') }}</h3><p>{{ config.role.objective || modeMeta.summary }}</p>
                </div>
                <div v-if="generation" class="builder-generation-status" :data-status="generation.status">
                  <LoaderCircle v-if="running" class="spin" :size="17" /><Check v-else-if="finished" :size="17" /><span>{{ t(`generation.status.${generation.status}`) }} · {{ generation.progress }}%</span><strong>{{ t('status.localProvider') }}</strong>
                </div>
              </div>
              <dl class="builder-preview-meta">
                <div><dt>{{ t('create.modelLabel') }}</dt><dd>{{ modeMeta.model }}</dd></div><div><dt>{{ t('create.builder.tone') }}</dt><dd>{{ config.role.tone }}</dd></div><div><dt>{{ t('create.builder.status') }}</dt><dd>{{ completion === 100 ? t('create.builder.ready') : t('create.builder.draft') }}</dd></div>
              </dl>
              <p v-if="error" class="form-error" role="alert">
                {{ error }}
              </p>
              <div class="builder-preview-actions">
                <RouterLink class="builder-continue" to="/workspace/billing">
                  <WalletCards :size="17" />{{ t('workspace.viewStatement') }}
                </RouterLink>
                <button v-if="!generation" class="builder-continue primary" type="button" :disabled="submitting" @click="submit">
                  <component :is="modeMeta.icon" :size="17" />{{ generateActionLabel }}
                </button>
                <button v-if="running" class="builder-continue" type="button" @click="cancelGeneration">
                  <Ban :size="17" />{{ t('actions.cancel') }}
                </button>
                <RouterLink v-else-if="finished && sourceTask" class="builder-continue primary" :to="`/market/demands/${sourceTask.id}?assetId=${generation?.outputAssetId}`">
                  {{ t('tasks.submitDelivery') }}<ArrowRight :size="17" />
                </RouterLink>
                <RouterLink v-else-if="finished && sourceAsset" class="builder-continue primary" :to="`/workspace/assets/${generation?.outputAssetId}`">
                  {{ t('actions.inspectProvenance') }}<ArrowRight :size="17" />
                </RouterLink>
                <RouterLink v-else-if="finished" class="builder-continue primary" :to="`/publish?assetId=${generation?.outputAssetId}&prompt=${encodeURIComponent(generation?.prompt || '')}`">
                  {{ t('actions.continuePublish') }}<ArrowRight :size="17" />
                </RouterLink>
                <button v-if="finished" class="builder-continue" type="button" @click="startAgain">
                  <RotateCcw :size="17" />{{ t('create.startAgain') }}
                </button>
              </div>
            </section>
          </aside>
        </Transition>
      </main>
    </div>

    <TransitionGroup name="builder-fab" tag="div" class="builder-floating-actions">
      <button v-for="action in floatingActions" :key="action.id" type="button" :title="action.label" @click="setBuilderView(action.id)">
        <component :is="action.icon" :size="18" /><span>{{ action.label }}</span>
      </button>
    </TransitionGroup>
  </section>
</template>

<style scoped lang="scss">
.builder-page {
  --builder-line: color-mix(in srgb, var(--border) 72%, transparent);
  padding: 20px 0 72px;
}

.builder-shell {
  min-height: calc(100dvh - 144px);
}

.builder-toolbar {
  min-height: 62px;
  display: grid;
  grid-template-columns: minmax(180px, 1fr) auto auto 40px;
  gap: 14px;
  align-items: center;
  margin-bottom: 12px;
  padding: 9px 10px 9px 14px;
  border: 1px solid var(--builder-line);
  border-radius: 8px;
  background: var(--surface);
}

.builder-identity {
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 10px;

  > span {
    width: 32px;
    height: 32px;
    display: grid;
    place-items: center;
    flex: 0 0 auto;
    border-radius: 8px;
    background: var(--accent);
    color: var(--accent-contrast);
  }

  > div { min-width: 0; display: grid; gap: 1px; }
  strong, small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  strong { font-size: 13px; }
  small { color: var(--text-tertiary); font-size: 10px; }
}

.builder-modes {
  display: flex;
  gap: 2px;
  padding: 2px;
  border-radius: 8px;
  background: color-mix(in srgb, var(--surface-muted) 72%, transparent);

  a {
    min-height: 32px;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 0 10px;
    border-radius: 6px;
    color: var(--text-secondary);
    font-size: 11px;
    font-weight: 600;
    transition: 150ms ease;

    &:hover { background: var(--surface); color: var(--text); }
    &.active { background: var(--accent-soft); color: var(--accent-readable); }
  }
}

.builder-progress {
  min-width: 42px;
  min-height: 32px;
  display: grid;
  place-items: center;
  padding: 0 8px;
  border-radius: 6px;
  background: var(--surface-muted);
  color: var(--text-tertiary);
  font-family: var(--font-mono);
  font-size: 10px;
}

.builder-body {
  height: calc(100dvh - 206px);
  min-height: 640px;
  display: grid;
  grid-template-columns: minmax(440px, 1.2fr) minmax(380px, .8fr);
  gap: 12px;
}

.builder-shell[data-view='chat'] .builder-body,
.builder-shell[data-view='preview'] .builder-body { grid-template-columns: minmax(0, 1fr); }

.builder-chat,
.builder-config {
  min-width: 0;
  min-height: 0;
  display: flex;
  overflow: hidden;
  flex-direction: column;
  border: 1px solid var(--builder-line);
  border-radius: 8px;
  background: var(--surface);
}
.builder-chat { position: relative; background: color-mix(in srgb, var(--canvas) 76%, var(--surface)); }

.builder-panel-header {
  min-height: 92px;
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 20px;
  padding: 22px 24px 14px;

  > div > span { color: var(--accent-readable); font-size: 10px; font-weight: 650; }
  h1, h2 { margin: 7px 0 0; font-size: 22px; font-weight: 520; line-height: 1.3; }
  > div > p { max-width: 58ch; margin: 5px 0 0; color: var(--text-tertiary); font-size: 11px; line-height: 1.45; }
  > small { display: inline-flex; align-items: center; gap: 6px; min-height: 26px; flex: 0 0 auto; padding: 0 8px; border-radius: 999px; background: var(--surface-muted); color: var(--text-tertiary); font-size: 10px; white-space: nowrap; }
  > small i { width: 7px; height: 7px; border-radius: 50%; background: var(--success); }
}

.config-heading { min-height: 84px; padding: 18px 20px 10px; }

.builder-messages {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 16px;
  overflow-y: auto;
  padding: 20px 24px 28px;

  article {
    width: min(82%, 700px);
    display: grid;
    grid-template-columns: 30px minmax(0, 1fr);
    gap: 10px;
    align-items: start;

    &[data-role='user'] {
      align-self: flex-end;
      grid-template-columns: minmax(0, 1fr) 30px;
      .message-avatar { grid-column: 2; grid-row: 1; background: var(--accent); color: var(--accent-contrast); }
      > div { grid-column: 1; grid-row: 1; padding: 11px 13px; border-radius: 8px 3px 8px 8px; background: var(--accent); color: var(--accent-contrast); }
      small { color: color-mix(in srgb, var(--accent-contrast) 70%, transparent); }
    }

    &[data-role='assistant'] > div { padding: 11px 13px; border-radius: 3px 8px 8px; background: var(--surface-muted); }
    > div > small { display: block; margin-bottom: 3px; color: var(--text-tertiary); font-size: 10px; }
    p { margin: 0; font-size: 13px; line-height: 1.65; }
  }
}

.message-avatar {
  width: 30px;
  height: 30px;
  display: grid;
  place-items: center;
  border-radius: 8px;
  background: var(--accent-soft);
  color: var(--accent-readable);
}

.thinking {
  height: 30px;
  display: flex;
  align-items: center;
  gap: 4px;
  i { width: 5px; height: 5px; border-radius: 50%; background: var(--text-tertiary); animation: builder-thinking 800ms infinite alternate; }
  i:nth-child(2) { animation-delay: 130ms; }
  i:nth-child(3) { animation-delay: 260ms; }
}

.builder-starters {
  display: flex;
  gap: 6px;
  overflow-x: auto;
  padding: 0 24px 9px;

  button { flex: 0 0 auto; min-height: 30px; padding: 0 10px; border: 0; border-radius: 6px; background: var(--surface-muted); color: var(--text-secondary); font-size: 10px; cursor: pointer; }
  button:hover { background: var(--accent-soft); color: var(--accent-readable); }
}

.builder-composer {
  margin: 0 24px 22px;
  padding: 10px;
  border: 1px solid var(--builder-line);
  border-radius: 8px;
  background: var(--surface);
  box-shadow: 0 8px 22px color-mix(in srgb, var(--text) 5%, transparent);
  transition: border-color 150ms ease, box-shadow 150ms ease;

  &:focus-within { border-color: var(--focus); box-shadow: 0 0 0 3px color-mix(in srgb, var(--focus) 14%, transparent); }
  textarea { width: 100%; min-height: 54px; resize: none; border: 0; outline: 0; background: transparent; font-size: 13px; }
  > div { display: flex; align-items: center; gap: 8px; }
  > div > small { flex: 1; color: var(--text-tertiary); font-size: 9px; text-align: right; }
}

.builder-context-picker {
  position: absolute;
  z-index: 4;
  right: 24px;
  bottom: 116px;
  left: 24px;
  max-height: min(360px, 52%);
  display: grid;
  gap: 10px;
  overflow-y: auto;
  padding: 12px;
  border: 1px solid var(--builder-line);
  border-radius: 8px;
  background: var(--surface);
  box-shadow: 0 12px 32px color-mix(in srgb, var(--text) 10%, transparent);

  > header { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
  > header > div { min-width: 0; display: grid; gap: 2px; }
  > header strong { font-size: 12px; }
  > header small, > p, .builder-context-empty { color: var(--text-secondary); font-size: 10px; }
  > p { margin: 0; }
}

.builder-context-list {
  display: grid;
  gap: 4px;

  > button { min-width: 0; min-height: 48px; display: grid; grid-template-columns: 32px minmax(0, 1fr) 16px; gap: 9px; align-items: center; padding: 7px 8px; border: 0; border-radius: 6px; background: var(--surface-muted); color: var(--text); cursor: pointer; text-align: left; }
  > button:hover { background: var(--accent-soft); }
  > button > span:first-child { width: 32px; height: 32px; display: grid; place-items: center; border-radius: 6px; background: var(--surface); color: var(--accent-readable); }
  > button > span:nth-child(2) { min-width: 0; display: grid; gap: 2px; }
  strong, small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  strong { font-size: 11px; }
  small { color: var(--text-tertiary); font-size: 9px; text-transform: capitalize; }
  > button > svg { color: var(--text-tertiary); }
}

.builder-context-empty { display: flex; justify-content: space-between; align-items: center; gap: 12px; }
.builder-context-empty p { margin: 0; }

.builder-send {
  width: 36px;
  height: 36px;
  display: grid;
  place-items: center;
  border: 0;
  border-radius: 8px;
  background: var(--accent);
  color: var(--accent-contrast);
  cursor: pointer;
  transition: 150ms ease;
  &:hover:not(:disabled) { background: var(--accent-hover); transform: translateY(-1px); }
  &:disabled { cursor: not-allowed; opacity: .4; }
}

.builder-tabs {
  width: fit-content;
  max-width: calc(100% - 40px);
  display: flex;
  gap: 2px;
  margin: 4px 20px 2px;
  padding: 3px;
  border-radius: 8px;
  background: var(--surface-muted);

  button { min-width: 96px; min-height: 34px; display: flex; align-items: center; justify-content: center; gap: 6px; padding: 0 10px; border: 0; border-radius: 6px; background: transparent; color: var(--text-secondary); font-size: 11px; cursor: pointer; }
  button:hover { background: var(--surface); color: var(--text); }
  button.active { background: var(--surface); color: var(--accent-readable); }
  .tab-check { color: var(--success); }
}

.builder-form {
  flex: 1;
  min-height: 0;
  display: grid;
  align-content: start;
  gap: 15px;
  overflow-y: auto;
  padding: 18px 20px 22px;

  label:not(.builder-tool) { display: grid; gap: 6px; }
  label > span { color: var(--text-secondary); font-size: 11px; font-weight: 600; }
  :is(input:not([type='range'], [type='checkbox']), textarea, select) { width: 100%; min-width: 0; border: 1px solid transparent; border-radius: 8px; outline: 0; background: var(--surface-muted); padding: 11px 12px; font-size: 12px; }
  :is(input, textarea, select):focus { border-color: var(--focus); box-shadow: 0 0 0 3px color-mix(in srgb, var(--focus) 13%, transparent); }
  textarea { resize: vertical; line-height: 1.55; }
}

.builder-field-pair { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.builder-primary-prompt textarea { min-height: 96px; }
.builder-range { input { width: 100%; accent-color: var(--accent); } span { display: flex; justify-content: space-between; } b { color: var(--accent-readable); font-family: var(--font-mono); } }

.builder-tool {
  min-height: 68px;
  display: grid;
  grid-template-columns: minmax(0, 1fr) 20px;
  gap: 14px;
  align-items: center;
  padding: 12px;
  border-radius: 8px;
  background: var(--surface-muted);
  cursor: pointer;
  > span { display: grid; gap: 2px; }
  small { color: var(--text-tertiary); font-size: 10px; font-weight: 400; }
  input { width: 18px; height: 18px; accent-color: var(--accent); }
}

.builder-evidence,
.builder-preview-meta {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 20px;
  margin: 0;
  padding: 14px;
  border-radius: 10px;
  background: var(--surface-muted);
  dt { color: var(--text-tertiary); font-size: 9px; }
  dd { margin: 2px 0 0; font-size: 11px; font-weight: 600; }
}

.builder-source {
  display: grid;
  grid-template-columns: 20px minmax(0, 1fr);
  gap: 9px;
  align-items: center;
  padding: 10px;
  border-radius: 9px;
  background: var(--surface-muted);
  color: var(--accent-readable);
  > span { min-width: 0; display: grid; gap: 2px; }
  small { color: var(--text-tertiary); font-size: 9px; }
  strong { overflow: hidden; color: var(--text); font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
}

.builder-continue {
  justify-self: end;
  min-height: 40px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  border: 1px solid var(--builder-line);
  min-width: 124px;
  padding: 0 16px;
  border-radius: 8px;
  background: var(--surface);
  color: var(--text);
  font-size: 12px;
  font-weight: 650;
  cursor: pointer;
  transition: 150ms ease;
  &:hover { border-color: var(--border-strong); transform: translateY(-1px); }
  &.primary { border-color: var(--accent); background: var(--accent); color: var(--accent-contrast); }
  &:disabled { cursor: not-allowed; opacity: .5; }
}

.builder-form-actions { display: flex; justify-content: flex-end; gap: 8px; }

.builder-config-footer {
  display: grid;
  grid-template-columns: 16px minmax(0, 1fr) 28px;
  gap: 8px;
  align-items: start;
  margin: 0 12px 12px;
  padding: 10px 12px;
  border-radius: 8px;
  background: var(--surface-muted);
  color: var(--text-tertiary);
  font-size: 9px;
  svg { color: var(--success); }
  span { display: grid; gap: 3px; }
  strong { color: var(--text-secondary); font-size: 10px; font-weight: 600; }
  a { width: 28px; height: 28px; display: grid; place-items: center; border-radius: 7px; }
  a:hover { background: var(--surface-muted); }
}

.builder-preview {
  flex: 1;
  min-height: 0;
  display: grid;
  grid-template-rows: auto minmax(360px, 1fr) auto auto auto;
  gap: 16px;
  overflow-y: auto;
  padding: 24px;
  > header { display: flex; justify-content: space-between; align-items: flex-start; gap: 18px; }
  > header span { color: var(--accent-readable); font-size: 10px; font-weight: 700; }
  > header h2 { margin: 5px 0 0; font-size: 22px; }
  > header > small { color: var(--text-tertiary); font-size: 10px; }
}

.builder-stage {
  position: relative;
  min-height: 360px;
  display: grid;
  place-items: center;
  overflow: hidden;
  border-radius: 12px;
  background: #12141a;
  color: #f5f7fb;
  .asset-renderer { width: 100%; height: 100%; }
}

.builder-stage-empty {
  max-width: 440px;
  display: grid;
  justify-items: center;
  gap: 10px;
  padding: 28px;
  text-align: center;
  > span { width: 46px; height: 46px; display: grid; place-items: center; border: 1px solid rgb(255 255 255 / 12%); border-radius: 11px; background: rgb(255 255 255 / 5%); color: #aeb6c6; }
  h3 { margin: 2px 0 0; font-size: 20px; }
  p { margin: 0; color: #aeb6c6; font-size: 12px; line-height: 1.55; }
}

.builder-generation-status {
  position: absolute;
  right: 12px;
  bottom: 12px;
  left: 12px;
  min-height: 42px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 11px;
  border: 1px solid rgb(255 255 255 / 13%);
  border-radius: 9px;
  background: rgb(10 12 17 / 82%);
  backdrop-filter: blur(14px);
  font-size: 11px;
  strong { margin-left: auto; color: #aeb6c6; font-size: 9px; }
  &[data-status='succeeded'] { border-color: color-mix(in srgb, var(--success) 55%, transparent); }
}

.builder-preview-meta { grid-template-columns: repeat(3, minmax(0, 1fr)); }
.builder-preview-actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 8px; }
.builder-preview-actions .builder-continue { min-width: 132px; padding: 0 14px; }

.builder-floating-actions {
  position: fixed;
  z-index: 40;
  right: 28px;
  bottom: 88px;
  display: flex;
  gap: 6px;
  padding: 4px;
  border: 1px solid var(--builder-line);
  border-radius: 999px;
  background: var(--surface);
  box-shadow: 0 8px 22px color-mix(in srgb, var(--text) 8%, transparent);
  button { width: 38px; height: 38px; display: grid; place-items: center; padding: 0; border: 0; border-radius: 50%; background: transparent; color: var(--text-secondary); cursor: pointer; transition: 180ms cubic-bezier(.22, 1, .36, 1); }
  button:hover { background: var(--accent-soft); color: var(--accent-readable); transform: translateY(-1px); }
  span { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
}

.builder-message-enter-active,
.builder-message-leave-active,
.builder-column-enter-active,
.builder-column-leave-active,
.builder-panel-enter-active,
.builder-panel-leave-active,
.builder-fab-enter-active,
.builder-fab-leave-active,
.builder-fab-move { transition: 260ms cubic-bezier(.22, 1, .36, 1); }
.builder-message-enter-from { opacity: 0; transform: translateY(8px) scale(.98); }
.builder-message-leave-to { opacity: 0; transform: translateY(-5px); }
.builder-column-enter-from, .builder-column-leave-to { opacity: 0; transform: translateX(14px); }
.builder-panel-enter-from { opacity: 0; transform: translateX(10px); }
.builder-panel-leave-to { opacity: 0; transform: translateX(-7px); }
.builder-fab-enter-from, .builder-fab-leave-to { opacity: 0; transform: translateY(12px) scale(.9); }

@keyframes builder-thinking { to { opacity: .35; transform: translateY(-3px); } }

@media (max-width: 1180px) {
  .builder-toolbar { grid-template-columns: minmax(160px, 1fr) auto 40px; }
  .builder-progress { display: none; }
  .builder-body { grid-template-columns: minmax(370px, 1.1fr) minmax(350px, .9fr); }
}

@media (max-width: 1023px) {
  .builder-page { padding: 16px 0 80px; }
  .builder-shell { width: min(100% - 32px, 100%); min-height: calc(100dvh - 156px); }
  .builder-toolbar { grid-template-columns: minmax(0, 1fr) 40px; }
  .builder-modes { grid-column: 1 / -1; grid-row: 2; overflow-x: auto; }
  .builder-body { height: auto; min-height: calc(100dvh - 224px); grid-template-columns: 1fr; }
  .builder-chat, .builder-config { min-height: calc(100dvh - 224px); }
  .builder-floating-actions { right: 16px; bottom: 76px; }
}

@media (max-width: 640px) {
  .builder-page { padding-top: 0; }
  .builder-shell { width: 100%; }
  .builder-toolbar { margin-bottom: 8px; border-inline: 0; border-radius: 0; }
  .builder-body { gap: 8px; }
  .builder-chat, .builder-config { border-inline: 0; border-radius: 0; }
  .builder-toolbar, .builder-panel-header, .builder-messages, .builder-form, .builder-preview { padding-inline: 16px; }
  .builder-identity small { display: none; }
  .builder-modes a { flex: 1 0 auto; justify-content: center; }
  .builder-composer { margin: 0 16px 86px; }
  .builder-context-picker { right: 16px; bottom: 180px; left: 16px; }
  .builder-composer > div > small { display: none; }
  .builder-composer .builder-send { margin-left: auto; }
  .builder-starters { padding-inline: 16px; }
  .builder-field-pair { grid-template-columns: 1fr; }
  .builder-tabs { width: auto; max-width: none; }
  .builder-tabs button { min-width: 0; flex: 1; }
  .builder-tabs span { display: none; }
  .builder-preview-meta { grid-template-columns: 1fr; }
}

@media (prefers-reduced-motion: reduce) {
  .builder-page *, .builder-page *::before, .builder-page *::after { animation-duration: .01ms !important; transition-duration: .01ms !important; }
}
</style>
