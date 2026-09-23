<script setup lang="ts">
import { FileText, Music, PackageOpen } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { inlineTextPreview, readTextPreview, TEXT_PREVIEW_BYTES } from '../../lib/textPreview'

const props = withDefaults(defineProps<{
  src: string
  kind: string
  mimeType?: string
  alt: string
  width?: number
  height?: number
  text?: string
  controls?: boolean
  eager?: boolean
}>(), {
  width: 1200,
  height: 900,
  text: '',
  mimeType: '',
  controls: true,
  eager: false,
})

const { t } = useI18n()
const loadedText = ref('')
const textError = ref('')
const textTruncated = ref(false)
const mediaFailed = ref(false)
const normalizedKind = computed(() => {
  const mime = props.mimeType.split(';')[0]?.trim().toLowerCase()
  if (mime === 'application/zip') return 'package'
  if (props.kind === 'document' && mime && !mime.startsWith('text/')) return 'file'
  return props.kind === 'music' ? 'audio' : props.kind
})

watch(
  () => [normalizedKind.value, props.src, props.text] as const,
  async ([kind, src, text], _previous, onCleanup) => {
    const controller = new globalThis.AbortController()
    let timer: ReturnType<typeof globalThis.setTimeout> | undefined
    onCleanup(() => { globalThis.clearTimeout(timer); timer = undefined; controller.abort() })
    const inline = inlineTextPreview(text)
    loadedText.value = inline.text
    textTruncated.value = inline.truncated
    textError.value = ''
    mediaFailed.value = false
    if (kind !== 'document' || text || !src) return
    timer = globalThis.setTimeout(() => controller.abort(), 15_000)
    try {
      // One extra byte distinguishes a complete file from a shortened preview.
      // The stream reader still enforces the cap if a server ignores Range.
      const response = await globalThis.fetch(src, { credentials: 'include', signal: controller.signal, headers: { Range: `bytes=0-${TEXT_PREVIEW_BYTES}` } })
      const preview = await readTextPreview(response)
      if (!controller.signal.aborted) {
        loadedText.value = preview.text
        textTruncated.value = preview.truncated
      }
    } catch {
      // A timer abort is an error for this preview; a superseded watch must not
      // overwrite its successor. Cleanup clears timer before aborting it.
      if (timer !== undefined) textError.value = t('status.mediaUnavailable')
    } finally {
      globalThis.clearTimeout(timer)
    }
  },
  { immediate: true },
)
</script>

<template>
  <div class="asset-renderer" :data-kind="normalizedKind">
    <div v-if="mediaFailed || (!src && !text)" class="asset-media-unavailable" role="status">
      <FileText :size="26" aria-hidden="true" />
      <span>{{ t('status.mediaUnavailable') }}</span>
    </div>
    <img
      v-else-if="normalizedKind === 'image'"
      :src="src"
      :alt="alt"
      :width="width"
      :height="height"
      :loading="eager ? 'eager' : 'lazy'"
      @error="mediaFailed = true"
    />
    <video
      v-else-if="normalizedKind === 'video'"
      :src="src"
      :aria-label="alt"
      :controls="controls"
      :muted="!controls"
      :autoplay="!controls"
      :loop="!controls"
      playsinline
      preload="metadata"
      @error="mediaFailed = true"
    ></video>
    <div v-else-if="normalizedKind === 'audio'" class="asset-audio">
      <Music :size="32" :stroke-width="1.5" aria-hidden="true" />
      <strong>{{ alt }}</strong>
      <audio v-if="controls" :src="src" controls preload="metadata" :aria-label="alt" @error="mediaFailed = true"></audio>
    </div>
    <div v-else-if="normalizedKind === 'package'" class="asset-document">
      <PackageOpen :size="32" :stroke-width="1.5" aria-hidden="true" />
      <strong>{{ alt }}</strong>
      <span>{{ t('workspace.packageFilesFormat', { format: 'ZIP' }) }}</span>
    </div>
    <div v-else-if="normalizedKind === 'document'" class="asset-document">
      <FileText :size="26" :stroke-width="1.5" aria-hidden="true" />
      <pre v-if="loadedText" tabindex="0">{{ loadedText }}</pre>
      <p v-else-if="textError" role="alert">
        {{ textError }}
      </p>
      <span v-else>{{ t('status.loadingTextResult') }}</span>
      <small v-if="textTruncated" class="asset-text-preview-note">{{ t('status.textPreviewTruncated') }}</small>
    </div>
    <div v-else class="asset-document">
      <FileText :size="26" :stroke-width="1.5" aria-hidden="true" />
      <strong>{{ alt }}</strong>
    </div>
  </div>
</template>

<style scoped>
.asset-media-unavailable { display: grid; place-content: center; justify-items: center; gap: 12px; width: 100%; height: 100%; min-height: 140px; padding: 20px; background: var(--surface-muted); color: var(--text-secondary); font-size: 13px; text-align: center; }
.asset-audio strong { max-width: 100%; overflow-wrap: anywhere; text-align: center; }
</style>
