<script setup lang="ts">
import { ChevronLeft, ChevronRight } from 'lucide-vue-next'
import UiIconButton from './UiIconButton.vue'
withDefaults(defineProps<{ page?: number; totalPages?: number; label?: string; previousLabel?: string; nextLabel?: string }>(), { page: 1, totalPages: 1, label: 'Pagination', previousLabel: 'Previous', nextLabel: 'Next' })
const emit = defineEmits<{ 'update:page': [value: number] }>()
</script>

<template>
  <nav class="ui-pagination" :aria-label="label">
    <UiIconButton :label="previousLabel" :disabled="page <= 1" @click="emit('update:page', page - 1)">
      <ChevronLeft :size="16" />
    </UiIconButton><span>{{ page }} / {{ totalPages }}</span><UiIconButton :label="nextLabel" :disabled="page >= totalPages" @click="emit('update:page', page + 1)">
      <ChevronRight :size="16" />
    </UiIconButton>
  </nav>
</template>
