<script setup lang="ts">
defineProps<{ title: string; message?: string }>()
</script>

<template>
  <div class="ui-empty-state">
    <slot name="icon"></slot><h2>{{ title }}</h2><p v-if="message">
      {{ message }}
    </p><slot></slot>
  </div>
</template>
