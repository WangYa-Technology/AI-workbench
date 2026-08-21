<script setup lang="ts">
defineOptions({ inheritAttrs: false })
withDefaults(defineProps<{ modelValue?: string; invalid?: boolean }>(), { modelValue: '', invalid: false })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <textarea v-bind="$attrs" class="ui-textarea" :class="{ 'ui-textarea--invalid': invalid }" :value="modelValue" @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"></textarea>
</template>
