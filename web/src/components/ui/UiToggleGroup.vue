<script setup lang="ts">
type Item = { value: string; label: string; disabled?: boolean }
const props = withDefaults(defineProps<{ modelValue?: string | string[]; items: Item[]; multiple?: boolean; label?: string; attached?: boolean }>(), { modelValue: '', multiple: false, label: '', attached: true })
const emit = defineEmits<{ 'update:modelValue': [value: string | string[]] }>()

function select(value: string) {
  if (!props.multiple) return emit('update:modelValue', value)
  const current = Array.isArray(props.modelValue) ? props.modelValue : []
  emit('update:modelValue', current.includes(value) ? current.filter(item => item !== value) : [...current, value])
}
function selected(value: string) { return Array.isArray(props.modelValue) ? props.modelValue.includes(value) : props.modelValue === value }
</script>

<template>
  <div class="ui-toggle-group" :data-attached="attached" :role="multiple ? 'group' : 'radiogroup'" :aria-label="label || undefined">
    <button v-for="item in items" :key="item.value" class="ui-toggle" type="button" :role="multiple ? undefined : 'radio'" :aria-checked="multiple ? undefined : selected(item.value)" :aria-pressed="multiple ? selected(item.value) : undefined" :disabled="item.disabled" @click="select(item.value)">{{ item.label }}</button>
  </div>
</template>
