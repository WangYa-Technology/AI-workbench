<script setup lang="ts">
import { ArrowRight, Megaphone, X } from 'lucide-vue-next'
import UiButton from './UiButton.vue'
import UiIconButton from './UiIconButton.vue'

withDefaults(defineProps<{
  title: string
  description: string
  actionLabel?: string
  dismissLabel: string
  dismissible?: boolean
}>(), {
  actionLabel: '',
  dismissible: false,
})

const emit = defineEmits<{
  action: []
  dismiss: []
}>()
</script>

<template>
  <aside class="ui-announcement" :aria-label="title">
    <header class="ui-announcement__header">
      <span class="ui-announcement__icon" aria-hidden="true">
        <Megaphone :size="16" :stroke-width="1.8" />
      </span>
      <UiIconButton
        v-if="dismissible"
        class="ui-announcement__close"
        size="sm"
        variant="ghost"
        :label="dismissLabel"
        @click="emit('dismiss')"
      >
        <X :size="14" :stroke-width="1.8" />
      </UiIconButton>
    </header>
    <div class="ui-announcement__body">
      <strong>{{ title }}</strong>
      <p>{{ description }}</p>
    </div>
    <UiButton
      v-if="actionLabel"
      class="ui-announcement__action"
      variant="soft"
      size="sm"
      type="button"
      @click="emit('action')"
    >
      {{ actionLabel }}
      <ArrowRight :size="14" :stroke-width="1.8" aria-hidden="true" />
    </UiButton>
  </aside>
</template>
