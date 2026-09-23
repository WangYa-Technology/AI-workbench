<script setup lang="ts">
import { RouterLink, type RouteLocationRaw } from 'vue-router'

defineProps<{ to?: RouteLocationRaw }>()
</script>

<template>
  <component :is="to ? RouterLink : 'span'" :to="to" class="ui-card-media ui-content-card__media">
    <span class="ui-card-media__content"><slot></slot></span>
  </component>
</template>

<style scoped>
.ui-card-media { position: relative; display: block; height: var(--catalog-list-media-height, 128px); }
/* Intrinsic image dimensions and document previews cannot stretch a catalog row. */
.ui-card-media__content { position: absolute; inset: 0; display: block; overflow: hidden; }
.ui-card-media__content > :deep(:first-child) { width: 100%; height: 100%; }
.ui-card-media__content :deep(img), .ui-card-media__content :deep(video) { display: block; width: 100%; height: 100%; object-fit: cover; }
.ui-catalog.is-grid .ui-card-media { height: var(--catalog-media-height); }
@media (max-width: 767px) {
  .ui-card-media { height: var(--catalog-media-height); }
}
</style>
