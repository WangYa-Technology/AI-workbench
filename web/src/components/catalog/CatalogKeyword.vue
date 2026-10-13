<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { X } from 'lucide-vue-next'
import UiButton from '../ui/UiButton.vue'
defineProps<{ query: string }>()
const emit = defineEmits<{ clear: [] }>()
const { t } = useI18n()
</script>

<template>
  <div v-if="query" class="catalog-keywords">
    <UiButton class="catalog-keyword" variant="ghost" size="sm" :aria-label="t('search.clearKeyword')" :title="t('search.activeKeyword', { query })" @click="emit('clear')">
      <span>{{ t('search.activeKeyword', { query }) }}</span><X :size="14" aria-hidden="true" />
    </UiButton>
  </div>
</template>
