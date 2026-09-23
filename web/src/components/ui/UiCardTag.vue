<script setup lang="ts">
import UiBadge from './UiBadge.vue'
withDefaults(defineProps<{ variant?: 'neutral' | 'primary' | 'success' | 'warning' | 'danger' }>(), { variant: 'primary' })
</script>

<template>
  <UiBadge class="ui-card-tag" :variant="variant">
    <slot></slot>
  </UiBadge>
</template>

<style scoped>
.ui-card-tag { min-height: 22px; padding: 2px 7px; gap: 5px; border-radius: var(--radius-control); font-size: 10px; font-weight: 650; line-height: 1.4; }
.ui-card-tag :deep(svg) { width: 13px; height: 13px; flex-shrink: 0; }
</style>
