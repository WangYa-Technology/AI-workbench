<script setup lang="ts">
withDefaults(defineProps<{ modelValue?: boolean; label?: string; disabled?: boolean }>(), { modelValue: false, label: '', disabled: false })
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
</script>

<template>
  <button class="ui-switch" type="button" role="switch" :aria-checked="modelValue" :aria-label="label || undefined" :disabled="disabled" :data-checked="modelValue ? 'true' : 'false'" @click="emit('update:modelValue', !modelValue)">
    <span class="ui-switch__thumb"></span>
  </button>
</template>
