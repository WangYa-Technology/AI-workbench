<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { components } from '../../api/schema'
import { formatDateTime } from '../../lib/format'
import { useSessionStore } from '../../stores/session'
import UiAlert from '../ui/UiAlert.vue'
import UiCardContent from '../ui/UiCardContent.vue'
import UiCardTag from '../ui/UiCardTag.vue'

defineProps<{ result: components['schemas']['SellerBankPayoutStatus'] }>()
const { t, locale } = useI18n()
const session = useSessionStore()
const date = (value: string) => formatDateTime(value, locale.value, session.user?.timezone || 'UTC')
</script>

<template>
  <section :aria-label="t('sellerBankResult.title')">
    <UiCardContent :title="t('sellerBankResult.title')" :summary="result.status === 'unconfirmed' ? t('sellerBankResult.noResult') : undefined">
      <template #tags>
        <UiCardTag>{{ t(`sellerBankResult.status.${result.status}`) }}</UiCardTag>
      </template>
      <template #meta>
        <span v-if="result.observedAt">{{ t('sellerBankResult.observed', { date: date(result.observedAt) }) }}</span>
        <span v-if="result.checkedAt">{{ t('sellerBankResult.checked', { date: date(result.checkedAt) }) }}</span>
      </template>
    </UiCardContent>
    <UiAlert v-if="result.requiresReview" variant="warning" :message="t('sellerBankResult.review')" />
  </section>
</template>
