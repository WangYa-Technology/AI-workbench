<script setup lang="ts">
import { CheckCircle2, Copy, Download, FileKey2, Github, Globe2, KeyRound, Landmark, Laptop2, LogOut, Mail, MailCheck, Plus, RefreshCw, Send, ShieldCheck, Smartphone, Trash2, UserRound, UsersRound, Webhook } from 'lucide-vue-next'
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api, messageFrom, type AccountSession, type DataRightsRequest, type DeveloperAccess, type DeveloperCredential, type DeveloperServiceAccount, type DeveloperWebhookAccess, type DeveloperWebhookCreate, type DeveloperWebhookCredential, type DeveloperWebhookEndpoint, type IdentityEmailAction, type OAuthProvider, type PayoutStatus } from '../api/client'
import { formatDateTime } from '../lib/format'
import { useNotificationsStore } from '../stores/notifications'
import { usePreferencesStore } from '../stores/preferences'
import { useSessionStore } from '../stores/session'
import UiButton from '../components/ui/UiButton.vue'
import UiCheckbox from '../components/ui/UiCheckbox.vue'
import UiInput from '../components/ui/UiInput.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiTextarea from '../components/ui/UiTextarea.vue'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const notifications = useNotificationsStore()
const preferences = usePreferencesStore()
const authMode = ref<'login' | 'register' | 'reset'>(route.query.auth === 'register' ? 'register' : 'login')
const sessions = ref<AccountSession[]>([])
const sessionNextCursor = ref<string | null>(null)
const sessionsLoadingMore = ref(false)
const emailActions = ref<IdentityEmailAction[]>([])
const emailActionNextCursor = ref<string | null>(null)
const emailActionsLoadingMore = ref(false)
const providers = ref<OAuthProvider[]>([])
const payoutStatus = ref<PayoutStatus | null>(null)
const payoutLoading = ref(false)
const dataRightsRequests = ref<DataRightsRequest[]>([])
const dataRightsNextCursor = ref<string | null>(null)
const dataRightsLoadingMore = ref(false)
const developerAccess = ref<DeveloperAccess | null>(null)
const revealedCredential = ref<DeveloperCredential | null>(null)
const webhookAccess = ref<DeveloperWebhookAccess | null>(null)
const revealedWebhookCredential = ref<DeveloperWebhookCredential | null>(null)
const webhookDeliveriesLoading = ref<Record<string, boolean>>({})
const localDemoAvailable = ref(false)
const loadingEvidence = ref(false)
const actionID = ref('')
const error = ref('')
const success = ref('')

const guessedTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
const loginForm = reactive({ email: '', password: '' })
const resetRequestForm = reactive({ email: '' })
const registerForm = reactive({ email: '', password: '', handle: '', displayName: '', locale: 'en-US' as 'en-US' | 'zh-CN', timezone: guessedTimezone })
const profileForm = reactive({ displayName: '', locale: 'en-US' as 'en-US' | 'zh-CN', timezone: 'UTC' })
const rightsForm = reactive({ identityConfirmation: '', deletionConfirmed: false })
const developerForm = reactive({ accountName: '', ttlDays: 90, ipAllowlist: '', reason: '', confirmed: false })
const webhookForm = reactive<{ name: string, url: string, eventTypes: DeveloperWebhookCreate['eventTypes'], reason: string, confirmed: boolean }>({ name: '', url: '', eventTypes: ['developer.webhook.test'], reason: '', confirmed: false })

const section = computed(() => {
  const value = String(route.query.section || 'profile')
  return ['profile', 'security', 'connections', 'payouts', 'developer', 'privacy'].includes(value) ? value : 'profile'
})
const safeReturnTo = computed(() => {
  const value = String(route.query.returnTo || '')
  return value.startsWith('/') && !value.startsWith('//') ? value : ''
})

function date(value: string) {
  return formatDateTime(value, locale.value, session.user?.timezone || 'UTC')
}

function webhookEventLabel(value: string) {
  return t(`account.webhookEventTypes.${value.replaceAll('.', '_')}`)
}

function syncProfile() {
  if (!session.user) return
  profileForm.displayName = session.user.displayName
  profileForm.locale = session.user.locale as 'en-US' | 'zh-CN'
  profileForm.timezone = session.user.timezone
}

async function loadEvidence() {
  if (!session.user) return
  loadingEvidence.value = true
  error.value = ''
  try {
    const [sessionResponse, emailResponse] = await Promise.all([api.listAccountSessions(), api.listAccountEmailActions({ limit: 5 })])
    sessions.value = sessionResponse.items
	sessionNextCursor.value = sessionResponse.nextCursor || null
    emailActions.value = emailResponse.items
    emailActionNextCursor.value = emailResponse.nextCursor || null
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    loadingEvidence.value = false
  }
}

async function loadMoreSessions() {
  if (!sessionNextCursor.value || sessionsLoadingMore.value) return
  sessionsLoadingMore.value = true
  error.value = ''
  try {
    const page = await api.listAccountSessions({ limit: 20, cursor: sessionNextCursor.value })
    const known = new Set(sessions.value.map(item => item.id))
    sessions.value = [...sessions.value, ...page.items.filter(item => !known.has(item.id))]
    sessionNextCursor.value = page.nextCursor || null
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    sessionsLoadingMore.value = false
  }
}

async function loadMoreEmailActions() {
  if (!emailActionNextCursor.value || emailActionsLoadingMore.value) return
  emailActionsLoadingMore.value = true
  error.value = ''
  try {
    const page = await api.listAccountEmailActions({ limit: 5, cursor: emailActionNextCursor.value })
    const known = new Set(emailActions.value.map(item => item.id))
    emailActions.value = [...emailActions.value, ...page.items.filter(item => !known.has(item.id))]
    emailActionNextCursor.value = page.nextCursor || null
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    emailActionsLoadingMore.value = false
  }
}

async function requestVerification() {
  actionID.value = 'email-verification'
  error.value = ''
  success.value = ''
  try {
    await api.requestEmailVerification()
    success.value = t('account.verificationQueued')
    for (let attempt = 0; attempt < 20; attempt += 1) {
      await loadEvidence()
      const current = emailActions.value.find(item => item.kind === 'verify_email')
      if (current && current.status !== 'queued') break
      await new Promise(resolve => globalThis.setTimeout(resolve, 250))
    }
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionID.value = ''
  }
}

async function requestReset() {
  actionID.value = 'password-reset'
  error.value = ''
  success.value = ''
  try {
    await api.requestPasswordReset(resetRequestForm.email)
    success.value = t('account.resetRequestAccepted')
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionID.value = ''
  }
}

