<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, APIError, messageFrom, type AdminRefundReadReceiptPage } from '../../api/client'
import { formatDateTime } from '../../lib/format'
import { useSessionStore } from '../../stores/session'
import UiButton from '../ui/UiButton.vue'
import UiCardTag from '../ui/UiCardTag.vue'
import RefundObservations from './RefundObservations.vue'
const props = defineProps<{ paymentId: string; checkId: string; count: number }>()
const emit = defineEmits<{ unavailable: [reason: unknown] }>()
const { t, locale } = useI18n()
const session = useSessionStore()
const allowed = computed(() => session.initialized && Boolean(session.user?.permissions.includes('admin:finance')))
const page = ref<AdminRefundReadReceiptPage>({ items: [] })
const loading = ref(false)
const error = ref('')
const cursor = ref('')
let epoch = 0
let controller: globalThis.AbortController | undefined
function clearEvidence() {
  ++epoch
  controller?.abort()
  page.value = { items: [] }
  error.value = cursor.value = ''
  loading.value = false
}
async function load(next = '') {
  if (!allowed.value || loading.value) return
  const current = epoch
  cursor.value = next
  controller?.abort()
  controller = new globalThis.AbortController()
  loading.value = true
  error.value = ''
  try {
    const result = await api.adminRefundReadReceipts(props.paymentId, props.checkId, next, controller.signal)
    if (allowed.value && current === epoch) page.value = result
  } catch (reason) {
    if (current !== epoch) return
    if (reason instanceof APIError && [401, 403, 404].includes(reason.status)) {
      clearEvidence()
      emit('unavailable', reason)
    }
    error.value = messageFrom(reason)
  } finally {
    if (current === epoch) loading.value = false
  }
}
watch(() => [props.paymentId, props.checkId, session.user?.id, allowed.value], (current, previous) => {
  clearEvidence()
  if (previous && (current[2] !== previous[2] || current[3] !== previous[3])) return
  void load()
}, { immediate: true, flush: 'sync' })
onBeforeUnmount(() => { ++epoch; controller?.abort() })
</script>

<template>
  <section v-if="allowed" class="refund-read-receipts" :aria-label="t('admin.refundHistory.lateReceipts', { count })">
    <h4>{{ t('admin.refundHistory.lateReceipts', { count }) }}</h4>
    <p>{{ t('admin.refundHistory.lateHelp') }}</p>
    <p v-if="loading" role="status">
      {{ t('admin.refundHistory.loading') }}
    </p>
    <p v-if="error" role="alert">
      {{ error }}
    </p>
    <UiButton v-if="error" variant="secondary" :disabled="loading" @click="load(cursor)">
      {{ t('admin.refundHistory.retry') }}
    </UiButton>
    <p v-if="!loading && !error && !page.items.length">
      {{ t('admin.refundHistory.noReceipts') }}
    </p>
    <article v-for="receipt in page.items" :key="receipt.id">
      <UiCardTag :variant="receipt.complete ? 'neutral' : 'warning'">
        {{ t(receipt.complete ? 'admin.refundHistory.completeRead' : 'admin.refundHistory.partialRead') }}
      </UiCardTag>
      <p>{{ formatDateTime(receipt.createdAt, locale, session.user?.timezone ?? 'UTC') }}</p>
      <RefundObservations :items="receipt.observations" :unresolved-ids="receipt.unresolvedProviderRefundIds" />
    </article>
    <div class="refund-receipt-navigation">
      <UiButton v-if="cursor" variant="secondary" :disabled="loading" @click="load()">
        {{ t('admin.refundHistory.newestReceipt') }}
      </UiButton>
      <UiButton v-if="page.nextCursor" variant="secondary" :disabled="loading" @click="load(page.nextCursor)">
        {{ t('admin.refundHistory.olderReceipt') }}
      </UiButton>
    </div>
  </section>
</template>

<style scoped>
.refund-read-receipts { display: grid; gap: 10px; min-width: 0; }
.refund-read-receipts h4, .refund-read-receipts p { margin: 0; }
.refund-read-receipts p { color: var(--text-secondary); line-height: 1.6; }
.refund-receipt-navigation { display: flex; flex-wrap: wrap; gap: 8px; }
</style>
