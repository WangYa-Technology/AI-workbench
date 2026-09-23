<script setup lang="ts">
import UiEmptyState from '../ui/UiEmptyState.vue'
import UiFilterBar from '../ui/UiFilterBar.vue'
import type { AdminContent } from '../../api/client'
import { ListFilter, LoaderCircle, ShieldCheck, X } from 'lucide-vue-next'
import UiButton from '../ui/UiButton.vue'
import UiIconButton from '../ui/UiIconButton.vue'
import UiInput from '../ui/UiInput.vue'
import UiSelect from '../ui/UiSelect.vue'

defineProps<{
  items: AdminContent[]; query: string; type: string; status: string; nextCursor: string | null; loadingMore: boolean
  t: (key: string) => string; localizedLabel: (keys: Record<string,string>, value: string) => string
  resourceTypeKeys: Record<string,string>; date: (value: string) => string
}>()
const emit = defineEmits<{ 'update:query':[string]; 'update:type':[string]; 'update:status':[string]; apply:[]; clear:[]; more:[]; open:[AdminContent] }>()
</script>
<template>
  <div class="admin-content-directory">
    <UiFilterBar fields density="compact" layout="grid" class="admin-user-filters" @submit.prevent="emit('apply')">
      <label>{{ t('admin.contentSearch') }}<UiInput :model-value="query" type="search" maxlength="120" :placeholder="t('admin.contentSearchPlaceholder')" @update:model-value="emit('update:query', String($event))" /></label>
      <label>{{ t('admin.resourceType') }}<UiSelect :model-value="type" @update:model-value="emit('update:type', String($event))"><option value="">{{ t('admin.allContentTypes') }}</option><option value="work">{{ localizedLabel(resourceTypeKeys, 'work') }}</option></UiSelect></label>
      <label>{{ t('admin.status') }}<UiSelect :model-value="status" @update:model-value="emit('update:status', String($event))"><option value="">{{ t('admin.allStatuses') }}</option><option v-for="item in ['draft','published','hidden','removed']" :key="item" :value="item">{{ t(`admin.states.${item}`) }}</option></UiSelect></label>
      <UiButton class="command-button secondary" type="submit" variant="secondary">
        <ListFilter :size="16" />{{ t('actions.applyFilters') }}
      </UiButton>
      <UiIconButton class="icon-button" type="button" :title="t('actions.clearFilters')" :label="t('actions.clearFilters')" @click="emit('clear')">
        <X :size="16" />
      </UiIconButton>
    </UiFilterBar>
    <div class="admin-list">
      <article v-for="item in items" :key="item.id">
        <div><strong>{{ item.title }}</strong><span>@{{ item.authorHandle }} · {{ item.aiDisclosure }}</span></div><span>{{ localizedLabel(resourceTypeKeys, item.resourceType) }}</span><span :data-status="item.status">{{ t(`admin.states.${item.status}`) }}</span><small>{{ date(item.updatedAt) }}</small><UiButton class="command-button secondary" type="button" variant="secondary" @click="emit('open', item)">
          <ShieldCheck :size="16" />{{ t('admin.review') }}
        </UiButton>
      </article>
    </div>
    <UiEmptyState v-if="!items.length" density="compact" :title="t('admin.noContent')" />
    <UiButton v-if="nextCursor" class="command-button secondary admin-load-more" type="button" :disabled="loadingMore" variant="secondary" @click="emit('more')">
      <LoaderCircle v-if="loadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}
    </UiButton>
  </div>
</template>
