<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { api, APIError, messageFrom, type SellerFundsBalance, type SellerPayoutItem, type SellerPayoutOption, type SellerPayoutBankOption } from '../api/client'
import { createScopedApi } from '../lib/scopedApi'
import { formatCurrency, formatDateTime } from '../lib/format'
import { useSessionStore } from '../stores/session'
import PayoutBankSummary from '../components/domain/PayoutBankSummary.vue'
import SellerPayoutBankResult from '../components/domain/SellerPayoutBankResult.vue'
import AuthRequiredState from '../components/domain/AuthRequiredState.vue'
import PageHeader from '../components/ui/PageHeader.vue'
import UiAlert from '../components/ui/UiAlert.vue'
import UiButton from '../components/ui/UiButton.vue'
import UiForm from '../components/ui/UiForm.vue'
import UiFormField from '../components/ui/UiFormField.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiCatalog from '../components/ui/UiCatalog.vue'
import UiContentCard from '../components/ui/UiContentCard.vue'
import UiCardContent from '../components/ui/UiCardContent.vue'
import UiCardActions from '../components/ui/UiCardActions.vue'
import UiCardTag from '../components/ui/UiCardTag.vue'
import UiEmptyState from '../components/ui/UiEmptyState.vue'

const { t, te, locale } = useI18n()
const session = useSessionStore()
const route = useRoute()
const requestId = computed(() => String(route.params.id || ''))
const funds = ref<SellerFundsBalance>()
const items = ref<SellerPayoutItem[]>([])
const options = ref<SellerPayoutOption[]>([])
const availability = ref('unavailable')
const cursor = ref<string>()
const optionCursor = ref<string>()
const selected = ref('')
const pending = ref<SellerPayoutOption>()
const chosen = computed(() => pending.value || options.value.find(item => item.settlementId === selected.value))
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const success = ref('')
const bankRequest = ref('')
const banks = ref<SellerPayoutBankOption[]>([])
const bank = ref('')
const bankError = ref('')
const bankLoading = ref(false)
const bankAttempt = ref(false)
const cancelling = ref('')
let generation = 0
let bankGeneration = 0
const money = (value: number, currency = 'USD') => formatCurrency(value, currency, locale.value)
const date = (value: string) => formatDateTime(value, locale.value, session.user?.timezone || 'UTC')
const status = (value: string) => te(`sellerPayouts.status.${value}`) ? t(`sellerPayouts.status.${value}`) : t('sellerSales.unknownStatus')
const sourceReturnStatus = (value: string) => te(`sellerPayouts.sourceReturn.status.${value}`) ? t(`sellerPayouts.sourceReturn.status.${value}`) : value
const optionLabel = (item: SellerPayoutOption) => `${item.title} · ${money(item.amountCents)} · ${t(`sellerSales.env.${item.environment}`)} · ${item.orderId}`

