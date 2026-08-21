<script setup lang="ts">
import { computed } from 'vue'

defineOptions({ inheritAttrs: false })
const props = withDefaults(defineProps<{ modelValue?: boolean | string[]; value?: string; checked?: boolean }>(), { modelValue: false, value: undefined, checked: undefined })
const emit = defineEmits<{ 'update:modelValue': [value: boolean | string[]]; change: [event: globalThis.Event] }>()
const checked = computed(() => Array.isArray(props.modelValue) ? props.value !== undefined && props.modelValue.includes(props.value) : props.checked !== undefined ? props.checked : Boolean(props.modelValue))

function update(event: { target: unknown }) {
  const nextChecked = (event.target as { checked: boolean }).checked
  if (Array.isArray(props.modelValue) && props.value !== undefined) {
    const next = new Set(props.modelValue)
    if (nextChecked) next.add(props.value)
    else next.delete(props.value)
    emit('update:modelValue', [...next])
    emit('change', event as globalThis.Event)
    return
  }
  emit('update:modelValue', nextChecked)
  emit('change', event as globalThis.Event)
}
</script>

<template><input v-bind="$attrs" class="ui-checkbox" type="checkbox" :value="value" :checked="checked" @change="update" /></template>
