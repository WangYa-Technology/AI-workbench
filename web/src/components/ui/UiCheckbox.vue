<script setup lang="ts">
import { computed } from 'vue'

defineOptions({ inheritAttrs: false })
const props = withDefaults(defineProps<{ modelValue?: boolean | string[]; value?: string }>(), { modelValue: false, value: undefined })
const emit = defineEmits<{ 'update:modelValue': [value: boolean | string[]] }>()
const checked = computed(() => Array.isArray(props.modelValue) ? props.value !== undefined && props.modelValue.includes(props.value) : Boolean(props.modelValue))

function update(event: { target: unknown }) {
  const nextChecked = (event.target as { checked: boolean }).checked
  if (Array.isArray(props.modelValue) && props.value !== undefined) {
    const next = new Set(props.modelValue)
    if (nextChecked) next.add(props.value)
    else next.delete(props.value)
    emit('update:modelValue', [...next])
    return
  }
  emit('update:modelValue', nextChecked)
}
</script>

<template><input v-bind="$attrs" class="ui-checkbox" type="checkbox" :value="value" :checked="checked" @change="update" /></template>
