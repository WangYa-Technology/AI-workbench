<script setup lang="ts">
withDefaults(defineProps<{ open?: boolean }>(), { open: false })
</script>

<template>
  <div class="ui-popover">
    <slot name="trigger"></slot><Transition name="ui-popover">
      <div v-if="open" class="ui-popover__content">
        <slot></slot>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.ui-popover { position: relative; }
.ui-popover__content { position: absolute; z-index: 20; min-width: 180px; padding: 10px; border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface); box-shadow: var(--shadow-panel); }
.ui-popover-enter-active, .ui-popover-leave-active { transition: opacity var(--duration-fast) var(--ease-smooth-out), transform var(--duration-fast) var(--ease-smooth-out); }
.ui-popover-enter-from, .ui-popover-leave-to { opacity: 0; transform: translateY(4px) scale(var(--scale-medium)); }
</style>
