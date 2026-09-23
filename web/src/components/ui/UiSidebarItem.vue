<script setup lang="ts">
import type { Component } from 'vue'
import { RouterLink, type RouteLocationRaw } from 'vue-router'

withDefaults(defineProps<{
  to: RouteLocationRaw
  label: string
  icon: Component
  active?: boolean
  collapsed?: boolean
  variant?: 'default' | 'back'
}>(), { active: false, collapsed: false, variant: 'default' })
</script>

<template>
  <RouterLink class="ui-sidebar-item" :class="{ active }" :to="to" :data-collapsed="collapsed" :data-variant="variant" :aria-current="active ? 'page' : undefined" :aria-label="label" :title="collapsed ? label : undefined">
    <component :is="icon" :size="18" :stroke-width="1.75" aria-hidden="true" />
    <span v-if="!collapsed">{{ label }}</span>
  </RouterLink>
</template>

<style scoped>
.ui-sidebar-item { min-width: 0; min-height: 36px; display: flex; align-items: center; gap: 10px; padding: 6px 10px; border-radius: var(--radius-control); color: var(--sidebar-text); font-size: 12px; font-weight: 480; line-height: 1.4; overflow-wrap: anywhere; transition: background var(--duration-compact) var(--ease-smooth-out), color var(--duration-compact) var(--ease-smooth-out); }
.ui-sidebar-item :deep(svg) { flex: 0 0 auto; }
.ui-sidebar-item:hover { background: var(--sidebar-raised); }
.ui-sidebar-item.active { background: var(--accent-soft); color: var(--accent-readable); box-shadow: none; }
.ui-sidebar-item:focus-visible { outline: 0; box-shadow: inset 0 0 0 2px var(--accent); }
.ui-sidebar-item[data-variant='back'] { color: var(--sidebar-muted); font-weight: 560; }
.ui-sidebar-item[data-variant='back']:hover { color: var(--sidebar-text); }
.ui-sidebar-item[data-collapsed='true'] { width: 36px; justify-content: center; gap: 0; padding-inline: 0; }
</style>
