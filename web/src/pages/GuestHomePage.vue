<script setup lang="ts">
import {
  ArrowRight, BriefcaseBusiness, Check, ImageIcon, Languages, MessageCircle,
  Moon, Music2, Play, Send, ShieldCheck, Sun, Video,
} from 'lucide-vue-next'
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRouter } from 'vue-router'
import { api, type TaskSummary, type Work } from '../api/client'
import AssetMedia from '../components/domain/AssetMedia.vue'
import BrandLogo from '../components/brand/BrandLogo.vue'
import { formatCurrency } from '../lib/format'
import { usePreferencesStore } from '../stores/preferences'
import { useSessionStore } from '../stores/session'

type CreationMode = 'chat' | 'image' | 'video' | 'music'

const { t, locale } = useI18n()
const router = useRouter()
const session = useSessionStore()
const preferences = usePreferencesStore()
const works = ref<Work[]>([])
const tasks = ref<TaskSummary[]>([])
const loading = ref(true)
const starter = ref('')
const activeMode = ref<CreationMode>('image')
const modeWasChosen = ref(false)
let modeTimer: ReturnType<typeof globalThis.setInterval> | undefined
let revealObserver: globalThis.IntersectionObserver | undefined

const modes = computed(() => [
  { id: 'image' as const, icon: ImageIcon, label: t('create.modes.image'), detail: t('home.demo.modes.image'), placeholder: t('home.composer.placeholders.image') },
  { id: 'video' as const, icon: Video, label: t('create.modes.video'), detail: t('home.demo.modes.video'), placeholder: t('home.composer.placeholders.video') },
  { id: 'music' as const, icon: Music2, label: t('create.modes.music'), detail: t('home.demo.modes.music'), placeholder: t('home.composer.placeholders.music') },
  { id: 'chat' as const, icon: MessageCircle, label: t('create.modes.chat'), detail: t('home.demo.modes.chat'), placeholder: t('home.composer.placeholders.chat') },
])
const activeModeConfig = computed(() => modes.value.find(mode => mode.id === activeMode.value) || modes.value[0])
const featured = computed(() => works.value[0])
const galleryWorks = computed(() => works.value.slice(0, 4))
const openTasks = computed(() => tasks.value.filter(task => task.status === 'open').slice(0, 3))
const heroAsset = computed(() => featured.value?.mediaKind === 'image' ? featured.value.mediaUrl : '/media/home-cinematic.jpg')

const creationLoop = computed(() => [
  { index: '01', title: t('home.loop.create.title'), summary: t('home.loop.create.summary'), to: '/create/image' },
  { index: '02', title: t('home.loop.publish.title'), summary: t('home.loop.publish.summary'), to: '/community' },
  { index: '03', title: t('home.loop.earn.title'), summary: t('home.loop.earn.summary'), to: '/market/demands' },
])

function chooseMode(mode: CreationMode) {
  modeWasChosen.value = true
  activeMode.value = mode
}

function startCreation() {
  const query = starter.value.trim() ? { starter: starter.value.trim() } : undefined
  void router.push({ path: `/create/${activeMode.value}`, query })
}

function currency(task: TaskSummary) {
  return formatCurrency(task.budgetCents, task.currency, locale.value)
}

function setupRevealObserver() {
  if (globalThis.matchMedia('(prefers-reduced-motion: reduce)').matches) return
  revealObserver = new globalThis.IntersectionObserver((entries) => {
    entries.forEach((entry) => {
      if (!entry.isIntersecting) return
      entry.target.classList.add('is-visible')
      revealObserver?.unobserve(entry.target)
    })
  }, { threshold: 0.1 })
  globalThis.document.querySelectorAll('[data-home-reveal]').forEach(element => revealObserver?.observe(element))
}

onMounted(async () => {
  const user = await session.ensure()
  if (user) {
    await router.replace('/discover')
    return
  }

  if (!globalThis.matchMedia('(prefers-reduced-motion: reduce)').matches) {
    modeTimer = globalThis.setInterval(() => {
      if (modeWasChosen.value || starter.value) return
      const current = modes.value.findIndex(mode => mode.id === activeMode.value)
      activeMode.value = modes.value[(current + 1) % modes.value.length].id
    }, 4200)
  }

  await nextTick()
  setupRevealObserver()

  try {
    const [workPage, taskPage] = await Promise.all([api.listWorks(), api.listTasks({ status: 'open' })])
    works.value = workPage.items
    tasks.value = taskPage.items
  } catch {
    // The public entry remains usable when community content is unavailable.
  } finally {
    loading.value = false
  }
})

onBeforeUnmount(() => {
  if (modeTimer) globalThis.clearInterval(modeTimer)
  revealObserver?.disconnect()
})
</script>

