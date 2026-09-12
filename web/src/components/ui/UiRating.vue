<script setup lang="ts">
import { Star } from 'lucide-vue-next'
import { ref } from 'vue'
const props = withDefaults(defineProps<{ modelValue?: number; max?: number; readonly?: boolean; disabled?: boolean; label?: string; allowClear?: boolean }>(), { modelValue: 0, max: 5, readonly: false, disabled: false, label: 'Rating', allowClear: false })
const emit = defineEmits<{ 'update:modelValue': [value: number] }>()
const hover = ref(0)
function select(value: number) { if (!props.readonly && !props.disabled) emit('update:modelValue', props.allowClear && value === props.modelValue ? 0 : value) }
</script>

<template>
  <div class="ui-rating" :role="readonly ? 'img' : 'radiogroup'" :aria-label="`${label}: ${modelValue} of ${max}`" @mouseleave="hover = 0">
    <button v-for="value in max" :key="value" type="button" :role="readonly ? undefined : 'radio'" :aria-checked="readonly ? undefined : value === modelValue" :aria-label="`${value} of ${max}`" :disabled="disabled || readonly" :data-filled="value <= (hover || modelValue)" @mouseenter="hover = value" @focus="hover = value" @blur="hover = 0" @click="select(value)">
      <Star :size="20" aria-hidden="true" />
    </button>
  </div>
</template>
