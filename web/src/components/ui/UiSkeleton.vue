<script setup lang="ts">
withDefaults(defineProps<{ width?: string; height?: string }>(), { width: '100%', height: '16px' })
</script>

<template><span class="ui-skeleton" :style="{ width, height }" aria-hidden="true"></span></template>
