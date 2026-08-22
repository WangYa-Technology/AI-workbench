<script setup lang="ts">
type NavigationItem = { value: string; label: string; href?: string; disabled?: boolean }
withDefaults(defineProps<{ items: NavigationItem[]; modelValue?: string; label?: string; orientation?: 'horizontal' | 'vertical'; variant?: 'pill' | 'line' }>(), { modelValue: '', label: 'Navigation', orientation: 'horizontal', variant: 'pill' })
const emit = defineEmits<{ 'update:modelValue': [value: string]; navigate: [item: NavigationItem] }>()
function activate(event: globalThis.MouseEvent, item: NavigationItem) { if (item.disabled) { event.preventDefault(); return } emit('update:modelValue', item.value); emit('navigate', item) }
</script>

<template><nav class="ui-navigation" :data-orientation="orientation" :data-variant="variant" :aria-label="label"><a v-for="item in items" :key="item.value" :href="item.href || '#'" :aria-current="item.value === modelValue ? 'page' : undefined" :aria-disabled="item.disabled || undefined" @click="activate($event, item)">{{ item.label }}</a></nav></template>
