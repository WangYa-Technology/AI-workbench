import { computed, nextTick, watchEffect } from 'vue'
import { defineStore } from 'pinia'
import { useLocalStorage, usePreferredDark } from '@vueuse/core'
import { i18n } from '../i18n'

export type Locale = 'en-US' | 'zh-CN'
export type Theme = 'light' | 'dark' | 'system'

let activeThemeTransition: ViewTransition | null = null
let themeTransitionFallbackTimer: ReturnType<typeof globalThis.setTimeout> | undefined

export const usePreferencesStore = defineStore('preferences', () => {
  const preferredDark = usePreferredDark()
  const theme = useLocalStorage<Theme>('hcai-theme', 'system')
  const sidebarCollapsed = useLocalStorage<boolean>('hcai-sidebar-collapsed', false)
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
    const nextTheme = resolvedTheme.value === 'dark' ? 'light' : 'dark'
    const root = document.documentElement
    const applyTheme = () => { theme.value = nextTheme }

    if (globalThis.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      applyTheme()
      return
    }

    if (typeof document.startViewTransition === 'function') {
      activeThemeTransition?.skipTransition()
      root.dataset.themeTransition = 'running'
      const transition = document.startViewTransition(async () => {
        applyTheme()
        await nextTick()
      })
      activeThemeTransition = transition
      const clearTransitionState = () => {
        if (activeThemeTransition !== transition) return
        delete root.dataset.themeTransition
        activeThemeTransition = null
      }
      void transition.finished.then(clearTransitionState, clearTransitionState)
      return
    }

    root.dataset.themeTransition = 'fallback'
    globalThis.requestAnimationFrame(() => {
      applyTheme()
      globalThis.clearTimeout(themeTransitionFallbackTimer)
      themeTransitionFallbackTimer = globalThis.setTimeout(() => {
        delete root.dataset.themeTransition
        themeTransitionFallbackTimer = undefined
      }, 250)
    })
  }

  function toggleLocale() {
    locale.value = locale.value === 'en-US' ? 'zh-CN' : 'en-US'
  }

  function toggleSidebar() {
    sidebarCollapsed.value = !sidebarCollapsed.value
  }

  return { theme, locale, resolvedTheme, sidebarCollapsed, toggleTheme, toggleLocale, toggleSidebar }
})
