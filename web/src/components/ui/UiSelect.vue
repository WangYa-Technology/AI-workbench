<script setup lang="ts">
defineOptions({ inheritAttrs: false })
const props = withDefaults(defineProps<{ modelValue?: string | number; modelModifiers?: { number?: boolean }; size?: 'sm' | 'md' | 'lg'; invalid?: boolean }>(), { modelValue: '', modelModifiers: undefined, size: 'md', invalid: false })
const emit = defineEmits<{ 'update:modelValue': [value: string]; change: [event: globalThis.Event] }>()

function update(event: { target: unknown }) {
  const value = (event.target as globalThis.HTMLSelectElement).value
  emit('update:modelValue', (props.modelModifiers?.number ? Number(value) : value) as string)
  emit('change', event as globalThis.Event)
}
</script>

<template>
  <select v-bind="$attrs" class="ui-select" :class="{ 'ui-select--invalid': invalid }" :data-size="size" :value="modelValue" @change="update">
    <slot></slot>
  </select>
</template>
