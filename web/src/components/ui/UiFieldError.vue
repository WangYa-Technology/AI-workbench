<script setup lang="ts">
defineProps<{ message?: string }>()
</script>

<template>
  <p v-if="message" class="ui-form-field__error" role="alert">
    {{ message }}
  </p>
</template>
