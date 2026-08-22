<script setup lang="ts">
withDefaults(defineProps<{ orientation?: 'horizontal' | 'vertical'; decorative?: boolean }>(), { orientation: 'horizontal', decorative: true })
</script>

<template><div class="ui-separator" :data-orientation="orientation" :role="decorative ? 'none' : 'separator'" :aria-orientation="decorative ? undefined : orientation"></div></template>
