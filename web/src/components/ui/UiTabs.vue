<script setup lang="ts">
import { computed } from 'vue'
const props = withDefaults(defineProps<{ modelValue: string; items: Array<{ value: string; label: string; disabled?: boolean }>; label?: string }>(), { label: '' })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const selectedIndex = computed(() => Math.max(0, props.items.findIndex((item) => item.value === props.modelValue)))
</script>

<template>
  <div class="ui-tabs" role="tablist" :aria-label="label || undefined">
    <span class="ui-tabs__indicator" :style="{ width: `${100 / Math.max(items.length, 1)}%`, transform: `translateX(${selectedIndex * 100}%)` }" aria-hidden="true"></span>
    <button v-for="item in items" :key="item.value" class="ui-tabs__tab" type="button" role="tab" :aria-selected="item.value === modelValue" :disabled="item.disabled" @click="emit('update:modelValue', item.value)">
      {{ item.label }}
    </button>
  </div>
</template>
