<script setup lang="ts">
withDefaults(defineProps<{ label?: string; description?: string; error?: string; required?: boolean; forId?: string }>(), { label: '', description: '', error: '', required: false, forId: undefined })
</script>

<template>
  <div class="ui-field" :data-invalid="Boolean(error)">
    <label v-if="label" class="ui-field__label" :for="forId">{{ label }}<span v-if="required" aria-hidden="true"> *</span></label><slot></slot><p v-if="error" class="ui-field__error" role="alert">
      {{ error }}
    </p><p v-else-if="description" class="ui-field__description">
      {{ description }}
    </p>
  </div>
</template>
