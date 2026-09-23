<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { api, APIError, messageFrom, type SellerPayoutReviewItem, type SellerPayoutReviewInput, type SellerFundingAdmissionInput } from '../api/client'
import { createScopedApi } from '../lib/scopedApi'
import { textWithin } from '../lib/unicodeText'
import { formatCurrency, formatDateTime } from '../lib/format'
import { useSessionStore } from '../stores/session'
import PayoutBankSummary from '../components/domain/PayoutBankSummary.vue'
import SellerBankPayoutPanel from '../components/domain/SellerBankPayoutPanel.vue'
import SellerSourceReversalPanel from '../components/domain/SellerSourceReversalPanel.vue'
import AuthRequiredState from '../components/domain/AuthRequiredState.vue'
import PageHeader from '../components/ui/PageHeader.vue'
import UiAlert from '../components/ui/UiAlert.vue'
import UiAlertDialog from '../components/ui/UiAlertDialog.vue'
import UiButton from '../components/ui/UiButton.vue'
import UiForm from '../components/ui/UiForm.vue'
import UiFormField from '../components/ui/UiFormField.vue'
import UiTextarea from '../components/ui/UiTextarea.vue'
import UiCatalog from '../components/ui/UiCatalog.vue'
import UiContentCard from '../components/ui/UiContentCard.vue'
import UiCardContent from '../components/ui/UiCardContent.vue'
import UiCardActions from '../components/ui/UiCardActions.vue'
import UiCardTag from '../components/ui/UiCardTag.vue'
import UiEmptyState from '../components/ui/UiEmptyState.vue'

const { t, te, locale } = useI18n()
const session = useSessionStore()
const route = useRoute()
const id = computed(() => String(route.params.id || ''))
const allowed = computed(() => session.initialized && session.user?.status === 'active' && session.user.permissions.includes('admin:finance'))
const items = ref<SellerPayoutReviewItem[]>([])
const detail = ref<SellerPayoutReviewItem>()
const cursor = ref<string>()
const reason = ref('')
const sellerMessage = ref('')
const loading = ref(false)
const busy = ref(false)
const denied = ref(false)
const error = ref('')
const success = ref('')
const confirmation = ref(false)
const pending = ref<{ id: string; sellerId: string; environment: SellerPayoutReviewItem['environment']; bankName?: string; last4?: string; input: SellerPayoutReviewInput; attempted: boolean }>()
const sourcePending = ref(false), sourceBusy = ref(false)
const sourceRevision = ref(0), bankRevision = ref(0)
const bankPending = ref(false)
const bankBusy = ref(false)
const fundingReason = ref('')
const fundingConfirmation = ref(false)
const fundingPending = ref<{ id: string; sellerId: string; environment: SellerPayoutReviewItem['environment']; bankName?: string; last4?: string; input: SellerFundingAdmissionInput; attempted: boolean }>()
let generation = 0
const validId = (value: string) => /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value) && value !== '00000000-0000-0000-0000-000000000000'
const money = (value: number) => formatCurrency(value, 'USD', locale.value)
const date = (value: string) => formatDateTime(value, locale.value, session.user?.timezone || 'UTC')
const status = (value: string) => te(`sellerPayouts.status.${value}`) ? t(`sellerPayouts.status.${value}`) : t('sellerSales.unknownStatus')
const actionable = computed(() => allowed.value && !denied.value && detail.value?.id === id.value &&
  detail.value.sellerId !== session.user?.id && validId(detail.value.settlementId) &&
  ['requested', 'under_review'].includes(detail.value.status) && !detail.value.funding)
const validReason = computed(() => textWithin(reason.value, 10, 1000) && !reason.value.includes('\0') && textWithin(sellerMessage.value, 10, 1000) && !sellerMessage.value.includes('\0'))

