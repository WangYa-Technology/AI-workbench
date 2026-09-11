import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { api, type SiteConfiguration } from '../api/client'
import { cloneSiteConfiguration } from '../lib/siteConfiguration'
import { usePreferencesStore } from './preferences'

const defaults: SiteConfiguration = {
  siteName: 'HCAI CHAT',
  serverUrl: 'http://127.0.0.1:8080',
  siteIconUrl: '/brand/logo.png',
  footerText: {
    enUS: 'Local Test product terms · Production legal acceptance pending',
    zhCN: '本地测试产品条款 · 上线前仍需法律验收',
  },
  policies: {
    terms: { enUS: '', zhCN: '' },
    privacy: { enUS: '', zhCN: '' },
    cookies: { enUS: '', zhCN: '' },
    acceptable: { enUS: '', zhCN: '' },
    ai: { enUS: '', zhCN: '' },
    licensing: { enUS: '', zhCN: '' },
    refunds: { enUS: '', zhCN: '' },
    copyright: { enUS: '', zhCN: '' },
  },
}

let pending: Promise<SiteConfiguration> | null = null

export const useSiteConfigStore = defineStore('siteConfig', () => {
  const preferences = usePreferencesStore()
  const current = ref<SiteConfiguration>(cloneSiteConfiguration(defaults))
  const initialized = ref(false)
  const localizedKey = computed(() => preferences.locale === 'zh-CN' ? 'zhCN' : 'enUS')
  const footerText = computed(() => current.value.footerText[localizedKey.value] || current.value.footerText.enUS)

  async function ensure(force = false) {
    if (initialized.value && !force) return current.value
    if (pending) return pending
    pending = api.siteConfiguration()
      .then(apply)
      .catch(() => current.value)
      .finally(() => {
        initialized.value = true
        pending = null
      })
    return pending
  }

  function apply(configuration: SiteConfiguration) {
    current.value = cloneSiteConfiguration(configuration)
    return current.value
  }

  function policy(key: keyof SiteConfiguration['policies']) {
    const value = current.value.policies[key]
    return value[localizedKey.value] || value.enUS
  }

  return { current, footerText, initialized, ensure, apply, policy }
})
