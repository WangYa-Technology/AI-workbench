<script setup lang="ts">
withDefaults(defineProps<{ attached?: boolean }>(), { attached: false })
</script>

<template>
  <div class="ui-button-group" :data-attached="attached ? 'true' : 'false'">
    <slot></slot>
  </div>
</template>
