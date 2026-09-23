<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, APIError, messageFrom, type SellerBankPayoutInput, type SellerBankPayoutResumeInput, type SellerBankPayoutOperation, type SellerPayoutReviewItem } from '../../api/client'
import { createScopedApi } from '../../lib/scopedApi'
import { formatCurrency, formatDateTime } from '../../lib/format'
import { textWithin } from '../../lib/unicodeText'
import { useSessionStore } from '../../stores/session'
import PayoutBankSummary from './PayoutBankSummary.vue'
import UiAlert from '../ui/UiAlert.vue'
import UiAlertDialog from '../ui/UiAlertDialog.vue'
import UiButton from '../ui/UiButton.vue'
import UiCardContent from '../ui/UiCardContent.vue'
import UiToolbar from '../ui/UiToolbar.vue'
import UiCardTag from '../ui/UiCardTag.vue'
import UiForm from '../ui/UiForm.vue'
import UiFormField from '../ui/UiFormField.vue'
import UiTextarea from '../ui/UiTextarea.vue'

const props = defineProps<{ requestId: string; blocked?: boolean }>()
const emit = defineEmits<{ updated: [request: SellerPayoutReviewItem]; changed: []; busy: [value: boolean]; pending: [value: boolean]; denied: [error: APIError] }>()
const { t, te, locale } = useI18n()
const session = useSessionStore()
const allowed = computed(() => session.initialized && session.user?.status === 'active' && session.user.permissions.includes('admin:finance'))
const operation = ref<SellerBankPayoutOperation>()
type PendingBankCommand = { id: string; request: SellerPayoutReviewItem; attempted: boolean } & (
  { kind: 'submit'; input: SellerBankPayoutInput } | { kind: 'resume'; input: SellerBankPayoutResumeInput }
)
const pending = ref<PendingBankCommand>()
const reason = ref(''), error = ref(''), success = ref('')
const loading = ref(false), busy = ref(false), confirmation = ref(false), denied = ref(false)
let generation = 0
const money = (cents: number) => formatCurrency(cents, 'USD', locale.value)
const date = (value: string) => formatDateTime(value, locale.value, session.user?.timezone || 'UTC')
const actionable = computed(() => allowed.value && !denied.value && !props.blocked && (operation.value?.canSubmit === true || operation.value?.canResume === true) && operation.value?.request.id === props.requestId && operation.value.request.sellerId !== session.user?.id)
const resuming = computed(() => pending.value ? pending.value.kind === 'resume' : operation.value?.canResume === true)
const currentJobStatus = computed(() => operation.value?.bank?.resume?.jobStatus || operation.value?.bank?.jobStatus)
const validReason = computed(() => textWithin(reason.value, 10, 1000) && !reason.value.includes('\0'))
const providerStatus = computed(() => {
  const value = operation.value?.bank?.providerStatus
  return value && te(`bankPayout.status.${value}`) ? t(`bankPayout.status.${value}`) : t('bankPayout.unconfirmed')
})
function clear() { operation.value = undefined; pending.value = undefined; emit('pending', false); reason.value = ''; confirmation.value = false; success.value = '' }
function scoped(current: number) {
  return createScopedApi(api, () => current === generation && allowed.value && !denied.value, cause => {
    if (cause instanceof APIError && [401, 403, 404].includes(cause.status)) {
      generation++; clear(); denied.value = true; error.value = messageFrom(cause)
      loading.value = false; busy.value = false; emit('busy', false)
      emit('denied', cause)
    }
  })
}
async function load() {
  if (!allowed.value || busy.value || pending.value?.attempted) return
  const current = ++generation
  clear(); denied.value = false; error.value = ''; loading.value = true
  try {
    operation.value = await scoped(current).getSellerBankPayoutOperation(props.requestId)
    emit('updated', operation.value.request)
  }
  catch (cause) { if (current === generation) error.value = messageFrom(cause) }
  finally { if (current === generation) loading.value = false }
}
function prepare() {
  const request = operation.value?.request
  if (!request?.funding || !request.latestReview || !actionable.value || !validReason.value || busy.value || loading.value || pending.value) return
  const snapshot = JSON.parse(JSON.stringify(request)) as SellerPayoutReviewItem
  if (operation.value?.canResume) {
    const bank = operation.value.bank
    const predecessor = bank?.resume?.jobId || bank?.jobId
    if (!bank || !predecessor) return
    pending.value = { kind: 'resume', id: props.requestId, request: snapshot, attempted: false, input: {
      commandId: bank.commandId, expectedJobId: predecessor, reason: reason.value.trim(), confirmed: true,
    } }
  } else pending.value = { kind: 'submit', id: props.requestId, request: snapshot, attempted: false, input: {
    sourceTransferId: request.funding.transferId, reviewId: request.latestReview.id, expectedRevision: request.latestReview.revision,
    amountCents: request.amountCents, bankDestinationId: request.bankDestinationId, reason: reason.value.trim(), confirmed: true,
  } }
  emit('pending', true)
  confirmation.value = true
}
function cancel() { if (!busy.value && !pending.value?.attempted) { pending.value = undefined; emit('pending', false) } }
async function submit() {
  const command = pending.value
  if (!command || command.id !== props.requestId || !allowed.value || denied.value || props.blocked || busy.value || loading.value) return
  const current = generation
  command.attempted = true; busy.value = true; emit('busy', true); confirmation.value = false; error.value = ''; success.value = ''
  try {
    const result = command.kind === 'resume'
      ? await scoped(current).resumeSellerBankPayout(command.id, command.input)
      : await scoped(current).submitSellerBankPayout(command.id, command.input)
    operation.value = result.operation; pending.value = undefined; emit('pending', false); reason.value = ''
    emit('updated', result.operation.request)
    emit('changed')
    success.value = t(command.kind === 'resume'
      ? (result.replayed ? 'bankPayout.resumeRecovered' : 'bankPayout.resumeSaved')
      : (result.replayed ? 'bankPayout.recovered' : 'bankPayout.saved'))
  } catch (cause) {
    if (current !== generation) return
    error.value = messageFrom(cause)
    if (cause instanceof APIError && [409, 422].includes(cause.status)) {
      pending.value = undefined; emit('pending', false); operation.value = undefined; reason.value = ''
      try {
        operation.value = await scoped(current).getSellerBankPayoutOperation(command.id)
        emit('updated', operation.value.request)
      }
      catch (readFailure) { if (current === generation) error.value = `${error.value} ${messageFrom(readFailure)}` }
    }
  } finally { if (current === generation) { busy.value = false; emit('busy', false) } }
}
watch([() => props.requestId, () => session.user, () => session.initialized], () => {
  generation++; clear(); busy.value = false; loading.value = false; denied.value = false; error.value = ''; emit('busy', false)
  void load()
}, { immediate: true, deep: true, flush: 'sync' })
onBeforeUnmount(() => { generation++; emit('busy', false); emit('pending', false) })
</script>

