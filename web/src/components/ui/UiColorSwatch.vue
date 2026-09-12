<script setup lang="ts">
withDefaults(defineProps<{ color: string; label?: string; selected?: boolean; disabled?: boolean; size?: 'sm' | 'md' | 'lg' }>(), { label: '', selected: false, disabled: false, size: 'md' })
const emit = defineEmits<{ select: [color: string] }>()
</script>

<template>
  <button class="ui-color-swatch" type="button" :data-size="size" :style="{ '--ui-swatch': color }" :aria-label="label || color" :aria-pressed="selected" :disabled="disabled" @click="emit('select', color)">
    <span aria-hidden="true"></span>
  </button>
</template>
