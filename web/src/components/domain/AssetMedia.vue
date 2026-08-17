<script setup lang="ts">
import { FileText, Music } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

const props = withDefaults(defineProps<{
  src: string
  kind: string
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
  controls: true,
  eager: false,
})

const { t } = useI18n()
const loadedText = ref('')
const textError = ref('')
const normalizedKind = computed(() => props.kind === 'music' ? 'audio' : props.kind)

watch(
  () => [normalizedKind.value, props.src, props.text] as const,
  async ([kind, src, text]) => {
    loadedText.value = text
    textError.value = ''
    if (kind !== 'document' || text || !src) return
    try {
      const response = await globalThis.fetch(src, { credentials: 'include' })
      if (!response.ok) throw new Error(`HTTP ${response.status}`)
      loadedText.value = await response.text()
    } catch {
      textError.value = t('status.mediaUnavailable')
    }
  },
  { immediate: true },
)
</script>

<template>
  <div class="asset-renderer" :data-kind="normalizedKind">
    <img
      v-if="normalizedKind === 'image'"
      :src="src"
      :alt="alt"
      :width="width"
      :height="height"
      :loading="eager ? 'eager' : 'lazy'"
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
    ></video>
    <div v-else-if="normalizedKind === 'audio'" class="asset-audio">
      <Music :size="32" :stroke-width="1.5" aria-hidden="true" />
      <strong>{{ alt }}</strong>
      <audio v-if="controls" :src="src" controls preload="metadata" :aria-label="alt"></audio>
    </div>
    <div v-else-if="normalizedKind === 'document'" class="asset-document">
      <FileText :size="26" :stroke-width="1.5" aria-hidden="true" />
      <pre v-if="loadedText" tabindex="0">{{ loadedText }}</pre>
      <p v-else-if="textError" role="alert">
        {{ textError }}
      </p>
      <span v-else>{{ t('status.loadingTextResult') }}</span>
    </div>
    <div v-else class="asset-document">
      <FileText :size="26" :stroke-width="1.5" aria-hidden="true" />
      <strong>{{ alt }}</strong>
    </div>
  </div>
</template>
