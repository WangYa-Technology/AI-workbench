<script setup lang="ts">
defineProps<{ caption?: string; tableClass?: string }>()
</script>

<template>
  <div class="ui-table-wrap">
    <table class="ui-table" :class="tableClass">
      <caption v-if="caption">
        {{ caption }}
      </caption><slot></slot>
    </table>
  </div>
</template>
