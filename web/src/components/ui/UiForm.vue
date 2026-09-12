<script setup lang="ts">
withDefaults(defineProps<{ disabled?: boolean; novalidate?: boolean }>(), { disabled: false, novalidate: false })
const emit = defineEmits<{ submit: [event: globalThis.SubmitEvent]; invalid: [event: globalThis.Event] }>()
function submit(event: globalThis.SubmitEvent) { const form = event.currentTarget as globalThis.HTMLFormElement; if (!form.checkValidity()) { emit('invalid', event); return } emit('submit', event) }
</script>

<template>
  <form class="ui-form" :novalidate="novalidate" @submit.prevent="submit">
    <fieldset :disabled="disabled">
      <slot></slot>
    </fieldset>
  </form>
</template>
