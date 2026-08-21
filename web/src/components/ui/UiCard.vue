<script setup lang="ts">
withDefaults(defineProps<{ raised?: boolean }>(), { raised: false })
</script>

<template>
  <section class="ui-card" :data-raised="raised ? 'true' : 'false'">
    <slot></slot>
  </section>
</template>