function clearBank() {
  bankGeneration++; bankRequest.value = ''; banks.value = []; bank.value = ''; bankError.value = ''; bankLoading.value = false; bankAttempt.value = false
}
function clearData() {
  funds.value = undefined
  items.value = []; options.value = []; cursor.value = undefined; optionCursor.value = undefined
  selected.value = ''; pending.value = undefined; availability.value = 'unavailable'; cancelling.value = ''; clearBank()
}
function scope(current: number) {
  return createScopedApi(api, () => current === generation && session.initialized && !!session.user, cause => {
    if (cause instanceof APIError && [401, 403, 404].includes(cause.status)) {
      clearData(); success.value = ''; error.value = messageFrom(cause)
      generation++; loading.value = false; busy.value = false
    }
  })
}
async function refresh(current: number) {
  const client = scope(current)
  if (requestId.value) {
    const item = await client.getSellerPayoutRequest(requestId.value)
    items.value = [item]; options.value = []; cursor.value = undefined; optionCursor.value = undefined
    selected.value = ''; clearBank(); cancelling.value = ''
    return
  }
  const [choices, history, balance] = await Promise.all([client.listSellerPayoutOptions(), client.listSellerPayoutRequests(), client.getSellerFunds()])
  if (current !== generation) return
  funds.value = balance
  options.value = choices.items; optionCursor.value = choices.nextCursor; availability.value = choices.availability
  items.value = history.items; cursor.value = history.nextCursor
  selected.value = ''; clearBank(); cancelling.value = ''
}
async function load() {
  if (busy.value) return
  const current = ++generation
  clearData(); error.value = ''; success.value = ''; loading.value = true
  if (!session.initialized || !session.user) { loading.value = !session.initialized; return }
  try { await refresh(current) }
  catch (cause) { if (current === generation) error.value = messageFrom(cause) }
  finally { if (current === generation) loading.value = false }
}
async function more(kind: 'options' | 'history') {
  const next = kind === 'options' ? optionCursor.value : cursor.value
  if (!next || busy.value || loading.value) return
  const current = generation; busy.value = true; error.value = ''
  try {
    const client = scope(current)
    if (kind === 'options') {
      const page = await client.listSellerPayoutOptions({ cursor: next })
      const seen = new Set(options.value.map(item => item.settlementId))
      options.value.push(...page.items.filter(item => !seen.has(item.settlementId)))
      optionCursor.value = page.nextCursor; availability.value = page.availability
    } else {
      const page = await client.listSellerPayoutRequests({ cursor: next })
      const seen = new Set(items.value.map(item => item.id))
      items.value.push(...page.items.filter(item => !seen.has(item.id))); cursor.value = page.nextCursor
    }
  } catch (cause) { if (current === generation) error.value = messageFrom(cause) }
  finally { if (current === generation) busy.value = false }
}
async function create() {
  if (!chosen.value || busy.value || loading.value || (!pending.value && availability.value !== 'available')) return
  const current = generation; const choice = { ...chosen.value }
  pending.value = choice; funds.value = undefined; busy.value = true; error.value = ''; success.value = ''
  try {
    const result = await scope(current).createSellerPayoutRequest({ settlementId: choice.settlementId, amountCents: choice.amountCents })
    pending.value = undefined; selected.value = ''
    success.value = ['requested', 'under_review'].includes(result.status)
      ? t('sellerPayouts.created') : t('sellerPayouts.recovered', { status: status(result.status) })
    // Refresh failure must not invite repeating an acknowledged creation.
    options.value = []; availability.value = 'unavailable'
    await refresh(current)
  } catch (cause) {
    if (current !== generation) return
    error.value = messageFrom(cause)
    if (cause instanceof APIError && (cause.status === 422 || [
      'seller_funds_insufficient', 'seller_recovery_due', 'seller_funds_reconciliation_required', 'seller_payout_allocation_required',
      'seller_payout_idempotency_conflict', 'seller_payout_unavailable',
    ].includes(cause.code))) pending.value = undefined
  }
  finally { if (current === generation) busy.value = false }
}
async function readBanks(item: SellerPayoutItem) {
  if (busy.value || bankLoading.value || !item.canSelectBank) return
  clearBank(); bankRequest.value = item.id; bankLoading.value = true
  const current = generation; const bankCurrent = bankGeneration
  try {
    const result = await scope(current).listSellerPayoutBanks(item.id)
    if (bankCurrent === bankGeneration) banks.value = result.items
  } catch (cause) { if (current === generation && bankCurrent === bankGeneration) bankError.value = messageFrom(cause) }
  finally { if (current === generation && bankCurrent === bankGeneration) bankLoading.value = false }
}
async function bind(item: SellerPayoutItem) {
  if (busy.value || !bank.value || bankRequest.value !== item.id || !banks.value.some(option => option.bankDestinationId === bank.value)) return
  const current = generation; busy.value = true; bankAttempt.value = true; bankError.value = ''; success.value = ''
  try {
    const target = await scope(current).bindSellerPayoutBank(item.id, bank.value)
    item.bankTarget = target; item.canSelectBank = false; clearBank(); success.value = t('sellerPayouts.bankSaved')
  } catch (cause) { if (current === generation) bankError.value = messageFrom(cause) }
  finally { if (current === generation) busy.value = false }
}
async function cancel(item: SellerPayoutItem) {
  if (busy.value || cancelling.value !== item.id || !item.canCancel) return
  funds.value = undefined
  const current = generation; busy.value = true; error.value = ''; success.value = ''
  try {
    const result = await scope(current).cancelSellerPayoutRequest(item.id)
    Object.assign(item, result, { canCancel: false, canSelectBank: false })
    clearBank(); cancelling.value = ''; success.value = t('sellerPayouts.cancelled')
    await refresh(current)
  } catch (cause) { if (current === generation) error.value = messageFrom(cause) }
  finally { if (current === generation) busy.value = false }
}
watch([() => session.user, () => session.initialized, () => route.fullPath], () => {
  generation++; busy.value = false; void load()
}, { immediate: true, flush: 'sync', deep: true })
onBeforeUnmount(() => { generation++; bankGeneration++ })
</script>

