<script setup lang="ts">
withDefaults(defineProps<{ count?: number; label?: string }>(), { count: 0, label: 'Notifications' })
</script>

<template><span class="ui-notification-badge" :aria-label="count ? `${label}: ${count}` : undefined">{{ count > 99 ? '99+' : count || '' }}</span></template>
