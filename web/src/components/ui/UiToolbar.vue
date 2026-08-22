<script setup lang="ts">
withDefaults(defineProps<{ label?: string; orientation?: 'horizontal' | 'vertical' }>(), { label: '', orientation: 'horizontal' })
</script>

<template><div class="ui-toolbar" role="toolbar" :aria-label="label || undefined" :aria-orientation="orientation" :data-orientation="orientation"><slot></slot></div></template>
