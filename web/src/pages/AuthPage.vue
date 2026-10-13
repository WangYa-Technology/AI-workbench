<script setup lang="ts">
import UiForm from '../components/ui/UiForm.vue'
import { validNewPassword } from '../lib/password'
import { ArrowLeft, ArrowUpRight, Eye, EyeOff, Mail, MailCheck, Send, UserRound } from 'lucide-vue-next'
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useMediaQuery, usePreferredReducedMotion } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api, messageFrom, type AuthChallenge, type Work } from '../api/client'
import { useNotificationsStore } from '../stores/notifications'
import { usePreferencesStore } from '../stores/preferences'
import { useSessionStore } from '../stores/session'
import { useSiteConfigStore } from '../stores/siteConfig'
import BrandLogo from '../components/brand/BrandLogo.vue'
import UiButton from '../components/ui/UiButton.vue'
import UiInput from '../components/ui/UiInput.vue'
import UiIconButton from '../components/ui/UiIconButton.vue'

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
const actionID = ref('')
const error = ref('')
const success = ref('')

const showGallery = useMediaQuery('(min-width: 768px)')
const galleryWorks = ref<Work[]>([])
const failedGalleryImages = ref(new Set<string>())
let galleryRequested = false
const galleryItems = computed(() => {
  const samples = [
    { id: 'landscape', src: '/tasks/task-video.webp', title: t('account.galleryLandscape') },
    { id: 'album', src: '/tasks/task-image.webp', title: t('account.galleryAlbum') },
  ]
  return samples.map((sample, index) => {
    const work = galleryWorks.value[index]
    if (work && !failedGalleryImages.value.has(work.mediaUrl)) {
      return { id: work.id, src: work.mediaUrl, title: work.title, credit: work.author.displayName, to: `/works/${work.id}` }
    }
    return { ...sample, credit: t('account.gallerySample'), to: '/discover' }
  })
})

watch([showGallery, () => session.initialized], async ([visible, initialized]) => {
  if (!visible || !initialized || session.user || galleryRequested) return
  galleryRequested = true
  try {
    const response = await api.listWorks()
    galleryWorks.value = response.items.filter(work => work.mediaKind === 'image' && work.mediaUrl).slice(0, 2)
  } catch {
    // The public gallery is optional; keep the local examples and sign-in available.
  }
}, { immediate: true })

const reducedMotion = usePreferredReducedMotion()
const introLines = computed(() => [t('account.authTitle'), t('account.authTitleAccent')])
const introCharacters = computed(() => introLines.value.map(line => Array.from(line)))
const typedCount = ref(0)
const isTyping = ref(false)
const typedLines = computed(() => {
  if (!isTyping.value) return introLines.value
  const [first = [], second = []] = introCharacters.value
  return [first.slice(0, typedCount.value).join(''), second.slice(0, Math.max(0, typedCount.value - first.length)).join('')]
})
const cursorLine = computed(() => typedCount.value <= (introCharacters.value[0]?.length || 0) ? 0 : 1)
let typingTimer: ReturnType<typeof globalThis.setTimeout> | undefined

function finishTyping() {
  globalThis.clearTimeout(typingTimer)
  typingTimer = undefined
  isTyping.value = false
}

function deleteNextCharacter() {
  typedCount.value = Math.max(0, typedCount.value - 1)
  typingTimer = typedCount.value === 0
    ? globalThis.setTimeout(typeNextCharacter, 450)
    : globalThis.setTimeout(deleteNextCharacter, 45)
}

function typeNextCharacter() {
  const [first = [], second = []] = introCharacters.value
  const characters = [...first, ...second]
  typedCount.value += 1
  if (typedCount.value >= characters.length) {
    typingTimer = globalThis.setTimeout(deleteNextCharacter, 3000)
    return
  }
  const delay = typedCount.value === first.length ? 300 : locale.value === 'zh-CN' ? 90 : 55
  typingTimer = globalThis.setTimeout(typeNextCharacter, delay)
}

