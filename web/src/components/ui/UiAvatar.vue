<script setup lang="ts">
withDefaults(defineProps<{ src?: string; alt?: string; initials?: string }>(), { src: '', alt: '', initials: '?' })
</script>

<template><span class="ui-avatar"><img v-if="src" :src="src" :alt="alt" /><span v-else>{{ initials }}</span></span></template>
