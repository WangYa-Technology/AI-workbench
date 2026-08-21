<script setup lang="ts">
import UiInput from './UiInput.vue'
withDefaults(defineProps<{ modelValue?: string; placeholder?: string }>(), { modelValue: '', placeholder: '' })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template><UiInput :model-value="modelValue" :placeholder="placeholder" role="combobox" aria-autocomplete="list" @update:model-value="emit('update:modelValue', $event)" /><slot></slot></template>
