<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { APIError, messageFrom, type MediaCleanup, type ExportJobQuery, type DeletionJobQuery, type MediaCleanupQuery } from '../../api/client'
import { useAdminApi } from '../../composables/useAdminContext'
import { useSessionStore } from '../../stores/session'
import { textWithin } from '../../lib/unicodeText'
import UiButton from '../ui/UiButton.vue'
import UiCheckbox from '../ui/UiCheckbox.vue'
import UiDrawer from '../ui/UiDrawer.vue'
import UiEmptyState from '../ui/UiEmptyState.vue'
import UiFilterBar from '../ui/UiFilterBar.vue'
import UiForm from '../ui/UiForm.vue'
import UiSelect from '../ui/UiSelect.vue'
import UiTextarea from '../ui/UiTextarea.vue'
const { t } = useI18n()
const props = withDefaults(defineProps<{ mode?: 'media' | 'exports' | 'deletions' }>(), { mode: 'media' })
const api = useAdminApi()
const session = useSessionStore()
const allowed = computed(() => session.initialized && Boolean(session.user?.permissions.includes('admin:data-rights')))
const prefix = computed(() => props.mode === 'deletions' ? 'admin.deletionRecovery' : props.mode === 'exports' ? 'admin.exportRecovery' : 'admin.mediaCleanup')
const kinds = computed(() => props.mode === 'deletions' ? ['all', 'prepare', 'cleanup'] : props.mode === 'exports' ? ['all', 'export', 'expiry'] : ['all', 'account', 'product'])
type RecoveryJob = Omit<MediaCleanup, 'kind' | 'unavailableReason'> & { kind: string; unavailableReason: string; requestId?: string }

const kind = ref('all')
const status = ref<NonNullable<MediaCleanupQuery['status']>>('failed')
const items = ref<RecoveryJob[]>([])
const nextCursor = ref<string>()
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const commandError = ref('')
const success = ref('')
const selected = ref<RecoveryJob | null>(null)
const reason = ref('')
const confirmed = ref(false)
let version = 0
let commandVersion = 0
let mounted = true
function clear() {
  ++version; ++commandVersion
  items.value = []; nextCursor.value = undefined; selected.value = null
  reason.value = ''; confirmed.value = false
  error.value = ''; commandError.value = ''; success.value = ''
  loading.value = saving.value = false
}
function fail(failure: unknown, command = false) {
  if (failure instanceof APIError && [401, 403, 404].includes(failure.status)) {
    clear()
    error.value = messageFrom(failure)
  } else if (command) commandError.value = messageFrom(failure)
  else error.value = messageFrom(failure)
}
async function load(more = false) {
  if (!allowed.value || !mounted) return
  if (more && (!nextCursor.value || loading.value)) return
  const current = ++version
  loading.value = true
  error.value = ''
  const cursor = more ? nextCursor.value : undefined
  try {
    const query = { status: status.value, limit: 20, cursor }
    const page = props.mode === 'deletions'
      ? await api.adminListDeletionJobs({ ...query, kind: kind.value as DeletionJobQuery['kind'] })
      : props.mode === 'exports'
      ? await api.adminListExportJobs({ ...query, kind: kind.value as ExportJobQuery['kind'] })
      : await api.adminListMediaCleanups({ ...query, kind: kind.value as MediaCleanupQuery['kind'] })
    if (!mounted || current !== version) return
    const seen = new Set(items.value.map(item => item.id))
    items.value = more ? [...items.value, ...page.items.filter(item => !seen.has(item.id))] : page.items
    nextCursor.value = page.nextCursor
  } catch (failure) {
    if (mounted && current === version) fail(failure)
  } finally { if (mounted && current === version) loading.value = false }
}
function open(item: RecoveryJob) {
  if (!allowed.value || saving.value || loading.value || !item.canRetry) return
  selected.value = item
  reason.value = ''
  confirmed.value = false
  commandError.value = ''
  success.value = ''
}
async function retry() {
  const item = selected.value
  if (!allowed.value || !item?.canRetry || saving.value || loading.value || !confirmed.value || !textWithin(reason.value, 10, 2000)) return
  const current = ++commandVersion
  saving.value = true
  commandError.value = ''
  try {
    const input = { expectedAttempts: item.attempts, reason: reason.value.trim(), confirmed: true as const }
    const replacement = props.mode === 'deletions' ? await api.adminRetryDeletionJob(item.id, input) : props.mode === 'exports' ? await api.adminRetryExportJob(item.id, input) : await api.adminRetryMediaCleanup(item.id, input)
    if (!mounted || current !== commandVersion) return
    // A failed refresh must not re-enable a recovery that was already accepted.
    items.value = items.value.map(current => current.id === item.id
      ? { ...current, canRetry: false, unavailableReason: 'already_retried', retryJobId: replacement.id }
      : current)
    selected.value = null
    success.value = t(`${prefix.value}.queued`)
    await load()
  } catch (failure) {
    if (mounted && current === commandVersion) fail(failure, true)
  } finally { if (mounted && current === commandVersion) saving.value = false }
}
watch([status, kind, () => props.mode, allowed, () => session.user?.id], () => { clear(); void load() }, { immediate: true, flush: 'sync' })
onBeforeUnmount(() => { mounted = false; clear() })
</script>

