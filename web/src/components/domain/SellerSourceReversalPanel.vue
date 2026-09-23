<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, APIError, messageFrom, type SellerSourceReversalInput, type SellerSourceClosureInput, type SellerSourceReversalOperation, type SellerPayoutReviewItem } from '../../api/client'
import { createScopedApi } from '../../lib/scopedApi'
import { formatCurrency, formatDateTime } from '../../lib/format'
import { textWithin } from '../../lib/unicodeText'
import { useSessionStore } from '../../stores/session'
import UiAlert from '../ui/UiAlert.vue'
import UiAlertDialog from '../ui/UiAlertDialog.vue'
import UiButton from '../ui/UiButton.vue'
import UiCardContent from '../ui/UiCardContent.vue'
import UiCardTag from '../ui/UiCardTag.vue'
import UiForm from '../ui/UiForm.vue'
import UiFormField from '../ui/UiFormField.vue'
import UiTextarea from '../ui/UiTextarea.vue'
import UiToolbar from '../ui/UiToolbar.vue'

const props = defineProps<{ requestId: string; blocked?: boolean }>()
const emit = defineEmits<{ updated: [request: SellerPayoutReviewItem]; changed: []; busy: [value: boolean]; pending: [value: boolean]; denied: [error: APIError] }>()
const { t, locale } = useI18n()
const session = useSessionStore()
const allowed = computed(() => session.initialized && session.user?.status === 'active' && session.user.permissions.includes('admin:finance'))
const operation = ref<SellerSourceReversalOperation>()
type Pending = { requestId: string; snapshot: SellerSourceReversalOperation; attempted: boolean } & (
  { kind: 'return'; id: string; input: SellerSourceReversalInput } | { kind: 'close'; id: string; input: SellerSourceClosureInput }
)
const pending = ref<Pending>()
const reason = ref(''), error = ref(''), success = ref('')
const loading = ref(false), busy = ref(false), confirmation = ref(false), denied = ref(false)
let generation = 0
const money = (value: number) => formatCurrency(value, 'USD', locale.value)
const date = (value: string) => formatDateTime(value, locale.value, session.user?.timezone || 'UTC')
const actionable = computed(() => allowed.value && !denied.value && !props.blocked && operation.value?.request.id === props.requestId && operation.value.request.sellerId !== session.user?.id && (operation.value.canSubmit || operation.value.canClose))
const closing = computed(() => pending.value ? pending.value.kind === 'close' : operation.value?.canClose === true)
const validReason = computed(() => textWithin(reason.value, 10, 1000) && !reason.value.includes('\0'))
const bankLabels = { not_reserved: 'notReserved', not_started: 'notStarted', failed: 'failed', returned: 'returned' } as const
function clear() { operation.value = undefined; pending.value = undefined; emit('pending', false); reason.value = ''; confirmation.value = false; success.value = '' }
function scoped(current: number) {
  return createScopedApi(api, () => current === generation && allowed.value && !denied.value, cause => {
    if (cause instanceof APIError && [401, 403, 404].includes(cause.status)) {
      generation++; clear(); denied.value = true; error.value = messageFrom(cause)
      loading.value = false; busy.value = false; emit('busy', false); emit('denied', cause)
    }
  })
}
async function read(current: number, updateRequest = false) {
  operation.value = await scoped(current).getSellerSourceReversalOperation(props.requestId)
  if (updateRequest) emit('updated', operation.value.request)
}
async function load() {
  if (!allowed.value || busy.value || props.blocked || pending.value) return
  const current = ++generation
  clear(); denied.value = false; error.value = ''; loading.value = true
  try { await read(current) }
  catch (cause) { if (current === generation) error.value = messageFrom(cause) }
  finally { if (current === generation) loading.value = false }
}
function prepare() {
  const op = operation.value
  if (!op || !actionable.value || !validReason.value || busy.value || loading.value || pending.value) return
  const snapshot = JSON.parse(JSON.stringify(op)) as SellerSourceReversalOperation
  if (op.canClose && op.command && op.acceptedReadId) pending.value = { kind: 'close', id: op.command.id, requestId: props.requestId, snapshot, attempted: false, input: {
    readId: op.acceptedReadId, expectedUpdatedAt: op.expectedUpdatedAt, reason: reason.value.trim(), confirmed: true,
  } }
  else if (op.canSubmit && op.request.funding && op.bank) pending.value = { kind: 'return', id: props.requestId, requestId: props.requestId, snapshot, attempted: false, input: {
    sourceTransferId: op.request.funding.transferId, expectedUpdatedAt: op.expectedUpdatedAt,
    bankCommandId: op.bank.commandId ?? null, bankResultId: op.bank.resultId ?? null, reason: reason.value.trim(), confirmed: true,
  } }
  else return
  emit('pending', true); confirmation.value = true
}
function cancel() { if (!busy.value && !pending.value?.attempted) { pending.value = undefined; emit('pending', false) } }
async function submit() {
  const command = pending.value
  if (!command || command.requestId !== props.requestId || !allowed.value || denied.value || props.blocked || busy.value || loading.value) return
  const current = generation
  command.attempted = true; busy.value = true; emit('busy', true); confirmation.value = false; error.value = ''; success.value = ''
  try {
    if (command.kind === 'close') await scoped(current).closeSellerSourceReversal(command.id, command.input)
    else await scoped(current).submitSellerSourceReversal(command.id, command.input)
    pending.value = undefined; emit('pending', false); reason.value = ''; operation.value = undefined
    success.value = t(command.kind === 'close' ? 'sourceReversal.closed' : 'sourceReversal.saved')
    emit('changed')
    // The command is known accepted. A subsequent GET failure must not
    // reintroduce an unknown POST or offer to submit it again.
    try { await read(current, true) }
    catch (cause) { if (current === generation) error.value = messageFrom(cause) }
  } catch (cause) {
    if (current !== generation) return
    error.value = messageFrom(cause)
    if (cause instanceof APIError && [409, 422].includes(cause.status)) {
      pending.value = undefined; emit('pending', false); reason.value = ''; operation.value = undefined
      try { await read(current) }
      catch (readFailure) { if (current === generation) error.value = `${error.value} ${messageFrom(readFailure)}` }
    }
  } finally { if (current === generation) { busy.value = false; emit('busy', false) } }
}
watch([() => props.requestId, () => session.user, () => session.initialized], () => {
  generation++; clear(); busy.value = false; loading.value = false; denied.value = false; error.value = ''; emit('busy', false)
  void load()
}, { immediate: true, deep: true, flush: 'sync' })
watch(() => props.blocked, blocked => {
  if (!blocked && !operation.value && !loading.value && !pending.value && !denied.value) void load()
})
onBeforeUnmount(() => { generation++; emit('busy', false); emit('pending', false) })
</script>

