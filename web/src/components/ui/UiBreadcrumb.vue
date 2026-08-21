<script setup lang="ts">
withDefaults(defineProps<{ items: Array<{ label: string; to?: string }>; label?: string }>(), { label: 'Breadcrumb' })
</script>

<template>
  <nav class="ui-breadcrumb" :aria-label="label">
    <template v-for="(item, index) in items" :key="`${item.label}-${index}`">
      <RouterLink v-if="item.to" :to="item.to">
        {{ item.label }}
      </RouterLink><span v-else aria-current="page">{{ item.label }}</span><span v-if="index < items.length - 1" aria-hidden="true">/</span>
    </template>
  </nav>
</template>
