<script setup lang="ts">
import { Grid2X2, List } from 'lucide-vue-next'
defineProps<{ modelValue: 'list' | 'grid'; label: string; listLabel: string; gridLabel: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: 'list' | 'grid'] }>()
</script>

<template>
  <div class="ui-layout-switcher" role="group" :aria-label="label" :data-layout="modelValue">
    <span class="ui-layout-switcher__indicator" aria-hidden="true"></span>
    <button type="button" :aria-label="listLabel" :title="listLabel" :aria-pressed="modelValue === 'list'" @click="emit('update:modelValue', 'list')">
      <List :size="17" aria-hidden="true" />
    </button>
    <button type="button" :aria-label="gridLabel" :title="gridLabel" :aria-pressed="modelValue === 'grid'" @click="emit('update:modelValue', 'grid')">
      <Grid2X2 :size="16" aria-hidden="true" />
    </button>
  </div>
</template>

<style scoped>
.ui-layout-switcher { position: relative; display: inline-flex; flex-shrink: 0; gap: 2px; padding: 3px; border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--surface-muted); }
.ui-layout-switcher__indicator { position: absolute; inset: 3px auto 3px 3px; width: 32px; border: 1px solid var(--border); border-radius: 6px; background: var(--surface); box-shadow: var(--shadow-xs); pointer-events: none; transition: transform var(--tabs-dur) var(--tabs-ease); }
.ui-layout-switcher[data-layout='grid'] .ui-layout-switcher__indicator { transform: translateX(34px); }
.ui-layout-switcher button { position: relative; z-index: 1; display: grid; place-items: center; width: 32px; height: 30px; padding: 0; border: 0; border-radius: 6px; background: transparent; color: var(--text-tertiary); cursor: pointer; transition: color var(--tabs-dur) var(--tabs-ease); }
.ui-layout-switcher button:hover { color: var(--text); }
.ui-layout-switcher button[aria-pressed='true'] { color: var(--accent-readable); }
.ui-layout-switcher button:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
@media (prefers-reduced-motion: reduce) { .ui-layout-switcher__indicator, .ui-layout-switcher button { transition: none; } }
</style>
