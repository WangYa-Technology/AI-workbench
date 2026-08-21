<script setup lang="ts">
import UiAvatar from './UiAvatar.vue'
defineProps<{ items?: Array<{ src?: string; alt?: string; initials?: string }> }>()
</script>

<template>
  <div class="ui-avatar-group">
    <UiAvatar v-for="(item, index) in items" :key="index" v-bind="item" />
  </div>
</template>
