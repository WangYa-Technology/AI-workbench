<script setup lang="ts">
import { X } from 'lucide-vue-next'

withDefaults(defineProps<{ selected?: boolean; removable?: boolean; disabled?: boolean; removeLabel?: string }>(), { selected: false, removable: false, disabled: false, removeLabel: 'Remove' })
const emit = defineEmits<{ click: [event: globalThis.MouseEvent]; remove: [] }>()
</script>

<template>
  <span class="ui-chip" :data-selected="selected" :data-disabled="disabled">
    <button class="ui-chip__main" type="button" :aria-pressed="selected" :disabled="disabled" @click="emit('click', $event)"><slot></slot></button>
    <button v-if="removable" class="ui-chip__remove" type="button" :aria-label="removeLabel" :disabled="disabled" @click="emit('remove')"><X :size="13" aria-hidden="true" /></button>
  </span>
</template>
