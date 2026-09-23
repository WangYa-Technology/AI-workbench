<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, APIError, type Order } from '../../api/client'
import { validProductReturnID } from '../../lib/productPayment'
import { useSessionStore } from '../../stores/session'
import UiCard from '../ui/UiCard.vue'
import UiCardActions from '../ui/UiCardActions.vue'
import UiButton from '../ui/UiButton.vue'
import ProductOrderDeliveryLink from './ProductOrderDeliveryLink.vue'

const props = defineProps<{ orderId: string; paymentId: string; returnKind: string }>()
const emit = defineEmits<{ updated: [order: Order] }>()
const { t } = useI18n()
const session = useSessionStore()
const order = ref<Order | null>(null)
const outcome = ref<'polling' | 'done' | 'timeout' | 'error' | 'invalid'>('polling')
const busy = ref(false)
const connectionFailed = ref(false)
let timer: ReturnType<typeof globalThis.setTimeout> | undefined
let controller: globalThis.AbortController | undefined
let version = 0
let deadline = 0
const validIdentifiers = computed(() => validProductReturnID(props.orderId) && validProductReturnID(props.paymentId))
const phase = computed(() => {
  if (outcome.value === 'invalid' || outcome.value === 'error') return 'unavailable'
  if (order.value?.checkoutReconciliationRequired) return 'reconciliation'
  if (outcome.value === 'timeout') return 'delayed'
  if (connectionFailed.value) return 'connection'
  if (order.value?.paymentStatus === 'refund_failed') return 'refundFailed'
  switch (order.value?.status) {
    case 'fulfilled': return 'fulfilled'
    case 'refund_requested': return 'refundPending'
    case 'refunded': case 'test_refunded': return 'refunded'
    case 'payment_failed': return 'failed'
    case 'cancelled': return order.value.checkoutClosedBeforePayment ? 'closedBeforePayment' : 'cancelled'
    default:
      if (order.value?.checkoutExpiresAt && Date.parse(order.value.checkoutExpiresAt) <= Date.now()) return 'expired'
      return props.returnKind === 'cancelled' ? 'closed' : 'pending'
  }
})

function stop() {
  version++
  if (timer !== undefined) globalThis.clearTimeout(timer)
  timer = undefined
  controller?.abort()
  controller = undefined
}

async function poll(expectedVersion: number) {
  if (expectedVersion !== version) return
  if (Date.now() >= deadline) { outcome.value = 'timeout'; return }
  busy.value = true
  const request = new globalThis.AbortController()
  controller = request
  const timeout = globalThis.setTimeout(() => request.abort(), Math.min(10000, deadline - Date.now()))
  try {
    const current = await api.getOrder(props.orderId, { signal: request.signal })
    if (expectedVersion !== version) return
    if (current.id !== props.orderId || current.paymentId !== props.paymentId) {
      order.value = null
      outcome.value = 'invalid'
      return
    }
    order.value = current
    connectionFailed.value = false
    emit('updated', current)
    if (!['payment_pending', 'payment_paid', 'refund_requested'].includes(current.status) || current.paymentStatus === 'refund_failed') {
      outcome.value = 'done'
      return
    }
  } catch (reason) {
    if (expectedVersion !== version) return
    if (reason instanceof APIError && [401, 403, 404, 422].includes(reason.status)) {
      order.value = null
      outcome.value = 'error'
      return
    }
    connectionFailed.value = true
  } finally {
    globalThis.clearTimeout(timeout)
    if (expectedVersion === version) { busy.value = false; controller = undefined }
  }
  if (expectedVersion !== version) return
  if (Date.now() >= deadline) { outcome.value = 'timeout'; return }
  timer = globalThis.setTimeout(() => void poll(expectedVersion), Math.min(2500, deadline - Date.now()))
}

function refresh() {
  stop()
  order.value = null
  busy.value = false
  connectionFailed.value = false
  if (!validIdentifiers.value || !session.user) { outcome.value = 'invalid'; return }
  outcome.value = 'polling'
  deadline = Date.now() + 60000
  void poll(version)
}

watch(() => [props.orderId, props.paymentId, props.returnKind, session.user?.id], refresh, { immediate: true })
onBeforeUnmount(stop)
</script>

<template>
  <UiCard class="product-payment-status" :data-payment-state="phase" aria-labelledby="product-payment-status-title">
    <div role="status" aria-live="polite" aria-atomic="true">
      <h2 id="product-payment-status-title">
        {{ t(`workspace.productPayment.${phase}Title`) }}
      </h2>
      <p>{{ t(`workspace.productPayment.${phase}Body`) }}</p>
    </div>
    <UiCardActions :value="order?.productTitle || t('workspace.productPayment.title')" :description="order ? t('workspace.orderReference') + ' · ' + order.id : ''">
      <ProductOrderDeliveryLink :order="order" class="product-payment-primary" />
      <UiButton v-if="order?.status === 'cancelled'" as="RouterLink" variant="primary" :to="`/market/assets/${order.productId}`">
        {{ t('workspace.productPayment.reviewProduct') }}
      </UiButton>
      <UiButton v-if="validIdentifiers" variant="secondary" :loading="busy" @click="refresh">
        {{ t('workspace.productPayment.refresh') }}
      </UiButton>
      <UiButton as="RouterLink" variant="ghost" to="/support">
        {{ t('workspace.productPayment.support') }}
      </UiButton>
    </UiCardActions>
    <small v-if="outcome === 'polling'">{{ t('workspace.productPayment.checking') }}</small>
  </UiCard>
</template>

<style scoped>
.product-payment-status { display: grid; gap: 16px; min-width: 0; padding: 20px; }
.product-payment-status h2 { margin: 0 0 8px; font-size: 18px; }
.product-payment-status p { margin: 0; color: var(--text-secondary); line-height: 1.7; }
.product-payment-status small { color: var(--text-tertiary); }
@media (max-width: 600px) {
  .product-payment-status :deep(.product-payment-primary) { flex-basis: 100%; }
}
</style>