<template>
  <section class="seller-payouts-page content-width">
    <PageHeader :title="t('sellerPayouts.title')" :summary="t('sellerPayouts.summary')" artwork-src="/illustrations/headers/tasks.webp">
      <template #actions>
        <UiButton v-if="requestId" as="RouterLink" variant="secondary" to="/workspace/payouts">
          {{ t('sellerPayouts.allRequests') }}
        </UiButton>
        <UiButton v-if="session.user" variant="ghost" :disabled="busy || loading || !!pending" @click="load">
          {{ t('sellerProducts.retry') }}
        </UiButton>
        <UiButton as="RouterLink" variant="secondary" to="/workspace/sales">
          {{ t('sellerSales.back') }}
        </UiButton>
        <UiButton as="RouterLink" variant="secondary" to="/settings?section=payouts">
          {{ t('account.payouts') }}
        </UiButton>
      </template>
    </PageHeader>
    <AuthRequiredState v-if="session.initialized && !session.user" :title="t('authRequired.workspaceTitle')" :summary="t('authRequired.workspaceSummary')" :return-to="route.fullPath" />
    <div v-else class="seller-payouts-content">
      <UiAlert variant="info" :message="t('sellerPayouts.boundary')" />
      <UiAlert v-if="success" variant="success" :message="success" />
      <UiAlert v-if="error" variant="danger" role="alert">
        {{ error }}
        <UiButton variant="ghost" :disabled="busy || loading || !!pending" @click="load">
          {{ t('sellerProducts.retry') }}
        </UiButton>
      </UiAlert>
      <p v-if="loading" role="status">
        {{ t('sellerPayouts.loading') }}
      </p>
      <template v-else>
        <section v-if="!requestId && funds" :aria-label="t('sellerPayouts.funds.title')" class="seller-payouts-section">
          <div class="task-results-meta">
            <strong>{{ t('sellerPayouts.funds.title') }}</strong>
            <span>{{ t('sellerPayouts.funds.asOf', { date: date(funds.asOf) }) }}</span>
          </div>
          <p>{{ t('sellerPayouts.funds.help') }}</p>
          <UiAlert v-if="funds.unresolvedRecords" variant="warning" :message="t('sellerPayouts.funds.unresolved', { count: funds.unresolvedRecords })" />
          <UiCatalog v-if="funds.accounts.length" grid>
            <UiContentCard v-for="account in funds.accounts" :key="account.accountId" class="seller-funds-account">
              <UiCardContent :title="`${account.provider === 'stripe' ? 'Stripe' : 'Waffo'} · ${account.currency}`" :summary="t('sellerPayouts.funds.account', { id: account.accountId.slice(-12) })">
                <template #tags>
                  <UiCardTag :variant="account.environment === 'test' ? 'neutral' : 'success'">
                    {{ t(`sellerSales.env.${account.environment}`) }}
                  </UiCardTag>
                </template>
              </UiCardContent>
              <dl class="seller-funds-balances">
                <div v-for="field in (['pendingCents', 'availableCents', 'reservedCents', 'recoveryDueCents', 'withdrawableCents'] as const)" :key="field">
                  <dt>{{ t(`sellerPayouts.funds.${field}`) }}</dt>
                  <dd>{{ money(account[field], account.currency) }}</dd>
                </div>
              </dl>
            </UiContentCard>
          </UiCatalog>
          <UiEmptyState v-else density="compact" :title="t('sellerPayouts.funds.empty')" :message="t('sellerPayouts.funds.emptyHelp')" />
        </section>
        <section v-if="!requestId" :aria-label="t('sellerPayouts.newRequest')" class="seller-payouts-section">
          <div class="task-results-meta">
            <strong>{{ t('sellerPayouts.newRequest') }}</strong>
          </div>
          <UiForm v-if="options.length || pending" :disabled="busy" @submit="create">
            <UiFormField :label="t('sellerPayouts.settlement')" :hint="t('sellerPayouts.selectionHelp')">
              <UiSelect v-model="selected" :aria-label="t('sellerPayouts.settlement')" :disabled="busy || !!pending">
                <option value="">
                  {{ t('sellerPayouts.choose') }}
                </option>
                <option v-for="item in options" :key="item.settlementId" :value="item.settlementId">
                  {{ optionLabel(item) }}
                </option>
              </UiSelect>
            </UiFormField>
            <UiButton v-if="optionCursor && !pending" variant="ghost" :disabled="busy" @click="more('options')">
              {{ t('sellerPayouts.moreOptions') }}
            </UiButton>
            <p v-if="pending" role="status">
              {{ t('sellerPayouts.retryHelp') }}
            </p>
            <UiCardActions :label="t('sellerPayouts.amount')" :value="chosen ? money(chosen.amountCents) : '—'" numeric>
              <UiButton type="submit" :loading="busy" :disabled="!chosen || (!pending && availability !== 'available')">
                {{ t(pending ? 'sellerPayouts.retryRequest' : 'sellerPayouts.submit') }}
              </UiButton>
            </UiCardActions>
          </UiForm>
          <UiEmptyState v-else-if="!error" density="compact" :title="t(`sellerPayouts.availability.${availability}`)" :message="t('sellerPayouts.noOptionsHelp')" />
        </section>
        <section :aria-label="t('sellerPayouts.history')" class="seller-payouts-section">
          <div class="task-results-meta">
            <strong>{{ t('sellerPayouts.history') }}</strong><span>{{ t('sellerPayouts.loaded', { count: items.length }) }}</span>
          </div>
          <UiEmptyState v-if="!items.length && !error" :title="t('sellerPayouts.empty')" :message="t('sellerPayouts.emptyHelp')" />
          <UiCatalog>
            <UiContentCard v-for="item in items" :key="item.id" class="seller-payout-row">
              <UiCardContent :title="date(item.createdAt)" :summary="t('sellerPayouts.requestId', { id: item.id })">
                <template #tags>
                  <UiCardTag>{{ status(item.status) }}</UiCardTag>
                  <UiCardTag>{{ t(`sellerSales.env.${item.environment}`) }}</UiCardTag>
                  <UiCardTag v-if="item.bankTarget">
                    {{ t('sellerPayouts.bound') }}
                  </UiCardTag>
                </template>
                <template v-if="item.bankTarget" #meta>
                  <PayoutBankSummary :bank-destination-id="item.bankTarget.bankDestinationId" :bank-name="item.bankTarget.bankName" :last4="item.bankTarget.last4" />
                </template>
              </UiCardContent>
              <UiCardActions :label="t('sellerPayouts.amount')" :value="money(item.amountCents)" numeric>
                <UiButton v-if="!requestId" as="RouterLink" variant="secondary" :to="`/workspace/payouts/${item.id}`">
                  {{ t('sellerPayouts.viewRequest') }}
                </UiButton>
                <UiButton v-if="item.canSelectBank" variant="secondary" :disabled="busy || bankLoading || bankAttempt" @click="readBanks(item)">
                  {{ t('sellerPayouts.selectBank') }}
                </UiButton>
                <UiButton v-if="item.canCancel" variant="ghost" :disabled="busy || !!pending" @click="cancelling = item.id">
                  {{ t('sellerPayouts.cancel') }}
                </UiButton>
              </UiCardActions>
              <section v-if="item.latestReview" class="seller-payout-row-detail" :aria-label="t('sellerPayouts.review')">
                <div><UiCardTag>{{ t(`payoutReviews.${item.latestReview.decision}`) }}</UiCardTag> · {{ date(item.latestReview.createdAt) }}</div>
                <p class="seller-payout-review-message">
                  {{ item.latestReview.sellerMessage || t('sellerPayouts.legacyReview') }}
                </p>
                <p>{{ t('sellerPayouts.currentState', { status: status(item.status) }) }}</p>
              </section>
              <SellerPayoutBankResult v-if="item.bankPayout" class="seller-payout-row-detail" :result="item.bankPayout" />
              <section v-if="item.sourceReturn" class="seller-payout-row-detail seller-source-return" :aria-label="t('sellerPayouts.sourceReturn.title')">
                <div class="seller-source-return-heading">
                  <strong>{{ t('sellerPayouts.sourceReturn.title') }}</strong>
                  <UiCardTag :variant="item.sourceReturn.requiresReview ? 'warning' : item.sourceReturn.status === 'closed' ? 'success' : 'neutral'">
                    {{ sourceReturnStatus(item.sourceReturn.status) }}
                  </UiCardTag>
                </div>
                <p>{{ t(`sellerPayouts.sourceReturn.help.${item.sourceReturn.status}`) }}</p>
                <dl class="seller-source-return-meta">
                  <div v-if="item.sourceReturn.observedAt"><dt>{{ t('sellerPayouts.sourceReturn.observedAt') }}</dt><dd>{{ date(item.sourceReturn.observedAt) }}</dd></div>
                  <div v-if="item.sourceReturn.closedAt"><dt>{{ t('sellerPayouts.sourceReturn.closedAt') }}</dt><dd>{{ date(item.sourceReturn.closedAt) }}</dd></div>
                  <div v-if="item.sourceReturn.resolution"><dt>{{ t('sellerPayouts.sourceReturn.resolution') }}</dt><dd>{{ t(`sellerPayouts.sourceReturn.resolutions.${item.sourceReturn.resolution}`) }}</dd></div>
                </dl>
              </section>
              <div v-if="cancelling === item.id" class="seller-payout-row-detail">
                <p>{{ t('sellerPayouts.cancelHelp') }}</p>
                <UiButton variant="destructive" :loading="busy" @click="cancel(item)">
                  {{ t('sellerPayouts.confirmCancel') }}
                </UiButton>
                <UiButton variant="ghost" :disabled="busy" @click="cancelling = ''">
                  {{ t('sellerPayouts.keep') }}
                </UiButton>
              </div>
              <div v-if="bankRequest === item.id" class="seller-payout-row-detail">
                <p v-if="bankLoading" role="status">
                  {{ t('sellerPayouts.loadingBanks') }}
                </p>
                <UiAlert v-if="bankError" variant="danger" role="alert">
                  {{ bankError }}
                </UiAlert>
                <UiForm v-if="banks.length" :disabled="busy" @submit="bind(item)">
                  <UiFormField :label="t('sellerPayouts.bank')" :hint="t('sellerPayouts.bankHelp')">
                    <UiSelect v-model="bank" :aria-label="t('sellerPayouts.bank')" :disabled="busy || bankAttempt">
                      <option value="">
                        {{ t('sellerPayouts.chooseBank') }}
                      </option>
                      <option v-for="entry in banks" :key="entry.bankDestinationId" :value="entry.bankDestinationId">
                        {{ entry.bankName }} · •••• {{ entry.last4 }} · {{ entry.currency }}
                      </option>
                    </UiSelect>
                  </UiFormField>
                  <UiButton type="submit" :disabled="!bank" :loading="busy">
                    {{ t(bankAttempt ? 'sellerPayouts.retryBank' : 'sellerPayouts.saveBank') }}
                  </UiButton>
                </UiForm>
                <UiEmptyState v-else-if="!bankLoading && !bankError" density="compact" :title="t('sellerPayouts.noBanks')" :message="t('sellerPayouts.noBanksHelp')" />
                <UiButton v-if="bankError && !banks.length" variant="secondary" :disabled="busy" @click="readBanks(item)">
                  {{ t('sellerProducts.retry') }}
                </UiButton>
              </div>
            </UiContentCard>
          </UiCatalog>
          <div v-if="cursor" class="catalog-pagination">
            <UiButton variant="secondary" :loading="busy" @click="more('history')">
              {{ t('sellerProducts.more') }}
            </UiButton>
          </div>
        </section>
      </template>
    </div>
  </section>
