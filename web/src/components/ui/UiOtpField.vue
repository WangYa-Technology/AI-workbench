<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
const props = withDefaults(defineProps<{ modelValue?: string; length?: number; type?: 'numeric' | 'text'; disabled?: boolean; label?: string }>(), { modelValue: '', length: 6, type: 'numeric', disabled: false, label: 'One-time password' })
const emit = defineEmits<{ 'update:modelValue': [value: string]; complete: [value: string] }>()
const root = ref<globalThis.HTMLElement | null>(null)
const chars = ref(Array.from({ length: props.length }, (_, index) => props.modelValue[index] ?? ''))
watch(() => props.modelValue, value => { chars.value = Array.from({ length: props.length }, (_, index) => value[index] ?? '') })
function focus(index: number) { nextTick(() => root.value?.querySelectorAll<globalThis.HTMLInputElement>('input')[Math.max(0, Math.min(props.length - 1, index))]?.focus()) }
function commit() { const value = chars.value.join(''); emit('update:modelValue', value); if (value.length === props.length) emit('complete', value) }
function onInput(event: globalThis.Event, index: number) { const raw = (event.target as globalThis.HTMLInputElement).value; const value = props.type === 'numeric' ? raw.replace(/\D/g, '') : raw; chars.value[index] = value.slice(-1); commit(); if (chars.value[index]) focus(index + 1) }
function onKeydown(event: globalThis.KeyboardEvent, index: number) { if (event.key === 'Backspace' && !chars.value[index]) focus(index - 1); else if (event.key === 'ArrowLeft') { event.preventDefault(); focus(index - 1) } else if (event.key === 'ArrowRight') { event.preventDefault(); focus(index + 1) } }
function onPaste(event: globalThis.ClipboardEvent) { event.preventDefault(); const raw = event.clipboardData?.getData('text') ?? ''; const value = (props.type === 'numeric' ? raw.replace(/\D/g, '') : raw).slice(0, props.length); chars.value = Array.from({ length: props.length }, (_, index) => value[index] ?? ''); commit(); focus(Math.min(value.length, props.length - 1)) }
</script>

<template>
  <div ref="root" class="ui-otp-field" role="group" :aria-label="label" @paste="onPaste">
    <input v-for="(_, index) in chars" :key="index" :value="chars[index]" :inputmode="type === 'numeric' ? 'numeric' : 'text'" maxlength="1" autocomplete="one-time-code" :disabled="disabled" :aria-label="`${label} ${index + 1}`" @input="onInput($event, index)" @keydown="onKeydown($event, index)" />
  </div>
</template>
