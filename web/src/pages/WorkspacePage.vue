<script setup lang="ts">
import {
  ArrowLeft, ArrowRight, Boxes, ClipboardList, Clock3, FileCheck2, PackageCheck, Plus,
  Ban, Bookmark, BookmarkX, Download, GitBranch, ListFilter, LoaderCircle, ReceiptText, RefreshCw, RotateCcw, ShieldCheck, ShoppingBag, Store, Upload, WalletCards, WandSparkles, X,
} from 'lucide-vue-next'
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api, messageFrom, type Asset, type BillingStatement, type Generation, type Order, type SavedWork, type TaskSummary } from '../api/client'
import { formatCurrency, formatDateTime } from '../lib/format'
import { useSessionStore } from '../stores/session'
import AssetMedia from '../components/domain/AssetMedia.vue'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const assets = ref<Asset[]>([])
const savedWorks = ref<SavedWork[]>([])
const generations = ref<Generation[]>([])
const tasks = ref<TaskSummary[]>([])
const orders = ref<Order[]>([])
const orderNextCursor = ref<string | null>(null)
const ordersLoadingMore = ref(false)
const billing = ref<BillingStatement | null>(null)
const selectedAsset = ref<Asset | null>(null)
const loading = ref(true)
const refunding = ref('')
const generationAction = ref('')
const error = ref('')
const success = ref('')
const refundReasons = ref<Record<string, string>>({})
const uploadOpen = ref(false)
const uploading = ref(false)
const uploadTitle = ref('')
const uploadFile = ref<globalThis.File | null>(null)
const versionOpen = ref(false)
const versionUploading = ref(false)
const versionTitle = ref('')
const versionNote = ref('')
const versionFile = ref<globalThis.File | null>(null)
const assetNextCursor = ref<string | null>(null)
const assetLoadingMore = ref(false)
const savedWorkNextCursor = ref<string | null>(null)
const savedWorkLoadingMore = ref(false)
const assetUsageLoadingMore = ref(false)
const generationNextCursor = ref<string | null>(null)
const generationLoadingMore = ref(false)
const generationMode = ref('')
const generationStatus = ref('')
const generationDateFrom = ref('')
const generationDateTo = ref('')
const billingNextCursor = ref<string | null>(null)
const billingLoadingMore = ref(false)
const billingDirection = ref('')
const billingEntryType = ref('')
const billingDateFrom = ref('')
const billingDateTo = ref('')
const billingEntryTypes = ['generation_charge', 'product_purchase', 'product_sale', 'product_refund', 'task_payment', 'task_earning', 'admin_adjustment', 'initial_credit'] as const

const assetID = computed(() => String(route.params.assetId || ''))
const section = computed(() => ['generations', 'purchases', 'orders', 'tasks', 'billing'].includes(String(route.params.section)) ? String(route.params.section) : 'assets')
const assetView = computed(() => section.value === 'assets' && route.query.view === 'saved' ? 'saved' : 'owned')
const generationFocus = computed(() => String(route.query.generationId || ''))
const visibleAssets = computed(() => section.value === 'purchases' ? assets.value.filter((item) => item.sourceType === 'purchase') : assets.value)
const sectionMeta = computed(() => ({
  assets: { title: t('workspace.assets'), summary: assetView.value === 'saved' ? t('workspace.savedSummary') : t('workspace.assetsSummary'), count: assetView.value === 'saved' ? savedWorks.value.length : visibleAssets.value.length, icon: Boxes, actionIcon: Upload, actionLabel: t('workspace.uploadAsset'), actionTo: '' },
  generations: { title: t('workspace.generations'), summary: t('workspace.generationsSummary'), count: generations.value.length, icon: Clock3, actionIcon: Plus, actionLabel: t('actions.newCreation'), actionTo: '/create/image' },
  purchases: { title: t('workspace.purchases'), summary: t('workspace.purchasesSummary'), count: visibleAssets.value.length, icon: ShoppingBag, actionIcon: Store, actionLabel: t('workspace.browseMarket'), actionTo: '/market' },
  orders: { title: t('workspace.orders'), summary: t('workspace.ordersSummary'), count: orders.value.length, icon: ReceiptText, actionIcon: Store, actionLabel: t('workspace.browseMarket'), actionTo: '/market' },
  tasks: { title: t('workspace.tasks'), summary: t('workspace.tasksSummary'), count: tasks.value.length, icon: ClipboardList, actionIcon: ArrowRight, actionLabel: t('workspace.browseTasks'), actionTo: '/market/demands' },
  billing: { title: t('workspace.billing'), summary: t('workspace.billingSummary'), count: billing.value?.entries.length || 0, icon: WalletCards, actionIcon: Plus, actionLabel: t('actions.newCreation'), actionTo: '/create/image' },
})[section.value]!)
const versionAccept = computed(() => ({ image: 'image/jpeg,image/png', video: 'video/mp4', audio: 'audio/wav', document: 'text/plain' }[selectedAsset.value?.kind || ''] || ''))

function date(value: string) {
  return formatDateTime(value, locale.value, session.user?.timezone || 'UTC')
}

function orderEventLabel(status: string) {
  return t(`marketplace.orderEvents.${status}`)
}

function generationListQuery(cursor = '') {
  const dateFrom = String(route.query.dateFrom || '')
  const dateTo = String(route.query.dateTo || '')
  return {
    mode: String(route.query.mode || ''),
    status: String(route.query.status || ''),
    dateFrom: dateFrom ? `${dateFrom}T00:00:00Z` : '',
    dateTo: dateTo ? `${dateTo}T23:59:59Z` : '',
    cursor,
    limit: Number(route.query.limit) || undefined,
  }
}

function syncGenerationFilters() {
  generationMode.value = String(route.query.mode || '')
  generationStatus.value = String(route.query.status || '')
  generationDateFrom.value = String(route.query.dateFrom || '')
  generationDateTo.value = String(route.query.dateTo || '')
}

