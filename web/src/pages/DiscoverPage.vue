<script setup lang="ts">
import { ArrowRight, BriefcaseBusiness, RefreshCw, Sparkles, UsersRound } from 'lucide-vue-next'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'
import { api, messageFrom, type Work } from '../api/client'
import AssetMedia from '../components/domain/AssetMedia.vue'

const { t } = useI18n()
const route = useRoute()
const works = ref<Work[]>([])
const loading = ref(true)
const error = ref('')
const query = computed(() => String(route.query.q || '').trim().toLocaleLowerCase())
const visibleWorks = computed(() => {
  if (!query.value) return works.value
  return works.value.filter((work) => [work.title, work.summary, work.author.displayName, work.modelName, work.licenseCode]
    .some((value) => value.toLocaleLowerCase().includes(query.value)))
})
const featured = computed(() => visibleWorks.value[0])
const supporting = computed(() => visibleWorks.value.slice(1))

async function load() {
  loading.value = true
  error.value = ''
  try {
    works.value = (await api.listWorks()).items
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    loading.value = false
  }
}

onMounted(() => void load())
</script>

<template>
  <section class="discover-page">
    <header v-if="!loading && !error" class="discover-intro content-width">
      <div>
        <h1>{{ t('discover.title') }}</h1>
        <p>{{ t('discover.summary') }}</p>
      </div>
      <nav class="discover-intro-actions" :aria-label="t('actions.quickLinks')">
        <RouterLink class="command-button primary" to="/create/image">
          <Sparkles :size="17" :stroke-width="1.75" />{{ t('actions.startCreating') }}
        </RouterLink>
        <RouterLink class="command-button secondary" to="/market/demands">
          <BriefcaseBusiness :size="17" :stroke-width="1.75" />{{ t('actions.findBrief') }}
        </RouterLink>
      </nav>
    </header>
    <section v-if="!loading && !error" class="discover-pathways content-width" :aria-label="t('discover.pathwaysLabel')">
      <RouterLink class="pathway-card" to="/create/image">
        <span class="pathway-icon"><Sparkles :size="18" :stroke-width="1.75" /></span>
        <span><strong>{{ t('discover.pathways.create.title') }}</strong><small>{{ t('discover.pathways.create.summary') }}</small></span>
        <ArrowRight :size="17" :stroke-width="1.75" />
      </RouterLink>
      <RouterLink class="pathway-card" to="/community">
        <span class="pathway-icon"><UsersRound :size="18" :stroke-width="1.75" /></span>
        <span><strong>{{ t('discover.pathways.community.title') }}</strong><small>{{ t('discover.pathways.community.summary') }}</small></span>
        <ArrowRight :size="17" :stroke-width="1.75" />
      </RouterLink>
      <RouterLink class="pathway-card" to="/market/demands">
        <span class="pathway-icon"><BriefcaseBusiness :size="18" :stroke-width="1.75" /></span>
        <span><strong>{{ t('discover.pathways.tasks.title') }}</strong><small>{{ t('discover.pathways.tasks.summary') }}</small></span>
        <ArrowRight :size="17" :stroke-width="1.75" />
      </RouterLink>
    </section>
    <div v-if="loading" class="page-state hero-state" aria-live="polite">
      {{ t('status.loadingWorks') }}
    </div>
    <div v-else-if="error" class="page-state hero-state" role="alert">
      <p>{{ error }}</p>
      <button class="command-button secondary" type="button" @click="load">
        <RefreshCw :size="17" :stroke-width="1.75" />{{ t('actions.retry') }}
      </button>
    </div>
    <div v-else-if="featured" class="discover-hero">
      <RouterLink class="hero-media" :to="`/works/${featured.id}`" :aria-label="featured.title">
        <AssetMedia :src="featured.mediaUrl" :kind="featured.mediaKind" :alt="featured.title" :width="featured.width || 1600" :height="featured.height || 1000" :controls="false" eager />
      </RouterLink>
      <div class="hero-copy">
        <span class="hero-eyebrow">{{ featured.licenseCode.startsWith('demo') ? t('status.demo') : t('discover.featured') }}</span>
        <h2>{{ featured.title }}</h2>
        <p>{{ featured.summary }}</p>
        <div class="work-byline">
          <RouterLink :to="`/creators/${featured.author.handle}`">
            <strong>{{ featured.author.displayName }}</strong>
          </RouterLink>
          <span>{{ featured.modelName }}</span>
        </div>
        <div class="hero-actions">
          <RouterLink class="command-button primary" :to="`/create/image?sourceWorkId=${featured.id}`">
            {{ t('actions.remix') }}
            <ArrowRight :size="17" :stroke-width="1.75" />
          </RouterLink>
          <RouterLink class="text-link" :to="`/works/${featured.id}`">
            {{ t('actions.viewWork') }}
          </RouterLink>
        </div>
      </div>
    </div>
    <div v-else class="discover-empty content-width">
      <div>
        <span class="hero-eyebrow">{{ query ? t('discover.noSearchResults') : t('discover.emptyTitle') }}</span>
        <h2>{{ query ? t('discover.tryAnotherSearch') : t('discover.emptySummary') }}</h2>
      </div>
      <RouterLink class="command-button primary" to="/create/image">
        <Sparkles :size="17" :stroke-width="1.75" />{{ t('actions.startCreating') }}
      </RouterLink>
    </div>

    <section id="recent" class="content-section content-width">
      <div class="section-heading">
        <h2>{{ t('discover.recent') }}</h2>
        <p>{{ t('discover.recentSummary') }}</p>
      </div>
      <div v-if="supporting.length" class="work-grid">
        <RouterLink v-for="work in supporting" :key="work.id" class="work-card" :to="`/works/${work.id}`">
          <div class="work-card-media">
            <AssetMedia :src="work.mediaUrl" :kind="work.mediaKind" :alt="work.title" :width="work.width || 800" :height="work.height || 800" :controls="false" />
          </div>
          <div class="work-card-copy">
            <h3>{{ work.title }}</h3>
            <span>{{ work.author.displayName }} · {{ work.modelName }}</span>
          </div>
        </RouterLink>
      </div>
      <div v-else class="inline-empty">
        <p>{{ t('discover.noRecent') }}</p>
        <RouterLink class="text-link" to="/create/image">
          {{ t('actions.createFirstWork') }} <ArrowRight :size="16" />
        </RouterLink>
      </div>
    </section>

    <section class="discovery-rail content-width">
      <RouterLink to="/market/demands">
        <strong>{{ t('discover.openDemands') }}</strong>
        <ArrowRight :size="18" :stroke-width="1.75" />
      </RouterLink>
      <RouterLink to="/market">
        <strong>{{ t('discover.promptLibrary') }}</strong>
        <ArrowRight :size="18" :stroke-width="1.75" />
      </RouterLink>
    </section>
  </section>
</template>
