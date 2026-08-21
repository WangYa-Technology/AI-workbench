<script setup lang="ts">
defineOptions({ inheritAttrs: false })
withDefaults(defineProps<{ modelValue?: string; value: string }>(), { modelValue: '' })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template><input v-bind="$attrs" class="ui-radio" type="radio" :value="value" :checked="modelValue === value" @change="emit('update:modelValue', value)" /></template>