const fundingActionable = computed(() => actionable.value && detail.value?.canAdmitFunding === true && detail.value.latestReview?.decision === 'approved')
const validFundingReason = computed(() => textWithin(fundingReason.value, 10, 1000) && !fundingReason.value.includes('\0'))
const fundingStatus = (value: string) => te(`payoutReviews.funding.status.${value}`) ? t(`payoutReviews.funding.status.${value}`) : t('sellerSales.unknownStatus')
function clearData() {
  sourcePending.value = false; sourceBusy.value = false
  bankPending.value = false; bankBusy.value = false
  fundingPending.value = undefined; fundingReason.value = ''; fundingConfirmation.value = false
  items.value = []; detail.value = undefined; cursor.value = undefined
  reason.value = ''; sellerMessage.value = ''; pending.value = undefined; confirmation.value = false; success.value = ''
}
function bankAccessDenied(cause: APIError) {
  generation++; clearData(); denied.value = true; error.value = messageFrom(cause)
  loading.value = false; busy.value = false
}
function updateBankRequest(request: SellerPayoutReviewItem) {
  if (!allowed.value || denied.value || request.id !== id.value) return
  if (detail.value?.status !== request.status) success.value = ''
  detail.value = request
}
function scope(current: number) {
  return createScopedApi(api, () => current === generation && allowed.value && !denied.value, cause => {
    if (cause instanceof APIError && [401, 403, 404].includes(cause.status)) {
      generation++; clearData(); denied.value = true; error.value = messageFrom(cause)
      loading.value = false; busy.value = false
    }
  })
}
async function load() {
  if (busy.value || sourceBusy.value || sourcePending.value || bankBusy.value || bankPending.value || pending.value?.attempted || fundingPending.value?.attempted) return
  const current = ++generation
  clearData(); denied.value = false; error.value = ''; loading.value = false
  if (!allowed.value) return
  if (id.value && !validId(id.value)) { error.value = t('payoutReviews.invalidId'); return }
  loading.value = true
  try {
    if (id.value) detail.value = await scope(current).getSellerPayoutReview(id.value)
    else {
      const page = await scope(current).listSellerPayoutReviews()
      items.value = page.items; cursor.value = page.nextCursor
    }
  } catch (cause) { if (current === generation) error.value = messageFrom(cause) }
  finally { if (current === generation) loading.value = false }
}
async function more() {
  if (!cursor.value || busy.value || loading.value || !allowed.value || denied.value) return
  const current = generation; busy.value = true; error.value = ''
  try {
    const page = await scope(current).listSellerPayoutReviews({ cursor: cursor.value })
    const seen = new Set(items.value.map(item => item.id))
    items.value.push(...page.items.filter(item => !seen.has(item.id))); cursor.value = page.nextCursor
  } catch (cause) { if (current === generation) error.value = messageFrom(cause) }
  finally { if (current === generation) busy.value = false }
}
function prepare(decision: SellerPayoutReviewInput['decision']) {
  const item = detail.value
  if (!item || !actionable.value || !validReason.value || pending.value || fundingPending.value || busy.value || loading.value) return
  if (decision === 'approved' && (!item.bankDestinationId || item.latestReview?.decision === 'approved')) return
  pending.value = { id: item.id, sellerId: item.sellerId, environment: item.environment, bankName: item.bankName, last4: item.last4, attempted: false, input: {
    expectedRevision: item.latestReview?.revision || 0, settlementId: item.settlementId,
    amountCents: item.amountCents, bankDestinationId: item.bankDestinationId,
    decision, reason: reason.value.trim(), sellerMessage: sellerMessage.value.trim(),
  } }
  confirmation.value = true
}
function cancelConfirmation() {
  if (!busy.value && !pending.value?.attempted) pending.value = undefined
}
async function submit() {
  const command = pending.value
  if (!command || command.id !== id.value || !allowed.value || denied.value || busy.value || loading.value) return
  const current = generation; command.attempted = true; busy.value = true; error.value = ''; success.value = ''
  confirmation.value = false
  try {
    const result = await scope(current).reviewSellerPayout(command.id, command.input)
    // A replay returns the original decision AND the current request, which may
    // already have been cancelled or rejected. Never restore the old state.
    detail.value = result.request; pending.value = undefined; reason.value = ''; sellerMessage.value = ''
    success.value = t(result.replayed ? 'payoutReviews.recovered' : 'payoutReviews.saved', { status: status(result.request.status) })
    // No dependent refresh: an acknowledged decision must survive a read error.
  } catch (cause) {
    if (current !== generation) return
    error.value = messageFrom(cause)
    if (cause instanceof APIError && [409, 422].includes(cause.status)) {
      pending.value = undefined; detail.value = undefined; reason.value = ''; sellerMessage.value = ''
      try { detail.value = await scope(current).getSellerPayoutReview(command.id) }
      catch (readFailure) { if (current === generation) error.value = `${error.value} ${messageFrom(readFailure)}` }
    }
  } finally { if (current === generation) busy.value = false }
}
function prepareFunding() {
  const item = detail.value
  if (!item?.latestReview || !fundingActionable.value || !validFundingReason.value || pending.value || fundingPending.value || busy.value || loading.value) return
  fundingPending.value = { id: item.id, sellerId: item.sellerId, environment: item.environment, bankName: item.bankName, last4: item.last4, attempted: false, input: {
    reviewId: item.latestReview.id, expectedRevision: item.latestReview.revision, settlementId: item.settlementId,
    amountCents: item.amountCents, bankDestinationId: item.bankDestinationId, reason: fundingReason.value.trim(), confirmed: true,
  } }
  fundingConfirmation.value = true
}
function cancelFundingConfirmation() {
  if (!busy.value && !fundingPending.value?.attempted) fundingPending.value = undefined
}
async function submitFunding() {
  const command = fundingPending.value
  if (!command || command.id !== id.value || !allowed.value || denied.value || pending.value || busy.value || loading.value) return
  const current = generation; command.attempted = true; busy.value = true; fundingConfirmation.value = false; error.value = ''; success.value = ''
  try {
    const result = await scope(current).admitSellerPayoutFunding(command.id, command.input)
    detail.value = result.request; fundingPending.value = undefined; fundingReason.value = ''
    success.value = t(result.replayed ? 'payoutReviews.funding.recovered' : 'payoutReviews.funding.saved')
  } catch (cause) {
    if (current !== generation) return
    error.value = messageFrom(cause)
    if (cause instanceof APIError && [409, 422].includes(cause.status)) {
      fundingPending.value = undefined; detail.value = undefined; fundingReason.value = ''
      try { detail.value = await scope(current).getSellerPayoutReview(command.id) }
      catch (readFailure) { if (current === generation) error.value = `${error.value} ${messageFrom(readFailure)}` }
    }
  } finally { if (current === generation) busy.value = false }
}
watch([() => session.user, () => session.initialized, () => route.fullPath], () => {
  generation++; busy.value = false; clearData(); void load()
}, { immediate: true, flush: 'sync', deep: true })
onBeforeUnmount(() => { generation++ })
</script>

