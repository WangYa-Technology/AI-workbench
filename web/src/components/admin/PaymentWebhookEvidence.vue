<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { APIError, messageFrom, type ProductWebhookQuarantine, type ProductWebhookQuarantineFilter } from '../../api/client'
import { useAdminApi } from '../../composables/useAdminContext'
import { formatCurrency } from '../../lib/format'
import { useSessionStore } from '../../stores/session'
import UiAlert from '../ui/UiAlert.vue'
import UiButton from '../ui/UiButton.vue'
import UiCardContent from '../ui/UiCardContent.vue'
import UiCardTag from '../ui/UiCardTag.vue'
import UiCatalog from '../ui/UiCatalog.vue'
import UiContentCard from '../ui/UiContentCard.vue'
import UiEmptyState from '../ui/UiEmptyState.vue'
import UiFilterBar from '../ui/UiFilterBar.vue'
import UiSelect from '../ui/UiSelect.vue'
import UiFormField from '../ui/UiFormField.vue'
import UiTextarea from '../ui/UiTextarea.vue'

const emit = defineEmits<{ updated: [] }>()
const { t, locale } = useI18n()
const session = useSessionStore()
const api = useAdminApi()
const allowed = computed(() => session.initialized && Boolean(session.user?.permissions.includes('admin:finance')))
const state = ref<NonNullable<ProductWebhookQuarantineFilter['state']>>('pending')
const mode = ref<NonNullable<ProductWebhookQuarantineFilter['mode']> | ''>('')
const provider = ref<NonNullable<ProductWebhookQuarantineFilter['provider']> | ''>('')
const items = ref<ProductWebhookQuarantine[]>([])
const cursor = ref<string>()
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const outcome = ref('')
const selected = ref<ProductWebhookQuarantine>()
const reason = ref('')
const reasonLength = computed(() => Array.from(reason.value.trim()).length)
let generation = 0
let actionVersion = 0
function clear() {
  ++generation; ++actionVersion
  items.value = []; cursor.value = undefined; selected.value = undefined
  reason.value = ''; outcome.value = ''; error.value = ''
  loading.value = busy.value = false
}
function fail(failure: unknown) {
  if (failure instanceof APIError && [401, 403, 404].includes(failure.status)) clear()
  error.value = messageFrom(failure)
}

async function load(more = false) {
  if (more && (loading.value || !cursor.value)) return
  const current = more ? generation : ++generation
  if (!more) { items.value = []; cursor.value = undefined; selected.value = undefined }
  error.value = ''
  loading.value = false
  if (!allowed.value) return
  loading.value = true
  try {
    const page = await api.adminWebhookQuarantines({ state: state.value, mode: mode.value || undefined, provider: provider.value || undefined, cursor: more ? cursor.value : undefined })
    if (current !== generation) return
    const merged = more ? [...items.value, ...page.items] : page.items
    items.value = [...new Map(merged.map(item => [item.id, item])).values()]
    cursor.value = page.nextCursor
  } catch (failure) { if (current === generation) fail(failure) }
  finally { if (current === generation) loading.value = false }
}
function select(item: ProductWebhookQuarantine) {
  if (!allowed.value || busy.value || loading.value || item.state !== 'pending') return
  selected.value = item; reason.value = ''; error.value = ''; outcome.value = ''
}
async function recheck() {
  const item = selected.value
  if (!allowed.value || !item || busy.value || loading.value || reasonLength.value < 10 || reasonLength.value > 1000) return
  const current = generation
  const action = ++actionVersion
  busy.value = true
  error.value = ''
  try {
    const result = await api.adminRecheckWebhook(item.id, { expectedVersion: item.version, reason: reason.value.trim() })
    if (action !== actionVersion || current !== generation) return
    outcome.value = t(`webhookEvidence.results.${result.state}`)
    // The command is confirmed even if the evidence refresh subsequently fails.
    emit('updated')
    await load()
  } catch (failure) { if (action === actionVersion) fail(failure) }
  finally { if (action === actionVersion) busy.value = false }
}
watch([state, mode, provider, allowed, () => session.user?.id], () => { clear(); void load() }, { immediate: true, flush: 'sync' })
onBeforeUnmount(clear)
</script>

