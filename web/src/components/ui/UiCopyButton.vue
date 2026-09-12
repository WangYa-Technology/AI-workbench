<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { Check, Copy } from 'lucide-vue-next'
const props = withDefaults(defineProps<{ value: string; label?: string; copiedLabel?: string; disabled?: boolean }>(), { label: 'Copy', copiedLabel: 'Copied', disabled: false })
const emit = defineEmits<{ copied: [value: string]; error: [error: unknown] }>()
const copied = ref(false)
let timer: ReturnType<typeof globalThis.setTimeout> | undefined
async function copy() { try { await globalThis.navigator.clipboard.writeText(props.value); copied.value = true; emit('copied', props.value); globalThis.clearTimeout(timer); timer = globalThis.setTimeout(() => { copied.value = false }, 1600) } catch (error) { emit('error', error) } }
onBeforeUnmount(() => globalThis.clearTimeout(timer))
</script>

<template>
  <button class="ui-copy-button ui-button" type="button" data-variant="outline" :disabled="disabled" :aria-label="copied ? copiedLabel : label" @click="copy">
    <span class="t-icon-swap" :data-state="copied ? 'b' : 'a'" aria-hidden="true"><Copy class="t-icon" data-icon="a" :size="15" /><Check class="t-icon" data-icon="b" :size="15" /></span><span>{{ copied ? copiedLabel : label }}</span>
  </button>
</template>
