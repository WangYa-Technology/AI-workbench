<script setup lang="ts">
import { computed } from 'vue'
import { PackageCheck } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'
import type { Order } from '../../api/client'
import { validProductReturnID } from '../../lib/productPayment'
import UiButton from '../ui/UiButton.vue'

const props = withDefaults(defineProps<{
  order?: Pick<Order, 'status' | 'assetId'> | null
  size?: 'sm' | 'md'
}>(), { order: null, size: 'md' })
const { t } = useI18n()
// Navigation does not grant access. Asset details and downloads still enforce
// current server-side rights, including ordinary refunds that remain pending.
const destination = computed(() => {
  const order = props.order
  if (!order?.assetId || !validProductReturnID(order.assetId)
    || !['fulfilled', 'refund_requested'].includes(order.status)) return null
  return `/workspace/assets/${order.assetId}`
})
</script>

<template>
  <UiButton v-if="destination" as="RouterLink" class="product-order-delivery-link" variant="primary" :size="size" :to="destination">
    <template #start>
      <PackageCheck :size="17" aria-hidden="true" />
    </template>
    {{ t('workspace.productPayment.openPurchase') }}
  </UiButton>
</template>
