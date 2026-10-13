<script setup lang="ts">
import { computed, nextTick, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { Ellipsis, RotateCcw } from 'lucide-vue-next'
import type { Order } from '../../api/client'
import { formatCurrency, formatDateTime } from '../../lib/format'
import { validRefundReason } from '../../lib/productRefund'
import UiButton from '../ui/UiButton.vue'
import UiDialog from '../ui/UiDialog.vue'
import UiTabs from '../ui/UiTabs.vue'
import UiTextarea from '../ui/UiTextarea.vue'
import UiDropdownMenu from '../ui/UiDropdownMenu.vue'
import ProductOrderDeliveryLink from './ProductOrderDeliveryLink.vue'
import ProductCheckoutClosure from './ProductCheckoutClosure.vue'

const props = defineProps<{
  order: Order; timezone: string; refreshing: string; refunding: string;
  copied: boolean; refundReason: string; initialOpen?: boolean;
}>()
const emit = defineEmits<{
  refresh: []; copy: []; refund: []; updated: [order: Order]; 'update:refundReason': [value: string];
}>()
const { t, locale } = useI18n()
const detailsOpen = ref(Boolean(props.initialOpen))
const detailTab = ref(props.initialOpen ? 'license' : 'transaction')
const detailTabId = `order-detail-${useId()}`
const detailTabs = computed(() => [
  { value: 'transaction', label: t('workspace.orderTransactionDetails') },
  { value: 'license', label: t('workspace.orderLicenseTerms') },
  { value: 'history', label: t('workspace.eventHistory') },
])
function openDetails() { detailTab.value = 'transaction'; detailsOpen.value = true }
const refundForm = ref<globalThis.HTMLFormElement | null>(null)
const shortDate = computed(() => new Intl.DateTimeFormat(locale.value, {
  dateStyle: 'medium', timeZone: props.timezone,
}).format(new Date(props.order.createdAt)))
const needsRefresh = computed(() => ['test_pending', 'payment_pending', 'test_paid', 'payment_paid', 'refund_requested', 'payment_failed'].includes(props.order.status)
  || props.order.paymentStatus === 'refund_failed')
const paymentBadge = computed(() => props.order.paymentMode === 'unverified'
  ? t('workspace.orderUnverifiedBadge')
  : !props.order.realCharge ? t('workspace.orderTestBadge') : '')
async function focusRefund() {
  await nextTick()
  if (refundOpen.value) refundForm.value?.querySelector('textarea')?.focus()
}
const refundOpen = ref(false)
const date = (value: string) => formatDateTime(value, locale.value, props.timezone)
function paymentMode(order: Order) {
  if (order.paymentMode === 'unverified') return t('workspace.unverifiedPaymentMode')
  if (order.paymentMode === 'test') return t('workspace.localTestMode')
  if (order.paymentMode === 'waffo_pancake') return t(order.realCharge ? 'workspace.waffoLiveMode' : 'workspace.waffoTestMode')
  if (order.paymentMode === 'epay') return t('workspace.epayMode')
  return t(order.realCharge ? 'workspace.stripeLiveMode' : 'workspace.stripeTestMode')
}
function statusTone(status: Order['status']) {
  if (status === 'fulfilled') return 'accepted'
  if (['test_pending', 'payment_pending', 'test_paid', 'payment_paid', 'refund_requested'].includes(status)) return 'submitted'
  if (status === 'payment_failed') return 'disputed'
  return 'neutral'
}
</script>

<template>
  <article class="order-row order-record" :data-order-status="order.status">
    <header class="order-card-header">
      <div class="order-card-identity">
        <div class="order-record-title">
          <RouterLink :to="`/market/assets/${order.productId}`">
            <h2>{{ order.productTitle }}</h2>
          </RouterLink>
          <div class="order-record-badges">
            <span class="task-status" :data-status="statusTone(order.status)">{{ t(`marketplace.orderStatus.${order.status}`) }}</span>
            <span v-if="paymentBadge" class="order-payment-badge">{{ paymentBadge }}</span>
          </div>
        </div>
        <p class="order-card-meta">
          <time :datetime="order.createdAt">{{ shortDate }}</time><span aria-hidden="true">·</span><span>{{ order.licenseName }}</span>
        </p>
      </div>
      <div class="order-card-amount">
        <span>{{ t('workspace.orderAmount') }}</span>
        <strong>{{ formatCurrency(order.amountCents, order.currency, locale) }}</strong>
      </div>
    </header>
    <ProductCheckoutClosure :order="order" @updated="emit('updated', $event)" />
    <div class="order-record-footer">
      <UiButton variant="ghost" size="sm" class="order-details-toggle" aria-haspopup="dialog" @click="openDetails">
        {{ t('workspace.orderDetails') }}
      </UiButton>
      <div class="order-actions">
        <UiButton v-if="needsRefresh" class="order-refresh" variant="secondary" size="sm" :loading="refreshing === order.id" :disabled="refreshing !== ''" @click="emit('refresh')">
          {{ t('workspace.refreshOrder') }}
        </UiButton>
        <ProductOrderDeliveryLink :order="order" size="sm" variant="secondary" />
        <UiDropdownMenu v-if="!needsRefresh || order.canRequestRefund" class="order-more" :label="t('workspace.orderMoreActions')" trigger-class="order-more-trigger" @select="focusRefund">
          <template #trigger>
            <Ellipsis :size="18" aria-hidden="true" />
          </template>
          <UiButton v-if="!needsRefresh" class="order-refresh" role="menuitem" variant="ghost" :loading="refreshing === order.id" :disabled="refreshing !== ''" @click="emit('refresh')">
            {{ t('workspace.refreshOrder') }}
          </UiButton>
          <UiButton v-if="order.canRequestRefund" class="order-refund-toggle" role="menuitem" variant="ghost" :aria-expanded="refundOpen" :aria-controls="`order-refund-${order.id}`" @click="refundOpen = !refundOpen">
            {{ t('workspace.orderRefundDisclosure') }}
          </UiButton>
        </UiDropdownMenu>
      </div>
    </div>
    <UiDialog v-model:open="detailsOpen" :title="t('workspace.orderDetails')" :close-label="t('actions.close')" size="lg">
      <div class="order-record-details">
        <header class="order-detail-overview">
          <div>
            <h3>{{ order.productTitle }}</h3><div class="order-record-badges">
              <span class="task-status" :data-status="statusTone(order.status)">{{ t(`marketplace.orderStatus.${order.status}`) }}</span><span v-if="paymentBadge" class="order-payment-badge">{{ paymentBadge }}</span>
            </div>
          </div>
          <div class="order-detail-amount">
            <span>{{ t('workspace.orderAmount') }}</span><strong>{{ formatCurrency(order.amountCents, order.currency, locale) }}</strong>
          </div>
        </header>
        <UiTabs v-model="detailTab" :id-prefix="detailTabId" :items="detailTabs" :label="t('workspace.orderDetails')" variant="segmented" class="order-detail-tabs" />
        <div class="order-detail-panels">
          <section v-show="detailTab === 'transaction'" :id="`${detailTabId}-panel-transaction`" role="tabpanel" :aria-labelledby="`${detailTabId}-tab-transaction`" tabindex="0">
            <dl class="order-detail-facts">
              <div class="order-reference">
                <dt>{{ t('workspace.orderReference') }}</dt><dd>{{ order.id }}</dd>
              </div>
              <div><dt>{{ t('workspace.placedAt') }}</dt><dd>{{ date(order.createdAt) }}</dd></div>
              <div><dt>{{ t('workspace.orderPaymentMode') }}</dt><dd>{{ paymentMode(order) }}</dd></div>
              <div><dt>{{ t('marketplace.refundWindow') }}</dt><dd>{{ t('marketplace.refundDays', { count: order.refundWindowDays }) }}</dd></div>
              <div v-if="order.refundDeadlineAt">
                <dt>{{ t('workspace.refundDeadline') }}</dt><dd>{{ date(order.refundDeadlineAt) }}</dd>
              </div>
            </dl>
            <p v-if="order.status === 'fulfilled' && order.refundUnavailableReason" class="order-refund-note">
              {{ t(`workspace.refundUnavailable.${order.refundUnavailableReason}`) }}
            </p>
          </section>
          <section v-show="detailTab === 'license'" :id="`${detailTabId}-panel-license`" class="order-license-panel" role="tabpanel" :aria-labelledby="`${detailTabId}-tab-license`" tabindex="0">
            <h3>{{ order.licenseName }} <span>v{{ order.licenseVersion }}</span></h3>
            <p>{{ order.licenseTerms }}</p>
          </section>
          <section v-show="detailTab === 'history'" :id="`${detailTabId}-panel-history`" class="order-history-panel" role="tabpanel" :aria-labelledby="`${detailTabId}-tab-history`" tabindex="0">
            <p v-if="!order.events.length" class="order-history-empty">
              {{ t('workspace.noOrderEvents') }}
            </p>
            <ol v-else>
              <li v-for="event in order.events" :key="`${event.toStatus}-${event.createdAt}`">
                <div><strong>{{ t(`marketplace.orderEvents.${event.toStatus}`) }}</strong><time :datetime="event.createdAt">{{ date(event.createdAt) }}</time></div>
                <p v-if="event.toStatus === 'refund_requested'">
                  {{ event.reason }}
                </p>
              </li>
            </ol>
          </section>
        </div>
      </div>
      <template #footer>
        <div class="order-support-actions">
          <UiButton variant="ghost" @click="emit('copy')">
            {{ t('workspace.copyOrder') }}
          </UiButton>
          <UiButton as="RouterLink" variant="secondary" :to="{ path: '/support', query: { orderId: order.id } }">
            {{ t('workspace.productPayment.support') }}
          </UiButton>
          <span v-if="copied" role="status">{{ t('workspace.orderCopied') }}</span>
        </div>
      </template>
    </UiDialog>
    <form v-if="order.canRequestRefund && refundOpen" :id="`order-refund-${order.id}`" ref="refundForm" class="refund-form" @submit.prevent="emit('refund')">
      <label>{{ t('workspace.refundReason') }}<UiTextarea :model-value="refundReason" rows="2" required :placeholder="t('workspace.refundPlaceholder')" @update:model-value="emit('update:refundReason', String($event))" /><small>{{ t('workspace.refundReasonLength') }}</small></label>
      <UiButton class="command-button secondary" variant="secondary" type="submit" :loading="refunding === order.id" :disabled="refunding !== '' || !validRefundReason(refundReason)">
        <template #start>
          <RotateCcw v-if="refunding !== order.id" :size="16" />
        </template>{{ t(order.paymentMode === 'test' ? 'workspace.requestRefund' : 'workspace.requestProviderRefund') }}
      </UiButton>
    </form>
  </article>
</template>

<style scoped>
.order-record.order-row { overflow: visible; padding: 16px; border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface); box-shadow: none; }
.order-record:has(.order-more [aria-expanded='true']) { z-index: 2; }
.order-record::before { display: none; }
.order-record .order-card-header { flex-wrap: nowrap; gap: 16px; }
.order-record .order-card-identity { flex: 1 1 auto; gap: 8px; }
.order-record-title { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 10px; min-width: 0; }
.order-record-badges { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; }
.order-record-title a { min-width: 0; color: var(--text); text-decoration: none; }
.order-record-title a:hover { color: var(--accent-readable); }
.order-record-title h2 { font-size: 16px; font-weight: 600; line-height: 1.5; overflow-wrap: anywhere; }
.order-record-title .task-status, .order-payment-badge { flex-shrink: 0; font-size: 12px; }
.order-payment-badge { padding: 3px 6px; border-radius: var(--radius-control); background: var(--surface-muted); color: var(--text-secondary); }
.order-record .task-status[data-status='neutral'] { background: var(--surface-muted); color: var(--text-secondary); }
.order-card-meta { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 8px; margin: 0; color: var(--text-secondary); font-size: 13px; line-height: 1.5; }
.order-record .order-card-amount { gap: 4px; }
.order-record .order-card-amount strong { font-family: inherit; font-size: 18px; font-weight: 600; font-variant-numeric: tabular-nums; }
.order-record .order-card-amount > span { font-size: 12px; }
.order-record-footer { display: flex; align-items: center; justify-content: space-between; gap: 8px 16px; flex-wrap: wrap; margin-top: 16px; }
.order-record .order-actions { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; margin: 0; padding: 0; border: 0; }
.order-record-footer :deep(.ui-button), .order-support-actions :deep(.ui-button) { min-height: 32px; padding-inline: 10px; font-size: 13px; font-weight: 400; }
.order-record-footer > .order-details-toggle { padding-inline: 0; color: var(--text-secondary); }
.order-more :deep(.order-more-trigger) { width: 32px; padding: 0; background: transparent; color: var(--text-secondary); box-shadow: none; border-color: transparent; }
.order-more :deep(.order-more-trigger:hover), .order-more :deep(.order-more-trigger[aria-expanded='true']) { background: var(--surface-muted); color: var(--text); }
.order-more :deep(.ui-dropdown-menu__content) { left: auto; right: 0; }
.order-more :deep([role='menuitem']) { display: flex; width: 100%; justify-content: flex-start; white-space: nowrap; }
.order-record .order-actions > span { color: var(--text-secondary); font-size: 12px; }
.order-record-details { display: grid; grid-template-columns: minmax(0, 1fr); gap: 20px; }
.order-detail-overview { display: flex; justify-content: space-between; align-items: start; gap: 20px; padding: 16px; border-radius: var(--radius-control); background: var(--surface-muted); }
.order-detail-overview > div:first-child { min-width: 0; }
.order-detail-overview h3 { margin: 0 0 10px; font-size: 16px; line-height: 1.5; font-weight: 600; overflow-wrap: anywhere; }
.order-detail-amount { display: grid; flex-shrink: 0; gap: 6px; text-align: right; }
.order-detail-amount span { font-size: 12px; color: var(--text-secondary); }
.order-detail-amount strong { font-size: 20px; font-weight: 600; font-variant-numeric: tabular-nums; }
.order-detail-tabs { width: max-content; max-width: 100%; min-width: 0; }
.order-detail-tabs :deep(.ui-tabs__tab) { min-height: 36px; padding-inline: 12px; font-size: 13px; }
.order-detail-panels { min-height: 250px; }
.order-detail-facts { display: grid; gap: 0; margin: 0; }
.order-detail-facts > div { display: grid; grid-template-columns: 96px minmax(0, 1fr); align-items: baseline; gap: 16px; padding: 12px 0; }
.order-detail-facts > div:first-child { padding-top: 0; }
.order-detail-facts dt { font-size: 13px; color: var(--text-secondary); }
.order-detail-facts dd { margin: 0; font-size: 13px; line-height: 1.6; text-align: right; overflow-wrap: anywhere; }
.order-reference dd { font-family: var(--font-mono); font-size: 12px; }
.order-refund-note { margin: 12px 0 0; padding: 12px; color: var(--text-secondary); background: var(--surface-muted); border-radius: var(--radius-control); font-size: 13px; line-height: 1.65; }
.order-license-panel h3 { display: flex; align-items: baseline; flex-wrap: wrap; gap: 8px; margin: 0 0 16px; font-size: 14px; font-weight: 600; }
.order-license-panel h3 span { font-size: 12px; color: var(--text-secondary); font-weight: 400; }
.order-license-panel p { margin: 0; font-size: 14px; line-height: 1.8; color: var(--text-secondary); white-space: pre-wrap; overflow-wrap: anywhere; }
.order-history-panel ol { display: grid; gap: 20px; margin: 0; padding: 0; list-style: none; }
.order-history-panel li { padding-left: 16px; border-left: 2px solid var(--border); }
.order-history-panel li > div { display: flex; align-items: baseline; justify-content: space-between; flex-wrap: wrap; gap: 6px 16px; }
.order-history-panel strong { font-size: 13px; font-weight: 500; }
.order-history-panel time { font-size: 12px; color: var(--text-secondary); }
.order-history-panel p { margin: 8px 0 0; font-size: 13px; line-height: 1.65; white-space: pre-wrap; overflow-wrap: anywhere; color: var(--text-secondary); }
.order-history-panel .order-history-empty { margin: 0; }
.order-support-actions { display: flex; flex-wrap: wrap; align-items: center; justify-content: flex-end; gap: 8px; width: 100%; }
.order-support-actions > span { flex-basis: 100%; font-size: 12px; color: var(--text-secondary); }
.order-record-details .task-status[data-status='neutral'] { color: var(--text-secondary); background: var(--surface-muted); }
.order-record .refund-form { margin-top: 16px; padding: 12px; background: var(--surface-muted); }
.order-record .refund-form label { font-size: 13px; font-weight: 400; }
@media (max-width: 767px) {
  .order-record .order-card-header { flex-direction: column; gap: 12px; }
  .order-record .order-card-identity { flex-basis: auto; }
  .order-record .order-card-amount { display: flex; align-items: baseline; flex-wrap: wrap; gap: 8px; text-align: left; }
  .order-record .refund-form { grid-template-columns: 1fr; }
  .order-detail-facts > div { grid-template-columns: 84px minmax(0, 1fr); gap: 12px; }
  .order-detail-tabs { width: 100%; }
  .order-detail-tabs :deep(.ui-tabs__tab) { flex: 1 1 0; min-width: 0; min-height: 40px; padding-inline: 4px; white-space: normal; line-height: 1.4; }
  .order-detail-overview { flex-direction: column; }
  .order-detail-amount { display: flex; align-items: baseline; gap: 8px; }
  .order-record .refund-form > :deep(.ui-button) { white-space: normal; }
}
</style>
