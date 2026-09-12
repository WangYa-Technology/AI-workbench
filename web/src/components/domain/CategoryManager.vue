<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, messageFrom, type TaskType } from '../../api/client'
import UiButton from '../ui/UiButton.vue'
import UiInput from '../ui/UiInput.vue'
import UiSelect from '../ui/UiSelect.vue'
const props = defineProps<{ scope: string }>()
const { locale, t } = useI18n()
const items = ref<TaskType[]>([])
const busy = ref(false)
const error = ref('')
const notice = ref('')
const deleting = ref('')
const replacement = ref('')
const contentID = ref('')
const assignedCategory = ref('')
const contentQuery = ref('')
const contents = ref<{ id: string; title: string; category: string }[]>([])
const nextCursor = ref('')
async function loadContent(more = false) {
  const page = await api.adminCategoryContent(props.scope, contentQuery.value, more ? nextCursor.value : '')
  contents.value = more ? [...contents.value, ...page.items] : page.items
  nextCursor.value = page.nextCursor
}
const fresh = () => ({ code: '', nameZh: '', nameEn: '', icon: 'mixed', sortOrder: 10, scope: props.scope })
const form = ref(fresh())
const icons = ['image', 'video', 'audio', 'prompt', 'workflow', 'mixed']
const label = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
async function run(action?: () => Promise<unknown>) {
  busy.value = true; error.value = ''; notice.value = ''
  try {
    if (action) await action()
    items.value = (await api.adminListTaskTypes(props.scope)).items
    if (props.scope !== 'task') await loadContent()
    if (action) notice.value = label('已保存', 'Saved')
  } catch (reason) { error.value = messageFrom(reason) }
  finally { busy.value = false }
}
async function create() {
  await run(async () => { await api.adminCreateTaskType(form.value); form.value = fresh() })
}
async function remove() {
  await run(async () => { await api.adminDeleteTaskType(deleting.value, replacement.value, props.scope); deleting.value = ''; replacement.value = '' })
}
watch(() => props.scope, () => { form.value = fresh(); deleting.value = ''; replacement.value = ''; void run() }, { immediate: true })
</script>

