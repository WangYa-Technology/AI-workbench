<script setup lang="ts">
import { computed, useAttrs } from 'vue'
import { RouterLink } from 'vue-router'

defineOptions({ inheritAttrs: false })
const props = withDefaults(defineProps<{
  as?: 'button' | 'a' | 'RouterLink'
  type?: 'button' | 'submit' | 'reset'
  variant?: 'default' | 'soft' | 'outline' | 'ghost'
  size?: 'sm' | 'md' | 'lg'
  label: string
  disabled?: boolean
}>(), { as: 'button', type: 'button', variant: 'default', size: 'md', disabled: false })
const attrs = useAttrs()
const component = computed(() => props.as === 'RouterLink' ? RouterLink : props.as)
const componentAttrs = computed(() => ({ ...attrs, 'aria-label': props.label, title: attrs.title || props.label, ...(props.as === 'button' ? { type: props.type, disabled: props.disabled } : { 'aria-disabled': props.disabled ? 'true' : undefined }) }))
</script>

<template>
  <component :is="component" v-bind="componentAttrs" class="ui-icon-button" :data-variant="variant" :data-size="size">
    <slot></slot>
  </component>
</template>
