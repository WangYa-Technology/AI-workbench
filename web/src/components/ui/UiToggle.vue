<script setup lang="ts">
withDefaults(defineProps<{ modelValue?: boolean; disabled?: boolean; label?: string; variant?: 'soft' | 'outline' | 'ghost'; size?: 'sm' | 'md' | 'lg' }>(), { modelValue: false, disabled: false, label: '', variant: 'soft', size: 'md' })
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
</script>

<template><button class="ui-toggle" type="button" :data-variant="variant" :data-size="size" :aria-pressed="modelValue" :aria-label="label || undefined" :disabled="disabled" @click="emit('update:modelValue', !modelValue)"><slot></slot></button></template>
