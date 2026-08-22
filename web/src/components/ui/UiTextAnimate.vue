<script setup lang="ts">
withDefaults(defineProps<{ text?: string; as?: 'span' | 'p' | 'div'; variant?: 'fade' | 'slide' | 'blur' }>(), { text: '', as: 'span', variant: 'fade' })
</script>

<template><Transition name="ui-text-animate" mode="out-in"><component :is="as" :key="text" class="ui-text-animate" :data-variant="variant"><slot>{{ text }}</slot></component></Transition></template>
