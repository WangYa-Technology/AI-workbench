<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

defineOptions({ inheritAttrs: false })
defineProps<{ title: string }>()
const marker = ref<globalThis.HTMLElement | null>(null)
const bar = ref<globalThis.HTMLElement | null>(null)
const titleText = ref<globalThis.HTMLElement | null>(null)
const titleWidth = ref(0)
const stuck = ref(false)
let intersection: globalThis.IntersectionObserver | undefined
let resize: globalThis.ResizeObserver | undefined
let titleResize: globalThis.ResizeObserver | undefined

function observe() {
  intersection?.disconnect()
  const element = bar.value
  const sentinel = marker.value
  if (!element || !sentinel) return
  const scrollRoot = element.closest<globalThis.HTMLElement>('#main-content')
  const mobile = globalThis.matchMedia('(max-width: 767px)').matches
  const top = Number.parseFloat(globalThis.getComputedStyle(element).top)
  const padding = !mobile && scrollRoot ? Number.parseFloat(globalThis.getComputedStyle(scrollRoot).paddingTop) : 0
  const offset = top + padding
  const edge = mobile ? offset : (scrollRoot?.getBoundingClientRect().top ?? 0) + (scrollRoot?.clientTop ?? 0) + offset
  stuck.value = sentinel.getBoundingClientRect().bottom <= edge
  intersection = new globalThis.IntersectionObserver(([entry]) => {
    if (entry) stuck.value = !entry.isIntersecting && entry.boundingClientRect.bottom <= (entry.rootBounds?.top ?? edge)
  }, { root: mobile ? null : scrollRoot, rootMargin: `-${offset}px 0px 0px`, threshold: 0 })
  intersection.observe(sentinel)
}

onMounted(() => {
  titleResize = new globalThis.ResizeObserver(() => {
    titleWidth.value = titleText.value?.getBoundingClientRect().width ?? 0
  })
  if (titleText.value) titleResize.observe(titleText.value)
  resize = new globalThis.ResizeObserver(observe)
  if (bar.value) {
    resize.observe(bar.value)
    const root = bar.value.closest('#main-content')
    if (root) resize.observe(root)
  }
  observe()
})
onBeforeUnmount(() => { intersection?.disconnect(); resize?.disconnect(); titleResize?.disconnect() })
</script>

<template>
  <div ref="marker" class="catalog-sticky-marker" aria-hidden="true"></div>
  <div ref="bar" v-bind="$attrs" class="catalog-sticky-bar" :class="{ 'is-stuck': stuck }" data-catalog-sticky>
    <div class="catalog-sticky-title" :style="{ '--catalog-title-width': `${titleWidth}px` }" aria-hidden="true">
      <span ref="titleText">{{ title }}</span>
    </div>
    <slot></slot>
  </div>
</template>

