<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { api, APIError, messageFrom, type Asset, type MarketplaceLicense, type ProductDraft, type SellerProduct, type TaskType } from '../api/client'
import { useSessionStore } from '../stores/session'
import { formatCurrency } from '../lib/format'
import AuthRequiredState from '../components/domain/AuthRequiredState.vue'
import PageHeader from '../components/ui/PageHeader.vue'
import UiAlert from '../components/ui/UiAlert.vue'
import UiButton from '../components/ui/UiButton.vue'
import UiForm from '../components/ui/UiForm.vue'
import UiInput from '../components/ui/UiInput.vue'
import UiTextarea from '../components/ui/UiTextarea.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiCheckbox from '../components/ui/UiCheckbox.vue'
import UiFilterBar from '../components/ui/UiFilterBar.vue'
import UiCatalog from '../components/ui/UiCatalog.vue'
import UiContentCard from '../components/ui/UiContentCard.vue'
import UiCardContent from '../components/ui/UiCardContent.vue'
import UiCardTag from '../components/ui/UiCardTag.vue'
import UiCardActions from '../components/ui/UiCardActions.vue'
import UiEmptyState from '../components/ui/UiEmptyState.vue'
import UiAlertDialog from '../components/ui/UiAlertDialog.vue'
import UiCollapsible from '../components/ui/UiCollapsible.vue'

