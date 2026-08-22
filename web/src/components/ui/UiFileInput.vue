<script setup lang="ts">
import { ref } from 'vue'

defineOptions({ inheritAttrs: false })
withDefaults(defineProps<{ size?: 'sm' | 'md' | 'lg' }>(), { size: 'md' })
const input = ref<globalThis.HTMLInputElement | null>(null)
defineExpose({ click: () => input.value?.click(), focus: () => input.value?.focus() })
</script>

<template>
  <input ref="input" v-bind="$attrs" class="ui-file-input" :data-size="size" type="file" />
</template>

<style scoped>
.ui-file-input {
  width: 100%;
  min-width: 0;
  min-height: var(--ui-height, var(--control-height-md));
  padding: 4px;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  background: var(--surface);
  color: var(--text-secondary);
  font-size: 12px;
  cursor: pointer;
}

.ui-file-input:hover:not(:disabled) { border-color: var(--border-strong); }
.ui-file-input:focus-visible { border-color: var(--focus); box-shadow: var(--focus-ring); outline: 0; }
.ui-file-input:disabled { cursor: not-allowed; opacity: .56; }
.ui-file-input[data-size='sm'] { --ui-height: var(--control-height-sm); font-size: 11px; }
.ui-file-input[data-size='lg'] { --ui-height: var(--control-height-lg); font-size: 13px; }
.ui-file-input::file-selector-button {
  min-height: calc(var(--ui-height, var(--control-height-md)) - 10px);
  margin-right: 8px;
  padding: 0 10px;
  border: 1px solid var(--border-strong);
  border-radius: calc(var(--radius-control) - 2px);
  background: var(--surface-muted);
  color: var(--text);
  font: inherit;
  cursor: pointer;
}

.ui-file-input::file-selector-button:hover { background: var(--surface-raised); }
</style>