<template>
  <section class="payout-reviews-page content-width">
    <PageHeader :title="t('payoutReviews.title')" :summary="t('payoutReviews.summary')" artwork-src="/illustrations/headers/tasks.webp">
      <template #actions>
        <UiButton v-if="allowed" variant="ghost" :disabled="busy || bankBusy || sourceBusy || sourcePending || loading || !!pending || !!fundingPending || bankPending" @click="load">
          {{ t('sellerProducts.retry') }}
        </UiButton>
        <UiButton as="RouterLink" variant="secondary" :to="id ? '/admin/payouts' : '/admin?tab=finance'">
          {{ t(id ? 'payoutReviews.back' : 'payoutReviews.finance') }}
        </UiButton>
      </template>
    </PageHeader>
    <AuthRequiredState v-if="session.initialized && !session.user" :title="t('authRequired.workspaceTitle')" :summary="t('authRequired.workspaceSummary')" :return-to="route.fullPath" />
    <div v-else class="payout-reviews-content">
      <p v-if="!session.initialized || loading" role="status">
        {{ t('sellerPayouts.loading') }}
      </p>
      <UiEmptyState v-else-if="!allowed" :title="t('payoutReviews.forbidden')" :message="t('payoutReviews.permissionHelp')" />
      <template v-if="allowed">
        <UiAlert v-if="error" variant="danger" role="alert" :message="error" />
        <UiAlert v-if="success" variant="success" role="status" :message="success" />
        <template v-if="!loading && !denied">
          <template v-if="!id">
            <div class="task-results-meta">
              <strong>{{ t('payoutReviews.directory') }}</strong><span>{{ t('sellerPayouts.loaded', { count: items.length }) }}</span>
            </div>
            <UiEmptyState v-if="!items.length && !error" :title="t('payoutReviews.empty')" :message="t('payoutReviews.emptyHelp')" />
            <UiCatalog>
              <UiContentCard v-for="item in items" :key="item.id" class="payout-review-row">
                <UiCardContent :title="t('sellerPayouts.requestId', { id: item.id })" :summary="date(item.createdAt)">
                  <template #tags>
                    <UiCardTag>{{ status(item.status) }}</UiCardTag><UiCardTag>{{ t(`sellerSales.env.${item.environment}`) }}</UiCardTag><UiCardTag v-if="item.latestReview">
                      {{ t(`payoutReviews.${item.latestReview.decision}`) }}
                    </UiCardTag>
                  </template>
                  <template #meta>
                    {{ t('payoutReviews.seller') }} · {{ item.sellerId }}
                  </template>
                </UiCardContent>
                <UiCardActions :label="t('sellerPayouts.amount')" :value="money(item.amountCents)" numeric>
                  <UiButton as="RouterLink" variant="secondary" :to="`/admin/payouts/${item.id}`">
                    {{ t('payoutReviews.open') }}
                  </UiButton>
                </UiCardActions>
              </UiContentCard>
            </UiCatalog>
            <UiButton v-if="cursor" variant="secondary" :loading="busy" @click="more">
              {{ t('sellerProducts.more') }}
            </UiButton>
          </template>
          <template v-else-if="detail">
            <UiContentCard class="payout-review-detail">
              <UiCardContent :title="t('sellerPayouts.requestId', { id: detail.id })" :summary="date(detail.createdAt)">
                <template #tags>
                  <UiCardTag>{{ status(detail.status) }}</UiCardTag><UiCardTag>{{ t(`sellerSales.env.${detail.environment}`) }}</UiCardTag>
                </template>
              </UiCardContent>
              <dl class="payout-review-facts">
                <div><dt>{{ t('sellerPayouts.amount') }}</dt><dd>{{ money(detail.amountCents) }}</dd></div>
                <div><dt>{{ t('payoutReviews.seller') }}</dt><dd>{{ detail.sellerId }}</dd></div>
                <div><dt>{{ t('sellerPayouts.settlement') }}</dt><dd>{{ detail.settlementId }}</dd></div>
                <div>
                  <dt>{{ t('payoutReviews.bank') }}</dt><dd>
                    <PayoutBankSummary v-if="detail.bankDestinationId" :bank-destination-id="detail.bankDestinationId" :bank-name="detail.bankName" :last4="detail.last4" /><template v-else>
                      {{ t('payoutReviews.noBank') }}
                    </template>
                  </dd>
                </div>
              </dl>
              <section v-if="detail.latestReview" class="payout-review-history" :aria-label="t('payoutReviews.latest')">
                <h2>{{ t('payoutReviews.latest') }} · {{ t(`payoutReviews.${detail.latestReview.decision}`) }}</h2>
                <p>{{ t('payoutReviews.revision', { revision: detail.latestReview.revision }) }} · {{ date(detail.latestReview.createdAt) }}</p>
                <p>{{ t('payoutReviews.reviewer') }} · {{ detail.latestReview.actorId }}</p>
                <p class="payout-review-reason">
                  {{ detail.latestReview.reason }}
                </p>
                <p class="payout-review-reason">
                  {{ t('payoutReviews.sellerMessage') }} · {{ detail.latestReview.sellerMessage || t('sellerPayouts.legacyReview') }}
                </p>
              </section>
            </UiContentCard>
            <UiAlert variant="info" :message="t('payoutReviews.boundary')" />
            <UiForm v-if="actionable || pending" :disabled="busy || !!pending || !!fundingPending" @submit="prepare('approved')">
              <UiFormField :label="t('payoutReviews.reason')" :hint="t('payoutReviews.reasonHelp')">
                <UiTextarea v-model="reason" :aria-label="t('payoutReviews.reason')" rows="4" required />
              </UiFormField>
              <UiFormField :label="t('payoutReviews.sellerMessage')" :hint="t('payoutReviews.sellerMessageHelp')">
                <UiTextarea v-model="sellerMessage" :aria-label="t('payoutReviews.sellerMessage')" rows="3" required />
              </UiFormField>
              <div class="payout-review-buttons">
                <UiButton type="submit" :disabled="!validReason || !detail.bankDestinationId || detail.latestReview?.decision === 'approved'">
                  {{ t('payoutReviews.approve') }}
                </UiButton>
                <UiButton variant="destructive" :disabled="!validReason" @click="prepare('rejected')">
                  {{ t('payoutReviews.reject') }}
                </UiButton>
              </div>
              <p v-if="!detail.bankDestinationId">
                {{ t('payoutReviews.bankHelp') }}
              </p>
            </UiForm>
            <UiAlert v-else variant="info" :message="t(detail.sellerId === session.user?.id ? 'payoutReviews.selfReview' : 'payoutReviews.readOnly')" />
            <section v-if="detail.funding" class="payout-review-history" :aria-label="t('payoutReviews.funding.title')">
              <h2>{{ t('payoutReviews.funding.title') }}</h2>
              <UiCardTag>{{ fundingStatus(detail.funding.status) }}</UiCardTag>
              <p>{{ t('payoutReviews.funding.resultBoundary') }}</p>
              <dl class="payout-review-facts">
                <div><dt>{{ t('payoutReviews.funding.transferId') }}</dt><dd>{{ detail.funding.transferId }}</dd></div>
                <div v-if="detail.funding.providerTransferId">
                  <dt>{{ t('payoutReviews.funding.receipt') }}</dt><dd>{{ detail.funding.providerTransferId }}</dd>
                </div>
                <div v-if="detail.funding.jobId">
                  <dt>{{ t('payoutReviews.funding.job') }}</dt><dd>{{ detail.funding.jobId }}</dd>
                </div>
                <div v-if="detail.funding.jobStatus">
                  <dt>{{ t('payoutReviews.funding.jobState') }}</dt><dd>{{ t(`payoutReviews.funding.jobs.${detail.funding.jobStatus}`) }}</dd>
                </div>
                <div v-if="detail.funding.startedAt">
                  <dt>{{ t('payoutReviews.funding.started') }}</dt><dd>{{ date(detail.funding.startedAt) }}</dd>
                </div>
              </dl>
              <UiAlert v-if="!detail.funding.admissionId || !detail.funding.jobId" variant="warning" :message="t('payoutReviews.funding.legacy')" />
              <UiAlert v-if="detail.funding.status === 'reconciliation_required' || (detail.funding.status !== 'succeeded' && ['failed', 'cancelled'].includes(detail.funding.jobStatus || ''))" variant="warning" :message="t('payoutReviews.funding.reconcile')" />
            </section>
            <UiForm v-else-if="fundingActionable || fundingPending" :disabled="busy || !!pending || !!fundingPending" @submit="prepareFunding">
              <UiFormField :label="t('payoutReviews.funding.reason')" :hint="t('payoutReviews.reasonHelp')">
                <UiTextarea v-model="fundingReason" :aria-label="t('payoutReviews.funding.reason')" rows="3" required />
              </UiFormField>
              <UiAlert variant="info" :message="t('payoutReviews.funding.boundary')" />
              <UiButton type="submit" :disabled="!validFundingReason || !fundingActionable">
                {{ t('payoutReviews.funding.prepare') }}
              </UiButton>
            </UiForm>
            <UiAlert v-else-if="detail.latestReview?.decision === 'approved' && actionable" variant="info" :message="t('payoutReviews.funding.unavailable')" />
            <SellerBankPayoutPanel v-if="detail.funding" :key="bankRevision" :request-id="detail.id" :blocked="busy || !!pending || !!fundingPending || sourceBusy || sourcePending" @changed="sourceRevision++" @busy="bankBusy = $event" @pending="bankPending = $event" @updated="updateBankRequest" @denied="bankAccessDenied" />
            <SellerSourceReversalPanel v-if="detail.funding?.status === 'succeeded'" :key="sourceRevision" :request-id="detail.id" :blocked="busy || !!pending || !!fundingPending || bankBusy || bankPending" @busy="sourceBusy = $event" @pending="sourcePending = $event" @updated="updateBankRequest" @changed="bankRevision++" @denied="bankAccessDenied" />
            <UiAlert v-if="fundingPending?.attempted" variant="warning" :message="t('payoutReviews.funding.unknown')">
              <UiButton variant="secondary" :loading="busy" @click="submitFunding">
                {{ t('payoutReviews.funding.retry') }}
              </UiButton>
            </UiAlert>
            <UiAlert v-if="pending?.attempted" variant="warning" :message="t('payoutReviews.unknown')">
              <p>{{ t('payoutReviews.retryDecision', { decision: t(`payoutReviews.${pending.input.decision}`) }) }}</p>
              <UiButton variant="secondary" :loading="busy" @click="submit">
                {{ t('payoutReviews.retry') }}
              </UiButton>
            </UiAlert>
          </template>
        </template>
      </template>
    </div>
    <UiAlertDialog v-model:open="confirmation" :title="t(pending?.input.decision === 'approved' ? 'payoutReviews.confirmApprove' : 'payoutReviews.confirmReject')" :description="t('payoutReviews.boundary')" :confirm-label="t('payoutReviews.confirm')" :cancel-label="t('payoutReviews.edit')" :destructive="pending?.input.decision === 'rejected'" :busy="busy" @confirm="submit" @cancel="cancelConfirmation">
      <dl v-if="pending" class="payout-review-facts">
        <div><dt>{{ t('payoutReviews.seller') }}</dt><dd>{{ pending.sellerId }} · {{ t(`sellerSales.env.${pending.environment}`) }}</dd></div>
        <div><dt>{{ t('sellerPayouts.requestId', { id: pending.id }) }}</dt><dd>{{ money(pending.input.amountCents) }}</dd></div>
        <div><dt>{{ t('sellerPayouts.settlement') }}</dt><dd>{{ pending.input.settlementId }}</dd></div>
        <div>
          <dt>{{ t('payoutReviews.bank') }}</dt><dd>
            <PayoutBankSummary v-if="pending.input.bankDestinationId" :bank-destination-id="pending.input.bankDestinationId" :bank-name="pending.bankName" :last4="pending.last4" /><template v-else>
              {{ t('payoutReviews.noBank') }}
            </template>
          </dd>
        </div>
        <div>
          <dt>{{ t('payoutReviews.reason') }}</dt><dd class="payout-review-reason">
            {{ pending.input.reason }}
          </dd>
        </div>
        <div>
          <dt>{{ t('payoutReviews.sellerMessage') }}</dt><dd class="payout-review-reason">
            {{ pending.input.sellerMessage }}
          </dd>
        </div>
      </dl>
    </UiAlertDialog>
    <UiAlertDialog v-model:open="fundingConfirmation" :title="t('payoutReviews.funding.confirmTitle')" :description="t('payoutReviews.funding.boundary')" :confirm-label="t('payoutReviews.funding.confirm')" :cancel-label="t('payoutReviews.edit')" :busy="busy" @confirm="submitFunding" @cancel="cancelFundingConfirmation">
      <dl v-if="fundingPending" class="payout-review-facts">
        <div><dt>{{ t('payoutReviews.seller') }}</dt><dd>{{ fundingPending.sellerId }} · {{ t(`sellerSales.env.${fundingPending.environment}`) }}</dd></div>
        <div><dt>{{ t('sellerPayouts.requestId', { id: fundingPending.id }) }}</dt><dd>{{ money(fundingPending.input.amountCents) }}</dd></div>
        <div><dt>{{ t('sellerPayouts.settlement') }}</dt><dd>{{ fundingPending.input.settlementId }}</dd></div>
        <div><dt>{{ t('payoutReviews.latest') }}</dt><dd>{{ t('payoutReviews.revision', { revision: fundingPending.input.expectedRevision }) }} · {{ fundingPending.input.reviewId }}</dd></div>
        <div><dt>{{ t('payoutReviews.bank') }}</dt><dd><PayoutBankSummary :bank-destination-id="fundingPending.input.bankDestinationId" :bank-name="fundingPending.bankName" :last4="fundingPending.last4" /></dd></div>
        <div>
          <dt>{{ t('payoutReviews.funding.reason') }}</dt><dd class="payout-review-reason">
            {{ fundingPending.input.reason }}
          </dd>
        </div>
      </dl>
    </UiAlertDialog>
  </section>
</template>

<style scoped>
.payout-reviews-content { display: grid; gap: 24px; padding-block: 24px; min-width: 0; }
.payout-review-row { display: grid; grid-template-columns: minmax(0, 1fr) 220px; gap: 20px; }
.payout-review-detail, .payout-review-history { display: grid; gap: 16px; min-width: 0; }
.payout-review-facts { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 20px; margin: 0; }
.payout-review-facts dt { color: var(--text-secondary); font-size: 12px; }
.payout-review-facts dd { margin: 6px 0 0; overflow-wrap: anywhere; font-variant-numeric: tabular-nums; }
.payout-review-history h2 { margin: 0; font-size: 15px; }
.payout-review-history p { margin: 0; color: var(--text-secondary); }
.payout-review-reason { white-space: pre-wrap; overflow-wrap: anywhere; }
.payout-review-buttons { display: flex; gap: 12px; flex-wrap: wrap; }
@media (max-width: 767px) { .payout-review-row, .payout-review-facts { grid-template-columns: minmax(0, 1fr); } }
</style>