const { t, locale } = useI18n()
const session = useSessionStore()
const route = useRoute()
const router = useRouter()
const review = computed(() => route.name === 'product-reviews')
const base = computed(() => review.value ? '/admin/products' : '/workspace/products')
const id = computed(() => String(route.params.id || ''))
const isNew = computed(() => id.value === 'new' && !review.value)
const canReview = computed(() => session.user?.permissions.includes('admin:content'))
const detail = ref<SellerProduct>()
const items = ref<SellerProduct[]>([])
const total = ref(0)
const cursor = ref<string>()
const status = ref('')
const loading = ref(true)
const formReady = ref(false)
const busy = ref(false)
const error = ref('')
const success = ref('')
const licenses = ref<MarketplaceLicense[]>([])
const categories = ref<TaskType[]>([])
const sources = ref<Asset[]>([])
const samples = ref<Asset[]>([])
const sourceCursor = ref<string>()
const sampleCursor = ref<string>()
const assetLoading = ref(false)
const rights = ref(false)
const reason = ref('')
const decision = ref('')
const conflict = ref(false)
const termsOpen = ref(false)
const fresh = (): ProductDraft => ({ title: '', description: '', productType: 'asset', category: 'market_asset', assetId: '', previewAssetId: null, priceCents: 1900, currency: 'USD', licenseCode: '', aiDisclosure: '', includedFiles: [''], files: [], compatibility: '' })
const draft = ref<ProductDraft>(fresh())
const hasFiles = computed(() => Boolean(draft.value.files?.length))
const price = ref('19.00')
const editable = computed(() => !review.value && (isNew.value || Boolean(detail.value && !['active', 'removed'].includes(detail.value.status) && !['pending', 'blocked'].includes(detail.value.reviewStatus))))
const selectedLicense = computed(() => licenses.value.find(item => item.code === draft.value.licenseCode))
const dirty = computed(() => Boolean(detail.value && (Object.entries(draft.value).some(([key, value]) => JSON.stringify(value) !== JSON.stringify(key === 'files' ? detail.value?.files || [] : detail.value?.[key as keyof SellerProduct])) || price.value !== (detail.value.priceCents / 100).toFixed(2))))
const canSubmit = computed(() => Boolean(detail.value && editable.value && !conflict.value && !dirty.value))
const canPause = computed(() => !review.value && detail.value && detail.value.reviewStatus !== 'blocked' && (detail.value.status === 'active' || detail.value.reviewStatus === 'pending'))
let version = 0
function accept(item: SellerProduct) {
  detail.value = item
  draft.value = { title: item.title, description: item.description, productType: item.productType, category: item.category, assetId: item.assetId, previewAssetId: item.previewAssetId, priceCents: item.priceCents, currency: item.currency, licenseCode: item.licenseCode, aiDisclosure: item.aiDisclosure, includedFiles: [...item.includedFiles], files: (item.files || []).map(file => ({ ...file })), compatibility: item.compatibility }
  price.value = (item.priceCents / 100).toFixed(2)
  rights.value = false
  reason.value = ''
  termsOpen.value = false
}
function syncFileLabels() {
  if (!draft.value.files?.length) return
  draft.value.assetId = draft.value.files[0]!.assetId
  draft.value.includedFiles = draft.value.files.map(file => file.name)
}
function addFile() {
  if (!editable.value || busy.value || (draft.value.files?.length || 0) >= 20) return
  if (!draft.value.files?.length) draft.value.files = [{ assetId: draft.value.assetId, name: draft.value.includedFiles[0] || '' }]
  draft.value.files.push({ assetId: '', name: '' })
  syncFileLabels()
}
function removeFile(index: number) {
  if (!editable.value || busy.value || !draft.value.files) return
  draft.value.files.splice(index, 1)
  syncFileLabels()
  if (draft.value.files.length === 1) draft.value.files = []
}
function moveFileUp(index: number) {
  if (!editable.value || busy.value || !draft.value.files || index < 1) return
  const files = draft.value.files
  ;[files[index - 1], files[index]] = [files[index]!, files[index - 1]!]
  syncFileLabels()
}
function isSourceSelected(assetId: string) {
  return hasFiles.value ? draft.value.files!.some(file => file.assetId === assetId) : draft.value.assetId === assetId
}
async function load() {
  const current = ++version
  detail.value = undefined; items.value = []; cursor.value = undefined; sources.value = []; samples.value = []; sourceCursor.value = undefined; sampleCursor.value = undefined
  draft.value = fresh(); price.value = '19.00'; licenses.value = []; categories.value = []; total.value = 0; termsOpen.value = false; formReady.value = false
  error.value = ''; success.value = ''; busy.value = false; assetLoading.value = false; conflict.value = false; decision.value = ''; rights.value = false; reason.value = ''; loading.value = true
  if (!session.initialized) return
  if (!session.user || (review.value && !canReview.value)) { loading.value = false; return }
  try {
    if (!id.value) {
      const page = await api.listSellerProducts(review.value, { status: status.value })
      if (current !== version) return
      items.value = page.items; total.value = page.total; cursor.value = page.nextCursor
    } else {
      const [licensePage, types, item] = await Promise.all([api.listSellerLicenses(), api.listTaskTypes('marketplace'), isNew.value ? Promise.resolve(undefined) : api.getSellerProduct(id.value, review.value)])
      if (current !== version) return
      licenses.value = licensePage.items; categories.value = types.items
      if (item) accept(item)
      else { draft.value = fresh(); draft.value.licenseCode = licenses.value[0]?.code || ''; price.value = '19.00' }
      formReady.value = true
      if (editable.value) await loadAssets(current, true)
    }
  } catch (cause) { if (current === version) error.value = messageFrom(cause) }
  finally { if (current === version) loading.value = false }
}
async function loadAssets(current = version, initial = false, kind: 'source' | 'sample' = 'source') {
  if (current !== version || assetLoading.value || !session.user || !editable.value) return
  assetLoading.value = true
  try {
    if (initial || kind === 'source') {
      const page = await api.listAssets({ purpose: 'product_source', limit: 50, cursor: initial ? undefined : sourceCursor.value })
      if (current !== version) return
      const seen = new Set(sources.value.map(a => a.id)); sources.value.push(...page.items.filter(a => !seen.has(a.id))); sourceCursor.value = page.nextCursor
    }
    if (initial || kind === 'sample') {
      const page = await api.listAssets({ purpose: 'product_preview', limit: 50, cursor: initial ? undefined : sampleCursor.value })
      if (current !== version) return
      const seen = new Set(samples.value.map(a => a.id)); samples.value.push(...page.items.filter(a => !seen.has(a.id))); sampleCursor.value = page.nextCursor
    }
  } catch (cause) { if (current === version) error.value = messageFrom(cause) }
  finally { if (current === version) assetLoading.value = false }
}
async function more() {
  if (!cursor.value || loading.value || busy.value || !session.user || (review.value && !canReview.value)) return
  const current = version; busy.value = true
  try {
    const page = await api.listSellerProducts(review.value, { status: status.value, cursor: cursor.value })
    if (current !== version) return
    const seen = new Set(items.value.map(item => item.id)); items.value.push(...page.items.filter(item => !seen.has(item.id))); total.value = page.total; cursor.value = page.nextCursor
  } catch (cause) { if (current === version) error.value = messageFrom(cause) }
  finally { if (current === version) busy.value = false }
}
async function mutate(action: string) {
  if (loading.value || !formReady.value || busy.value || conflict.value || !session.user || (review.value && !canReview.value) || (!isNew.value && !detail.value)) return
  const current = version; busy.value = true; error.value = ''; success.value = ''
  try {
    let value: ProductDraft | undefined
    if (action === 'create' || action === 'edit') {
      if (!/^\d+(\.\d{1,2})?$/.test(price.value)) throw new Error(t('sellerProducts.price', { currency: draft.value.currency }))
      const [whole, fraction = ''] = price.value.split('.')
      const cents = Number(whole) * 100 + Number(fraction.padEnd(2, '0'))
      if (!Number.isSafeInteger(cents) || cents < 50 || cents > 99999999) throw new Error(t('sellerProducts.price', { currency: draft.value.currency }))
      syncFileLabels()
      value = { ...draft.value, priceCents: cents }
    }
    const item = await api.mutateSellerProduct(isNew.value ? null : detail.value!.id, action, { expectedVersion: detail.value?.version, draft: value, rightsConfirmed: rights.value, confirmed: review.value, reason: reason.value }, review.value)
    if (current !== version) return
    decision.value = ''; accept(item); success.value = t(action === 'create' || action === 'edit' ? 'sellerProducts.saved' : 'sellerProducts.updated')
    if (isNew.value) await router.replace(`${base.value}/${item.id}`)
    else if (editable.value) { sources.value = []; samples.value = []; await loadAssets(current, true) }
  } catch (cause) {
    if (current === version) {
      conflict.value = cause instanceof APIError && cause.status === 409
      error.value = conflict.value ? t('sellerProducts.conflict') : messageFrom(cause)
      decision.value = ''
    }
  } finally { if (current === version) busy.value = false }
}
watch([() => route.fullPath, () => session.user?.id, () => session.initialized, () => review.value && canReview.value], load, { immediate: true })
onBeforeUnmount(() => { version++ })
</script>

