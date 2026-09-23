<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { api, messageFrom, type ProductDeliveryStatus } from '../api/client'
import { useSessionStore } from '../stores/session'
import { textWithin } from '../lib/unicodeText'
import AuthRequiredState from '../components/domain/AuthRequiredState.vue'
import PageHeader from '../components/ui/PageHeader.vue'
import UiAlertDialog from '../components/ui/UiAlertDialog.vue'
import UiAlert from '../components/ui/UiAlert.vue'
import UiButton from '../components/ui/UiButton.vue'
import UiForm from '../components/ui/UiForm.vue'
import UiInput from '../components/ui/UiInput.vue'
import UiFileInput from '../components/ui/UiFileInput.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiTextarea from '../components/ui/UiTextarea.vue'
import UiEmptyState from '../components/ui/UiEmptyState.vue'
import UiContentCard from '../components/ui/UiContentCard.vue'
import DeliveryEvidenceInventory from '../components/admin/DeliveryEvidenceInventory.vue'

const { t } = useI18n()
const session = useSessionStore()
const route = useRoute()
const router = useRouter()
const id = computed(() => String(route.params.id || ''))
const allowed = computed(() => Boolean(session.user?.permissions.includes('admin:media')))
const order = ref('')
const detail = ref<ProductDeliveryStatus>()
const source = ref('original')
const backup = ref('')
const file = ref<globalThis.File>()
const fileInputKey = ref(0)
let uploadController: globalThis.AbortController | undefined
const reason = ref('')
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const success = ref('')
const confirmation = ref<'repair' | 'resume' | 'upload' | ''>('')
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const validId = (value: string) => uuid.test(value) && value !== '00000000-0000-0000-0000-000000000000'
const validFile = computed(() => Boolean(file.value && file.value.size > 0 && file.value.size <= 100 * 1024 * 1024 && file.value.size === detail.value?.sizeBytes))
const pendingUpload = computed(() => detail.value?.pendingId && detail.value.pendingSource === 'upload' && detail.value.needed && detail.value.state === 'ready')
const valid = computed(() => detail.value?.canRepair && textWithin(reason.value, 10, 2000) && (source.value === 'original' || (source.value === 'backup' && validId(backup.value.trim())) || (source.value === 'upload' && validFile.value)))
function selectFile(event: globalThis.Event) { file.value = (event.target as globalThis.HTMLInputElement).files?.[0] }
let version = 0
let command = { payload: '', key: '' }

