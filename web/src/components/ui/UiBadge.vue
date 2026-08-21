<script setup lang="ts">
withDefaults(defineProps<{ variant?: 'neutral' | 'primary' | 'success' | 'warning' | 'danger'; dot?: boolean }>(), { variant: 'neutral', dot: false })
</script>

<template><span class="ui-badge" :data-variant="variant"><span v-if="dot" aria-hidden="true">•</span><slot></slot></span></template>
