<script setup lang="ts">
withDefaults(defineProps<{ maxHeight?: string; orientation?: 'vertical' | 'horizontal' | 'both' }>(), { maxHeight: 'none', orientation: 'vertical' })
</script>

<template><div class="ui-scroll-area" :data-orientation="orientation" :style="{ maxHeight }"><slot></slot></div></template>