async function load() {
  const current = ++version
  uploadController?.abort(); uploadController = undefined
  file.value = undefined; fileInputKey.value++
  detail.value = undefined; error.value = ''; success.value = ''; confirmation.value = ''
  loading.value = false; busy.value = false
  order.value = id.value; reason.value = ''; backup.value = ''; source.value = 'original'
  if (!session.initialized || !allowed.value || !id.value) return
  if (!validId(id.value)) { error.value = t('deliveryRepair.invalidOrder'); return }
  loading.value = true
  try {
    const result = await api.adminInspectDelivery(id.value)
    if (current === version) detail.value = result
  } catch (failure) { if (current === version) error.value = messageFrom(failure) }
  finally { if (current === version) loading.value = false }
}
async function lookup() {
  if (!allowed.value || loading.value || busy.value || !validId(order.value.trim())) return
  if (id.value === order.value.trim()) await load()
  else await router.push(`/admin/deliveries/${encodeURIComponent(order.value.trim())}`)
}
async function submit() {
  const item = detail.value
  const action = confirmation.value
  if (!allowed.value || !item || busy.value || loading.value || item.orderId !== id.value || !action) return
  if (action === 'repair' && !valid.value) return
  if (action === 'upload' && (!pendingUpload.value || !validFile.value)) return
  if (action === 'resume' && (!item.pendingId || !item.needed || item.state !== 'ready')) return
  const current = version
  const selectedFile = file.value
  const controller = new globalThis.AbortController()
  uploadController = controller
  busy.value = true; error.value = ''; success.value = ''
  try {
    const input = { expectedRevision: item.revision, confirmed: true as const, reason: reason.value.trim(), ...(source.value === 'backup' ? { sourceAssetId: backup.value.trim() } : {}) }
    // A lost HTTP response may follow a committed reservation. Keep the same
    // command key for unchanged input; refresh exposes any pending continuation.
    const payload = JSON.stringify([session.user?.id, item.orderId, source.value, input])
    if (command.payload !== payload) command = { payload, key: globalThis.crypto.randomUUID() }
    let result: ProductDeliveryStatus
    if (action === 'resume') result = await api.adminResumeDeliveryRepair(item.orderId, item.pendingId!)
    else if (action === 'upload') result = await api.adminUploadDeliveryRepair(item.orderId, item.pendingId!, selectedFile!, controller.signal)
    else if (source.value === 'upload') {
      result = await api.adminPrepareDeliveryUpload(item.orderId, input, command.key)
      // Do not send private file bytes after navigating away or changing user.
      if (current !== version) return
      if (result.pendingId) result = await api.adminUploadDeliveryRepair(item.orderId, result.pendingId, selectedFile!, controller.signal)
    } else result = await api.adminRepairDelivery(item.orderId, input, command.key)
    if (current !== version) return
    detail.value = result
    confirmation.value = ''; reason.value = ''; backup.value = ''
    file.value = undefined; fileInputKey.value++
    success.value = t(result.health === 'healthy' ? 'deliveryRepair.completed' : 'deliveryRepair.recheck')
  } catch (failure) {
    if (current !== version) return
    confirmation.value = ''
    error.value = messageFrom(failure)
    file.value = undefined; fileInputKey.value++
    // Do not re-enable a stale revision if failure followed a committed write.
    detail.value = undefined
    try {
      const result = await api.adminInspectDelivery(item.orderId)
      if (current === version) detail.value = result
    } catch { /* Keep the mutation error and require explicit refresh. */ }
  } finally { if (current === version) busy.value = false }
}
watch([id, allowed, () => session.user?.id, () => session.initialized], load, { immediate: true })
onBeforeUnmount(() => { ++version; uploadController?.abort(); file.value = undefined })
</script>

