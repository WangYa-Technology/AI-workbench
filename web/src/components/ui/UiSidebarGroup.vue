<script setup lang="ts">
import type { Component } from 'vue'
import type { RouteLocationRaw } from 'vue-router'
import UiSidebarItem from './UiSidebarItem.vue'

defineProps<{
  label: string
  collapsed?: boolean
  items: Array<{ key: string; label: string; to: RouteLocationRaw; icon: Component; active?: boolean }>
}>()
</script>

<template>
  <section v-if="items.length" class="nav-section" :aria-label="label">
    <span v-if="!collapsed" class="nav-section-label">{{ label }}</span>
    <UiSidebarItem v-for="item in items" :key="item.key" :to="item.to" :label="item.label" :icon="item.icon" :active="item.active" :collapsed="collapsed" />
  </section>
</template>

<style scoped>
.nav-section { display: grid; gap: 3px; }
.nav-section-label { min-height: 20px; display: flex; align-items: center; padding: 0 10px; color: var(--sidebar-muted); font-size: 9px; line-height: 1; font-weight: 650; letter-spacing: .08em; text-transform: uppercase; }
</style>
