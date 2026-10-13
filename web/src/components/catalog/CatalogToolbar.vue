<script setup lang="ts">
import { ref, watch } from 'vue'
import CatalogStickyBar from '../layout/CatalogStickyBar.vue'
import UiFilterBar from '../ui/UiFilterBar.vue'

// Layout follows the rendered CatalogViews and .catalog-display group in the slot.
// Keep business controls in .catalog-controls; no page-specific alignment prop is needed.
defineProps<{ title: string; filterClass?: string }>()
const emit = defineEmits<{ resize: [height: number] }>()
const controls = ref<InstanceType<typeof UiFilterBar> | null>(null)
watch(controls, (value, _, onCleanup) => {
  const element = value?.$el
  if (!(element instanceof globalThis.HTMLElement)) return
  const measure = () => emit('resize', element.getBoundingClientRect().height)
  const observer = new globalThis.ResizeObserver(measure)
  observer.observe(element)
  measure()
  onCleanup(() => observer.disconnect())
}, { flush: 'post' })
</script>

<template>
  <CatalogStickyBar :title="title" class="catalog-toolbar">
    <UiFilterBar ref="controls" as="div" split class="catalog-toolbar__row" :class="filterClass">
      <slot></slot>
    </UiFilterBar>
  </CatalogStickyBar>
</template>

<style src="./catalog-page.css"></style>
