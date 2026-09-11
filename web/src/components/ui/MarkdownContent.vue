<script setup lang="ts">
import DOMPurify from 'dompurify'
import { marked } from 'marked'
import { computed } from 'vue'

defineOptions({ inheritAttrs: false })

const props = withDefaults(defineProps<{
  source?: string
  inline?: boolean
}>(), {
  source: '',
  inline: false,
})

const renderedHTML = computed(() => {
  const source = props.source.trim()
  if (!source) return ''

  const rendered = props.inline
    ? marked.parseInline(source, { gfm: true, breaks: true })
    : marked.parse(source, { gfm: true, breaks: true, async: false })

  return DOMPurify.sanitize(rendered as string, {
    USE_PROFILES: { html: true },
    FORBID_ATTR: ['style'],
    FORBID_TAGS: ['embed', 'form', 'iframe', 'math', 'object', 'script', 'style', 'svg'],
  })
})
</script>

<template>
  <span v-if="inline" v-bind="$attrs" class="markdown-content markdown-content--inline" v-html="renderedHTML"></span>
  <div v-else v-bind="$attrs" class="markdown-content" v-html="renderedHTML"></div>
</template>
