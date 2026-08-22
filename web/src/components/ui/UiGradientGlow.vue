<script setup lang="ts">
import { computed, type CSSProperties } from 'vue'
const props = withDefaults(defineProps<{ color?: string; size?: number; intensity?: number }>(), { color: 'var(--accent)', size: 220, intensity: 0.16 })
const style = computed(() => ({ '--ui-glow-color': props.color, '--ui-glow-size': `${props.size}px`, '--ui-glow-opacity': String(props.intensity) } as CSSProperties))
</script>

<template><div class="ui-gradient-glow" :style="style"><span class="ui-gradient-glow__effect" aria-hidden="true"></span><div class="ui-gradient-glow__content"><slot></slot></div></div></template>
