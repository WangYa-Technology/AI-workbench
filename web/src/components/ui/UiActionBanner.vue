<script setup lang="ts">
import { Sparkles } from 'lucide-vue-next'

defineProps<{
  title: string
  summary: string
}>()
</script>

<template>
  <section class="ui-action-banner" :aria-label="title">
    <div class="ui-action-banner__mark" aria-hidden="true">
      <span class="ui-action-banner__symbol"><slot name="icon"><Sparkles :size="24" :stroke-width="1.75" /></slot></span>
      <Sparkles class="ui-action-banner__spark" :size="15" :stroke-width="1.75" />
    </div>
    <div class="ui-action-banner__copy">
      <h2>{{ title }}</h2>
      <p>{{ summary }}</p>
    </div>
    <div v-if="$slots.actions" class="ui-action-banner__actions">
      <slot name="actions"></slot>
    </div>
  </section>
</template>

<style scoped>
.ui-action-banner {
  container-type: inline-size;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 16px;
  min-width: 0;
  margin-block-start: 16px;
  padding: 20px;
  border: 1px solid var(--border);
  border-radius: var(--radius-surface);
  background: color-mix(in srgb, var(--accent-soft) 45%, var(--surface));
}
.ui-action-banner__mark {
  position: relative;
  display: grid;
  flex: 0 0 48px;
  place-items: center;
  width: 48px;
  height: 48px;
  color: var(--accent-readable);
}
.ui-action-banner__mark::before {
  position: absolute;
  inset: 5px 7px 1px 1px;
  content: '';
  border: 1px solid color-mix(in srgb, var(--accent-readable) 20%, var(--border));
  border-radius: var(--radius-control);
  background: var(--accent-soft);
  transform: rotate(-10deg);
}
.ui-action-banner__symbol {
  position: relative;
  display: grid;
  place-items: center;
  width: 40px;
  height: 40px;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  background: var(--surface);
}
.ui-action-banner__spark { position: absolute; inset-block-start: -3px; inset-inline-end: -2px; }
.ui-action-banner__copy { flex: 1 1 240px; min-width: 0; }
.ui-action-banner__copy h2 { margin: 0; color: var(--text); font-size: 14px; line-height: 1.5; font-weight: 650; overflow-wrap: anywhere; }
.ui-action-banner__copy p { margin: 4px 0 0; color: var(--text-secondary); font-size: 12px; line-height: 1.6; overflow-wrap: anywhere; }
.ui-action-banner__actions { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; min-width: 0; margin-inline-start: auto; }
@container (max-width: 560px) {
  .ui-action-banner__copy { flex-basis: calc(100% - 64px); }
  .ui-action-banner__actions { flex-basis: 100%; margin-inline-start: 64px; }
  .ui-action-banner__actions :deep(.ui-button) { flex: 1 1 auto; min-width: 0; white-space: normal; text-align: center; }
}
@container (max-width: 340px) {
  .ui-action-banner__actions { margin-inline-start: 0; }
}
</style>
