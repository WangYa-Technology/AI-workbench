<script setup lang="ts">
import { RouterLink, type RouteLocationRaw } from 'vue-router'
defineProps<{ title: string; summary?: string; to?: RouteLocationRaw }>()
</script>

<template>
  <div class="ui-card-content">
    <div v-if="$slots.tags" class="ui-card-content__tags">
      <slot name="tags"></slot>
    </div>
    <h2 class="ui-card-content__title">
      <RouterLink v-if="to" :to="to">
        {{ title }}
      </RouterLink><template v-else>
        {{ title }}
      </template>
    </h2>
    <p v-if="summary" class="ui-card-content__summary">
      {{ summary }}
    </p>
    <div v-if="$slots.meta" class="ui-card-content__meta">
      <slot name="meta"></slot>
    </div>
  </div>
</template>

<style scoped>
.ui-card-content { min-width: 0; display: flex; flex-direction: column; justify-content: center; gap: 7px; padding-block: 4px; }
.ui-card-content__tags { min-height: 22px; display: flex; flex-wrap: wrap; align-items: center; gap: 6px; }
.ui-card-content__title { margin: 0; color: var(--text); font-size: 15px; font-weight: 650; line-height: 1.35; overflow-wrap: anywhere; }
.ui-card-content__title, .ui-card-content__summary { display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow: hidden; }
.ui-card-content__title a:hover { color: var(--accent-readable); }
.ui-card-content__summary { margin: 0; color: var(--text-secondary); font-size: 12px; line-height: 1.5; overflow-wrap: anywhere; }
.ui-card-content__meta { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 12px; color: var(--text-tertiary); font-size: 11px; line-height: 1.5; overflow-wrap: anywhere; }
.ui-card-content__meta :deep(a) { color: var(--text-secondary); }
.ui-card-content__meta :deep(small) { font-size: inherit; }
.ui-catalog.is-grid .ui-card-content { justify-content: flex-start; }
.ui-catalog.is-grid .ui-card-content__meta { margin-top: auto; }
</style>
