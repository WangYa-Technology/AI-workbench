<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Bookmark, BookmarkX, Boxes, GitBranch, ShieldCheck, Upload, WandSparkles } from 'lucide-vue-next'
import type { Asset, SavedWork } from '../../api/client'
import { creationPath, licenseLabel } from '../../lib/contentPresentation'
import CatalogEntry from '../catalog/CatalogEntry.vue'
import UiCardTag from '../ui/UiCardTag.vue'
import UiIconButton from '../ui/UiIconButton.vue'
import UiButton from '../ui/UiButton.vue'
import AssetMedia from './AssetMedia.vue'

const props = defineProps<{ asset?: Asset; saved?: SavedWork; dateLabel: string }>()
const emit = defineEmits<{ publish: []; remove: [] }>()
const { t } = useI18n()
const item = computed(() => props.asset || props.saved)
const target = computed(() => props.asset ? `/workspace/assets/${props.asset.id}` : `/works/${props.saved?.workId}`)
</script>

<template>
  <CatalogEntry v-if="item" class="asset-row asset-library-entry" :data-type="asset?.sourceType || 'saved'" :title="item.title" :to="target" :open-label="t('actions.viewDetails')">
    <template #media>
      <AssetMedia v-if="saved" :src="saved.mediaUrl" :kind="saved.mediaKind" :alt="saved.title" :width="saved.width || 800" :height="saved.height || 800" :controls="false" preview-only />
      <AssetMedia v-else-if="asset?.scanStatus === 'clean' && (!asset.purchaseAccess || asset.purchaseAccess.canDownload)" :src="asset.mediaUrl" :kind="asset.kind" :mime-type="asset.mimeType" :alt="asset.title" :width="asset.width || 800" :height="asset.height || 800" :controls="false" preview-only />
      <span v-else-if="asset" class="asset-row-scan-state"><ShieldCheck :size="24" /><span>{{ asset.purchaseAccess && !asset.purchaseAccess.canDownload ? t(`workspace.purchaseExperience.status.${asset.purchaseAccess.status}`) : t(`workspace.scanStatus.${asset.scanStatus}`) }}</span></span>
    </template>
    <template #meta>
      <div class="asset-library-tags">
        <UiCardTag v-if="asset">
          <Boxes :size="13" aria-hidden="true" />{{ t(`workspace.sourceTypes.${asset.sourceType}`) }}
        </UiCardTag>
        <UiCardTag v-else>
          <Bookmark :size="13" aria-hidden="true" />{{ t('workspace.referenceOnly') }}
        </UiCardTag>
        <span>{{ asset?.mimeType === 'application/zip' ? t('workspace.purchaseExperience.package') : t(`workspace.mediaKinds.${asset?.kind || saved?.mediaKind}`) }}</span>
        <span v-if="asset" class="asset-library-version" :title="t('workspace.assetVersion')"><GitBranch :size="13" aria-hidden="true" />v{{ asset.versionNumber }}</span>
      </div>
      <div class="asset-library-provenance">
        <span v-if="saved">@{{ saved.authorHandle }}</span><span>{{ dateLabel }}</span>
      </div>
    </template>
    <template #aside>
      <div class="asset-library-status">
        <UiCardTag v-if="asset && !asset.purchaseAccess" :variant="asset.scanStatus === 'clean' ? 'success' : asset.scanStatus === 'rejected' ? 'danger' : 'warning'">
          <ShieldCheck :size="13" aria-hidden="true" />{{ t(`workspace.scanStatus.${asset.scanStatus}`) }}
        </UiCardTag>
        <UiCardTag v-if="asset?.purchaseAccess" :variant="asset.purchaseAccess.canDownload ? 'success' : 'warning'">
          {{ t(`workspace.purchaseExperience.status.${asset.purchaseAccess.status}`) }}
        </UiCardTag>
      </div>
      <span class="asset-library-license">{{ licenseLabel(item.licenseCode) }}</span>
    </template>
    <template #actions>
      <UiIconButton v-if="saved" size="sm" variant="outline" :label="t('workspace.removeSaved')" @click="emit('remove')">
        <BookmarkX :size="16" />
      </UiIconButton>
      <UiIconButton v-if="asset && !['purchase', 'delivery'].includes(asset.sourceType) && asset.scanStatus === 'clean'" size="sm" variant="outline" class="asset-publish-action" :label="t('actions.publishAsset')" @click="emit('publish')">
        <Upload :size="16" />
      </UiIconButton>
      <UiButton v-if="asset?.purchaseAccess?.orderId" as="RouterLink" size="sm" variant="ghost" :to="{ path: '/workspace/orders', query: { orderId: asset.purchaseAccess.orderId } }">
        {{ t('marketplace.viewOrder') }}
      </UiButton>
      <UiIconButton v-if="asset?.sourceType === 'purchase' && asset.purchaseAccess?.canReuse" as="RouterLink" size="sm" variant="outline" :to="{ path: creationPath(asset.kind), query: { sourceAssetId: asset.id } }" :label="t('actions.useInCreate')">
        <WandSparkles :size="16" />
      </UiIconButton>
    </template>
  </CatalogEntry>
</template>

<style scoped>
.asset-library-entry { --catalog-entry-aside: 180px; --catalog-entry-aside-compact: 152px; }
.asset-library-tags, .asset-library-provenance { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 12px; width: 100%; }
.asset-library-provenance { color: var(--text-tertiary); }
.asset-library-version { display: inline-flex; align-items: center; gap: 4px; }
.asset-library-status { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 6px; }
.asset-library-license { overflow-wrap: anywhere; text-align: end; }
.asset-library-entry :deep(.catalog-entry__copy) { justify-content: center; gap: 12px; }
.asset-library-entry :deep(.catalog-entry__meta) { margin-top: 0; }
</style>
