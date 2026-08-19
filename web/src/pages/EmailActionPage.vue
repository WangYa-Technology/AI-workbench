<script setup lang="ts">
import { CheckCircle2, KeyRound, MailCheck, RefreshCw } from 'lucide-vue-next'
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'
import { api, messageFrom } from '../api/client'
import { useSessionStore } from '../stores/session'

const { t } = useI18n()
const route = useRoute()
const session = useSessionStore()
const loading = ref(false)
const complete = ref(false)
const error = ref('')
const form = reactive({ password: '', confirmation: '' })
const isVerification = computed(() => route.name === 'verify-email')
const token = computed(() => String(route.query.token || ''))

async function submit() {
  error.value = ''
  if (!token.value) {
    error.value = t('emailAction.missingToken')
    return
  }
  if (!isVerification.value && (form.password.length < 10 || form.password !== form.confirmation)) {
    error.value = t('emailAction.passwordMismatch')
    return
  }
  loading.value = true
  try {
    if (isVerification.value) {
      await api.confirmEmailVerification(token.value)
      await session.ensure(true)
    } else {
      await api.confirmPasswordReset(token.value, form.password)
      session.clear()
    }
    complete.value = true
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <section class="email-action-page content-width">
    <div class="email-action-panel">
      <div class="email-action-icon" aria-hidden="true">
        <MailCheck v-if="isVerification" :size="24" /><KeyRound v-else :size="24" />
      </div>
      <template v-if="complete">
        <CheckCircle2 class="success-icon" :size="28" />
        <h1>{{ t(isVerification ? 'emailAction.verificationComplete' : 'emailAction.resetComplete') }}</h1>
        <p>{{ t(isVerification ? 'emailAction.verificationCompleteSummary' : 'emailAction.resetCompleteSummary') }}</p>
        <RouterLink class="command-button primary" :to="isVerification ? '/settings?section=security' : '/settings'">
          {{ t(isVerification ? 'emailAction.openSecurity' : 'emailAction.signIn') }}
        </RouterLink>
      </template>
      <form v-else class="account-form" @submit.prevent="submit">
        <div>
          <h1>{{ t(isVerification ? 'emailAction.verifyTitle' : 'emailAction.resetTitle') }}</h1>
          <p>{{ t(isVerification ? 'emailAction.verifySummary' : 'emailAction.resetSummary') }}</p>
        </div>
        <template v-if="!isVerification">
          <label>{{ t('emailAction.newPassword') }}<input v-model="form.password" type="password" minlength="10" maxlength="128" autocomplete="new-password" required /></label>
          <label>{{ t('emailAction.confirmPassword') }}<input v-model="form.confirmation" type="password" minlength="10" maxlength="128" autocomplete="new-password" required /></label>
        </template>
        <p v-if="error" class="form-error" role="alert">
          <RefreshCw :size="16" />{{ error }}
        </p>
        <button class="command-button primary" type="submit" :disabled="loading || !token">
          <MailCheck v-if="isVerification" :size="17" /><KeyRound v-else :size="17" />{{ loading ? t('emailAction.processing') : t(isVerification ? 'emailAction.verify' : 'emailAction.reset') }}
        </button>
      </form>
    </div>
  </section>
</template>

<style scoped>
.email-action-page { min-height: calc(100vh - 72px); display: grid; place-items: center; padding-block: 48px; }
.email-action-panel { width: min(100%, 480px); display: grid; gap: 20px; padding: 28px; border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--surface); }
.email-action-panel h1 { margin: 0 0 8px; font-size: 1.5rem; letter-spacing: 0; }
.email-action-panel p { margin: 0; color: var(--text-secondary); line-height: 1.6; }
.email-action-icon { width: 44px; height: 44px; display: grid; place-items: center; border-radius: var(--radius-control); background: var(--surface-muted); color: var(--accent); }
.success-icon { color: var(--success); }
.form-error { display: flex; gap: 8px; align-items: flex-start; }
</style>
