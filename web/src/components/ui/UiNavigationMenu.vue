<script setup lang="ts">
import { ChevronDown } from 'lucide-vue-next'
import { onBeforeUnmount, onMounted, ref } from 'vue'
type Link = { label: string; href: string; description?: string }
type NavigationMenuItem = { id: string; label: string; href?: string; links?: Link[] }
withDefaults(defineProps<{ items: NavigationMenuItem[]; label?: string; orientation?: 'horizontal' | 'vertical' }>(), { label: 'Main navigation', orientation: 'horizontal' })
const root = ref<globalThis.HTMLElement | null>(null)
const open = ref('')
let closeTimer: ReturnType<typeof globalThis.setTimeout> | undefined
function show(id: string) { globalThis.clearTimeout(closeTimer); open.value = id }
function scheduleClose() { globalThis.clearTimeout(closeTimer); closeTimer = globalThis.setTimeout(() => { open.value = '' }, 120) }
function outside(event: globalThis.PointerEvent) { if (!root.value?.contains(event.target as globalThis.Node)) open.value = '' }
onMounted(() => globalThis.document.addEventListener('pointerdown', outside))
onBeforeUnmount(() => { globalThis.clearTimeout(closeTimer); globalThis.document.removeEventListener('pointerdown', outside) })
</script>

<template><nav ref="root" class="ui-navigation-menu" :data-orientation="orientation" :aria-label="label" @mouseleave="scheduleClose"><ul><li v-for="item in items" :key="item.id" @mouseenter="item.links?.length ? show(item.id) : undefined"><a v-if="!item.links?.length" :href="item.href">{{ item.label }}</a><button v-else type="button" :aria-expanded="open === item.id" @focus="show(item.id)" @click="open = open === item.id ? '' : item.id">{{ item.label }}<ChevronDown :size="14" aria-hidden="true" /></button><Transition name="ui-dropdown"><div v-if="open === item.id && item.links?.length" class="ui-navigation-menu__content"><a v-for="link in item.links" :key="link.href" :href="link.href"><strong>{{ link.label }}</strong><span v-if="link.description">{{ link.description }}</span></a></div></Transition></li></ul></nav></template>
