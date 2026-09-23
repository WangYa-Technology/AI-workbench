<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, APIError, messageFrom, type AdminRefundHistory } from '../../api/client'
import { formatCurrency, formatDateTime } from '../../lib/format'
import { useSessionStore } from '../../stores/session'
import UiDrawer from '../ui/UiDrawer.vue'
import UiButton from '../ui/UiButton.vue'
import UiCard from '../ui/UiCard.vue'
import UiCardTag from '../ui/UiCardTag.vue'
import UiEmptyState from '../ui/UiEmptyState.vue'
import RefundCheckHistory from './RefundCheckHistory.vue'
const props = defineProps<{ paymentId: string | null }>()
const emit = defineEmits<{ close: []; updated: [] }>()
const { t, locale } = useI18n()
const session = useSessionStore()
const allowed = computed(() => session.initialized && Boolean(session.user?.permissions.includes('admin:finance')))
const history = ref<AdminRefundHistory | null>(null)
const loading = ref(false)
const requesting = ref(false)
const error = ref('')
const historyRevision = ref(0)
let version = 0
let controller: globalThis.AbortController | undefined
function clearEvidence(reason?: unknown) {
  ++version
  controller?.abort()
  history.value = null
  error.value = reason ? messageFrom(reason) : ''
  loading.value = false
  requesting.value = false
}
function handleFailure(reason: unknown) {
  if (reason instanceof APIError && [401, 403, 404].includes(reason.status)) clearEvidence(reason)
  else error.value = messageFrom(reason)
}
async function load(more = false) {
  const id = props.paymentId
  if (!allowed.value || !id || loading.value || requesting.value) return
  const cursor = more ? history.value?.nextCursor : undefined
  if (more && !cursor) return
  const requestVersion = ++version
  controller?.abort()
  controller = new globalThis.AbortController()
  loading.value = true
  error.value = ''
  try {
    const result = await api.adminRefundHistory(id, cursor, controller.signal)
    if (!allowed.value || version !== requestVersion || props.paymentId !== id) return
    if (more && history.value) {
      const existing = new Set(history.value.items.map(item => item.operationId))
      result.items = [...history.value.items, ...result.items.filter(item => !existing.has(item.operationId))]
    }
    history.value = result
    if (!more) { ++historyRevision.value; emit('updated') }
  } catch (reason) {
    if (version === requestVersion && props.paymentId === id) handleFailure(reason)
  } finally { if (version === requestVersion) loading.value = false }
}
async function requestCheck() {
  const id = props.paymentId
  if (!allowed.value || !id || !history.value?.canCheck || requesting.value || loading.value) return
  const requestVersion = ++version
  requesting.value = true
  error.value = ''
  try {
    const result = await api.adminRequestRefundCheck(id, history.value.paymentVersion)
    if (!allowed.value || version !== requestVersion || props.paymentId !== id) return
    history.value = result
    ++historyRevision.value
    emit('updated')
  } catch (reason) {
    if (version === requestVersion && props.paymentId === id) handleFailure(reason)
  } finally { if (version === requestVersion) requesting.value = false }
}
watch(() => [props.paymentId, session.user?.id, allowed.value], (current, previous) => {
  clearEvidence()
  // An account or authority change ends this inspection. It must not silently
  // reopen with the previous operator's selected payment after reauthorization.
  if (previous && (current[1] !== previous[1] || current[2] !== previous[2])) {
    if (props.paymentId) emit('close')
    return
  }
  void load()
}, { immediate: true, flush: 'sync' })
onBeforeUnmount(() => { ++version; controller?.abort() })
</script>

<template>
  <UiDrawer :open="allowed && Boolean(paymentId)" size="lg" :label="t('admin.refundHistory.title')" @update:open="!$event && emit('close')">
    <div v-if="allowed" class="refund-history-panel">
      <header>
        <h2>{{ t('admin.refundHistory.title') }}</h2>
        <UiButton variant="ghost" @click="emit('close')">
          {{ t('admin.refundHistory.close') }}
        </UiButton>
      </header>
      <p>{{ t('admin.refundHistory.description') }}</p>
      <div class="refund-history-controls">
        <UiButton :disabled="!history?.canCheck || loading || requesting" @click="requestCheck">
          {{ t('admin.refundHistory.check') }}
        </UiButton>
        <UiButton variant="secondary" :disabled="loading || requesting" @click="load()">
          {{ t('admin.refundHistory.refresh') }}
        </UiButton>
      </div>
      <p v-if="history && !history.canCheck">
        {{ t('admin.refundHistory.unavailable') }}
      </p>
      <p v-if="error" role="alert">
        {{ error }}
      </p>
      <p v-if="loading" role="status">
        {{ t('admin.refundHistory.loading') }}
      </p>
      <RefundCheckHistory v-if="history && paymentId" :payment-id="paymentId" :revision="historyRevision" @unavailable="clearEvidence" />
      <h3>{{ t('admin.refundHistory.attempts') }}</h3>
      <UiEmptyState v-if="history && !history.items.length && !loading" :title="t('admin.refundHistory.empty')" density="compact" />
      <UiCard v-for="item in history?.items ?? []" :key="item.operationId" class="refund-history-attempt">
        <div class="refund-history-controls">
          <UiCardTag>{{ t(`admin.refundHistory.statuses.${item.status}`) }}</UiCardTag>
          <UiCardTag v-if="item.reconciliationRequired" variant="warning">
            {{ t('admin.refundHistory.review') }}
          </UiCardTag>
          <strong>{{ formatCurrency(item.amountCents, item.currency, locale) }}</strong>
        </div>
        <p>{{ item.provider }} · {{ formatDateTime(item.requestedAt, locale, session.user?.timezone ?? 'UTC') }}</p>
        <code>{{ item.operationId }}</code>
        <code>{{ item.providerRefundId ?? t('admin.refundHistory.unbound') }}</code>
      </UiCard>
      <UiButton v-if="history?.nextCursor" variant="secondary" :disabled="loading || requesting" @click="load(true)">
        {{ t('admin.refundHistory.more') }}
      </UiButton>
    </div>
  </UiDrawer>
</template>

<style scoped>
.refund-history-panel { display: grid; align-content: start; gap: 16px; height: 100%; overflow-y: auto; padding: 24px; }
.refund-history-panel header, .refund-history-controls { display: flex; align-items: center; flex-wrap: wrap; gap: 12px; }
.refund-history-panel header { justify-content: space-between; }
.refund-history-panel h2, .refund-history-panel h3, .refund-history-panel p { margin: 0; }
.refund-history-panel h2 { font-size: 20px; }
.refund-history-panel h3 { font-size: 16px; }
.refund-history-panel p { color: var(--text-secondary); line-height: 1.6; }
.refund-history-panel code { display: block; overflow-wrap: anywhere; }
.refund-history-attempt { display: grid; gap: 12px; padding: 20px; min-width: 0; }
</style>
