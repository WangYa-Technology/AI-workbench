<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { ChevronLeft, ChevronRight } from 'lucide-vue-next'
import { addDays, dateKey, parseDate, startOfCalendarMonth } from './uiDate'

const props = withDefaults(defineProps<{ modelValue?: string; locale?: string; min?: string; max?: string; disabledDates?: string[]; weekStartsOn?: 0 | 1; label?: string; previousMonthLabel?: string; nextMonthLabel?: string }>(), { modelValue: '', locale: 'en-US', min: '', max: '', disabledDates: () => [], weekStartsOn: 0, label: 'Choose date', previousMonthLabel: 'Previous month', nextMonthLabel: 'Next month' })
const emit = defineEmits<{ 'update:modelValue': [value: string]; monthChange: [value: string] }>()
const selected = computed(() => parseDate(props.modelValue))
const month = ref(selected.value ? new Date(selected.value.getFullYear(), selected.value.getMonth(), 1) : new Date(new Date().getFullYear(), new Date().getMonth(), 1))
const calendar = ref<globalThis.HTMLElement | null>(null)
const today = dateKey(new Date())
const disabledSet = computed(() => new Set(props.disabledDates))
const monthLabel = computed(() => new Intl.DateTimeFormat(props.locale, { month: 'long', year: 'numeric' }).format(month.value))
const weekdays = computed(() => Array.from({ length: 7 }, (_, index) => {
  const sunday = new Date(2024, 0, 7 + props.weekStartsOn + index)
  return new Intl.DateTimeFormat(props.locale, { weekday: 'short' }).format(sunday)
}))
const days = computed(() => {
  const start = startOfCalendarMonth(month.value, props.weekStartsOn)
  return Array.from({ length: 42 }, (_, index) => {
    const date = addDays(start, index)
    const key = dateKey(date)
    const disabled = Boolean((props.min && key < props.min) || (props.max && key > props.max) || disabledSet.value.has(key))
    return { date, key, day: date.getDate(), outside: date.getMonth() !== month.value.getMonth(), disabled }
  })
})
const weeks = computed(() => Array.from({ length: 6 }, (_, index) => days.value.slice(index * 7, index * 7 + 7)))

watch(() => props.modelValue, (value) => { const date = parseDate(value); if (date) month.value = new Date(date.getFullYear(), date.getMonth(), 1) })
function changeMonth(amount: number) { month.value = new Date(month.value.getFullYear(), month.value.getMonth() + amount, 1); emit('monthChange', dateKey(month.value)) }
function selectDay(key: string, disabled: boolean) { if (!disabled) emit('update:modelValue', key) }
function focusDate(key: string) { nextTick(() => calendar.value?.querySelector<globalThis.HTMLButtonElement>(`[data-date="${key}"]`)?.focus()) }
function onDayKeydown(event: globalThis.KeyboardEvent, date: Date) {
  const moves: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 }
  if (event.key in moves) { event.preventDefault(); const next = addDays(date, moves[event.key]); month.value = new Date(next.getFullYear(), next.getMonth(), 1); focusDate(dateKey(next)) }
  else if (event.key === 'PageUp' || event.key === 'PageDown') { event.preventDefault(); const next = new Date(date.getFullYear(), date.getMonth() + (event.key === 'PageUp' ? -1 : 1), date.getDate()); month.value = new Date(next.getFullYear(), next.getMonth(), 1); focusDate(dateKey(next)) }
}
</script>

<template>
  <section ref="calendar" class="ui-calendar" :aria-label="label">
    <header>
      <button type="button" class="ui-icon-button" :aria-label="previousMonthLabel" @click="changeMonth(-1)">
        <ChevronLeft :size="16" />
      </button><strong aria-live="polite">{{ monthLabel }}</strong><button type="button" class="ui-icon-button" :aria-label="nextMonthLabel" @click="changeMonth(1)">
        <ChevronRight :size="16" />
      </button>
    </header>
    <div class="ui-calendar__weekdays" aria-hidden="true">
      <span v-for="day in weekdays" :key="day">{{ day }}</span>
    </div>
    <div class="ui-calendar__grid" role="grid">
      <div v-for="(week, weekIndex) in weeks" :key="weekIndex" role="row">
        <button v-for="item in week" :key="item.key" type="button" role="gridcell" :data-date="item.key" :data-outside="item.outside" :data-today="item.key === today" :aria-selected="item.key === modelValue" :disabled="item.disabled" :tabindex="item.key === modelValue || (!modelValue && item.key === today) ? 0 : -1" @click="selectDay(item.key, item.disabled)" @keydown="onDayKeydown($event, item.date)">
          {{ item.day }}
        </button>
      </div>
    </div>
  </section>
</template>
