<script setup lang="ts">
import { ArrowLeft } from 'lucide-vue-next'
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

const props = defineProps<{ root: globalThis.HTMLElement | null; disabled?: boolean }>()
const route = useRoute()
const bar = ref<globalThis.HTMLElement | null>(null)
const title = ref('')
const href = ref('')
const visible = ref(false)
const inset = ref(0)
let source: globalThis.HTMLElement | null = null
let intersection: globalThis.IntersectionObserver | undefined
let resize: globalThis.ResizeObserver | undefined
let mutation: globalThis.MutationObserver | undefined
let mobile: globalThis.MediaQueryList | undefined

function align() {
  // Compact headers omit artwork, so align to the content edge rather than
  // carrying over the full header's illustration column and gap.
  const label = source?.matches('.ui-page-header') ? source : source?.querySelector(href.value ? 'a' : 'h1')
  if (label && bar.value) inset.value = Math.max(0, label.getBoundingClientRect().left - bar.value.getBoundingClientRect().left)
}

function sync(force = false) {
  const root = props.root
  const next = !props.disabled && root ? root.querySelector<globalThis.HTMLElement>('[data-page-context], [data-page-heading], .page-hero-header.page-hero-banner') : null
  const link = next?.matches('[data-page-context]') ? next.querySelector('a') : null
  const hadTitle = Boolean(title.value)
  title.value = (link || next?.querySelector('h1'))?.textContent?.trim() || ''
  href.value = link?.getAttribute('href') || ''
  if (!force && source === next && hadTitle === Boolean(title.value)) { align(); return }
  intersection?.disconnect()
  resize?.disconnect()
  source = next
  visible.value = false
  if (!root || !source || !title.value) return
  resize = new globalThis.ResizeObserver(align)
  resize.observe(root)
  resize.observe(source)
  void nextTick(align)
  const viewport = mobile?.matches ?? globalThis.innerWidth <= 767
  intersection = new globalThis.IntersectionObserver(([entry]) => {
    const top = entry?.rootBounds?.top ?? (viewport ? 52 : root.getBoundingClientRect().top)
    visible.value = Boolean(entry && !entry.isIntersecting && entry.boundingClientRect.bottom <= top)
  }, { root: viewport ? null : root, rootMargin: viewport ? '-52px 0px 0px' : '0px', threshold: 0 })
  intersection.observe(source)
}

function connect() {
  mutation?.disconnect()
  if (props.root) {
    mutation = new globalThis.MutationObserver(() => sync())
    mutation.observe(props.root, { childList: true, subtree: true, characterData: true, attributes: true, attributeFilter: ['href'] })
  }
  sync(true)
}
function refresh() { sync(true) }
onMounted(() => {
  mobile = globalThis.matchMedia('(max-width: 767px)')
  mobile.addEventListener('change', refresh)
  connect()
})
watch(() => [props.root, props.disabled, route.path], () => void nextTick(connect))
onBeforeUnmount(() => {
  intersection?.disconnect()
  resize?.disconnect()
  mutation?.disconnect()
  mobile?.removeEventListener('change', refresh)
})
</script>

<template>
  <div v-if="title" ref="bar" class="content-context-bar" :class="{ 'is-visible': visible, 'is-interactive': !!href }" :aria-hidden="!href || !visible" :inert="!visible">
    <div class="content-context-bar-surface" :style="{ paddingLeft: `${inset}px` }">
      <RouterLink v-if="href" class="text-link context-back" :to="href">
        <ArrowLeft :size="17" />{{ title }}
      </RouterLink>
      <span v-else>{{ title }}</span>
    </div>
  </div>
</template>

<style scoped>
.content-context-bar { margin-inline: calc(-1 * var(--space-page-x)); position: sticky; z-index: 18; top: calc(-1 * var(--space-page-y)); height: 0; pointer-events: none; }
.content-context-bar-surface { min-width: 0; height: 48px; display: flex; align-items: center; padding: 0 18px; overflow: hidden; border-bottom: 1px solid color-mix(in srgb, var(--border) 78%, transparent); background: color-mix(in srgb, var(--surface) 82%, transparent); color: var(--text); opacity: 0; visibility: hidden; transform: translateY(-8px); -webkit-backdrop-filter: saturate(150%) blur(18px); backdrop-filter: saturate(150%) blur(18px); transition: opacity var(--duration-quick) var(--ease-smooth-out), transform var(--duration-quick) var(--ease-smooth-out), visibility 0s linear var(--duration-quick); }
.content-context-bar-surface span { min-width: 0; overflow: hidden; font-size: 14px; font-weight: 650; line-height: 1.3; text-overflow: ellipsis; white-space: nowrap; }
.content-context-bar.is-visible .content-context-bar-surface { opacity: 1; visibility: visible; transform: translateY(0); transition-delay: 0s; }
@media (min-width: 768px) and (max-width: 1023px) { .content-context-bar { top: -16px; margin-inline: -18px; } }
@media (max-width: 767px) { .content-context-bar { top: 52px; margin-inline: -16px; } }
@media (max-width: 767px) { .content-context-bar-surface { height: 44px; padding-inline: 12px; } }
.content-context-bar.is-interactive.is-visible .content-context-bar-surface { pointer-events: auto; }
.context-back { gap: 8px; min-height: 40px; margin: 0; font-size: 13px; }
@media (prefers-reduced-motion: reduce) { .content-context-bar-surface { transition: none; transform: none; } }
</style>
