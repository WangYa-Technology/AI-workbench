<script setup lang="ts">
withDefaults(defineProps<{ raised?: boolean }>(), { raised: false })
</script>

<template>
  <div class="ui-card ui-panel" :data-raised="raised ? 'true' : 'false'">
    <slot></slot>
  </div>
</template>