<template>
  <section v-if="allowed" class="admin-finance-section webhook-evidence" :aria-label="t('webhookEvidence.title')" :aria-busy="loading || busy">
    <header><div><h2>{{ t('webhookEvidence.title') }}</h2><p>{{ t('webhookEvidence.summary') }}</p></div></header>
    <UiFilterBar as="div" fields layout="grid" class="webhook-evidence__filters">
      <UiFormField :label="t('webhookEvidence.state')">
        <UiSelect v-model="state" :disabled="busy">
          <option v-for="value in ['pending', 'admitted', 'all']" :key="value" :value="value">
            {{ t(`webhookEvidence.states.${value}`) }}
          </option>
        </UiSelect>
      </UiFormField>
      <UiFormField :label="t('admin.paymentMode')">
        <UiSelect v-model="mode" :disabled="busy">
          <option value="">
            {{ t('admin.allPaymentModes') }}
          </option><option v-for="value in ['live', 'test']" :key="value" :value="value">
            {{ t(`admin.paymentModes.${value}`) }}
          </option>
        </UiSelect>
      </UiFormField>
      <UiFormField :label="t('webhookEvidence.provider')">
        <UiSelect v-model="provider" :disabled="busy">
          <option value="">
            {{ t('webhookEvidence.allProviders') }}
          </option><option value="stripe">
            {{ t('admin.paymentGatewayTabs.stripe') }}
          </option><option value="waffo_pancake">
            {{ t('admin.paymentGatewayTabs.waffo') }}
          </option>
        </UiSelect>
      </UiFormField>
      <UiButton variant="secondary" :disabled="loading || busy" @click="load()">
        {{ t('webhookEvidence.refresh') }}
      </UiButton>
    </UiFilterBar>
    <UiAlert v-if="error" variant="danger" role="alert">
      {{ error }}
    </UiAlert>
    <UiAlert v-if="outcome" role="status">
      {{ outcome }}
    </UiAlert>
    <p v-if="loading" role="status">
      {{ t('webhookEvidence.loading') }}
    </p>
    <UiCatalog v-if="items.length">
      <UiContentCard v-for="item in items" :key="item.id" class="webhook-evidence__card">
        <UiCardContent :title="`${item.provider === 'stripe' ? 'Stripe' : 'Waffo'} · ${item.eventType}`" :summary="item.state === 'admitted' ? t('webhookEvidence.admittedSummary') : t(`webhookEvidence.codes.${item.lastErrorCode || item.rejectionCode}`)">
          <template #tags>
            <UiCardTag :variant="item.state === 'pending' ? 'warning' : 'neutral'">
              {{ t(`webhookEvidence.states.${item.state}`) }}
            </UiCardTag><UiCardTag variant="neutral">
              {{ t(`admin.paymentModes.${item.liveMode ? 'live' : 'test'}`) }}
            </UiCardTag>
          </template>
          <template #meta>
            <span>{{ item.providerEventId }}</span><time :datetime="item.receivedAt">{{ new Date(item.receivedAt).toLocaleString(locale) }}</time><span v-if="item.amountCents != null && item.currency">{{ formatCurrency(item.amountCents, item.currency, locale) }}</span>
          </template>
        </UiCardContent>
        <p v-if="item.claimedPaymentId" class="webhook-evidence__identity">
          {{ t('webhookEvidence.claimedPayment') }} <code>{{ item.claimedPaymentId }}</code>
        </p>
        <p v-if="item.hasReviewHold" class="webhook-evidence__identity">
          {{ t('webhookEvidence.matchedRemote') }}
        </p>
        <UiButton v-if="item.state === 'pending' && selected?.id !== item.id" variant="secondary" :disabled="busy || loading" @click="select(item)">
          {{ t('webhookEvidence.recheck') }}
        </UiButton>
        <form v-if="selected?.id === item.id" class="webhook-evidence__form" @submit.prevent="recheck">
          <p>{{ t('webhookEvidence.recheckSummary') }}</p>
          <UiFormField :label="t('webhookEvidence.reason')" :hint="t('webhookEvidence.reasonHint')" required>
            <UiTextarea v-model="reason" :disabled="busy" :maxlength="2000" required />
          </UiFormField>
          <div class="webhook-evidence__actions">
            <UiButton type="submit" :loading="busy" :disabled="loading || reasonLength < 10 || reasonLength > 1000">
              {{ t('webhookEvidence.confirm') }}
            </UiButton><UiButton variant="secondary" :disabled="busy" @click="selected = undefined">
              {{ t('actions.cancel') }}
            </UiButton>
          </div>
        </form>
      </UiContentCard>
    </UiCatalog>
    <UiEmptyState v-else-if="!loading && !error" density="compact" :title="t('webhookEvidence.empty')" :message="t('webhookEvidence.emptySummary')" />
    <UiButton v-if="cursor" variant="secondary" :disabled="busy" :loading="loading" @click="load(true)">
      {{ t('webhookEvidence.more') }}
    </UiButton>
  </section>
</template>

<style scoped>
.webhook-evidence { display: grid; gap: 20px; }
.webhook-evidence__filters { grid-template-columns: repeat(3, minmax(0, 1fr)) auto; }
@media (max-width: 767px) { .webhook-evidence__filters { grid-template-columns: minmax(0, 1fr); } }
.webhook-evidence__card { display: grid; gap: 12px; padding: 20px; }
.webhook-evidence__identity { margin: 0; color: var(--text-secondary); overflow-wrap: anywhere; }
.webhook-evidence__form { display: grid; gap: 16px; }
.webhook-evidence__actions { display: flex; flex-wrap: wrap; gap: 12px; }
.webhook-evidence__card :deep(.ui-card-content__summary) { display: block; }
</style>
