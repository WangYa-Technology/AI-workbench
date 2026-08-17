import { createI18n } from 'vue-i18n'
import enUS from './messages/en-US'
import zhCN from './messages/zh-CN'

export const i18n = createI18n({
  legacy: false,
  locale: 'en-US',
  fallbackLocale: 'en-US',
  messages: { 'en-US': enUS, 'zh-CN': zhCN },
})
