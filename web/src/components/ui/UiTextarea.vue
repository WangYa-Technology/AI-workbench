<script setup lang="ts">
defineOptions({ inheritAttrs: false })
const props = withDefaults(defineProps<{ modelValue?: string; modelModifiers?: { trim?: boolean }; invalid?: boolean }>(), { modelValue: '', modelModifiers: undefined, invalid: false })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

function update(event: { target: unknown }) {
  const value = (event.target as globalThis.HTMLTextAreaElement).value
  emit('update:modelValue', props.modelModifiers?.trim ? value.trim() : value)
}
</script>

<template>
  <textarea v-bind="$attrs" class="ui-textarea" :class="{ 'ui-textarea--invalid': invalid }" :value="modelValue" @input="update"></textarea>
</template>
