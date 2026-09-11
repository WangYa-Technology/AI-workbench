<script setup lang="ts">
import { Github, Globe2, KeyRound, Mail, MailCheck, Send, ShieldCheck, UserRound, UsersRound, X } from 'lucide-vue-next'
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { api, messageFrom, type AuthChallenge, type OAuthProvider } from '../api/client'
import { useNotificationsStore } from '../stores/notifications'
import { usePreferencesStore } from '../stores/preferences'
import { useSessionStore } from '../stores/session'
import { useSiteConfigStore } from '../stores/siteConfig'
import BrandLogo from '../components/brand/BrandLogo.vue'
import UiButton from '../components/ui/UiButton.vue'
import UiIconButton from '../components/ui/UiIconButton.vue'
import UiInput from '../components/ui/UiInput.vue'

type AuthStep = 'email' | 'existing' | 'existing-code' | 'registration' | 'reset'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const notifications = useNotificationsStore()
const preferences = usePreferencesStore()
const siteConfig = useSiteConfigStore()

const authStep = ref<AuthStep>('email')
const authEmail = ref('')
const authCode = ref('')
const authChallenge = ref<AuthChallenge | null>(null)
const providers = ref<OAuthProvider[]>([])
const localDemoAvailable = ref(false)
const actionID = ref('')
const error = ref('')
const success = ref('')

const guessedTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
const loginForm = reactive({ email: '', password: '' })
const resetRequestForm = reactive({ email: '' })
const registerForm = reactive({ email: '', password: '', handle: '', displayName: '', locale: 'en-US' as 'en-US' | 'zh-CN', timezone: guessedTimezone })
const safeReturnTo = computed(() => {
  const value = String(route.query.returnTo || '')
  return value.startsWith('/') && !value.startsWith('//') ? value : ''
})
const destination = computed(() => safeReturnTo.value || '/discover')

function authLocale() {
  return locale.value === 'zh-CN' ? 'zh-CN' as const : 'en-US' as const
}

function resetAuthError() {
  error.value = ''
  success.value = ''
}

function closeAuth() {
  void router.replace(destination.value)
}

async function finishAuth(user: Awaited<ReturnType<typeof session.login>>) {
  if (!user) {
    error.value = session.error
    return
  }
  if (authStep.value === 'registration') preferences.locale = registerForm.locale
  await notifications.refreshCount()
  await router.replace(destination.value)
}

async function submitAuthEmail() {
  resetAuthError()
  actionID.value = 'auth-email'
  try {
    const result = await api.unifiedAuthStart({ email: authEmail.value, locale: authLocale() })
    authEmail.value = result.email
    loginForm.email = result.email
    registerForm.email = result.email
    authChallenge.value = result.challenge || null
    authCode.value = ''
    authStep.value = result.accountExists ? 'existing' : 'registration'
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionID.value = ''
  }
}

async function sendAuthCode(purpose: 'login_code' | 'registration_code') {
  resetAuthError()
  actionID.value = `auth-code-${purpose}`
  try {
    const result = await api.unifiedAuthSendCode({ email: authEmail.value, purpose, locale: authLocale() })
    authChallenge.value = result.challenge
    authCode.value = ''
    authStep.value = purpose === 'login_code' ? 'existing-code' : 'registration'
    success.value = t('account.codeSent')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionID.value = ''
  }
}

async function submitLogin() {
  resetAuthError()
  await finishAuth(await session.login(loginForm))
}

async function submitLoginCode() {
  resetAuthError()
  if (!authChallenge.value) return
  await finishAuth(await session.loginWithCode({ challengeId: authChallenge.value.challengeId, email: authEmail.value, code: authCode.value }))
}

async function submitRegistration() {
  resetAuthError()
  if (!authChallenge.value) return
  registerForm.displayName = registerForm.displayName.trim() || registerForm.handle.trim()
  await finishAuth(await session.registerWithCode({ challengeId: authChallenge.value.challengeId, code: authCode.value, email: registerForm.email, password: registerForm.password, handle: registerForm.handle, displayName: registerForm.displayName, locale: registerForm.locale, timezone: registerForm.timezone }))
}

async function requestReset() {
  actionID.value = 'password-reset'
  resetAuthError()
  try {
    await api.requestPasswordReset(resetRequestForm.email)
    success.value = t('account.resetRequestAccepted')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionID.value = ''
  }
}

async function startDemo(actor: 'creator' | 'publisher') {
  actionID.value = `demo-${actor}`
  resetAuthError()
  const user = await session.startDemoSession(actor)
  actionID.value = ''
  await finishAuth(user)
}

