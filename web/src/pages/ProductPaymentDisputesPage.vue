<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { api, APIError, messageFrom, type ProductPaymentDispute, type ProductPaymentDisputeCommand, type ProductPaymentDisputeDetail, type ProductPaymentDisputeQuery } from '../api/client'
import { createScopedApi } from '../lib/scopedApi'
import { textWithin } from '../lib/unicodeText'
import { formatCurrency, formatDateTime } from '../lib/format'
import { useSessionStore } from '../stores/session'
import AuthRequiredState from '../components/domain/AuthRequiredState.vue'
import UiFilterBar from '../components/ui/UiFilterBar.vue'
import { maskDisputeReference } from '../lib/productDisputes'
import PageHeader from '../components/ui/PageHeader.vue'
import UiAlert from '../components/ui/UiAlert.vue'
import UiAlertDialog from '../components/ui/UiAlertDialog.vue'
import UiButton from '../components/ui/UiButton.vue'
import UiCatalog from '../components/ui/UiCatalog.vue'
import UiContentCard from '../components/ui/UiContentCard.vue'
import UiCardContent from '../components/ui/UiCardContent.vue'
import UiCardActions from '../components/ui/UiCardActions.vue'
import UiCardTag from '../components/ui/UiCardTag.vue'
import UiEmptyState from '../components/ui/UiEmptyState.vue'
import UiForm from '../components/ui/UiForm.vue'
import UiFormField from '../components/ui/UiFormField.vue'
import UiInput from '../components/ui/UiInput.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiTextarea from '../components/ui/UiTextarea.vue'

