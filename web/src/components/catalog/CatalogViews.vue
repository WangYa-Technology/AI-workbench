<script setup lang="ts">
import { nextTick, ref, watch, type Component } from 'vue'
import UiTabs from '../ui/UiTabs.vue'

defineOptions({ inheritAttrs: false })
const props = defineProps<{
  modelValue: string
  items: Array<{ value: string; label: string; icon?: Component; disabled?: boolean }>
  label: string
  count: number
  pending?: boolean
  status: string
}>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const viewport = ref<globalThis.HTMLElement | null>(null)
function revealSelected() {
  const root = viewport.value
  const active = root?.querySelector('[aria-selected="true"]')
  if (!root || !active) return
  const bounds = root.getBoundingClientRect(), tab = active.getBoundingClientRect()
  if (tab.left < bounds.left) root.scrollLeft += tab.left - bounds.left
  else if (tab.right > bounds.right) root.scrollLeft += tab.right - bounds.right
}
watch(() => [props.modelValue, props.count, props.items], () => void nextTick(revealSelected))
watch(viewport, (element, _, onCleanup) => {
  if (!element) return
  const observer = new globalThis.ResizeObserver(revealSelected)
  observer.observe(element)
  revealSelected()
  onCleanup(() => observer.disconnect())
}, { flush: 'post' })
</script>

<template>
  <div v-if="items.length > 1" ref="viewport" class="catalog-views view-switcher-bar">
    <UiTabs v-bind="$attrs" class="catalog-view-tabs" :model-value="modelValue" :items="items" :label="label" @update:model-value="emit('update:modelValue', $event)">
      <template #label="{ item, selected }">
        <component :is="item.icon" v-if="item.icon" :size="16" aria-hidden="true" />
        <span>{{ item.label }}</span>
        <span v-if="selected" class="catalog-view-count" aria-hidden="true">{{ pending ? '—' : count }}</span>
      </template>
    </UiTabs>
  </div>
  <span class="sr-only" role="status" aria-live="polite">{{ status }}</span>
</template>