<template>
  <section v-if="allowed" class="bank-payout-panel" :aria-label="t('bankPayout.title')">
    <UiCardContent :title="t('bankPayout.title')" :summary="t('bankPayout.boundary')">
      <template v-if="operation?.bank" #tags>
        <UiCardTag>{{ providerStatus }}</UiCardTag>
      </template>
      <template v-if="operation?.bank" #meta>
        <span>{{ t('bankPayout.command', { id: operation.bank.commandId }) }}</span>
        <span v-if="operation.bank.jobStatus">{{ t('bankPayout.job') }} · {{ t(`payoutReviews.funding.jobs.${operation.bank.jobStatus}`) }}</span>
        <span v-if="operation.bank.resume">{{ t('bankPayout.continuation', { revision: operation.bank.resume.revision }) }} · {{ t(`payoutReviews.funding.jobs.${operation.bank.resume.jobStatus}`) }}</span>
        <span v-if="operation.bank.checkedAt">{{ t('bankPayout.checked', { date: date(operation.bank.checkedAt) }) }}</span>
      </template>
    </UiCardContent>
    <p v-if="loading" role="status">
      {{ t('sellerPayouts.loading') }}
    </p>
    <UiAlert v-if="error" variant="danger" role="alert" :message="error" />
    <UiAlert v-if="success" variant="success" role="status" :message="success" />
    <template v-if="!denied">
      <UiAlert v-if="operation?.bank?.requiresReview" variant="warning" :message="t('bankPayout.reviewNeeded')" />
      <UiAlert v-if="operation?.bank && !operation.bank.startedAt && ['failed', 'cancelled', 'succeeded'].includes(currentJobStatus || '')" variant="warning" :message="t(operation.canResume ? 'bankPayout.resumable' : 'bankPayout.stopped')" />
      <UiForm v-if="actionable || pending" :disabled="busy || !!pending || !!blocked" @submit="prepare">
        <UiFormField :label="t('bankPayout.reason')" :hint="t(resuming ? 'bankPayout.resumeReasonHelp' : 'bankPayout.reasonHelp')">
          <UiTextarea v-model="reason" :aria-label="t('bankPayout.reason')" rows="3" required />
        </UiFormField>
        <UiButton type="submit" :disabled="!validReason">
          {{ t(resuming ? 'bankPayout.resumePrepare' : 'bankPayout.prepare') }}
        </UiButton>
      </UiForm>
      <p v-else-if="operation && !operation.bank">
        {{ t('bankPayout.unavailable') }}
      </p>
      <UiAlert v-if="pending?.attempted" variant="warning" :message="t('bankPayout.unknown')" />
      <UiToolbar :label="t('bankPayout.title')">
        <UiButton v-if="pending?.attempted" :disabled="busy || blocked" @click="submit">
          {{ t('bankPayout.retry') }}
        </UiButton>
        <UiButton v-else variant="secondary" :disabled="busy || loading || !!pending || blocked" @click="load">
          {{ t('bankPayout.refresh') }}
        </UiButton>
      </UiToolbar>
    </template>
    <UiAlertDialog v-model:open="confirmation" :title="t(resuming ? 'bankPayout.resumeTitle' : 'bankPayout.confirmTitle')" :description="t(resuming ? 'bankPayout.resumeHelp' : 'bankPayout.confirmHelp')" :confirm-label="t(resuming ? 'bankPayout.resumeConfirm' : 'bankPayout.confirm')" :cancel-label="t('payoutReviews.edit')" :busy="busy" @confirm="submit" @cancel="cancel">
      <div v-if="pending" class="bank-payout-confirmation">
        <strong>{{ money(pending.request.amountCents) }} · {{ t(`sellerSales.env.${pending.request.environment}`) }}</strong>
        <p>{{ t('payoutReviews.seller') }} · {{ pending.request.sellerId }}</p>
        <p>{{ t('sellerPayouts.requestId', { id: pending.id }) }}</p>
        <template v-if="pending.kind === 'submit'">
          <p>{{ t('payoutReviews.funding.transferId') }} · {{ pending.input.sourceTransferId }}</p>
          <p>{{ t('payoutReviews.revision', { revision: pending.input.expectedRevision }) }} · {{ pending.input.reviewId }}</p>
        </template>
        <template v-else>
          <p>{{ t('bankPayout.command', { id: pending.input.commandId }) }}</p>
          <p>{{ t('bankPayout.predecessor', { id: pending.input.expectedJobId }) }}</p>
        </template>
        <PayoutBankSummary :bank-destination-id="pending.request.bankDestinationId" :bank-name="pending.request.bankName" :last4="pending.request.last4" />
        <p>{{ pending.input.reason }}</p>
      </div>
    </UiAlertDialog>
  </section>
</template>

<style scoped>
.bank-payout-panel { display: grid; gap: 16px; min-width: 0; }
.bank-payout-panel p, .bank-payout-confirmation p { margin: 0; overflow-wrap: anywhere; white-space: pre-wrap; }
.bank-payout-confirmation { display: grid; gap: 12px; }
</style>
