<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch, type Component } from 'vue'
const props = withDefaults(defineProps<{ modelValue: string; items: Array<{ value: string; label: string; disabled?: boolean; icon?: Component }>; label?: string; variant?: 'segmented' | 'underline' }>(), { label: '', variant: 'segmented' })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const root = ref<globalThis.HTMLElement | null>(null)
const ready = ref(false)
const indicatorStyle = ref({ width: '0px', transform: 'translateX(0px)' })
const tabElements = new Map<string, globalThis.HTMLElement>()
let observer: globalThis.ResizeObserver | undefined

function setTabElement(value: string, element: unknown) {
  if (element instanceof globalThis.HTMLElement) tabElements.set(value, element)
  else tabElements.delete(value)
}

function onKeydown(event: globalThis.KeyboardEvent, value: string) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  const enabled = props.items.filter(item => !item.disabled)
  if (!enabled.length) return
  const index = enabled.findIndex(item => item.value === value)
  const direction = root.value && globalThis.getComputedStyle(root.value).direction === 'rtl' ? -1 : 1
  const step = (event.key === 'ArrowRight' ? 1 : -1) * direction
  const nextIndex = event.key === 'Home' ? 0 : event.key === 'End' ? enabled.length - 1 : (index + step + enabled.length) % enabled.length
  const next = enabled[nextIndex]!
  event.preventDefault()
  tabElements.get(next.value)?.focus()
  emit('update:modelValue', next.value)
}

function updateIndicator() {
  const element = tabElements.get(props.modelValue)
  if (!element) return
  indicatorStyle.value = { width: `${element.offsetWidth}px`, transform: `translateX(${element.offsetLeft}px)` }
}

watch(() => [props.modelValue, props.items], () => void nextTick(updateIndicator), { deep: true })
onMounted(() => {
  observer = new globalThis.ResizeObserver(updateIndicator)
  if (root.value) observer.observe(root.value)
  updateIndicator()
  globalThis.requestAnimationFrame(() => { ready.value = true })
})
onBeforeUnmount(() => observer?.disconnect())
defineExpose({
  scrollToValue: (value: string) => root.value?.querySelector<globalThis.HTMLElement>(`[data-value="${globalThis.CSS.escape(value)}"]`)?.scrollIntoView({ block: 'nearest', inline: 'center' }),
})
</script>

<template>
  <div ref="root" class="ui-tabs" :data-variant="variant" :data-ready="ready ? 'true' : 'false'" role="tablist" :aria-label="label || undefined">
    <span class="ui-tabs__indicator" :style="indicatorStyle" aria-hidden="true"></span>
    <button v-for="item in items" :key="item.value" :ref="element => setTabElement(item.value, element)" class="ui-tabs__tab" type="button" role="tab" :data-value="item.value" :aria-selected="item.value === modelValue" :disabled="item.disabled" :tabindex="item.value === modelValue || (!items.some(tab => tab.value === modelValue && !tab.disabled) && item === items.find(tab => !tab.disabled)) ? 0 : -1" @keydown="onKeydown($event, item.value)" @click="emit('update:modelValue', item.value)">
      <component :is="item.icon" v-if="item.icon" :size="16" aria-hidden="true" />{{ item.label }}
    </button>
  </div>
</template>
