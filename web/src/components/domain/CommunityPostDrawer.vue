<script setup lang="ts">
import { MessageSquareText, Send, X } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, messageFrom, type CommunityPost } from '../../api/client'
import UiButton from '../ui/UiButton.vue'
import UiDrawer from '../ui/UiDrawer.vue'
import UiIconButton from '../ui/UiIconButton.vue'
import UiInput from '../ui/UiInput.vue'
import UiTextarea from '../ui/UiTextarea.vue'

const props = withDefaults(defineProps<{ open?: boolean }>(), { open: false })
const emit = defineEmits<{
  'update:open': [value: boolean]
  created: [post: CommunityPost]
}>()

const { t } = useI18n()
const title = ref('')
const body = ref('')
const submitting = ref(false)
const error = ref('')
const titleLength = computed(() => title.value.trim().length)
const bodyLength = computed(() => body.value.trim().length)
const canSubmit = computed(() => titleLength.value >= 3 && titleLength.value <= 120 && bodyLength.value >= 2 && bodyLength.value <= 2000)

function reset() {
  title.value = ''
  body.value = ''
  error.value = ''
}

async function submit() {
  if (!canSubmit.value || submitting.value) return
  submitting.value = true
  error.value = ''
  try {
    const post = await api.createCommunityPost({ title: title.value.trim(), body: body.value.trim() })
    emit('created', post)
    emit('update:open', false)
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    submitting.value = false
  }
}

watch(() => props.open, (open) => {
  if (open) reset()
})
</script>

<template>
  <UiDrawer :open="open" side="right" size="md" :label="t('community.publishPost')" @update:open="emit('update:open', $event)">
    <form class="community-post-drawer" @submit.prevent="submit">
      <header class="community-post-drawer-header">
        <div>
          <span class="status-label"><MessageSquareText :size="14" />{{ t('community.discussionLabel') }}</span>
          <h2>{{ t('community.publishPost') }}</h2>
          <p>{{ t('community.publishPostSummary') }}</p>
        </div>
        <UiIconButton variant="ghost" :label="t('actions.close')" @click="emit('update:open', false)">
          <X :size="19" />
        </UiIconButton>
      </header>

      <div class="community-post-drawer-body">
        <section class="community-post-guidance">
          <MessageSquareText :size="19" />
          <div>
            <strong>{{ t('community.postGuidanceTitle') }}</strong>
            <p>{{ t('community.postGuidance') }}</p>
          </div>
        </section>

        <fieldset class="community-post-fields">
          <legend>{{ t('community.postDetails') }}</legend>
          <label for="community-post-title">
            <span>{{ t('community.postTitle') }}</span>
            <small aria-hidden="true">{{ titleLength }}/120</small>
          </label>
          <UiInput id="community-post-title" v-model="title" required minlength="3" maxlength="120" autofocus :placeholder="t('community.postTitlePlaceholder')" />

          <label for="community-post-body">
            <span>{{ t('community.postBody') }}</span>
            <small aria-hidden="true">{{ bodyLength }}/2000</small>
          </label>
          <UiTextarea id="community-post-body" v-model="body" required minlength="2" maxlength="2000" rows="12" :placeholder="t('community.postBodyPlaceholder')" />
          <p class="community-post-field-hint">{{ t('community.postBodyHint') }}</p>
        </fieldset>

        <p v-if="error" class="form-error" role="alert">{{ error }}</p>
      </div>

      <footer class="community-post-drawer-footer">
        <UiButton variant="secondary" type="button" :disabled="submitting" @click="emit('update:open', false)">
          {{ t('actions.cancel') }}
        </UiButton>
        <UiButton variant="primary" type="submit" :disabled="!canSubmit" :loading="submitting">
          <template #start><Send v-if="!submitting" :size="16" /></template>{{ submitting ? t('community.publishingPost') : t('community.publishPost') }}
        </UiButton>
      </footer>
    </form>
  </UiDrawer>
</template>

<style scoped>
.community-post-drawer { height: 100%; min-height: 0; display: grid; grid-template-rows: auto minmax(0, 1fr) auto; background: var(--surface); }
.community-post-drawer-header { min-height: 106px; display: flex; justify-content: space-between; align-items: flex-start; gap: 20px; padding: 20px 22px 18px; border-bottom: 1px solid var(--border); }
.community-post-drawer-header > div { min-width: 0; }
.community-post-drawer-header .status-label { display: inline-flex; align-items: center; gap: 6px; color: var(--accent-readable); }
.community-post-drawer-header h2 { margin: 8px 0 4px; font-size: 22px; font-weight: 600; line-height: 1.2; }
.community-post-drawer-header p { margin: 0; color: var(--text-secondary); font-size: 12px; line-height: 1.5; }
.community-post-drawer-body { min-height: 0; display: grid; align-content: start; gap: 22px; padding: 22px; overflow-y: auto; scrollbar-width: thin; }
.community-post-guidance { display: grid; grid-template-columns: 30px minmax(0, 1fr); gap: 11px; align-items: start; padding: 14px; border-radius: var(--radius-control); background: var(--accent-soft); }
.community-post-guidance > svg { margin-top: 1px; color: var(--accent-readable); }
.community-post-guidance div { display: grid; gap: 3px; }
.community-post-guidance strong { font-size: 13px; font-weight: 650; }
.community-post-guidance p { margin: 0; color: var(--text-secondary); font-size: 11px; line-height: 1.55; }
.community-post-fields { min-width: 0; display: grid; gap: 9px; margin: 0; padding: 0; border: 0; }
.community-post-fields legend { width: 100%; margin-bottom: 5px; padding: 0 0 12px; border-bottom: 1px solid var(--border); color: var(--accent-readable); font-size: 12px; font-weight: 650; }
.community-post-fields label { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-top: 5px; font-size: 13px; font-weight: 600; }
.community-post-fields label small { color: var(--text-tertiary); font-size: 10px; font-weight: 500; }
.community-post-fields :deep(.ui-input), .community-post-fields :deep(.ui-textarea) { width: 100%; }
.community-post-fields :deep(.ui-textarea) { min-height: 250px; resize: vertical; line-height: 1.65; }
.community-post-field-hint { margin: 0; color: var(--text-tertiary); font-size: 11px; line-height: 1.5; }
.community-post-drawer-footer { min-height: 70px; display: grid; grid-template-columns: minmax(0, .75fr) minmax(0, 1.25fr); gap: 10px; padding: 13px 22px; border-top: 1px solid var(--border); background: var(--surface); }
.community-post-drawer-footer :deep(.ui-button) { width: 100%; }
@media (max-width: 560px) {
  .community-post-drawer-header, .community-post-drawer-body, .community-post-drawer-footer { padding-inline: 16px; }
}
</style>
