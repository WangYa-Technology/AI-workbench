<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { computed, type Component } from 'vue'
import { FileText, Image, Layers, LayoutGrid, Music, Video, Workflow } from 'lucide-vue-next'
import UiCategorySidebar from '../ui/UiCategorySidebar.vue'
import type { TaskType } from '../../api/client'
const props = defineProps<{ items: TaskType[]; modelValue: string; counts?: Record<string, number> }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { locale } = useI18n()
const categoryIcons: Record<string, Component> = { image: Image, video: Video, audio: Music, prompt: FileText, document: FileText, workflow: Workflow, mixed: Layers }
const categoryIcon = (icon: string) => categoryIcons[icon] || LayoutGrid
const sidebarItems = computed(() => [
  { value: '', label: locale.value.startsWith('zh') ? '全部' : 'All', icon: LayoutGrid, count: props.counts ? Object.values(props.counts).reduce((sum, count) => sum + count, 0) : undefined },
  ...props.items.map(item => ({ value: item.code, label: locale.value.startsWith('zh') ? item.nameZh : item.nameEn, icon: categoryIcon(item.icon), count: props.counts ? props.counts[item.code] || 0 : undefined })),
])
</script>

<template>
  <div class="category-browser" :class="{ 'has-categories': items.length }">
    <UiCategorySidebar v-if="items.length" class="category-sidebar" :title="locale.startsWith('zh') ? '分类' : 'Categories'" :items="sidebarItems" :model-value="modelValue" @update:model-value="emit('update:modelValue', $event)" />
    <div class="category-content">
      <slot></slot>
    </div>
  </div>
</template>

<style>
.category-content { min-width: 0; }
.category-sidebar { display: none; }
@media (min-width: 1321px) {
  .category-browser.has-categories { display: grid; grid-template-columns: var(--category-sidebar-width) minmax(0, 1fr); gap: 18px; align-items: start; }
  .category-sidebar.task-category-panel { display: block; }
  .has-category-sidebar .market-filters { grid-template-columns: minmax(0, 1fr) 190px var(--control-height-toolbar); }
  .has-category-sidebar .controls-with-switcher .community-toolbar { grid-template-columns: minmax(160px, 1fr) auto; }
  .has-category-sidebar .category-filter { display: none !important; }
}
</style>