function billingListQuery(cursor = '') {
  const dateFrom = String(route.query.dateFrom || '')
  const dateTo = String(route.query.dateTo || '')
  return {
    direction: String(route.query.direction || ''),
    entryType: String(route.query.entryType || ''),
    dateFrom: dateFrom ? `${dateFrom}T00:00:00Z` : '',
    dateTo: dateTo ? `${dateTo}T23:59:59Z` : '',
    cursor,
    limit: Number(route.query.limit) || undefined,
  }
}

function syncBillingFilters() {
  billingDirection.value = String(route.query.direction || '')
  billingEntryType.value = String(route.query.entryType || '')
  billingDateFrom.value = String(route.query.dateFrom || '')
  billingDateTo.value = String(route.query.dateTo || '')
}

async function loadBillingStatement(cursor = '') {
  const page = await api.billingStatement(billingListQuery(cursor))
  if (cursor && billing.value) {
    const known = new Set(billing.value.entries.map(item => item.id))
    billing.value = { ...page, entries: [...billing.value.entries, ...page.entries.filter(item => !known.has(item.id))] }
  } else {
    billing.value = page
  }
  billingNextCursor.value = page.nextCursor || null
}

async function applyBillingFilters() {
  const query: Record<string, string> = {}
  if (billingDirection.value) query.direction = billingDirection.value
  if (billingEntryType.value) query.entryType = billingEntryType.value
  if (billingDateFrom.value) query.dateFrom = billingDateFrom.value
  if (billingDateTo.value) query.dateTo = billingDateTo.value
  await router.push({ path: '/workspace/billing', query })
}

async function clearBillingFilters() {
  billingDirection.value = ''
  billingEntryType.value = ''
  billingDateFrom.value = ''
  billingDateTo.value = ''
  await router.push('/workspace/billing')
}

async function loadMoreBilling() {
  if (!billingNextCursor.value || billingLoadingMore.value) return
  billingLoadingMore.value = true
  error.value = ''
  try {
    await loadBillingStatement(billingNextCursor.value)
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    billingLoadingMore.value = false
  }
}

async function loadGenerationList() {
  syncGenerationFilters()
  const page = await api.listGenerations(generationListQuery())
  generations.value = page.items
  generationNextCursor.value = page.nextCursor || null
  if (generationFocus.value && !generations.value.some(item => item.id === generationFocus.value)) {
    const focused = await api.getGeneration(generationFocus.value)
    generations.value = [focused, ...generations.value]
  }
}

async function applyGenerationFilters() {
  const query: Record<string, string> = {}
  if (generationMode.value) query.mode = generationMode.value
  if (generationStatus.value) query.status = generationStatus.value
  if (generationDateFrom.value) query.dateFrom = generationDateFrom.value
  if (generationDateTo.value) query.dateTo = generationDateTo.value
  await router.push({ path: '/workspace/generations', query })
}

async function clearGenerationFilters() {
  generationMode.value = ''
  generationStatus.value = ''
  generationDateFrom.value = ''
  generationDateTo.value = ''
  await router.push('/workspace/generations')
}

async function load() {
  loading.value = true
  error.value = ''
  success.value = ''
  try {
    const user = await session.ensure()
    if (!user) throw new Error(session.error || t('status.authenticationFailed'))
    if (assetID.value) {
      selectedAsset.value = await api.getAsset(assetID.value)
      versionTitle.value = selectedAsset.value.title
      versionOpen.value = false
      return
    }
    if (section.value === 'assets') {
      const [assetResponse, savedResponse] = await Promise.all([api.listAssets(), api.listSavedWorks()])
      assets.value = assetResponse.items
      assetNextCursor.value = assetResponse.nextCursor || null
      savedWorks.value = savedResponse.items
      savedWorkNextCursor.value = savedResponse.nextCursor || null
    } else if (section.value === 'generations') {
      await loadGenerationList()
    } else if (section.value === 'purchases') {
      const page = await api.listAssets()
      assets.value = page.items
      assetNextCursor.value = page.nextCursor || null
    } else if (section.value === 'orders') {
	  const page = await api.listOrders({ limit: 20 })
	  orders.value = page.items
	  orderNextCursor.value = page.nextCursor || null
    } else if (section.value === 'tasks') {
      tasks.value = (await api.listTasks({ mine: true })).items
    } else if (section.value === 'billing') {
      syncBillingFilters()
      await loadBillingStatement()
    }
    selectedAsset.value = null
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    loading.value = false
  }
}

async function loadMoreOrders() {
  if (!orderNextCursor.value || ordersLoadingMore.value) return
  ordersLoadingMore.value = true
  error.value = ''
  try {
    const page = await api.listOrders({ limit: 20, cursor: orderNextCursor.value })
    const known = new Set(orders.value.map(item => item.id))
    orders.value = [...orders.value, ...page.items.filter(item => !known.has(item.id))]
    orderNextCursor.value = page.nextCursor || null
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    ordersLoadingMore.value = false
  }
}

async function loadMoreAssets() {
  if (!assetNextCursor.value || assetLoadingMore.value) return
  assetLoadingMore.value = true
  error.value = ''
  try {
    const page = await api.listAssets({ cursor: assetNextCursor.value })
    const known = new Set(assets.value.map(item => item.id))
    assets.value = [...assets.value, ...page.items.filter(item => !known.has(item.id))]
    assetNextCursor.value = page.nextCursor || null
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    assetLoadingMore.value = false
  }
}

async function loadMoreSavedWorks() {
  if (!savedWorkNextCursor.value || savedWorkLoadingMore.value) return
  savedWorkLoadingMore.value = true
  error.value = ''
  try {
    const page = await api.listSavedWorks({ cursor: savedWorkNextCursor.value })
    const known = new Set(savedWorks.value.map(item => item.postId))
    savedWorks.value = [...savedWorks.value, ...page.items.filter(item => !known.has(item.postId))]
    savedWorkNextCursor.value = page.nextCursor || null
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    savedWorkLoadingMore.value = false
  }
}

