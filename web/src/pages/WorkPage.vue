<script setup lang="ts">
import { ArrowLeft, RefreshCw, Sparkles } from 'lucide-vue-next'
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'
import { api, messageFrom, type Work } from '../api/client'
import AssetMedia from '../components/domain/AssetMedia.vue'
import UiButton from '../components/ui/UiButton.vue'
import UiCollapsible from '../components/ui/UiCollapsible.vue'
import UiCopyButton from '../components/ui/UiCopyButton.vue'
import { contentListReturn, creationPath, licenseLabel } from '../lib/contentPresentation'

const { t } = useI18n()
const route = useRoute()
const work = ref<Work | null>(null)
const loading = ref(true)
const error = ref('')
const disclosureOpen = ref(false)
const copyError = ref('')
const promptExpanded = ref(false)

let requestVersion = 0
async function load() {
  const version = ++requestVersion
  loading.value = true
  error.value = ''
  copyError.value = ''
  disclosureOpen.value = false
  promptExpanded.value = false
  try {
    const result = await api.getWork(String(route.params.id))
    if (version === requestVersion) work.value = result
  } catch (reason) {
    if (version === requestVersion) error.value = messageFrom(reason)
  } finally {
    if (version === requestVersion) loading.value = false
  }
}

watch(() => route.params.id, () => void load(), { immediate: true })
</script>

<template>
  <section class="work-detail content-width">
    <RouterLink class="text-link back-link" :to="contentListReturn('/discover')">
      <ArrowLeft :size="16" />{{ t('actions.backDiscover') }}
    </RouterLink>
    <div v-if="loading" class="page-state" aria-live="polite">
      {{ t('status.loadingWork') }}
    </div>
    <div v-else-if="error" class="page-state" role="alert">
      <p>{{ error }}</p>
      <UiButton class="command-button secondary" variant="secondary" @click="load">
        <template #start>
          <RefreshCw :size="17" />
        </template>{{ t('actions.retry') }}
      </UiButton>
    </div>
    <div v-else-if="work" class="work-detail-grid">
      <header class="work-detail-heading">
        <span class="status-label">{{ work.licenseCode.startsWith('demo') ? t('status.demo') : t('status.published') }}</span>
        <h1>{{ work.title }}</h1>
        <p class="work-summary">
          {{ work.summary }}
        </p>
        <UiButton as="RouterLink" variant="primary" :to="{ path: creationPath(work.mediaKind), query: { sourceWorkId: work.id } }">
          <Sparkles :size="17" />{{ t('actions.remix') }}
        </UiButton>
      </header>
      <div class="work-detail-media">
        <AssetMedia :src="work.mediaUrl" :kind="work.mediaKind" :alt="work.title" :width="work.width || 1600" :height="work.height || 1000" />
        <a v-if="work.mediaUrl" class="work-full-preview" :href="work.mediaUrl" target="_blank" rel="noopener noreferrer">{{ t('content.preview') }}</a>
      </div>
      <aside class="work-inspector">
        <dl class="metadata-list">
          <div>
            <dt>{{ t('work.creator') }}</dt><dd>
              <RouterLink class="text-link" :to="`/creators/${work.author.handle}`">
                {{ work.author.displayName }} · @{{ work.author.handle }}
              </RouterLink>
            </dd>
          </div>
          <div><dt>{{ t('work.model') }}</dt><dd>{{ work.modelName }}</dd></div>
          <div><dt>{{ t('work.license') }}</dt><dd>{{ licenseLabel(work.licenseCode) }}</dd></div>
        </dl>
        <section class="detail-block">
          <h2>{{ t('work.prompt') }}</h2>
          <UiCopyButton v-if="work.prompt" :value="work.prompt" :label="t('content.copy')" :copied-label="t('content.copied')" @copied="copyError = ''" @error="copyError = t('content.copyFailed')" />
          <p v-if="copyError" role="alert">
            {{ copyError }}
          </p>
          <p v-if="work.prompt" id="work-prompt" class="prompt-text" :class="{ 'is-expanded': promptExpanded }" tabindex="0">
            {{ work.prompt }}
          </p>
          <p v-else>
            {{ t('work.promptPrivate') }}
          </p>
          <UiButton v-if="work.prompt && work.prompt.length > 600" variant="ghost" size="sm" :aria-expanded="promptExpanded" aria-controls="work-prompt" @click="promptExpanded = !promptExpanded">
            {{ t(promptExpanded ? 'content.collapsePrompt' : 'content.expandPrompt') }}
          </UiButton>
        </section>
        <UiCollapsible v-model:open="disclosureOpen" class="detail-block disclosure-block" :title="t('content.details')">
          <p>{{ work.aiDisclosure }}</p>
          <small>{{ work.licenseCode }}</small>
        </UiCollapsible>
      </aside>
    </div>
  </section>
</template>

<style scoped>
.work-detail-grid { grid-template-columns: minmax(0, 1fr) minmax(300px, 380px); gap: 24px; }
.work-detail-heading { grid-column: 1 / -1; display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 8px 20px; align-items: center; }
.work-detail-heading .status-label { grid-column: 1 / -1; }
.work-detail-heading h1 { margin: 0; font-size: clamp(24px, 2.4vw, 32px); line-height: 1.25; overflow-wrap: anywhere; }
.work-detail-heading .work-summary { grid-column: 1; margin: 0; max-width: 76ch; font-size: 14px; line-height: 1.65; }
.work-detail-heading .ui-button { grid-column: 2; grid-row: 2 / 4; }
.work-detail-media { position: relative; min-height: 0; height: min(62dvh, 600px); }
.work-detail-media :deep(img), .work-detail-media :deep(video) { width: 100%; height: 100%; max-height: 100%; object-fit: contain; }
.work-full-preview { position: absolute; right: 12px; bottom: 12px; padding: 10px 14px; border-radius: var(--radius-control); background: rgb(0 0 0 / 72%); color: white; font-size: 13px; }
.work-inspector { padding: 18px; border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface); }
.metadata-list { margin: 0 0 20px; border-top: 0; font-size: 13px; }
.metadata-list > div { grid-template-columns: 90px minmax(0, 1fr); gap: 12px; }
.detail-block { margin: 20px 0 0; }
.detail-block h2 { display: inline-block; margin: 0 12px 12px 0; }
.prompt-text { margin-top: 12px; max-height: 280px; overflow-y: auto; white-space: pre-wrap; background: var(--surface-muted); font-size: 14px; line-height: 1.7; }
.prompt-text.is-expanded { max-height: none; }
.disclosure-block p { margin-top: 12px; line-height: 1.6; }
.disclosure-block small { display: block; margin-top: 8px; overflow-wrap: anywhere; }
@media(max-width: 1000px) { .work-detail-grid { grid-template-columns: minmax(0, 1fr); } .work-inspector { position: static; } }
@media(max-width: 600px) { .work-detail-heading { grid-template-columns: 1fr; } .work-detail-heading .ui-button { grid-column: 1; grid-row: auto; justify-self: start; } .work-detail-media { height: min(50dvh, 420px); } }
</style>
