import { createRouter, createWebHistory } from 'vue-router'
import { rememberContentList } from '../lib/contentPresentation'

export const router = createRouter({
  history: createWebHistory(),
  scrollBehavior: (to, from, savedPosition) => {
    const publishStateChanged = to.path === from.path
      && (to.query.publish !== from.query.publish || to.query.draftId !== from.query.draftId)
    if (publishStateChanged) return false
    return savedPosition || { top: 0 }
  },
  routes: [
    { path: '/', name: 'home', component: () => import('../pages/GuestHomePage.vue') },
    { path: '/discover', name: 'discover', component: () => import('../pages/DiscoverPage.vue') },
    { path: '/search', name: 'search', component: () => import('../pages/SearchPage.vue') },
    { path: '/create/:mode?', name: 'create', component: () => import('../pages/CreatePage.vue') },
    { path: '/works/:id', name: 'work', component: () => import('../pages/WorkPage.vue') },
    { path: '/creators/:handle', name: 'creator', component: () => import('../pages/CreatorPage.vue') },
    { path: '/market', name: 'marketplace', component: () => import('../pages/MarketplacePage.vue') },
    { path: '/market/assets/:id', name: 'product', component: () => import('../pages/MarketplacePage.vue') },
    { path: '/market/demands', name: 'demands', component: () => import('../pages/TaskMarketplacePage.vue') },
    { path: '/market/demands/:id', name: 'demand', component: () => import('../pages/TaskMarketplacePage.vue') },
    { path: '/community', name: 'community', component: () => import('../pages/CommunityPage.vue') },
    { path: '/community/posts/:id', name: 'community-post', component: () => import('../pages/CommunityPostPage.vue') },
    { path: '/workspace/assets/:assetId', name: 'asset', component: () => import('../pages/WorkspacePage.vue') },
    { path: '/workspace/generations', redirect: '/create/image' },
    { path: '/workspace/:section?', name: 'workspace', component: () => import('../pages/WorkspacePage.vue') },
    {
      path: '/publish',
      redirect: (to) => {
        const { assetId, ...query } = to.query
        const requestedAsset = Array.isArray(assetId) ? assetId[0] : assetId
        return {
          path: '/workspace/assets',
          query: requestedAsset ? { ...query, publish: requestedAsset } : query,
        }
      },
    },
    { path: '/notifications', name: 'notifications', component: () => import('../pages/NotificationsPage.vue') },
    { path: '/support/:caseId?', name: 'support', component: () => import('../pages/SupportPage.vue') },
    { path: '/auth', name: 'auth', component: () => import('../pages/AuthPage.vue') },
    { path: '/settings', name: 'settings', component: () => import('../pages/AccountPage.vue') },
    { path: '/verify-email', name: 'verify-email', component: () => import('../pages/EmailActionPage.vue') },
    { path: '/reset-password', name: 'reset-password', component: () => import('../pages/EmailActionPage.vue') },
    { path: '/admin', name: 'admin', component: () => import('../pages/AdminPage.vue') },
    { path: '/policies/:policy?', name: 'policies', component: () => import('../pages/LegalPage.vue') },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})
router.afterEach(to => rememberContentList(to.path, to.fullPath))