<template>
  <section v-if="allowed" class="source-reversal-panel" :aria-label="t('sourceReversal.title')">
    <UiCardContent :title="t('sourceReversal.title')" :summary="t('sourceReversal.boundary')">
      <template v-if="operation?.command" #tags>
        <UiCardTag>{{ t(`sourceReversal.${operation.closure?.resolution || (operation.canClose ? 'ready' : 'pending')}`) }}</UiCardTag>
      </template>
      <template v-if="operation" #meta>
        <span v-if="operation.bank">{{ t(`sourceReversal.${bankLabels[operation.bank.disposition]}`) }}</span>
        <span v-if="operation.command">{{ t('sourceReversal.command', { id: operation.command.id }) }}</span>
        <span v-if="operation.jobStatus">{{ t('bankPayout.job') }} · {{ t(`payoutReviews.funding.jobs.${operation.jobStatus}`) }}</span>
        <span v-if="operation.latestRead?.finishedAt">{{ t('sourceReversal.checked', { date: date(operation.latestRead.finishedAt) }) }}</span>
        <span v-if="operation.acceptedReadId">{{ t('sourceReversal.receipt', { id: operation.acceptedReadId }) }}</span>
      </template>
    </UiCardContent>
    <p v-if="loading" role="status">
      {{ t('sellerPayouts.loading') }}
    </p>
    <UiAlert v-if="error" variant="danger" role="alert" :message="error" />
    <UiAlert v-if="success" variant="success" role="status" :message="success" />
    <template v-if="!denied">
      <UiAlert v-if="operation?.requiresReview" variant="warning" :message="t('sourceReversal.review')" />
      <UiAlert v-if="operation?.command && !operation.startedAt && ['failed', 'cancelled', 'succeeded'].includes(operation.jobStatus || '')" variant="warning" :message="t('sourceReversal.stopped')" />
      <UiForm v-if="actionable || pending" :disabled="busy || !!pending || !!blocked" @submit="prepare">
        <UiFormField :label="t('sourceReversal.reason')" :hint="t('sourceReversal.reasonHelp')">
          <UiTextarea v-model="reason" :aria-label="t('sourceReversal.reason')" rows="3" required />
        </UiFormField>
        <UiButton type="submit" :disabled="!validReason">
          {{ t(closing ? 'sourceReversal.closePrepare' : 'sourceReversal.prepare') }}
        </UiButton>
      </UiForm>
      <p v-else-if="operation && !operation.closure">
        {{ t('sourceReversal.unavailable') }}
      </p>
      <UiAlert v-if="pending?.attempted" variant="warning" :message="t('sourceReversal.unknown')" />
      <UiToolbar :label="t('sourceReversal.title')">
        <UiButton v-if="pending?.attempted" :disabled="busy || blocked" @click="submit">
          {{ t('sourceReversal.retry') }}
        </UiButton>
        <UiButton v-else variant="secondary" :disabled="busy || loading || !!pending || blocked" @click="load">
          {{ t('sourceReversal.refresh') }}
        </UiButton>
      </UiToolbar>
    </template>
    <UiAlertDialog v-model:open="confirmation" :title="t(closing ? 'sourceReversal.closeTitle' : 'sourceReversal.confirmTitle')" :description="t(closing ? 'sourceReversal.closeHelp' : 'sourceReversal.confirmHelp')" :confirm-label="t(closing ? 'sourceReversal.closeConfirm' : 'sourceReversal.confirm')" :cancel-label="t('payoutReviews.edit')" :busy="busy" @confirm="submit" @cancel="cancel">
      <div v-if="pending" class="source-reversal-confirmation">
        <strong>{{ money(pending.snapshot.request.amountCents) }} · {{ t(`sellerSales.env.${pending.snapshot.request.environment}`) }}</strong>
        <p>{{ t('payoutReviews.seller') }} · {{ pending.snapshot.request.sellerId }}</p>
        <p>{{ t('sellerPayouts.requestId', { id: pending.requestId }) }}</p>
        <p>{{ t('payoutReviews.funding.transferId') }} · {{ pending.snapshot.request.funding?.transferId }}</p>
        <p v-if="pending.snapshot.bank">
          {{ t(`sourceReversal.${bankLabels[pending.snapshot.bank.disposition]}`) }} · {{ pending.snapshot.bank.commandId }} · {{ pending.snapshot.bank.resultId }}
        </p>
        <p>{{ t('sourceReversal.version', { date: date(pending.input.expectedUpdatedAt) }) }}</p>
        <template v-if="pending.kind === 'close'">
          <p>{{ t('sourceReversal.command', { id: pending.id }) }}</p>
          <p>{{ t('sourceReversal.receipt', { id: pending.input.readId }) }}</p>
          <strong v-if="pending.snapshot.closeResolution">{{ t(`sourceReversal.${pending.snapshot.closeResolution}`) }}</strong>
        </template>
        <p>{{ pending.input.reason }}</p>
      </div>
    </UiAlertDialog>
  </section>
</template>

<style scoped>
.source-reversal-panel { display: grid; gap: 16px; min-width: 0; }
.source-reversal-panel p, .source-reversal-confirmation p { margin: 0; overflow-wrap: anywhere; white-space: pre-wrap; }
.source-reversal-confirmation { display: grid; gap: 12px; }
</style>
