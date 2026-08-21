<script setup lang="ts">
withDefaults(defineProps<{ modelValue?: number; min?: number; max?: number; step?: number }>(), { modelValue: 0, min: 0, max: 100, step: 1 })
const emit = defineEmits<{ 'update:modelValue': [value: number] }>()
</script>

<template><input class="ui-slider" type="range" :min="min" :max="max" :step="step" :value="modelValue" @input="emit('update:modelValue', Number(($event.target as HTMLInputElement).value))" /></template>
