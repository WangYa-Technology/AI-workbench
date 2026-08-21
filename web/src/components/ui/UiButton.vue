<script setup lang="ts">
import { computed, useAttrs } from 'vue'
import { RouterLink } from 'vue-router'
import { LoaderCircle } from 'lucide-vue-next'

defineOptions({ inheritAttrs: false })

type Variant = 'primary' | 'primary-outline' | 'secondary' | 'soft' | 'outline' | 'ghost' | 'destructive' | 'light'
type Size = 'sm' | 'md' | 'lg' | 'icon-sm' | 'icon-md' | 'icon-lg'

const props = withDefaults(defineProps<{
  as?: 'button' | 'a' | 'RouterLink'
  type?: 'button' | 'submit' | 'reset'
  variant?: Variant
  size?: Size
  loading?: boolean
  disabled?: boolean
  wide?: boolean
}>(), { as: 'button', type: 'button', variant: 'primary', size: 'md', loading: false, disabled: false, wide: false })

const attrs = useAttrs()
const component = computed(() => props.as === 'RouterLink' ? RouterLink : props.as)
const componentAttrs = computed(() => ({ ...attrs, ...(props.as === 'button' ? { type: props.type, disabled: props.disabled || props.loading } : { 'aria-disabled': props.disabled || props.loading ? 'true' : undefined, tabindex: props.disabled || props.loading ? -1 : undefined }) }))
const onClick = (event: globalThis.MouseEvent) => {
  if (props.as !== 'button' && (props.disabled || props.loading)) {
    event.preventDefault()
    event.stopImmediatePropagation()
  }
}
</script>

<template>
  <component :is="component" v-bind="componentAttrs" class="ui-button" :class="{ 'ui-button--wide': wide }" :data-variant="variant" :data-size="size" :data-loading="loading ? 'true' : 'false'" @click="onClick">
    <LoaderCircle v-if="loading" class="ui-button__spinner" :size="15" aria-hidden="true" />
    <slot name="start"></slot>
    <span v-if="$slots.default"><slot></slot></span>
    <slot name="end"></slot>
  </component>
</template>

<style scoped>
.ui-button--wide { width: 100%; }
</style>
