<script setup lang="ts">
withDefaults(defineProps<{ src: string; alt?: string; selected?: boolean; disabled?: boolean; aspect?: string; label?: string }>(), { alt: '', selected: false, disabled: false, aspect: '1 / 1', label: '' })
const emit = defineEmits<{ select: [] }>()
</script>

<template>
  <button class="ui-thumbnail" type="button" :aria-pressed="selected" :aria-label="label || alt || undefined" :disabled="disabled" :style="{ aspectRatio: aspect }" @click="emit('select')">
    <img :src="src" :alt="alt" /><span v-if="$slots.default" class="ui-thumbnail__overlay"><slot></slot></span>
  </button>
</template>
