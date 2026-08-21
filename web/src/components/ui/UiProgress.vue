<script setup lang="ts">
withDefaults(defineProps<{ value?: number; max?: number }>(), { value: 0, max: 100 })
</script>

<template><progress class="ui-progress" :value="value" :max="max"></progress></template>