</template>

<style scoped>
.seller-payouts-content, .seller-payouts-section { display: grid; gap: 20px; min-width: 0; }
.seller-funds-account { display: grid; gap: 16px; }
.seller-funds-balances { display: grid; gap: 8px; margin: 0; font-size: 13px; }
.seller-funds-balances > div { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 4px 12px; }
.seller-funds-balances dt { color: var(--text-secondary); }
.seller-funds-balances dd { margin: 0; font-variant-numeric: tabular-nums; }
.seller-payouts-content { padding-block: 24px; gap: 28px; }
.seller-payout-row { display: grid; grid-template-columns: minmax(0, 1fr) 240px; gap: 20px; }
.seller-payout-row-detail { grid-column: 1 / -1; display: grid; gap: 12px; min-width: 0; }
.seller-payout-row-detail p { margin: 0; color: var(--text-secondary); }
.seller-payout-review-message { white-space: pre-wrap; overflow-wrap: anywhere; }
.seller-source-return { border-top: 1px solid var(--border-subtle); padding-top: 12px; }
.seller-source-return-heading { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; }
.seller-source-return-meta { display: flex; flex-wrap: wrap; gap: 8px 20px; margin: 0; color: var(--text-secondary); font-size: 13px; }
.seller-source-return-meta div { display: flex; gap: 6px; }
.seller-source-return-meta dt { font-weight: 600; }
.seller-source-return-meta dd { margin: 0; }
@media (max-width: 767px) { .seller-payout-row { grid-template-columns: minmax(0, 1fr); } }
</style>
