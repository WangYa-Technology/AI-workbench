<script setup lang="ts">
import { RouterLink, type RouteLocationRaw } from 'vue-router'
import { ChevronRight } from 'lucide-vue-next'
import UiContentCard from '../ui/UiContentCard.vue'
withDefaults(defineProps<{ title: string; summary?: string; to: RouteLocationRaw; openLabel: string; hasMedia?: boolean }>(), { summary: '', hasMedia: true })
</script>

<template>
  <UiContentCard class="catalog-entry" :class="{ 'is-text-only': !hasMedia }">
    <div v-if="hasMedia" class="catalog-entry__media ui-content-card__media">
      <slot name="media"></slot>
    </div>
    <div class="catalog-entry__copy">
      <h2 class="catalog-entry__title">
        <RouterLink :to="to">
          {{ title }}
        </RouterLink>
      </h2>
      <p v-if="summary" class="catalog-entry__summary">
        {{ summary }}
      </p>
      <div v-if="$slots.meta" class="catalog-entry__meta">
        <slot name="meta"></slot>
      </div>
    </div>
    <div class="catalog-entry__aside">
      <div v-if="$slots.aside" class="catalog-entry__details">
        <slot name="aside"></slot>
      </div>
      <div v-if="$slots.actions" class="catalog-entry__actions">
        <slot name="actions"></slot>
      </div>
      <RouterLink class="catalog-entry__open" :to="to">
        {{ openLabel }}<ChevronRight :size="15" aria-hidden="true" />
      </RouterLink>
    </div>
  </UiContentCard>
</template>

<style scoped>
.ui-catalog .catalog-entry { position: relative; isolation: isolate; display: grid; grid-template-columns: 176px minmax(0, 1fr) var(--catalog-entry-aside, 168px); grid-template-rows: auto; grid-template-areas: 'media copy aside'; align-items: center; gap: 20px; min-height: 144px; padding: 16px; }
.catalog-entry .catalog-entry__media { grid-area: media; width: 100%; height: auto; min-height: 0; aspect-ratio: 16 / 10; }
.catalog-entry__media :deep(.asset-renderer), .catalog-entry__media :deep(img), .catalog-entry__media :deep(video) { width: 100%; height: 100%; object-fit: cover; }
.catalog-entry__media :deep(.asset-audio), .catalog-entry__media :deep(.asset-document) { min-height: 0; height: 100%; overflow: hidden; }
.catalog-entry__copy { grid-area: copy; align-self: stretch; display: flex; flex-direction: column; gap: 8px; min-width: 0; }
.catalog-entry__title { margin: 0; color: var(--text); font-size: 16px; font-weight: 600; line-height: 1.5; }
.catalog-entry__summary { margin: 0; color: var(--text-secondary); font-size: 13px; line-height: 1.65; }
.catalog-entry__title, .catalog-entry__summary { display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow: hidden; overflow-wrap: anywhere; }
.catalog-entry__title a, .catalog-entry__meta :deep(a), .catalog-entry__actions { position: relative; z-index: 2; }
.catalog-entry__title a:hover { color: var(--accent-readable); }
.catalog-entry__meta { margin-top: auto; display: flex; flex-wrap: wrap; align-items: center; gap: 6px 10px; min-width: 0; color: var(--text-secondary); font-size: 12px; line-height: 1.5; }
.catalog-entry__aside { grid-area: aside; display: flex; flex-direction: column; align-items: flex-end; align-self: stretch; gap: 8px; min-width: 0; padding-block: 2px; color: var(--text-secondary); font-size: 12px; }
.catalog-entry__details { flex: 1; display: flex; flex-direction: column; align-items: flex-end; justify-content: space-between; gap: 8px; max-width: 100%; }
.catalog-entry__actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 8px; }
.catalog-entry__open { display: inline-flex; align-items: center; gap: 5px; min-height: 24px; font-size: 12px; color: var(--text-secondary); }
.catalog-entry__open::after { content: ''; position: absolute; inset: 0; z-index: 1; border-radius: var(--radius-surface); }
.catalog-entry__open:focus-visible::after { outline: 2px solid var(--text-secondary); outline-offset: -3px; }
.catalog-entry__open:hover { color: var(--text); }
.ui-catalog .catalog-entry.is-text-only { grid-template-columns: minmax(0, 1fr) var(--catalog-entry-aside, 168px); grid-template-areas: 'copy aside'; min-height: 128px; }
.ui-catalog.is-grid .catalog-entry .catalog-entry__media { height: auto; min-height: 0; aspect-ratio: 16 / 10; }
.ui-catalog.is-grid .catalog-entry { grid-template-columns: minmax(0, 1fr); grid-template-rows: auto 1fr auto; grid-template-areas: 'media' 'copy' 'aside'; align-items: start; gap: 12px; }
.ui-catalog.is-grid .catalog-entry.is-text-only { align-self: start; grid-template-rows: auto auto; grid-template-areas: 'copy' 'aside'; }
.ui-catalog.is-grid .catalog-entry__copy { height: 100%; }
.ui-catalog.is-grid .catalog-entry__aside { flex-direction: row; flex-wrap: wrap; align-items: center; gap: 6px 12px; padding: 0; }
.ui-catalog.is-grid .catalog-entry__details { flex: 1 1 100%; flex-direction: row; align-items: center; }
.ui-catalog.is-grid .catalog-entry__open { margin-left: auto; }
@media (min-width: 768px) and (max-width: 1100px) {
  .ui-catalog:not(.is-grid) .catalog-entry { grid-template-columns: 132px minmax(0, 1fr) var(--catalog-entry-aside-compact, 136px); gap: 16px; }
  .ui-catalog:not(.is-grid) .catalog-entry.is-text-only { grid-template-columns: minmax(0, 1fr) var(--catalog-entry-aside-compact, 136px); }
}
@media (max-width: 767px) {
  .ui-catalog .catalog-entry { grid-template-columns: minmax(0, 1fr); grid-template-rows: auto 1fr auto; grid-template-areas: 'media' 'copy' 'aside'; padding: 14px; gap: 12px; }
  .ui-catalog .catalog-entry.is-text-only { grid-template-columns: minmax(0, 1fr); grid-template-rows: 1fr auto; grid-template-areas: 'copy' 'aside'; }
  .catalog-entry__aside { flex-direction: row; flex-wrap: wrap; align-items: center; gap: 6px 12px; padding: 0; }
  .catalog-entry__details { flex: 1 1 100%; flex-direction: row; align-items: center; }
  .catalog-entry__open { margin-left: auto; min-height: 32px; }
}
</style>
