<script setup lang="ts">
import { Minus, Plus } from 'lucide-vue-next'
const props = withDefaults(defineProps<{ modelValue?: number | null; min?: number; max?: number; step?: number; disabled?: boolean; label?: string; decrementLabel?: string; incrementLabel?: string }>(), { modelValue: 0, min: undefined, max: undefined, step: 1, disabled: false, label: 'Number', decrementLabel: 'Decrease', incrementLabel: 'Increase' })
const emit = defineEmits<{ 'update:modelValue': [value: number | null] }>()
function clamp(value: number) { return Math.min(props.max ?? Infinity, Math.max(props.min ?? -Infinity, value)) }
function stepBy(amount: number) { emit('update:modelValue', clamp((props.modelValue ?? 0) + amount * props.step)) }
function input(event: globalThis.Event) { const value = (event.target as globalThis.HTMLInputElement).value; emit('update:modelValue', value === '' ? null : clamp(Number(value))) }
</script>

<template>
  <div class="ui-number-field">
    <button type="button" :aria-label="decrementLabel" :disabled="disabled || (min !== undefined && (modelValue ?? 0) <= min)" @click="stepBy(-1)">
      <Minus :size="15" />
    </button><input type="number" :value="modelValue ?? ''" :min="min" :max="max" :step="step" :disabled="disabled" :aria-label="label" @input="input" /><button type="button" :aria-label="incrementLabel" :disabled="disabled || (max !== undefined && (modelValue ?? 0) >= max)" @click="stepBy(1)">
      <Plus :size="15" />
    </button>
  </div>
</template>
