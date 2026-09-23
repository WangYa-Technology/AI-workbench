<script setup lang="ts">
import { ref } from 'vue'
const input = ref<globalThis.HTMLInputElement | null>(null)
defineExpose({
  focus: (options?: globalThis.FocusOptions) => input.value?.focus(options),
  blur: () => input.value?.blur(),
  select: () => input.value?.select(),
})
defineOptions({ inheritAttrs: false })
const props = withDefaults(defineProps<{ modelValue?: string | number; modelModifiers?: { trim?: boolean; number?: boolean }; size?: 'sm' | 'md' | 'lg'; invalid?: boolean }>(), { modelValue: '', modelModifiers: undefined, size: 'md', invalid: false })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

function update(event: { target: unknown }) {
  let value = (event.target as globalThis.HTMLInputElement).value
  if (props.modelModifiers?.trim) value = value.trim()
  if (props.modelModifiers?.number || (event.target as globalThis.HTMLInputElement).type === 'number') {
    const numeric = value === '' ? '' : Number(value)
    emit('update:modelValue', numeric as string)
    return
  }
  emit('update:modelValue', value)
}
</script>

<template>
  <input ref="input" v-bind="$attrs" class="ui-input" :class="{ 'ui-input--invalid': invalid }" :data-size="size" :value="modelValue" @input="update" />
</template>
