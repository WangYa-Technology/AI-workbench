<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
const props = withDefaults(defineProps<{ target: string | number | Date; now?: number; compact?: boolean; daySuffix?: string }>(), { now: undefined, compact: false, daySuffix: 'd' })
const emit = defineEmits<{ complete: [] }>()
const current = ref(props.now ?? Date.now())
let timer: ReturnType<typeof globalThis.setInterval> | undefined
const remaining = computed(() => Math.max(0, new Date(props.target).getTime() - current.value))
const parts = computed(() => { const total = Math.floor(remaining.value / 1000); return { days: Math.floor(total / 86400), hours: Math.floor(total % 86400 / 3600), minutes: Math.floor(total % 3600 / 60), seconds: total % 60 } })
watch(remaining, (value, previous) => { if (value === 0 && previous > 0) emit('complete') })
onMounted(() => { if (props.now === undefined) timer = globalThis.setInterval(() => { current.value = Date.now() }, 1000) })
onBeforeUnmount(() => globalThis.clearInterval(timer))
</script>

<template><time class="ui-countdown" :datetime="new Date(target).toISOString()" :data-compact="compact"><slot v-bind="parts"><template v-if="compact">{{ parts.days }}{{ daySuffix }} {{ String(parts.hours).padStart(2, '0') }}:{{ String(parts.minutes).padStart(2, '0') }}:{{ String(parts.seconds).padStart(2, '0') }}</template><template v-else><span v-for="(value, key) in parts" :key="key"><strong>{{ String(value).padStart(2, '0') }}</strong><small>{{ key }}</small></span></template></slot></time></template>
