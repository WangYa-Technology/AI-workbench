<script setup lang="ts">
defineProps<{ grid?: boolean }>()
</script>

<template>
  <div class="ui-catalog" :class="{ 'is-grid': grid }">
    <slot></slot>
  </div>
</template>

<style src="../../styles/catalog.css"></style>
