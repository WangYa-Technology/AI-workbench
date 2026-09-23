<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch, type Component } from 'vue'
import type { RouteLocationRaw } from 'vue-router'
import UiButton from './UiButton.vue'

const props = defineProps<{
  title: string
  label?: string
  modelValue: string
  items: Array<{ value: string; label: string; icon: Component; count?: number; to?: RouteLocationRaw }>
}>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const navigation = ref<globalThis.HTMLElement | null>(null)
const indicator = ref<globalThis.HTMLElement | null>(null)
const positioned = ref(false)
let observer: globalThis.ResizeObserver | undefined

function updateIndicator(animate = false) {
  const active = navigation.value?.querySelector<globalThis.HTMLElement>('.ui-button.active')
  const pill = indicator.value
  if (!pill) return
  if (!active || !active.offsetWidth) {
    positioned.value = false
    return
  }
  const geometry = {
    transform: `translateY(${active.offsetTop}px)`,
    left: `${active.offsetLeft}px`,
    width: `${active.offsetWidth}px`,
    height: `${active.offsetHeight}px`,
  }
  if (positioned.value && Object.entries(geometry).every(([key, value]) => pill.style.getPropertyValue(key) === value)) return
  // Initial placement and responsive reflow must not animate from a stale position.
  const immediate = !animate || !positioned.value
  if (immediate) pill.style.transition = 'none'
  Object.assign(pill.style, geometry)
  if (immediate) {
    void pill.offsetHeight
    pill.style.removeProperty('transition')
  }
  positioned.value = true
}

watch([() => props.modelValue, () => props.items], ([value], [previousValue]) => updateIndicator(value !== previousValue), { deep: true, flush: 'post' })
onMounted(() => {
  updateIndicator()
  observer = new globalThis.ResizeObserver(() => updateIndicator())
  if (navigation.value) observer.observe(navigation.value)
})
onBeforeUnmount(() => observer?.disconnect())
</script>

<template>
  <aside class="ui-category-sidebar task-category-panel" :aria-label="label || title">
    <h2>{{ title }}</h2>
    <nav ref="navigation" :data-positioned="positioned">
      <span ref="indicator" class="ui-category-sidebar__indicator" aria-hidden="true"></span>
      <UiButton v-for="item in items" :key="item.value" :as="item.to ? 'RouterLink' : 'button'" :to="item.to" variant="ghost" :class="{ active: modelValue === item.value }" :aria-current="item.to && modelValue === item.value ? 'page' : undefined" :aria-pressed="item.to ? undefined : modelValue === item.value" @click="!item.to && emit('update:modelValue', item.value)">
        <component :is="item.icon" :size="17" aria-hidden="true" /><span>{{ item.label }}</span>
        <small v-if="item.count !== undefined">{{ item.count }}</small>
      </UiButton>
    </nav>
    <slot></slot>
  </aside>
</template>

<style scoped>
.ui-category-sidebar nav { position: relative; isolation: isolate; }
.ui-category-sidebar__indicator {
  position: absolute;
  top: 0;
  z-index: -1;
  border-radius: var(--radius-control);
  background: var(--accent-soft);
  pointer-events: none;
  visibility: hidden;
  transition: transform var(--tabs-dur) var(--tabs-ease), height var(--tabs-dur) var(--tabs-ease);
}
.ui-category-sidebar nav[data-positioned='true'] .ui-category-sidebar__indicator { visibility: visible; }
.ui-category-sidebar nav[data-positioned='true'] .ui-button.active { background: transparent; }
.ui-category-sidebar nav .ui-button { padding-block: 6px; transition: background-color var(--tabs-dur) var(--tabs-ease), color var(--tabs-dur) var(--tabs-ease); }
.ui-category-sidebar nav .ui-button > span { min-width: 0; white-space: normal; overflow-wrap: anywhere; }
.ui-category-sidebar nav .ui-button.active { transition-property: color; }
@media (prefers-reduced-motion: reduce) {
  .ui-category-sidebar__indicator, .ui-category-sidebar nav .ui-button { transition: none !important; }
}
</style>