async function loadMoreAssetUsages() {
  const item = selectedAsset.value
  if (!item?.usageNextCursor || assetUsageLoadingMore.value) return
  assetUsageLoadingMore.value = true
  error.value = ''
  try {
    const page = await api.listAssetUsages(item.id, { cursor: item.usageNextCursor })
    const known = new Set((item.usages || []).map(usage => `${usage.kind}:${usage.resourceId}`))
    selectedAsset.value = {
      ...item,
      usages: [...(item.usages || []), ...page.items.filter(usage => !known.has(`${usage.kind}:${usage.resourceId}`))],
      usageNextCursor: page.nextCursor,
    }
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    assetUsageLoadingMore.value = false
  }
}

async function loadMoreGenerations() {
  if (!generationNextCursor.value || generationLoadingMore.value) return
  generationLoadingMore.value = true
  error.value = ''
  try {
    const page = await api.listGenerations(generationListQuery(generationNextCursor.value))
    const known = new Set(generations.value.map(item => item.id))
    generations.value = [...generations.value, ...page.items.filter(item => !known.has(item.id))]
    generationNextCursor.value = page.nextCursor || null
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    generationLoadingMore.value = false
  }
}

async function removeSavedWork(item: SavedWork) {
  error.value = ''
  success.value = ''
  try {
    await api.setCommunityReaction(item.postId, 'bookmark', false)
    savedWorks.value = savedWorks.value.filter(saved => saved.postId !== item.postId)
    success.value = t('workspace.savedRemoved')
  } catch (reason) {
    error.value = messageFrom(reason)
  }
}

async function changeGeneration(item: Generation, action: 'cancel' | 'retry') {
  generationAction.value = item.id
  error.value = ''
  success.value = ''
  try {
    await (action === 'cancel'
      ? api.cancelGeneration(item.id, 'Cancelled from personal generation history.')
      : api.retryGeneration(item.id))
    const [, statement] = await Promise.all([loadGenerationList(), api.billingStatement()])
    billing.value = statement
    success.value = action === 'cancel' ? t('workspace.generationCancelled') : t('workspace.generationRetried')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    generationAction.value = ''
  }
}

async function requestRefund(order: Order) {
  refunding.value = order.id
  error.value = ''
  success.value = ''
  try {
    const updated = await api.refundOrder(order.id, refundReasons.value[order.id] || '')
    orders.value = orders.value.map((item) => item.id === updated.id ? updated : item)
    const page = await api.listAssets()
    assets.value = page.items
    assetNextCursor.value = page.nextCursor || null
    success.value = t('workspace.refundComplete')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    refunding.value = ''
  }
}

function chooseUpload(event: globalThis.Event) {
  const input = event.target as globalThis.HTMLInputElement
  uploadFile.value = input.files?.[0] || null
  if (uploadFile.value && !uploadTitle.value) uploadTitle.value = uploadFile.value.name.replace(/\.[^.]+$/, '').slice(0, 120)
}

function chooseVersion(event: globalThis.Event) {
  const input = event.target as globalThis.HTMLInputElement
  versionFile.value = input.files?.[0] || null
}

async function uploadVersion() {
  if (!selectedAsset.value || !versionFile.value) return
  versionUploading.value = true
  error.value = ''
  try {
    const form = new globalThis.FormData()
    form.append('title', versionTitle.value || selectedAsset.value.title)
    form.append('note', versionNote.value)
    form.append('file', versionFile.value)
    const item = await api.uploadAssetVersion(selectedAsset.value.id, form)
    versionOpen.value = false
    versionFile.value = null
    versionNote.value = ''
    await router.push(`/workspace/assets/${item.id}`)
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    versionUploading.value = false
  }
}

async function uploadAsset() {
  if (!uploadFile.value) return
  uploading.value = true
  error.value = ''
  success.value = ''
  try {
    const form = new globalThis.FormData()
    form.append('title', uploadTitle.value)
    form.append('file', uploadFile.value)
    const item = await api.uploadAsset(form)
    assets.value = [item, ...assets.value]
    success.value = t('workspace.uploadQueued')
    uploadTitle.value = ''
    uploadFile.value = null
    uploadOpen.value = false
    void pollUpload(item.id)
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    uploading.value = false
  }
}

async function pollUpload(id: string) {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    await new Promise(resolve => globalThis.setTimeout(resolve, 750))
    try {
      const updated = await api.getAsset(id)
      assets.value = assets.value.map(item => item.id === updated.id ? updated : item)
      if (updated.scanStatus !== 'pending') {
        success.value = updated.scanStatus === 'clean' ? t('workspace.uploadReady') : t('workspace.uploadReview')
        return
      }
    } catch {
      return
    }
  }
}

watch(() => route.fullPath, () => void load())
onMounted(() => void load())
</script>

