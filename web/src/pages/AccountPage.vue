<script setup lang="ts">
import UiEmptyState from '../components/ui/UiEmptyState.vue'
import UiForm from '../components/ui/UiForm.vue'
import { CheckCircle2, Copy, Download, FileKey2, Github, Globe2, KeyRound, Landmark, Laptop2, LogOut, Mail, MailCheck, Pencil, Plus, RefreshCw, Send, ShieldCheck, Smartphone, Trash2, UserRound, Webhook, X } from 'lucide-vue-next'
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
import UiAvatar from '../components/ui/UiAvatar.vue'
import UiInput from '../components/ui/UiInput.vue'
import UiDrawer from '../components/ui/UiDrawer.vue'
import UiIconButton from '../components/ui/UiIconButton.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiStatus from '../components/ui/UiStatus.vue'
import UiTextarea from '../components/ui/UiTextarea.vue'
import PageHeader from '../components/ui/PageHeader.vue'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const notifications = useNotificationsStore()
const preferences = usePreferencesStore()
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
const loadingEvidence = ref(false)
const actionID = ref('')
const error = ref('')
const success = ref('')
const profileDrawerOpen = ref(false)

const profileForm = reactive({ displayName: '', locale: 'en-US' as 'en-US' | 'zh-CN', timezone: 'UTC' })
const rightsForm = reactive({ identityConfirmation: '', deletionConfirmed: false })
const developerForm = reactive({ accountName: '', ttlDays: 90, ipAllowlist: '', reason: '', confirmed: false })
const webhookForm = reactive<{ name: string, url: string, eventTypes: DeveloperWebhookCreate['eventTypes'], reason: string, confirmed: boolean }>({ name: '', url: '', eventTypes: ['developer.webhook.test'], reason: '', confirmed: false })

const section = computed(() => {
  const value = String(route.query.section || 'profile')
  return ['profile', 'security', 'connections', 'payouts', 'developer', 'privacy'].includes(value) ? value : 'profile'
})

const accountInitials = computed(() => {
  const displayName = session.user?.displayName.trim() || ''
  if (!displayName) return '?'
  return displayName.split(/\s+/).map(part => part[0]).join('').slice(0, 2).toUpperCase()
})

const accountSections = computed(() => [
  { id: 'profile', label: t('account.profile'), summary: t('account.profileSummary'), icon: UserRound },
  { id: 'security', label: t('account.security'), summary: t('account.emailVerificationSummary'), icon: ShieldCheck },
  { id: 'connections', label: t('account.signInMethods'), summary: t('account.signInMethodsSummary'), icon: KeyRound },
  { id: 'payouts', label: t('account.payouts'), summary: t('account.payoutsSummary'), icon: Landmark },
  { id: 'developer', label: t('account.developerAccess'), summary: t('account.developerAccessSummary'), icon: Webhook },
  { id: 'privacy', label: t('account.privacyRights'), summary: t('account.privacyRightsSummary'), icon: FileKey2 },
])

const accountHeroStats = computed(() => {
  if (!session.user) return []
  return [
    { value: t(`account.roleNames.${session.user.role}`), label: t('account.role'), icon: UserRound, tone: 'blue' as const },
    { value: t(`account.statusNames.${session.user.status}`), label: t('account.status'), icon: ShieldCheck, tone: 'green' as const },
    { value: session.user.emailVerified ? t('account.verified') : t('account.unverified'), label: t('account.email'), icon: MailCheck, tone: 'violet' as const },
  ]
})

function openProfileEditor() {
  syncProfile()
  error.value = ''
  success.value = ''
  profileDrawerOpen.value = true
}
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
    await loadPayoutStatus()
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
  if (item.requestType === 'account_deletion') {
    const deadline = item.cancelUntil ? Date.parse(item.cancelUntil) : Number.NaN
    if (!Number.isFinite(deadline) || Date.now() >= deadline) return false
  }
  return ['queued', 'scheduled', 'blocked'].includes(item.status) || (item.status === 'failed' && item.requestType === 'data_export')
}

function exportURL(item: DataRightsRequest) {
  return `/api/v1/account/data-rights/${encodeURIComponent(item.id)}/export`
}

