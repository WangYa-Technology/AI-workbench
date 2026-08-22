<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{ value?: number; min?: number; max?: number; low?: number; high?: number; optimum?: number; label?: string; showValue?: boolean }>(), { value: 0, min: 0, max: 100, low: undefined, high: undefined, optimum: undefined, label: '', showValue: false })
const percent = computed(() => Math.round(((props.value - props.min) / Math.max(1, props.max - props.min)) * 100))
</script>

<template>
  <div class="ui-meter">
    <span v-if="label || showValue" class="ui-meter__label"><span>{{ label }}</span><strong v-if="showValue">{{ percent }}%</strong></span>
    <meter :value="value" :min="min" :max="max" :low="low" :high="high" :optimum="optimum" :aria-label="label || undefined"></meter>
  </div>
</template>
