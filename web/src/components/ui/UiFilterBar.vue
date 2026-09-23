<script setup lang="ts">
withDefaults(defineProps<{
  as?: 'form' | 'div' | 'nav'
  fields?: boolean
  split?: boolean
  density?: 'regular' | 'compact'
  layout?: 'wrap' | 'grid'
}>(), { as: 'form', fields: false, split: false, density: 'regular', layout: 'wrap' })
</script>

<template>
  <component :is="as" class="ui-filter-bar" :data-density="density" :class="{ 'ui-filter-bar--fields': fields, 'ui-filter-bar--split': split, 'ui-filter-bar--grid': layout === 'grid' }">
    <slot></slot>
  </component>
</template>

<style src="./filter-bar.css"></style>