function bytes(value: number) {
  return new Intl.NumberFormat(locale.value, { style: 'unit', unit: value >= 1024 ? 'kilobyte' : 'byte', maximumFractionDigits: 1 }).format(value >= 1024 ? value / 1024 : value)
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
  profileDrawerOpen.value = false
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
  error.value = ''
  if (!await session.logout()) {
    error.value = session.error || t('errors.codes.command_session_changed')
    return
  }
  notifications.clear()
  sessions.value = []
  await router.replace({ path: '/auth', query: { returnTo: route.fullPath } })
}

watch(() => session.user?.id, () => {
  syncProfile()
})

watch(section, value => {
  if (value === 'developer' && session.user && !developerAccess.value) void loadDeveloperAccess()
  if (value === 'payouts' && session.user) void loadPayoutStatus()
})

onMounted(async () => {
  const [user, providerResponse] = await Promise.all([
    session.ensure(),
    api.listOAuthProviders().catch(() => ({ items: [] as OAuthProvider[] })),
  ])
  providers.value = providerResponse.items
  if (!user) {
    const returnTo = String(route.query.returnTo || route.fullPath)
    await router.replace({ path: '/auth', query: returnTo.startsWith('/') && !returnTo.startsWith('//') ? { returnTo } : undefined })
    return
  }
  syncProfile()
  await Promise.all([loadEvidence(), loadDataRights(), section.value === 'developer' ? loadDeveloperAccess() : Promise.resolve(), section.value === 'payouts' ? loadPayoutStatus() : Promise.resolve()])
  if (route.query.connect === 'return' || route.query.connect === 'refresh') success.value = t('account.payoutReturned')
})
</script>

