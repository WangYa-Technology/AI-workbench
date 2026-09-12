<script setup lang="ts">
import type { AdminMediaItem } from '../../api/client'
import { ListFilter, LoaderCircle, ShieldCheck, X } from 'lucide-vue-next'
import UiButton from '../ui/UiButton.vue'
import UiIconButton from '../ui/UiIconButton.vue'
import UiInput from '../ui/UiInput.vue'
import UiSelect from '../ui/UiSelect.vue'
defineProps<{ items: AdminMediaItem[]; query: string; kind: string; status: string; nextCursor: string | null; loadingMore: boolean; t:(key:string)=>string; localizedLabel:(keys:Record<string,string>, value:string)=>string; mediaKindKeys:Record<string,string>; date:(value:string)=>string }>()
const emit=defineEmits<{ 'update:query':[string]; 'update:kind':[string]; 'update:status':[string]; apply:[]; clear:[]; more:[]; open:[AdminMediaItem] }>()
</script>
<template>
  <div class="admin-media-directory">
    <form class="admin-user-filters" @submit.prevent="emit('apply')">
      <label>{{ t('admin.mediaSearch') }}<UiInput :model-value="query" type="search" maxlength="120" :placeholder="t('admin.mediaSearchPlaceholder')" @update:model-value="emit('update:query', String($event))" /></label>
      <label>{{ t('admin.mediaType') }}<UiSelect :model-value="kind" @update:model-value="emit('update:kind', String($event))"><option value="">{{ t('admin.allMediaTypes') }}</option><option v-for="item in ['image','video','audio','document','prompt','workflow']" :key="item" :value="item">{{ localizedLabel(mediaKindKeys, item) }}</option></UiSelect></label>
      <label>{{ t('admin.scanDecision') }}<UiSelect :model-value="status" @update:model-value="emit('update:status', String($event))"><option value="">{{ t('admin.allScanStatuses') }}</option><option v-for="item in ['pending','clean','review','rejected']" :key="item" :value="item">{{ t(`workspace.scanStatus.${item}`) }}</option></UiSelect></label>
      <UiButton class="command-button secondary" type="submit" variant="secondary"><ListFilter :size="16" />{{ t('actions.applyFilters') }}</UiButton><UiIconButton class="icon-button" type="button" :title="t('actions.clearFilters')" :label="t('actions.clearFilters')" @click="emit('clear')"><X :size="16" /></UiIconButton>
    </form>
    <div class="admin-list media-admin-list"><article v-for="item in items" :key="item.id"><div><strong>{{ item.title }}</strong><span>@{{ item.ownerHandle }} · {{ item.uploadedFilename || item.mimeType }}</span><small>{{ item.scanReason || t('workspace.scanPendingDetail') }}</small></div><span>{{ localizedLabel(mediaKindKeys, item.kind) }}</span><span :data-status="item.scanStatus">{{ t(`workspace.scanStatus.${item.scanStatus}`) }}</span><small>{{ date(item.scannedAt || item.createdAt) }}</small><UiButton class="command-button secondary" type="button" variant="secondary" @click="emit('open', item)"><ShieldCheck :size="16" />{{ t('admin.review') }}</UiButton></article></div>
    <p v-if="!items.length" class="inline-empty">{{ t('admin.noMedia') }}</p><UiButton v-if="nextCursor" class="command-button secondary admin-load-more" type="button" :disabled="loadingMore" variant="secondary" @click="emit('more')"><LoaderCircle v-if="loadingMore" class="spin" :size="16" /><ListFilter v-else :size="16" />{{ t('actions.loadMore') }}</UiButton>
  </div>
</template>
