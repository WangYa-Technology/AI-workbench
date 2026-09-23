<script setup lang="ts">
import type { Component } from 'vue'

export type PageHeaderTone = 'blue' | 'violet' | 'green'

export interface PageHeaderStat {
  value: string | number
  label: string
  icon: Component
  tone: PageHeaderTone
}

withDefaults(defineProps<{
  eyebrow?: string
  eyebrowIcon?: Component
  title: string
  summary: string
  stats?: PageHeaderStat[]
  statsLabel?: string
  statFormat?: 'number' | 'text'
  artworkSrc?: string
  artworkWidth?: number
  artworkHeight?: number
}>(), {
  stats: () => [],
  eyebrow: '',
  eyebrowIcon: undefined,
  statsLabel: undefined,
  statFormat: 'number',
  artworkSrc: '',
  artworkWidth: 256,
  artworkHeight: 256,
})
</script>

<template>
  <header class="page-hero-header page-hero-banner ui-page-hero ui-page-header" data-page-heading :data-stat-format="statFormat">
    <div class="page-header-main">
      <div class="page-header-identity">
        <div v-if="artworkSrc || $slots.visual" class="page-header-visual" aria-hidden="true">
          <slot name="visual">
            <img class="page-hero-art" :src="artworkSrc" alt="" :width="artworkWidth" :height="artworkHeight" />
          </slot>
        </div>
        <div class="page-hero-copy">
          <span v-if="eyebrow" class="page-hero-eyebrow">
            <component :is="eyebrowIcon" v-if="eyebrowIcon" :size="14" :stroke-width="1.75" aria-hidden="true" />
            {{ eyebrow }}
          </span>
          <h1>{{ title }}</h1>
          <p v-if="summary">
            {{ summary }}
          </p>
        </div>
      </div>
      <div v-if="$slots.actions" class="page-hero-actions">
        <slot name="actions"></slot>
      </div>
    </div>
    <div v-if="stats.length" class="page-hero-stats" :aria-label="statsLabel">
      <article v-for="stat in stats" :key="stat.label">
        <span class="page-hero-stat-icon" :data-tone="stat.tone"><component :is="stat.icon" :size="16" :stroke-width="1.75" aria-hidden="true" /></span>
        <div><strong>{{ stat.value }}</strong><span>{{ stat.label }}</span></div>
      </article>
    </div>
    <div v-if="$slots.notice" class="page-header-notice" role="note">
      <slot name="notice"></slot>
    </div>
  </header>
</template>

<style src="./page-hero.css"></style>
