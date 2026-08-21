<script setup lang="ts">
import UiDrawer from './UiDrawer.vue'
defineProps<{ open?: boolean }>()
const emit = defineEmits<{ 'update:open': [value: boolean] }>()
</script>

<template>
  <UiDrawer :open="open" side="right" @update:open="emit('update:open', $event)">
    <slot></slot>
  </UiDrawer>
</template>