<template>
  <section class="workspace-page content-width">
    <template v-if="assetID">
      <RouterLink class="text-link asset-back" to="/workspace/assets">
        <ArrowLeft :size="17" />{{ t('workspace.assetBack') }}
      </RouterLink>
      <div v-if="loading" class="page-state" aria-live="polite">
        {{ t('status.loadingAssets') }}
      </div>
      <div v-else-if="error || !selectedAsset" class="page-state" role="alert">
        <p>{{ error }}</p><button class="command-button secondary" type="button" @click="load">
          <RefreshCw :size="17" />{{ t('actions.retry') }}
        </button>
      </div>
      <div v-else class="asset-detail-layout">
        <div v-if="selectedAsset.scanStatus === 'clean'" class="asset-detail-media">
          <AssetMedia :src="selectedAsset.mediaUrl" :kind="selectedAsset.kind" :alt="selectedAsset.title" :width="selectedAsset.width || 1600" :height="selectedAsset.height || 1200" />
        </div>
        <div v-else class="asset-detail-media asset-scan-state" :data-status="selectedAsset.scanStatus">
          <ShieldCheck :size="28" /><strong>{{ t(`workspace.scanStatus.${selectedAsset.scanStatus}`) }}</strong><p>{{ selectedAsset.scanReason || t('workspace.scanPendingDetail') }}</p>
        </div>
        <aside class="asset-inspector">
          <span class="status-label">{{ t('workspace.provenance') }}</span><h1>{{ selectedAsset.title }}</h1>
          <dl class="asset-facts">
            <div><dt>{{ t('marketplace.license') }}</dt><dd>{{ selectedAsset.licenseCode }}</dd></div><div><dt>{{ t('workspace.sourceAsset') }}</dt><dd>{{ t(`workspace.sourceTypes.${selectedAsset.sourceType}`) }}</dd></div><div><dt>{{ t('workspace.assetVersion') }}</dt><dd>v{{ selectedAsset.versionNumber }}</dd></div><div><dt>{{ t('workspace.granted') }}</dt><dd>{{ date(selectedAsset.createdAt) }}</dd></div>
          </dl>

          <section class="provenance-block asset-version-block">
            <header><GitBranch :size="19" /><h2>{{ t('workspace.versionHistory') }}</h2></header>
            <RouterLink v-for="version in selectedAsset.versions" :key="version.id" class="source-lineage" :to="`/workspace/assets/${version.id}`">
              <span>v{{ version.versionNumber }} · {{ t(`workspace.scanStatus.${version.scanStatus}`) }}</span><strong>{{ version.title }}</strong><small>{{ version.versionNote || date(version.createdAt) }}</small>
            </RouterLink>
            <button v-if="selectedAsset.isLatestVersion && selectedAsset.sourceType !== 'purchase'" class="command-button secondary wide" type="button" @click="versionOpen = !versionOpen">
              <Upload :size="17" />{{ t('workspace.uploadVersion') }}
            </button>
            <form v-if="versionOpen" class="asset-version-form" @submit.prevent="uploadVersion">
              <label>{{ t('workspace.versionTitle') }}<input v-model="versionTitle" type="text" minlength="3" maxlength="120" :placeholder="selectedAsset.title" /></label>
              <label>{{ t('workspace.versionNote') }}<textarea v-model="versionNote" rows="2" minlength="3" maxlength="500" required></textarea></label>
              <label>{{ t('workspace.versionFile') }}<input type="file" :accept="versionAccept" required @change="chooseVersion" /></label>
              <small>{{ t('workspace.versionScanBoundary') }}</small>
              <button class="command-button primary wide" type="submit" :disabled="versionUploading || !versionFile">
                <LoaderCircle v-if="versionUploading" class="spin" :size="17" /><Upload v-else :size="17" />{{ t('workspace.queueVersion') }}
              </button>
            </form>
          </section>

          <section class="provenance-block">
            <header><GitBranch :size="19" /><h2>{{ t('workspace.assetUsage') }}</h2></header>
            <p>{{ t('workspace.assetUsageSummary') }}</p>
            <template v-for="usage in selectedAsset.usages" :key="`${usage.kind}-${usage.resourceId}`">
              <RouterLink v-if="usage.targetPath" class="source-lineage usage-lineage" :to="usage.targetPath">
                <span>{{ t(`workspace.usageKinds.${usage.kind}`) }} · {{ t('workspace.assetUsageVersion', { version: usage.assetVersion }) }}</span><strong>{{ usage.title }}</strong><small>{{ t(`workspace.usageStatuses.${usage.status}`) }} · {{ date(usage.createdAt) }}</small>
              </RouterLink>
              <div v-else class="source-lineage usage-lineage unavailable">
                <span>{{ t(`workspace.usageKinds.${usage.kind}`) }} · {{ t('workspace.assetUsageVersion', { version: usage.assetVersion }) }}</span><strong>{{ usage.title }}</strong><small>{{ t(`workspace.usageStatuses.${usage.status}`) }} · {{ date(usage.createdAt) }}</small>
              </div>
            </template>
            <small v-if="!selectedAsset.usages?.length">{{ t('workspace.noAssetUsage') }}</small>
            <button v-if="selectedAsset.usageNextCursor" class="command-button secondary wide" type="button" :disabled="assetUsageLoadingMore" @click="loadMoreAssetUsages">
              <LoaderCircle v-if="assetUsageLoadingMore" class="spin" :size="16" /><Plus v-else :size="16" />{{ t('actions.loadMore') }}
            </button>
          </section>

          <section v-if="selectedAsset.provenance?.purchase" class="provenance-block">
            <header><ShoppingBag :size="19" /><h2>{{ t('workspace.purchasedFrom') }}</h2></header>
            <strong>{{ selectedAsset.provenance.purchase.productTitle }}</strong><span>{{ selectedAsset.provenance.purchase.sellerName }} · @{{ selectedAsset.provenance.purchase.sellerHandle }}</span>
            <p>{{ selectedAsset.provenance.purchase.licenseName }}</p><small>{{ t('workspace.localTestMode') }} · {{ t(`marketplace.orderStatus.${selectedAsset.provenance.purchase.orderStatus}`) }}</small>
            <RouterLink v-if="selectedAsset.provenance.purchase.orderStatus === 'fulfilled'" class="command-button primary wide" :to="`/create/image?sourceAssetId=${selectedAsset.id}`">
              <WandSparkles :size="17" />{{ t('actions.useInCreate') }}
            </RouterLink>
            <a v-if="selectedAsset.provenance.purchase.orderStatus === 'fulfilled'" class="command-button secondary wide" :href="selectedAsset.mediaUrl" :download="selectedAsset.title"><Download :size="17" />{{ t('actions.downloadLicensed') }}</a>
            <RouterLink class="command-button secondary wide" to="/workspace/orders">
              <ReceiptText :size="17" />{{ t('marketplace.viewOrder') }}
            </RouterLink>
          </section>

          <section v-if="selectedAsset.provenance?.generation" class="provenance-block">
            <header><WandSparkles :size="19" /><h2>{{ t('workspace.generatedWith') }}</h2></header>
            <strong>{{ selectedAsset.provenance.generation.modelName }}</strong><span>{{ selectedAsset.provenance.generation.provider === 'local_test' ? t('status.localProvider') : selectedAsset.provenance.generation.provider }}</span>
            <p>{{ selectedAsset.provenance.generation.prompt }}</p>
            <div v-if="selectedAsset.provenance.generation.sourceAsset" class="source-lineage">
              <span>{{ t('workspace.sourceAsset') }}</span><RouterLink :to="`/workspace/assets/${selectedAsset.provenance.generation.sourceAsset.id}`">
                {{ selectedAsset.provenance.generation.sourceAsset.title }}<ArrowRight :size="15" />
              </RouterLink><small>{{ selectedAsset.provenance.generation.sourceAsset.purchase?.licenseName || selectedAsset.provenance.generation.sourceAsset.licenseCode }}</small>
            </div>
          </section>

          <section v-if="selectedAsset.sourceType === 'upload'" class="provenance-block">
            <header><Upload :size="19" /><h2>{{ t('workspace.uploadEvidence') }}</h2></header><strong>{{ selectedAsset.uploadedFilename }}</strong><span>{{ selectedAsset.mimeType }} · {{ selectedAsset.sizeBytes ? t('workspace.fileSize', { size: selectedAsset.sizeBytes }) : '' }}</span><p>{{ selectedAsset.scanReason || t('workspace.scanPendingDetail') }}</p>
          </section>
          <RouterLink v-if="selectedAsset.sourceType !== 'purchase' && selectedAsset.scanStatus === 'clean'" class="command-button secondary wide" :to="`/publish?assetId=${selectedAsset.id}`">
            {{ t('actions.publishAsset') }}
          </RouterLink>
        </aside>
      </div>
    </template>

    <template v-else>
      <header class="workspace-header">
        <div>
          <span class="status-label"><component :is="sectionMeta.icon" :size="14" />{{ t('workspace.workbenchLabel') }} · {{ t('workspace.itemCount', { count: sectionMeta.count }) }}</span>
          <h1>{{ sectionMeta.title }}</h1><p>{{ sectionMeta.summary }}</p>
        </div>
        <button v-if="section === 'assets' && assetView === 'owned'" class="command-button primary" type="button" @click="uploadOpen = !uploadOpen">
          <Upload :size="17" />{{ sectionMeta.actionLabel }}
        </button><RouterLink v-else-if="section !== 'assets'" class="command-button primary" :to="sectionMeta.actionTo">
          <component :is="sectionMeta.actionIcon" :size="17" />{{ sectionMeta.actionLabel }}
        </RouterLink>
      </header>
      <nav class="section-tabs workspace-switcher" :aria-label="t('workspace.sectionsLabel')">
        <RouterLink to="/workspace/assets" :class="{ active: section === 'assets' }">
          <Boxes :size="17" />{{ t('workspace.assets') }}
        </RouterLink>
        <RouterLink to="/workspace/generations" :class="{ active: section === 'generations' }">
          <Clock3 :size="17" />{{ t('workspace.generations') }}
        </RouterLink>
        <RouterLink to="/workspace/purchases" :class="{ active: section === 'purchases' }">
          <ShoppingBag :size="17" />{{ t('workspace.purchases') }}
        </RouterLink>
        <RouterLink to="/workspace/orders" :class="{ active: section === 'orders' }">
          <ReceiptText :size="17" />{{ t('workspace.orders') }}
        </RouterLink>
        <RouterLink to="/workspace/tasks" :class="{ active: section === 'tasks' }">
          <ClipboardList :size="17" />{{ t('workspace.tasks') }}
        </RouterLink>
        <RouterLink to="/workspace/billing" :class="{ active: section === 'billing' }">
          <WalletCards :size="17" />{{ t('workspace.billing') }}
        </RouterLink>
      </nav>

      <nav v-if="section === 'assets'" class="asset-view-switcher" :aria-label="t('workspace.assetViewsLabel')">
        <RouterLink to="/workspace/assets" :class="{ active: assetView === 'owned' }">
          <Boxes :size="16" />{{ t('workspace.ownedAssets') }}
        </RouterLink>
        <RouterLink to="/workspace/assets?view=saved" :class="{ active: assetView === 'saved' }">
          <Bookmark :size="16" />{{ t('workspace.savedWorks') }}
        </RouterLink>
      </nav>

      <form v-if="uploadOpen && section === 'assets' && assetView === 'owned'" class="asset-upload-panel" @submit.prevent="uploadAsset">
        <header>
          <div><span class="status-label">{{ t('workspace.localScanLabel') }}</span><h2>{{ t('workspace.uploadAsset') }}</h2><p>{{ t('workspace.uploadSummary') }}</p></div><button class="icon-button" type="button" :aria-label="t('actions.close')" @click="uploadOpen = false">
            <X :size="17" />
          </button>
        </header>
        <div><label>{{ t('workspace.uploadTitle') }}<input v-model="uploadTitle" type="text" minlength="3" maxlength="120" required /></label><label>{{ t('workspace.uploadFile') }}<input type="file" accept="image/jpeg,image/png,video/mp4,audio/wav,text/plain" required @change="chooseUpload" /></label></div>
        <small>{{ t('workspace.uploadLimits') }}</small><button class="command-button primary" type="submit" :disabled="uploading || !uploadFile">
          <LoaderCircle v-if="uploading" class="spin" :size="17" /><Upload v-else :size="17" />{{ uploading ? t('workspace.uploading') : t('workspace.queueUpload') }}
        </button>
      </form>

      <div v-if="success && section === 'assets'" class="task-feedback success" role="status">
        <FileCheck2 :size="18" />{{ success }}
      </div>

      <div v-if="loading" class="page-state" aria-live="polite">
        {{ t('status.loadingWorkspace') }}
      </div>
      <div v-else-if="error && section !== 'orders'" class="page-state" role="alert">
        <p>{{ error }}</p><button class="command-button secondary" type="button" @click="load">
          <RefreshCw :size="17" />{{ t('actions.retry') }}
        </button>
      </div>

      <template v-else-if="section === 'assets' && assetView === 'saved'">
        <div class="saved-rights-notice" role="note">
          <ShieldCheck :size="19" /><div>
            <strong>{{ t('workspace.savedRightsTitle') }}</strong><p>{{ t('workspace.savedRightsSummary') }}</p>
          </div>
        </div>
        <div class="asset-grid">
          <article v-for="item in savedWorks" :key="item.postId" class="asset-card saved-work-card">
            <RouterLink class="asset-media" :to="`/works/${item.workId}`">
              <AssetMedia :src="item.mediaUrl" :kind="item.mediaKind" :alt="item.title" :width="item.width || 800" :height="item.height || 800" :controls="false" />
            </RouterLink>
            <div class="asset-copy">
              <div class="asset-meta">
                <span>{{ t('workspace.referenceOnly') }}</span><span>@{{ item.authorHandle }}</span>
              </div>
              <h2>{{ item.title }}</h2><p>{{ item.licenseCode }}</p>
              <footer>
                <small>{{ date(item.savedAt) }}</small><div class="asset-actions">
                  <RouterLink class="icon-button" :to="`/works/${item.workId}`" :aria-label="t('actions.viewDetails')" :title="t('actions.viewDetails')">
                    <ArrowRight :size="16" />
                  </RouterLink>
                  <button class="icon-button" type="button" :aria-label="t('workspace.removeSaved')" :title="t('workspace.removeSaved')" @click="removeSavedWork(item)">
                    <BookmarkX :size="16" />
                  </button>
                </div>
              </footer>
            </div>
          </article>
          <div v-if="!savedWorks.length" class="workspace-empty">
            <Bookmark :size="22" /><h2>{{ t('workspace.noSavedTitle') }}</h2><p>{{ t('workspace.noSaved') }}</p><RouterLink class="command-button secondary" to="/community">
              {{ t('workspace.browseCommunity') }}<ArrowRight :size="16" />
            </RouterLink>
          </div>
        </div>
        <button v-if="savedWorkNextCursor" class="command-button secondary generation-load-more" type="button" :disabled="savedWorkLoadingMore" @click="loadMoreSavedWorks">
          <LoaderCircle v-if="savedWorkLoadingMore" class="spin" :size="16" /><Plus v-else :size="16" />{{ t('actions.loadMore') }}
        </button>
      </template>

      <template v-else-if="section === 'assets' || section === 'purchases'">
        <div class="asset-grid">
          <article v-for="asset in visibleAssets" :key="asset.id" class="asset-card">
            <RouterLink v-if="asset.scanStatus === 'clean'" class="asset-media" :to="`/workspace/assets/${asset.id}`">
              <AssetMedia :src="asset.mediaUrl" :kind="asset.kind" :alt="asset.title" :width="asset.width || 800" :height="asset.height || 800" :controls="false" />
            </RouterLink><RouterLink v-else class="asset-media asset-scan-state" :to="`/workspace/assets/${asset.id}`">
              <ShieldCheck :size="24" /><strong>{{ t(`workspace.scanStatus.${asset.scanStatus}`) }}</strong>
            </RouterLink>
            <div class="asset-copy">
              <div class="asset-meta">
                <span>{{ t(`workspace.sourceTypes.${asset.sourceType}`) }}</span><span class="asset-scan" :data-status="asset.scanStatus"><ShieldCheck :size="13" />{{ t(`workspace.scanStatus.${asset.scanStatus}`) }}</span>
              </div>
              <h2>{{ asset.title }}</h2><p>{{ asset.licenseCode }}</p>
              <footer>
                <small>{{ date(asset.createdAt) }}</small><div class="asset-actions">
                  <RouterLink class="icon-button" :to="`/workspace/assets/${asset.id}`" :aria-label="t('actions.viewDetails')" :title="t('actions.viewDetails')">
                    <ArrowRight :size="16" />
                  </RouterLink>
                  <RouterLink v-if="asset.sourceType === 'purchase'" class="icon-button" :to="`/create/image?sourceAssetId=${asset.id}`" :aria-label="t('actions.useInCreate')" :title="t('actions.useInCreate')">
                    <WandSparkles :size="16" />
                  </RouterLink>
                </div>
              </footer>
            </div>
          </article>
          <div v-if="!visibleAssets.length" class="workspace-empty">
            <ShoppingBag v-if="section === 'purchases'" :size="22" /><Boxes v-else :size="22" />
            <h2>{{ section === 'purchases' ? t('workspace.noPurchasesTitle') : t('workspace.noAssetsTitle') }}</h2>
            <p>{{ section === 'purchases' ? t('workspace.noPurchases') : t('workspace.noAssets') }}</p><RouterLink class="command-button secondary" :to="section === 'purchases' ? '/market' : '/create/image'">
              {{ section === 'purchases' ? t('workspace.browseMarket') : t('actions.startCreating') }}<ArrowRight :size="16" />
            </RouterLink>
          </div>
        </div>
        <button v-if="(section === 'assets' || section === 'purchases') && assetNextCursor" class="command-button secondary generation-load-more" type="button" :disabled="assetLoadingMore" @click="loadMoreAssets">
          <LoaderCircle v-if="assetLoadingMore" class="spin" :size="16" /><Plus v-else :size="16" />{{ t('actions.loadMore') }}
        </button>
      </template>

      <div v-else-if="section === 'generations'" class="generation-list">
        <form class="generation-filters" @submit.prevent="applyGenerationFilters">
          <label><span>{{ t('workspace.generationModeFilter') }}</span><select v-model="generationMode">
            <option value="">{{ t('workspace.allModes') }}</option><option value="chat">{{ t('create.modes.chat') }}</option><option value="image">{{ t('create.modes.image') }}</option><option value="video">{{ t('create.modes.video') }}</option><option value="music">{{ t('create.modes.music') }}</option>
          </select></label>
          <label><span>{{ t('workspace.generationStatusFilter') }}</span><select v-model="generationStatus">
            <option value="">{{ t('workspace.allStatuses') }}</option><option value="queued">{{ t('generation.status.queued') }}</option><option value="running">{{ t('generation.status.running') }}</option><option value="succeeded">{{ t('generation.status.succeeded') }}</option><option value="failed">{{ t('generation.status.failed') }}</option><option value="cancelled">{{ t('generation.status.cancelled') }}</option>
          </select></label>
          <label><span>{{ t('workspace.dateFrom') }}</span><input v-model="generationDateFrom" type="date" /></label>
          <label><span>{{ t('workspace.dateTo') }}</span><input v-model="generationDateTo" type="date" /></label>
          <button class="command-button secondary" type="submit">
            <ListFilter :size="16" />{{ t('actions.applyFilters') }}
          </button>
          <button class="icon-button" type="button" :aria-label="t('actions.clearFilters')" :title="t('actions.clearFilters')" @click="clearGenerationFilters">
            <X :size="16" />
          </button>
        </form>
        <div v-if="success" class="task-feedback success" role="status">
          <FileCheck2 :size="18" />{{ success }}
        </div>
        <div v-if="error" class="task-feedback error" role="alert">
          <RefreshCw :size="18" />{{ error }}
        </div>
        <article v-for="item in generations" :key="item.id" class="generation-row" :class="{ 'usage-focus': generationFocus === item.id }">
          <div v-if="item.outputMediaUrl" class="generation-thumb">
            <AssetMedia :src="item.outputMediaUrl" :kind="item.mode === 'music' ? 'audio' : item.mode === 'chat' ? 'document' : item.mode" :alt="item.prompt" :text="item.outputText" :width="100" :height="100" :controls="false" />
          </div><div v-else class="generation-thumb placeholder">
            <Clock3 :size="20" />
          </div><div class="generation-row-copy">
            <strong>{{ item.prompt }}</strong><span>{{ t(`create.modes.${item.mode}`) }} · {{ item.provider === 'local_test' ? t('status.localProvider') : item.provider }}</span>
          </div><span class="generation-row-data model-cell"><small>{{ t('workspace.model') }}</small><strong>{{ item.modelName }}</strong></span><span class="generation-row-data created-cell"><small>{{ t('workspace.created') }}</small><strong>{{ date(item.createdAt) }}</strong></span><span class="generation-row-data cost-cell"><small>{{ t('workspace.cost') }}</small><strong>{{ formatCurrency(item.chargedCostCents, 'USD', locale) }}</strong></span><div class="generation-row-status" :data-status="item.status">
            <strong>{{ t(`generation.status.${item.status}`) }}</strong><span>{{ item.progress }}%</span>
          </div><div class="generation-actions">
            <button v-if="item.actions.canCancel" class="icon-button" type="button" :disabled="generationAction === item.id" :aria-label="t('actions.cancel')" :title="t('actions.cancel')" @click="changeGeneration(item, 'cancel')">
              <Ban :size="16" />
            </button>
            <button v-if="item.actions.canRetry" class="icon-button" type="button" :disabled="generationAction === item.id" :aria-label="t('actions.retry')" :title="t('actions.retry')" @click="changeGeneration(item, 'retry')">
              <RotateCcw :size="16" />
            </button>
            <a v-if="item.actions.canDownload && item.actions.downloadPath" class="icon-button" :href="item.actions.downloadPath" :download="item.prompt" :aria-label="t('actions.download')" :title="t('actions.download')"><Download :size="16" /></a>
            <RouterLink v-if="item.actions.canReuse && item.actions.reusePath" class="icon-button" :to="item.actions.reusePath" :aria-label="t('actions.useInCreate')" :title="t('actions.useInCreate')">
              <WandSparkles :size="16" />
            </RouterLink>
            <RouterLink v-if="item.actions.canView" class="icon-button" :to="item.actions.viewPath" :aria-label="t('actions.viewDetails')" :title="t('actions.viewDetails')">
              <ArrowRight :size="16" />
            </RouterLink>
          </div>
          <div v-if="generationFocus === item.id" class="generation-focus-detail">
            <span>{{ t('workspace.generationId') }} <strong>{{ item.id }}</strong></span><span v-if="item.retryOfGenerationId">{{ t('workspace.retryOf') }} <strong>{{ item.retryOfGenerationId }}</strong></span><p v-if="item.errorMessage">
              {{ item.errorMessage }}
            </p><p v-else-if="item.cancelReason">
              {{ item.cancelReason }}
            </p>
          </div>
        </article>
        <div v-if="!generations.length" class="workspace-empty">
          <Clock3 :size="22" /><h2>{{ t('workspace.noGenerationsTitle') }}</h2><p>{{ t('workspace.noGenerations') }}</p><RouterLink class="command-button secondary" to="/create/image">
            {{ t('actions.startCreating') }}<ArrowRight :size="16" />
          </RouterLink>
        </div>
        <button v-if="generationNextCursor" class="command-button secondary generation-load-more" type="button" :disabled="generationLoadingMore" @click="loadMoreGenerations">
          <LoaderCircle v-if="generationLoadingMore" class="spin" :size="16" /><Plus v-else :size="16" />{{ t('actions.loadMore') }}
        </button>
      </div>

      <div v-else-if="section === 'orders'" class="order-list">
        <div v-if="success" class="task-feedback success" role="status">
          <FileCheck2 :size="18" />{{ success }}
        </div>
        <div v-if="error" class="task-feedback error" role="alert">
          <RefreshCw :size="18" />{{ error }}
        </div>
        <article v-for="order in orders" :key="order.id" class="order-row">
          <header><div><span class="task-status" :data-status="order.status === 'fulfilled' ? 'accepted' : 'disputed'">{{ t(`marketplace.orderStatus.${order.status}`) }}</span><h2>{{ order.productTitle }}</h2><small>{{ order.licenseName }} · v{{ order.licenseVersion }} · {{ date(order.createdAt) }}</small></div><div><strong>{{ formatCurrency(order.amountCents, order.currency, locale) }}</strong><span>{{ t('workspace.localTestMode') }}</span></div></header>
          <div class="order-evidence">
            <section><h3><ShieldCheck :size="17" />{{ t('workspace.licenseEvidence') }}</h3><p>{{ order.licenseTerms }}</p><small>{{ t('marketplace.refundDays', { count: order.refundWindowDays }) }}</small></section><section>
              <h3><ReceiptText :size="17" />{{ t('workspace.eventHistory') }}</h3><ol>
                <li v-for="event in order.events" :key="`${event.toStatus}-${event.createdAt}`">
                  <span><PackageCheck :size="15" /></span><div>
                    <strong>{{ orderEventLabel(event.toStatus) }}</strong><small>{{ date(event.createdAt) }}</small><p v-if="event.toStatus === 'refund_requested'">
                      {{ event.reason }}
                    </p>
                  </div>
                </li>
              </ol>
            </section>
          </div>
          <form v-if="order.status === 'fulfilled'" class="refund-form" @submit.prevent="requestRefund(order)">
            <label>{{ t('workspace.refundReason') }}<textarea v-model="refundReasons[order.id]" rows="2" minlength="10" maxlength="500" required :placeholder="t('workspace.refundPlaceholder')"></textarea></label><button class="command-button secondary" type="submit" :disabled="refunding === order.id">
              <RotateCcw :size="17" />{{ t('workspace.requestRefund') }}
            </button>
          </form>
        </article>
        <div v-if="!orders.length" class="workspace-empty">
          <ReceiptText :size="22" /><h2>{{ t('workspace.noOrdersTitle') }}</h2><p>{{ t('workspace.noOrders') }}</p><RouterLink class="command-button secondary" to="/market">
            {{ t('workspace.browseMarket') }}<ArrowRight :size="16" />
          </RouterLink>
        </div>
        <button v-if="orderNextCursor" class="command-button secondary generation-load-more" type="button" :disabled="ordersLoadingMore" @click="loadMoreOrders">
          <LoaderCircle v-if="ordersLoadingMore" class="spin" :size="16" /><Plus v-else :size="16" />{{ t('actions.loadMore') }}
        </button>
      </div>

      <div v-else-if="section === 'billing' && billing" class="billing-workspace">
        <section class="billing-balance">
          <span class="status-label"><ShieldCheck :size="14" />{{ t('workspace.localTestMode') }}</span>
          <h2>{{ formatCurrency(billing.account.availableCents, billing.account.currency, locale) }}</h2>
          <p>{{ t('workspace.availableCredits') }}</p>
          <dl class="asset-facts">
            <div><dt>{{ t('workspace.totalBalance') }}</dt><dd>{{ formatCurrency(billing.account.balanceCents, billing.account.currency, locale) }}</dd></div>
            <div><dt>{{ t('workspace.reservedCredits') }}</dt><dd>{{ formatCurrency(billing.account.reservedCents, billing.account.currency, locale) }}</dd></div>
            <div><dt>{{ t('workspace.paymentMode') }}</dt><dd>{{ t('workspace.localTestMode') }}</dd></div>
          </dl>
        </section>
        <section class="billing-entries">
          <h2>{{ t('workspace.statement') }}</h2>
          <form class="billing-filters" @submit.prevent="applyBillingFilters">
            <label><span>{{ t('workspace.billingDirection') }}</span><select v-model="billingDirection">
              <option value="">{{ t('workspace.allDirections') }}</option><option value="debit">{{ t('workspace.billingDirections.debit') }}</option><option value="credit">{{ t('workspace.billingDirections.credit') }}</option>
            </select></label>
            <label><span>{{ t('workspace.billingEntryType') }}</span><select v-model="billingEntryType">
              <option value="">{{ t('workspace.allEntryTypes') }}</option><option v-for="entryType in billingEntryTypes" :key="entryType" :value="entryType">{{ t(`workspace.billingEntryTypes.${entryType}`) }}</option>
            </select></label>
            <label><span>{{ t('workspace.dateFrom') }}</span><input v-model="billingDateFrom" type="date" /></label>
            <label><span>{{ t('workspace.dateTo') }}</span><input v-model="billingDateTo" type="date" /></label>
            <button class="command-button secondary" type="submit">
              <ListFilter :size="16" />{{ t('actions.applyFilters') }}
            </button>
            <button class="icon-button" type="button" :aria-label="t('actions.clearFilters')" :title="t('actions.clearFilters')" @click="clearBillingFilters">
              <X :size="16" />
            </button>
          </form>
          <article v-for="entry in billing.entries" :key="entry.id" class="billing-entry">
            <span :data-direction="entry.direction">{{ entry.direction === 'credit' ? '+' : '-' }}</span>
            <div><strong>{{ entry.description }}</strong><small>{{ date(entry.createdAt) }} · {{ t(`workspace.billingEntryTypes.${entry.entryType}`) }}</small></div>
            <strong>{{ formatCurrency(entry.amountCents, entry.currency, locale) }}</strong>
            <small>{{ formatCurrency(entry.balanceAfterCents, entry.currency, locale) }}</small>
          </article>
          <div v-if="!billing.entries.length" class="workspace-empty">
            <WalletCards :size="22" /><h2>{{ t('workspace.noBillingEntries') }}</h2>
          </div>
          <button v-if="billingNextCursor" class="command-button secondary billing-load-more" type="button" :disabled="billingLoadingMore" @click="loadMoreBilling">
            <LoaderCircle v-if="billingLoadingMore" class="spin" :size="16" /><Plus v-else :size="16" />{{ t('actions.loadMore') }}
          </button>
        </section>
      </div>

      <div v-else class="workspace-task-list">
        <RouterLink v-for="item in tasks" :key="item.id" class="workspace-task-row" :to="`/market/demands/${item.id}`">
          <span class="task-status" :data-status="item.status">{{ t(`tasks.status.${item.status}`) }}</span><span><strong>{{ item.title }}</strong><small>{{ item.client.displayName }} / {{ t(`tasks.types.${item.deliverableType}`) }}</small></span><span><small>{{ t('tasks.reward') }}</small><strong>{{ formatCurrency(item.budgetCents, item.currency, locale) }}</strong></span><span><small>{{ t('tasks.deadline') }}</small><strong>{{ formatDateTime(item.deadline, locale, item.clientTimezone) }}</strong></span><ArrowRight :size="17" />
        </RouterLink>
        <div v-if="!tasks.length" class="workspace-empty">
          <ClipboardList :size="22" /><h2>{{ t('workspace.noTasksTitle') }}</h2><p>{{ t('workspace.noTasks') }}</p><RouterLink class="command-button secondary" to="/market/demands">
            {{ t('workspace.browseTasks') }}<ArrowRight :size="16" />
          </RouterLink>
        </div>
      </div>
    </template>
  </section>
</template>
