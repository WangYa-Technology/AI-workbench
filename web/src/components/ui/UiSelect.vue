<script setup lang="ts">
defineOptions({ inheritAttrs: false })
withDefaults(defineProps<{ modelValue?: string | number; size?: 'sm' | 'md' | 'lg'; invalid?: boolean }>(), { modelValue: '', size: 'md', invalid: false })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <select v-bind="$attrs" class="ui-select" :class="{ 'ui-select--invalid': invalid }" :data-size="size" :value="modelValue" @change="emit('update:modelValue', ($event.target as HTMLSelectElement).value)">
    <slot></slot>
  </select>
</template>
