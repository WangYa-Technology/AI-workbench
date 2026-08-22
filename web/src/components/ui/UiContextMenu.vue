<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
type MenuItem = { id: string; label?: string; disabled?: boolean; danger?: boolean; separator?: boolean; shortcut?: string }
withDefaults(defineProps<{ items: MenuItem[]; label?: string }>(), { label: 'Context menu' })
const emit = defineEmits<{ select: [id: string] }>()
const open = ref(false)
const x = ref(0)
const y = ref(0)
const menu = ref<globalThis.HTMLElement | null>(null)
const active = ref(0)
function show(event: globalThis.MouseEvent) { event.preventDefault(); x.value = Math.min(event.clientX, globalThis.innerWidth - 220); y.value = Math.min(event.clientY, globalThis.innerHeight - 260); open.value = true; active.value = 0; nextTick(() => menu.value?.focus()) }
function select(item: MenuItem) { if (item.disabled || item.separator) return; emit('select', item.id); open.value = false }
function keydown(event: globalThis.KeyboardEvent) { const buttons = [...(menu.value?.querySelectorAll<globalThis.HTMLButtonElement>('button:not(:disabled)') ?? [])]; if (event.key === 'Escape') open.value = false; else if ((event.key === 'ArrowDown' || event.key === 'ArrowUp') && buttons.length) { event.preventDefault(); active.value = (active.value + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length; buttons[active.value]?.focus() } }
function close(event: globalThis.PointerEvent) { if (open.value && !menu.value?.contains(event.target as globalThis.Node)) open.value = false }
onMounted(() => globalThis.document.addEventListener('pointerdown', close))
onBeforeUnmount(() => globalThis.document.removeEventListener('pointerdown', close))
</script>

<template><div class="ui-context-menu" @contextmenu="show"><slot></slot><Teleport to="body"><Transition name="ui-dropdown"><div v-if="open" ref="menu" class="ui-context-menu__content" role="menu" :aria-label="label" tabindex="-1" :style="{ left: `${x}px`, top: `${y}px` }" @keydown="keydown"><template v-for="item in items" :key="item.id"><div v-if="item.separator" class="ui-menu-separator" role="separator"></div><button v-else type="button" role="menuitem" :data-danger="item.danger" :disabled="item.disabled" @click="select(item)"><span>{{ item.label }}</span><kbd v-if="item.shortcut">{{ item.shortcut }}</kbd></button></template></div></Transition></Teleport></div></template>
