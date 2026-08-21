<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import MultimodalCreationStudio from '../components/create/MultimodalCreationStudio.vue'

type CreationMode = 'chat' | 'image' | 'video' | 'music'

const route = useRoute()
const modes: CreationMode[] = ['chat', 'image', 'video', 'music']
const activeMode = computed<CreationMode>(() => {
  const requested = String(route.params.mode || 'chat')
  return modes.includes(requested as CreationMode) ? requested as CreationMode : 'chat'
})
</script>

<template>
  <MultimodalCreationStudio :mode="activeMode" />
</template>
