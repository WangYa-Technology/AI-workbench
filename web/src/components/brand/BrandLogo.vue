<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useSiteConfigStore } from '../../stores/siteConfig'

const siteConfig = useSiteConfigStore()
const failed = ref(false)
const source = computed(() => failed.value ? '/brand/logo.png' : siteConfig.current.siteIconUrl)
watch(() => siteConfig.current.siteIconUrl, () => { failed.value = false })
</script>

<template>
  <img class="brand-logo" :src="source" alt="" aria-hidden="true" @error="failed = true" />
</template>

<style scoped>
.brand-logo {
  display: block;
  object-fit: contain;
  background: var(--surface);
}
</style>
