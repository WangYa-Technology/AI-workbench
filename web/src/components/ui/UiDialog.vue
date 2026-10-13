<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'
import { X } from 'lucide-vue-next'
const props = withDefaults(defineProps<{ open?: boolean; title?: string; labelledBy?: string; closeLabel?: string; size?: 'md' | 'lg' }>(), { open: false, title: '', labelledBy: '', closeLabel: 'Close', size: 'md' })
const emit = defineEmits<{ 'update:open': [value: boolean] }>()
const titleId = `ui-dialog-title-${useId()}`
const panel = ref<globalThis.HTMLDialogElement | null>(null)
let ownsBodyLock = false
const close = () => emit('update:open', false)
function release() {
  panel.value?.close()
  if (ownsBodyLock) globalThis.document.body.classList.remove('ui-dialog-open')
  ownsBodyLock = false
}
watch(() => props.open, async open => {
  if (!open) { release(); return }
  await nextTick()
  if (!props.open || !panel.value) return
  // Native modality makes the background inert and restores the opener.
  panel.value.showModal()
  ownsBodyLock = !globalThis.document.body.classList.contains('ui-dialog-open')
  globalThis.document.body.classList.add('ui-dialog-open')
}, { immediate: true })
onBeforeUnmount(release)
function containTab(event: globalThis.KeyboardEvent) {
  if (event.key !== 'Tab' || !panel.value) return
  const items = [...panel.value.querySelectorAll<globalThis.HTMLElement>('button:not(:disabled), a[href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])')].filter(item => item.getClientRects().length && item.tabIndex >= 0)
  const first = items[0]
  const last = items[items.length - 1]
  if (event.shiftKey && globalThis.document.activeElement === first) { event.preventDefault(); last?.focus() }
  else if (!event.shiftKey && globalThis.document.activeElement === last) { event.preventDefault(); first?.focus() }
}
function backdrop(event: globalThis.MouseEvent) {
  if (event.target !== panel.value || !panel.value) return
  const rect = panel.value.getBoundingClientRect()
  if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) close()
}
</script>

<template>
  <Teleport to="body">
    <dialog v-if="open" ref="panel" class="ui-dialog" :data-size="size" aria-modal="true" :aria-labelledby="labelledBy || titleId" @cancel.prevent="close" @click="backdrop" @keydown="containTab">
      <header class="ui-dialog__header">
        <slot name="header">
          <h2 :id="titleId">
            {{ title }}
          </h2>
        </slot>
        <button class="ui-icon-button" type="button" :aria-label="closeLabel" :title="closeLabel" autofocus @click="close">
          <X :size="18" aria-hidden="true" />
        </button>
      </header>
      <div class="ui-dialog__body">
        <slot></slot>
      </div>
      <footer v-if="$slots.footer" class="ui-dialog__footer">
        <slot name="footer"></slot>
      </footer>
    </dialog>
  </Teleport>
</template>

<style scoped>
.ui-dialog { width: min(560px, calc(100% - 32px)); max-width: none; max-height: calc(100dvh - 48px); margin: auto; padding: 0; overflow: hidden; color: var(--text); border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface); box-shadow: var(--shadow-panel); }
.ui-dialog[open] { display: flex; flex-direction: column; }
.ui-dialog[data-size='lg'] { width: min(680px, calc(100% - 32px)); }
.ui-dialog::backdrop { background: rgb(7 11 18 / 46%); }
.ui-dialog__header, .ui-dialog__footer { flex-shrink: 0; display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 16px 24px; border-bottom: 1px solid var(--border); }
.ui-dialog__header h2 { margin: 0; font-size: 16px; }
.ui-dialog__body { min-height: 0; overflow: auto; overscroll-behavior: contain; padding: 24px; }
.ui-dialog__footer { border-top: 1px solid var(--border); border-bottom: 0; justify-content: flex-end; flex-wrap: wrap; }
@media (max-width: 767px) {
  .ui-dialog { max-height: calc(100dvh - 24px); }
  .ui-dialog__header, .ui-dialog__footer, .ui-dialog__body { padding: 16px; }
}
</style>
