<script setup lang="ts">
import { ArrowRight, BadgeCheck, CalendarDays, RefreshCw, Sparkles, UserPlus, UsersRound } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'
import { api, messageFrom, type CreatorProfile } from '../api/client'
import AssetMedia from '../components/domain/AssetMedia.vue'
import { formatCurrency } from '../lib/format'
import { useSessionStore } from '../stores/session'
import UiButton from '../components/ui/UiButton.vue'

const { t, locale } = useI18n()
const route = useRoute()
const session = useSessionStore()
const profile = ref<CreatorProfile | null>(null)
const loading = ref(true)
const actionLoading = ref(false)
const error = ref('')
const actionError = ref('')
const isSelf = computed(() => session.user?.id === profile.value?.id)

async function load() {
  loading.value = true
  error.value = ''
  try {
    await session.ensure()
    profile.value = await api.getCreator(String(route.params.handle))
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    loading.value = false
  }
}

async function toggleFollow() {
  if (!profile.value || !session.user) return
  actionLoading.value = true
  actionError.value = ''
  try {
    const state = await api.setCommunityFollow(profile.value.id, !profile.value.viewerFollowing)
    profile.value = {
      ...profile.value,
      viewerFollowing: state.following,
      followerCount: Math.max(0, profile.value.followerCount + (state.following ? 1 : -1)),
    }
  } catch (reason) {
    actionError.value = messageFrom(reason)
  } finally {
    actionLoading.value = false
  }
}

function money(cents: number, currency: string) {
  return formatCurrency(cents, currency, locale.value)
}

function memberDate(value: string) {
  return new Intl.DateTimeFormat(locale.value, { month: 'long', year: 'numeric' }).format(new Date(value))
}

watch(() => route.params.handle, () => void load(), { immediate: true })
</script>

<template>
  <section class="creator-page">
    <div v-if="loading" class="page-state content-width" aria-live="polite">
      {{ t('creator.loading') }}
    </div>
    <div v-else-if="error" class="page-state content-width" role="alert">
      <p>{{ error }}</p><UiButton class="command-button secondary" variant="secondary" @click="load">
        <template #start>
          <RefreshCw :size="17" />
        </template>{{ t('actions.retry') }}
      </UiButton>
    </div>
    <template v-else-if="profile">
      <header class="creator-header content-width">
        <div class="creator-avatar" aria-hidden="true">
          {{ profile.displayName.slice(0, 1) }}
        </div>
        <div class="creator-identity">
          <span class="status-label">{{ t('creator.publicProfile') }}</span>
          <h1>{{ profile.displayName }}</h1>
          <p>@{{ profile.handle }}</p>
          <div class="creator-evidence">
            <span><UsersRound :size="15" />{{ t('creator.followers', { count: profile.followerCount }) }}</span>
            <span>{{ t('creator.followingCount', { count: profile.followingCount }) }}</span>
            <span><CalendarDays :size="15" />{{ t('creator.memberSince', { date: memberDate(profile.memberSince) }) }}</span>
          </div>
        </div>
        <UiButton v-if="session.user && !isSelf" class="command-button secondary" variant="secondary" :loading="actionLoading" @click="toggleFollow">
          <template #start>
            <UserPlus v-if="!actionLoading" :size="17" />
          </template>{{ profile.viewerFollowing ? t('community.following') : t('community.follow') }}
        </UiButton>
        <UiButton v-else-if="!session.user" as="RouterLink" class="command-button secondary" variant="secondary" :to="{ path: '/auth', query: { returnTo: route.fullPath } }">
          <template #start>
            <UserPlus :size="17" />
          </template>{{ t('creator.signInToFollow') }}
        </UiButton>
        <UiButton v-else as="RouterLink" class="command-button secondary" variant="secondary" to="/settings">
          {{ t('creator.editAccount') }}
        </UiButton>
        <p v-if="actionError" class="inline-error" role="alert">
          {{ actionError }}
        </p>
      </header>

      <section v-if="profile.works.length" class="creator-showcase content-width" :aria-label="t('creator.featuredWork')">
        <RouterLink v-for="work in profile.works.slice(0, 3)" :key="work.id" :to="`/works/${work.id}`">
          <AssetMedia :src="work.mediaUrl" :kind="work.mediaKind" :alt="work.title" :width="work.width || 1200" :height="work.height || 900" :controls="false" :eager="work === profile.works[0]" />
          <span><strong>{{ work.title }}</strong><small>{{ work.modelName }}</small></span>
        </RouterLink>
      </section>

      <section class="creator-section content-width">
        <header><div><span class="status-label">{{ t('creator.portfolioLabel') }}</span><h2>{{ t('creator.publishedWorks') }}</h2></div><span>{{ profile.works.length }}</span></header>
        <div v-if="profile.works.length" class="creator-work-grid">
          <RouterLink v-for="work in profile.works" :key="work.id" :to="`/works/${work.id}`">
            <AssetMedia :src="work.mediaUrl" :kind="work.mediaKind" :alt="work.title" :width="work.width || 900" :height="work.height || 700" :controls="false" />
            <div><h3>{{ work.title }}</h3><span><Sparkles :size="14" />{{ work.aiDisclosure }}</span></div>
          </RouterLink>
        </div>
        <p v-else class="inline-empty">
          {{ t('creator.noWorks') }}
        </p>
      </section>

      <section v-if="profile.products.length" class="creator-section content-width">
        <header><div><span class="status-label">{{ t('creator.marketLabel') }}</span><h2>{{ t('creator.activeProducts') }}</h2></div><span>{{ profile.products.length }}</span></header>
        <div class="creator-product-list">
          <RouterLink v-for="product in profile.products" :key="product.id" :to="`/market/assets/${product.id}`">
            <AssetMedia :src="product.mediaUrl" :kind="product.mediaKind" :alt="product.title" :width="320" :height="240" :controls="false" />
            <div><span>{{ t(`marketplace.types.${product.productType}`) }} · {{ product.licenseCode }}</span><h3>{{ product.title }}</h3><p>{{ product.description }}</p><small><BadgeCheck :size="14" />{{ product.aiDisclosure }}</small></div>
            <strong>{{ money(product.priceCents, product.currency) }}</strong><ArrowRight :size="18" />
          </RouterLink>
        </div>
      </section>
    </template>
  </section>
</template>
