<script setup lang="ts">
import type { AdminUser } from '../../api/client'
import UiButton from '../ui/UiButton.vue'
import UiIconButton from '../ui/UiIconButton.vue'
import UiInput from '../ui/UiInput.vue'
import UiSelect from '../ui/UiSelect.vue'
import UiTable from '../ui/UiTable.vue'
import { ListFilter, LoaderCircle, Pencil, X } from 'lucide-vue-next'

const { users, query, role, status, nextCursor, loadingMore, t, localizedLabel, roleKeys, shortUserId, date } = defineProps<{
  users: AdminUser[]
  query: string
  role: string
  status: string
  nextCursor: string | null
  loadingMore: boolean
  t: (key: string) => string
  localizedLabel: (keys: Record<string, string>, value: string) => string
  roleKeys: Record<string, string>
  shortUserId: (id: string) => string
  date: (value: string) => string
}>()
const emit = defineEmits<{
  'update:query': [value: string]
  'update:role': [value: string]
  'update:status': [value: string]
  apply: []
  clear: []
  more: []
  open: [user: AdminUser]
}>()
</script>

<template>
  <div class="admin-user-directory">
    <form class="admin-user-filters" @submit.prevent="emit('apply')">
      <label>{{ t('admin.userSearch') }}<UiInput :model-value="query" type="search" maxlength="120" :placeholder="t('admin.userSearchPlaceholder')" @update:model-value="emit('update:query', String($event))" /></label>
      <label>{{ t('admin.role') }}<UiSelect :model-value="role" @update:model-value="emit('update:role', String($event))"><option value="">{{ t('admin.allRoles') }}</option><option v-for="item in ['member','creator','publisher','moderator','admin']" :key="item" :value="item">{{ localizedLabel(roleKeys, item) }}</option></UiSelect></label>
      <label>{{ t('admin.status') }}<UiSelect :model-value="status" @update:model-value="emit('update:status', String($event))"><option value="">{{ t('admin.allStatuses') }}</option><option v-for="item in ['active','suspended','deleted']" :key="item" :value="item">{{ t(`admin.states.${item}`) }}</option></UiSelect></label>
      <UiButton class="command-button secondary" variant="secondary" type="submit"><template #start><ListFilter :size="16" /></template>{{ t('actions.applyFilters') }}</UiButton>
      <UiIconButton class="icon-button" :label="t('actions.clearFilters')" @click="emit('clear')"><X :size="16" /></UiIconButton>
    </form>
    <UiTable v-if="users.length" class="admin-user-table-shell" table-class="admin-user-table" :caption="t('admin.userTableCaption')">
      <thead><tr><th class="admin-user-col-identity">{{ t('admin.userIdentity') }}</th><th class="admin-user-col-id">{{ t('admin.userId') }}</th><th class="admin-user-col-status">{{ t('admin.status') }}</th><th class="admin-user-col-role">{{ t('admin.role') }}</th><th class="admin-user-col-locale">{{ t('admin.localeAndTimezone') }}</th><th class="admin-user-col-created">{{ t('admin.createdAt') }}</th><th class="admin-user-col-last-active">{{ t('admin.lastActive') }}</th><th><span class="sr-only">{{ t('admin.userActions') }}</span></th></tr></thead>
      <tbody><tr v-for="item in users" :key="item.id" :data-status="item.status">
        <td class="admin-user-col-identity"><div class="admin-user-identity"><strong>{{ item.displayName }}</strong><span>@{{ item.handle }} · {{ item.email }}</span></div></td>
        <td class="admin-user-col-id"><code :title="item.id">{{ shortUserId(item.id) }}</code></td>
        <td class="admin-user-col-status"><span class="admin-user-status" :data-status="item.status"><i aria-hidden="true"></i>{{ t(`admin.states.${item.status}`) }}</span></td>
        <td class="admin-user-col-role">{{ localizedLabel(roleKeys, item.role) }}</td><td class="admin-user-col-locale"><span>{{ item.locale }}</span><small>{{ item.timezone }}</small></td>
        <td class="admin-user-col-created"><time :datetime="item.createdAt">{{ date(item.createdAt) }}</time></td><td class="admin-user-col-last-active"><time v-if="item.lastSeenAt" :datetime="item.lastSeenAt">{{ date(item.lastSeenAt) }}</time><span v-else>{{ t('admin.neverActive') }}</span></td>
        <td><UiIconButton size="sm" variant="ghost" :label="t('admin.manage')" @click="emit('open', item)"><Pencil :size="15" /></UiIconButton></td>
      </tr></tbody>
    </UiTable>
    <p v-else class="inline-empty">{{ t('admin.noUsers') }}</p>
    <UiButton v-if="nextCursor" class="command-button secondary admin-load-more" type="button" :disabled="loadingMore" variant="secondary" @click="emit('more')"><LoaderCircle v-if="loadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}</UiButton>
  </div>
</template>
