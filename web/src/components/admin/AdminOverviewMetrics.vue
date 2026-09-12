<script setup lang="ts">
import type { AdminOverview } from '../../api/client'

defineProps<{
  overview: AdminOverview
  label: (key: string) => string
  statusLabel: (key: 'users' | 'works' | 'generations' | 'orders' | 'tasks' | 'risks' | 'providers', status: string) => string
}>()

const keys = ['users', 'works', 'generations', 'orders', 'tasks', 'risks', 'providers'] as const
</script>

<template>
  <div class="admin-overview-grid overview-metrics-grid" role="list" :aria-label="label('overview')">
    <article v-for="key in keys" :key="key" role="listitem">
      <span>{{ label(`metrics.${key}`) }}</span>
      <strong>{{ overview[key].total }}</strong>
      <div class="admin-metric-breakdown">
        <small v-for="(count, status) in overview[key].byStatus" :key="status"><span>{{ statusLabel(key, status) }}</span><b>{{ count }}</b></small>
      </div>
    </article>
  </div>
</template>
