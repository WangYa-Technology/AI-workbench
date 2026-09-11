<script setup lang="ts">
import type { Component } from 'vue'

export type PageHeroTone = 'blue' | 'violet' | 'green'

export interface PageHeroStat {
  value: string | number
  label: string
  icon: Component
  tone: PageHeroTone
}

withDefaults(defineProps<{
  eyebrow: string
  eyebrowIcon: Component
  title: string
  summary: string
  stats: PageHeroStat[]
  statsLabel: string
  artworkSrc?: string
  artworkWidth?: number
  artworkHeight?: number
  adaptArtworkForDark?: boolean
}>(), {
  artworkSrc: '',
  artworkWidth: 768,
  artworkHeight: 714,
  adaptArtworkForDark: false,
})
</script>

<template>
  <header class="page-hero-header page-hero-banner ui-page-hero">
    <div class="page-hero-copy">
      <span class="page-hero-eyebrow">
        <component :is="eyebrowIcon" :size="14" :stroke-width="1.75" aria-hidden="true" />
        {{ eyebrow }}
      </span>
      <h1>{{ title }}</h1>
      <p>{{ summary }}</p>
      <div class="page-hero-stats" :aria-label="statsLabel">
        <article v-for="stat in stats" :key="stat.label">
          <span class="page-hero-stat-icon" :data-tone="stat.tone">
            <component :is="stat.icon" :size="23" :stroke-width="1.75" aria-hidden="true" />
          </span>
          <div><strong>{{ stat.value }}</strong><span>{{ stat.label }}</span></div>
        </article>
      </div>
    </div>
    <div v-if="$slots.actions" class="page-hero-actions">
      <slot name="actions"></slot>
    </div>
    <slot name="visual">
      <img
        v-if="artworkSrc"
        class="page-hero-art"
        :class="{ 'page-hero-art--dark-adaptive': adaptArtworkForDark }"
        :src="artworkSrc"
        alt=""
        :width="artworkWidth"
        :height="artworkHeight"
        aria-hidden="true"
      />
    </slot>
  </header>
</template>
