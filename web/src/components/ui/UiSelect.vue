<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useAttrs, useId, useSlots, isVNode, type VNode } from 'vue'
import { Check, ChevronDown, ChevronsUpDown } from 'lucide-vue-next'

defineOptions({ inheritAttrs: false })

type SelectValue = string | number
type SelectOption = { value: string; label: string; disabled: boolean }

const props = withDefaults(defineProps<{
  modelValue?: SelectValue
  modelModifiers?: { number?: boolean }
  size?: 'sm' | 'md' | 'lg'
  variant?: 'outline' | 'soft'
  alignItemWithTrigger?: boolean
  invalid?: boolean
  disabled?: boolean
}>(), { modelValue: '', modelModifiers: undefined, size: 'md', variant: 'outline', alignItemWithTrigger: undefined, invalid: false, disabled: false })

const emit = defineEmits<{
  'update:modelValue': [value: SelectValue]
  change: [event: globalThis.Event]
}>()

const attrs = useAttrs()
const slots = useSlots()
const root = ref<globalThis.HTMLElement | null>(null)
const trigger = ref<globalThis.HTMLButtonElement | null>(null)
const menu = ref<globalThis.HTMLElement | null>(null)
const open = ref(false)
const activeIndex = ref(0)
const listboxId = `ui-select-${useId()}`
const menuStyle = ref<Record<string, string>>({})

function textFromVNode(node: unknown): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node)
  if (Array.isArray(node)) return node.map(textFromVNode).join('')
  if (isVNode(node)) return textFromVNode(node.children)
  return ''
}

function readOptions(nodes: VNode[]): SelectOption[] {
  const options: SelectOption[] = []
  for (const node of nodes) {
    if (typeof node.type === 'string' && node.type.toLowerCase() === 'option') {
      options.push({
        value: String(node.props?.value ?? ''),
        label: textFromVNode(node.children).trim(),
        disabled: Boolean(node.props?.disabled),
      })
      continue
    }
    if (Array.isArray(node.children)) options.push(...readOptions(node.children.filter(isVNode)))
  }
  return options
}

const options = computed(() => readOptions(slots.default?.() ?? []))
const stringValue = computed(() => String(props.modelValue ?? ''))
const currentOption = computed(() => options.value.find(option => option.value === stringValue.value))
const enabledIndexes = computed(() => options.value.reduce<number[]>((indexes, option, index) => {
  if (!option.disabled) indexes.push(index)
  return indexes
}, []))
const isDisabled = computed(() => props.disabled || attrs.disabled === '' || attrs.disabled === true)
const isInvalid = computed(() => props.invalid || attrs['aria-invalid'] === true || attrs['aria-invalid'] === 'true')
const triggerLabel = computed(() => currentOption.value?.label ?? '')
// Appica disables selected-item overlay when a start adornment is present.
const alignWithTrigger = computed(() => props.alignItemWithTrigger ?? !slots.start)
const typeaheadQuery = ref('')
let typeaheadTimer: ReturnType<typeof globalThis.setTimeout> | undefined

const triggerAttrs = computed(() => {
  const result: Record<string, unknown> = {}
  for (const [key, value] of Object.entries(attrs)) {
    if (['class', 'style', 'name', 'required', 'form', 'disabled', 'value'].includes(key) || key.startsWith('on')) continue
    result[key] = value
  }
  return result
})

const nativeAttrs = computed(() => {
  const result: Record<string, unknown> = {}
  for (const [key, value] of Object.entries(attrs)) {
    if (['class', 'style', 'id', 'disabled', 'value'].includes(key) || key.startsWith('on') || key.startsWith('aria-')) continue
    result[key] = value
  }
  return result
})

function emitValue(value: string) {
  const nextValue = props.modelModifiers?.number ? Number(value) : value
  emit('update:modelValue', nextValue)
  emit('change', new globalThis.Event('change', { bubbles: true }))
}

