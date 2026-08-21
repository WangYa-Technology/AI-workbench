<script setup lang="ts">
import { AlertCircle, CheckCircle2, Info, TriangleAlert } from 'lucide-vue-next'
import { computed } from 'vue'
const props = withDefaults(defineProps<{ variant?: 'info' | 'success' | 'warning' | 'danger'; title?: string; message?: string }>(), { variant: 'info', title: '', message: '' })
const icon = computed(() => ({ info: Info, success: CheckCircle2, warning: TriangleAlert, danger: AlertCircle })[props.variant])
</script>

<template>
  <div class="ui-alert" :data-variant="variant" role="status">
    <component :is="icon" :size="18" aria-hidden="true" /><div>
      <p v-if="title" class="ui-alert__title">
        {{ title }}
      </p><p v-if="message" class="ui-alert__message">
        {{ message }}
      </p><slot></slot>
    </div>
  </div>
</template>
