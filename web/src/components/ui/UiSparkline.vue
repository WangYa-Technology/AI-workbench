<script setup lang="ts">
import { computed } from 'vue'
const props = withDefaults(defineProps<{ values: number[]; width?: number; height?: number; color?: string; fill?: boolean; label?: string }>(), { width: 120, height: 32, color: 'var(--accent)', fill: false, label: 'Trend' })
const points = computed(() => { if (!props.values.length) return ''; const min = Math.min(...props.values); const max = Math.max(...props.values); const range = max - min || 1; return props.values.map((value, index) => `${props.values.length === 1 ? props.width / 2 : index / (props.values.length - 1) * props.width},${props.height - (value - min) / range * (props.height - 4) - 2}`).join(' ') })
const area = computed(() => points.value ? `0,${props.height} ${points.value} ${props.width},${props.height}` : '')
</script>

<template><svg class="ui-sparkline" :viewBox="`0 0 ${width} ${height}`" :width="width" :height="height" role="img" :aria-label="label"><polygon v-if="fill" :points="area" :fill="color" opacity=".12" /><polyline :points="points" fill="none" :stroke="color" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" vector-effect="non-scaling-stroke" /></svg></template>