<template>
  <section class="account-page content-width">
    <div v-if="!session.initialized || session.loading && !session.user" class="page-state" aria-live="polite">
      {{ t('account.checkingSession') }}
    </div>

    <template v-else-if="session.user">
      <PageHeader
        class="account-hero" stat-format="text"
        :eyebrow="t('account.accountLabel')"
        :eyebrow-icon="UserRound"
        :title="session.user.displayName"
        :summary="t('account.identityOverviewSummary')"
        :stats="accountHeroStats"
        :stats-label="t('account.identityOverview')"
      >
        <template #actions>
          <UiButton class="command-button secondary" variant="secondary" :loading="session.loading" @click="signOut">
            <template #start>
              <LogOut :size="17" />
            </template>{{ t('account.signOut') }}
          </UiButton>
        </template>
        <template #visual>
          <div class="account-hero-visual" aria-hidden="true">
            <span class="account-hero-avatar-mark">
              <UiAvatar :initials="accountInitials" />
              <span class="account-hero-shield"><ShieldCheck :size="20" /></span>
            </span>
          </div>
        </template>
      </PageHeader>

      <div class="account-workspace">
        <aside class="account-section-nav task-category-panel">
          <h2>{{ t('account.accountNavigation') }}</h2>
          <nav :aria-label="t('account.settingsSections')">
            <RouterLink v-for="item in accountSections" :key="item.id" :to="{ path: '/settings', query: { section: item.id } }" :aria-label="item.label" :class="{ active: section === item.id }">
              <component :is="item.icon" :size="18" />
              <span><strong>{{ item.label }}</strong><small>{{ item.summary }}</small></span>
            </RouterLink>
          </nav>
        </aside>

        <div class="account-settings-main task-results">
          <div v-if="success" class="task-feedback success account-feedback" role="status">
            <CheckCircle2 :size="18" />{{ success }}
          </div>
          <div v-if="error || session.error" class="task-feedback error account-feedback" role="alert">
            <RefreshCw :size="18" />{{ error || session.error }}
          </div>

          <div v-if="section === 'profile'" class="account-profile-overview">
            <section class="account-profile-section">
              <header>
                <div><h3>{{ t('account.identityOverview') }}</h3><p>{{ t('account.identityOverviewSummary') }}</p></div>
                <UiButton variant="secondary" @click="openProfileEditor">
                  <template #start>
                    <Pencil :size="16" />
                  </template>{{ t('account.editProfile') }}
                </UiButton>
              </header>
              <dl class="account-profile-data">
                <div><dt>{{ t('account.displayName') }}</dt><dd>{{ session.user.displayName }}</dd></div>
                <div><dt>{{ t('account.handle') }}</dt><dd>@{{ session.user.handle }}</dd></div>
                <div><dt>{{ t('account.email') }}</dt><dd>{{ session.user.email }}<UiStatus :variant="session.user.emailVerified ? 'success' : 'neutral'" :label="session.user.emailVerified ? t('account.verified') : t('account.unverified')" /></dd></div>
              </dl>
            </section>

            <section class="account-profile-section">
              <header><div><h3>{{ t('account.regionalPreferences') }}</h3><p>{{ t('account.regionalPreferencesSummary') }}</p></div></header>
              <dl class="account-profile-data compact">
                <div><dt>{{ t('account.language') }}</dt><dd>{{ session.user.locale === 'zh-CN' ? '简体中文' : 'English (US)' }}</dd></div>
                <div><dt>{{ t('account.timezone') }}</dt><dd>{{ session.user.timezone }}</dd></div>
              </dl>
            </section>

            <section class="account-profile-section">
              <header><div><h3>{{ t('account.access') }}</h3><p>{{ t('account.accessSummary') }}</p></div></header>
              <dl class="account-profile-data compact">
                <div><dt>{{ t('account.role') }}</dt><dd>{{ t(`account.roleNames.${session.user.role}`) }}</dd></div>
                <div><dt>{{ t('account.status') }}</dt><dd>{{ t(`account.statusNames.${session.user.status}`) }}</dd></div>
              </dl>
              <ul class="account-permissions" :aria-label="t('account.permissions')">
                <li v-for="permission in session.user.permissions" :key="permission">
                  <CheckCircle2 :size="14" />{{ permission }}
                </li>
              </ul>
            </section>
          </div>

          <div v-else-if="section === 'security'" class="account-security-grid">
            <section class="account-settings-card">
              <header class="account-settings-card-header">
                <span class="account-settings-card-icon"><MailCheck :size="20" /></span>
                <div><h3>{{ t('account.emailVerification') }}</h3><p>{{ t('account.emailVerificationSummary') }}</p></div>
              </header>
              <div class="account-settings-card-body connection-list">
                <article>
                  <span class="account-setting-row-icon"><MailCheck :size="19" /></span>
                  <div><strong>{{ session.user.email }}</strong><span>{{ session.user.emailVerified ? t('account.verified') : t('account.unverified') }}</span></div>
                  <span v-if="session.user.emailVerified" class="availability-label available">{{ t('account.verified') }}</span>
                  <UiButton v-else class="command-button secondary" variant="secondary" :loading="actionID === 'email-verification'" :disabled="Boolean(actionID)" @click="requestVerification">
                    <template #start>
                      <Send v-if="actionID !== 'email-verification'" :size="16" />
                    </template>{{ t('account.sendVerification') }}
                  </UiButton>
                </article>
                <article v-for="item in emailActions" :key="item.id">
                  <span class="account-setting-row-icon"><Mail :size="19" /></span>
                  <div><strong>{{ t(`account.emailActionKinds.${item.kind}`) }}</strong><span>{{ item.emailHint }} · {{ t(`account.emailActionStatuses.${item.status}`) }}</span><small>{{ t('account.emailActionEvidence', { attempts: item.attemptCount, date: date(item.expiresAt) }) }}</small></div>
                </article>
                <div v-if="emailActionNextCursor" class="account-evidence-pagination">
                  <UiButton class="command-button secondary" variant="secondary" :loading="emailActionsLoadingMore" @click="loadMoreEmailActions">
                    {{ t('actions.loadMore') }}
                  </UiButton>
                </div>
              </div>
            </section>

            <section class="account-settings-card">
              <header class="account-settings-card-header account-settings-card-header--action">
                <span class="account-settings-card-icon"><Laptop2 :size="20" /></span>
                <div><h3>{{ t('account.activeSessions') }}</h3><p>{{ t('account.activeSessionsSummary') }}</p></div>
                <UiButton variant="secondary" size="sm" type="button" :loading="actionID === 'others'" @click="revokeOthers">
                  {{ t('account.signOutOthers') }}
                </UiButton>
              </header>
              <div class="account-settings-card-body session-list">
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
              </div>
            </section>
          </div>

          <div v-else-if="section === 'connections'" class="settings-layout">
            <aside><span class="settings-aside-title">{{ t('account.signInMethods') }}</span><p>{{ t('account.signInMethodsSummary') }}</p></aside>
            <section class="settings-panel connection-list">
              <article><Mail :size="19" /><div><strong>{{ t('account.emailPassword') }}</strong><span>{{ session.user.email }}</span></div><span class="availability-label available">{{ t('account.active') }}</span></article>
              <article v-for="provider in providers" :key="provider.provider">
                <Github v-if="provider.provider === 'github'" :size="19" /><Globe2 v-else :size="19" /><div><strong>{{ provider.name }}</strong><span>{{ t('account.signInMethodsSummary') }}</span></div><span class="availability-label">{{ t('account.unavailable') }}</span>
              </article>
            </section>
            <aside><span class="settings-aside-title">{{ t('account.notifications') }}</span><p>{{ t('account.notificationsSummary') }}</p></aside>
            <section class="settings-panel connection-list">
              <RouterLink class="settings-command" to="/notifications?view=preferences">
                <span><strong>{{ t('account.manageNotifications') }}</strong><small>{{ t('account.manageNotificationsSummary') }}</small></span><RefreshCw :size="17" />
              </RouterLink>
            </section>
          </div>

          <div v-else-if="section === 'payouts'" class="settings-layout payout-layout">
            <aside><span class="settings-aside-title">{{ t('account.payouts') }}</span><p>{{ t('account.payoutsSummary') }}</p></aside>
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
            <aside><span class="settings-aside-title">{{ t('account.developerAccess') }}</span><p>{{ t('account.developerAccessSummary') }}</p></aside>
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

            <aside><span class="settings-aside-title">{{ t('account.serviceAccounts') }}</span><p>{{ t('account.serviceAccountsSummary') }}</p></aside>
            <section class="settings-panel developer-accounts">
              <UiForm surface="muted" class="account-form developer-create" @submit.prevent="createDeveloperAccount">
                <label>{{ t('account.serviceAccountName') }}<UiInput v-model="developerForm.accountName" minlength="3" maxlength="80" required /></label>
                <UiButton class="command-button primary" variant="primary" type="submit" :loading="actionID === 'developer-account-create'" :disabled="!developerAccess?.control.enabled">
                  <template #start>
                    <Plus v-if="actionID !== 'developer-account-create'" :size="17" />
                  </template>{{ t('account.createServiceAccount') }}
                </UiButton>
              </UiForm>
              <UiEmptyState v-if="developerAccess && !developerAccess.accounts.length" density="compact" :title="t('account.noServiceAccounts')" />
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

            <aside><span class="settings-aside-title">{{ t('account.oneTimeKey') }}</span><p>{{ t('account.oneTimeKeySummary') }}</p></aside>
            <section class="settings-panel developer-secret">
              <div v-if="revealedCredential">
                <code>{{ revealedCredential.plaintextKey }}</code><UiButton class="command-button secondary" variant="secondary" @click="copyDeveloperKey">
                  <template #start>
                    <Copy :size="16" />
                  </template>{{ t('account.copyKey') }}
                </UiButton>
              </div>
              <UiEmptyState v-else density="compact" :title="t('account.noRevealedKey')" />
            </section>

            <aside><span class="settings-aside-title">{{ t('account.webhookEndpoints') }}</span><p>{{ t('account.webhookEndpointsSummary') }}</p></aside>
            <section class="settings-panel webhook-endpoints">
              <UiForm surface="muted" class="account-form webhook-create" @submit.prevent="createDeveloperWebhook">
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
              </UiForm>
              <UiEmptyState v-if="webhookAccess && !webhookAccess.endpoints.length" density="compact" :title="t('account.noWebhooks')" />
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
                <UiEmptyState v-else density="compact" :title="t('account.noWebhookDeliveries')" />
              </article>
              <div v-if="webhookAccess?.endpoints.some(endpoint => endpoint.status === 'active')" class="developer-danger-controls webhook-danger-controls">
                <label>{{ t('account.operationReason') }}<UiTextarea v-model="webhookForm.reason" rows="2" minlength="10" maxlength="500" /></label>
                <label class="rights-confirm"><UiCheckbox v-model="webhookForm.confirmed" />{{ t('account.webhookConfirm') }}</label>
              </div>
            </section>

            <aside><span class="settings-aside-title">{{ t('account.oneTimeWebhookSecret') }}</span><p>{{ t('account.oneTimeWebhookSecretSummary') }}</p></aside>
            <section class="settings-panel developer-secret">
              <div v-if="revealedWebhookCredential">
                <code>{{ revealedWebhookCredential.signingSecret }}</code><UiButton class="command-button secondary" variant="secondary" @click="copyWebhookSecret">
                  <template #start>
                    <Copy :size="16" />
                  </template>{{ t('account.copyWebhookSecret') }}
                </UiButton>
              </div>
              <UiEmptyState v-else density="compact" :title="t('account.noRevealedWebhookSecret')" />
            </section>
          </div>

          <div v-else-if="section === 'privacy'" class="settings-layout data-rights-layout">
            <aside><span class="settings-aside-title">{{ t('account.privacyRights') }}</span><p>{{ t('account.privacyRightsSummary') }}</p></aside>
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

            <aside><span class="settings-aside-title">{{ t('account.rightsHistory') }}</span><p>{{ t('account.rightsHistorySummary') }}</p></aside>
            <section class="settings-panel data-rights-list">
              <UiEmptyState v-if="!dataRightsRequests.length" density="compact" :title="t('account.noRightsRequests')" />
              <article v-for="item in dataRightsRequests" v-else :key="item.id">
                <div>
                  <strong>{{ t(`account.rightsTypes.${item.requestType}`) }}</strong>
                  <span>{{ t(`account.rightsStatuses.${item.status}`) }} · {{ date(item.createdAt) }}</span>
                  <small v-if="item.export">{{ bytes(item.export.sizeBytes) }} · SHA-256 {{ item.export.checksumSha256.slice(0, 16) }}… · {{ t('account.expires', { date: date(item.export.expiresAt) }) }}</small>
                  <small v-else-if="item.cancelUntil">{{ t('account.cancelUntil', { date: date(item.cancelUntil) }) }}</small>
                  <small v-if="item.status === 'failed' && item.requestType === 'data_export'">{{ t('account.exportFailedHelp') }}</small>
                  <small v-if="item.failureCode && ['data_export_too_large', 'data_export_record_too_large', 'data_export_storage_unavailable', 'data_export_busy'].includes(item.failureCode)">{{ t(`errors.codes.${item.failureCode}`) }}</small>
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
        </div>
      </div>

      <UiDrawer v-model:open="profileDrawerOpen" size="md" :label="t('account.editProfile')">
        <UiForm surface="muted" class="account-profile-drawer" @submit.prevent="saveProfile">
          <header>
            <div><span class="status-label">{{ t('account.accountLabel') }}</span><h2>{{ t('account.editProfile') }}</h2><p>{{ t('account.editProfileSummary') }}</p></div>
            <UiIconButton variant="ghost" :label="t('actions.close')" @click="profileDrawerOpen = false">
              <X :size="19" />
            </UiIconButton>
          </header>
          <div class="account-profile-drawer-body account-form">
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
          </div>
          <footer>
            <UiButton variant="secondary" @click="profileDrawerOpen = false">
              {{ t('actions.cancel') }}
            </UiButton>
            <UiButton variant="primary" type="submit" :loading="session.loading">
              {{ t('account.saveProfile') }}
            </UiButton>
          </footer>
        </UiForm>
      </UiDrawer>
    </template>
    <div v-else class="page-state">
      <p v-if="error || session.error" class="form-error" role="alert">
        {{ error || session.error }}
      </p>
      <UiButton as="RouterLink" :to="{ path: '/auth', query: { returnTo: route.fullPath } }">
        {{ t('account.signIn') }}
      </UiButton>
    </div>
  </section>
</template>
