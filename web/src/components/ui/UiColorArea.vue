<script setup lang="ts">
import { computed, ref, type CSSProperties } from 'vue'
import { hexToHsl, hslToHex } from './uiColor'
const props = withDefaults(defineProps<{ modelValue?: string; hue?: number; disabled?: boolean; label?: string }>(), { modelValue: '#1F63E9', hue: undefined, disabled: false, label: 'Color saturation and lightness' })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const area = ref<globalThis.HTMLElement | null>(null)
const hsl = computed(() => hexToHsl(props.modelValue))
const activeHue = computed(() => props.hue ?? hsl.value.h)
const style = computed(() => ({ '--ui-color-hue': String(activeHue.value), '--ui-color-x': `${hsl.value.s}%`, '--ui-color-y': `${100 - hsl.value.l}%` } as CSSProperties))
function update(clientX: number, clientY: number) { if (props.disabled || !area.value) return; const rect = area.value.getBoundingClientRect(); const s = Math.round(Math.max(0, Math.min(1, (clientX - rect.left) / rect.width)) * 100); const l = 100 - Math.round(Math.max(0, Math.min(1, (clientY - rect.top) / rect.height)) * 100); emit('update:modelValue', hslToHex({ h: activeHue.value, s, l })) }
function pointer(event: globalThis.PointerEvent) { (event.currentTarget as globalThis.HTMLElement).setPointerCapture(event.pointerId); update(event.clientX, event.clientY) }
function keydown(event: globalThis.KeyboardEvent) { const delta: Record<string, [number, number]> = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, 1], ArrowDown: [0, -1] }; const move = delta[event.key]; if (!move) return; event.preventDefault(); emit('update:modelValue', hslToHex({ h: activeHue.value, s: hsl.value.s + move[0], l: hsl.value.l + move[1] })) }
</script>

<template><div ref="area" class="ui-color-area" role="slider" tabindex="0" :aria-label="label" aria-valuemin="0" aria-valuemax="100" :aria-valuenow="hsl.s" :aria-disabled="disabled" :style="style" @pointerdown="pointer" @pointermove="($event.buttons ? pointer($event) : undefined)" @keydown="keydown"><span class="ui-color-area__thumb" aria-hidden="true"></span></div></template>
