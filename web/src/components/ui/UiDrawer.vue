<script setup lang="ts">
withDefaults(defineProps<{ open?: boolean; side?: 'left' | 'right' }>(), { open: false, side: 'right' })
const emit = defineEmits<{ 'update:open': [value: boolean] }>()
</script>

<template>
  <Teleport to="body">
    <Transition name="ui-drawer">
      <div v-if="open" class="ui-drawer-backdrop" @mousedown.self="emit('update:open', false)">
        <aside class="ui-drawer" :data-side="side">
          <slot></slot>
        </aside>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.ui-drawer-backdrop { position: fixed; z-index: 100; inset: 0; background: rgb(7 11 18 / 36%); }
.ui-drawer { position: absolute; inset-block: 0; width: min(420px, 90vw); padding: 20px; overflow: auto; border: 1px solid var(--border); background: var(--surface); box-shadow: var(--shadow-panel); }
.ui-drawer[data-side='right'] { right: 0; }
.ui-drawer[data-side='left'] { left: 0; }
.ui-drawer-enter-active, .ui-drawer-leave-active { transition: opacity var(--duration-fast) var(--ease-smooth-out); }
.ui-drawer-enter-active .ui-drawer, .ui-drawer-leave-active .ui-drawer { transition: transform var(--duration-fast) var(--ease-smooth-out); }
.ui-drawer-enter-from, .ui-drawer-leave-to { opacity: 0; }
.ui-drawer-enter-from .ui-drawer[data-side='right'], .ui-drawer-leave-to .ui-drawer[data-side='right'] { transform: translateX(100%); }
.ui-drawer-enter-from .ui-drawer[data-side='left'], .ui-drawer-leave-to .ui-drawer[data-side='left'] { transform: translateX(-100%); }
</style>