<template>
  <section v-if="allowed" :aria-label="t(`${prefix}.title`)">
    <header><div><h2>{{ t(`${prefix}.title`) }}</h2><p>{{ t(`${prefix}.description`) }}</p></div></header>
    <UiFilterBar as="div" fields density="compact">
      <label>{{ t(`${prefix}.scope`) }}<UiSelect v-model="kind" :disabled="saving">
        <option v-for="scope in kinds" :key="scope" :value="scope">{{ t(`${prefix}.kinds.${scope}`) }}</option>
      </UiSelect></label>
      <label>{{ t('admin.status') }}<UiSelect v-model="status" :disabled="saving">
        <option value="all">{{ t(`${prefix}.all`) }}</option>
        <option v-for="state in ['failed', 'queued', 'running', 'succeeded', 'cancelled']" :key="state" :value="state">{{ t(`admin.jobStates.${state}`) }}</option>
      </UiSelect></label>
      <UiButton variant="secondary" :disabled="loading || saving" @click="load()">
        {{ t(`${prefix}.refresh`) }}
      </UiButton>
    </UiFilterBar>
    <p v-if="error" role="alert">
      {{ error }}
    </p>
    <p v-if="success" role="status">
      {{ success }}
    </p>
    <p v-if="loading" role="status">
      {{ t(`${prefix}.loading`) }}
    </p>
    <div v-if="items.length" class="admin-list cleanup-job-list">
      <article v-for="item in items" :key="item.id">
        <div>
          <strong :title="item.id">{{ t(`${prefix}.job`) }} · {{ item.id.slice(0, 8) }}</strong><small>{{ t(`${prefix}.kinds.${item.kind}`) }}</small>
          <small v-if="item.userId" :title="item.userId">{{ t(`${prefix}.subject`) }} · {{ item.userId.slice(0, 8) }}</small>
          <small v-if="item.orderId" :title="item.orderId">{{ t(`${prefix}.order`) }} · {{ item.orderId }}</small>
          <small v-if="item.requestId" :title="item.requestId">{{ t(`${prefix}.request`) }} &#183; {{ item.requestId }}</small>
          <small>{{ t(`${prefix}.attempts`, { used: item.attempts, max: item.maxAttempts }) }} · {{ item.errorCode || '—' }}</small>
        </div>
        <span :data-status="item.status">{{ t(`admin.jobStates.${item.status}`) }}</span>
        <UiButton v-if="item.canRetry" variant="secondary" :disabled="saving || loading" @click="open(item)">
          {{ t(`${prefix}.retry`) }}
        </UiButton>
        <small v-else-if="item.unavailableReason !== 'not_failed'" class="cleanup-job-reason">{{ t(`${prefix}.reasons.${item.unavailableReason}`) }}</small><span v-else></span>
        <small v-if="item.retryJobId" class="admin-row-wide">{{ t(`${prefix}.replacement`) }} · {{ item.retryJobId }}</small>
        <small v-if="item.retryOf" class="admin-row-wide">{{ t(`${prefix}.original`) }} · {{ item.retryOf }}</small>
      </article>
    </div>
    <UiEmptyState v-else-if="!loading && !error" density="compact" :title="t(`${prefix}.empty`)" />
    <UiButton v-if="nextCursor" class="admin-load-more" variant="secondary" :disabled="loading || saving" @click="load(true)">
      {{ t('actions.loadMore') }}
    </UiButton>
    <UiDrawer :open="Boolean(selected)" :label="t(`${prefix}.retry`)" @update:open="!$event && !saving && (selected = null)">
      <UiForm class="cleanup-retry-form" :disabled="saving" @submit="retry">
        <h2>{{ t(`${prefix}.retry`) }}</h2>
        <p>{{ t(`${prefix}.confirmDescription`) }}</p>
        <p v-if="selected && mode === 'deletions'">
          {{ t(`${prefix}.scope`) }} · {{ t(`${prefix}.kinds.${selected.kind}`) }}
        </p>
        <p v-if="selected">
          {{ t(`${prefix}.job`) }} · {{ selected.id }}
        </p>
        <p v-if="selected?.requestId">
          {{ t(`${prefix}.request`) }} · {{ selected.requestId }}
        </p>
        <p v-if="selected?.orderId">
          {{ t(`${prefix}.order`) }} · {{ selected.orderId }}
        </p>
        <p v-if="selected?.errorCode && ['data_export_too_large', 'data_export_record_too_large', 'data_export_storage_unavailable', 'data_export_busy'].includes(selected.errorCode)">
          {{ t(`errors.codes.${selected.errorCode}`) }}
        </p>
        <label>{{ t('admin.resolutionReason') }}<UiTextarea v-model.trim="reason" rows="4" required /></label>
        <label class="admin-checkbox"><UiCheckbox v-model="confirmed" required />{{ t(`${prefix}.confirm`) }}</label>
        <p v-if="commandError" role="alert">
          {{ commandError }}
        </p>
        <div class="cleanup-retry-actions">
          <UiButton type="button" variant="secondary" @click="selected = null">
            {{ t('actions.cancel') }}
          </UiButton>
          <UiButton type="submit" :disabled="!confirmed || !textWithin(reason, 10, 2000)">
            {{ t(`${prefix}.retry`) }}
          </UiButton>
        </div>
      </UiForm>
    </UiDrawer>
  </section>
</template>

<style scoped>
.cleanup-retry-form { padding: 24px; max-height: 100%; overflow-y: auto; }
.cleanup-retry-form h2, .cleanup-retry-form p { margin: 0; }
.cleanup-retry-actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 12px; }
.cleanup-job-list article { grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr); padding-block: 14px; }
.cleanup-job-list article small { min-width: 0; white-space: normal; overflow-wrap: anywhere; }
.cleanup-job-list article > small { display: block; }
.cleanup-job-list article > .ui-button { justify-self: end; }
.cleanup-job-reason { line-height: 1.5; }
.cleanup-job-list .admin-row-wide { grid-column: 1 / -1; }
@media (max-width: 700px) {
  .cleanup-job-list article { grid-template-columns: minmax(0, 1fr) auto; }
  .cleanup-job-list article > div { grid-column: 1 / -1; }
  .cleanup-job-list article > .cleanup-job-reason { grid-column: 1 / -1; }
}
</style>