<style scoped>
.catalog-sticky-marker { height: 1px; margin-bottom: -1px; pointer-events: none; }
.catalog-sticky-bar { position: sticky; top: var(--catalog-sticky-top); z-index: 18; isolation: isolate; display: flex; align-items: flex-start; gap: 0; margin-bottom: 24px; transition: gap var(--duration-fast) var(--ease-smooth-out); }
/* The title and controls share one row and one continuous backdrop. */
.catalog-sticky-bar::before { content: ''; position: absolute; inset: 0 calc(-1 * var(--catalog-sticky-bleed)); z-index: -1; pointer-events: none; visibility: hidden; opacity: 0; background: var(--content-sticky-background); border-bottom: 1px solid var(--content-sticky-border); -webkit-backdrop-filter: var(--content-sticky-backdrop); backdrop-filter: var(--content-sticky-backdrop); transition: opacity var(--duration-fast) var(--ease-smooth-out), visibility 0s linear var(--duration-fast); }
.catalog-sticky-bar > :deep(.ui-filter-bar) { flex: 1; min-width: 0; }
.catalog-sticky-title { display: flex; flex: 0 0 auto; align-items: center; width: 0; height: 56px; overflow: hidden; color: var(--text); font-size: var(--catalog-title-size, 14px); font-weight: var(--catalog-title-weight, 650); line-height: 1.3; white-space: nowrap; opacity: 0; visibility: hidden; transform: translateX(-4px); pointer-events: none; transition: width var(--duration-fast) var(--ease-smooth-out), opacity var(--duration-quick) var(--ease-in-out), transform var(--duration-fast) var(--ease-smooth-out), visibility 0s linear var(--duration-fast); }
.catalog-sticky-title > span { flex-shrink: 0; }
.is-stuck { gap: var(--catalog-title-gap, 20px); }
.is-stuck::before { visibility: visible; opacity: 1; transition-delay: 0s; }
.is-stuck > .catalog-sticky-title { width: var(--catalog-title-width); opacity: 1; visibility: visible; transform: translateX(0); transition-delay: 0s; }
/* Compact desktop controls without changing the row's height at the sticky edge. */
@media (min-width: 768px) {
  .catalog-sticky-bar :deep(.ui-filter-bar),
  .catalog-sticky-bar :deep(.ui-filter-bar :is(.ui-tabs, .ui-tabs__tab, .view-switcher-bar, .ui-select__trigger, .ui-filter-bar__search, .ui-layout-switcher, .task-toolbar__reset, .community-reset)) {
    transition: height var(--duration-fast) var(--ease-smooth-out), min-height var(--duration-fast) var(--ease-smooth-out), padding var(--duration-fast) var(--ease-smooth-out), border-radius var(--duration-fast) var(--ease-smooth-out), color var(--duration-quick) var(--ease-smooth-out), background-color var(--duration-quick) var(--ease-smooth-out);
  }
  .catalog-sticky-bar.is-stuck > :deep(.ui-filter-bar) {
    --filter-control-height: 36px;
    --community-toolbar-control-height: 36px;
    padding-block: var(--catalog-stuck-padding, 10px);
  }
  .catalog-sticky-bar.is-stuck :deep(.ui-filter-bar .ui-tabs[data-variant="segmented"]) { height: var(--catalog-stuck-tabs-height, 36px); border-radius: 10px; }
  .catalog-sticky-bar.is-stuck :deep(.ui-filter-bar .ui-tabs[data-variant="segmented"] .ui-tabs__tab) { min-height: calc(var(--catalog-stuck-tabs-height, 36px) - 8px); padding-inline: 7px; gap: 4px; font-size: 13px; border-radius: 6px; }
  .catalog-sticky-bar.is-stuck :deep(.ui-filter-bar .ui-tabs[data-variant="segmented"] .ui-tabs__indicator) { border-radius: 6px; }
  .catalog-sticky-bar.is-stuck :deep(.ui-filter-bar .ui-tabs[data-variant="underline"]) { height: 36px; }
  .catalog-sticky-bar.is-stuck :deep(.ui-filter-bar .ui-tabs[data-variant="underline"] .ui-tabs__tab) { min-height: 36px; }
  .catalog-sticky-bar.is-stuck :deep(.ui-filter-bar .view-switcher-bar) { min-height: var(--catalog-stuck-tabs-height, 36px); }
  .catalog-sticky-bar.is-stuck :deep(.ui-filter-bar :is(.ui-select__trigger, .ui-filter-bar__search, .task-toolbar__reset, .community-reset, .ui-layout-switcher)) { height: 36px; min-height: 36px; border-radius: 10px; }
  .catalog-sticky-bar.is-stuck :deep(.ui-filter-bar :is(.ui-select__trigger, .task-toolbar__reset, .community-reset)) { padding-inline: 10px; font-size: 13px; }
  .catalog-sticky-bar.is-stuck :deep(.ui-filter-bar :is(.ui-tabs__tab, .ui-select__trigger) svg) { width: 14px; height: 14px; }
  .catalog-sticky-bar.is-stuck :deep(.ui-filter-bar .ui-layout-switcher button) { height: 100%; border-radius: 6px; }
  .catalog-sticky-bar.is-stuck :deep(.ui-filter-bar .ui-layout-switcher__indicator) { border-radius: 6px; }
  .catalog-sticky-bar.is-stuck :deep(.ui-filter-bar .ui-layout-switcher svg) { width: 15px; height: 15px; }
}
/* Preserve room for the controls on small screens without adding a title row. */
@media (max-width: 767px) {
  .catalog-sticky-bar { transition: none; }
  .catalog-sticky-bar::before { transition: none; }
  .catalog-sticky-title { display: none; }
}
@media (prefers-reduced-motion: reduce) {
  .catalog-sticky-bar, .catalog-sticky-bar::before, .catalog-sticky-title,
  .catalog-sticky-bar :deep(.ui-filter-bar), .catalog-sticky-bar :deep(.ui-filter-bar *) { transition: none; }
}
</style>
