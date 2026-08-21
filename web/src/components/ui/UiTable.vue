<script setup lang="ts">
defineProps<{ caption?: string }>()
</script>

<template>
  <div class="ui-table-wrap">
    <table class="ui-table">
      <caption v-if="caption">
        {{ caption }}
      </caption><slot></slot>
    </table>
  </div>
</template>
