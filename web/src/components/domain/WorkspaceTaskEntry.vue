<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Clock3, ClipboardList } from 'lucide-vue-next'
import type { TaskSummary } from '../../api/client'
import { formatCurrency, formatDateTime } from '../../lib/format'
import CatalogEntry from '../catalog/CatalogEntry.vue'
import UiCardTag from '../ui/UiCardTag.vue'

const props = defineProps<{ item: TaskSummary; userId: string; typeLabel: string }>()
const { t, locale } = useI18n()
const roleLabel = computed(() => t(props.item.client.id === props.userId ? 'workspace.taskRoleCommissioner' : props.item.assignee?.id === props.userId ? 'workspace.taskRoleCreator' : 'workspace.taskRoleParticipant'))
const nextAction = computed(() => t(['open', 'accepted'].includes(props.item.status) ? 'tasks.reviewBrief' : 'tasks.continueTask'))
</script>

<template>
  <CatalogEntry class="workspace-task-card" :data-status="item.status" :data-type="item.deliverableType" :title="item.title" :summary="item.summary" :to="`/market/demands/${item.id}`" :open-label="nextAction" :has-media="false">
    <template #meta>
      <div class="workspace-task-meta">
        <div class="workspace-task-labels">
          <UiCardTag><ClipboardList :size="13" aria-hidden="true" />{{ typeLabel }}</UiCardTag>
          <span class="task-status" :data-status="item.status">{{ t(`tasks.status.${item.status}`) }}</span>
          <span class="workspace-task-role">{{ roleLabel }}</span>
        </div>
        <div class="workspace-task-people">
          <span :title="`@${item.client.handle}`">{{ item.client.displayName || item.client.handle }}</span>
          <span v-if="item.assignee" :title="`@${item.assignee.handle}`">{{ t('workspace.taskAssignee') }} · {{ item.assignee.displayName || item.assignee.handle }}</span>
          <span>{{ t('tasks.proposals') }} · {{ item.proposalCount }}</span>
        </div>
      </div>
    </template>
    <template #aside>
      <div class="workspace-task-budget">
        <span>{{ t('tasks.reward') }}</span><strong>{{ formatCurrency(item.budgetCents, item.currency, locale) }}</strong>
      </div>
      <span class="workspace-task-deadline"><Clock3 :size="13" aria-hidden="true" /><span>{{ t('tasks.deadline') }} · {{ formatDateTime(item.deadline, locale, item.clientTimezone) }}</span></span>
    </template>
  </CatalogEntry>
</template>

<style scoped>
.workspace-task-card { --catalog-entry-aside: 232px; --catalog-entry-aside-compact: 200px; }
.workspace-task-card::before { display: none; }
.workspace-task-meta { display: grid; gap: 8px; width: 100%; }
.workspace-task-labels, .workspace-task-people { display: flex; align-items: center; flex-wrap: wrap; gap: 6px 12px; min-width: 0; }
.workspace-task-role { font-weight: 500; color: var(--text-secondary); }
.workspace-task-people { color: var(--text-tertiary); font-size: 12px; }
.workspace-task-people > span { overflow-wrap: anywhere; }
.workspace-task-budget { display: grid; justify-items: end; gap: 3px; }
.workspace-task-budget > span { font-size: 11px; color: var(--text-tertiary); }
.workspace-task-budget strong { font-size: 20px; font-weight: 600; font-variant-numeric: tabular-nums; color: var(--text); }
.workspace-task-deadline { display: flex; align-items: flex-start; justify-content: end; gap: 4px; text-align: right; font-size: 11px; line-height: 1.6; }
.workspace-task-deadline svg { flex-shrink: 0; margin-top: 2px; }
.ui-catalog.is-grid .workspace-task-budget { justify-items: start; }
@media (max-width: 767px) {
  .workspace-task-budget { justify-items: start; }
  .workspace-task-deadline { margin-left: auto; }
}
</style>
