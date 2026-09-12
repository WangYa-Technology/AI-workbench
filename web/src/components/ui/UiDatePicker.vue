<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { CalendarDays } from 'lucide-vue-next'
import UiCalendar from './UiCalendar.vue'

const props = withDefaults(defineProps<{ modelValue?: string; open?: boolean; locale?: string; min?: string; max?: string; placeholder?: string; disabled?: boolean }>(), { modelValue: '', open: undefined, locale: 'en-US', min: '', max: '', placeholder: 'Choose date', disabled: false })
const emit = defineEmits<{ 'update:modelValue': [value: string]; 'update:open': [value: boolean] }>()
const root = ref<globalThis.HTMLElement | null>(null)
const internalOpen = ref(false)
const isOpen = computed(() => props.open ?? internalOpen.value)
const display = computed(() => props.modelValue ? new Intl.DateTimeFormat(props.locale, { dateStyle: 'medium' }).format(new Date(`${props.modelValue}T00:00:00`)) : props.placeholder)
function setOpen(value: boolean) { internalOpen.value = value; emit('update:open', value) }
function select(value: string) { emit('update:modelValue', value); setOpen(false) }
function outside(event: globalThis.PointerEvent) { if (isOpen.value && !root.value?.contains(event.target as globalThis.Node)) setOpen(false) }
onMounted(() => globalThis.document.addEventListener('pointerdown', outside))
onBeforeUnmount(() => globalThis.document.removeEventListener('pointerdown', outside))
</script>

<template>
  <div ref="root" class="ui-date-picker">
    <button class="ui-date-picker__trigger" type="button" :aria-expanded="isOpen" aria-haspopup="dialog" :disabled="disabled" @click="setOpen(!isOpen)">
      <CalendarDays :size="16" aria-hidden="true" /><span :data-placeholder="!modelValue">{{ display }}</span>
    </button><Transition name="ui-dropdown">
      <div v-if="isOpen" class="ui-date-picker__popover">
        <UiCalendar :model-value="modelValue" :locale="locale" :min="min" :max="max" @update:model-value="select" />
      </div>
    </Transition>
  </div>
</template>
