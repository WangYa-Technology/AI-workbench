<script setup lang="ts">
import { onMounted, onUnmounted, watch } from 'vue'
import { X } from 'lucide-vue-next'
const props = withDefaults(defineProps<{ open?: boolean; title?: string; labelledBy?: string }>(), { open: false, title: '', labelledBy: '' })
const emit = defineEmits<{ 'update:open': [value: boolean] }>()
const close = () => emit('update:open', false)
const onKeydown = (event: globalThis.KeyboardEvent) => { if (event.key === 'Escape' && props.open) close() }
watch(() => props.open, (value) => { globalThis.document.body.classList.toggle('ui-dialog-open', value) })
onMounted(() => globalThis.document.addEventListener('keydown', onKeydown))
onUnmounted(() => { globalThis.document.removeEventListener('keydown', onKeydown); globalThis.document.body.classList.remove('ui-dialog-open') })
</script>

<template>
  <Teleport to="body">
    <Transition name="ui-dialog">
      <div v-if="open" class="ui-dialog-backdrop" @mousedown.self="close">
        <section class="ui-dialog" role="dialog" aria-modal="true" :aria-labelledby="labelledBy || undefined">
          <header v-if="title || $slots.header" class="ui-dialog__header">
            <slot name="header">
              <h2>{{ title }}</h2>
            </slot><button class="ui-icon-button" type="button" aria-label="Close" title="Close" @click="close">
              <X :size="16" aria-hidden="true" />
            </button>
          </header><div class="ui-dialog__body">
            <slot></slot>
          </div><footer v-if="$slots.footer" class="ui-dialog__footer">
            <slot name="footer"></slot>
          </footer>
        </section>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.ui-dialog-backdrop { position: fixed; z-index: 100; inset: 0; display: grid; place-items: center; padding: 24px; background: rgb(7 11 18 / 46%); }
.ui-dialog { width: min(100%, 560px); max-height: min(720px, calc(100dvh - 48px)); overflow: auto; border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface); box-shadow: var(--shadow-panel); }
.ui-dialog__header, .ui-dialog__footer { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 16px 18px; border-bottom: 1px solid var(--border); }
.ui-dialog__header h2 { margin: 0; font-size: 16px; }
.ui-dialog__body { padding: 18px; }
.ui-dialog__footer { border-top: 1px solid var(--border); border-bottom: 0; justify-content: flex-end; }
.ui-dialog-enter-active, .ui-dialog-leave-active { transition: opacity var(--duration-fast) var(--ease-smooth-out); }
.ui-dialog-enter-active .ui-dialog, .ui-dialog-leave-active .ui-dialog { transition: transform var(--duration-fast) var(--ease-smooth-out), opacity var(--duration-fast) var(--ease-smooth-out); }
.ui-dialog-enter-from, .ui-dialog-leave-to { opacity: 0; }
.ui-dialog-enter-from .ui-dialog, .ui-dialog-leave-to .ui-dialog { opacity: 0; transform: scale(var(--scale-large)); }
</style>