function updateMenuPosition() {
  if (!open.value || !trigger.value) return
  const rect = trigger.value.getBoundingClientRect()
  const viewportPadding = 8
  const gap = 6
  const preferredWidth = rect.width + (alignWithTrigger.value ? 16 : 0)
  const minimumWidth = alignWithTrigger.value ? 144 : rect.width
  const width = Math.min(Math.max(preferredWidth, minimumWidth), globalThis.innerWidth - viewportPadding * 2)
  const estimatedHeight = Math.min(options.value.length * 36 + 16, 320)
  const roomBelow = globalThis.innerHeight - rect.bottom - gap - viewportPadding
  const placeAbove = roomBelow < Math.min(estimatedHeight, 180) && rect.top > roomBelow
  const top = placeAbove
    ? Math.max(viewportPadding, rect.top - Math.min(estimatedHeight, rect.top - viewportPadding) - gap)
    : rect.bottom + gap
  const maxHeight = Math.max(120, Math.min(320, placeAbove ? rect.top - top - gap : roomBelow))
  const leftOffset = alignWithTrigger.value ? 8 : 0
  const left = Math.min(Math.max(viewportPadding, rect.left - leftOffset), globalThis.innerWidth - width - viewportPadding)

  menuStyle.value = {
    top: `${top}px`,
    left: `${left}px`,
    width: `${width}px`,
    maxHeight: `${maxHeight}px`,
  }
}

function scheduleMenuPosition() {
  globalThis.requestAnimationFrame(updateMenuPosition)
}

function close() {
  if (!open.value) return
  open.value = false
  nextTick(() => trigger.value?.focus())
}

function openMenu() {
  if (isDisabled.value || !options.value.length) return
  open.value = true
  const currentIndex = options.value.findIndex(option => option.value === stringValue.value && !option.disabled)
  activeIndex.value = currentIndex >= 0 ? currentIndex : (enabledIndexes.value[0] ?? 0)
  nextTick(updateMenuPosition)
}

function selectOption(index: number) {
  const option = options.value[index]
  if (!option || option.disabled || isDisabled.value) return
  emitValue(option.value)
  activeIndex.value = index
  close()
}

function moveActive(step: number) {
  const indexes = enabledIndexes.value
  if (!indexes.length) return
  const currentPosition = Math.max(0, indexes.indexOf(activeIndex.value))
  const nextPosition = (currentPosition + step + indexes.length) % indexes.length
  activeIndex.value = indexes[nextPosition]
}

function onTriggerClick() {
  if (open.value) close()
  else openMenu()
}

function onTriggerKeydown(event: globalThis.KeyboardEvent) {
  if (isDisabled.value) return
  if (event.key === 'Escape') {
    event.preventDefault()
    close()
    return
  }
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault()
    if (!open.value) openMenu()
    else moveActive(event.key === 'ArrowDown' ? 1 : -1)
    return
  }
  if (event.key === 'Home' || event.key === 'End') {
    if (!open.value) return
    event.preventDefault()
    const indexes = enabledIndexes.value
    if (indexes.length) activeIndex.value = event.key === 'Home' ? indexes[0] : indexes[indexes.length - 1]
    return
  }
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault()
    if (!open.value) openMenu()
    else selectOption(activeIndex.value)
    return
  }
  if (open.value && event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
    const nextQuery = `${typeaheadQuery.value}${event.key}`.toLocaleLowerCase()
    typeaheadQuery.value = nextQuery
    if (typeaheadTimer) globalThis.clearTimeout(typeaheadTimer)
    typeaheadTimer = globalThis.setTimeout(() => {
      typeaheadQuery.value = ''
      typeaheadTimer = undefined
    }, 500)
    const match = enabledIndexes.value.find(index => options.value[index].label.toLocaleLowerCase().startsWith(nextQuery))
    if (match !== undefined) activeIndex.value = match
  }
}

