<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
withDefaults(defineProps<{ href?: string; openDelay?: number; closeDelay?: number; placement?: 'top' | 'bottom' }>(), { href: '#', openDelay: 180, closeDelay: 100, placement: 'top' })
const open = ref(false)
let timer: ReturnType<typeof globalThis.setTimeout> | undefined
function schedule(value: boolean, delay: number) { globalThis.clearTimeout(timer); timer = globalThis.setTimeout(() => { open.value = value }, delay) }
onBeforeUnmount(() => globalThis.clearTimeout(timer))
</script>

<template><span class="ui-preview-card" :data-placement="placement" @mouseenter="schedule(true, openDelay)" @mouseleave="schedule(false, closeDelay)" @focusin="schedule(true, 0)" @focusout="schedule(false, closeDelay)"><a :href="href"><slot name="trigger"></slot></a><Transition name="ui-popover"><span v-if="open" class="ui-preview-card__content" role="tooltip"><slot></slot></span></Transition></span></template>
