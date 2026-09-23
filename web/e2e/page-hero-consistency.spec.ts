import { fixtureCredentials } from './helpers/identity'
import { expect, test, type Page } from '@playwright/test'

const heroPages = [
  '/market/demands',
  '/community',
  '/market',
  '/workspace/assets',
  '/workspace/purchases',
  '/workspace/orders',
  '/workspace/tasks',
  '/support',
  '/discover',
] as const

type Box = { x: number; y: number; width: number; height: number } | null

async function measureHero(page: Page, path: string) {
  await page.goto(path)
  const hero = page.locator('.ui-page-hero')
  await expect(hero).toBeVisible()
  if (['/discover', '/community', '/market'].includes(path)) {
    await expect(hero.locator('.page-hero-stats')).toHaveCount(0)
  }
  await expect(hero.locator('.page-hero-account')).toHaveCount(0)

  await hero.evaluate(async (element) => {
    const animations = []
    for (let node: Element | null = element; node; node = node.parentElement) {
      animations.push(...node.getAnimations())
    }
    await Promise.all(animations.map(animation => animation.finished.catch(() => {})))
  })
  return hero.evaluate((element) => {
    const root = element.getBoundingClientRect()
    const box = (selector: string): Box => {
      const rect = element.querySelector(selector)?.getBoundingClientRect()
      return rect && rect.width > 0 && rect.height > 0
        ? { x: rect.x - root.x, y: rect.y - root.y, width: rect.width, height: rect.height }
        : null
    }
    return {
      root: { x: root.x, y: root.y, width: root.width, height: root.height },
      copy: box('.page-hero-copy'),
      stats: box('.page-hero-stats'),
      actions: box('.page-hero-actions'),
      artwork: box('.page-hero-art'),
    }
  })
}

test('keeps compact shared headers and illustrations within bounds across pages', async ({ page }) => {
  const session = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  expect(session.ok()).toBeTruthy()
  await page.addInitScript(() => localStorage.setItem('hcai-theme', 'light'))

  for (const viewport of [{ width: 1287, height: 904 }, { width: 1024, height: 900 }, { width: 768, height: 900 }, { width: 360, height: 844 }]) {
    await page.setViewportSize(viewport)
    const measurements = []
    for (const path of heroPages) {
      measurements.push(await measureHero(page, path))
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
      expect(overflow, `${path} has horizontal overflow at ${viewport.width}px`).toBe(0)
    }

    const baseline = measurements[1]!
    for (const [index, measurement] of measurements.entries()) {
      expect(measurement.root.width).toBe(baseline.root.width)
      for (const key of ['copy', 'stats', 'actions', 'artwork'] as const) {
        const part = measurement[key]
        if (!part) continue
        expect(part.x, heroPages[index]).toBeGreaterThanOrEqual(0)
        expect(part.y, heroPages[index]).toBeGreaterThanOrEqual(0)
        expect(part.x + part.width).toBeLessThanOrEqual(measurement.root.width + 1)
        expect(part.y + part.height).toBeLessThanOrEqual(measurement.root.height + 1)
      }
      expect(measurement.artwork).not.toBeNull()
      expect(measurement.artwork!.width).toBe(viewport.width >= 768 ? 112 : 64)
      expect(measurement.artwork!.x).toBe(0)
      expect(measurement.artwork!.x + measurement.artwork!.width).toBeLessThanOrEqual(measurement.copy!.x)
      if (viewport.width >= 1101 && measurement.actions) {
        expect(measurement.copy!.x + measurement.copy!.width).toBeLessThanOrEqual(measurement.actions.x)
      }
      if (viewport.width >= 1101 && ['/community', '/discover'].includes(heroPages[index]!)) {
        expect(measurement.root.height).toBeLessThanOrEqual(160)
      }
    }
  }
})

test('keeps shared Hero artwork visible and dimensions stable in dark mode', async ({ page }) => {
  const session = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  expect(session.ok()).toBeTruthy()
  await page.addInitScript(() => localStorage.setItem('hcai-theme', 'dark'))
  await page.setViewportSize({ width: 1287, height: 904 })

  for (const path of heroPages) {
    await page.goto(path)
    const hero = page.locator('.ui-page-hero')
    await expect(hero).toBeVisible()
    await expect(hero.locator('.page-hero-art')).toBeVisible()
    await expect(hero.locator('.page-hero-art')).toHaveCSS('width', '112px')
    expect((await hero.boundingBox())!.height).toBeLessThan(320)
  }
})

test('shows the current page title in the content scrollport after the Hero leaves view', async ({ page }) => {
  const session = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  expect(session.ok()).toBeTruthy()
  await page.addInitScript(() => localStorage.setItem('hcai-theme', 'light'))
  await page.setViewportSize({ width: 1287, height: 904 })
  await page.goto('/notifications?view=preferences')
  await expect(page.locator('.notification-preference-panel article').first()).toBeVisible()

  const main = page.locator('#main-content')
  const contextBar = page.locator('.content-context-bar-surface')
  await expect(contextBar).toBeHidden()

  await main.evaluate((element) => {
    const hero = element.querySelector<HTMLElement>('.page-hero-header')
    element.scrollTop = (hero?.offsetTop || 0) + (hero?.offsetHeight || 0) + 1
  })

  await expect(contextBar).toBeVisible()
  await expect(contextBar).toHaveText('Notifications')
  await expect(contextBar).toHaveCSS('backdrop-filter', /blur\(18px\)/)
  const headerLeft = await page.locator('.ui-page-header').evaluate(element => element.getBoundingClientRect().left)
  await expect.poll(() => contextBar.locator('span').evaluate(element => element.getBoundingClientRect().left)).toBeCloseTo(headerLeft, 0)

  await main.evaluate(element => { element.scrollTop = 0 })
  await expect(contextBar).toBeHidden()
})
