<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = defineProps<{ count: number; grid: boolean; resetKey: string; minCardWidth?: number }>()
const emit = defineEmits<{ visible: [ranges: Array<{ start: number; end: number }>] }>()
const root = ref<globalThis.HTMLElement | null>(null)
const width = ref(1000), viewportTop = ref(0), viewportHeight = ref(900), narrow = ref(false)
const measurements = ref<Record<number, number>>({})
const focusedRow = ref<number | null>(null)
const columns = computed(() => props.grid && !narrow.value ? Math.max(1, Math.floor((width.value + 12) / ((props.minCardWidth ?? 260) + 12))) : 1)
const rowCount = computed(() => Math.ceil(props.count / columns.value))
const enabled = computed(() => props.count > 60)
const estimate = computed(() => props.grid || narrow.value ? 410 : 174)
const offsets = computed(() => {
  const result = [0]
  for (let i = 0; i < rowCount.value; i++) result.push(result[i]! + (measurements.value[i] ?? estimate.value) + 12)
  return result
})
const visibleRows = computed(() => {
  const result: number[] = []
  for (let i = 0; i < rowCount.value; i++) {
    if (offsets.value[i + 1]! >= viewportTop.value - estimate.value * 2 && offsets.value[i]! <= viewportTop.value + viewportHeight.value + estimate.value * 2) result.push(i)
  }
  return result
})
const rows = computed(() => !enabled.value ? Array.from({ length: rowCount.value }, (_, i) => i) : [...new Set([...visibleRows.value, ...(focusedRow.value === null ? [] : [focusedRow.value])])].sort((a, b) => a - b))
watch([visibleRows, columns, () => props.resetKey], () => emit('visible', visibleRows.value.map(row => ({ start: row * columns.value, end: Math.min(props.count, (row + 1) * columns.value) }))), { immediate: true, flush: 'post' })
const elements = new Map<number, globalThis.HTMLElement>()
let scrollParent: globalThis.HTMLElement | null = null
let observer: globalThis.ResizeObserver | undefined
let frame = 0
function sync() {
  frame = 0
  if (!root.value || !scrollParent) return
  width.value = root.value.clientWidth
  narrow.value = globalThis.matchMedia('(max-width: 767px)').matches
  const documentScroll = globalThis.getComputedStyle(scrollParent).overflowY === 'visible'
  viewportTop.value = Math.max(0, (documentScroll ? 0 : scrollParent.getBoundingClientRect().top) - root.value.getBoundingClientRect().top)
  viewportHeight.value = documentScroll ? globalThis.innerHeight : scrollParent.clientHeight
}
function schedule() { if (!frame) frame = globalThis.requestAnimationFrame(sync) }
function attach(index: number, element: unknown) {
  const old = elements.get(index)
  if (old) observer?.unobserve(old)
  if (!(element instanceof globalThis.HTMLElement)) { elements.delete(index); return }
  elements.set(index, element)
  observer?.observe(element)
}
function focusIn(index: number) { focusedRow.value = index }
function focusOut() {
  void nextTick(() => {
    if (!root.value?.contains(globalThis.document.activeElement)) focusedRow.value = null
  })
}
async function keyboard(event: globalThis.KeyboardEvent, index: number) {
  if (!enabled.value || event.key !== 'Tab') return
  const links = [...(elements.get(index)?.querySelectorAll<globalThis.HTMLElement>('a[href], button:not([disabled]), input:not([disabled]), textarea:not([disabled]), [tabindex="0"]') || [])]
  const boundary = event.shiftKey ? links[0] : links.at(-1)
  const target = index + (event.shiftKey ? -1 : 1)
  if (globalThis.document.activeElement !== boundary || target < 0 || target >= rowCount.value) return
  event.preventDefault()
  focusedRow.value = target
  await nextTick()
  const next = [...(elements.get(target)?.querySelectorAll<globalThis.HTMLElement>('a[href], button:not([disabled]), input:not([disabled]), textarea:not([disabled]), [tabindex="0"]') || [])]
  const destination = event.shiftKey ? next.at(-1) : next[0]
  destination?.focus({ preventScroll: true })
  destination?.scrollIntoView({ block: 'nearest' })
}
watch([columns, () => props.resetKey], () => {
  measurements.value = {}; focusedRow.value = null
  void nextTick(sync)
})
watch(() => props.count, () => void nextTick(sync))
onMounted(() => {
  scrollParent = root.value?.closest<globalThis.HTMLElement>('#main-content') || null
  observer = new globalThis.ResizeObserver(entries => {
    const changed = { ...measurements.value }
    for (const entry of entries) {
      const index = Number((entry.target as globalThis.HTMLElement).dataset.row)
      if (Number.isInteger(index)) changed[index] = entry.target.getBoundingClientRect().height
    }
    measurements.value = changed
    schedule()
  })
  if (root.value) observer.observe(root.value)
  elements.forEach(element => observer?.observe(element))
  scrollParent?.addEventListener('scroll', schedule, { passive: true })
  globalThis.addEventListener('resize', schedule)
  globalThis.addEventListener('scroll', schedule, { passive: true })
  sync()
})
onBeforeUnmount(() => {
  observer?.disconnect(); scrollParent?.removeEventListener('scroll', schedule)
  globalThis.removeEventListener('scroll', schedule); globalThis.removeEventListener('resize', schedule); globalThis.cancelAnimationFrame(frame)
})
</script>

<template>
  <div ref="root" class="ui-catalog asset-catalog-viewport" :class="{ 'is-grid': grid }" :data-virtual="enabled" :style="enabled ? { height: `${offsets[rowCount] || 0}px`, display: 'block', position: 'relative' } : {}">
    <slot v-if="!count" :start="0" :end="0"></slot>
    <div v-for="row in rows" :key="`${resetKey}:${columns}:${row}`" :ref="element => attach(row, element)" class="asset-catalog-viewport__row" :data-row="row" :style="{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))`, ...(enabled ? { position: 'absolute', insetInline: 0, top: `${offsets[row]}px` } : {}) }" @focusin="focusIn(row)" @focusout="focusOut" @keydown="keyboard($event, row)">
      <slot :start="row * columns" :end="Math.min(count, (row + 1) * columns)"></slot>
    </div>
  </div>
</template>

<style src="../../styles/catalog.css"></style>
<style scoped>
.asset-catalog-viewport.ui-catalog { display: block; }
.asset-catalog-viewport__row { display: grid; gap: 12px; margin-bottom: 12px; }
</style>
