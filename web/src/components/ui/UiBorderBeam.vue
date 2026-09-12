<script setup lang="ts">
import { computed, type CSSProperties } from 'vue'
const props = withDefaults(defineProps<{ color?: string; length?: number; thickness?: number; speed?: number; delay?: number; reveal?: boolean; revealOn?: 'always' | 'hover' | 'press' }>(), { color: 'var(--accent)', length: 10, thickness: 1, speed: 5, delay: 0, reveal: false, revealOn: 'always' })
const style = computed(() => ({ '--ui-beam-color': props.color, '--ui-beam-length': `${props.length}%`, '--ui-beam-thickness': `${props.thickness}px`, '--ui-beam-speed': `${props.speed}s`, '--ui-beam-delay': `${props.delay}s` } as CSSProperties))
</script>

<template>
  <div class="ui-border-beam" :data-reveal-on="revealOn" :data-reveal="reveal" :style="style">
    <span class="ui-border-beam__effect" aria-hidden="true"></span><slot></slot>
  </div>
</template>
