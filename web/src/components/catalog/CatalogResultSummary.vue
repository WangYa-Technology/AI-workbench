<script setup lang="ts">
import { Info } from 'lucide-vue-next'
import { onBeforeUnmount, onMounted, ref, useId } from 'vue'
import UiButton from '../ui/UiButton.vue'

defineProps<{ note: string; noteLabel?: string }>()
const help = ref<globalThis.HTMLElement | null>(null)
const noteOpen = ref(false)
const noteId = `catalog-note-${useId()}`
function closeOnLeave() {
  if (!help.value?.contains(globalThis.document.activeElement)) noteOpen.value = false
}
function closeOnBlur(event: globalThis.FocusEvent) {
  if (!help.value?.contains(event.relatedTarget as globalThis.Node | null)) noteOpen.value = false
}
function closeOutside(event: globalThis.PointerEvent) {
  if (!help.value?.contains(event.target as globalThis.Node)) noteOpen.value = false
}
onMounted(() => globalThis.document.addEventListener('pointerdown', closeOutside))
onBeforeUnmount(() => globalThis.document.removeEventListener('pointerdown', closeOutside))
</script>

<template>
  <div class="catalog-result-summary" :class="{ 'catalog-result-summary--compact': noteLabel }">
    <div class="catalog-result-summary__count">
      <slot></slot>
    </div>
    <span v-if="noteLabel" ref="help" class="catalog-result-summary__help" @mouseenter="noteOpen = true" @mouseleave="closeOnLeave" @focusin="noteOpen = true" @focusout="closeOnBlur" @keydown.esc.stop.prevent="noteOpen = false">
      <UiButton variant="ghost" class="catalog-result-summary__info" :aria-label="noteLabel" :aria-describedby="noteId" @click="noteOpen = true">
        <Info :size="14" aria-hidden="true" />
      </UiButton>
      <span v-show="noteOpen" :id="noteId" role="tooltip" class="catalog-result-summary__tooltip">{{ note }}</span>
    </span>
    <p v-else class="catalog-result-summary__note">
      <Info :size="14" aria-hidden="true" />
      <span>{{ note }}</span>
    </p>
  </div>
</template>

<style scoped>
.catalog-result-summary { position: relative; display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px 20px; min-height: 24px; padding-inline: 2px; color: var(--text-secondary); font-size: 12px; line-height: 1.6; }
.catalog-result-summary__count { min-width: 0; color: var(--text); font-size: 13px; font-weight: 500; font-variant-numeric: tabular-nums; }
.catalog-result-summary__count :deep(p) { margin: 0; }
.catalog-result-summary__note { display: flex; align-items: flex-start; gap: 6px; min-width: 0; margin: 0; }
.catalog-result-summary__note svg { flex-shrink: 0; margin-top: 3px; color: var(--text-tertiary); }
.catalog-result-summary--compact { justify-content: flex-start; gap: 6px; }
.catalog-result-summary__help { display: inline-flex; }
.catalog-result-summary__info { width: 28px; height: 28px; min-height: 28px; padding: 0; color: var(--text-secondary); border-radius: var(--radius-control); }
.catalog-result-summary__info:hover, .catalog-result-summary__info:active { transform: none; }
.catalog-result-summary__tooltip { position: absolute; top: 100%; left: 0; z-index: 25; width: max-content; max-width: min(360px, 100%); padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--surface); color: var(--text-secondary); box-shadow: var(--shadow-panel); font-size: 12px; line-height: 1.6; }
</style>
