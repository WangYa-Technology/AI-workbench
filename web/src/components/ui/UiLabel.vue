<script setup lang="ts">
withDefaults(defineProps<{ forId?: string; required?: boolean }>(), { forId: undefined, required: false })
</script>

<template><label class="ui-form-field__label" :for="forId"><slot></slot><span v-if="required" aria-hidden="true"> *</span></label></template>
