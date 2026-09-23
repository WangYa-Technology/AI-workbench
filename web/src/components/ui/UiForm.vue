<script setup lang="ts">
// Business forms share control density and surface; pages retain their field layout.
const props = withDefaults(defineProps<{ surface?: 'default' | 'muted'; disabled?: boolean; novalidate?: boolean }>(), { surface: 'default', disabled: false, novalidate: false })
const emit = defineEmits<{ submit: [event: globalThis.SubmitEvent]; invalid: [event: globalThis.Event] }>()
function submit(event: globalThis.SubmitEvent) {
  if (props.disabled) return
  const form = event.currentTarget as globalThis.HTMLFormElement
  if (!props.novalidate && !form.checkValidity()) { emit('invalid', event); return }
  emit('submit', event)
}
</script>

<template>
  <form class="ui-form" :data-surface="surface" :novalidate="novalidate" @submit.prevent="submit">
    <fieldset :disabled="disabled">
      <slot></slot>
    </fieldset>
  </form>
</template>

<style scoped>
.ui-form > fieldset { min-width: 0; margin: 0; padding: 0; border: 0; }
.ui-form { min-width: 0; display: grid; gap: var(--ui-form-gap, 16px); }
.ui-form > fieldset { display: contents; }
.ui-form :deep(label:has(> :is(.ui-input, .ui-select-root, .ui-textarea))) { min-width: 0; display: grid; gap: 6px; color: var(--text-secondary); font-size: 12px; font-weight: 600; }
.ui-form :deep(.ui-input),
.ui-form :deep(.ui-select-root) { --ui-height: var(--control-height-lg); }
.ui-form[data-surface='muted'] { --ui-field-background: var(--canvas); }
.ui-form[data-surface='default'] { --ui-field-background: var(--surface); }
</style>
