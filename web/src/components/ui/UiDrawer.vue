<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

let openDrawerCount = 0

const props = withDefaults(defineProps<{
  open?: boolean
  side?: 'left' | 'right'
  size?: 'sm' | 'md' | 'lg' | 'xl'
  label?: string
}>(), { open: false, side: 'right', size: 'md', label: '' })
const emit = defineEmits<{ 'update:open': [value: boolean]; 'after-close': [] }>()
const panel = ref<globalThis.HTMLElement | null>(null)
let returnFocus: globalThis.HTMLElement | null = null
let bodyLocked = false

const focusableSelector = 'button:not(:disabled), [href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])'

function setBodyLock(locked: boolean) {
  if (locked === bodyLocked) return
  bodyLocked = locked
  openDrawerCount = Math.max(0, openDrawerCount + (locked ? 1 : -1))
  const hasOpenDrawer = openDrawerCount > 0
  globalThis.document.body.classList.toggle('ui-dialog-open', hasOpenDrawer)
  const appRoot = globalThis.document.getElementById('app')
  appRoot?.toggleAttribute('inert', hasOpenDrawer)
  if (hasOpenDrawer) appRoot?.setAttribute('aria-hidden', 'true')
  else appRoot?.removeAttribute('aria-hidden')
}

function close() {
  emit('update:open', false)
}

function onKeydown(event: globalThis.KeyboardEvent) {
  if (!props.open) return
  if (event.key === 'Escape') {
    event.preventDefault()
    close()
    return
  }
  if (event.key !== 'Tab' || !panel.value) return
  const focusable = [...panel.value.querySelectorAll<globalThis.HTMLElement>(focusableSelector)]
  if (!focusable.length) {
    event.preventDefault()
    panel.value.focus()
    return
  }
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  if (event.shiftKey && globalThis.document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && globalThis.document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

watch(() => props.open, (open) => {
  setBodyLock(open)
  if (open) {
    returnFocus = globalThis.document.activeElement instanceof globalThis.HTMLElement ? globalThis.document.activeElement : null
    void nextTick(() => panel.value?.querySelector<globalThis.HTMLElement>(focusableSelector)?.focus())
  } else if (returnFocus) {
    const target = returnFocus
    returnFocus = null
    void nextTick(() => target.isConnected && target.focus())
  }
}, { immediate: true })

onMounted(() => globalThis.document.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => {
  globalThis.document.removeEventListener('keydown', onKeydown)
  setBodyLock(false)
})
</script>

<template>
  <Teleport to="body">
    <Transition name="ui-drawer" @after-leave="emit('after-close')">
      <div v-if="open" class="ui-drawer-backdrop" @mousedown.self="close">
        <aside ref="panel" class="ui-drawer" :data-side="side" :data-size="size" role="dialog" aria-modal="true" :aria-label="label || undefined" tabindex="-1">
          <slot></slot>
        </aside>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.ui-drawer-backdrop { position: fixed; z-index: 100; inset: 0; overflow: hidden; background: rgb(7 11 18 / 42%); }
.ui-drawer { position: absolute; inset-block: 0; width: min(480px, calc(100vw - 16px)); overflow: hidden; border: 1px solid var(--border); border-radius: 8px 0 0 8px; outline: 0; background: var(--surface); box-shadow: -20px 0 56px rgb(4 8 14 / 18%); }
.ui-drawer[data-size='sm'] { width: min(400px, calc(100vw - 16px)); }
.ui-drawer[data-size='lg'] { width: min(640px, calc(100vw - 16px)); }
.ui-drawer[data-size='xl'] { width: min(780px, calc(100vw - 16px)); }
.ui-drawer[data-side='right'] { right: 0; }
.ui-drawer[data-side='left'] { left: 0; border-radius: 0 8px 8px 0; }
.ui-drawer-enter-active { transition: opacity var(--duration-slow) var(--ease-smooth-out); }
.ui-drawer-leave-active { transition: opacity var(--duration-medium) var(--ease-smooth-out); }
.ui-drawer-enter-active .ui-drawer { transition: transform var(--duration-slow) var(--ease-smooth-out), filter var(--duration-slow) var(--ease-smooth-out); }
.ui-drawer-leave-active .ui-drawer { transition: transform var(--duration-medium) var(--ease-smooth-out), filter var(--duration-medium) var(--ease-smooth-out); }
.ui-drawer-enter-from, .ui-drawer-leave-to { opacity: 0; }
.ui-drawer-enter-from .ui-drawer[data-side='right'], .ui-drawer-leave-to .ui-drawer[data-side='right'] { transform: translateX(var(--distance-large)); filter: blur(var(--blur-small)); }
.ui-drawer-enter-from .ui-drawer[data-side='left'], .ui-drawer-leave-to .ui-drawer[data-side='left'] { transform: translateX(calc(-1 * var(--distance-large))); filter: blur(var(--blur-small)); }
@media (max-width: 767px) {
  .ui-drawer, .ui-drawer[data-size] { width: 100vw; max-width: 100%; border-radius: 0; }
}
@media (prefers-reduced-motion: reduce) {
  .ui-drawer-enter-active, .ui-drawer-leave-active, .ui-drawer-enter-active .ui-drawer, .ui-drawer-leave-active .ui-drawer { transition-duration: 0.01ms; }
  .ui-drawer-enter-from .ui-drawer, .ui-drawer-leave-to .ui-drawer { transform: none; filter: none; }
}
</style>
