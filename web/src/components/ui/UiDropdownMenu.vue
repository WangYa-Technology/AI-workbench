<script setup lang="ts">
import { ref } from 'vue'
const open = ref(false)
withDefaults(defineProps<{ label?: string }>(), { label: '' })
</script>

<template>
  <div class="ui-dropdown-menu">
    <button class="ui-button" type="button" aria-haspopup="menu" :aria-expanded="open" @click="open = !open">
      <slot name="trigger">
        {{ label }}
      </slot>
    </button><Transition name="ui-dropdown">
      <div v-if="open" class="ui-dropdown-menu__content" role="menu">
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