<template>
  <div class="guest-home">
    <header class="home-header">
      <div class="home-header-inner home-width">
        <RouterLink class="home-brand" to="/" :aria-label="t('brand')">
          <BrandLogo class="brand-mark" /><strong>{{ t('brand') }}</strong>
        </RouterLink>
        <nav class="home-nav" :aria-label="t('accessibility.primaryNavigation')">
          <RouterLink to="/create/image">
            {{ t('nav.create') }}
          </RouterLink>
          <RouterLink to="/discover">
            {{ t('home.nav.work') }}
          </RouterLink>
          <RouterLink to="/market/demands">
            {{ t('home.nav.tasks') }}
          </RouterLink>
          <RouterLink to="/community">
            {{ t('home.nav.community') }}
          </RouterLink>
        </nav>
        <div class="home-actions">
          <button type="button" :aria-label="t('actions.language')" :title="t('actions.language')" @click="preferences.toggleLocale">
            <Languages :size="16" :stroke-width="1.7" />
          </button>
          <button type="button" :aria-label="t('actions.theme')" :title="t('actions.theme')" @click="preferences.toggleTheme">
            <Sun v-if="preferences.resolvedTheme === 'dark'" :size="16" :stroke-width="1.7" /><Moon v-else :size="16" :stroke-width="1.7" />
          </button>
          <RouterLink class="home-sign-in" :to="{ path: '/settings', query: { auth: 'login', returnTo: '/' } }">
            {{ t('home.signIn') }}
          </RouterLink>
          <RouterLink class="home-register" :to="{ path: '/settings', query: { auth: 'register', returnTo: '/create/image' } }">
            {{ t('home.createAccount') }}
          </RouterLink>
        </div>
      </div>
    </header>

    <main class="home-content">
      <section class="home-hero">
        <div class="hero-main home-width">
          <div class="hero-copy">
            <h1>{{ t('home.title') }}</h1>
            <p>{{ t('home.summary') }}</p>
            <div class="hero-actions">
              <RouterLink class="primary-action" to="/create/image">
                {{ t('home.startCreating') }}<ArrowRight :size="16" />
              </RouterLink>
              <RouterLink class="secondary-action" to="/discover">
                {{ t('home.exploreWork') }}
              </RouterLink>
            </div>
            <nav class="mode-switcher" :aria-label="t('home.demo.switcherLabel')">
              <button v-for="mode in modes" :key="mode.id" type="button" :class="{ active: activeMode === mode.id }" :aria-pressed="activeMode === mode.id" @click="chooseMode(mode.id)">
                <component :is="mode.icon" :size="16" :stroke-width="1.7" /><span><strong>{{ mode.label }}</strong><small>{{ mode.detail }}</small></span>
              </button>
            </nav>
          </div>

          <div class="hero-product" :aria-label="t('home.demo.label')">
            <div class="window-topbar">
              <span class="window-brand"><BrandLogo />{{ t('home.demo.workspace') }}</span><span class="window-state"><i></i>{{ t('home.demo.ready') }}</span>
            </div>
            <Transition name="preview" mode="out-in">
              <div :key="activeMode" :class="['creation-preview', `preview-${activeMode}`]">
                <template v-if="activeMode === 'image'">
                  <div class="hero-output">
                    <img :src="heroAsset" :alt="t('home.demo.imageAlt')" />
                  </div>
                  <div class="hero-output-panel">
                    <span class="output-kind"><ImageIcon :size="14" />{{ t('home.demo.output.image') }}</span>
                    <strong>{{ t('home.demo.imageTitle') }}</strong>
                    <small>{{ t('home.demo.imageMeta') }}</small>
                    <div class="output-swatches" aria-hidden="true">
                      <i></i><i></i><i></i>
                    </div>
                  </div>
                </template>
                <template v-else-if="activeMode === 'video'">
                  <div class="hero-output">
                    <video src="/media/local-video-test.mp4" :poster="heroAsset" muted autoplay loop playsinline preload="metadata"></video>
                    <span class="play-control"><Play :size="20" fill="currentColor" /></span>
                  </div>
                  <div class="hero-output-panel video-panel">
                    <span class="output-kind"><Video :size="14" />{{ t('home.demo.output.video') }}</span>
                    <strong>{{ activeModeConfig.detail }}</strong>
                    <div class="storyboard" aria-hidden="true">
                      <img :src="heroAsset" alt="" /><img :src="heroAsset" alt="" /><img :src="heroAsset" alt="" />
                    </div>
                    <div class="video-progress">
                      <i></i><span>00:03 / 00:08</span>
                    </div>
                  </div>
                </template>
                <template v-else-if="activeMode === 'music'">
                  <div class="album-art">
                    <img :src="heroAsset" :alt="t('home.demo.musicAlt')" />
                  </div>
                  <div class="track-copy">
                    <span>{{ t('home.demo.output.music') }}</span><strong>{{ t('home.demo.musicTitle') }}</strong><small>{{ t('home.demo.musicMeta') }}</small>
                  </div>
                  <div class="waveform" aria-hidden="true">
                    <i v-for="index in 38" :key="index" :style="{ '--bar': `${18 + ((index * 19) % 68)}%` }"></i>
                  </div>
                </template>
                <template v-else>
                  <div class="chat-line is-user">
                    {{ t('home.demo.chatPrompt') }}
                  </div>
                  <div class="chat-line is-ai">
                    <BrandLogo /><p>{{ t('home.demo.chatResponse') }}</p>
                  </div>
                  <div class="chat-actions">
                    <span>{{ t('home.demo.chatActionOne') }}</span><span>{{ t('home.demo.chatActionTwo') }}</span>
                  </div>
                </template>
              </div>
            </Transition>
            <form class="hero-composer" @submit.prevent="startCreation">
              <span class="composer-mode"><component :is="activeModeConfig.icon" :size="15" />{{ activeModeConfig.label }}</span>
              <input v-model="starter" type="text" maxlength="500" :placeholder="activeModeConfig.placeholder" :aria-label="t('home.composer.ideaLabel')" @input="modeWasChosen = true" />
              <button type="submit" :aria-label="t('home.composer.start')" :title="t('home.composer.start')">
                <Send :size="16" />
              </button>
            </form>
          </div>
        </div>
      </section>

      <section class="workspace-story home-width" data-home-reveal>
        <div class="story-copy">
          <h2>{{ t('home.workspace.title') }}</h2><p>{{ t('home.workspace.summary') }}</p>
          <ul><li><Check :size="15" />{{ t('home.workspace.references') }}</li><li><Check :size="15" />{{ t('home.workspace.generations') }}</li><li><Check :size="15" />{{ t('home.trust.title') }}</li></ul>
          <RouterLink to="/create/image">
            {{ t('home.creation.open') }}<ArrowRight :size="15" />
          </RouterLink>
        </div>
        <div class="workspace-visual">
          <img class="workspace-media" :src="heroAsset" :alt="t('home.workspace.resultAlt')" />
          <div class="workspace-caption">
            <div class="workspace-result-copy">
              <small>{{ t('home.workspace.complete') }}</small><strong>{{ t('home.workspace.resultTitle') }}</strong>
            </div>
            <p>{{ t('home.workspace.prompt') }}</p>
            <span>16:9</span>
          </div>
        </div>
      </section>

      <section class="work-section home-width" data-home-reveal>
        <header class="section-heading">
          <div><h2>{{ t('home.work.title') }}</h2><p>{{ t('home.work.summary') }}</p></div><RouterLink to="/discover">
            {{ t('home.exploreWork') }}<ArrowRight :size="14" />
          </RouterLink>
        </header>
        <div v-if="loading" class="home-loading" aria-live="polite">
          <span v-for="index in 4" :key="index"></span>
        </div>
        <div v-else-if="galleryWorks.length" class="home-work-grid">
          <RouterLink v-for="work in galleryWorks" :key="work.id" class="home-work" :to="`/works/${work.id}`">
            <AssetMedia :src="work.mediaUrl" :kind="work.mediaKind" :alt="work.title" :width="work.width || 1000" :height="work.height || 800" :controls="false" /><span><strong>{{ work.title }}</strong><small>{{ work.author.displayName }} / {{ work.modelName }}</small></span>
          </RouterLink>
        </div>
        <div v-else class="home-empty">
          <p>{{ t('home.work.empty') }}</p><RouterLink to="/create/image">
            {{ t('home.work.create') }}<ArrowRight :size="14" />
          </RouterLink>
        </div>
      </section>

      <section class="loop-section" data-home-reveal>
        <div class="home-width loop-layout">
          <header><h2>{{ t('home.loop.title') }}</h2><p>{{ t('home.loop.summary') }}</p></header><div class="loop-path">
            <RouterLink v-for="item in creationLoop" :key="item.index" :to="item.to">
              <span>{{ item.index }}</span><div><h3>{{ item.title }}</h3><p>{{ item.summary }}</p></div><ArrowRight :size="16" />
            </RouterLink>
          </div>
        </div>
      </section>

      <section class="task-section home-width" data-home-reveal>
        <header class="section-heading">
          <div><h2>{{ t('home.tasks.title') }}</h2><p>{{ t('home.tasks.summary') }}</p></div><RouterLink to="/market/demands">
            {{ t('home.tasks.browse') }}<ArrowRight :size="14" />
          </RouterLink>
        </header>
        <div v-if="openTasks.length" class="home-task-list">
          <RouterLink v-for="task in openTasks" :key="task.id" :to="`/market/demands/${task.id}`">
            <span class="task-icon"><BriefcaseBusiness :size="16" /></span><span class="task-copy"><small>{{ t(`tasks.types.${task.deliverableType}`) }}</small><strong>{{ task.title }}</strong><span>{{ task.summary }}</span></span><span class="task-reward"><small>{{ t('tasks.reward') }}</small><strong>{{ currency(task) }}</strong></span><ArrowRight :size="16" />
          </RouterLink>
        </div>
        <div v-else-if="!loading" class="home-empty">
          <p>{{ t('home.tasks.empty') }}</p>
        </div>
      </section>

      <section class="trust-section home-width" data-home-reveal>
        <ShieldCheck :size="23" :stroke-width="1.6" /><div><h2>{{ t('home.trust.title') }}</h2><p>{{ t('home.trust.summary') }}</p></div><nav :aria-label="t('home.trust.linksLabel')">
          <RouterLink to="/policies/ai">
            {{ t('legal.topics.ai.title') }}
          </RouterLink><RouterLink to="/policies/licensing">
            {{ t('legal.topics.licensing.title') }}
          </RouterLink><RouterLink to="/policies/privacy">
            {{ t('legal.topics.privacy.title') }}
          </RouterLink>
        </nav>
      </section>

      <section class="closing-section" data-home-reveal>
        <div class="home-width">
          <div><h2>{{ t('home.closing.title') }}</h2><p>{{ t('home.closing.summary') }}</p></div><nav>
            <RouterLink class="closing-primary" :to="{ path: '/settings', query: { auth: 'register', returnTo: '/create/image' } }">
              {{ t('home.createAccount') }}<ArrowRight :size="15" />
            </RouterLink><RouterLink to="/create/image">
              {{ t('home.startCreating') }}
            </RouterLink>
          </nav>
        </div>
      </section>
    </main>

    <footer class="home-footer">
      <RouterLink class="home-brand" to="/">
        <BrandLogo class="brand-mark" /><strong>{{ t('brand') }}</strong>
      </RouterLink><p>{{ t('home.footer') }}</p><nav :aria-label="t('legal.footerLabel')">
        <RouterLink to="/policies/terms">
          {{ t('legal.topics.terms.title') }}
        </RouterLink><RouterLink to="/policies/privacy">
          {{ t('legal.topics.privacy.title') }}
        </RouterLink><RouterLink to="/support">
          {{ t('nav.support') }}
        </RouterLink>
      </nav>
    </footer>
  </div>