<template>
  <section class="seller-products-page content-width">
    <PageHeader :title="t(review ? 'sellerProducts.reviewTitle' : 'sellerProducts.title')" :summary="t(review ? 'sellerProducts.reviewSummary' : 'sellerProducts.summary')" artwork-src="/illustrations/headers/tasks.webp">
      <template #actions>
        <UiButton v-if="!review && session.user" as="RouterLink" variant="secondary" :to="{ path: '/workspace/sales', query: detail ? { productId: detail.id } : {} }">
          {{ t('sellerSales.title') }}
        </UiButton>
        <UiButton v-if="id" as="RouterLink" variant="secondary" :to="base">
          {{ t('sellerProducts.back') }}
        </UiButton>
        <UiButton v-else-if="!review && session.user" as="RouterLink" :to="`${base}/new`">
          {{ t('sellerProducts.new') }}
        </UiButton>
        <UiButton as="RouterLink" variant="secondary" :to="review ? '/admin?tab=content' : '/market'">
          {{ t(review ? 'admin.title' : 'sellerProducts.market') }}
        </UiButton>
      </template>
    </PageHeader>
    <AuthRequiredState v-if="session.initialized && !session.user" :title="t('authRequired.workspaceTitle')" :summary="t('authRequired.workspaceSummary')" :return-to="route.fullPath" />
    <UiEmptyState v-else-if="session.initialized && review && !canReview" :title="t('sellerProducts.noPermission')" />
    <template v-else>
      <UiAlert v-if="error" variant="danger" role="alert">
        {{ error }} <UiButton variant="ghost" :disabled="busy" @click="load">
          {{ t('sellerProducts.retry') }}
        </UiButton>
      </UiAlert>
      <UiAlert v-if="success" variant="success" role="status">
        {{ success }}
      </UiAlert>
      <p v-if="loading" role="status">
        {{ t('sellerProducts.loading') }}
      </p>
      <template v-else-if="!id">
        <UiFilterBar>
          <UiSelect v-model="status" :aria-label="t('sellerProducts.all')" @change="load">
            <option value="">
              {{ t('sellerProducts.all') }}
            </option>
            <option v-for="value in ['draft','pending','active','paused','rejected','blocked','removed']" :key="value" :value="value">
              {{ t(`sellerProducts.status.${value}`) }}
            </option>
          </UiSelect>
        </UiFilterBar>
        <div class="task-results-meta">
          <strong>{{ t('sellerProducts.count', { count: total }) }}</strong>
        </div>
        <UiEmptyState v-if="!items.length && !error" :title="t('sellerProducts.empty')" :message="t('sellerProducts.emptySummary')" />
        <UiCatalog>
          <UiContentCard v-for="item in items" :key="item.id" :to="`${base}/${item.id}`">
            <UiCardContent :title="item.title" :summary="item.description">
              <template #tags>
                <UiCardTag>{{ t(`sellerProducts.status.${item.status}`) }}</UiCardTag><UiCardTag>{{ t(`sellerProducts.status.${item.reviewStatus}`) }}</UiCardTag>
              </template>
            </UiCardContent>
            <UiCardActions :value="formatCurrency(item.priceCents, item.currency, locale)" :action-label="t('sellerProducts.manage')" />
          </UiContentCard>
        </UiCatalog>
        <UiButton v-if="cursor" variant="secondary" :loading="busy" @click="more">
          {{ t('sellerProducts.more') }}
        </UiButton>
      </template>
      <template v-else-if="formReady && (detail || isNew)">
        <UiCardActions v-if="detail" :value="t(`sellerProducts.status.${detail.reviewStatus}`)" :description="detail.reviewReason || (!editable ? t('sellerProducts.readOnly') : '')">
          <UiButton v-if="detail.status === 'active'" as="RouterLink" variant="secondary" :to="`/market/assets/${detail.id}`">
            {{ t('marketplace.viewProduct') }}
          </UiButton>
          <UiButton v-if="canPause" variant="secondary" :disabled="busy || conflict" @click="decision = 'pause'">
            {{ t('sellerProducts.pause') }}
          </UiButton>
        </UiCardActions>
        <UiForm class="seller-product-form" :disabled="busy || !editable" @submit="mutate(isNew ? 'create' : 'edit')">
          <label>{{ t('sellerProducts.name') }}<UiInput v-model="draft.title" required maxlength="160" /></label>
          <label>{{ t('sellerProducts.description') }}<UiTextarea v-model="draft.description" required maxlength="10000" rows="5" /></label>
          <div class="seller-product-fields">
            <label>{{ t('sellerProducts.type') }}<UiSelect v-model="draft.productType"><option v-for="kind in ['prompt','workflow','asset','work']" :key="kind" :value="kind">{{ t(`sellerProducts.types.${kind}`) }}</option></UiSelect></label>
            <label>{{ t('sellerProducts.category') }}<UiSelect v-model="draft.category"><option v-for="category in categories" :key="category.code" :value="category.code">{{ locale.startsWith('zh') ? category.nameZh : category.nameEn }}</option></UiSelect></label>
            <label v-if="!hasFiles">{{ t('sellerProducts.source') }}<UiSelect v-model="draft.assetId" required><option value="">{{ t('sellerProducts.select') }}</option><option v-if="draft.assetId && !sources.some(a => a.id === draft.assetId)" :value="draft.assetId">{{ draft.assetId }}</option><option v-for="asset in sources" :key="asset.id" :value="asset.id">{{ asset.title }}</option></UiSelect></label>
            <label>{{ t('sellerProducts.sample') }}<UiSelect :model-value="draft.previewAssetId || ''" @update:model-value="draft.previewAssetId = String($event) || null"><option value="">{{ t('sellerProducts.none') }}</option><option v-if="draft.previewAssetId && !samples.some(a => a.id === draft.previewAssetId)" :value="draft.previewAssetId">{{ draft.previewAssetId }}</option><option v-for="asset in samples.filter(a => !isSourceSelected(a.id))" :key="asset.id" :value="asset.id">{{ asset.title }}</option></UiSelect></label>
            <label>{{ t('sellerProducts.price', { currency: draft.currency }) }}<UiInput v-model="price" inputmode="decimal" required pattern="[0-9]+(\.[0-9]{1,2})?" /></label>
            <label>{{ t('sellerProducts.currency') }}<UiSelect v-model="draft.currency"><option v-if="draft.currency !== 'USD'" :value="draft.currency">{{ draft.currency }}</option><option value="USD">{{ t('sellerProducts.usd') }}</option></UiSelect></label>
            <label>{{ t('sellerProducts.license') }}<UiSelect v-model="draft.licenseCode" required><option v-if="draft.licenseCode && !licenses.some(item => item.code === draft.licenseCode)" :value="draft.licenseCode">{{ draft.licenseCode }}</option><option v-for="license in licenses" :key="license.code" :value="license.code">{{ license.name }}</option></UiSelect></label>
          </div>
          <p v-if="editable">
            {{ t('sellerProducts.sourceHelp') }}
          </p>
          <div v-if="editable" class="seller-product-links">
            <UiButton as="RouterLink" variant="ghost" to="/workspace/assets">
              {{ t('sellerProducts.upload') }}
            </UiButton><UiButton v-if="sourceCursor" variant="ghost" :loading="assetLoading" @click="loadAssets(version, false, 'source')">
              {{ t('sellerProducts.moreSources') }}
            </UiButton><UiButton v-if="sampleCursor" variant="ghost" :loading="assetLoading" @click="loadAssets(version, false, 'sample')">
              {{ t('sellerProducts.moreSamples') }}
            </UiButton>
          </div>
          <label>{{ t('sellerProducts.disclosure') }}<UiTextarea v-model="draft.aiDisclosure" required maxlength="2000" rows="3" /></label>
          <div v-if="!hasFiles && draft.includedFiles.length !== 1">
            <p>{{ t('sellerProducts.singleFileHelp') }}</p><p>{{ draft.includedFiles.join(' · ') }}</p><UiButton v-if="editable" variant="secondary" @click="draft.includedFiles = [draft.includedFiles[0] || '']">
              {{ t('sellerProducts.singleFileConfirm') }}
            </UiButton>
          </div>
          <label v-if="!hasFiles">{{ t('sellerProducts.included') }}<UiInput v-model="draft.includedFiles[0]" required maxlength="200" /></label>
          <template v-else>
            <UiAlert>{{ t('sellerProducts.bundleDelivery') }}</UiAlert>
            <p>{{ t('sellerProducts.bundleHelp') }}</p>
            <div v-for="(file, index) in draft.files" :key="index" class="seller-product-file">
              <div class="seller-product-fields">
                <label>{{ t('sellerProducts.fileSource', { number: index + 1 }) }}<UiSelect v-model="file.assetId" required @change="syncFileLabels"><option value="">{{ t('sellerProducts.select') }}</option><option v-if="file.assetId && !sources.some(a => a.id === file.assetId)" :value="file.assetId">{{ file.assetId }}</option><option v-for="asset in sources.filter(a => a.id === file.assetId || (!isSourceSelected(a.id) && a.id !== draft.previewAssetId))" :key="asset.id" :value="asset.id">{{ asset.title }}</option></UiSelect></label>
                <label>{{ t('sellerProducts.fileName', { number: index + 1 }) }}<UiInput v-model="file.name" required maxlength="200" @input="syncFileLabels" /></label>
              </div>
              <div v-if="editable" class="seller-product-links">
                <UiButton v-if="index > 0" variant="ghost" @click="moveFileUp(index)">
                  {{ t('sellerProducts.moveFileUp', { number: index + 1 }) }}
                </UiButton>
                <UiButton variant="ghost" @click="removeFile(index)">
                  {{ t('sellerProducts.removeFile', { number: index + 1 }) }}
                </UiButton>
              </div>
            </div>
          </template>
          <UiButton v-if="editable && (draft.files?.length || 0) < 20" variant="secondary" @click="addFile">
            {{ t('sellerProducts.addFile') }}
          </UiButton>
          <label>{{ t('sellerProducts.compatibility') }}<UiTextarea v-model="draft.compatibility" maxlength="2000" rows="2" /></label>
          <UiButton v-if="editable" type="submit" :loading="busy" :disabled="conflict">
            {{ t('sellerProducts.save') }}
          </UiButton>
        </UiForm>
        <UiCollapsible v-if="selectedLicense" v-model:open="termsOpen" class="seller-product-license" :title="`${t('sellerProducts.terms')} · ${selectedLicense.name}`">
          <p>{{ selectedLicense.terms }}</p>
        </UiCollapsible>
        <p v-if="dirty" role="status">
          {{ t('sellerProducts.saveFirst') }}
        </p>
        <UiForm v-if="canSubmit" class="seller-product-command" :disabled="busy" @submit="mutate('submit')">
          <label class="seller-product-consent"><UiCheckbox v-model="rights" required />{{ t('sellerProducts.rights') }}</label>
          <UiButton type="submit" :disabled="!rights" :loading="busy">
            {{ t('sellerProducts.submit') }}
          </UiButton>
        </UiForm>
        <div v-if="review && detail" class="seller-product-links">
          <UiButton v-if="!detail.files?.length" as="a" variant="secondary" :href="`/api/v1/admin/products/${detail.id}/content?kind=source&version=${detail.version}`" target="_blank" rel="noopener">
            {{ t('sellerProducts.downloadOriginal') }}
          </UiButton>
          <UiButton v-for="(file, index) in detail.files" :key="file.assetId" as="a" variant="secondary" :href="`/api/v1/admin/products/${detail.id}/content?kind=source&fileIndex=${index}&version=${detail.version}`" target="_blank" rel="noopener">
            {{ t('sellerProducts.downloadFile', { number: index + 1, name: file.name }) }}
          </UiButton>
          <UiButton v-if="detail.previewAssetId" as="a" variant="secondary" :href="`/api/v1/admin/products/${detail.id}/content?kind=preview&version=${detail.version}`" target="_blank" rel="noopener">
            {{ t('sellerProducts.downloadSample') }}
          </UiButton>
        </div>
        <UiForm v-if="review && detail && detail.sellerId !== session.user?.id" class="seller-product-command" :disabled="busy || conflict" @submit="decision = 'approve'">
          <label>{{ t('sellerProducts.reason') }}<UiTextarea v-model="reason" minlength="10" maxlength="2000" required rows="3" /></label>
          <div class="seller-product-links">
            <UiButton v-if="detail.reviewStatus === 'pending'" type="submit">
              {{ t('sellerProducts.approve') }}
            </UiButton>
            <UiButton v-if="detail.reviewStatus === 'pending'" variant="secondary" :disabled="[...reason.trim()].length < 10" @click="decision = 'reject'">
              {{ t('sellerProducts.reject') }}
            </UiButton>
            <UiButton v-if="detail.reviewStatus === 'blocked'" variant="secondary" :disabled="[...reason.trim()].length < 10" @click="decision = 'reopen'">
              {{ t('sellerProducts.reopen') }}
            </UiButton>
            <UiButton v-else variant="destructive" :disabled="[...reason.trim()].length < 10" @click="decision = 'block'">
              {{ t('sellerProducts.block') }}
            </UiButton>
          </div>
        </UiForm>
      </template>
    </template>
    <UiAlertDialog :open="Boolean(decision)" :title="t(`sellerProducts.${decision || 'confirm'}`)" :description="t(decision === 'pause' ? 'sellerProducts.pauseHelp' : 'sellerProducts.reviewHelp')" :confirm-label="t('sellerProducts.confirm')" :cancel-label="t('sellerProducts.cancel')" :busy="busy" :destructive="decision === 'block'" @update:open="!$event && (decision = '')" @confirm="mutate(decision)" />
  </section>
</template>

<style scoped>
.seller-product-form, .seller-product-command { max-width: 900px; margin-block: 24px; }
.seller-product-fields { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 20px; }
.seller-product-consent { display: flex; align-items: flex-start; gap: 10px; line-height: 1.6; }
.seller-product-consent :deep(input) { flex: none; margin-top: 4px; }
.seller-product-links { display: flex; align-items: center; flex-wrap: wrap; gap: 12px; }
.seller-product-license { max-width: 900px; padding-block: 16px; border-block: 1px solid var(--border); }
.seller-product-license p { white-space: pre-wrap; overflow-wrap: anywhere; color: var(--text-secondary); }
.seller-product-form p { color: var(--text-secondary); line-height: 1.7; }
@media (max-width: 640px) { .seller-product-fields { grid-template-columns: minmax(0, 1fr); } }
</style>
