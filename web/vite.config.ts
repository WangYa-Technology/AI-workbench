import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

const apiTarget = process.env.VITE_API_TARGET ?? 'http://127.0.0.1:8080'

export default defineConfig({
  plugins: [vue({
    template: {
      compilerOptions: {
        isCustomElement: tag => tag === 'markdown-toolbar' || tag.startsWith('md-'),
      },
    },
  })],
  build: {
    rollupOptions: {
      output: {
        // Cache the shared framework separately from application code and translations.
        onlyExplicitManualChunks: true,
        manualChunks(id) {
          // Keep each static locale independently cacheable without changing synchronous language switching.
          const locale = id.match(/\/src\/i18n\/messages\/(en-US|zh-CN)\.ts$/)?.[1]
          if (locale) return `locale-${locale}`
          if (/\/node_modules\/(?:@vue\/|@intlify\/|vue\/|vue-router\/|vue-i18n\/|pinia\/)/.test(id)) return 'framework'
        },
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': apiTarget,
      '/health': apiTarget,
      '/ready': apiTarget,
    },
  },
})