watch([() => session.initialized && !session.loading, locale, reducedMotion], ([ready, language, motion], previous) => {
  if (!ready || motion === 'reduce') {
    finishTyping()
    return
  }
  // Keep one loop running, and restart with the new copy when the locale changes.
  if (isTyping.value && previous?.[1] === language) return
  finishTyping()
  typedCount.value = 0
  isTyping.value = true
  typingTimer = globalThis.setTimeout(typeNextCharacter, 250)
}, { immediate: true })

onUnmounted(finishTyping)

const guessedTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
const loginForm = reactive({ password: '' })
const passwordVisible = ref(false)
watch(authStep, () => { passwordVisible.value = false })
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
  session.error = ''
  success.value = ''
}

watch(authEmail, () => {
  passwordVisible.value = false
  authChallenge.value = null
  authCode.value = ''
  resetAuthError()
}, { flush: 'sync' })

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
  const email = authEmail.value
  const step = authStep.value
  actionID.value = `auth-code-${purpose}`
  try {
    const result = await api.unifiedAuthSendCode({ email, purpose, locale: authLocale() })
    if (email !== authEmail.value || step !== authStep.value) return
    authChallenge.value = result.challenge
    authCode.value = ''
    authStep.value = purpose === 'login_code' ? 'existing-code' : 'registration'
    success.value = t('account.codeSent')
  } catch (reason) {
    if (email === authEmail.value && step === authStep.value) error.value = messageFrom(reason)
  } finally {
    actionID.value = ''
  }
}

async function switchToEmailCode(event: globalThis.MouseEvent) {
  // Switching methods validates the email, without requiring a password.
  const button = event.currentTarget as globalThis.HTMLButtonElement
  const emailInput = button.form?.querySelector<globalThis.HTMLInputElement>('input[type="email"]')
  if (!emailInput?.reportValidity()) return
  await sendAuthCode('login_code')
}

async function submitLogin() {
  resetAuthError()
  await finishAuth(await session.login({ email: authEmail.value, password: loginForm.password }))
}

async function submitLoginCode() {
  resetAuthError()
  if (!authChallenge.value) return
  await finishAuth(await session.loginWithCode({ challengeId: authChallenge.value.challengeId, email: authEmail.value, code: authCode.value }))
}

