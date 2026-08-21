<script setup lang="ts">
import UiTabs from './UiTabs.vue'
defineProps<{ modelValue: string; items: Array<{ value: string; label: string; disabled?: boolean }> }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template><UiTabs :model-value="modelValue" :items="items" @update:model-value="emit('update:modelValue', $event)" /></template>