const { t, te, locale } = useI18n()
const session = useSessionStore()
const route = useRoute()
const id = computed(() => String(route.params.id || ''))
const allowed = computed(() => session.initialized && !session.loading && session.user?.status === 'active' && session.user.permissions.includes('admin:finance'))
const items = ref<ProductPaymentDispute[]>([])
const detail = ref<ProductPaymentDisputeDetail>()
const cursor = ref<string>()
const query = ref('')
const actionStatus = ref<NonNullable<ProductPaymentDisputeQuery['actionStatus']> | ''>('')
const reviewStatus = ref<NonNullable<ProductPaymentDisputeQuery['reviewStatus']> | ''>('')
const binding = ref<NonNullable<ProductPaymentDisputeQuery['binding']> | ''>('')
const mode = ref<NonNullable<ProductPaymentDisputeQuery['mode']> | ''>('')
const loading = ref(false)
const busy = ref(false)
const denied = ref(false)
const error = ref('')
const success = ref('')
const action = ref<ProductPaymentDisputeCommand['action']>('route')
const targetRoute = ref<ProductPaymentDisputeCommand['route']>('finance')
const evidenceReference = ref('')
const reason = ref('')
const confirmation = ref(false)
const pending = ref<{ id: string; input: ProductPaymentDisputeCommand; attempted: boolean }>()
let generation = 0
let appliedFilters: ProductPaymentDisputeQuery = {}
const validId = (value: string) => /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value) && value !== '00000000-0000-0000-0000-000000000000'
const date = (value: string) => formatDateTime(value, locale.value, session.user?.timezone || 'UTC')
const money = (item: ProductPaymentDispute) => formatCurrency(item.amountCents, item.currency, locale.value)
const validReason = computed(() => textWithin(reason.value, 10, 2000) && !reason.value.includes('\0'))
const needsReference = computed(() => action.value === 'record_evidence_submission')
const validReference = computed(() => !needsReference.value || /^[A-Za-z0-9][A-Za-z0-9._:/#-]{2,499}$/.test(evidenceReference.value.trim()))
const canOperate = computed(() => allowed.value && !denied.value && detail.value?.id === id.value && validId(id.value) && !busy.value && !loading.value && !pending.value)
const validAction = computed(() => !(action.value === 'request_evidence' && detail.value?.evidenceStatus === 'submitted') && !(needsReference.value && detail.value?.evidenceStatus === 'not_requested'))
const stateLabel = (group: string, value: string) => te(`productDisputes.${group}.${value}`) ? t(`productDisputes.${group}.${value}`) : t('productDisputes.unknownState')
const safeReference = (value?: string) => maskDisputeReference(value) || t('productDisputes.noReference')
watch(action, value => { if (value === 'escalate_recovery') targetRoute.value = 'collections' })
function clearData() {
  items.value = []; detail.value = undefined; cursor.value = undefined
  reason.value = ''; evidenceReference.value = ''; pending.value = undefined; confirmation.value = false; success.value = ''; error.value = ''; action.value = 'route'; targetRoute.value = 'finance'
}
function scope(current: number) {
  return createScopedApi(api, () => current === generation && allowed.value && !denied.value, cause => {
    if (cause instanceof APIError && [401, 403, 404].includes(cause.status)) {
      generation++; clearData(); denied.value = true; error.value = messageFrom(cause); loading.value = false; busy.value = false
    }
  })
}
function querySnapshot(): ProductPaymentDisputeQuery {
  return { q: query.value.trim() || undefined, actionStatus: actionStatus.value || undefined, reviewStatus: reviewStatus.value || undefined, binding: binding.value || undefined, mode: mode.value || undefined, limit: 20 }
}
async function load() {
  if (pending.value?.attempted || busy.value) return
  const current = ++generation
  clearData(); denied.value = false; error.value = ''; loading.value = false
  if (!allowed.value) return
  if (id.value && !validId(id.value)) { error.value = t('productDisputes.invalidId'); return }
  loading.value = true
  try {
    if (id.value) detail.value = await scope(current).getProductPaymentDispute(id.value)
    else {
      appliedFilters = querySnapshot()
      const page = await scope(current).listProductPaymentDisputes(appliedFilters)
      items.value = page.items; cursor.value = page.nextCursor
    }
  } catch (cause) { if (current === generation) error.value = messageFrom(cause) }
  finally { if (current === generation) loading.value = false }
}
async function more() {
  if (!cursor.value || busy.value || loading.value || !allowed.value || denied.value) return
  const current = generation; busy.value = true; error.value = ''
  try {
    const page = await scope(current).listProductPaymentDisputes({ ...appliedFilters, cursor: cursor.value })
    const seen = new Set(items.value.map(item => item.id)); items.value.push(...page.items.filter(item => !seen.has(item.id))); cursor.value = page.nextCursor
  } catch (cause) { if (current === generation) error.value = messageFrom(cause) }
  finally { if (current === generation) busy.value = false }
}
function prepare() {
  const item = detail.value
  if (!item || !canOperate.value || !validReason.value || !validReference.value || !validAction.value) return
  if (action.value === 'escalate_recovery') targetRoute.value = 'collections'
  pending.value = { id: item.id, attempted: false, input: {
    action: action.value, route: targetRoute.value, expectedVersion: item.version,
    reason: reason.value.trim(), confirmed: true,
    ...(needsReference.value ? { evidenceReference: evidenceReference.value.trim() } : {}),
  } }
  confirmation.value = true
}
function cancelConfirmation() {
  if (busy.value || pending.value?.attempted) return
  pending.value = undefined; confirmation.value = false
}
async function submit() {
  const command = pending.value
  if (!command || command.id !== id.value || !allowed.value || denied.value || busy.value) return
  const current = generation; command.attempted = true; busy.value = true; error.value = ''; success.value = ''; confirmation.value = false
  try {
    const result = await scope(current).operateProductPaymentDispute(command.id, command.input)
    detail.value = result; pending.value = undefined; reason.value = ''; evidenceReference.value = ''
    success.value = t(result.replayed ? 'productDisputes.recovered' : 'productDisputes.saved', { version: result.version })
  } catch (cause) {
    if (current !== generation) return
    error.value = messageFrom(cause)
    // Keep the original command/key available after ambiguous transport failures.
    if (cause instanceof APIError && [400, 409, 422].includes(cause.status)) {
      pending.value = undefined; detail.value = undefined; reason.value = ''; evidenceReference.value = ''
      try { detail.value = await scope(current).getProductPaymentDispute(command.id) }
      catch (readFailure) { if (current === generation) error.value = `${error.value} ${messageFrom(readFailure)}` }
    }
  } finally { if (current === generation) busy.value = false }
}
watch([() => session.user, () => session.initialized, () => session.loading, () => route.fullPath], () => {
  generation++; busy.value = false; clearData(); void load()
}, { immediate: true, flush: 'sync', deep: true })
onBeforeUnmount(() => { generation++ })
</script>

<template>
  <section class="product-disputes-page content-width">
    <PageHeader :title="t('productDisputes.title')" :summary="t('productDisputes.summary')" artwork-src="/illustrations/headers/tasks.webp">
      <template #actions>
        <UiButton v-if="allowed" variant="ghost" :disabled="busy || loading || !!pending" @click="load">
          {{ t('sellerProducts.retry') }}
        </UiButton>
        <UiButton as="RouterLink" variant="secondary" :to="id ? '/admin/product-disputes' : '/admin?tab=finance'">
          {{ t(id ? 'productDisputes.back' : 'productDisputes.finance') }}
        </UiButton>
      </template>
    </PageHeader>
    <AuthRequiredState v-if="session.initialized && !session.user" :title="t('authRequired.workspaceTitle')" :summary="t('authRequired.workspaceSummary')" :return-to="route.fullPath" />
    <div v-else class="product-disputes-content">
      <p v-if="!session.initialized || session.loading || loading" role="status">
        {{ t('sellerPayouts.loading') }}
      </p>
      <UiEmptyState v-else-if="!allowed || denied" :title="t('productDisputes.forbidden')" :message="t('productDisputes.permissionHelp')" />
      <template v-if="allowed">
        <UiAlert v-if="error" variant="danger" role="alert" :message="error" />
        <UiAlert v-if="success" variant="success" role="status" :message="success" />
        <template v-if="!loading && !denied">
          <template v-if="!id">
            <UiFilterBar class="product-dispute-filters" @submit.prevent="load">
              <label>{{ t('productDisputes.search') }}<UiInput v-model="query" :aria-label="t('productDisputes.search')" type="search" maxlength="120" /></label>
              <label>{{ t('productDisputes.actionStatus') }}<UiSelect v-model="actionStatus" :aria-label="t('productDisputes.actionStatus')"><option value="">{{ t('productDisputes.all') }}</option><option v-for="value in ['needs_response','warning_needs_response','under_review','warning_under_review','won','lost','charge_refunded','prevented','requires_review']" :key="value" :value="value">{{ t(`productDisputes.status.${value}`) }}</option></UiSelect></label>
              <label>{{ t('productDisputes.reviewStatus') }}<UiSelect v-model="reviewStatus" :aria-label="t('productDisputes.reviewStatus')"><option value="">{{ t('productDisputes.all') }}</option><option v-for="value in ['new','acknowledged','evidence_requested','evidence_submitted','escalated']" :key="value" :value="value">{{ t(`productDisputes.review.${value}`) }}</option></UiSelect></label>
              <label>{{ t('productDisputes.binding') }}<UiSelect v-model="binding" :aria-label="t('productDisputes.binding')"><option value="">{{ t('productDisputes.all') }}</option><option value="bound">{{ t('productDisputes.bound') }}</option><option value="unbound">{{ t('productDisputes.unbound') }}</option></UiSelect></label>
              <label>{{ t('productDisputes.mode') }}<UiSelect v-model="mode" :aria-label="t('productDisputes.mode')"><option value="">{{ t('productDisputes.all') }}</option><option value="live">{{ t('productDisputes.live') }}</option><option value="test">{{ t('productDisputes.test') }}</option></UiSelect></label>
              <UiButton type="submit">
                {{ t('actions.applyFilters') }}
              </UiButton>
            </UiFilterBar>
            <div class="task-results-meta">
              <strong>{{ t('productDisputes.directory') }}</strong><span>{{ t('sellerPayouts.loaded', { count: items.length }) }}</span>
            </div>
            <UiEmptyState v-if="!items.length && !error" :title="t('productDisputes.empty')" :message="t('productDisputes.emptyHelp')" />
            <UiCatalog>
              <UiContentCard v-for="item in items" :key="item.id" class="product-dispute-row">
                <UiCardContent :title="item.productTitle || t('productDisputes.untitled')" :summary="`${t('productDisputes.dueBy')} ${date(item.dueBy)}`">
                  <template #tags>
                    <UiCardTag>{{ t(`productDisputes.status.${item.actionStatus}`) }}</UiCardTag><UiCardTag>{{ t(`productDisputes.review.${item.reviewStatus}`) }}</UiCardTag><UiCardTag>{{ item.liveMode ? t('productDisputes.live') : t('productDisputes.test') }}</UiCardTag><UiCardTag v-if="!item.bound">
                      {{ t('productDisputes.unbound') }}
                    </UiCardTag>
                  </template>
                  <template #meta>
                    {{ item.sellerHandle ? `@${item.sellerHandle}` : t('productDisputes.unknownSeller') }} · {{ t('productDisputes.evidenceStatus') }}: {{ t(`productDisputes.evidence.${item.evidenceStatus}`) }}
                  </template>
                </UiCardContent>
                <UiCardActions :label="t('productDisputes.amount')" :value="money(item)" numeric>
                  <UiButton as="RouterLink" variant="secondary" :to="`/admin/product-disputes/${item.id}`">
                    {{ t('productDisputes.open') }}
                  </UiButton>
                </UiCardActions>
              </UiContentCard>
            </UiCatalog>
            <UiButton v-if="cursor" variant="secondary" :loading="busy" @click="more">
              {{ t('sellerProducts.more') }}
            </UiButton>
          </template>
          <template v-else-if="detail">
            <UiContentCard class="product-dispute-detail">
              <UiCardContent :title="detail.productTitle || t('productDisputes.untitled')" :summary="`${t('productDisputes.reference')} ${detail.id}`">
                <template #tags>
                  <UiCardTag>{{ t(`productDisputes.status.${detail.actionStatus}`) }}</UiCardTag><UiCardTag>{{ t(`productDisputes.review.${detail.reviewStatus}`) }}</UiCardTag><UiCardTag>{{ t(detail.liveMode ? 'productDisputes.live' : 'productDisputes.test') }}</UiCardTag>
                </template>
              </UiCardContent>
              <dl class="product-dispute-facts">
                <div><dt>{{ t('productDisputes.amount') }}</dt><dd>{{ money(detail) }}</dd></div>
                <div><dt>{{ t('productDisputes.route') }}</dt><dd>{{ t(`productDisputes.routes.${detail.reviewRoute}`) }}</dd></div>
                <div><dt>{{ t('productDisputes.binding') }}</dt><dd>{{ t(detail.bound ? 'productDisputes.bound' : 'productDisputes.unbound') }}</dd></div>
                <div><dt>{{ t('productDisputes.seller') }}</dt><dd>{{ detail.sellerHandle ? `@${detail.sellerHandle}` : t('productDisputes.unknownSeller') }}</dd></div>
                <div><dt>{{ t('productDisputes.providerStatus') }}</dt><dd>{{ stateLabel('status', detail.providerStatus) }}</dd></div>
                <div><dt>{{ t('productDisputes.dueBy') }}</dt><dd>{{ date(detail.dueBy) }}</dd></div>
                <div><dt>{{ t('productDisputes.version') }}</dt><dd>{{ detail.version }}</dd></div>
              </dl>
            </UiContentCard>
            <UiAlert variant="info" :message="t('productDisputes.boundary')" />
            <UiContentCard>
              <UiCardContent :title="t('productDisputes.events')" />
              <UiEmptyState v-if="!detail.events.length" :title="t('productDisputes.noEvents')" /><ol v-else class="product-dispute-history">
                <li v-for="event in detail.events" :key="event.id">
                  <strong>{{ stateLabel('eventsLabels', event.eventType) }}</strong><span>{{ stateLabel('status', event.providerStatus) }} · {{ date(event.occurredAt) }}</span><small>{{ t(event.applied ? 'productDisputes.applied' : 'productDisputes.notApplied') }}</small>
                </li>
              </ol>
            </UiContentCard>
            <UiContentCard>
              <UiCardContent :title="t('productDisputes.evidenceSubmissions')" />
              <UiEmptyState v-if="!detail.evidenceSubmissions.length" :title="t('productDisputes.noEvidence')" />
              <ol v-else class="product-dispute-history">
                <li v-for="entry in detail.evidenceSubmissions" :key="entry.id">
                  <strong>{{ t('productDisputes.evidenceRecorded') }}</strong><span>{{ date(entry.submittedAt) }} · @{{ entry.submitterHandle }}</span><small>{{ safeReference(entry.providerReference) }}</small>
                </li>
              </ol>
            </UiContentCard>
            <UiContentCard>
              <UiCardContent :title="t('productDisputes.auditHistory')" />
              <UiEmptyState v-if="!detail.operations.length" :title="t('productDisputes.noOperations')" />
              <ol v-else class="product-dispute-history">
                <li v-for="operation in detail.operations" :key="operation.id">
                  <strong>{{ t(`productDisputes.actions.${operation.action}`) }} · {{ t(`productDisputes.routes.${operation.route}`) }}</strong><span>{{ operation.actorDisplayName || `@${operation.actorHandle}` }} · {{ t('productDisputes.versionTransition', { from: operation.expectedVersion, to: operation.resultingVersion }) }} · {{ date(operation.createdAt) }}</span><small>{{ stateLabel('review', operation.fromReviewStatus) }} → {{ stateLabel('review', operation.toReviewStatus) }} · {{ stateLabel('evidence', operation.fromEvidenceStatus) }} → {{ stateLabel('evidence', operation.toEvidenceStatus) }}</small><small>{{ operation.reason }}</small><small v-if="operation.evidenceReference">{{ safeReference(operation.evidenceReference) }}</small>
                </li>
              </ol>
            </UiContentCard>
            <UiForm v-if="allowed && !denied" class="product-dispute-operation-form" :disabled="!canOperate" @submit="prepare">
              <UiFormField :label="t('productDisputes.operation')" required>
                <UiSelect v-model="action" :aria-label="t('productDisputes.operation')">
                  <option value="route">
                    {{ t('productDisputes.actions.route') }}
                  </option><option value="request_evidence" :disabled="detail.evidenceStatus === 'submitted'">
                    {{ t('productDisputes.actions.request_evidence') }}
                  </option><option value="record_evidence_submission" :disabled="detail.evidenceStatus === 'not_requested'">
                    {{ t('productDisputes.actions.record_evidence_submission') }}
                  </option><option value="escalate_recovery">
                    {{ t('productDisputes.actions.escalate_recovery') }}
                  </option>
                </UiSelect>
              </UiFormField>
              <UiFormField :label="t('productDisputes.route')" required>
                <UiSelect v-model="targetRoute" :aria-label="t('productDisputes.route')" :disabled="action === 'escalate_recovery'">
                  <option value="finance">
                    {{ t('productDisputes.routes.finance') }}
                  </option><option value="seller_support">
                    {{ t('productDisputes.routes.seller_support') }}
                  </option><option value="provider_review">
                    {{ t('productDisputes.routes.provider_review') }}
                  </option><option value="collections">
                    {{ t('productDisputes.routes.collections') }}
                  </option>
                </UiSelect>
              </UiFormField>
              <UiFormField v-if="needsReference" :label="t('productDisputes.evidenceReference')" :hint="t('productDisputes.referenceHelp')" required>
                <UiInput v-model="evidenceReference" :aria-label="t('productDisputes.evidenceReference')" maxlength="500" autocomplete="off" />
              </UiFormField>
              <UiFormField :label="t('productDisputes.reason')" :hint="t('productDisputes.reasonHelp')" required>
                <UiTextarea v-model="reason" :aria-label="t('productDisputes.reason')" rows="4" maxlength="2000" />
              </UiFormField>
              <p>{{ t('productDisputes.expectedVersion', { version: detail.version }) }}</p>
              <UiButton type="submit" :disabled="!canOperate || !validReason || !validReference || !validAction">
                {{ t('productDisputes.reviewAndConfirm') }}
              </UiButton>
            </UiForm>
            <UiAlert v-if="pending?.attempted" variant="warning" :message="t('productDisputes.unknownResult')">
              <p>{{ stateLabel('actions', pending.input.action) }} · {{ stateLabel('routes', pending.input.route) }} · {{ t('productDisputes.versionValue', { version: pending.input.expectedVersion }) }}</p>
              <UiButton variant="secondary" :loading="busy" @click="submit">
                {{ t('productDisputes.retryOriginal') }}
              </UiButton>
            </UiAlert>
          </template>
        </template>
      </template>
    </div>
    <UiAlertDialog :open="confirmation" :title="t('productDisputes.confirmTitle')" :description="t('productDisputes.confirmBody', { version: pending?.input.expectedVersion })" :confirm-label="t('productDisputes.confirm')" :cancel-label="t('productDisputes.cancel')" :busy="busy" @update:open="!$event && cancelConfirmation()" @confirm="submit">
      <template v-if="pending">
        <p>{{ stateLabel('actions', pending.input.action) }} · {{ stateLabel('routes', pending.input.route) }} · {{ t('productDisputes.versionValue', { version: pending.input.expectedVersion }) }}</p>
        <p>{{ pending.input.reason }}</p>
        <p v-if="pending.input.evidenceReference">
          {{ safeReference(pending.input.evidenceReference) }}
        </p>
      </template>
    </UiAlertDialog>
  </section>
</template>

<style scoped>
.product-disputes-content { display: grid; gap: 20px; padding-block: 20px; min-width: 0; }
.product-dispute-filters { display: grid; grid-template-columns: minmax(180px, 1.5fr) repeat(4, minmax(130px, 1fr)) auto; gap: 12px; align-items: end; }
.product-dispute-filters label { display: grid; gap: 6px; min-width: 0; color: var(--text-secondary); font-size: 13px; }
.product-dispute-row { min-width: 0; display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 20px; padding: 20px; }
.product-dispute-detail { min-width: 0; padding: 20px; }
.product-disputes-content > .ui-content-card { padding: 20px; }
.product-dispute-facts { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: 16px; margin: 20px 0 0; }
.product-dispute-facts div { min-width: 0; }
.product-dispute-facts dt { color: var(--text-secondary); font-size: 12px; }
.product-dispute-facts dd { margin: 4px 0 0; overflow-wrap: anywhere; }
.product-dispute-history { display: grid; gap: 1px; margin: 0; padding: 0; list-style: none; }
.product-dispute-history li { display: grid; gap: 5px; padding: 14px 18px; border-top: 1px solid var(--border); }
.product-dispute-history span, .product-dispute-history small { color: var(--text-secondary); overflow-wrap: anywhere; }
.product-dispute-operation-form { display: grid; gap: 16px; max-width: 720px; }
@media (max-width: 900px) { .product-dispute-filters { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 560px) { .product-dispute-row { grid-template-columns: minmax(0, 1fr); } .product-dispute-filters { grid-template-columns: minmax(0, 1fr); } }
</style>
