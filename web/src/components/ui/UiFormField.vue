<script setup lang="ts">
withDefaults(defineProps<{ label?: string; hint?: string; error?: string; required?: boolean; forId?: string }>(), { label: '', hint: '', error: '', required: false, forId: undefined })
</script>

<template>
  <label class="ui-form-field" :for="forId">
    <span v-if="label" class="ui-form-field__label">{{ label }}<span v-if="required" aria-hidden="true"> *</span></span>
    <slot></slot>
    <p v-if="error" class="ui-form-field__error" role="alert">{{ error }}</p>
    <p v-else-if="hint" class="ui-form-field__hint">{{ hint }}</p>
  </label>
</template>
