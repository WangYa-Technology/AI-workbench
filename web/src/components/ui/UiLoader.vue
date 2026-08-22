<script setup lang="ts">
withDefaults(defineProps<{ variant?: 'spinner' | 'dots' | 'bar'; label?: string; currentColor?: boolean }>(), { variant: 'spinner', label: 'Loading', currentColor: false })
</script>

<template>
  <span class="ui-loader" :data-variant="variant" :data-current-color="currentColor" role="status" :aria-label="label">
    <span v-if="variant === 'spinner'" class="ui-loader__spinner" aria-hidden="true"></span>
    <span v-else-if="variant === 'dots'" class="ui-loader__dots" aria-hidden="true"><i></i><i></i><i></i></span>
    <span v-else class="ui-loader__bar" aria-hidden="true"><i></i></span>
  </span>
</template>
