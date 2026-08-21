<script setup lang="ts">
import { Bookmark, Heart } from 'lucide-vue-next'
import { computed, onBeforeUnmount, ref, watch } from 'vue'

const props = withDefaults(defineProps<{
  active: boolean
  kind?: 'heart' | 'bookmark'
  size?: number
}>(), {
  kind: 'heart',
  size: 16,
})

const bursting = ref(false)
let burstTimer: ReturnType<typeof globalThis.setTimeout> | undefined
const icon = computed(() => props.kind === 'bookmark' ? Bookmark : Heart)
const particles = [
  { '--px': '0px', '--py': '-20px', '--pdelay': '0ms', '--psize': '1' },
  { '--px': '14px', '--py': '-14px', '--pdelay': '20ms', '--psize': '.8' },
  { '--px': '20px', '--py': '0px', '--pdelay': '5ms', '--psize': '1.1' },
  { '--px': '14px', '--py': '14px', '--pdelay': '30ms', '--psize': '.75' },
  { '--px': '0px', '--py': '20px', '--pdelay': '10ms', '--psize': '.95' },
  { '--px': '-14px', '--py': '14px', '--pdelay': '25ms', '--psize': '.7' },
  { '--px': '-20px', '--py': '0px', '--pdelay': '0ms', '--psize': '1.05' },
  { '--px': '-14px', '--py': '-14px', '--pdelay': '35ms', '--psize': '.8' },
]

watch(() => props.active, (active, previous) => {
  if (!active || previous) return
  bursting.value = false
  globalThis.requestAnimationFrame(() => {
    bursting.value = true
    globalThis.clearTimeout(burstTimer)
    burstTimer = globalThis.setTimeout(() => { bursting.value = false }, 650)
  })
})

onBeforeUnmount(() => globalThis.clearTimeout(burstTimer))
</script>

<template>
  <span class="t-like" :class="{ 'is-bursting': bursting }" :data-liked="String(active)" aria-hidden="true">
    <span class="t-like-icon"><component :is="icon" class="t-like-heart" :size="size" :fill="active ? 'currentColor' : 'none'" /></span>
    <span class="t-like-particles"><i v-for="(style, index) in particles" :key="index" :style="style"></i></span>
  </span>
</template>
