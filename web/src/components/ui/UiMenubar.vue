<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
type MenuAction = { id: string; label: string; disabled?: boolean; shortcut?: string; separator?: boolean }
type Menu = { id: string; label: string; disabled?: boolean; items: MenuAction[] }
withDefaults(defineProps<{ menus: Menu[]; label?: string; orientation?: 'horizontal' | 'vertical' }>(), { label: 'Application menu', orientation: 'horizontal' })
const emit = defineEmits<{ select: [menuId: string, itemId: string] }>()
const root = ref<globalThis.HTMLElement | null>(null)
const open = ref('')
function toggle(id: string) { open.value = open.value === id ? '' : id }
function choose(menuId: string, item: MenuAction) { if (item.disabled || item.separator) return; emit('select', menuId, item.id); open.value = '' }
function outside(event: globalThis.PointerEvent) { if (!root.value?.contains(event.target as globalThis.Node)) open.value = '' }
function onTriggerKeydown(event: globalThis.KeyboardEvent, index: number) { const triggers = [...(root.value?.querySelectorAll<globalThis.HTMLButtonElement>('.ui-menubar__trigger') ?? [])]; const delta = event.key === 'ArrowRight' || event.key === 'ArrowDown' ? 1 : event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : 0; if (delta) { event.preventDefault(); triggers[(index + delta + triggers.length) % triggers.length]?.focus() } else if (event.key === 'Escape') open.value = ''; else if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); toggle(triggers[index]?.dataset.id ?? ''); nextTick(() => root.value?.querySelector<globalThis.HTMLButtonElement>('.ui-menubar__content button:not(:disabled)')?.focus()) } }
onMounted(() => globalThis.document.addEventListener('pointerdown', outside))
onBeforeUnmount(() => globalThis.document.removeEventListener('pointerdown', outside))
</script>

<template>
  <div ref="root" class="ui-menubar" role="menubar" :data-orientation="orientation" :aria-label="label">
    <div v-for="(menu, index) in menus" :key="menu.id" class="ui-menubar__menu">
      <button class="ui-menubar__trigger" type="button" role="menuitem" :data-id="menu.id" :aria-expanded="open === menu.id" :disabled="menu.disabled" @click="toggle(menu.id)" @keydown="onTriggerKeydown($event, index)">
        {{ menu.label }}
      </button><Transition name="ui-dropdown">
        <div v-if="open === menu.id" class="ui-menubar__content" role="menu">
          <template v-for="item in menu.items" :key="item.id">
            <div v-if="item.separator" class="ui-menu-separator" role="separator"></div><button v-else type="button" role="menuitem" :disabled="item.disabled" @click="choose(menu.id, item)">
              <span>{{ item.label }}</span><kbd v-if="item.shortcut">{{ item.shortcut }}</kbd>
            </button>
          </template>
        </div>
      </Transition>
    </div>
  </div>
</template>
