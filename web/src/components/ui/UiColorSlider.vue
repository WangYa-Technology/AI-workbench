<script setup lang="ts">
withDefaults(defineProps<{ modelValue?: number; channel?: 'hue' | 'alpha'; color?: string; disabled?: boolean; label?: string }>(), { modelValue: 0, channel: 'hue', color: '#1F63E9', disabled: false, label: '' })
const emit = defineEmits<{ 'update:modelValue': [value: number] }>()
</script>

<template><input class="ui-color-slider" type="range" :data-channel="channel" :style="{ '--ui-color': color }" :min="0" :max="channel === 'hue' ? 360 : 100" :value="modelValue" :disabled="disabled" :aria-label="label || channel" @input="emit('update:modelValue', Number(($event.target as HTMLInputElement).value))" /></template>