<template>
  <section class="delivery-repair-page content-width">
    <PageHeader :title="t('deliveryRepair.title')" :summary="t('deliveryRepair.summary')" artwork-src="/illustrations/headers/purchases.webp">
      <template #actions>
        <UiButton as="RouterLink" variant="secondary" to="/admin?tab=media">
          {{ t('deliveryRepair.back') }}
        </UiButton>
      </template>
    </PageHeader>
    <AuthRequiredState v-if="session.initialized && !session.user" :title="t('authRequired.workspaceTitle')" :summary="t('authRequired.workspaceSummary')" :return-to="route.fullPath" />
    <UiEmptyState v-else-if="session.initialized && !allowed" :title="t('deliveryRepair.forbidden')" />
    <template v-else-if="allowed">
      <UiForm class="delivery-repair-lookup" :disabled="loading || busy" @submit="lookup">
        <label>{{ t('deliveryRepair.order') }}<UiInput v-model.trim="order" required autocomplete="off" /></label>
        <UiButton type="submit" :disabled="!validId(order.trim())" :loading="loading">
          {{ t('deliveryRepair.inspect') }}
        </UiButton>
      </UiForm>
      <UiAlert v-if="error" variant="danger" role="alert">
        {{ error }}
      </UiAlert>
      <p v-if="success" role="status">
        {{ success }}
      </p>
      <UiContentCard v-if="detail" class="delivery-repair-detail">
        <h2>{{ detail.title }}</h2>
        <dl>
          <div><dt>{{ t('deliveryRepair.healthLabel') }}</dt><dd>{{ t(`deliveryRepair.health.${detail.health}`) }}</dd></div>
          <div><dt>{{ t('deliveryRepair.stateLabel') }}</dt><dd>{{ t(`deliveryRepair.states.${detail.state}`) }}</dd></div>
          <div><dt>{{ t('deliveryRepair.size') }}</dt><dd>{{ t('deliveryRepair.bytes', { count: detail.sizeBytes }) }}</dd></div>
          <div><dt>{{ t('deliveryRepair.revision') }}</dt><dd>{{ detail.revision }}</dd></div>
          <div class="delivery-repair-digest">
            <dt>{{ t('deliveryRepair.digest') }}</dt><dd><code>{{ detail.sha256 }}</code></dd>
          </div>
        </dl>
        <p v-if="!detail.needed">
          {{ t('deliveryRepair.notNeeded') }}
        </p>
        <div v-if="detail.pendingId && detail.needed && detail.state === 'ready'" class="delivery-repair-pending">
          <p>{{ t('deliveryRepair.pending') }}</p>
          <UiButton variant="secondary" :disabled="busy" @click="confirmation = 'resume'">
            {{ t('deliveryRepair.resume') }}
          </UiButton>
        </div>
        <div v-if="pendingUpload || (detail.canRepair && source === 'upload')" class="delivery-repair-upload">
          <label>{{ t('deliveryRepair.file') }}<UiFileInput :key="fileInputKey" :disabled="busy" @change="selectFile" /></label>
          <p>{{ t('deliveryRepair.uploadHint') }}</p>
          <UiAlert v-if="file && !validFile" variant="danger" role="alert">
            {{ t('deliveryRepair.fileMismatch', { count: detail.sizeBytes }) }}
          </UiAlert>
          <UiButton v-if="pendingUpload" :disabled="busy || !validFile" :loading="busy" @click="confirmation = 'upload'">
            {{ t('deliveryRepair.uploadPending') }}
          </UiButton>
        </div>
        <UiForm v-if="detail.canRepair" :disabled="busy" @submit="confirmation = 'repair'">
          <label>{{ t('deliveryRepair.source') }}<UiSelect v-model="source"><option value="original">{{ t('deliveryRepair.original') }}</option><option value="backup">{{ t('deliveryRepair.backup') }}</option><option value="upload">{{ t('deliveryRepair.upload') }}</option></UiSelect></label>
          <template v-if="source === 'backup'">
            <label>{{ t('deliveryRepair.backupId') }}<UiInput v-model.trim="backup" required autocomplete="off" /></label>
            <p>
              {{ t('deliveryRepair.backupHint') }} <UiButton as="RouterLink" variant="ghost" to="/workspace/assets">
                {{ t('deliveryRepair.assets') }}
              </UiButton>
            </p>
          </template>
          <label>{{ t('deliveryRepair.reason') }}<UiTextarea v-model="reason" rows="3" required /></label>
          <p>{{ t('deliveryRepair.reasonHint') }}</p>
          <UiButton type="submit" :disabled="!valid" :loading="busy">
            {{ t('deliveryRepair.repair') }}
          </UiButton>
        </UiForm>
      </UiContentCard>
      <DeliveryEvidenceInventory v-else-if="!id && !loading" />
      <UiAlertDialog :open="Boolean(confirmation)" :busy="busy" :title="t(confirmation === 'resume' ? 'deliveryRepair.resume' : 'deliveryRepair.repair')" :description="t('deliveryRepair.confirmSummary')" :confirm-label="t('deliveryRepair.confirm')" :cancel-label="t('actions.cancel')" @update:open="!$event && (confirmation = '')" @confirm="submit" />
    </template>
  </section>
</template>

<style scoped>
.delivery-repair-lookup { display: flex; align-items: end; flex-wrap: wrap; gap: 16px; padding-block: 24px; }
.delivery-repair-lookup label { flex: 1 1 280px; }
.delivery-repair-detail { display: grid; gap: 24px; padding: 24px; }
.delivery-repair-detail h2, .delivery-repair-detail p { margin: 0; }
.delivery-repair-detail dl { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 20px; margin: 0; }
.delivery-repair-detail dt { color: var(--text-secondary); font-size: 13px; }
.delivery-repair-detail dd { margin: 6px 0 0; overflow-wrap: anywhere; }
.delivery-repair-upload { display: grid; gap: 12px; }
.delivery-repair-digest { grid-column: 1 / -1; }
.delivery-repair-pending { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 16px; }
@media (max-width: 600px) { .delivery-repair-detail { padding: 20px; } }
</style>
