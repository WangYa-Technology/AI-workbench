<script setup lang="ts">
import { ref, watch } from 'vue'
const props = defineProps<{ loading: boolean }>()
const root = ref<globalThis.HTMLElement | null>(null)
const retainedHeight = ref(0)
// Measure the previous results before Vue replaces them with the loading state.
watch(() => props.loading, (loading, previous) => {
  if (loading && !previous) retainedHeight.value = root.value?.getBoundingClientRect().height ?? 0
  if (!loading) retainedHeight.value = 0
}, { flush: 'pre' })
</script>

<template>
  <div ref="root" class="catalog-results" :aria-busy="loading" :style="{ minHeight: loading && retainedHeight ? `${retainedHeight}px` : undefined }">
    <slot></slot>
  </div>
</template>

<style scoped>
.catalog-results { min-width: 0; overflow-anchor: none; }
</style>
