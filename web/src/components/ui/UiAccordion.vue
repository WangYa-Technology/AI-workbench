<script setup lang="ts">
import { ChevronDown } from 'lucide-vue-next'
type Item = { value: string; title: string; content?: string; disabled?: boolean }
const props = withDefaults(defineProps<{ items: Item[]; modelValue?: string | string[]; multiple?: boolean }>(), { modelValue: '', multiple: false })
const emit = defineEmits<{ 'update:modelValue': [value: string | string[]] }>()
function isOpen(value: string) { return Array.isArray(props.modelValue) ? props.modelValue.includes(value) : props.modelValue === value }
function toggle(value: string) { if (!props.multiple) return emit('update:modelValue', isOpen(value) ? '' : value); const current = Array.isArray(props.modelValue) ? props.modelValue : []; emit('update:modelValue', current.includes(value) ? current.filter(item => item !== value) : [...current, value]) }
</script>

<template><div class="ui-accordion"><section v-for="item in items" :key="item.value" class="ui-collapsible" :data-open="isOpen(item.value)"><h3><button class="ui-collapsible__trigger" type="button" :aria-expanded="isOpen(item.value)" :disabled="item.disabled" @click="toggle(item.value)"><slot name="trigger" :item="item" :open="isOpen(item.value)">{{ item.title }}</slot><ChevronDown :size="16" aria-hidden="true" /></button></h3><div class="ui-collapsible__panel" role="region"><div class="ui-collapsible__inner"><slot name="item" :item="item">{{ item.content }}</slot></div></div></section></div></template>
