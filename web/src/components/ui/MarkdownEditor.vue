<script setup lang="ts">
import '@github/markdown-toolbar-element'
import {
  Bold,
  Code2,
  FileCode2,
  Heading2,
  Italic,
  Link,
  List,
  ListChecks,
  ListOrdered,
  Quote,
  Strikethrough,
} from 'lucide-vue-next'
import { computed, nextTick, ref, useAttrs, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import MarkdownContent from './MarkdownContent.vue'
import UiTextarea from './UiTextarea.vue'
import UiTooltip from './UiTooltip.vue'

defineOptions({ inheritAttrs: false })

const props = withDefaults(defineProps<{
  modelValue?: string
  maxlength?: number
  placeholder?: string
  invalid?: boolean
}>(), {
  modelValue: '',
  maxlength: undefined,
  placeholder: '',
  invalid: false,
})

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const attrs = useAttrs()
const { t } = useI18n()
const generatedId = `markdown-editor-${useId()}`
const textareaId = computed(() => typeof attrs.id === 'string' ? attrs.id : generatedId)
const writeTabId = `${generatedId}-write-tab`
const previewTabId = `${generatedId}-preview-tab`
const panelId = `${generatedId}-panel`
const effectivePlaceholder = computed(() => props.placeholder || t('markdownEditor.placeholder'))
const mode = ref<'write' | 'preview'>('write')
const writeTab = ref<globalThis.HTMLButtonElement | null>(null)
const previewTab = ref<globalThis.HTMLButtonElement | null>(null)

async function selectMode(nextMode: 'write' | 'preview', focus = false) {
  mode.value = nextMode
  if (!focus) return
  await nextTick()
  const selectedTab = nextMode === 'write' ? writeTab.value : previewTab.value
  selectedTab?.focus()
}

function handleTabKeydown(event: globalThis.KeyboardEvent) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  void selectMode(event.key === 'ArrowLeft' || event.key === 'Home' ? 'write' : 'preview', true)
}
</script>

<template>
  <div
    class="markdown-editor"
    :class="{ 'markdown-editor--invalid': invalid, 'is-previewing': mode === 'preview' }"
  >
    <div class="markdown-editor__header">
      <div class="markdown-editor__tabs" role="tablist" :aria-label="t('markdownEditor.modeLabel')" @keydown="handleTabKeydown">
        <button
          :id="writeTabId"
          ref="writeTab"
          type="button"
          role="tab"
          :aria-controls="panelId"
          :aria-selected="mode === 'write'"
          :tabindex="mode === 'write' ? 0 : -1"
          @click="selectMode('write')"
        >
          {{ t('markdownEditor.write') }}
        </button>
        <button
          :id="previewTabId"
          ref="previewTab"
          type="button"
          role="tab"
          :aria-controls="panelId"
          :aria-selected="mode === 'preview'"
          :tabindex="mode === 'preview' ? 0 : -1"
          @click="selectMode('preview')"
        >
          {{ t('markdownEditor.preview') }}
        </button>
      </div>

      <markdown-toolbar
        v-show="mode === 'write'"
        class="markdown-editor__toolbar"
        :for="textareaId"
        :aria-label="t('markdownEditor.toolbarLabel')"
      >
        <UiTooltip :label="t('markdownEditor.heading')">
          <md-header role="button" level="2" :aria-label="t('markdownEditor.heading')" :title="t('markdownEditor.heading')">
            <Heading2 :size="18" />
          </md-header>
        </UiTooltip>
        <span class="markdown-editor__divider" aria-hidden="true"></span>
        <UiTooltip :label="t('markdownEditor.bold')">
          <md-bold role="button" :aria-label="t('markdownEditor.bold')" :title="t('markdownEditor.bold')">
            <Bold :size="18" />
          </md-bold>
        </UiTooltip>
        <UiTooltip :label="t('markdownEditor.italic')">
          <md-italic role="button" :aria-label="t('markdownEditor.italic')" :title="t('markdownEditor.italic')">
            <Italic :size="18" />
          </md-italic>
        </UiTooltip>
        <UiTooltip :label="t('markdownEditor.strikethrough')">
          <md-strikethrough role="button" :aria-label="t('markdownEditor.strikethrough')" :title="t('markdownEditor.strikethrough')">
            <Strikethrough :size="18" />
          </md-strikethrough>
        </UiTooltip>
        <span class="markdown-editor__divider" aria-hidden="true"></span>
        <UiTooltip :label="t('markdownEditor.quote')">
          <md-quote role="button" :aria-label="t('markdownEditor.quote')" :title="t('markdownEditor.quote')">
            <Quote :size="18" />
          </md-quote>
        </UiTooltip>
        <UiTooltip :label="t('markdownEditor.code')">
          <md-code role="button" :aria-label="t('markdownEditor.code')" :title="t('markdownEditor.code')">
            <Code2 :size="18" />
          </md-code>
        </UiTooltip>
        <UiTooltip :label="t('markdownEditor.link')">
          <md-link role="button" :aria-label="t('markdownEditor.link')" :title="t('markdownEditor.link')">
            <Link :size="18" />
          </md-link>
        </UiTooltip>
        <span class="markdown-editor__divider" aria-hidden="true"></span>
        <UiTooltip :label="t('markdownEditor.unorderedList')">
          <md-unordered-list role="button" :aria-label="t('markdownEditor.unorderedList')" :title="t('markdownEditor.unorderedList')">
            <List :size="18" />
          </md-unordered-list>
        </UiTooltip>
        <UiTooltip :label="t('markdownEditor.orderedList')">
          <md-ordered-list role="button" :aria-label="t('markdownEditor.orderedList')" :title="t('markdownEditor.orderedList')">
            <ListOrdered :size="18" />
          </md-ordered-list>
        </UiTooltip>
        <UiTooltip :label="t('markdownEditor.taskList')">
          <md-task-list role="button" :aria-label="t('markdownEditor.taskList')" :title="t('markdownEditor.taskList')">
            <ListChecks :size="18" />
          </md-task-list>
        </UiTooltip>
      </markdown-toolbar>
    </div>

    <div :id="panelId" class="markdown-editor__body" role="tabpanel" :aria-labelledby="mode === 'write' ? writeTabId : previewTabId">
      <UiTextarea
        v-if="mode === 'write'"
        v-bind="attrs"
        :id="textareaId"
        class="markdown-editor__textarea"
        :model-value="modelValue"
        :maxlength="maxlength"
        :placeholder="effectivePlaceholder"
        :invalid="invalid"
        :aria-invalid="invalid || undefined"
        @update:model-value="emit('update:modelValue', $event)"
      />
      <div v-else class="markdown-editor__preview">
        <MarkdownContent v-if="modelValue.trim()" :source="modelValue" />
        <p v-else class="markdown-editor__empty">
          {{ t('markdownEditor.emptyPreview') }}
        </p>
      </div>
    </div>

    <div class="markdown-editor__footer">
      <FileCode2 :size="16" aria-hidden="true" />
      <span>{{ t('markdownEditor.supported') }}</span>
    </div>
  </div>
</template>
