<script setup lang="ts">
import { Search } from 'lucide-vue-next'

defineProps<{ label: string }>()
</script>

<template>
  <label class="ui-filter-bar__search">
    <span class="sr-only">{{ label }}</span>
    <Search :size="18" :stroke-width="1.75" aria-hidden="true" />
    <slot></slot>
  </label>
</template>
