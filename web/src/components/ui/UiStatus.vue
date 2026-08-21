<script setup lang="ts">
import UiBadge from './UiBadge.vue'
withDefaults(defineProps<{ variant?: 'neutral' | 'primary' | 'success' | 'warning' | 'danger'; label?: string }>(), { variant: 'neutral', label: '' })
</script>

<template>
  <UiBadge :variant="variant" dot>
    {{ label }}<slot></slot>
  </UiBadge>
</template>