async function submitRegistration() {
  resetAuthError()
  if (!validNewPassword(registerForm.password)) {
    error.value = t('account.passwordHint')
    return
  }
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

onMounted(async () => {
  const user = await session.ensure()
  if (user) {
    await router.replace(destination.value)
    return
  }
  error.value ||= session.error
})
</script>

<template>
  <section class="auth-page">
    <header class="auth-header">
      <RouterLink class="auth-brand" to="/">
        <BrandLogo class="auth-brand-mark" />
        <span>{{ siteConfig.current.siteName }}</span>
      </RouterLink>
      <RouterLink class="auth-home-link" to="/">
        <ArrowLeft :size="16" aria-hidden="true" />
        <span>{{ t('account.backToHome') }}</span>
      </RouterLink>
    </header>

    <div v-if="!session.initialized || session.loading && !session.user" class="page-state" aria-live="polite">
      {{ t('account.checkingSession') }}
    </div>

    <div v-else class="auth-layout auth-layout--login">
      <section class="auth-intro" aria-labelledby="auth-visual-title">
        <h1 id="auth-visual-title" :aria-label="introLines.join(' ')">
          <span v-for="(line, index) in introLines" :key="index" class="auth-type-line" :class="{ 'auth-intro-emphasis': index === 1 }" aria-hidden="true">
            <span class="auth-type-reserve">{{ line }}</span>
            <span class="auth-type-text">{{ typedLines[index] }}<span v-if="isTyping && cursorLine === index" class="auth-type-cursor"></span></span>
          </span>
        </h1>
        <p class="auth-intro-summary">
          <span class="auth-intro-summary-lead">{{ t('account.authSummaryLead') }}</span>
          <span>{{ t('account.authSummary') }}</span>
        </p>
        <div v-if="showGallery" class="auth-gallery" role="group" :aria-label="t('account.galleryLabel')">
          <RouterLink v-for="item in galleryItems" :key="item.id" class="auth-gallery-work" :to="item.to">
            <span class="auth-gallery-image">
              <img v-if="!failedGalleryImages.has(item.src)" :src="item.src" alt="" width="480" height="320" decoding="async" @error="failedGalleryImages.add(item.src)" />
              <span v-else class="auth-gallery-unavailable">{{ t('status.mediaUnavailable') }}</span>
            </span>
            <span class="auth-gallery-caption"><strong>{{ item.title }}</strong><small>{{ item.credit }}</small></span>
          </RouterLink>
        </div>
        <RouterLink class="auth-explore-link" to="/discover">
          <span>{{ t('account.exploreBeforeSignIn') }}</span>
          <span class="auth-explore-icon" aria-hidden="true"><ArrowUpRight :size="17" /></span>
        </RouterLink>
      </section>

      <section class="auth-panel" aria-labelledby="auth-panel-title">
        <header class="auth-panel-heading">
          <h2 id="auth-panel-title">
            {{ authStep === 'reset' ? t('account.resetPassword') : authStep === 'registration' ? t('account.createAccount') : authStep === 'email' ? t('account.welcomeBack') : t('account.signIn') }}
          </h2>
        </header>

        <UiForm v-if="authStep === 'email'" surface="default" class="account-form" @submit.prevent="submitAuthEmail">
          <p class="auth-form-summary">
            {{ t('account.unifiedAuthSummary') }}
          </p>
          <label>{{ t('account.email') }}<UiInput v-model="authEmail" type="email" autocomplete="email" required /></label>
          <p v-if="error" class="form-error" role="alert">
            {{ error }}
          </p>
          <UiButton class="command-button primary" variant="primary" type="submit" :loading="actionID === 'auth-email'">
            <template #start>
              <Mail v-if="actionID !== 'auth-email'" :size="17" />
            </template>{{ t('account.next') }}
          </UiButton>
        </UiForm>

        <UiForm v-else-if="authStep === 'existing'" surface="default" class="account-form" @submit.prevent="submitLogin">
          <label>{{ t('account.email') }}<UiInput v-model="authEmail" type="email" autocomplete="email" required :disabled="Boolean(actionID) || session.loading" /></label>
          <div class="auth-password-field">
            <div class="auth-password-heading">
              <label for="auth-login-password">{{ t('account.password') }}</label>
              <UiButton class="text-link auth-password-help" variant="ghost" size="sm" type="button" :disabled="Boolean(actionID) || session.loading" @click="authStep = 'reset'; resetRequestForm.email = authEmail; resetAuthError()">
                {{ t('account.forgotPassword') }}
              </UiButton>
            </div>
            <div class="auth-password-input">
              <UiInput id="auth-login-password" v-model="loginForm.password" :type="passwordVisible ? 'text' : 'password'" autocomplete="current-password" :spellcheck="false" autocapitalize="off" required />
              <UiIconButton class="auth-password-toggle" variant="ghost" motion="none" :label="t(passwordVisible ? 'account.hidePassword' : 'account.showPassword')" aria-controls="auth-login-password" @click="passwordVisible = !passwordVisible">
                <EyeOff v-if="passwordVisible" :size="18" aria-hidden="true" />
                <Eye v-else :size="18" aria-hidden="true" />
              </UiIconButton>
            </div>
          </div>
          <p v-if="error" class="form-error" role="alert">
            {{ error }}
          </p>
          <UiButton class="command-button primary" variant="primary" type="submit" :loading="session.loading" :disabled="Boolean(actionID)">
            <template #start>
              <Mail v-if="!session.loading" :size="17" />
            </template>{{ session.loading ? t('account.signingIn') : t('account.signIn') }}
          </UiButton>
          <div class="auth-method-switch">
            <UiButton class="text-link" variant="ghost" size="sm" type="button" :loading="actionID === 'auth-code-login_code'" :disabled="session.loading" @click="switchToEmailCode">
              {{ t('account.useEmailCode') }}
            </UiButton>
          </div>
        </UiForm>

        <UiForm v-else-if="authStep === 'existing-code'" surface="default" class="account-form" @submit.prevent="submitLoginCode">
          <p class="auth-form-summary">
            {{ t('account.codeSentSummary', { email: authEmail }) }}
          </p>
          <label>{{ t('account.verificationCode') }}<UiInput v-model="authCode" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" minlength="6" maxlength="6" required /></label>
          <p v-if="error" class="form-error" role="alert">
            {{ error }}
          </p>
          <p v-if="success" class="task-feedback success" role="status">
            {{ success }}
          </p>
          <UiButton class="command-button primary" variant="primary" type="submit" :loading="session.loading">
            <template #start>
              <MailCheck v-if="!session.loading" :size="17" />
            </template>{{ t('account.verifyAndSignIn') }}
          </UiButton>
          <UiButton class="text-link" variant="ghost" size="sm" type="button" :loading="actionID === 'auth-code-login_code'" @click="sendAuthCode('login_code')">
            {{ t('account.resendCode') }}
          </UiButton>
          <UiButton class="text-link" variant="ghost" size="sm" type="button" @click="authStep = 'existing'; resetAuthError()">
            {{ t('account.usePassword') }}
          </UiButton>
        </UiForm>

        <UiForm v-else-if="authStep === 'registration'" surface="default" class="account-form" @submit.prevent="submitRegistration">
          <p class="auth-form-summary">
            {{ t('account.registrationCodeSummary', { email: registerForm.email }) }}
          </p>
          <label>{{ t('account.verificationCode') }}<UiInput v-model="authCode" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" minlength="6" maxlength="6" required /></label>
          <label>{{ t('account.handle') }}<UiInput v-model="registerForm.handle" pattern="[a-z0-9_]{3,30}" autocomplete="username" required /></label>
          <label>{{ t('account.password') }}<UiInput v-model="registerForm.password" type="password" autocomplete="new-password" minlength="10" maxlength="72" required /><small>{{ t('account.passwordHint') }}</small></label>
          <p v-if="error" class="form-error" role="alert">
            {{ error }}
          </p>
          <UiButton class="command-button primary" variant="primary" type="submit" :loading="session.loading">
            <template #start>
              <UserRound v-if="!session.loading" :size="17" />
            </template>{{ session.loading ? t('account.creating') : t('account.createAccount') }}
          </UiButton>
          <UiButton class="text-link" variant="ghost" size="sm" type="button" :loading="actionID === 'auth-code-registration_code'" @click="sendAuthCode('registration_code')">
            {{ t('account.resendCode') }}
          </UiButton>
          <UiButton class="text-link" variant="ghost" size="sm" type="button" @click="authStep = 'email'; authEmail = ''; authChallenge = null; resetAuthError()">
            {{ t('account.useDifferentEmail') }}
          </UiButton>
        </UiForm>

        <UiForm v-else surface="default" class="account-form" @submit.prevent="requestReset">
          <p class="auth-form-summary">
            {{ t('account.resetPasswordSummary') }}
          </p>
          <label>{{ t('account.email') }}<UiInput v-model="resetRequestForm.email" type="email" autocomplete="email" required /></label>
          <p v-if="success" class="task-feedback success" role="status">
            {{ success }}
          </p>
          <p v-if="error" class="form-error" role="alert">
            {{ error }}
          </p>
          <UiButton class="command-button primary" variant="primary" type="submit" :loading="Boolean(actionID)">
            <template #start>
              <Send v-if="!actionID" :size="17" />
            </template>{{ t('account.sendResetLink') }}
          </UiButton>
          <UiButton class="text-link" variant="ghost" size="sm" type="button" @click="authStep = 'email'; resetAuthError()">
            {{ t('account.backToSignIn') }}
          </UiButton>
        </UiForm>
      </section>
    </div>
  </section>
</template>

<style scoped>
.auth-type-line { position: relative; }
/* Reserve the complete heading, including wrapped English lines, throughout typing. */
.auth-type-reserve { visibility: hidden; }
.auth-type-text { position: absolute; inset: 0; }
.auth-type-cursor {
  display: inline-block;
  width: 0;
  height: .9em;
  margin-left: 3px;
  margin-right: -5px;
  border-left: 2px solid currentColor;
  vertical-align: -.05em;
  animation: auth-cursor-blink 700ms step-end infinite;
}

@keyframes auth-cursor-blink {
  0%, 100% { opacity: 1; }
  50% { opacity: 0; }
}

@media (prefers-reduced-motion: reduce) {
  .auth-type-reserve { visibility: visible; }
  .auth-type-text { display: none; }
  .auth-type-cursor { animation: none; }
}
</style>
