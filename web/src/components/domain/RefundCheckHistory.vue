<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, APIError, messageFrom, type AdminRefundCheckDetail, type AdminRefundCheckPage } from '../../api/client'
import { formatDateTime } from '../../lib/format'
import { useSessionStore } from '../../stores/session'
import UiButton from '../ui/UiButton.vue'
import UiCardTag from '../ui/UiCardTag.vue'
import UiCollapsible from '../ui/UiCollapsible.vue'
import UiEmptyState from '../ui/UiEmptyState.vue'
import UiTabs from '../ui/UiTabs.vue'
import RefundObservations from './RefundObservations.vue'
import RefundReadReceipts from './RefundReadReceipts.vue'

const props = defineProps<{ paymentId: string; revision: number }>()
const emit = defineEmits<{ unavailable: [reason: unknown] }>()
const { t, locale } = useI18n()
const session = useSessionStore()
const allowed = computed(() => session.initialized && Boolean(session.user?.permissions.includes('admin:finance')))
const review = ref('all')
const page = ref<AdminRefundCheckPage>({ items: [] })
const loading = ref(false)
const error = ref('')
const openId = ref<string | null>(null)
const detail = ref<AdminRefundCheckDetail | null>(null)
const detailLoading = ref(false)
const detailError = ref('')
let epoch = 0
let detailVersion = 0
let listController: globalThis.AbortController | undefined
let detailController: globalThis.AbortController | undefined

function clearEvidence() {
  ++epoch
  ++detailVersion
  listController?.abort()
  detailController?.abort()
  page.value = { items: [] }
  openId.value = null
  detail.value = null
  error.value = detailError.value = ''
  loading.value = detailLoading.value = false
}
function unavailable(reason: unknown) {
  clearEvidence()
  error.value = messageFrom(reason)
  emit('unavailable', reason)
}
function handleFailure(reason: unknown, fromDetail = false) {
  if (reason instanceof APIError && [401, 403, 404].includes(reason.status)) unavailable(reason)
  else if (fromDetail) detailError.value = messageFrom(reason)
  else error.value = messageFrom(reason)
}

async function load(more = false) {
  if (!allowed.value || loading.value) return
  const cursor = more ? page.value.nextCursor : undefined
  if (more && !cursor) return
  const current = epoch
  listController?.abort()
  listController = new globalThis.AbortController()
  loading.value = true
  error.value = ''
  try {
    const result = await api.adminRefundChecks(props.paymentId, review.value === 'unresolved' ? 'unresolved' : 'all', cursor, listController.signal)
    if (!allowed.value || current !== epoch) return
    if (more) {
      const known = new Set(page.value.items.map(item => item.id))
      result.items = [...page.value.items, ...result.items.filter(item => !known.has(item.id))]
    }
    page.value = result
  } catch (reason) {
    if (current === epoch) handleFailure(reason)
  } finally {
    if (current === epoch) loading.value = false
  }
}

async function show(id: string, open: boolean) {
  if (!allowed.value) return
  const current = epoch
  const request = ++detailVersion
  detailController?.abort()
  openId.value = open ? id : null
  detail.value = null
  detailError.value = ''
  detailLoading.value = false
  if (!open) return
  detailController = new globalThis.AbortController()
  detailLoading.value = true
  try {
    const result = await api.adminRefundCheck(props.paymentId, id, detailController.signal)
    if (allowed.value && current === epoch && request === detailVersion && openId.value === id) detail.value = result
  } catch (reason) {
    if (current === epoch && request === detailVersion) handleFailure(reason, true)
  } finally {
    if (current === epoch && request === detailVersion) detailLoading.value = false
  }
}

watch(() => [props.paymentId, props.revision, review.value, session.user?.id, allowed.value], (current, previous) => {
  clearEvidence()
  // The parent closes this inspection on an identity change. Do not start a
  // request for its old payment while waiting for the subtree to unmount.
  if (previous && (current[3] !== previous[3] || current[4] !== previous[4])) return
  void load()
}, { immediate: true, flush: 'sync' })
onBeforeUnmount(() => { ++epoch; ++detailVersion; listController?.abort(); detailController?.abort() })
</script>