async function loadDataRights() {
	if (!session.user) return
	try {
		const page = await api.listDataRightsRequests({ limit: 20 })
		dataRightsRequests.value = page.items
		dataRightsNextCursor.value = page.nextCursor || null
	} catch (reason) {
		error.value = messageFrom(reason)
	}
}

async function loadMoreDataRights() {
	if (!dataRightsNextCursor.value || dataRightsLoadingMore.value) return
	dataRightsLoadingMore.value = true
	error.value = ''
	try {
		const page = await api.listDataRightsRequests({ limit: 20, cursor: dataRightsNextCursor.value })
		const known = new Set(dataRightsRequests.value.map(item => item.id))
		dataRightsRequests.value = [...dataRightsRequests.value, ...page.items.filter(item => !known.has(item.id))]
		dataRightsNextCursor.value = page.nextCursor || null
	} catch (reason) {
		error.value = messageFrom(reason)
	} finally {
		dataRightsLoadingMore.value = false
	}
}

async function loadDeveloperAccess() {
  if (!session.user) return
  try {
    const [credentials, webhooks] = await Promise.all([api.getDeveloperAccess(), api.getDeveloperWebhooks()])
    developerAccess.value = credentials
    webhookAccess.value = webhooks
  } catch (reason) {
    error.value = messageFrom(reason)
  }
}

async function loadPayoutStatus() {
  if (!session.user) return
  payoutLoading.value = true
  try {
    payoutStatus.value = await api.getPayoutStatus()
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    payoutLoading.value = false
  }
}

async function beginPayoutOnboarding() {
  actionID.value = 'payout-onboarding'
  error.value = ''
  success.value = ''
  try {
    const link = await api.beginPayoutOnboarding()
    // Account Links are single-use and short-lived; leave HCAI immediately for Stripe-hosted onboarding.
    globalThis.location.assign(link.url)
  } catch (reason) {
    error.value = messageFrom(reason)
    actionID.value = ''
  }
}

async function createDeveloperWebhook() {
  actionID.value = 'webhook-create'; error.value = ''; success.value = ''; revealedWebhookCredential.value = null
  try {
    revealedWebhookCredential.value = await api.createDeveloperWebhook({ name: webhookForm.name, url: webhookForm.url, eventTypes: webhookForm.eventTypes })
    Object.assign(webhookForm, { name: '', url: '', eventTypes: ['developer.webhook.test'], reason: '', confirmed: false })
    success.value = t('account.webhookCreated')
    await loadDeveloperAccess()
  } catch (reason) { error.value = messageFrom(reason) } finally { actionID.value = '' }
}

async function loadMoreWebhookDeliveries(endpoint: DeveloperWebhookEndpoint) {
  if (!endpoint.deliveryNextCursor || webhookDeliveriesLoading.value[endpoint.id]) return
  webhookDeliveriesLoading.value = { ...webhookDeliveriesLoading.value, [endpoint.id]: true }
  error.value = ''
  try {
    const page = await api.listDeveloperWebhookDeliveries(endpoint.id, { limit: 5, cursor: endpoint.deliveryNextCursor })
    const known = new Set(endpoint.deliveries.map(item => item.id))
    const deliveries = [...endpoint.deliveries, ...page.items.filter(item => !known.has(item.id))]
    if (webhookAccess.value) {
      webhookAccess.value = {
        ...webhookAccess.value,
        endpoints: webhookAccess.value.endpoints.map(item => item.id === endpoint.id
          ? { ...item, deliveries, deliveryNextCursor: page.nextCursor }
          : item),
      }
    }
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    webhookDeliveriesLoading.value = { ...webhookDeliveriesLoading.value, [endpoint.id]: false }
  }
}

async function rotateDeveloperWebhook(endpoint: DeveloperWebhookEndpoint) {
  actionID.value = `webhook-rotate-${endpoint.id}`; error.value = ''; success.value = ''; revealedWebhookCredential.value = null
  try {
    revealedWebhookCredential.value = await api.rotateDeveloperWebhook(endpoint.id, { expectedVersion: endpoint.version, reason: webhookForm.reason, confirmed: webhookForm.confirmed })
    webhookForm.reason = ''; webhookForm.confirmed = false
    success.value = t('account.webhookSecretRotated')
    await loadDeveloperAccess()
  } catch (reason) { error.value = messageFrom(reason) } finally { actionID.value = '' }
}

async function revokeDeveloperWebhook(endpoint: DeveloperWebhookEndpoint) {
  actionID.value = `webhook-revoke-${endpoint.id}`; error.value = ''; success.value = ''
  try {
    await api.revokeDeveloperWebhook(endpoint.id, { expectedVersion: endpoint.version, reason: webhookForm.reason, confirmed: webhookForm.confirmed })
    webhookForm.reason = ''; webhookForm.confirmed = false
    success.value = t('account.webhookRevoked')
    await loadDeveloperAccess()
  } catch (reason) { error.value = messageFrom(reason) } finally { actionID.value = '' }
}

async function testDeveloperWebhook(endpoint: DeveloperWebhookEndpoint) {
  actionID.value = `webhook-test-${endpoint.id}`; error.value = ''; success.value = ''
  try {
    const queued = await api.testDeveloperWebhook(endpoint.id)
    success.value = t('account.webhookTestQueued')
    for (let attempt = 0; attempt < 20; attempt += 1) {
      await new Promise(resolve => globalThis.setTimeout(resolve, 250))
      await loadDeveloperAccess()
      const current = webhookAccess.value?.endpoints.flatMap(item => item.deliveries).find(item => item.id === queued.id)
      if (current && ['succeeded', 'dead_letter', 'cancelled', 'retry_scheduled'].includes(current.status)) break
    }
  } catch (reason) { error.value = messageFrom(reason) } finally { actionID.value = '' }
}

async function copyWebhookSecret() {
  if (!revealedWebhookCredential.value) return
  await globalThis.navigator.clipboard.writeText(revealedWebhookCredential.value.signingSecret)
  success.value = t('account.webhookSecretCopied')
}

function developerKeyInput() {
  return {
    scopes: ['developer:identity:read' as const],
    ttlDays: developerForm.ttlDays,
    ipAllowlist: developerForm.ipAllowlist.split(/[\n,]/).map(item => item.trim()).filter(Boolean),
  }
}

async function createDeveloperAccount() {
  actionID.value = 'developer-account-create'; error.value = ''; success.value = ''
  try {
    await api.createDeveloperServiceAccount({ name: developerForm.accountName })
    developerForm.accountName = ''
    success.value = t('account.developerAccountCreated')
    await loadDeveloperAccess()
  } catch (reason) { error.value = messageFrom(reason) } finally { actionID.value = '' }
}

