import type { Directive } from 'vue'

type MotionTabsState = {
  active?: HTMLElement
  frame?: number
  initialized: boolean
  pill: HTMLElement
  resizeObserver: ResizeObserver
}

const tabStates = new WeakMap<HTMLElement, MotionTabsState>()

function selectedTab(root: HTMLElement) {
  return root.querySelector<HTMLElement>('.t-tab[aria-selected="true"]')
}

function movePill(root: HTMLElement, animate: boolean) {
  const state = tabStates.get(root)
  const active = selectedTab(root)
  if (!state || !active) return

  const move = () => {
    state.pill.style.transform = `translateX(${active.offsetLeft}px)`
    state.pill.style.width = `${active.offsetWidth}px`
  }

  if (!animate) {
    const previous = state.pill.style.transition
    state.pill.style.transition = 'none'
    move()
    void state.pill.offsetWidth
    state.pill.style.transition = previous
  } else {
    move()
  }

  state.active = active
  state.initialized = true
}

function scheduleMove(root: HTMLElement, animate: boolean) {
  const state = tabStates.get(root)
  if (!state) return
  if (state.frame !== undefined) cancelAnimationFrame(state.frame)
  state.frame = requestAnimationFrame(() => {
    state.frame = undefined
    movePill(root, animate)
  })
}

export const motionTabs: Directive<HTMLElement> = {
  mounted(root) {
    const pill = root.querySelector<HTMLElement>('.t-tabs-pill')
    if (!pill) return
    const resizeObserver = new ResizeObserver(() => scheduleMove(root, false))
    tabStates.set(root, { initialized: false, pill, resizeObserver })
    resizeObserver.observe(root)
    scheduleMove(root, false)
  },
  updated(root) {
    const state = tabStates.get(root)
    const active = selectedTab(root)
    if (!state || !active) return
    if (state.active === active) return
    scheduleMove(root, state.initialized)
  },
  beforeUnmount(root) {
    const state = tabStates.get(root)
    state?.resizeObserver.disconnect()
    if (state?.frame !== undefined) cancelAnimationFrame(state.frame)
    tabStates.delete(root)
  },
}
