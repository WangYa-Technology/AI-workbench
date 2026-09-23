<script setup lang="ts">
import { useI18n } from 'vue-i18n'

defineProps<{ bankDestinationId: string; bankName?: string; last4?: string }>()
const { t } = useI18n()
</script>

<template>
  <span class="payout-bank-summary">
    <span>{{ last4 ? (bankName || t('sellerPayouts.unnamedBank')) : t('sellerPayouts.legacyBank') }}<template v-if="last4"> · <bdi>•••• {{ last4 }}</bdi></template></span>
    <small><bdi>{{ bankDestinationId }}</bdi></small>
  </span>
</template>

<style scoped>
.payout-bank-summary { display: flex; flex-direction: column; gap: 4px; min-width: 0; overflow-wrap: anywhere; }
.payout-bank-summary small { color: var(--text-tertiary); font-size: 11px; line-height: 1.5; }
</style>
