<script setup lang="ts">
defineProps<{ items?: unknown[] }>()
</script>

<template>
  <div class="ui-data-list">
    <slot></slot>
  </div>
</template>
