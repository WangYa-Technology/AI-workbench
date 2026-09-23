<script setup lang="ts">
import type { Component } from 'vue'
import type { RouteLocationRaw } from 'vue-router'
import { ArrowRight } from 'lucide-vue-next'
import UiContentCard from './UiContentCard.vue'

defineProps<{ title: string; summary: string; icon: Component; to?: RouteLocationRaw }>()
</script>

<template>
  <UiContentCard as="button" :to="to" class="ui-action-card">
    <span class="ui-action-card__icon" aria-hidden="true"><component :is="icon" :size="28" :stroke-width="1.75" /></span>
    <span class="ui-action-card__copy"><strong>{{ title }}</strong><small>{{ summary }}</small></span>
    <ArrowRight class="ui-action-card__arrow" :size="16" aria-hidden="true" />
  </UiContentCard>
</template>

<style scoped>
.ui-action-card {
  --catalog-card-padding: 20px;
  width: 100%;
  min-height: 96px;
  display: grid;
  grid-template-columns: 44px minmax(0, 1fr) 16px;
  gap: 14px;
  align-items: center;
  text-align: left;
  cursor: pointer;
}
.ui-action-card__icon { width: 44px; height: 44px; display: grid; place-items: center; color: var(--accent-readable); }
.ui-action-card__copy { min-width: 0; display: grid; gap: 5px; overflow-wrap: anywhere; }
.ui-action-card__copy strong { color: var(--text); font-size: 14px; font-weight: 600; line-height: 1.4; }
.ui-action-card__copy small { color: var(--text-secondary); font-size: 12px; font-weight: 400; line-height: 1.5; }
.ui-action-card__arrow { color: var(--text-tertiary); }
@media (max-width: 767px) {
  .ui-action-card { --catalog-card-padding: 16px; gap: 12px; }
}
</style>
