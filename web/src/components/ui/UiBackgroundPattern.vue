<script setup lang="ts">
import { computed, ref, type CSSProperties } from 'vue'
const props = withDefaults(defineProps<{ variant?: 'dots' | 'grid' | 'dashed-grid' | 'hexagons'; spotlight?: boolean; persistent?: boolean; cellSize?: number; color?: string }>(), { variant: 'dots', spotlight: false, persistent: false, cellSize: 0, color: 'var(--border-strong)' })
const x = ref('50%')
const y = ref('50%')
const style = computed(() => ({ '--ui-pattern-size': `${props.cellSize || (props.variant === 'dots' ? 14 : props.variant === 'hexagons' ? 40 : 28)}px`, '--ui-pattern-color': props.color, '--ui-pattern-x': x.value, '--ui-pattern-y': y.value } as CSSProperties))
function move(event: globalThis.PointerEvent) { const rect = (event.currentTarget as globalThis.HTMLElement).getBoundingClientRect(); x.value = `${event.clientX - rect.left}px`; y.value = `${event.clientY - rect.top}px` }
</script>

<template>
  <div class="ui-background-pattern" :data-variant="variant" :data-spotlight="spotlight" :data-persistent="persistent" :style="style" @pointermove="move">
    <span class="ui-background-pattern__texture" aria-hidden="true"></span><span v-if="spotlight" class="ui-background-pattern__spotlight" aria-hidden="true"></span><div class="ui-background-pattern__content">
      <slot></slot>
    </div>
  </div>
</template>
