<script setup lang="ts">
import { RouterLink, type RouteLocationRaw } from 'vue-router'
withDefaults(defineProps<{ as?: 'article' | 'div' | 'button'; to?: RouteLocationRaw; layout?: 'custom' | 'media' }>(), { as: 'article', to: undefined, layout: 'custom' })
</script>

<template>
  <component :is="to ? RouterLink : as" :to="to" :type="!to && as === 'button' ? 'button' : undefined" class="ui-content-card" :class="{ 'ui-content-card--media': layout === 'media' }">
    <slot></slot>
  </component>
</template>
