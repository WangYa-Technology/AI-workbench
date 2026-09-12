<script setup lang="ts">
type TocItem = { id: string; label: string; level?: number }
withDefaults(defineProps<{ items: TocItem[]; activeId?: string; label?: string }>(), { activeId: '', label: 'On this page' })
const emit = defineEmits<{ navigate: [item: TocItem] }>()
</script>

<template>
  <nav class="ui-toc" :aria-label="label">
    <strong>{{ label }}</strong><ol>
      <li v-for="item in items" :key="item.id" :style="{ '--ui-toc-level': String(Math.max(0, (item.level ?? 1) - 1)) }">
        <a :href="`#${item.id}`" :aria-current="item.id === activeId ? 'location' : undefined" @click="emit('navigate', item)">{{ item.label }}</a>
      </li>
    </ol>
  </nav>
</template>
