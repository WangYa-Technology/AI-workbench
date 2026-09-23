<script setup lang="ts">
import { ChevronDown } from 'lucide-vue-next'
withDefaults(defineProps<{ open?: boolean; title?: string; disabled?: boolean }>(), { open: false, title: '', disabled: false })
const emit = defineEmits<{ 'update:open': [value: boolean] }>()
</script>

<template>
  <div class="ui-collapsible" :data-open="open">
    <button class="ui-collapsible__trigger" type="button" :aria-expanded="open" :disabled="disabled" @click="emit('update:open', !open)">
      <slot name="trigger">
        {{ title }}
      </slot><ChevronDown class="ui-collapsible__chevron" :size="16" aria-hidden="true" />
    </button><div class="ui-collapsible__panel" :inert="!open" :aria-hidden="!open">
      <div class="ui-collapsible__inner">
        <slot></slot>
      </div>
    </div>
  </div>
</template>
