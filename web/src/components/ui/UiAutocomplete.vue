<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { Search } from 'lucide-vue-next'
type Option = { value: string; label: string; disabled?: boolean }
const props = withDefaults(defineProps<{ modelValue?: string; options: Array<string | Option>; placeholder?: string; disabled?: boolean; allowCustom?: boolean; noResultsText?: string }>(), { modelValue: '', placeholder: '', disabled: false, allowCustom: false, noResultsText: 'No results' })
const emit = defineEmits<{ 'update:modelValue': [value: string]; queryChange: [value: string] }>()
const root = ref<globalThis.HTMLElement | null>(null)
const input = ref<globalThis.HTMLInputElement | null>(null)
const open = ref(false)
const active = ref(0)
const listboxId = `ui-autocomplete-${useId()}`
const normalized = computed<Option[]>(() => props.options.map(item => typeof item === 'string' ? { value: item, label: item } : item))
const selected = computed(() => normalized.value.find(item => item.value === props.modelValue))
const query = ref(selected.value?.label ?? props.modelValue)
const filtered = computed(() => { const term = query.value.trim().toLocaleLowerCase(); return normalized.value.filter(item => !term || item.label.toLocaleLowerCase().includes(term)) })
watch(() => props.modelValue, () => { if (globalThis.document.activeElement !== input.value) query.value = selected.value?.label ?? props.modelValue })
function choose(option: Option) { if (option.disabled) return; emit('update:modelValue', option.value); query.value = option.label; open.value = false; nextTick(() => input.value?.focus()) }
function onInput(event: globalThis.Event) { query.value = (event.target as globalThis.HTMLInputElement).value; emit('queryChange', query.value); if (props.allowCustom) emit('update:modelValue', query.value); open.value = true; active.value = 0 }
function onKeydown(event: globalThis.KeyboardEvent) { if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); open.value = true; const count = filtered.value.length; if (count) active.value = (active.value + (event.key === 'ArrowDown' ? 1 : -1) + count) % count } else if (event.key === 'Enter' && open.value) { event.preventDefault(); const option = filtered.value[active.value]; if (option) choose(option) } else if (event.key === 'Escape') { open.value = false } }
function outside(event: globalThis.PointerEvent) { if (!root.value?.contains(event.target as globalThis.Node)) open.value = false }
onMounted(() => globalThis.document.addEventListener('pointerdown', outside))
onBeforeUnmount(() => globalThis.document.removeEventListener('pointerdown', outside))
</script>

<template><div ref="root" class="ui-autocomplete"><Search :size="16" aria-hidden="true" /><input ref="input" class="ui-input" role="combobox" :value="query" :placeholder="placeholder" :disabled="disabled" autocomplete="off" :aria-expanded="open" :aria-controls="listboxId" :aria-activedescendant="open && filtered[active] ? `${listboxId}-${active}` : undefined" @focus="open = true" @input="onInput" @keydown="onKeydown" /><Transition name="ui-dropdown"><div v-if="open" :id="listboxId" class="ui-autocomplete__list" role="listbox"><button v-for="(option, index) in filtered" :id="`${listboxId}-${index}`" :key="option.value" type="button" role="option" :aria-selected="option.value === modelValue" :data-active="index === active" :disabled="option.disabled" @mousedown.prevent @click="choose(option)">{{ option.label }}</button><p v-if="!filtered.length">{{ noResultsText }}</p></div></Transition></div></template>
