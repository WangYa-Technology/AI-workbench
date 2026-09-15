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
  await expect(hero.locator('.page-hero-stats article')).toHaveCount(3)
  await expect(hero.locator('.page-hero-account')).toHaveCount(0)

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

test('uses one Hero geometry across marketplace and workspace pages', async ({ page }) => {
  const session = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(session.ok()).toBeTruthy()
  await page.addInitScript(() => localStorage.setItem('hcai-theme', 'light'))

  for (const viewport of [{ width: 1287, height: 904 }, { width: 390, height: 844 }]) {
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
      if (viewport.width >= 768) {
        expect(measurement.root.height).toBe(baseline.root.height)
        for (const key of ['copy', 'stats', 'artwork'] as const) {
          // Task rewards intentionally have wider copy and statistic columns.
          if (index === 0 && key !== 'artwork') {
            expect(measurement[key]?.y).toBe(baseline[key]?.y)
            expect(measurement[key]?.width).toBeGreaterThanOrEqual(baseline[key]?.width || 0)
          } else expect(measurement[key]).toEqual(baseline[key])
        }
      } else {
        expect(measurement.root.height).toBeGreaterThanOrEqual(320)
        if (measurement.actions) {
          expect(measurement.actions.y + measurement.actions.height).toBeLessThanOrEqual(measurement.root.height)
        }
      }
    }
  }
})

test('keeps shared Hero artwork visible and dimensions stable in dark mode', async ({ page }) => {
  const session = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(session.ok()).toBeTruthy()
  await page.addInitScript(() => localStorage.setItem('hcai-theme', 'dark'))
  await page.setViewportSize({ width: 1287, height: 904 })

  for (const path of heroPages) {
    await page.goto(path)
    const hero = page.locator('.ui-page-hero')
    await expect(hero).toBeVisible()
    await expect(hero.locator('.page-hero-art')).toBeVisible()
    await expect(hero).toHaveCSS('height', '272px')
  }
})

test('shows the current page title in the content scrollport after the Hero leaves view', async ({ page }) => {
  const session = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
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

  await main.evaluate(element => { element.scrollTop = 0 })
  await expect(contextBar).toBeHidden()
})