</template>

<style scoped lang="scss">
.guest-home {
  --home-blue: var(--accent);
  --home-blue-strong: var(--accent-hover);
  --home-blue-soft: var(--accent-soft);
  --home-hero: var(--canvas);
  --home-product: #14213d;
  --home-product-text: #ffffff;
  --home-muted: var(--text-secondary);
  --home-subtle: var(--text-secondary);
  --home-border: var(--border);
  min-height: 100dvh;
  overflow: clip;
  background: var(--surface);
  color: var(--text);
}
.home-width { width: min(calc(100% - 48px), 1040px); margin-inline: auto; }
.home-header { position: fixed; inset: 0 0 auto; z-index: 40; height: 64px; border-bottom: 1px solid color-mix(in srgb, var(--border) 56%, transparent); background: color-mix(in srgb, var(--surface) 94%, transparent); backdrop-filter: blur(18px) saturate(130%); }
.home-header-inner { height: 100%; display: grid; grid-template-columns: 1fr auto 1fr; align-items: center; gap: 22px; }
.home-brand { display: inline-flex; align-items: center; gap: 9px; width: max-content; color: var(--text); font-size: 13px; }
.home-brand .brand-mark { width: 29px; height: 29px; flex: 0 0 auto; border-radius: 7px; }
.home-brand strong { font-weight: 630; }
.home-nav { display: flex; align-items: center; gap: 28px; color: var(--text-secondary); font-size: 12px; font-weight: 520; }
.home-nav a:hover { color: var(--home-blue); }
.home-actions { justify-self: end; display: flex; align-items: center; gap: 4px; }
.home-actions button { width: 30px; height: 30px; display: grid; place-items: center; border-radius: 8px; background: transparent; color: var(--text-secondary); cursor: pointer; }
.home-actions button:hover { background: var(--surface-muted); color: var(--text); }
.home-sign-in, .home-register { min-height: 34px; display: inline-flex; align-items: center; padding: 0 12px; border-radius: 9px; font-size: 12px; font-weight: 560; white-space: nowrap; }
.home-sign-in { color: var(--text-secondary); }
.home-register { margin-left: 4px; background: var(--home-blue); color: var(--accent-contrast); }
.home-register:hover, .primary-action:hover { background: var(--home-blue-strong); }
.home-content { padding-top: 64px; }

