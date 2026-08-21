import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { i18n } from './i18n'
import { router } from './router'
import { motionTabs } from './directives/motionTabs'
import './styles/tokens.css'
import './styles/base.css'
import './styles/layout.css'
import './styles/motion.css'
import './styles/ui.css'

createApp(App).use(createPinia()).use(i18n).use(router).directive('motion-tabs', motionTabs).mount('#app')
