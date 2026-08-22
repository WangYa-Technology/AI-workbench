<script setup lang="ts">
import UiColorSwatch from './UiColorSwatch.vue'
type Swatch = string | { value: string; label?: string; disabled?: boolean }
withDefaults(defineProps<{ modelValue?: string; colors: Swatch[]; label?: string }>(), { modelValue: '', label: 'Choose color' })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
function value(item: Swatch) { return typeof item === 'string' ? item : item.value }
</script>

<template><div class="ui-color-swatch-picker" role="radiogroup" :aria-label="label"><UiColorSwatch v-for="item in colors" :key="value(item)" role="radio" :color="value(item)" :label="typeof item === 'string' ? item : item.label" :selected="modelValue === value(item)" :disabled="typeof item === 'string' ? false : item.disabled" @select="emit('update:modelValue', $event)" /></div></template>