<template>
  <section class="admin-panel category-manager" :aria-busy="busy">
    <h2>{{ label('分类管理', 'Category management') }}</h2>
    <p>{{ label('分类独立于媒体格式。排序值越小越靠前；删除已使用的分类时，请选择接收内容的分类。', 'Categories are independent of media formats. Lower order values appear first. Choose a replacement when deleting a category that contains content.') }}</p>
    <p v-if="error" role="alert" class="form-error">
      {{ error }} <UiButton :disabled="busy" @click="run()">
        {{ t('actions.retry') }}
      </UiButton>
    </p>
    <p v-if="notice" role="status">
      {{ notice }}
    </p>
    <form class="category-edit-row" @submit.prevent="create">
      <label>{{ label('标识符', 'Code') }}<UiInput v-model="form.code" required pattern="[a-z][a-z0-9_\-]{0,47}" maxlength="48" /></label>
      <label>{{ label('中文名称', 'Chinese name') }}<UiInput v-model="form.nameZh" required maxlength="80" /></label>
      <label>{{ label('英文名称', 'English name') }}<UiInput v-model="form.nameEn" required maxlength="80" /></label>
      <label>{{ label('图标', 'Icon') }}<UiSelect v-model="form.icon"><option v-for="icon in icons" :key="icon" :value="icon">{{ icon }}</option></UiSelect></label>
      <label>{{ label('排序', 'Order') }}<UiInput v-model.number="form.sortOrder" type="number" min="0" max="100000" /></label>
      <UiButton type="submit" :disabled="busy">
        {{ label('新增', 'Add') }}
      </UiButton>
    </form>
    <form v-for="item in items" :key="item.code" class="category-edit-row" @submit.prevent="run(() => api.adminUpdateTaskType(item.code, { ...item, scope }))">
      <code>{{ item.code }}</code>
      <label>{{ label('中文名称', 'Chinese name') }}<UiInput v-model="item.nameZh" required maxlength="80" /></label>
      <label>{{ label('英文名称', 'English name') }}<UiInput v-model="item.nameEn" required maxlength="80" /></label>
      <label>{{ label('图标', 'Icon') }}<UiSelect v-model="item.icon"><option v-for="icon in icons" :key="icon" :value="icon">{{ icon }}</option></UiSelect></label>
      <label>{{ label('排序', 'Order') }}<UiInput v-model.number="item.sortOrder" type="number" min="0" max="100000" /></label>
      <div>
        <UiButton type="submit" :disabled="busy">
          {{ t('actions.save') }}
        </UiButton><UiButton type="button" :disabled="busy" @click="deleting = item.code; replacement = ''">
          {{ t('actions.delete') }}
        </UiButton>
      </div>
    </form>
    <form v-if="deleting" class="category-delete" @submit.prevent="remove">
      <strong>{{ label('删除分类', 'Delete category') }}: {{ deleting }}</strong>
      <UiSelect v-model="replacement" :aria-label="label('转移至', 'Move content to')">
        <option value="">
          {{ label('无关联内容，直接删除', 'Delete without replacement (empty only)') }}
        </option><option v-for="item in items.filter(i => i.code !== deleting)" :key="item.code" :value="item.code">
          {{ label(item.nameZh, item.nameEn) }}
        </option>
      </UiSelect>
      <UiButton type="submit" :disabled="busy">
        {{ t('actions.delete') }}
      </UiButton><UiButton type="button" :disabled="busy" @click="deleting = ''">
        {{ t('actions.cancel') }}
      </UiButton>
    </form>
    <form v-if="scope !== 'task'" class="category-delete" @submit.prevent="run(() => api.adminAssignCategory(scope, contentID.trim(), assignedCategory))">
      <strong>{{ label('调整内容分类', 'Assign content category') }}</strong>
      <label>{{ label('搜索内容', 'Search content') }}<UiInput v-model="contentQuery" type="search" /></label>
      <UiButton type="button" :disabled="busy" @click="run()">
        {{ t('actions.search') }}
      </UiButton>
      <UiSelect v-model="contentID" required :aria-label="label('选择内容', 'Select content')">
        <option value="" disabled>
          {{ label('选择内容', 'Select content') }}
        </option><option v-for="item in contents" :key="item.id" :value="item.id">
          {{ item.title || item.id }} · {{ label(items.find(c => c.code === item.category)?.nameZh || item.category, items.find(c => c.code === item.category)?.nameEn || item.category) }}
        </option>
      </UiSelect>
      <UiButton v-if="nextCursor" type="button" :disabled="busy" @click="async () => { busy = true; try { await loadContent(true) } catch (reason) { error = messageFrom(reason) } finally { busy = false } }">
        {{ t('actions.loadMore') }}
      </UiButton>
      <UiSelect v-model="assignedCategory" required :aria-label="label('目标分类', 'Target category')">
        <option value="" disabled>
          {{ label('选择分类', 'Select category') }}
        </option><option v-for="item in items" :key="item.code" :value="item.code">
          {{ label(item.nameZh, item.nameEn) }}
        </option>
      </UiSelect>
      <UiButton type="submit" :disabled="busy || !assignedCategory || !contentID">
        {{ t('actions.save') }}
      </UiButton>
    </form>
  </section>
</template>

<style scoped>
.category-manager { padding: 22px; }
.category-edit-row { display: grid; grid-template-columns: repeat(3, minmax(100px, 1fr)) 110px 90px auto; gap: 12px; align-items: end; padding: 16px 0; border-bottom: 1px solid var(--border); }
.category-edit-row label { display: grid; gap: 6px; font-size: 12px; }
.category-edit-row code { overflow-wrap: anywhere; align-self: center; }
.category-delete { display: flex; flex-wrap: wrap; gap: 12px; padding-top: 20px; align-items: center; }
@media (max-width: 1000px) { .category-edit-row { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
</style>
