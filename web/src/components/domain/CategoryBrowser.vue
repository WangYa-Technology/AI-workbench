<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { Component } from 'vue'
import { FileText, Image, Layers, LayoutGrid, Music, Video, Workflow } from 'lucide-vue-next'
import UiButton from '../ui/UiButton.vue'
import type { TaskType } from '../../api/client'
defineProps<{ items: TaskType[]; modelValue: string; counts?: Record<string, number> }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { locale } = useI18n()
const categoryIcons: Record<string, Component> = { image: Image, video: Video, audio: Music, prompt: FileText, document: FileText, workflow: Workflow, mixed: Layers }
const categoryIcon = (icon: string) => categoryIcons[icon] || LayoutGrid
</script>

<template>
  <div class="category-browser" :class="{ 'has-categories': items.length }">
    <aside v-if="items.length" class="category-sidebar task-category-panel" :aria-label="locale.startsWith('zh') ? '分类' : 'Categories'">
      <h2>{{ locale.startsWith('zh') ? '分类' : 'Categories' }}</h2>
      <nav>
        <UiButton variant="ghost" type="button" :class="{ active: !modelValue }" :aria-pressed="!modelValue" @click="emit('update:modelValue', '')">
          <LayoutGrid :size="17" aria-hidden="true" /><span>{{ locale.startsWith('zh') ? '全部' : 'All' }}</span>
          <small v-if="counts">{{ Object.values(counts).reduce((sum, count) => sum + count, 0) }}</small>
        </UiButton>
        <UiButton v-for="item in items" :key="item.code" variant="ghost" type="button" :class="{ active: modelValue === item.code }" :aria-pressed="modelValue === item.code" @click="emit('update:modelValue', item.code)">
          <component :is="categoryIcon(item.icon)" :size="17" aria-hidden="true" /><span>{{ locale.startsWith('zh') ? item.nameZh : item.nameEn }}</span>
          <small v-if="counts">{{ counts[item.code] || 0 }}</small>
        </UiButton>
      </nav>
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
  .category-browser.has-categories { display: grid; grid-template-columns: 216px minmax(0, 1fr); gap: 18px; align-items: start; }
  .category-sidebar.task-category-panel { display: block; }
  .has-category-sidebar .market-filters { grid-template-columns: minmax(0, 1fr) 190px var(--control-height-toolbar); }
  .has-category-sidebar .controls-with-switcher .community-toolbar { grid-template-columns: minmax(160px, 1fr) auto; }
  .has-category-sidebar .category-filter { display: none !important; }
}
</style>
