<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { api, APIError, messageFrom, type SellerSale, type SellerSaleDetail, type SellerSaleEvent } from '../api/client'
import { useSessionStore } from '../stores/session'
import { formatCurrency, formatDateTime } from '../lib/format'
import AuthRequiredState from '../components/domain/AuthRequiredState.vue'
import PageHeader from '../components/ui/PageHeader.vue'
import UiAlert from '../components/ui/UiAlert.vue'
import UiButton from '../components/ui/UiButton.vue'
import UiFilterBar from '../components/ui/UiFilterBar.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiCatalog from '../components/ui/UiCatalog.vue'
import UiContentCard from '../components/ui/UiContentCard.vue'
import UiCardContent from '../components/ui/UiCardContent.vue'
import UiCardTag from '../components/ui/UiCardTag.vue'
import UiCardActions from '../components/ui/UiCardActions.vue'
import UiEmptyState from '../components/ui/UiEmptyState.vue'
import UiCollapsible from '../components/ui/UiCollapsible.vue'

const { t, te, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const id = computed(() => String(route.params.id || ''))
const queryValue = (key: string) => typeof route.query[key] === 'string' ? route.query[key] as string : ''
const status = computed(() => queryValue('status'))
const environment = computed(() => queryValue('environment'))
const productId = computed(() => queryValue('productId'))
const items = ref<SellerSale[]>([])
const detail = ref<SellerSaleDetail>()
const events = ref<SellerSaleEvent[]>([])
const total = ref(0)
const licenseOpen = ref(false)
const date = (value: string) => formatDateTime(value, locale.value, session.user?.timezone || 'UTC')
const cursor = ref<string>()
const eventCursor = ref<string>()
const loading = ref(true)
const busy = ref(false)
const eventsBusy = ref(false)
const error = ref('')
const eventError = ref('')
let version = 0
const orderStatuses = ['payment_pending', 'fulfilled', 'refund_requested', 'refunded', 'cancelled', 'payment_failed', 'payment_paid', 'test_pending', 'test_paid', 'test_refunded']
function statusLabel(value: string) {
  return te(`sellerSales.status.${value}`) ? t(`sellerSales.status.${value}`) : t('sellerSales.unknownStatus')
}
function setFilter(key: string, value: unknown) {
  void router.replace({ path: '/workspace/sales', query: { ...route.query, [key]: String(value || '') || undefined } })
}
async function loadEvents(current = version, more = false) {
  if (eventsBusy.value || !id.value || (more && !eventCursor.value)) return
  eventsBusy.value = true; eventError.value = ''
  try {
    const page = await api.listSellerSaleEvents(id.value, { cursor: more ? eventCursor.value : undefined })
    if (current !== version) return
    const seen = new Set(events.value.map(item => item.sequence))
    events.value.push(...page.items.filter(item => !seen.has(item.sequence)))
    eventCursor.value = page.nextCursor
  } catch (cause) {
    if (current !== version) return
    if (cause instanceof APIError && [401, 403, 404].includes(cause.status)) {
      detail.value = undefined
      events.value = []
      eventCursor.value = undefined
      error.value = messageFrom(cause)
      eventError.value = ''
    } else eventError.value = messageFrom(cause)
  }
  finally { if (current === version) eventsBusy.value = false }
}
async function load() {
  const current = ++version
  items.value = []; detail.value = undefined; events.value = []; total.value = 0
  cursor.value = undefined; eventCursor.value = undefined; error.value = ''; eventError.value = ''
  licenseOpen.value = false; loading.value = true; busy.value = false; eventsBusy.value = false
  if (!session.initialized || !session.user) { loading.value = !session.initialized; return }
  try {
    if (id.value) {
      const item = await api.getSellerSale(id.value)
      if (current !== version) return
      detail.value = item
      await loadEvents(current)
    } else {
      const page = await api.listSellerSales({ status: status.value, environment: environment.value, productId: productId.value })
      if (current !== version) return
      items.value = page.items; total.value = page.total; cursor.value = page.nextCursor
    }
  } catch (cause) { if (current === version) error.value = messageFrom(cause) }
  finally { if (current === version) loading.value = false }
}
async function more() {
  if (!cursor.value || loading.value || busy.value) return
  const current = version; busy.value = true; error.value = ''
  try {
    const page = await api.listSellerSales({ status: status.value, environment: environment.value, productId: productId.value, cursor: cursor.value })
    if (current !== version) return
    const seen = new Set(items.value.map(item => item.orderId))
    items.value.push(...page.items.filter(item => !seen.has(item.orderId))); total.value = page.total; cursor.value = page.nextCursor
  } catch (cause) {
    if (current !== version) return
    if (cause instanceof APIError && [401, 403, 404].includes(cause.status)) {
      items.value = []
      total.value = 0
      cursor.value = undefined
      error.value = messageFrom(cause)
    } else error.value = messageFrom(cause)
  }
  finally { if (current === version) busy.value = false }
}
watch([() => route.fullPath, () => session.user?.id, () => session.initialized], load, { immediate: true })
onBeforeUnmount(() => { version++ })
</script>

<template>
  <section class="seller-sales-page content-width">
    <PageHeader :title="t('sellerSales.title')" :summary="t('sellerSales.summary')" artwork-src="/illustrations/headers/tasks.webp">
      <template #actions>
        <UiButton as="RouterLink" variant="secondary" to="/workspace/payouts">
          {{ t('sellerPayouts.title') }}
        </UiButton>
        <UiButton v-if="id" as="RouterLink" variant="secondary" :to="{ path: '/workspace/sales', query: route.query }">
          {{ t('sellerSales.back') }}
        </UiButton>
        <UiButton as="RouterLink" variant="secondary" to="/workspace/products">
          {{ t('sellerProducts.title') }}
        </UiButton>
      </template>
    </PageHeader>
    <AuthRequiredState v-if="session.initialized && !session.user" :title="t('authRequired.workspaceTitle')" :summary="t('authRequired.workspaceSummary')" :return-to="route.fullPath" />
    <template v-else>
      <UiFilterBar v-if="!id" as="div" split>
        <UiButton v-if="status || environment || productId" as="RouterLink" variant="ghost" to="/workspace/sales">
          {{ t('sellerSales.clear') }}
        </UiButton>
        <div class="ui-filter-bar__controls ui-filter-bar__controls--end">
          <UiSelect :model-value="status" :aria-label="t('sellerSales.orderStatus')" @update:model-value="setFilter('status', $event)">
            <option value="">
              {{ t('sellerSales.allStatuses') }}
            </option>
            <option v-for="value in orderStatuses" :key="value" :value="value">
              {{ statusLabel(value) }}
            </option>
          </UiSelect>
          <UiSelect :model-value="environment" :aria-label="t('sellerSales.environment')" @update:model-value="setFilter('environment', $event)">
            <option value="">
              {{ t('sellerSales.allEnvironments') }}
            </option>
            <option v-for="value in ['live', 'test', 'unknown']" :key="value" :value="value">
              {{ t(`sellerSales.env.${value}`) }}
            </option>
          </UiSelect>
        </div>
      </UiFilterBar>
      <p class="seller-sales-note">
        {{ t('sellerSales.settlementNote') }}
      </p>
      <UiAlert v-if="error" variant="danger" role="alert">
        {{ error }} <UiButton variant="ghost" :disabled="loading || busy" @click="cursor && items.length ? more() : load()">
          {{ t('sellerProducts.retry') }}
        </UiButton>
      </UiAlert>
      <p v-if="loading" role="status">
        {{ t('sellerSales.loading') }}
      </p>
      <template v-else-if="!id">
        <div class="task-results-meta">
          <strong>{{ t(total === 1 ? 'sellerSales.countOne' : 'sellerSales.count', { count: total }) }}</strong><span v-if="productId">{{ t('sellerSales.productFilter') }}</span>
        </div>
        <UiEmptyState v-if="!items.length && !error" :title="t('sellerSales.empty')" :message="t('sellerSales.emptySummary')" />
        <UiCatalog>
          <UiContentCard v-for="item in items" :key="item.orderId" :to="{ path: `/workspace/sales/${item.orderId}`, query: route.query }">
            <UiCardContent :title="item.title" :summary="date(item.createdAt)">
              <template #tags>
                <UiCardTag>{{ statusLabel(item.status) }}</UiCardTag>
                <UiCardTag>{{ t(`sellerSales.env.${item.environment}`) }}</UiCardTag>
                <UiCardTag v-if="item.settlement">
                  {{ t(`sellerSales.settlement.status.${item.settlement.status}`) }}
                </UiCardTag>
                <UiCardTag v-if="item.needsReview">
                  {{ t('sellerSales.needsReview') }}
                </UiCardTag>
              </template>
            </UiCardContent>
            <UiCardActions :label="t('sellerSales.amount')" :value="formatCurrency(item.amountCents, item.currency, locale)" :action-label="t('sellerSales.view')" />
          </UiContentCard>
        </UiCatalog>
        <div v-if="cursor" class="catalog-pagination">
          <UiButton variant="secondary" :loading="busy" @click="more">
            {{ t('sellerProducts.more') }}
          </UiButton>
        </div>
      </template>
      <section v-else-if="detail" class="seller-sale-detail" :aria-label="t('sellerSales.view')">
        <UiCardContent :title="detail.title">
          <template #tags>
            <UiCardTag>{{ statusLabel(detail.status) }}</UiCardTag><UiCardTag>{{ t(`sellerSales.env.${detail.environment}`) }}</UiCardTag>
          </template>
        </UiCardContent>
        <p v-if="detail.needsReview" role="status">
          {{ t('sellerSales.reviewHelp') }}
        </p>
        <p v-if="!detail.hasContract">
          {{ t('sellerSales.legacyHelp') }}
        </p>
        <dl class="seller-sale-facts">
          <div><dt>{{ t('sellerSales.amount') }}</dt><dd>{{ formatCurrency(detail.amountCents, detail.currency, locale) }}</dd></div>
          <div><dt>{{ t('sellerSales.paymentStatus') }}</dt><dd>{{ detail.paymentStatus ? statusLabel(detail.paymentStatus) : t('sellerSales.unknownStatus') }}</dd></div>
          <div><dt>{{ t('sellerSales.orderId') }}</dt><dd>{{ detail.orderId }}</dd></div>
          <div><dt>{{ t('sellerSales.createdAt') }}</dt><dd>{{ date(detail.createdAt) }}</dd></div>
          <div v-if="detail.licenseAcceptedAt">
            <dt>{{ t('sellerSales.acceptedAt') }}</dt><dd>{{ date(detail.licenseAcceptedAt) }}</dd>
          </div>
          <div v-if="detail.paidAt">
            <dt>{{ t('sellerSales.paidAt') }}</dt><dd>{{ date(detail.paidAt) }}</dd>
          </div>
          <div v-if="detail.refundRequestedAt">
            <dt>{{ t('sellerSales.refundRequestedAt') }}</dt><dd>{{ date(detail.refundRequestedAt) }}</dd>
          </div>
          <div v-if="detail.refundedAt">
            <dt>{{ t('sellerSales.refundedAt') }}</dt><dd>{{ date(detail.refundedAt) }}</dd>
          </div>
        </dl>
        <section class="seller-settlement" :aria-label="t('sellerSales.settlement.title')">
          <UiCardContent :title="t('sellerSales.settlement.title')">
            <template v-if="detail.settlement" #tags>
              <UiCardTag>{{ t(`sellerSales.settlement.status.${detail.settlement.status}`) }}</UiCardTag>
              <UiCardTag>{{ t(`sellerSales.env.${detail.settlement.environment}`) }}</UiCardTag>
            </template>
          </UiCardContent>
          <template v-if="detail.settlement">
            <dl class="seller-sale-facts">
              <div><dt>{{ t('sellerSales.settlement.gross') }}</dt><dd>{{ formatCurrency(detail.settlement.grossAmountCents, detail.settlement.currency, locale) }}</dd></div>
              <div><dt>{{ t('sellerSales.settlement.feeRate') }}</dt><dd>{{ new Intl.NumberFormat(locale, { style: 'percent', maximumFractionDigits: 2 }).format(detail.settlement.feeBps / 10000) }}</dd></div>
              <div><dt>{{ t('sellerSales.settlement.fee') }}</dt><dd>{{ formatCurrency(detail.settlement.feeCents, detail.settlement.currency, locale) }}</dd></div>
              <div><dt>{{ t('sellerSales.settlement.net') }}</dt><dd>{{ formatCurrency(detail.settlement.netAmountCents, detail.settlement.currency, locale) }}</dd></div>
              <div><dt>{{ t('sellerSales.settlement.recovery') }}</dt><dd>{{ formatCurrency(detail.settlement.recoveryAmountCents, detail.settlement.currency, locale) }}</dd></div>
              <div v-if="detail.settlement.availableAt">
                <dt>{{ t('sellerSales.settlement.availableAt') }}</dt>
                <dd>{{ date(detail.settlement.availableAt) }}</dd>
              </div>
              <div v-if="detail.settlement.transferredAt">
                <dt>{{ t('sellerSales.settlement.transferredAt') }}</dt>
                <dd>{{ date(detail.settlement.transferredAt) }}</dd>
              </div>
            </dl>
            <p>{{ t('sellerSales.settlement.timing') }}</p>
          </template>
          <p v-else>
            {{ t('sellerSales.settlement.missing') }}
          </p>
        </section>
        <UiCollapsible v-model:open="licenseOpen" :title="t('sellerSales.license')">
          <p>{{ detail.licenseName }} · {{ detail.licenseVersion }}</p>
          <p class="seller-sale-terms">
            {{ detail.licenseTerms }}
          </p>
        </UiCollapsible>
        <section :aria-label="t('sellerSales.history')">
          <h2>{{ t('sellerSales.history') }}</h2>
          <p v-if="eventError" role="alert">
            {{ eventError }} <UiButton variant="ghost" :disabled="eventsBusy" @click="loadEvents(version, Boolean(eventCursor))">
              {{ t('sellerProducts.retry') }}
            </UiButton>
          </p>
          <p v-if="!events.length && !eventError && !eventsBusy">
            {{ t('sellerSales.noEvents') }}
          </p>
          <ol class="seller-sale-events">
            <li v-for="event in events" :key="event.sequence">
              <span>{{ event.fromStatus ? `${statusLabel(event.fromStatus)} → ` : '' }}{{ statusLabel(event.toStatus) }}</span><time :datetime="event.createdAt">{{ date(event.createdAt) }}</time>
            </li>
          </ol>
          <UiButton v-if="eventCursor" variant="secondary" :loading="eventsBusy" @click="loadEvents(version, true)">
            {{ t('sellerProducts.more') }}
          </UiButton>
        </section>
        <UiButton variant="secondary" :disabled="loading || eventsBusy" @click="load">
          {{ t('sellerProducts.retry') }}
        </UiButton>
      </section>
    </template>
  </section>
</template>

<style scoped>
.seller-sales-note { color: var(--text-secondary); margin-block: 20px; }
.seller-sale-detail { display: grid; gap: 24px; min-width: 0; }
.seller-settlement { display: grid; gap: 16px; min-width: 0; }
.seller-settlement p { margin: 0; color: var(--text-secondary); }
.seller-sale-facts { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(240px, 100%), 1fr)); gap: 20px; margin: 0; }
.seller-sale-facts dt { color: var(--text-secondary); margin-bottom: 6px; }
.seller-sale-facts dd { margin: 0; overflow-wrap: anywhere; }
.seller-sale-terms { white-space: pre-wrap; overflow-wrap: anywhere; }
.seller-sale-events { padding-left: 20px; }
.seller-sale-events li { padding-block: 10px; }
.seller-sale-events time { display: block; color: var(--text-secondary); }
</style>