.home-hero { background: var(--home-hero); }
.hero-main { min-height: 620px; display: grid; grid-template-columns: minmax(350px, .72fr) minmax(540px, 1.12fr); gap: clamp(56px, 6vw, 76px); align-items: center; padding-block: 54px 42px; }
.hero-copy { position: relative; z-index: 2; }
.hero-copy h1 { max-width: 430px; margin: 0; font-size: clamp(38px, 3.25vw, 47px); font-weight: 560; line-height: 1.14; letter-spacing: 0; text-wrap: balance; }
.hero-copy p { max-width: 410px; margin: 20px 0 26px; color: var(--text-secondary); font-size: 14px; line-height: 1.72; }
.hero-actions { display: flex; align-items: center; gap: 14px; }
.primary-action, .secondary-action { min-height: 41px; display: inline-flex; align-items: center; justify-content: center; gap: 8px; padding: 0 18px; border-radius: 10px; font-size: 12px; font-weight: 580; }
.primary-action { background: var(--home-blue); color: var(--accent-contrast); box-shadow: 0 9px 22px color-mix(in srgb, var(--home-blue) 18%, transparent); }
.secondary-action { border: 1px solid color-mix(in srgb, var(--text-secondary) 42%, transparent); color: var(--text); }
.secondary-action:hover { border-color: var(--home-blue); color: var(--home-blue); }
.hero-product { position: relative; min-width: 0; height: 410px; border-radius: 16px; background: var(--home-product); box-shadow: 0 26px 54px color-mix(in srgb, var(--home-product) 16%, transparent); }
.window-topbar { position: absolute; inset: 0 0 auto; height: 50px; display: flex; justify-content: space-between; align-items: center; padding: 0 20px; color: var(--home-product-text); font-size: 9px; }
.window-brand, .window-state { display: inline-flex; align-items: center; gap: 7px; }
.window-brand .brand-logo { width: 23px; height: 23px; border-radius: 6px; }
.window-state i { width: 6px; height: 6px; border-radius: 50%; background: #35bd7a; }
.creation-preview { position: absolute; inset: 50px 18px 76px; overflow: hidden; border-radius: 10px; background: #f5f8fe; }
.preview-image, .preview-video { display: grid; grid-template-columns: minmax(0, 1.5fr) minmax(145px, .62fr); gap: 10px; padding: 10px; }
.hero-output { position: relative; min-width: 0; overflow: hidden; border-radius: 11px; background: #cfddf2; }
.hero-output > :is(img, video) { width: 100%; height: 100%; display: block; object-fit: cover; }
.hero-output-panel { min-width: 0; display: grid; align-content: start; gap: 7px; padding: 17px 13px; border-radius: 11px; background: var(--surface); color: var(--text); }
.hero-output-panel .output-kind { display: inline-flex; align-items: center; gap: 6px; color: var(--home-blue); font-size: 8px; }
.hero-output-panel strong { margin-top: 5px; font-size: 14px; font-weight: 600; line-height: 1.2; }
.hero-output-panel small { color: var(--home-muted); font-size: 8px; line-height: 1.45; }
.output-swatches { display: flex; gap: 5px; margin-top: 8px; }
.output-swatches i { width: 17px; height: 17px; border-radius: 50%; background: #0d1726; }
.output-swatches i:nth-child(2) { background: #f15438; }
.output-swatches i:nth-child(3) { background: #ffd6a6; }
.output-lines { display: grid; gap: 6px; margin-top: 9px; }
.output-lines span { height: 6px; border-radius: 4px; background: #e0e9f6; }
.output-lines span:nth-child(2) { width: 82%; }
.output-lines span:nth-child(3) { width: 56%; }
.play-control { position: absolute; inset: 50% auto auto 50%; width: 48px; height: 48px; display: grid; place-items: center; border-radius: 50%; background: rgb(255 255 255 / 90%); color: var(--home-blue); transform: translate(-50%, -50%); }
.storyboard { display: grid; grid-template-columns: repeat(3, 1fr); gap: 4px; margin-top: 10px; }
.storyboard img { width: 100%; aspect-ratio: 1; display: block; border-radius: 5px; object-fit: cover; }
.storyboard img:nth-child(2) { filter: saturate(.6) brightness(1.2); }
.storyboard img:nth-child(3) { filter: hue-rotate(22deg) brightness(.85); }
.video-progress { display: flex; align-items: center; gap: 7px; margin-top: 8px; color: var(--home-muted); font-size: 7px; }
.video-progress i { flex: 1; height: 3px; border-radius: 3px; background: linear-gradient(90deg, var(--home-blue) 38%, #d7e3f4 38%); }
.preview-music { display: grid; grid-template-columns: 160px 1fr; grid-template-rows: 1fr 64px; gap: 18px; padding: 26px; background: linear-gradient(135deg, #f6f9ff, #dceaff); color: #18202d; }
.album-art { grid-row: 1 / 3; overflow: hidden; border-radius: 16px; box-shadow: 0 18px 34px rgb(29 68 130 / 22%); }
.album-art img { width: 100%; height: 100%; object-fit: cover; }
.track-copy { align-self: end; display: grid; gap: 4px; }
.track-copy span, .track-copy small { color: var(--home-muted); font-size: 9px; }
.track-copy strong { font-size: 21px; font-weight: 560; }
.waveform { display: flex; align-items: center; gap: 3px; }
.waveform i { flex: 1; height: var(--bar); min-width: 2px; border-radius: 3px; background: var(--home-blue); animation: wave 1.1s ease-in-out infinite alternate; }
.preview-chat { display: grid; align-content: center; gap: 13px; padding: 28px; background: var(--home-hero); color: var(--text); }
.chat-line { max-width: 76%; padding: 11px 13px; border-radius: 14px; font-size: 10px; line-height: 1.55; }
.chat-line.is-user { justify-self: end; background: var(--home-blue); color: var(--accent-contrast); border-bottom-right-radius: 5px; }
.chat-line.is-ai { display: grid; grid-template-columns: 25px 1fr; gap: 9px; max-width: 86%; padding: 0; }
.chat-line.is-ai > .brand-logo { width: 25px; height: 25px; border-radius: 6px; }
.chat-line p { margin: 0; padding: 10px 12px; border-radius: 5px 14px 14px; background: #fff; box-shadow: 0 7px 24px rgb(40 69 112 / 8%); }
.chat-actions { display: flex; gap: 7px; padding-left: 34px; }
.chat-actions span { padding: 7px 9px; border-radius: 8px; background: #e8f0ff; color: #3566ae; font-size: 8px; }
.hero-composer { position: absolute; z-index: 4; right: 20px; bottom: 16px; left: -34px; min-height: 62px; display: grid; grid-template-columns: auto minmax(0, 1fr) 38px; align-items: center; gap: 12px; padding: 8px 10px 8px 14px; border: 1px solid var(--home-border); border-radius: 12px; background: var(--surface); box-shadow: 0 15px 34px color-mix(in srgb, var(--home-product) 16%, transparent); }
.composer-mode { display: inline-flex; align-items: center; gap: 6px; padding-right: 12px; border-right: 1px solid var(--home-border); color: var(--home-blue); font-size: 10px; white-space: nowrap; }
.hero-composer input { min-width: 0; height: 38px; border: 0; outline: 0; background: transparent; color: var(--text); font-size: 11px; }
.hero-composer input::placeholder { color: var(--home-muted); }
.hero-composer button { width: 38px; height: 38px; display: grid; place-items: center; border-radius: 11px; background: var(--home-blue); color: var(--accent-contrast); cursor: pointer; }
.hero-composer button:hover { background: var(--home-blue-strong); }
.preview-enter-active, .preview-leave-active { transition: opacity 220ms ease, transform 300ms cubic-bezier(.16, 1, .3, 1); }
.preview-enter-from { opacity: 0; transform: translateY(7px); }
.preview-leave-to { opacity: 0; transform: translateY(-5px); }
.mode-switcher { width: 100%; margin-top: 42px; display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); }
.mode-switcher button { position: relative; min-width: 0; min-height: 42px; display: flex; align-items: center; gap: 7px; padding: 0 4px 10px; border-bottom: 2px solid transparent; background: transparent; color: var(--text-tertiary); cursor: pointer; text-align: left; }
.mode-switcher button > span { min-width: 0; display: block; }
.mode-switcher strong { color: var(--text-secondary); font-size: 11px; font-weight: 560; }
.mode-switcher small { display: none; }
.mode-switcher button.active { border-color: var(--home-blue); color: var(--home-blue); }
.mode-switcher button.active strong { color: var(--home-blue); }

.workspace-story { min-height: 680px; display: grid; grid-template-columns: .78fr 1.22fr; gap: clamp(56px, 8vw, 118px); align-items: center; padding-block: 112px; }
.story-copy h2, .section-heading h2, .loop-section h2, .trust-section h2, .closing-section h2 { margin: 0; font-size: clamp(29px, 3vw, 40px); font-weight: 470; line-height: 1.18; letter-spacing: 0; text-wrap: balance; }
.story-copy > p { margin: 19px 0 24px; color: var(--home-muted); font-size: 14px; line-height: 1.75; }
.story-copy ul { display: grid; gap: 12px; margin: 0 0 28px; padding: 0; list-style: none; color: var(--home-muted); font-size: 12px; }
.story-copy li { display: flex; align-items: center; gap: 9px; }
.story-copy li svg { color: var(--home-blue); }
.story-copy > a, .section-heading > a, .home-empty a { display: inline-flex; align-items: center; gap: 7px; color: var(--home-blue); font-size: 12px; font-weight: 580; }
.workspace-visual { min-width: 0; }
.workspace-media { width: 100%; aspect-ratio: 16 / 9.8; display: block; object-fit: cover; border-radius: 12px; }
.workspace-caption { display: grid; grid-template-columns: 150px minmax(0, 1fr) auto; align-items: start; gap: 28px; padding-top: 15px; }
.workspace-result-copy { display: grid; gap: 3px; }
.workspace-result-copy small { color: var(--home-blue); font-size: 8px; }
.workspace-result-copy strong { font-size: 11px; font-weight: 580; }
.workspace-caption p { max-width: 390px; margin: 0; color: var(--home-muted); font-size: 9px; line-height: 1.55; }
.workspace-caption > span { color: var(--home-muted); font-size: 9px; }
.work-section, .task-section { padding-block: 96px; }
.section-heading { display: flex; justify-content: space-between; align-items: end; gap: 50px; margin-bottom: 34px; }
.section-heading > div { max-width: 700px; }
.section-heading p { max-width: 620px; margin: 13px 0 0; color: var(--text-secondary); font-size: 13px; line-height: 1.65; }
.section-heading > a { flex: 0 0 auto; margin-bottom: 3px; }
.home-work-grid, .home-loading { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 16px; }
.home-work { min-width: 0; }
.home-work :deep(.asset-renderer), .home-loading span { aspect-ratio: 4 / 3; overflow: hidden; border-radius: 14px; background: var(--surface-muted); }
.home-work :deep(img), .home-work :deep(video) { width: 100%; height: 100%; object-fit: cover; transition: transform 360ms cubic-bezier(.16, 1, .3, 1); }
.home-work:hover :deep(img), .home-work:hover :deep(video) { transform: scale(1.025); }
.home-work > span { display: block; padding: 10px 2px 0; }
.home-work strong, .home-work small { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.home-work strong { font-size: 12px; font-weight: 570; }
.home-work small { margin-top: 3px; color: var(--text-tertiary); font-size: 9px; }
.home-loading span { animation: home-pulse 1.5s ease-in-out infinite; }
.home-empty { min-height: 170px; display: grid; align-content: center; justify-items: start; gap: 9px; color: var(--text-secondary); }
.home-empty p { margin: 0; font-size: 12px; }
.loop-section { padding-block: 108px; background: var(--home-blue-soft); }
.loop-layout { display: grid; grid-template-columns: .78fr 1.22fr; gap: clamp(60px, 9vw, 130px); }
.loop-section header p { max-width: 430px; margin: 17px 0 0; color: var(--text-secondary); font-size: 13px; line-height: 1.7; }
.loop-path { display: grid; gap: 33px; }
.loop-path a { display: grid; grid-template-columns: 42px minmax(0, 1fr) 20px; align-items: start; gap: 18px; color: var(--text); }
.loop-path > a > span { color: var(--home-blue); font-family: var(--font-mono); font-size: 11px; }
.loop-path h3 { margin: -3px 0 7px; font-size: 17px; font-weight: 570; }
.loop-path p { max-width: 480px; margin: 0; color: var(--text-secondary); font-size: 11px; line-height: 1.65; }
.loop-path svg { margin-top: 3px; color: var(--home-blue); transition: transform 160ms ease; }
.loop-path a:hover svg { transform: translateX(4px); }
.home-task-list { display: grid; }
.home-task-list > a { min-height: 94px; display: grid; grid-template-columns: 36px minmax(0, 1fr) 110px 18px; align-items: center; gap: 18px; border-bottom: 1px solid var(--border); }
.home-task-list > a:first-child { border-top: 1px solid var(--border); }
.home-task-list > a:hover { color: var(--home-blue); }
.task-icon { width: 32px; height: 32px; display: grid; place-items: center; border-radius: 10px; background: var(--home-blue-soft); color: var(--home-blue); }
.task-copy { min-width: 0; display: grid; gap: 3px; }
.task-copy small { color: var(--home-blue); font-size: 8px; }
.task-copy strong { overflow: hidden; color: var(--text); font-size: 13px; font-weight: 580; text-overflow: ellipsis; white-space: nowrap; }
.task-copy span { overflow: hidden; color: var(--text-secondary); font-size: 10px; text-overflow: ellipsis; white-space: nowrap; }
.task-reward { text-align: right; }
.task-reward small, .task-reward strong { display: block; }
.task-reward small { margin-bottom: 3px; color: var(--text-tertiary); font-size: 8px; }
.task-reward strong { color: var(--text); font-size: 12px; font-weight: 590; }
.trust-section { display: grid; grid-template-columns: 26px minmax(0, 1fr) auto; align-items: start; gap: 20px; margin-block: 6px 100px; padding-block: 28px; border-top: 1px solid var(--border); border-bottom: 1px solid var(--border); }
.trust-section > svg { color: var(--home-blue); }
.trust-section h2 { font-size: 19px; }
.trust-section p { max-width: 650px; margin: 8px 0 0; color: var(--text-secondary); font-size: 11px; line-height: 1.6; }
.trust-section nav { display: flex; gap: 18px; color: var(--text-secondary); font-size: 10px; }
.trust-section nav a:hover { color: var(--home-blue); }
.closing-section { padding-block: 88px; background: var(--home-product); color: var(--home-product-text); }
.closing-section .home-width { display: flex; justify-content: space-between; align-items: end; gap: 70px; }
.closing-section h2 { max-width: 660px; }
.closing-section p { max-width: 570px; margin: 14px 0 0; color: color-mix(in srgb, var(--home-product-text) 74%, transparent); font-size: 13px; line-height: 1.65; }
.closing-section nav { flex: 0 0 auto; display: flex; align-items: center; gap: 18px; }
.closing-section nav a { color: color-mix(in srgb, var(--home-product-text) 82%, transparent); font-size: 11px; }
.closing-section .closing-primary { min-height: 40px; display: inline-flex; align-items: center; gap: 7px; padding: 0 15px; border-radius: 8px; background: var(--home-product-text); color: var(--home-product); font-weight: 600; }
.closing-section .closing-primary:hover { background: #eff5ff; }
.home-footer { min-height: 108px; display: grid; grid-template-columns: 1fr auto 1fr; align-items: center; gap: 24px; padding: 22px 30px; background: var(--surface); color: var(--text-tertiary); font-size: 9px; }
.home-footer p { margin: 0; text-align: center; }
.home-footer nav { justify-self: end; display: flex; gap: 16px; }
.home-footer nav a:hover { color: var(--home-blue); }
[data-theme='dark'] .guest-home { --home-blue: #ff9b91; --home-blue-strong: #ffaaa0; --home-product: #0d172a; }
[data-theme='dark'] .window-topbar { color: var(--home-product-text); }
[data-theme='dark'] .hero-composer { border-color: var(--border); background: var(--surface); }
[data-theme='dark'] .composer-mode { border-color: var(--border); }
[data-theme='dark'] .hero-composer input { color: var(--text); }
[data-theme='dark'] .hero-composer input::placeholder { color: var(--text-secondary); }
[data-theme='dark'] .mode-switcher button.active, [data-theme='dark'] .mode-switcher button.active strong { color: var(--home-blue); }
@keyframes home-pulse { 50% { opacity: .56; } }
@keyframes wave { from { transform: scaleY(.58); opacity: .62; } to { transform: scaleY(1); opacity: 1; } }
@media (prefers-reduced-motion: no-preference) { [data-home-reveal] { transform: translateY(24px); transition: transform 650ms cubic-bezier(.16, 1, .3, 1); } [data-home-reveal].is-visible { transform: translateY(0); } }
@media (max-width: 1040px) { .home-nav { display: none; } .home-header-inner { grid-template-columns: 1fr auto; } .hero-main { grid-template-columns: minmax(320px, .78fr) minmax(480px, 1.22fr); gap: 42px; } }
@media (max-width: 820px) { .home-hero { min-height: auto; } .hero-main { min-height: auto; grid-template-columns: 1fr; gap: 38px; padding-block: 52px 42px; } .hero-copy { text-align: center; } .hero-copy h1, .hero-copy p { margin-inline: auto; } .hero-actions { justify-content: center; } .hero-product { width: min(100%, 620px); justify-self: center; } .mode-switcher { width: min(100%, 430px); margin-inline: auto; } .workspace-story { min-height: 0; grid-template-columns: 1fr; gap: 50px; padding-block: 88px; } .story-copy { max-width: 620px; } .workspace-visual { width: min(100%, 650px); } .home-work-grid, .home-loading { grid-template-columns: repeat(2, 1fr); } .loop-layout { grid-template-columns: 1fr; gap: 48px; } }
@media (max-width: 767px) {
  .home-width { width: min(calc(100% - 32px), 100%); } .home-header { height: 54px; } .home-actions button, .home-sign-in { display: none; } .home-register { min-height: 31px; padding-inline: 10px; } .home-content { padding-top: 54px; }
  .home-hero { min-height: 0; } .hero-main { min-height: 0; display: block; padding-block: 34px 20px; } .hero-copy h1 { font-size: 32px; line-height: 1.14; } .hero-copy p { margin: 14px auto 18px; font-size: 12px; line-height: 1.58; } .primary-action, .secondary-action { min-height: 38px; padding-inline: 13px; font-size: 11px; }
  .hero-product { height: 274px; margin-top: 25px; border-radius: 19px; } .window-topbar { height: 36px; padding-inline: 11px; } .creation-preview { inset: 36px 9px 59px; border-radius: 11px; } .preview-image, .preview-video { grid-template-columns: minmax(0, 1.35fr) minmax(94px, .65fr); gap: 7px; padding: 7px; } .hero-output, .hero-output-panel { border-radius: 8px; } .hero-output-panel { gap: 4px; padding: 10px 8px; } .hero-output-panel strong { font-size: 10px; } .hero-output-panel small, .output-lines { display: none; } .output-swatches { margin-top: 4px; }
  .preview-music { grid-template-columns: 92px 1fr; grid-template-rows: 1fr 42px; gap: 10px; padding: 15px; } .album-art { border-radius: 10px; } .track-copy strong { font-size: 15px; } .waveform { gap: 2px; } .preview-chat { gap: 9px; padding: 16px; } .chat-line { max-width: 88%; padding: 8px 9px; font-size: 8px; } .chat-line.is-ai { max-width: 94%; } .chat-actions { display: none; }
  .hero-composer { right: 7px; bottom: 8px; left: 7px; min-height: 55px; grid-template-columns: auto minmax(0, 1fr) 34px; gap: 8px; padding: 7px 8px 7px 10px; border-radius: 12px; } .composer-mode { padding-right: 8px; font-size: 0; } .composer-mode svg { width: 15px; } .hero-composer input { font-size: 9px; } .hero-composer button { width: 34px; height: 34px; border-radius: 9px; }
  .mode-switcher { margin-top: 24px; grid-template-columns: repeat(4, minmax(0, 1fr)); } .mode-switcher button { min-height: 40px; justify-content: center; gap: 5px; padding: 0 3px 7px; border-bottom-width: 2px; } .mode-switcher small { display: none; } .mode-switcher strong { font-size: 10px; }
  .workspace-story { gap: 36px; padding-block: 72px; } .story-copy h2, .section-heading h2, .loop-section h2, .closing-section h2 { font-size: 27px; } .story-copy > p { font-size: 12px; } .workspace-media { border-radius: 9px; } .workspace-caption { grid-template-columns: 1fr auto; gap: 8px 18px; padding-top: 11px; } .workspace-caption p { grid-column: 1 / 3; grid-row: 2; }
  .work-section, .task-section { padding-block: 70px; } .section-heading { display: grid; gap: 16px; margin-bottom: 26px; } .section-heading p { font-size: 11px; } .home-work-grid, .home-loading { grid-template-columns: 1fr 1fr; gap: 11px; } .home-work :deep(.asset-renderer), .home-loading span { border-radius: 10px; } .home-work strong { font-size: 10px; } .home-work small { font-size: 8px; }
  .loop-section { padding-block: 72px; } .loop-layout { gap: 40px; } .loop-path { gap: 28px; } .loop-path a { grid-template-columns: 31px minmax(0, 1fr) 16px; gap: 11px; } .loop-path h3 { font-size: 15px; }
  .home-task-list > a { grid-template-columns: 30px minmax(0, 1fr) 16px; gap: 11px; padding-block: 13px; } .task-reward { grid-column: 2; text-align: left; } .task-reward small, .task-reward strong { display: inline; margin-right: 5px; } .home-task-list > a > svg { grid-column: 3; grid-row: 1 / span 2; }
  .trust-section { grid-template-columns: 24px 1fr; margin-bottom: 66px; } .trust-section nav { grid-column: 2; flex-wrap: wrap; } .closing-section { padding-block: 66px; } .closing-section .home-width { display: grid; gap: 24px; } .closing-section nav { justify-self: start; } .home-footer { grid-template-columns: 1fr; justify-items: start; gap: 13px; padding: 28px 16px; } .home-footer p { text-align: left; } .home-footer nav { justify-self: start; }
}
@media (prefers-reduced-motion: reduce) { .home-loading span, .waveform i { animation: none; } .home-work :deep(img), .home-work :deep(video), .preview-enter-active, .preview-leave-active { transition: none; } }
</style>
