import { computed, watchEffect } from 'vue'
import { defineStore } from 'pinia'
import { useLocalStorage, usePreferredDark } from '@vueuse/core'
import { i18n } from '../i18n'

export type Locale = 'en-US' | 'zh-CN'
export type Theme = 'light' | 'dark' | 'system'

export const usePreferencesStore = defineStore('preferences', () => {
  const preferredDark = usePreferredDark()
  const theme = useLocalStorage<Theme>('hcai-theme', 'system')
  const storedLocale = useLocalStorage<string>('hcai-locale', 'en-US')
  const locale = computed<Locale>({
    get: () => storedLocale.value === 'zh-CN' ? 'zh-CN' : 'en-US',
    set: (value) => { storedLocale.value = value },
  })
  const resolvedTheme = computed(() => theme.value === 'system' ? (preferredDark.value ? 'dark' : 'light') : theme.value)

  watchEffect(() => {
    document.documentElement.dataset.theme = resolvedTheme.value
    document.documentElement.lang = locale.value === 'zh-CN' ? 'zh-CN' : 'en'
    i18n.global.locale.value = locale.value
  })

  function toggleTheme() {
    theme.value = resolvedTheme.value === 'dark' ? 'light' : 'dark'
  }

  function toggleLocale() {
    locale.value = locale.value === 'en-US' ? 'zh-CN' : 'en-US'
  }

  return { theme, locale, resolvedTheme, toggleTheme, toggleLocale }
})
