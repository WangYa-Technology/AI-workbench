<script setup lang="ts">
defineOptions({ inheritAttrs: false })
withDefaults(defineProps<{ modelValue?: string; min?: string; max?: string; step?: number; disabled?: boolean; invalid?: boolean }>(), { modelValue: '', min: undefined, max: undefined, step: 60, disabled: false, invalid: false })
const emit = defineEmits<{ 'update:modelValue': [value: string]; change: [event: globalThis.Event] }>()
function update(event: globalThis.Event) { emit('update:modelValue', (event.target as globalThis.HTMLInputElement).value); emit('change', event) }
</script>

<template><input v-bind="$attrs" class="ui-input ui-time-field" type="time" :value="modelValue" :min="min" :max="max" :step="step" :disabled="disabled" :aria-invalid="invalid || undefined" @change="update" /></template>
