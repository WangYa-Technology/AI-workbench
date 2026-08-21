<script setup lang="ts">
defineOptions({ inheritAttrs: false })
withDefaults(defineProps<{ modelValue?: string | number; size?: 'sm' | 'md' | 'lg'; invalid?: boolean }>(), { modelValue: '', size: 'md', invalid: false })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <input v-bind="$attrs" class="ui-input" :class="{ 'ui-input--invalid': invalid }" :data-size="size" :value="modelValue" @input="emit('update:modelValue', ($event.target as HTMLInputElement).value)" />
</template>
