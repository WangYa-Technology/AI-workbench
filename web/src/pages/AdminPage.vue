<script setup lang="ts">
import { onBeforeUnmount, provide, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { ShieldAlert } from 'lucide-vue-next'
import { adminAccessBoundary, useAdminContext } from '../composables/useAdminContext'
import { useSessionStore } from '../stores/session'
import { UiAlert, UiButton, UiEmptyState } from '../components/ui'
import AdminWorkspace from './AdminWorkspace.vue'

const context = useAdminContext()
const revision = ref(0)
const denied = ref<401 | 403 | null>(null)
const verifying = ref(false)
const verificationError = ref('')
const session = useSessionStore()
const route = useRoute()
const { t } = useI18n()
let active = true
// Every visit owns fresh directories, selections, drafts and request state.
// A monotonic key also discards a context changed twice in the same tick.
watch(context, () => {
  ++revision.value
  denied.value = null
  verifying.value = false
  verificationError.value = ''
}, { flush: 'sync' })
onBeforeUnmount(() => { active = false; ++revision.value })
provide(adminAccessBoundary, {
  revision,
  deny(status) {
    if (!active || denied.value) return
    ++revision.value
    denied.value = status
  },
})

async function verifyAccess() {
  if (verifying.value || !denied.value) return
  const started = revision.value
  verifying.value = true
  verificationError.value = ''
  try {
    // This is a read, never a replay of the rejected command. If the identity
    // changes, the synchronous watcher already creates the appropriate new view.
    await session.ensure(true)
    if (!active || revision.value !== started) return
    if (session.error) { verificationError.value = session.error; return }
    ++revision.value
    denied.value = null
  } finally {
    if (active && (revision.value === started || !denied.value)) verifying.value = false
  }
}
</script>

<template>
  <section v-if="denied" class="admin-page content-width">
    <UiEmptyState :title="t('admin.accessVerificationTitle')" :message="t('admin.accessVerificationDetail')">
      <template #icon>
        <ShieldAlert :size="24" />
      </template>
      <template #actions>
        <UiButton :disabled="verifying" @click="verifyAccess">
          {{ t('admin.verifyAccess') }}
        </UiButton>
        <UiButton v-if="denied === 401" as="RouterLink" variant="secondary" :to="{ path: '/auth', query: { auth: 'login', returnTo: route.fullPath } }">
          {{ t('account.signIn') }}
        </UiButton>
      </template>
    </UiEmptyState>
    <UiAlert v-if="verificationError" variant="danger" :message="verificationError" />
  </section>
  <AdminWorkspace v-else :key="revision" />
</template>
