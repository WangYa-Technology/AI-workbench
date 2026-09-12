<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import UiButton from '../ui/UiButton.vue'
import type { TaskType } from '../../api/client'
defineProps<{ items: TaskType[]; modelValue: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { locale } = useI18n()
</script>

<template>
  <div class="category-browser" :class="{ 'has-categories': items.length }">
    <aside v-if="items.length" class="category-sidebar" :aria-label="locale.startsWith('zh') ? '分类' : 'Categories'">
      <strong>{{ locale.startsWith('zh') ? '分类' : 'Categories' }}</strong>
      <UiButton variant="ghost" type="button" :class="{ active: !modelValue }" :aria-pressed="!modelValue" @click="emit('update:modelValue', '')">
        {{ locale.startsWith('zh') ? '全部' : 'All' }}
      </UiButton>
      <UiButton v-for="item in items" :key="item.code" variant="ghost" type="button" :class="{ active: modelValue === item.code }" :aria-pressed="modelValue === item.code" @click="emit('update:modelValue', item.code)">
        {{ locale.startsWith('zh') ? item.nameZh : item.nameEn }}
      </UiButton>
    </aside>
    <div class="category-content">
      <slot></slot>
    </div>
  </div>
</template>

<style>
.category-content { min-width: 0; }
.category-sidebar { display: none; }
@media (min-width: 1321px) {
  .category-browser.has-categories { display: grid; grid-template-columns: 180px minmax(0, 1fr); gap: 24px; align-items: start; }
  .category-sidebar { display: grid; gap: 4px; padding: 14px 0; }
  .category-sidebar strong { padding: 0 12px 12px; font-size: 14px; }
  .category-sidebar button { padding: 10px 12px; border: 0; border-radius: 6px; background: transparent; color: var(--text-secondary); text-align: start; font: inherit; cursor: pointer; overflow-wrap: anywhere; }
  .category-sidebar button:hover, .category-sidebar button.active { background: var(--accent-soft); color: var(--accent-readable); }
  .has-category-sidebar .market-filters { grid-template-columns: minmax(0, 1fr) 190px var(--control-height-toolbar); }
  .has-category-sidebar .controls-with-switcher .community-toolbar { grid-template-columns: minmax(160px, 1fr) auto; }
  .category-sidebar .ui-button { justify-content: flex-start; height: auto; white-space: normal; }
  .has-category-sidebar .category-filter { display: none !important; }
}
</style>
