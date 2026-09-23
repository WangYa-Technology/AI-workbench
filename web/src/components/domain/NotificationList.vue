<script setup lang="ts">
import { ArrowUpRight, Check } from 'lucide-vue-next'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import type { Notification } from '../../api/client'
import UiIconButton from '../ui/UiIconButton.vue'
import UiBadge from '../ui/UiBadge.vue'

defineProps<{
  items: Notification[]
  titleFor: (item: Notification) => string
  bodyFor: (item: Notification) => string
  categoryFor: (kind: string) => string
  dateFor: (value: string) => string
}>()
const emit = defineEmits<{ read: [item: Notification] }>()
const { t } = useI18n()
</script>

<template>
  <div class="notification-list notice-feed">
    <article v-for="item in items" :key="item.id" class="notice-row" :class="{ 'is-unread': !item.readAt }" :aria-labelledby="'notice-title-' + item.id">
      <RouterLink class="notice-content" :to="item.targetPath">
        <div class="notice-meta">
          <UiBadge variant="primary">
            {{ categoryFor(item.kind) }}
          </UiBadge>
          <time :datetime="item.createdAt">{{ dateFor(item.createdAt) }}</time>
          <span v-if="!item.readAt" class="notice-unread" :aria-label="t('notifications.unread')"></span>
        </div>
        <h3 :id="'notice-title-' + item.id">
          {{ titleFor(item) }}
        </h3>
        <p>{{ bodyFor(item) }}</p>
      </RouterLink>
      <div class="notice-actions">
        <UiIconButton v-if="!item.readAt" class="notice-action" variant="outline" motion="none" :label="t('notifications.markRead')" @click="emit('read', item)">
          <Check :size="17" aria-hidden="true" />
        </UiIconButton>
        <UiIconButton as="RouterLink" class="notice-action" variant="outline" motion="none" :to="item.targetPath" :label="t('notifications.open')">
          <ArrowUpRight :size="17" aria-hidden="true" />
        </UiIconButton>
      </div>
    </article>
  </div>
</template>

<style scoped>
.notice-feed { display: grid; gap: 10px; }
.notice-row { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: center; gap: 20px; padding: 16px 18px; border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface); }
.notice-content { display: grid; gap: 8px; min-width: 0; color: var(--text); text-decoration: none; border-radius: 4px; }
.notice-meta { display: flex; align-items: center; flex-wrap: wrap; gap: 10px; }
.notice-meta time { color: var(--text-tertiary); font-size: 11px; line-height: 1.5; }
.notice-unread { width: 6px; height: 6px; border-radius: 50%; background: var(--accent); }
.notice-content h3 { margin: 0; font-size: 14px; font-weight: 500; line-height: 1.5; overflow-wrap: anywhere; }
.is-unread h3 { font-weight: 650; }
.notice-content p { margin: 0; max-width: 85ch; color: var(--text-secondary); font-size: 13px; line-height: 1.6; overflow-wrap: anywhere; }
.notice-actions { display: flex; gap: 6px; }
.notice-content { transition: none; animation: none; transform: none; filter: none; }
.notice-action { --ui-size: 36px; }
.notice-content:focus-visible { outline: 2px solid var(--focus); outline-offset: 3px; }
@media (max-width: 767px) {
  .notice-row { grid-template-columns: minmax(0, 1fr); padding: 14px; gap: 12px; }
  .notice-actions { justify-self: end; }
  .notice-action { --ui-size: 40px; }
}
</style>
