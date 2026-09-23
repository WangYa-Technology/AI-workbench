<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { AdminRefundCheckDetail } from '../../api/client'
import { formatCurrency } from '../../lib/format'
import UiCardTag from '../ui/UiCardTag.vue'
defineProps<{ items: AdminRefundCheckDetail['observations']; unresolvedIds: string[] }>()
const { t, locale } = useI18n()
</script>

<template>
  <div v-for="item in items" :key="item.providerId" class="refund-observation">
    <UiCardTag v-if="unresolvedIds.includes(item.providerId)" variant="warning">
      {{ t('admin.refundHistory.currentReview') }}
    </UiCardTag>
    <code>{{ item.providerId }}</code>
    <span>{{ formatCurrency(item.amountCents, item.currency, locale) }} · {{ t(`admin.refundHistory.statuses.${item.status}`) }}</span>
    <span>{{ t('admin.refundHistory.originalPayment') }} <code>{{ item.providerPaymentId }}</code></span>
    <span v-if="item.operationId">{{ t('admin.refundHistory.originalOperation') }} <code>{{ item.operationId }}</code></span>
  </div>
</template>

<style scoped>
.refund-observation { display: grid; gap: 10px; min-width: 0; border-top: 1px solid var(--border); padding-block: 12px; }
.refund-observation code { display: block; overflow-wrap: anywhere; }
</style>
