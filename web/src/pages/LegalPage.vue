<script setup lang="ts">
import { ArrowUpRight, BookOpen, FileWarning, ShieldCheck } from 'lucide-vue-next'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'

const { t } = useI18n()
const route = useRoute()
const topics = ['terms', 'privacy', 'cookies', 'acceptable', 'ai', 'licensing', 'refunds', 'copyright'] as const
const active = computed(() => topics.includes(String(route.params.policy) as typeof topics[number]) ? String(route.params.policy) : 'terms')
</script>

<template>
  <section class="legal-page content-width">
    <header class="legal-header">
      <span class="status-label"><BookOpen :size="14" />{{ t('legal.policiesLabel') }}</span><h1>{{ t('legal.title') }}</h1><p>{{ t('legal.summary') }}</p>
    </header>
    <div class="legal-layout">
      <nav :aria-label="t('legal.title')">
        <RouterLink v-for="topic in topics" :key="topic" :to="`/policies/${topic}`" :class="{ active: active === topic }">
          {{ t(`legal.topics.${topic}.title`) }}
        </RouterLink>
      </nav>
      <article class="legal-policy-copy">
        <span>{{ t('legal.policiesLabel') }}</span><h2>{{ t(`legal.topics.${active}.title`) }}</h2><p>{{ t(`legal.topics.${active}.summary`) }}</p>
        <div v-if="active === 'copyright'" class="legal-intake">
          <ShieldCheck :size="22" /><div>
            <h3>{{ t('legal.copyrightTitle') }}</h3><p>{{ t('legal.copyrightSummary') }}</p><RouterLink class="command-button primary" to="/support">
              <span>{{ t('legal.openSupport') }}</span><ArrowUpRight :size="16" />
            </RouterLink>
          </div>
        </div>
        <div class="legal-boundary">
          <FileWarning :size="20" /><div><h3>{{ t('legal.boundaryTitle') }}</h3><p>{{ t('legal.boundarySummary') }}</p></div>
        </div>
      </article>
    </div>
  </section>
</template>
