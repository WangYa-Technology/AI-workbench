<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, APIError, messageFrom, type Order } from '../../api/client'
import { useSessionStore } from '../../stores/session'
import UiAlertDialog from '../ui/UiAlertDialog.vue'
import UiButton from '../ui/UiButton.vue'
import UiCardActions from '../ui/UiCardActions.vue'

const props = defineProps<{ order: Order }>()
const emit = defineEmits<{ updated: [order: Order] }>()
const { t } = useI18n()
const session = useSessionStore()
const open = ref(false)
const busy = ref(false)
const error = ref('')
const attemptedVersion = ref<number>()
let generation = 0
const closed = computed(() => props.order.status === 'cancelled' && props.order.checkoutClosedBeforePayment)
const reconciliation = computed(() => props.order.checkoutReconciliationRequired === true)
const eligible = computed(() => session.user && !reconciliation.value && props.order.canCloseCheckout && Number.isSafeInteger(props.order.paymentVersion) && (props.order.paymentVersion ?? 0) > 0)
const phase = computed(() => reconciliation.value ? 'reconciliation' : closed.value ? 'closedBeforePayment' : error.value ? 'unavailable' : 'closure')
const description = computed(() => error.value && !reconciliation.value && !closed.value ? 'closureUncertain' : `${phase.value}Body`)

function confirm() {
  if (!eligible.value || busy.value) return
  attemptedVersion.value = props.order.paymentVersion
  open.value = true
}

async function refresh() {
  if (busy.value || !session.user) return
  const current = generation
  const { id, paymentId } = props.order
  busy.value = true
  try {
    const updated = await api.getOrder(id)
    if (current !== generation) return
    if (updated.id !== id || updated.paymentId !== paymentId) throw new Error(t('workspace.productPayment.unavailableBody'))
    error.value = ''
    if (!updated.canCloseCheckout) attemptedVersion.value = undefined
    emit('updated', updated)
  } catch (reason) {
    if (current === generation) error.value = messageFrom(reason)
  } finally {
    if (current === generation) busy.value = false
  }
}

async function closeOrder() {
  if (busy.value || !session.user || attemptedVersion.value === undefined) return
  const current = generation
  const { id, paymentId } = props.order
  busy.value = true
  error.value = ''
  try {
    const updated = await api.closeProductCheckout(id, attemptedVersion.value)
    if (current !== generation) return
    if (updated.id !== id || updated.paymentId !== paymentId) throw new Error(t('workspace.productPayment.unavailableBody'))
    attemptedVersion.value = undefined
    // Use the accepted command response directly; a follow-up refresh cannot
    // turn a successful closure into an apparent failed command.
    emit('updated', updated)
  } catch (reason) {
    if (current !== generation) return
    error.value = messageFrom(reason)
    if (reason instanceof APIError && [401, 403, 404, 409, 422].includes(reason.status)) attemptedVersion.value = undefined
  } finally {
    if (current === generation) { busy.value = false; open.value = false }
  }
}

watch(() => [props.order.id, props.order.paymentId, session.user?.id], () => {
  generation++
  open.value = false
  busy.value = false
  error.value = ''
  attemptedVersion.value = undefined
})
onBeforeUnmount(() => { generation++ })
</script>

<template>
  <section v-if="eligible || closed || reconciliation || error" class="product-checkout-closure" :aria-label="t(`workspace.productPayment.${phase}Title`)">
    <UiCardActions :value="t(`workspace.productPayment.${phase}Title`)" :description="t(`workspace.productPayment.${description}`)">
      <UiButton v-if="reconciliation" as="RouterLink" variant="secondary" to="/support">
        {{ t('workspace.productPayment.support') }}
      </UiButton>
      <UiButton v-else-if="closed" as="RouterLink" variant="secondary" :to="`/market/assets/${order.productId}`">
        {{ t('workspace.productPayment.reviewProduct') }}
      </UiButton>
      <UiButton v-else-if="error && attemptedVersion !== undefined" variant="secondary" :loading="busy" @click="closeOrder">
        {{ t('workspace.productPayment.closeRetry') }}
      </UiButton>
      <UiButton v-else-if="eligible && !error" variant="secondary" :loading="busy" @click="confirm">
        {{ t('workspace.productPayment.closeCheckout') }}
      </UiButton>
      <UiButton v-if="error || reconciliation" variant="ghost" :disabled="busy" @click="refresh">
        {{ t('workspace.productPayment.refresh') }}
      </UiButton>
    </UiCardActions>
    <p v-if="error" class="form-error" role="alert">
      {{ error }}
    </p>
    <UiAlertDialog v-model:open="open" :title="t('workspace.productPayment.closeConfirmation')" :description="t('workspace.productPayment.closeConfirmationBody')" :confirm-label="t('workspace.productPayment.closeCheckout')" :cancel-label="t('actions.cancel')" :busy="busy" @confirm="closeOrder" />
  </section>
</template>
