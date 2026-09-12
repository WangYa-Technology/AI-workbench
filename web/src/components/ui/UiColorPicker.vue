<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import UiColorArea from './UiColorArea.vue'
import UiColorSlider from './UiColorSlider.vue'
import { hexToHsl, hslToHex, normalizeHex } from './uiColor'
const props = withDefaults(defineProps<{ modelValue?: string; disabled?: boolean; label?: string; hexLabel?: string }>(), { modelValue: '#1F63E9', disabled: false, label: 'Choose color', hexLabel: 'Hex color' })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const draft = ref(normalizeHex(props.modelValue) ?? '#1F63E9')
watch(() => props.modelValue, value => { draft.value = normalizeHex(value) ?? draft.value })
const hue = computed(() => hexToHsl(draft.value).h)
function update(value: string) { draft.value = value; emit('update:modelValue', value) }
function updateHue(value: number) { const current = hexToHsl(draft.value); update(hslToHex({ ...current, h: value })) }
function input(event: globalThis.Event) { const value = normalizeHex((event.target as globalThis.HTMLInputElement).value); if (value) update(value) }
</script>

<template>
  <fieldset class="ui-color-picker" :disabled="disabled">
    <legend class="sr-only">
      {{ label }}
    </legend><UiColorArea :model-value="draft" :hue="hue" @update:model-value="update" /><UiColorSlider :model-value="hue" channel="hue" @update:model-value="updateHue" /><div class="ui-color-picker__value">
      <input type="color" :value="draft" :aria-label="label" @input="update(($event.target as globalThis.HTMLInputElement).value.toUpperCase())" /><input class="ui-input" :value="draft" maxlength="7" spellcheck="false" :aria-label="hexLabel" @change="input" />
    </div>
  </fieldset>
</template>
