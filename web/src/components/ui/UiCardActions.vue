<script setup lang="ts">
import { ArrowRight } from 'lucide-vue-next'

withDefaults(defineProps<{
  label?: string
  value: string
  description?: string
  numeric?: boolean
  actionLabel?: string
}>(), { label: '', description: '', numeric: false, actionLabel: '' })
</script>

<template>
  <div class="ui-card-actions">
    <div class="ui-card-actions__info">
      <span v-if="label" class="ui-card-actions__label">{{ label }}</span>
      <strong class="ui-card-actions__value" :class="{ 'is-numeric': numeric }">{{ value }}</strong>
      <div v-if="description || $slots.description" class="ui-card-actions__description">
        <slot name="description">
          {{ description }}
        </slot>
      </div>
    </div>
    <div v-if="actionLabel || $slots.default" class="ui-card-actions__buttons">
      <!-- Whole-card links use a visual affordance, avoiding nested interactive elements. -->
      <span v-if="actionLabel" class="ui-button" data-variant="secondary" data-size="sm">
        {{ actionLabel }}<ArrowRight :size="16" :stroke-width="1.75" aria-hidden="true" />
      </span>
      <slot></slot>
    </div>
  </div>
</template>

<style scoped>
.ui-card-actions { min-width: 0; display: flex; flex-direction: column; gap: 12px; padding: 4px 0 4px 12px; }
.ui-card-actions__info { min-width: 0; display: flex; flex-direction: column; gap: 4px; }
.ui-card-actions__label { color: var(--text-tertiary); font-size: 11px; line-height: 1.5; }
.ui-card-actions__value { color: var(--text); font-size: 12px; font-weight: 600; line-height: 1.5; overflow-wrap: anywhere; }
.ui-card-actions__value.is-numeric { font-family: var(--font-mono); font-size: 16px; font-variant-numeric: tabular-nums; }
.ui-card-actions__description { display: flex; align-items: baseline; gap: 5px; color: var(--text-secondary); font-size: 11px; line-height: 1.6; overflow-wrap: anywhere; }
.ui-card-actions__description :deep(svg) { flex: 0 0 auto; align-self: flex-start; margin-top: 2px; }
.ui-card-actions__buttons { min-width: 0; display: flex; flex-wrap: wrap; align-items: stretch; gap: 8px; margin-top: auto; }
.ui-card-actions__buttons :deep(.ui-button) { min-width: 0; max-width: 100%; flex: 1 1 0; white-space: normal; overflow-wrap: anywhere; }
.ui-card-actions__buttons :deep(.ui-icon-button) { flex: 0 0 auto; }
.ui-catalog.is-grid .ui-card-actions { padding: 12px 0 0; border-top: 1px solid var(--border); margin-top: auto; }
@media (max-width: 767px) {
  .ui-card-actions { grid-column: 1 / -1; padding: 12px 0 0; border-top: 1px solid var(--border); }
}
</style>
