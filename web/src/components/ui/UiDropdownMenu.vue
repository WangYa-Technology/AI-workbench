<script setup lang="ts">
import { nextTick, ref } from 'vue'
import { onClickOutside } from '@vueuse/core'
const open = ref(false)
const root = ref<globalThis.HTMLElement | null>(null)
onClickOutside(root, () => { open.value = false })
function close() { open.value = false; root.value?.querySelector('button')?.focus() }
async function navigate(event: globalThis.KeyboardEvent) {
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  open.value = true
  await nextTick()
  const items = Array.from(root.value?.querySelectorAll<globalThis.HTMLElement>('.ui-dropdown-menu__content button:not(:disabled), .ui-dropdown-menu__content a[href]') || [])
  if (!items.length) return
  const index = items.indexOf(globalThis.document.activeElement as globalThis.HTMLElement)
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : index < 0 ? (event.key === 'ArrowUp' ? items.length - 1 : 0) : (index + (event.key === 'ArrowUp' ? -1 : 1) + items.length) % items.length
  items[next]?.focus()
}
withDefaults(defineProps<{ label?: string }>(), { label: '' })
</script>

<template>
  <div ref="root" class="ui-dropdown-menu" @keydown="navigate" @keydown.esc.stop.prevent="close" @focusout="event => { if (!root?.contains(event.relatedTarget as globalThis.Node)) open = false }">
    <button class="ui-button" type="button" :aria-label="label || undefined" aria-haspopup="menu" :aria-expanded="open" @click="open = !open">
      <slot name="trigger">
        {{ label }}
      </slot>
    </button><Transition name="ui-dropdown">
      <div v-if="open" class="ui-dropdown-menu__content ui-menu-surface" role="menu" @click="open = false">
        <slot></slot>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.ui-dropdown-menu { position: relative; display: inline-flex; }
.ui-dropdown-menu__content { position: absolute; z-index: 20; top: calc(100% + 6px); left: 0; min-width: 180px; padding: 6px; border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface); box-shadow: var(--shadow-panel); }
.ui-dropdown-enter-active, .ui-dropdown-leave-active { transition: opacity var(--duration-fast) var(--ease-smooth-out), transform var(--duration-fast) var(--ease-smooth-out); transform-origin: top left; }
.ui-dropdown-enter-from, .ui-dropdown-leave-to { opacity: 0; transform: scale(var(--scale-medium)); }
</style>