<template>
  <section v-if="allowed" class="refund-check-history" :aria-label="t('admin.refundHistory.checkHistory')">
    <h3>{{ t('admin.refundHistory.checkHistory') }}</h3>
    <UiTabs v-model="review" :label="t('admin.refundHistory.checkFilter')" :items="[{ value: 'all', label: t('admin.refundHistory.allChecks') }, { value: 'unresolved', label: t('admin.refundHistory.currentReview') }]" />
    <p>{{ t('admin.refundHistory.historyHelp') }}</p>
    <p v-if="error" role="alert">
      {{ error }}
    </p>
    <UiButton v-if="error" variant="secondary" :disabled="loading" @click="load(Boolean(page.nextCursor))">
      {{ t('admin.refundHistory.retry') }}
    </UiButton>
    <p v-if="loading" role="status">
      {{ t('admin.refundHistory.loading') }}
    </p>
    <UiEmptyState v-if="!loading && !error && !page.items.length" :title="t(review === 'unresolved' ? 'admin.refundHistory.noCurrentReview' : 'admin.refundHistory.noChecks')" density="compact" />
    <UiCollapsible v-for="check in page.items" :key="check.id" :open="openId === check.id" @update:open="show(check.id, $event)">
      <template #trigger>
        <span class="refund-check-summary">
          <span>{{ t(`admin.refundHistory.statuses.${check.status}`) }} · {{ formatDateTime(check.createdAt, locale, session.user?.timezone ?? 'UTC') }}</span>
          <span>{{ t(check.origin === 'automatic' ? 'admin.refundHistory.automatic' : 'admin.refundHistory.operator') }}</span>
          <UiCardTag v-if="check.requiresReview" variant="warning">{{ t('admin.refundHistory.currentReview') }}</UiCardTag>
        </span>
      </template>
      <div v-if="openId === check.id" class="refund-check-detail">
        <p v-if="detailLoading" role="status">
          {{ t('admin.refundHistory.loading') }}
        </p>
        <template v-if="detailError">
          <p role="alert">
            {{ detailError }}
          </p>
          <UiButton variant="secondary" @click="show(check.id, true)">
            {{ t('admin.refundHistory.retry') }}
          </UiButton>
        </template>
        <template v-if="detail">
          <code>{{ detail.id }}</code>
          <p v-if="detail.unrecordedReadCount" role="status">
            {{ t('admin.refundHistory.unrecordedReads', { count: detail.unrecordedReadCount }) }}
          </p>
          <p v-if="detail.recoveredReadCount">
            {{ t('admin.refundHistory.recoveredReads', { count: detail.recoveredReadCount }) }}
          </p>
          <p>{{ t('admin.refundHistory.source') }}</p>
          <p v-if="detail.status === 'failed'">
            {{ t('admin.refundHistory.error') }}
          </p>
          <p>{{ t('admin.refundHistory.historicalCount', { count: detail.unresolvedCount }) }}</p>
          <p v-if="detail.observedAt">
            {{ t('admin.refundHistory.observedAt') }} · {{ formatDateTime(detail.observedAt, locale, session.user?.timezone ?? 'UTC') }}
          </p>
          <p v-if="detail.completedAt">
            {{ t('admin.refundHistory.completedAt') }} · {{ formatDateTime(detail.completedAt, locale, session.user?.timezone ?? 'UTC') }}
          </p>
          <h4>{{ t('admin.refundHistory.observations') }}</h4>
          <p v-if="!detail.observedAt">
            {{ t('admin.refundHistory.notObserved') }}
          </p>
          <p v-else-if="!detail.observations.length">
            {{ t('admin.refundHistory.noObservations') }}
          </p>
          <RefundObservations :items="detail.observations" :unresolved-ids="detail.unresolvedProviderRefundIds" />
          <RefundReadReceipts v-if="detail.lateReceiptCount" :key="`${detail.id}:${revision}`" :payment-id="paymentId" :check-id="detail.id" :count="detail.lateReceiptCount" @unavailable="unavailable" />
        </template>
      </div>
    </UiCollapsible>
    <UiButton v-if="page.nextCursor" variant="secondary" :disabled="loading" @click="load(true)">
      {{ t('admin.refundHistory.moreChecks') }}
    </UiButton>
  </section>
</template>

<style scoped>
.refund-check-history, .refund-check-summary, .refund-check-detail { display: grid; gap: 10px; min-width: 0; }
.refund-check-summary { text-align: start; }
.refund-check-detail { padding-block: 12px; }
.refund-check-history h3, .refund-check-history h4, .refund-check-history p { margin: 0; }
.refund-check-history p { color: var(--text-secondary); line-height: 1.6; }
.refund-check-history code { display: block; overflow-wrap: anywhere; }
</style>