async function issueDeveloperKey(account: DeveloperServiceAccount) {
  actionID.value = `developer-issue-${account.id}`; error.value = ''; success.value = ''; revealedCredential.value = null
  try {
    revealedCredential.value = await api.issueDeveloperAPIKey(account.id, developerKeyInput())
    success.value = t('account.developerKeyIssued')
    await loadDeveloperAccess()
  } catch (reason) { error.value = messageFrom(reason) } finally { actionID.value = '' }
}

async function rotateDeveloperKey(account: DeveloperServiceAccount, key: DeveloperServiceAccount['keys'][number]) {
  actionID.value = `developer-rotate-${key.id}`; error.value = ''; success.value = ''; revealedCredential.value = null
  try {
    revealedCredential.value = await api.rotateDeveloperAPIKey(account.id, key.id, { ...developerKeyInput(), expectedVersion: key.version, reason: developerForm.reason, confirmed: developerForm.confirmed })
    success.value = t('account.developerKeyRotated')
    developerForm.reason = ''; developerForm.confirmed = false
    await loadDeveloperAccess()
  } catch (reason) { error.value = messageFrom(reason) } finally { actionID.value = '' }
}

async function revokeDeveloperKey(account: DeveloperServiceAccount, key: DeveloperServiceAccount['keys'][number]) {
  actionID.value = `developer-revoke-${key.id}`; error.value = ''; success.value = ''
  try {
    await api.revokeDeveloperAPIKey(account.id, key.id, { expectedVersion: key.version, reason: developerForm.reason, confirmed: developerForm.confirmed })
    success.value = t('account.developerKeyRevoked')
    developerForm.reason = ''; developerForm.confirmed = false
    await loadDeveloperAccess()
  } catch (reason) { error.value = messageFrom(reason) } finally { actionID.value = '' }
}

async function revokeDeveloperAccount(account: DeveloperServiceAccount) {
  actionID.value = `developer-account-${account.id}`; error.value = ''; success.value = ''
  try {
    await api.revokeDeveloperServiceAccount(account.id, { expectedVersion: account.version, reason: developerForm.reason, confirmed: developerForm.confirmed })
    success.value = t('account.developerAccountRevoked')
    developerForm.reason = ''; developerForm.confirmed = false
    await loadDeveloperAccess()
  } catch (reason) { error.value = messageFrom(reason) } finally { actionID.value = '' }
}

async function copyDeveloperKey() {
  if (!revealedCredential.value) return
  await globalThis.navigator.clipboard.writeText(revealedCredential.value.plaintextKey)
  success.value = t('account.developerKeyCopied')
}

async function createDataRightsRequest(requestType: 'data_export' | 'account_deletion') {
  actionID.value = `rights-${requestType}`
  error.value = ''
  success.value = ''
  try {
    await api.createDataRightsRequest({ requestType, identityConfirmation: rightsForm.identityConfirmation })
    success.value = t(requestType === 'data_export' ? 'account.exportRequested' : 'account.deletionScheduled')
    rightsForm.identityConfirmation = ''
    rightsForm.deletionConfirmed = false
    await loadDataRights()
    if (requestType === 'data_export') {
      for (let attempt = 0; attempt < 20 && dataRightsRequests.value.some(item => item.requestType === 'data_export' && ['queued', 'processing'].includes(item.status)); attempt += 1) {
        await new Promise(resolve => globalThis.setTimeout(resolve, 250))
        await loadDataRights()
      }
    }
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionID.value = ''
  }
}

async function cancelDataRightsRequest(item: DataRightsRequest) {
  actionID.value = item.id
  error.value = ''
  success.value = ''
  try {
    await api.cancelDataRightsRequest(item.id)
    success.value = t('account.rightsCancelled')
    await loadDataRights()
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionID.value = ''
  }
}

function canCancel(item: DataRightsRequest) {
  return ['queued', 'scheduled', 'blocked'].includes(item.status)
}

function exportURL(item: DataRightsRequest) {
  return `/api/v1/account/data-rights/${encodeURIComponent(item.id)}/export`
}

function bytes(value: number) {
  return new Intl.NumberFormat(locale.value, { style: 'unit', unit: value >= 1024 ? 'kilobyte' : 'byte', maximumFractionDigits: 1 }).format(value >= 1024 ? value / 1024 : value)
}

async function submitLogin() {
  error.value = ''
  success.value = ''
  const user = await session.login(loginForm)
  if (!user) {
    error.value = session.error
    return
  }
  syncProfile()
  await Promise.all([loadEvidence(), loadDataRights(), notifications.refreshCount()])
  if (safeReturnTo.value) await router.replace(safeReturnTo.value)
}

async function submitRegistration() {
  error.value = ''
  success.value = ''
  const user = await session.register(registerForm)
  if (!user) {
    error.value = session.error
    return
  }
  preferences.locale = registerForm.locale
  syncProfile()
  success.value = t('account.created')
  await Promise.all([loadEvidence(), loadDataRights(), notifications.refreshCount()])
  if (safeReturnTo.value) await router.replace(safeReturnTo.value)
}

async function startDemo(actor: 'creator' | 'publisher') {
  actionID.value = `demo-${actor}`
  error.value = ''
  const user = await session.startDemoSession(actor)
  actionID.value = ''
  if (!user) {
    error.value = session.error
    return
  }
  syncProfile()
  await Promise.all([loadEvidence(), loadDataRights(), notifications.refreshCount()])
}

async function saveProfile() {
  error.value = ''
  success.value = ''
  const user = await session.updateProfile(profileForm)
  if (!user) {
    error.value = session.error
    return
  }
  preferences.locale = profileForm.locale
  success.value = t('account.profileSaved')
}

async function revoke(item: AccountSession) {
  actionID.value = item.id
  error.value = ''
  success.value = ''
  try {
    await api.revokeAccountSession(item.id)
    if (item.current) {
      session.clear()
      notifications.clear()
      sessions.value = []
      return
    }
    success.value = t('account.sessionRevoked')
    await Promise.all([loadEvidence(), loadDataRights()])
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionID.value = ''
  }
}

async function revokeOthers() {
  actionID.value = 'others'
  error.value = ''
  success.value = ''
  try {
    const result = await api.revokeOtherAccountSessions()
    success.value = t('account.otherSessionsRevoked', { count: result.revokedCount })
    await loadEvidence()
  } catch (reason) {
    error.value = messageFrom(reason)
  } finally {
    actionID.value = ''
  }
}

async function signOut() {
  await session.logout()
  notifications.clear()
  sessions.value = []
  authMode.value = 'login'
}

watch(() => session.user?.id, () => {
  syncProfile()
})

