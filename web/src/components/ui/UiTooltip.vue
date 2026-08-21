<script setup lang="ts">
defineProps<{ label: string }>()
</script>

<template><span class="ui-tooltip" :data-tooltip="label"><slot></slot></span></template>

<style scoped>
.ui-tooltip { position: relative; display: inline-flex; }
.ui-tooltip::after { content: attr(data-tooltip); position: absolute; z-index: 30; left: 50%; bottom: calc(100% + 7px); width: max-content; max-width: 220px; padding: 5px 8px; border-radius: 6px; background: var(--text); color: var(--canvas); font-size: 11px; line-height: 1.3; opacity: 0; pointer-events: none; transform: translate(-50%, 3px) scale(var(--scale-small)); transition: opacity var(--duration-quick) var(--ease-out), transform var(--duration-quick) var(--ease-out); }
.ui-tooltip:hover::after, .ui-tooltip:focus-within::after { opacity: 1; transform: translate(-50%, 0) scale(1); }
</style>
