import { nextTick } from 'vue'
import type { Router, RouteLocationNormalized } from 'vue-router'

let homePosition: { left: number; top: number } | undefined
let homeExitWasSlide = false

export async function restorePublicHomePosition(
  to: RouteLocationNormalized,
  from: RouteLocationNormalized,
  savedPosition: { left: number; top: number } | null,
) {
  if (to.name !== 'home' || !from.matched.length || from.name === 'home') return
  const position = savedPosition ?? homePosition ?? { left: 0, top: 0 }
  // Set the header's final height before the router restores the scroll offset.
  // Otherwise its compact layout shifts the content after the snapshot is taken.
  await nextTick()
  const root = globalThis.document.documentElement
  root.dataset.publicHomeRestoring = 'true'
  globalThis.dispatchEvent(new CustomEvent('public-page-snapshot', { detail: position }))
  await nextTick()
  globalThis.setTimeout(() => { delete root.dataset.publicHomeRestoring }, 0)
  return { ...position, behavior: 'instant' as const }
}

// Capture the outgoing shell before Vue commits the new route. Resolve the router
// guard inside the snapshot update, then wait for rendering and scroll restoration.
export function installPublicPageTransition(router: Router) {
  let active: { transition?: ViewTransition; settle: () => void; release: () => void } | undefined
  let fallbackTimer: ReturnType<typeof globalThis.setTimeout> | undefined
  let direction: 'forward' | 'back' | 'slide-forward' | 'slide-back' | undefined
  let revision = 0
  const root = globalThis.document.documentElement

  function setOrigin() {
    const button = globalThis.document.querySelector('.home-login')
    const box = button?.getBoundingClientRect()
    const width = root.clientWidth
    const height = globalThis.innerHeight
    // A direct visit to /auth still closes toward the destination's login button.
    const left = Math.max(0, Math.min(box?.left ?? width - 96, width - 1))
    const top = Math.max(0, Math.min(box?.top ?? 24, height - 1))
    const right = Math.max(0, width - Math.min(box?.right ?? width - 24, width))
    const bottom = Math.max(0, height - Math.min(box?.bottom ?? 60, height))
    const buttonWidth = Math.max(1, width - right - left)
    const buttonHeight = Math.max(1, height - bottom - top)
    // Scale uniformly so text and artwork never squash. Crop the excess to the
    // button's actual bounds and radius in snapshot coordinates.
    const scale = Math.max(buttonWidth / width, buttonHeight / height)
    const insetX = Math.max(0, (width - buttonWidth / scale) / 2)
    const insetY = Math.max(0, (height - buttonHeight / scale) / 2)
    const buttonRadius = Number.parseFloat(button
      ? globalThis.getComputedStyle(button).borderTopLeftRadius
      : globalThis.getComputedStyle(root).getPropertyValue('--radius-button')) || 10
    const radius = Math.min(buttonRadius, buttonWidth / 2, buttonHeight / 2) / scale
    root.style.setProperty('--public-page-origin', `${left + buttonWidth / 2}px ${top + buttonHeight / 2}px`)
    root.style.setProperty('--public-page-start-transform', `translate(${left - insetX * scale}px, ${top - insetY * scale}px) scale(${scale})`)
    root.style.setProperty('--public-page-button-clip', `inset(${insetY}px ${insetX}px round ${radius}px)`)
  }

  function cancel() {
    revision++
    const previous = active
    active = undefined
    previous?.transition?.skipTransition()
    previous?.release()
    previous?.settle()
    globalThis.clearTimeout(fallbackTimer)
    delete root.dataset.publicPageTransition
    delete root.dataset.publicPageFallback
    root.style.removeProperty('--public-page-origin')
    root.style.removeProperty('--public-page-start-transform')
    root.style.removeProperty('--public-page-button-clip')
  }

  router.beforeResolve((to, from) => {
    // A protected home entry may immediately redirect to sign-in after mounting.
    const redirectedEntry = Boolean(active && direction === 'slide-forward' && to.name === 'auth')
    cancel()
    if (from.name === 'home' && to.name !== 'home') {
      homePosition = { left: globalThis.scrollX, top: globalThis.scrollY }
      // Only the header login entry expands from the login button. Actions
      // requiring authentication retain their ordinary directional transition.
      homeExitWasSlide = to.name !== 'auth' || Boolean(to.query.returnTo && to.query.returnTo !== '/')
    }
    direction = from.name === 'home' && to.name !== 'home'
      ? (homeExitWasSlide ? 'slide-forward' : 'forward')
      : to.name === 'home' && from.matched.length && from.name !== 'home'
        ? (from.name === 'auth' && !homeExitWasSlide ? 'back' : 'slide-back')
        : redirectedEntry ? 'slide-forward' : undefined
    // Keep the home content settled even after the route animation finishes.
    // Removing this in cancel() would restart its CSS entrance animations.
    if (direction) root.dataset.publicHomeEntry = 'static'
    else delete root.dataset.publicHomeEntry
    if (!direction || globalThis.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      direction = undefined
      return
    }
    // Do not compete with an in-progress theme snapshot.
    if (root.dataset.themeTransition) { direction = undefined; return }
    if (direction === 'forward') setOrigin()
    if (typeof globalThis.document.startViewTransition !== 'function') return

    return new Promise<void>((release) => {
      let settle!: () => void
      const committed = new Promise<void>(resolve => { settle = resolve })
      const state = { release, settle, transition: undefined as ViewTransition | undefined }
      active = state
      root.dataset.publicPageTransition = direction
      try {
        state.transition = globalThis.document.startViewTransition(async () => {
          release()
          await committed
          await nextTick()
          // Animation frames are suspended while a view-transition update runs.
          // A task boundary lets Vue Router apply its next-tick scroll position.
          await new Promise<void>(resolve => globalThis.setTimeout(resolve, 0))
          if (active === state && (direction === 'back' || direction === 'slide-back')) {
            // Scroll events/frames are deferred during capture. Let the sticky
            // header settle to its restored state before measuring the target.
            globalThis.dispatchEvent(new Event('public-page-snapshot'))
            await nextTick()
            if (direction === 'back') setOrigin()
          }
        })
        const cleanup = () => { if (active === state) cancel() }
        void state.transition.finished.then(cleanup, cleanup)
      } catch {
        cancel()
      }
    })
  })

  router.afterEach(async (_to, _from, failure) => {
    if (failure) { cancel(); direction = undefined; return }
    if (active) { active.settle(); return }
    if (!direction) return
    const fallbackDirection = direction
    const fallbackRevision = revision
    direction = undefined
    await nextTick()
    if (fallbackRevision !== revision) return
    if (fallbackDirection === 'back') setOrigin()
    root.dataset.publicPageFallback = fallbackDirection
    fallbackTimer = globalThis.setTimeout(() => {
      delete root.dataset.publicPageFallback
      root.style.removeProperty('--public-page-origin')
      root.style.removeProperty('--public-page-start-transform')
      root.style.removeProperty('--public-page-button-clip')
    }, 550)
  })
  router.onError(() => { cancel(); direction = undefined })
}
