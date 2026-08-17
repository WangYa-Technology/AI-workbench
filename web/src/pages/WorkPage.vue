<script setup lang="ts">
import { ArrowLeft, RefreshCw, Sparkles } from 'lucide-vue-next'
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'
import { api, messageFrom, type Work } from '../api/client'
import AssetMedia from '../components/domain/AssetMedia.vue'

const { t } = useI18n()
const route = useRoute()
const work = ref<Work | null>(null)
const loading = ref(true)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    work.value = await api.getWork(String(route.params.id))
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    loading.value = false
  }
}

onMounted(() => void load())
</script>

<template>
  <section class="work-detail content-width">
    <RouterLink class="text-link back-link" to="/discover">
      <ArrowLeft :size="16" />{{ t('actions.backDiscover') }}
    </RouterLink>
    <div v-if="loading" class="page-state" aria-live="polite">
      {{ t('status.loadingWork') }}
    </div>
    <div v-else-if="error" class="page-state" role="alert">
      <p>{{ error }}</p>
      <button class="command-button secondary" type="button" @click="load">
        <RefreshCw :size="17" />{{ t('actions.retry') }}
      </button>
    </div>
    <div v-else-if="work" class="work-detail-grid">
      <div class="work-detail-media">
        <AssetMedia :src="work.mediaUrl" :kind="work.mediaKind" :alt="work.title" :width="work.width || 1600" :height="work.height || 1000" />
      </div>
      <aside class="work-inspector">
        <span class="status-label">{{ work.licenseCode.startsWith('demo') ? t('status.demo') : t('status.published') }}</span>
        <h1>{{ work.title }}</h1>
        <p class="work-summary">
          {{ work.summary }}
        </p>
        <dl class="metadata-list">
          <div>
            <dt>{{ t('work.creator') }}</dt><dd>
              <RouterLink class="text-link" :to="`/creators/${work.author.handle}`">
                {{ work.author.displayName }} · @{{ work.author.handle }}
              </RouterLink>
            </dd>
          </div>
          <div><dt>{{ t('work.model') }}</dt><dd>{{ work.modelName }}</dd></div>
          <div><dt>{{ t('work.license') }}</dt><dd>{{ work.licenseCode }}</dd></div>
        </dl>
        <section class="detail-block">
          <h2>{{ t('work.prompt') }}</h2>
          <p v-if="work.prompt" class="prompt-text">
            {{ work.prompt }}
          </p>
          <p v-else>
            {{ t('work.promptPrivate') }}
          </p>
        </section>
        <section class="detail-block disclosure-block">
          <h2>{{ t('work.aiDisclosure') }}</h2>
          <p>{{ work.aiDisclosure }}</p>
        </section>
        <RouterLink class="command-button primary wide" :to="`/create/image?sourceWorkId=${work.id}`">
          <Sparkles :size="17" :stroke-width="1.75" />{{ t('actions.remix') }}
        </RouterLink>
      </aside>
    </div>
  </section>
</template>