onMounted(async () => {
  const user = await session.ensure()
  if (user) {
    await router.replace(destination.value)
    return
  }
  const [meta, providerResponse] = await Promise.all([
    api.meta().catch(() => null),
    api.listOAuthProviders().catch(() => ({ items: [] as OAuthProvider[] })),
  ])
  localDemoAvailable.value = Boolean(meta?.localDemoAvailable)
  providers.value = providerResponse.items
})
</script>

<template>
  <section class="auth-page">
    <UiIconButton class="auth-close" size="sm" :label="t('actions.close')" @click="closeAuth">
      <X :size="19" :stroke-width="1.75" />
    </UiIconButton>

    <div v-if="!session.initialized || session.loading && !session.user" class="page-state" aria-live="polite">
      {{ t('account.checkingSession') }}
    </div>

    <div v-else class="auth-layout auth-layout--login">
      <section class="auth-visual" aria-labelledby="auth-visual-title">
        <img class="auth-visual-media" src="/media/login-hero.png" alt="" aria-hidden="true" />
        <div class="auth-visual-scrim" aria-hidden="true"></div>
        <div class="auth-visual-content">
          <div class="auth-visual-brand">
            <BrandLogo class="auth-brand-mark" />
            <span>{{ siteConfig.current.siteName }}</span>
          </div>

          <header class="auth-intro">
            <span class="status-label">{{ t('account.identityLabel') }}</span>
            <h1 id="auth-visual-title">{{ t('account.authTitle') }}</h1>
            <p>{{ t('account.authSummary') }}</p>
            <dl>
              <div><ShieldCheck :size="19" /><dt>{{ t('account.sessionProtection') }}</dt><dd>{{ t('account.sessionProtectionDetail') }}</dd></div>
              <div><KeyRound :size="19" /><dt>{{ t('account.credentialProtection') }}</dt><dd>{{ t('account.credentialProtectionDetail') }}</dd></div>
            </dl>
          </header>
        </div>
      </section>

      <div class="auth-panel">
        <header class="auth-panel-heading">
          <span class="status-label">{{ t('account.identityLabel') }}</span>
          <h2>{{ authStep === 'reset' ? t('account.resetPassword') : authStep === 'registration' ? t('account.createAccount') : authStep === 'email' ? t('account.continueWithEmail') : t('account.signIn') }}</h2>
        </header>

        <form v-if="authStep === 'email'" class="account-form" @submit.prevent="submitAuthEmail">
          <p class="auth-form-summary">{{ t('account.unifiedAuthSummary') }}</p>
          <label>{{ t('account.email') }}<UiInput v-model="authEmail" type="email" autocomplete="email" required /></label>
          <p v-if="error" class="form-error" role="alert">{{ error }}</p>
          <UiButton class="command-button primary" variant="primary" type="submit" :loading="actionID === 'auth-email'">
            <template #start><Mail v-if="actionID !== 'auth-email'" :size="17" /></template>{{ t('account.next') }}
          </UiButton>
        </form>

        <form v-else-if="authStep === 'existing'" class="account-form" @submit.prevent="submitLogin">
          <label>{{ t('account.email') }}<UiInput v-model="loginForm.email" type="email" autocomplete="email" readonly /></label>
          <label>{{ t('account.password') }}<UiInput v-model="loginForm.password" type="password" autocomplete="current-password" required /></label>
          <p v-if="error" class="form-error" role="alert">{{ error }}</p>
          <UiButton class="command-button primary" variant="primary" type="submit" :loading="session.loading">
            <template #start><Mail v-if="!session.loading" :size="17" /></template>{{ session.loading ? t('account.signingIn') : t('account.signIn') }}
          </UiButton>
          <UiButton class="text-link" variant="ghost" size="sm" type="button" :loading="actionID === 'auth-code-login_code'" @click="sendAuthCode('login_code')">{{ t('account.useEmailCode') }}</UiButton>
          <UiButton class="text-link" variant="ghost" size="sm" type="button" @click="authStep = 'reset'; resetRequestForm.email = authEmail; resetAuthError()">{{ t('account.forgotPassword') }}</UiButton>
          <UiButton class="text-link" variant="ghost" size="sm" type="button" @click="authStep = 'email'; resetAuthError()">{{ t('account.useDifferentEmail') }}</UiButton>
        </form>

        <form v-else-if="authStep === 'existing-code'" class="account-form" @submit.prevent="submitLoginCode">
          <p class="auth-form-summary">{{ t('account.codeSentSummary', { email: authEmail }) }}</p>
          <label>{{ t('account.verificationCode') }}<UiInput v-model="authCode" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" minlength="6" maxlength="6" required /></label>
          <p v-if="error" class="form-error" role="alert">{{ error }}</p>
          <p v-if="success" class="task-feedback success" role="status">{{ success }}</p>
          <UiButton class="command-button primary" variant="primary" type="submit" :loading="session.loading">
            <template #start><MailCheck v-if="!session.loading" :size="17" /></template>{{ t('account.verifyAndSignIn') }}
          </UiButton>
          <UiButton class="text-link" variant="ghost" size="sm" type="button" :loading="actionID === 'auth-code-login_code'" @click="sendAuthCode('login_code')">{{ t('account.resendCode') }}</UiButton>
          <UiButton class="text-link" variant="ghost" size="sm" type="button" @click="authStep = 'existing'; resetAuthError()">{{ t('account.usePassword') }}</UiButton>
        </form>

        <form v-else-if="authStep === 'registration'" class="account-form" @submit.prevent="submitRegistration">
          <p class="auth-form-summary">{{ t('account.registrationCodeSummary', { email: registerForm.email }) }}</p>
          <label>{{ t('account.verificationCode') }}<UiInput v-model="authCode" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" minlength="6" maxlength="6" required /></label>
          <label>{{ t('account.handle') }}<UiInput v-model="registerForm.handle" pattern="[a-z0-9_]{3,30}" autocomplete="username" required /></label>
          <label>{{ t('account.password') }}<UiInput v-model="registerForm.password" type="password" autocomplete="new-password" minlength="10" maxlength="128" required /><small>{{ t('account.passwordHint') }}</small></label>
          <p v-if="error" class="form-error" role="alert">{{ error }}</p>
          <UiButton class="command-button primary" variant="primary" type="submit" :loading="session.loading">
            <template #start><UserRound v-if="!session.loading" :size="17" /></template>{{ session.loading ? t('account.creating') : t('account.createAccount') }}
          </UiButton>
          <UiButton class="text-link" variant="ghost" size="sm" type="button" :loading="actionID === 'auth-code-registration_code'" @click="sendAuthCode('registration_code')">{{ t('account.resendCode') }}</UiButton>
          <UiButton class="text-link" variant="ghost" size="sm" type="button" @click="authStep = 'email'; authEmail = ''; authChallenge = null; resetAuthError()">{{ t('account.useDifferentEmail') }}</UiButton>
        </form>

        <form v-else class="account-form" @submit.prevent="requestReset">
          <p class="auth-form-summary">{{ t('account.resetPasswordSummary') }}</p>
          <label>{{ t('account.email') }}<UiInput v-model="resetRequestForm.email" type="email" autocomplete="email" required /></label>
          <p v-if="success" class="task-feedback success" role="status">{{ success }}</p>
          <p v-if="error" class="form-error" role="alert">{{ error }}</p>
          <UiButton class="command-button primary" variant="primary" type="submit" :loading="Boolean(actionID)">
            <template #start><Send v-if="!actionID" :size="17" /></template>{{ t('account.sendResetLink') }}
          </UiButton>
          <UiButton class="text-link" variant="ghost" size="sm" type="button" @click="authStep = 'email'; resetAuthError()">{{ t('account.backToSignIn') }}</UiButton>
        </form>

        <section v-if="providers.length" class="oauth-boundary" aria-labelledby="oauth-heading">
          <h2 id="oauth-heading">{{ t('account.otherMethods') }}</h2>
          <UiButton v-for="provider in providers" :key="provider.provider" class="provider-row" variant="ghost" disabled :title="t('account.signInMethodsSummary')">
            <template #start><Github v-if="provider.provider === 'github'" :size="19" /><Globe2 v-else :size="19" /></template>
            <span><strong>{{ provider.name }}</strong><small>{{ t('account.signInMethodsSummary') }}</small></span>
            <template #end><span class="availability-label">{{ t('account.unavailable') }}</span></template>
          </UiButton>
        </section>

        <section v-if="localDemoAvailable" class="local-demo-boundary">
          <h2>{{ t('account.localDemo') }}</h2>
          <p>{{ t('account.localDemoDetail') }}</p>
          <div>
            <UiButton class="command-button secondary" variant="secondary" :loading="actionID === 'demo-creator'" :disabled="Boolean(actionID)" @click="startDemo('creator')">
              <template #start><UserRound v-if="actionID !== 'demo-creator'" :size="17" /></template>{{ t('account.demoCreator') }}
            </UiButton>
            <UiButton class="command-button secondary" variant="secondary" :loading="actionID === 'demo-publisher'" :disabled="Boolean(actionID)" @click="startDemo('publisher')">
              <template #start><UsersRound v-if="actionID !== 'demo-publisher'" :size="17" /></template>{{ t('account.demoPublisher') }}
            </UiButton>
          </div>
        </section>
      </div>
    </div>
  </section>
</template>
