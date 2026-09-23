<script setup lang="ts">
import { Inbox } from 'lucide-vue-next'
import { useId } from 'vue'

withDefaults(defineProps<{
  title: string
  message?: string
  density?: 'default' | 'compact'
}>(), { density: 'default', message: '' })

const titleId = useId()
</script>

<template>
  <section class="ui-empty-state" :data-density="density" :aria-labelledby="titleId">
    <div v-if="$slots.icon || density !== 'compact'" class="ui-empty-state__icon" aria-hidden="true">
      <slot name="icon">
        <Inbox :size="24" :stroke-width="1.75" />
      </slot>
    </div>
    <div class="ui-empty-state__copy">
      <component :is="density === 'compact' ? 'p' : 'h2'" :id="titleId" class="ui-empty-state__title">
        {{ title }}
      </component>
      <p v-if="message" class="ui-empty-state__message">
        {{ message }}
      </p>
    </div>
    <div v-if="$slots.actions || $slots.default" class="ui-empty-state__actions">
      <slot name="actions">
        <slot></slot>
      </slot>
    </div>
  </section>
</template>

<style scoped>
.ui-empty-state { grid-column: 1 / -1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 16px; min-width: 0; min-height: 248px; padding: 32px 20px; border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface); text-align: center; }
.ui-empty-state__icon { display: grid; place-items: center; flex-shrink: 0; width: 48px; height: 48px; border-radius: var(--radius-surface); background: var(--surface-muted); color: var(--text-secondary); }
.ui-empty-state__copy { min-width: 0; max-width: 480px; }
.ui-empty-state .ui-empty-state__title { margin: 0; color: var(--text); font-size: 16px; font-weight: 600; line-height: 1.5; overflow-wrap: anywhere; }
.ui-empty-state .ui-empty-state__message { margin: 6px 0 0; color: var(--text-secondary); font-size: 13px; line-height: 1.65; overflow-wrap: anywhere; }
.ui-empty-state__actions { display: flex; flex-wrap: wrap; justify-content: center; gap: 8px; max-width: 100%; }
.ui-empty-state__actions :deep(.ui-button) { max-width: 100%; white-space: normal; overflow-wrap: anywhere; }
.ui-empty-state[data-density='compact'] { align-items: flex-start; min-height: 0; padding: 16px 0; border: 0; background: transparent; text-align: start; }
.ui-empty-state[data-density='compact'] .ui-empty-state__title { color: var(--text-secondary); font-size: 13px; font-weight: 500; }
.ui-empty-state[data-density='compact'] .ui-empty-state__actions { justify-content: flex-start; }
@media (max-width: 560px) {
  .ui-empty-state { min-height: 220px; padding: 24px 16px; }
  .ui-empty-state__actions { width: 100%; }
  .ui-empty-state__actions :deep(.ui-button) { flex: 1 1 140px; }
}
</style>
