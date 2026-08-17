<script setup lang="ts">
defineProps<{ title: string; summary: string; status?: string }>()
</script>

<template>
  <header class="page-intro content-width">
    <span v-if="status" class="status-label">{{ status }}</span>
    <h1>{{ title }}</h1>
    <p>{{ summary }}</p>
    <slot></slot>
  </header>
</template>
