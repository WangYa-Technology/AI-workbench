<script setup lang="ts">
withDefaults(defineProps<{ open?: boolean; message?: string; variant?: 'info' | 'success' | 'danger' }>(), { open: false, message: '', variant: 'info' })
</script>

<template>
  <Transition name="ui-toast">
    <div v-if="open" class="ui-toast" :data-variant="variant" role="status">
      <slot>{{ message }}</slot>
    </div>
  </Transition>
</template>

<style scoped>
.ui-toast { position: fixed; z-index: 110; right: 24px; bottom: 24px; max-width: min(360px, calc(100vw - 32px)); padding: 12px 14px; border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface); color: var(--text); box-shadow: var(--shadow-panel); }
.ui-toast-enter-active, .ui-toast-leave-active { transition: opacity var(--duration-medium) var(--ease-smooth-out), transform var(--duration-medium) var(--ease-smooth-out); }
.ui-toast-enter-from, .ui-toast-leave-to { opacity: 0; transform: translateY(8px); }
</style>
