<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, APIError, messageFrom, type ProductDeliveryEvidenceFilter, type ProductDeliveryEvidenceGap } from '../../api/client'
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

const { t, locale } = useI18n()
const session = useSessionStore()
const allowed = computed(() => session.initialized && Boolean(session.user?.permissions.includes('admin:media')))
const gap = ref<NonNullable<ProductDeliveryEvidenceFilter['gap']> | ''>('')
const environment = ref<NonNullable<ProductDeliveryEvidenceFilter['environment']> | ''>('')
const scope = ref<NonNullable<ProductDeliveryEvidenceFilter['scope']> | ''>('active')
const items = ref<ProductDeliveryEvidenceGap[]>([])
const scanned = ref(0)
const nextCursor = ref<string>()
const loading = ref(false)
const error = ref('')
const gaps = ['contract_missing', 'legacy_unfrozen', 'required_snapshot_missing', 'legacy_snapshot_unbound'] as const
let generation = 0

async function load(more = false) {
  if (more && (loading.value || !nextCursor.value)) return
  const current = more ? generation : ++generation
  if (!more) { items.value = []; scanned.value = 0; nextCursor.value = undefined }
  error.value = ''
  loading.value = false
  if (!allowed.value) return
  loading.value = true
  try {
    const result = await api.adminDeliveryEvidenceGaps({ gap: gap.value || undefined, environment: environment.value || undefined, scope: scope.value || undefined, cursor: more ? nextCursor.value : undefined })
    if (current !== generation) return
    items.value = more ? [...items.value, ...result.items] : result.items
    scanned.value += result.scanned
    nextCursor.value = result.nextCursor
  } catch (failure) {
    if (current !== generation) return
    if (failure instanceof APIError && [401, 403, 404].includes(failure.status)) {
      ++generation
      items.value = []; scanned.value = 0; nextCursor.value = undefined; loading.value = false
    }
    error.value = messageFrom(failure)
  }
  finally { if (current === generation) loading.value = false }
}
watch([gap, environment, scope, allowed, () => session.user?.id], () => load(), { immediate: true, flush: 'sync' })
onBeforeUnmount(() => { ++generation })
</script>

<template>
  <section v-if="allowed" class="delivery-evidence-inventory" :aria-label="t('deliveryEvidence.title')" :aria-busy="loading">
    <header>
      <h2>{{ t('deliveryEvidence.title') }}</h2>
      <p>{{ t('deliveryEvidence.summary') }}</p>
    </header>
    <UiFilterBar as="div" fields layout="grid" class="delivery-evidence-filters">
      <label>{{ t('deliveryEvidence.gapLabel') }}<UiSelect v-model="gap"><option value="">{{ t('deliveryEvidence.allGaps') }}</option><option v-for="value in gaps" :key="value" :value="value">{{ t(`deliveryEvidence.gaps.${value}`) }}</option></UiSelect></label>
      <label>{{ t('deliveryEvidence.environmentLabel') }}<UiSelect v-model="environment"><option value="">{{ t('deliveryEvidence.allEnvironments') }}</option><option v-for="value in ['live', 'test', 'unknown']" :key="value" :value="value">{{ t(`deliveryEvidence.environments.${value}`) }}</option></UiSelect></label>
      <label>{{ t('deliveryEvidence.scopeLabel') }}<UiSelect v-model="scope"><option value="active">{{ t('deliveryEvidence.activeScope') }}</option><option value="unsettled">{{ t('deliveryEvidence.unsettledScope') }}</option><option value="">{{ t('deliveryEvidence.allScope') }}</option></UiSelect></label>
      <UiButton variant="secondary" :disabled="loading" @click="load()">
        {{ t('deliveryEvidence.refresh') }}
      </UiButton>
    </UiFilterBar>
    <UiAlert v-if="error" variant="danger" role="alert">
      {{ error }}
    </UiAlert>
    <p role="status">
      {{ loading ? t('deliveryEvidence.loading') : t('deliveryEvidence.loaded', { count: items.length, scanned }) }}
    </p>
    <UiCatalog v-if="items.length">
      <UiContentCard v-for="item in items" :key="item.orderId" class="delivery-evidence-card">
        <UiCardContent :title="item.title" :summary="t(`deliveryEvidence.guidance.${item.gap}`)">
          <template #tags>
            <UiCardTag variant="warning">
              {{ t(`deliveryEvidence.gaps.${item.gap}`) }}
            </UiCardTag>
            <UiCardTag variant="neutral">
              {{ t(`deliveryEvidence.environments.${item.environment}`) }}
            </UiCardTag>
            <UiCardTag v-if="item.hasActiveRights" variant="primary">
              {{ t('deliveryEvidence.activeRights') }}
            </UiCardTag>
            <UiCardTag v-if="item.hasPendingPayment" variant="primary">
              {{ t('deliveryEvidence.pendingPayment') }}
            </UiCardTag>
            <UiCardTag v-if="item.hasUnsettledFunds" variant="warning">
              {{ t('deliveryEvidence.unsettledFunds') }}
            </UiCardTag>
          </template>
          <template #meta>
            <span>{{ t('deliveryRepair.order') }} <code>{{ item.orderId }}</code></span>
            <time :datetime="item.createdAt">{{ new Date(item.createdAt).toLocaleString(locale) }}</time>
          </template>
        </UiCardContent>
        <UiButton v-if="item.hasSnapshot" as="RouterLink" variant="secondary" :to="`/admin/deliveries/${item.orderId}`">
          {{ t('deliveryRepair.inspect') }}
        </UiButton>
      </UiContentCard>
    </UiCatalog>
    <UiEmptyState v-else-if="!loading && !error" :title="t(nextCursor ? 'deliveryEvidence.batchEmpty' : 'deliveryEvidence.empty')" :message="t(nextCursor ? 'deliveryEvidence.batchEmptySummary' : 'deliveryEvidence.emptySummary')" />
    <UiButton v-if="nextCursor" variant="secondary" :loading="loading" @click="load(true)">
      {{ t('deliveryEvidence.more') }}
    </UiButton>
  </section>
</template>

<style scoped>
.delivery-evidence-inventory { display: grid; gap: 20px; padding-block: 24px; }
.delivery-evidence-inventory h2 { margin: 0; font-size: 18px; }
.delivery-evidence-inventory p { margin: 8px 0 0; color: var(--text-secondary); }
.delivery-evidence-filters { grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr)); }
.delivery-evidence-card { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 16px; padding: 20px; }
.delivery-evidence-card .ui-card-content { flex: 1 1 280px; }
.delivery-evidence-card :deep(.ui-card-content__summary) { display: block; }
.delivery-evidence-card code { overflow-wrap: anywhere; }
</style>
