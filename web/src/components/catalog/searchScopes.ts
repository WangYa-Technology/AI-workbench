import type { RouteLocationNormalizedLoaded } from 'vue-router'
import { Boxes, ClipboardList, Compass, ReceiptText, ShoppingBag, UsersRound } from 'lucide-vue-next'

// Register a catalog here once its list restores the q parameter from the URL.
export const catalogSearchScopes = [
  { route: 'demands', value: 'tasks', label: 'search.currentTasks', placeholder: 'search.taskPlaceholder', inputLabel: 'tasks.searchPlaceholder', icon: ClipboardList },
  { route: 'community', value: 'community', label: 'community.title', placeholder: 'community.searchPlaceholder', inputLabel: 'community.searchLabel', icon: UsersRound },
  { route: 'marketplace', value: 'products', label: 'nav.resourceMarket', placeholder: 'marketplace.searchPlaceholder', inputLabel: 'marketplace.searchLabel', icon: ShoppingBag },
  { route: 'workspace', matches: (route: RouteLocationNormalizedLoaded) => !route.params.section || route.params.section === 'assets', value: 'assets', label: 'workspace.assets', placeholder: 'workspace.searchAssetPlaceholder', inputLabel: 'workspace.searchAssets', icon: Boxes, maxLength: 120 },
  { route: 'workspace', matches: (route: RouteLocationNormalizedLoaded) => route.params.section === 'purchases', value: 'purchases', label: 'workspace.purchases', placeholder: 'workspace.searchPurchasePlaceholder', inputLabel: 'workspace.searchAssets', icon: ShoppingBag, maxLength: 120 },
  { route: 'workspace', matches: (route: RouteLocationNormalizedLoaded) => route.params.section === 'orders', value: 'orders', label: 'workspace.loadedOrders', placeholder: 'workspace.searchLoadedOrders', inputLabel: 'workspace.searchLoadedOrders', icon: ReceiptText, maxLength: 120 },
  { route: 'workspace', matches: (route: RouteLocationNormalizedLoaded) => route.params.section === 'tasks', value: 'workspaceTasks', label: 'tasks.myTasks', placeholder: 'workspace.searchMyTasks', inputLabel: 'workspace.searchMyTasks', icon: ClipboardList, maxLength: 120 },
  { route: 'discover', value: 'works', label: 'discover.libraryTitle', placeholder: 'discover.searchPlaceholder', inputLabel: 'discover.searchLabel', icon: Compass, maxLength: 120 },
]
