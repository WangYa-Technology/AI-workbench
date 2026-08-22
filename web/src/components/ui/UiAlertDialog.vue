<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'
import UiButton from './UiButton.vue'
const props = withDefaults(defineProps<{ open?: boolean; title: string; description?: string; confirmLabel?: string; cancelLabel?: string; destructive?: boolean; busy?: boolean }>(), { open: false, description: '', confirmLabel: 'Confirm', cancelLabel: 'Cancel', destructive: false, busy: false })
const emit = defineEmits<{ 'update:open': [value: boolean]; confirm: []; cancel: [] }>()
const titleId = `ui-alert-dialog-${useId()}`
const dialog = ref<globalThis.HTMLElement | null>(null)
function close(cancelled = false) { if (props.busy) return; emit('update:open', false); if (cancelled) emit('cancel') }
function keydown(event: globalThis.KeyboardEvent) { if (event.key === 'Escape' && props.open) close(true); if (event.key !== 'Tab' || !dialog.value) return; const items = [...dialog.value.querySelectorAll<globalThis.HTMLElement>('button,[href],[tabindex]:not([tabindex="-1"])')]; if (!items.length) return; const first = items[0]; const last = items[items.length - 1]; if (event.shiftKey && globalThis.document.activeElement === first) { event.preventDefault(); last.focus() } else if (!event.shiftKey && globalThis.document.activeElement === last) { event.preventDefault(); first.focus() } }
watch(() => props.open, value => { globalThis.document.body.classList.toggle('ui-dialog-open', value); if (value) nextTick(() => dialog.value?.querySelector<globalThis.HTMLElement>('button')?.focus()) })
onBeforeUnmount(() => globalThis.document.body.classList.remove('ui-dialog-open'))
</script>

<template><Teleport to="body"><Transition name="ui-dialog"><div v-if="open" class="ui-dialog-backdrop" @keydown="keydown"><section ref="dialog" class="ui-alert-dialog" role="alertdialog" aria-modal="true" :aria-labelledby="titleId"><header><h2 :id="titleId">{{ title }}</h2><p v-if="description">{{ description }}</p></header><div v-if="$slots.default" class="ui-alert-dialog__body"><slot></slot></div><footer><UiButton variant="secondary" :disabled="busy" @click="close(true)">{{ cancelLabel }}</UiButton><UiButton :variant="destructive ? 'destructive' : 'primary'" :loading="busy" @click="emit('confirm')">{{ confirmLabel }}</UiButton></footer></section></div></Transition></Teleport></template>