function onDocumentPointerDown(event: globalThis.PointerEvent) {
  const target = event.target as globalThis.Node
  if (root.value && !root.value.contains(target) && !menu.value?.contains(target)) close()
}

onMounted(() => {
  globalThis.document.addEventListener('pointerdown', onDocumentPointerDown)
  globalThis.addEventListener('resize', scheduleMenuPosition)
  globalThis.addEventListener('scroll', scheduleMenuPosition, true)
})
onBeforeUnmount(() => {
  globalThis.document.removeEventListener('pointerdown', onDocumentPointerDown)
  globalThis.removeEventListener('resize', scheduleMenuPosition)
  globalThis.removeEventListener('scroll', scheduleMenuPosition, true)
  if (typeaheadTimer) globalThis.clearTimeout(typeaheadTimer)
})
</script>

<template>
  <div ref="root" class="ui-select-root" :class="[attrs.class, { 'ui-select-root--open': open, 'ui-select-root--invalid': isInvalid }]" :style="attrs.style" :data-size="size" :data-variant="variant">
    <button
      :id="typeof attrs.id === 'string' ? attrs.id : undefined"
      ref="trigger"
      v-bind="triggerAttrs"
      class="ui-select__trigger"
      type="button"
      role="combobox"
      aria-haspopup="listbox"
      :aria-expanded="open"
      :aria-controls="listboxId"
      :aria-activedescendant="open ? `${listboxId}-option-${activeIndex}` : undefined"
      :aria-invalid="isInvalid ? 'true' : undefined"
      :aria-disabled="isDisabled ? 'true' : undefined"
      :disabled="isDisabled"
      :data-popup-open="open ? 'true' : 'false'"
      :data-placeholder="currentOption ? 'false' : 'true'"
      @click="onTriggerClick"
      @keydown="onTriggerKeydown"
    >
      <span v-if="$slots.start" class="ui-select__start"><slot name="start"></slot></span>
      <span class="ui-select__value" :class="{ 'ui-select__value--placeholder': !currentOption }">{{ triggerLabel }}</span>
      <ChevronsUpDown v-if="alignWithTrigger" class="ui-select__chevron" :size="18" aria-hidden="true" />
      <ChevronDown v-else class="ui-select__chevron" :size="18" aria-hidden="true" />
    </button>

    <Teleport to="body">
      <Transition name="ui-select-menu">
        <div
          v-if="open"
          :id="listboxId"
          ref="menu"
          class="ui-select__menu"
          :class="alignWithTrigger ? 'ui-select__menu--aligned' : 'ui-select__menu--detached'"
          role="listbox"
          :style="menuStyle"
          :aria-label="typeof attrs['aria-label'] === 'string' ? attrs['aria-label'] : undefined"
        >
          <div
            v-for="(option, index) in options"
            :id="`${listboxId}-option-${index}`"
            :key="`${option.value}-${index}`"
            class="ui-select__option"
            :class="{ 'ui-select__option--active': activeIndex === index, 'ui-select__option--selected': option.value === stringValue, 'ui-select__option--disabled': option.disabled }"
            role="option"
            :aria-selected="option.value === stringValue"
            :aria-disabled="option.disabled ? 'true' : undefined"
            tabindex="-1"
            @mousedown.prevent
            @click="selectOption(index)"
          >
            <span>{{ option.label }}</span>
            <Check v-if="option.value === stringValue" class="ui-select__check" :size="16" aria-hidden="true" />
          </div>
        </div>
      </Transition>
    </Teleport>

    <select v-bind="nativeAttrs" class="ui-select-native" :value="stringValue" :disabled="isDisabled" aria-hidden="true" tabindex="-1">
      <option
        v-for="(option, index) in options"
        :key="`${option.value}-${index}`"
        :value="option.value"
        :disabled="option.disabled"
      >
        {{ option.label }}
      </option>
    </select>
  </div>
</template>