watch(() => route.query.auth, value => {
  if (!session.user && (value === 'login' || value === 'register')) authMode.value = value
})

watch(section, value => {
  if (value === 'developer' && session.user && !developerAccess.value) void loadDeveloperAccess()
  if (value === 'payouts' && session.user) void loadPayoutStatus()
})

onMounted(async () => {
  const [user, meta, providerResponse] = await Promise.all([
    session.ensure(),
    api.meta().catch(() => null),
    api.listOAuthProviders().catch(() => ({ items: [] as OAuthProvider[] })),
  ])
  localDemoAvailable.value = Boolean(meta?.localDemoAvailable)
  providers.value = providerResponse.items
  if (user) {
    syncProfile()
    await Promise.all([loadEvidence(), loadDataRights(), section.value === 'developer' ? loadDeveloperAccess() : Promise.resolve(), section.value === 'payouts' ? loadPayoutStatus() : Promise.resolve()])
    if (route.query.connect === 'return' || route.query.connect === 'refresh') success.value = t('account.payoutReturned')
  }
})
</script>

<template>
  <section class="account-page content-width">
    <div v-if="!session.initialized || session.loading && !session.user" class="page-state" aria-live="polite">
      {{ t('account.checkingSession') }}
    </div>

    <div v-else-if="!session.user" class="auth-layout">
      <header class="auth-intro">
        <span class="status-label">{{ t('account.identityLabel') }}</span>
        <h1>{{ t('account.authTitle') }}</h1>
        <p>{{ t('account.authSummary') }}</p>
        <dl>
          <div><ShieldCheck :size="19" /><dt>{{ t('account.sessionProtection') }}</dt><dd>{{ t('account.sessionProtectionDetail') }}</dd></div>
          <div><KeyRound :size="19" /><dt>{{ t('account.credentialProtection') }}</dt><dd>{{ t('account.credentialProtectionDetail') }}</dd></div>
        </dl>
      </header>

      <div class="auth-panel">
        <nav class="auth-tabs" :aria-label="t('account.authMode')">
          <UiButton type="button" variant="ghost" :class="{ active: authMode === 'login' }" @click="authMode = 'login'">
            {{ t('account.signIn') }}
          </UiButton>
          <UiButton type="button" variant="ghost" :class="{ active: authMode === 'register' }" @click="authMode = 'register'">
            {{ t('account.createAccount') }}
          </UiButton>
        </nav>

        <form v-if="authMode === 'login'" class="account-form" @submit.prevent="submitLogin">
          <label>{{ t('account.email') }}<UiInput v-model="loginForm.email" type="email" autocomplete="email" required /></label>
          <label>{{ t('account.password') }}<UiInput v-model="loginForm.password" type="password" autocomplete="current-password" required /></label>
          <p v-if="error" class="form-error" role="alert">
            {{ error }}
          </p>
          <UiButton class="command-button primary" variant="primary" type="submit" :loading="session.loading">
            <template #start>
              <Mail v-if="!session.loading" :size="17" />
            </template>{{ session.loading ? t('account.signingIn') : t('account.signIn') }}
          </UiButton>
          <UiButton class="text-link" variant="ghost" size="sm" type="button" @click="resetRequestForm.email = loginForm.email; authMode = 'reset'; error = ''; success = ''">
            {{ t('account.forgotPassword') }}
          </UiButton>
        </form>

        <form v-else-if="authMode === 'register'" class="account-form" @submit.prevent="submitRegistration">
          <div class="form-pair">
            <label>{{ t('account.displayName') }}<UiInput v-model="registerForm.displayName" autocomplete="name" minlength="2" maxlength="80" required /></label>
            <label>{{ t('account.handle') }}<UiInput v-model="registerForm.handle" pattern="[a-z0-9_]{3,30}" autocomplete="username" required /></label>
          </div>
          <label>{{ t('account.email') }}<UiInput v-model="registerForm.email" type="email" autocomplete="email" required /></label>
          <label>{{ t('account.password') }}<UiInput v-model="registerForm.password" type="password" autocomplete="new-password" minlength="10" maxlength="128" required /><small>{{ t('account.passwordHint') }}</small></label>
          <div class="form-pair">
            <label>{{ t('account.language') }}<UiSelect v-model="registerForm.locale"><option value="en-US">English (US)</option><option value="zh-CN">简体中文</option></UiSelect></label>
            <label>{{ t('account.timezone') }}<UiInput v-model="registerForm.timezone" autocomplete="off" required /></label>
          </div>
          <p v-if="error" class="form-error" role="alert">
            {{ error }}
          </p>
          <UiButton class="command-button primary" variant="primary" type="submit" :loading="session.loading">
            <template #start>
              <UserRound v-if="!session.loading" :size="17" />
            </template>{{ session.loading ? t('account.creating') : t('account.createAccount') }}
          </UiButton>
        </form>

        <form v-else class="account-form" @submit.prevent="requestReset">
          <div>
            <h2>{{ t('account.resetPassword') }}</h2><p>
              {{ t('account.resetPasswordSummary') }}
            </p>
          </div>
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
          <UiButton class="text-link" variant="ghost" size="sm" type="button" @click="authMode = 'login'; error = ''; success = ''">
            {{ t('account.backToSignIn') }}
          </UiButton>
        </form>

        <section class="oauth-boundary" aria-labelledby="oauth-heading">
          <h2 id="oauth-heading">
            {{ t('account.otherMethods') }}
          </h2>
          <UiButton v-for="provider in providers" :key="provider.provider" class="provider-row" variant="ghost" disabled :title="t('account.signInMethodsSummary')">
            <template #start>
              <Github v-if="provider.provider === 'github'" :size="19" /><Globe2 v-else :size="19" />
            </template>
            <span><strong>{{ provider.name }}</strong><small>{{ t('account.signInMethodsSummary') }}</small></span>
            <template #end>
              <span class="availability-label">{{ t('account.unavailable') }}</span>
            </template>
          </UiButton>
        </section>

        <section v-if="localDemoAvailable" class="local-demo-boundary">
          <h2>{{ t('account.localDemo') }}</h2>
          <p>{{ t('account.localDemoDetail') }}</p>
          <div>
            <UiButton class="command-button secondary" variant="secondary" :loading="actionID === 'demo-creator'" :disabled="Boolean(actionID)" @click="startDemo('creator')">
              <template #start>
                <UserRound v-if="actionID !== 'demo-creator'" :size="17" />
              </template>{{ t('account.demoCreator') }}
            </UiButton>
            <UiButton class="command-button secondary" variant="secondary" :loading="actionID === 'demo-publisher'" :disabled="Boolean(actionID)" @click="startDemo('publisher')">
              <template #start>
                <UsersRound v-if="actionID !== 'demo-publisher'" :size="17" />
              </template>{{ t('account.demoPublisher') }}
            </UiButton>
          </div>
        </section>
      </div>
    </div>

    <template v-else>
      <header class="account-header">
        <div>
          <span class="status-label">{{ t('account.accountLabel') }}</span>
          <h1>{{ session.user.displayName }}</h1>
          <p>@{{ session.user.handle }} · {{ session.user.email }}</p>
        </div>
        <UiButton class="command-button secondary" variant="secondary" @click="signOut">
          <template #start>
            <LogOut :size="17" />
          </template>{{ t('account.signOut') }}
        </UiButton>
      </header>

      <nav class="account-section-nav" :aria-label="t('account.settingsSections')">
        <RouterLink :to="{ path: '/settings', query: { section: 'profile' } }" :class="{ active: section === 'profile' }">
          <UserRound :size="17" />{{ t('account.profile') }}
        </RouterLink>
        <RouterLink :to="{ path: '/settings', query: { section: 'security' } }" :class="{ active: section === 'security' }">
          <ShieldCheck :size="17" />{{ t('account.security') }}
        </RouterLink>
        <RouterLink :to="{ path: '/settings', query: { section: 'connections' } }" :class="{ active: section === 'connections' }">
          <KeyRound :size="17" />{{ t('account.signInMethods') }}
        </RouterLink>
        <RouterLink :to="{ path: '/settings', query: { section: 'payouts' } }" :class="{ active: section === 'payouts' }">
          <Landmark :size="17" />{{ t('account.payouts') }}
        </RouterLink>
        <RouterLink :to="{ path: '/settings', query: { section: 'developer' } }" :class="{ active: section === 'developer' }">
          <KeyRound :size="17" />{{ t('account.developerAccess') }}
        </RouterLink>
        <RouterLink :to="{ path: '/settings', query: { section: 'privacy' } }" :class="{ active: section === 'privacy' }">
          <FileKey2 :size="17" />{{ t('account.privacyRights') }}
        </RouterLink>
      </nav>

      <div v-if="success" class="task-feedback success account-feedback" role="status">
        <CheckCircle2 :size="18" />{{ success }}
      </div>
      <div v-if="error" class="task-feedback error account-feedback" role="alert">
        <RefreshCw :size="18" />{{ error }}
      </div>

      <div v-if="section === 'profile'" class="settings-layout">
        <aside><h2>{{ t('account.profile') }}</h2><p>{{ t('account.profileSummary') }}</p></aside>
        <form class="settings-panel account-form" @submit.prevent="saveProfile">
          <label>{{ t('account.displayName') }}<UiInput v-model="profileForm.displayName" minlength="2" maxlength="80" required /></label>
          <div class="identity-readonly">
            <span>{{ t('account.handle') }}</span><strong>@{{ session.user.handle }}</strong><small>{{ t('account.handleStable') }}</small>
          </div>
          <div class="identity-readonly">
            <span>{{ t('account.email') }}</span><strong>{{ session.user.email }}</strong><small>{{ t('account.emailStable') }}</small>
          </div>
          <div class="form-pair">
            <label>{{ t('account.language') }}<UiSelect v-model="profileForm.locale"><option value="en-US">English (US)</option><option value="zh-CN">简体中文</option></UiSelect></label>
            <label>{{ t('account.timezone') }}<UiInput v-model="profileForm.timezone" required /></label>
          </div>
          <UiButton class="command-button primary" variant="primary" type="submit" :loading="session.loading">
            {{ t('account.saveProfile') }}
          </UiButton>
        </form>

        <aside><h2>{{ t('account.access') }}</h2><p>{{ t('account.accessSummary') }}</p></aside>
        <section class="settings-panel access-evidence">
          <div><span>{{ t('account.role') }}</span><strong>{{ session.user.role }}</strong></div>
          <div><span>{{ t('account.status') }}</span><strong>{{ session.user.status }}</strong></div>
          <ul>
            <li v-for="permission in session.user.permissions" :key="permission">
              <CheckCircle2 :size="15" />{{ permission }}
            </li>
          </ul>
        </section>
      </div>

      <div v-else-if="section === 'security'" class="settings-layout">
        <aside><h2>{{ t('account.emailVerification') }}</h2><p>{{ t('account.emailVerificationSummary') }}</p></aside>
        <section class="settings-panel connection-list">
          <article>
            <MailCheck :size="19" />
            <div><strong>{{ session.user.email }}</strong><span>{{ session.user.emailVerified ? t('account.verified') : t('account.unverified') }}</span></div>
            <span v-if="session.user.emailVerified" class="availability-label available">{{ t('account.verified') }}</span>
            <UiButton v-else class="command-button secondary" variant="secondary" :loading="actionID === 'email-verification'" :disabled="Boolean(actionID)" @click="requestVerification">
              <template #start>
                <Send v-if="actionID !== 'email-verification'" :size="16" />
              </template>{{ t('account.sendVerification') }}
            </UiButton>
          </article>
          <article v-for="item in emailActions" :key="item.id">
            <Mail :size="19" /><div><strong>{{ t(`account.emailActionKinds.${item.kind}`) }}</strong><span>{{ item.emailHint }} · {{ t(`account.emailActionStatuses.${item.status}`) }}</span><small>{{ t('account.emailActionEvidence', { attempts: item.attemptCount, date: date(item.expiresAt) }) }}</small></div>
          </article>
          <div v-if="emailActionNextCursor" class="account-evidence-pagination">
            <UiButton class="command-button secondary" variant="secondary" :loading="emailActionsLoadingMore" @click="loadMoreEmailActions">
              {{ t('actions.loadMore') }}
            </UiButton>
          </div>
        </section>
        <aside>
          <h2>{{ t('account.activeSessions') }}</h2><p>{{ t('account.activeSessionsSummary') }}</p><UiButton class="text-link" variant="ghost" size="sm" type="button" :loading="actionID === 'others'" @click="revokeOthers">
            {{ t('account.signOutOthers') }}
          </UiButton>
        </aside>
        <section class="settings-panel session-list">
          <div v-if="loadingEvidence" class="inline-empty">
            {{ t('account.loadingSessions') }}
          </div>
          <article v-for="item in sessions" v-else :key="item.id" class="session-row">
            <span class="session-icon"><Smartphone v-if="/iOS|Android/.test(item.clientLabel)" :size="19" /><Laptop2 v-else :size="19" /></span>
            <div><strong>{{ item.clientLabel }}</strong><span>{{ item.current ? t('account.currentSession') : t(`account.sessionStatus.${item.status}`) }}</span><small>{{ t('account.lastActive', { date: date(item.lastSeenAt) }) }}<template v-if="item.networkHint"> · {{ t('account.networkHint', { hint: item.networkHint }) }}</template></small></div>
            <UiButton class="command-button secondary" variant="secondary" type="button" :loading="actionID === item.id" :disabled="item.status !== 'active'" @click="revoke(item)">
              {{ item.current ? t('account.signOut') : t('account.revoke') }}
            </UiButton>
          </article>
          <div v-if="sessionNextCursor" class="account-evidence-pagination">
            <UiButton class="command-button secondary" variant="secondary" :loading="sessionsLoadingMore" @click="loadMoreSessions">
              {{ t('actions.loadMore') }}
            </UiButton>
          </div>
        </section>
      </div>

      <div v-else-if="section === 'connections'" class="settings-layout">
        <aside><h2>{{ t('account.signInMethods') }}</h2><p>{{ t('account.signInMethodsSummary') }}</p></aside>
        <section class="settings-panel connection-list">
          <article><Mail :size="19" /><div><strong>{{ t('account.emailPassword') }}</strong><span>{{ session.user.email }}</span></div><span class="availability-label available">{{ t('account.active') }}</span></article>
          <article v-for="provider in providers" :key="provider.provider">
            <Github v-if="provider.provider === 'github'" :size="19" /><Globe2 v-else :size="19" /><div><strong>{{ provider.name }}</strong><span>{{ t('account.signInMethodsSummary') }}</span></div><span class="availability-label">{{ t('account.unavailable') }}</span>
          </article>
        </section>
        <aside><h2>{{ t('account.notifications') }}</h2><p>{{ t('account.notificationsSummary') }}</p></aside>
        <section class="settings-panel connection-list">
          <RouterLink class="settings-command" to="/notifications?view=preferences">
            <span><strong>{{ t('account.manageNotifications') }}</strong><small>{{ t('account.manageNotificationsSummary') }}</small></span><RefreshCw :size="17" />
          </RouterLink>
        </section>
      </div>

      <div v-else-if="section === 'payouts'" class="settings-layout payout-layout">
        <aside><h2>{{ t('account.payouts') }}</h2><p>{{ t('account.payoutsSummary') }}</p></aside>
        <section class="settings-panel payout-panel">
          <div v-if="payoutLoading" class="inline-empty">
            {{ t('account.checkingSession') }}
          </div>
          <template v-else-if="payoutStatus">
            <div class="payout-heading">
              <div>
                <span class="status-label">{{ t('account.payoutStatus') }}</span><strong>{{ t(`account.payoutStatuses.${payoutStatus.status}`) }}</strong>
              </div>
              <span class="availability-label" :class="{ available: payoutStatus.status === 'verified' }">{{ payoutStatus.providerAvailable ? (payoutStatus.liveMode ? t('account.payoutLiveMode') : t('account.payoutTestMode')) : t('account.unavailable') }}</span>
            </div>
            <dl class="payout-evidence">
              <div><dt>{{ t('account.payoutProvider') }}</dt><dd>{{ payoutStatus.provider }}</dd></div>
              <div><dt>{{ t('account.payoutCapabilities') }}</dt><dd>{{ t('account.payoutCapabilitiesValue', { charges: payoutStatus.chargesEnabled ? t('account.payoutCapabilityEnabled') : t('account.payoutCapabilityPending'), payouts: payoutStatus.payoutsEnabled ? t('account.payoutCapabilityEnabled') : t('account.payoutCapabilityPending') }) }}</dd></div>
              <div v-if="payoutStatus.destinationId">
                <dt>{{ t('account.payoutAccount') }}</dt><dd><code>{{ payoutStatus.destinationId }}</code></dd>
              </div>
            </dl>
            <UiButton v-if="payoutStatus.canStartOnboarding" class="command-button primary" variant="primary" :loading="actionID === 'payout-onboarding'" @click="beginPayoutOnboarding">
              <template #start>
                <Landmark v-if="actionID !== 'payout-onboarding'" :size="17" />
              </template>{{ payoutStatus.status === 'not_started' ? t('account.payoutStart') : t('account.payoutResume') }}
            </UiButton>
            <p v-if="!payoutStatus.providerAvailable" class="retention-note">
              {{ t('account.payoutUnavailable') }}
            </p>
            <p class="retention-note">
              {{ t('account.payoutHostedBoundary') }}
            </p>
          </template>
        </section>
      </div>

      <div v-else-if="section === 'developer'" class="settings-layout developer-layout">
        <aside><h2>{{ t('account.developerAccess') }}</h2><p>{{ t('account.developerAccessSummary') }}</p></aside>
        <section class="settings-panel developer-control-evidence">
          <div v-if="!developerAccess" class="inline-empty">
            {{ t('account.developerLoading') }}
          </div>
          <template v-else>
            <div><span>{{ t('account.status') }}</span><strong>{{ developerAccess.control.enabled ? t('account.active') : t('account.unavailable') }}</strong></div>
            <div><span>{{ t('account.developerLimits') }}</span><strong>{{ t('account.developerLimitValue', { accounts: developerAccess.control.maxServiceAccounts, keys: developerAccess.control.maxActiveKeys, days: developerAccess.control.defaultTtlDays }) }}</strong></div>
            <p v-if="!developerAccess.control.enabled" class="retention-note">
              {{ t('account.developerDisabled') }}
            </p>
          </template>
        </section>

        <aside><h2>{{ t('account.serviceAccounts') }}</h2><p>{{ t('account.serviceAccountsSummary') }}</p></aside>
        <section class="settings-panel developer-accounts">
          <form class="account-form developer-create" @submit.prevent="createDeveloperAccount">
            <label>{{ t('account.serviceAccountName') }}<UiInput v-model="developerForm.accountName" minlength="3" maxlength="80" required /></label>
            <UiButton class="command-button primary" variant="primary" type="submit" :loading="actionID === 'developer-account-create'" :disabled="!developerAccess?.control.enabled">
              <template #start>
                <Plus v-if="actionID !== 'developer-account-create'" :size="17" />
              </template>{{ t('account.createServiceAccount') }}
            </UiButton>
          </form>
          <p v-if="developerAccess && !developerAccess.accounts.length" class="inline-empty">
            {{ t('account.noServiceAccounts') }}
          </p>
          <article v-for="account in developerAccess?.accounts || []" :key="account.id" class="developer-account-row">
            <header>
              <div><strong>{{ account.name }}</strong><span>{{ t(`account.developerStatuses.${account.status}`) }} · v{{ account.version }}</span></div><UiButton v-if="account.status === 'active'" class="command-button secondary" variant="secondary" :loading="actionID === `developer-account-${account.id}`" :disabled="!developerForm.confirmed" @click="revokeDeveloperAccount(account)">
                <template #start>
                  <Trash2 v-if="actionID !== `developer-account-${account.id}`" :size="16" />
                </template>{{ t('account.revoke') }}
              </UiButton>
            </header>
            <div class="developer-key-controls">
              <label>{{ t('account.keyTtl') }}<UiInput v-model.number="developerForm.ttlDays" type="number" min="1" max="365" /></label>
              <label>{{ t('account.ipAllowlist') }}<UiInput v-model="developerForm.ipAllowlist" :placeholder="t('account.ipAllowlistPlaceholder')" /></label>
              <UiButton class="command-button secondary" variant="secondary" :loading="actionID === `developer-issue-${account.id}`" :disabled="account.status !== 'active'" @click="issueDeveloperKey(account)">
                <template #start>
                  <KeyRound v-if="actionID !== `developer-issue-${account.id}`" :size="16" />
                </template>{{ t('account.issueKey') }}
              </UiButton>
            </div>
            <div v-for="key in account.keys" :key="key.id" class="developer-key-row">
              <div><strong>{{ t('account.keyDisplay', { prefix: key.publicPrefix, hint: key.displayHint }) }}</strong><span>{{ t(`account.developerStatuses.${key.status}`) }} · {{ t('account.keyUses', { count: key.usageCount }) }}</span><small>{{ t('account.keyExpires', { date: date(key.expiresAt) }) }}<template v-if="key.lastUsedAt"> · {{ t('account.keyLastUsed', { date: date(key.lastUsedAt) }) }}</template></small></div>
              <div v-if="key.status === 'active'">
                <UiButton class="command-button secondary" variant="secondary" :loading="actionID === `developer-rotate-${key.id}`" :disabled="!developerForm.confirmed" @click="rotateDeveloperKey(account, key)">
                  <template #start>
                    <RefreshCw v-if="actionID !== `developer-rotate-${key.id}`" :size="15" />
                  </template>{{ t('account.rotateKey') }}
                </UiButton><UiButton class="command-button secondary" variant="secondary" :loading="actionID === `developer-revoke-${key.id}`" :disabled="!developerForm.confirmed" @click="revokeDeveloperKey(account, key)">
                  <template #start>
                    <Trash2 v-if="actionID !== `developer-revoke-${key.id}`" :size="15" />
                  </template>{{ t('account.revoke') }}
                </UiButton>
              </div>
            </div>
          </article>
          <div v-if="developerAccess?.accounts.some(account => account.status === 'active')" class="developer-danger-controls">
            <label>{{ t('account.operationReason') }}<UiTextarea v-model="developerForm.reason" rows="2" minlength="10" maxlength="500" /></label>
            <label class="rights-confirm"><UiCheckbox v-model="developerForm.confirmed" />{{ t('account.developerConfirm') }}</label>
          </div>
        </section>

        <aside><h2>{{ t('account.oneTimeKey') }}</h2><p>{{ t('account.oneTimeKeySummary') }}</p></aside>
        <section class="settings-panel developer-secret">
          <div v-if="revealedCredential">
            <code>{{ revealedCredential.plaintextKey }}</code><UiButton class="command-button secondary" variant="secondary" @click="copyDeveloperKey">
              <template #start>
                <Copy :size="16" />
              </template>{{ t('account.copyKey') }}
            </UiButton>
          </div>
          <p v-else class="inline-empty">
            {{ t('account.noRevealedKey') }}
          </p>
        </section>

        <aside><h2>{{ t('account.webhookEndpoints') }}</h2><p>{{ t('account.webhookEndpointsSummary') }}</p></aside>
        <section class="settings-panel webhook-endpoints">
          <form class="account-form webhook-create" @submit.prevent="createDeveloperWebhook">
            <div class="form-pair">
              <label>{{ t('account.webhookName') }}<UiInput v-model="webhookForm.name" minlength="3" maxlength="80" required /></label>
              <label>{{ t('account.webhookUrl') }}<UiInput v-model="webhookForm.url" type="url" maxlength="2048" :placeholder="t('account.webhookUrlPlaceholder')" required /></label>
            </div>
            <fieldset class="webhook-event-grid">
              <legend>{{ t('account.webhookEvents') }}</legend>
              <label v-for="eventType in webhookAccess?.eventTypes || []" :key="eventType"><UiCheckbox v-model="webhookForm.eventTypes" :value="eventType" />{{ webhookEventLabel(eventType) }}</label>
            </fieldset>
            <UiButton class="command-button primary" variant="primary" type="submit" :loading="actionID === 'webhook-create'" :disabled="!developerAccess?.control.enabled || !webhookForm.eventTypes.length">
              <template #start>
                <Plus v-if="actionID !== 'webhook-create'" :size="17" />
              </template>{{ t('account.createWebhook') }}
            </UiButton>
          </form>
          <p v-if="webhookAccess && !webhookAccess.endpoints.length" class="inline-empty">
            {{ t('account.noWebhooks') }}
          </p>
          <article v-for="endpoint in webhookAccess?.endpoints || []" :key="endpoint.id" class="webhook-endpoint-row">
            <header>
              <div><strong>{{ endpoint.name }}</strong><span>{{ endpoint.url }}</span><small>{{ t(`account.developerStatuses.${endpoint.status}`) }} · {{ t('account.webhookSecretVersion', { version: endpoint.currentSecretVersion, hint: endpoint.secretHint }) }}</small></div>
              <div v-if="endpoint.status === 'active'" class="webhook-actions">
                <UiButton class="command-button secondary" variant="secondary" :loading="actionID === `webhook-test-${endpoint.id}`" @click="testDeveloperWebhook(endpoint)">
                  <template #start>
                    <Send v-if="actionID !== `webhook-test-${endpoint.id}`" :size="15" />
                  </template>{{ t('account.sendWebhookTest') }}
                </UiButton>
                <UiButton class="command-button secondary" variant="secondary" :loading="actionID === `webhook-rotate-${endpoint.id}`" :disabled="!webhookForm.confirmed" @click="rotateDeveloperWebhook(endpoint)">
                  <template #start>
                    <RefreshCw v-if="actionID !== `webhook-rotate-${endpoint.id}`" :size="15" />
                  </template>{{ t('account.rotateWebhookSecret') }}
                </UiButton>
                <UiButton class="command-button secondary" variant="secondary" :loading="actionID === `webhook-revoke-${endpoint.id}`" :disabled="!webhookForm.confirmed" @click="revokeDeveloperWebhook(endpoint)">
                  <template #start>
                    <Trash2 v-if="actionID !== `webhook-revoke-${endpoint.id}`" :size="15" />
                  </template>{{ t('account.revoke') }}
                </UiButton>
              </div>
            </header>
            <div class="webhook-subscriptions">
              <span v-for="eventType in endpoint.eventTypes" :key="eventType">{{ webhookEventLabel(eventType) }}</span>
            </div>
            <div v-if="endpoint.deliveries.length" class="webhook-deliveries">
              <div v-for="delivery in endpoint.deliveries" :key="delivery.id" class="webhook-delivery-row">
                <div><Webhook :size="16" /><span><strong>{{ webhookEventLabel(delivery.eventType) }}</strong><small>{{ date(delivery.createdAt) }} · {{ t('account.webhookAttemptCount', { count: delivery.attemptCount }) }}</small></span></div>
                <span :data-status="delivery.status">{{ t(`account.webhookStatuses.${delivery.status}`) }}</span>
                <small v-if="delivery.lastStatusCode">{{ t('account.webhookHttpStatus', { code: delivery.lastStatusCode }) }}</small><small v-else-if="delivery.lastErrorCode">{{ delivery.lastErrorCode }}</small>
              </div>
              <div v-if="endpoint.deliveryNextCursor" class="webhook-delivery-pagination">
                <UiButton class="command-button secondary" variant="secondary" :loading="webhookDeliveriesLoading[endpoint.id]" @click="loadMoreWebhookDeliveries(endpoint)">
                  {{ t('actions.loadMore') }}
                </UiButton>
              </div>
            </div>
            <p v-else class="inline-empty">
              {{ t('account.noWebhookDeliveries') }}
            </p>
          </article>
          <div v-if="webhookAccess?.endpoints.some(endpoint => endpoint.status === 'active')" class="developer-danger-controls webhook-danger-controls">
            <label>{{ t('account.operationReason') }}<UiTextarea v-model="webhookForm.reason" rows="2" minlength="10" maxlength="500" /></label>
            <label class="rights-confirm"><UiCheckbox v-model="webhookForm.confirmed" />{{ t('account.webhookConfirm') }}</label>
          </div>
        </section>

        <aside><h2>{{ t('account.oneTimeWebhookSecret') }}</h2><p>{{ t('account.oneTimeWebhookSecretSummary') }}</p></aside>
        <section class="settings-panel developer-secret">
          <div v-if="revealedWebhookCredential">
            <code>{{ revealedWebhookCredential.signingSecret }}</code><UiButton class="command-button secondary" variant="secondary" @click="copyWebhookSecret">
              <template #start>
                <Copy :size="16" />
              </template>{{ t('account.copyWebhookSecret') }}
            </UiButton>
          </div>
          <p v-else class="inline-empty">
            {{ t('account.noRevealedWebhookSecret') }}
          </p>
        </section>
      </div>

      <div v-else-if="section === 'privacy'" class="settings-layout data-rights-layout">
        <aside><h2>{{ t('account.privacyRights') }}</h2><p>{{ t('account.privacyRightsSummary') }}</p></aside>
        <section class="settings-panel data-rights-actions">
          <label>{{ t('account.confirmHandle') }}<UiInput v-model="rightsForm.identityConfirmation" autocomplete="off" :placeholder="session.user.handle" /></label>
          <article>
            <div><FileKey2 :size="19" /><span><strong>{{ t('account.exportData') }}</strong><small>{{ t('account.exportDataSummary') }}</small></span></div>
            <UiButton class="command-button secondary" variant="secondary" :loading="actionID === 'rights-data_export'" :disabled="!rightsForm.identityConfirmation" @click="createDataRightsRequest('data_export')">
              <template #start>
                <Download v-if="actionID !== 'rights-data_export'" :size="17" />
              </template>{{ t('account.requestExport') }}
            </UiButton>
          </article>
          <article class="danger-zone">
            <div><Trash2 :size="19" /><span><strong>{{ t('account.deleteAccount') }}</strong><small>{{ t('account.deleteAccountSummary') }}</small></span></div>
            <label class="rights-confirm"><UiCheckbox v-model="rightsForm.deletionConfirmed" />{{ t('account.deleteConfirmation') }}</label>
            <UiButton class="command-button secondary" variant="secondary" :loading="actionID === 'rights-account_deletion'" :disabled="!rightsForm.identityConfirmation || !rightsForm.deletionConfirmed" @click="createDataRightsRequest('account_deletion')">
              <template #start>
                <Trash2 v-if="actionID !== 'rights-account_deletion'" :size="17" />
              </template>{{ t('account.scheduleDeletion') }}
            </UiButton>
          </article>
          <p class="retention-note">
            {{ t('account.retentionBoundary') }}
          </p>
        </section>

        <aside><h2>{{ t('account.rightsHistory') }}</h2><p>{{ t('account.rightsHistorySummary') }}</p></aside>
        <section class="settings-panel data-rights-list">
          <p v-if="!dataRightsRequests.length" class="inline-empty">
            {{ t('account.noRightsRequests') }}
          </p>
          <article v-for="item in dataRightsRequests" v-else :key="item.id">
            <div>
              <strong>{{ t(`account.rightsTypes.${item.requestType}`) }}</strong>
              <span>{{ t(`account.rightsStatuses.${item.status}`) }} · {{ date(item.createdAt) }}</span>
              <small v-if="item.export">{{ bytes(item.export.sizeBytes) }} · SHA-256 {{ item.export.checksumSha256.slice(0, 16) }}… · {{ t('account.expires', { date: date(item.export.expiresAt) }) }}</small>
              <small v-else-if="item.cancelUntil">{{ t('account.cancelUntil', { date: date(item.cancelUntil) }) }}</small>
              <small v-if="item.receipt">{{ t('account.receiptEvidence', { checksum: item.receipt.checksumSha256.slice(0, 16) }) }}</small>
            </div>
            <UiButton v-if="item.requestType === 'data_export' && item.status === 'ready' && item.export && !item.export.purgedAt" as="a" class="command-button secondary" variant="secondary" :href="exportURL(item)" download>
              <template #start>
                <Download :size="16" />
              </template>{{ t('account.downloadExport') }}
            </UiButton>
            <UiButton v-else-if="canCancel(item)" class="command-button secondary" variant="secondary" :loading="actionID === item.id" @click="cancelDataRightsRequest(item)">
              {{ t('account.cancelRequest') }}
            </UiButton>
          </article>
          <div v-if="dataRightsNextCursor" class="account-evidence-pagination">
            <UiButton class="command-button secondary" variant="secondary" :loading="dataRightsLoadingMore" @click="loadMoreDataRights">
              {{ t('actions.loadMore') }}
            </UiButton>
          </div>
        </section>
      </div>
    </template>
  </section>
</template>
