import { fixtureCredentials } from './helpers/identity'
import { expect, test, type Page } from '@playwright/test'

const measureLayout = async (page: Page, path: string, tabName: string) => {
  await page.goto(path)
  await expect(page.getByRole('tab', { name: tabName, exact: true })).toBeVisible()
  const header = await page.locator('.page-hero-header').boundingBox()
  const switcherBar = await page.locator('.view-switcher-bar').boundingBox()
  const activeTab = page.locator('.view-switcher [aria-selected="true"]')
  const activePill = page.locator('.view-switcher .ui-tabs__indicator')
  expect(header).not.toBeNull()
  expect(switcherBar).not.toBeNull()
  await expect(activeTab).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
  await expect.poll(() => activePill.evaluate(element => element.getBoundingClientRect().width)).toBeGreaterThan(0)
  await expect.poll(async () => {
    const [tab, pill] = await Promise.all([activeTab.boundingBox(), activePill.boundingBox()])
    return tab && pill ? Math.abs(tab.x - pill.x) : Number.POSITIVE_INFINITY
  }).toBeLessThanOrEqual(1)
  await expect.poll(async () => {
    const [tab, pill] = await Promise.all([activeTab.boundingBox(), activePill.boundingBox()])
    return tab && pill ? Math.abs(tab.width - pill.width) : Number.POSITIVE_INFINITY
  }).toBeLessThanOrEqual(1)
  const activeTabBox = await activeTab.boundingBox()
  const activePillBox = await activePill.boundingBox()
  expect(activeTabBox).not.toBeNull()
  expect(activePillBox).not.toBeNull()
  const parts = await page.locator('.page-hero-header').evaluate(element => {
    const box = (selector: string) => {
      const rect = element.querySelector(selector)?.getBoundingClientRect()
      return rect ? { x: rect.x, y: rect.y, width: rect.width, height: rect.height } : null
    }
    return {
      copy: box('.page-hero-copy'),
      stats: box('.page-hero-stats'),
      actions: box('.page-hero-actions'),
      art: box('.page-hero-art'),
    }
  })
  return { header: header!, switcherBar: switcherBar!, parts }
}

const expectSlidingPill = async (page: Page, path: string, fromName: string, toName: string) => {
  await page.goto(path)
  const from = page.getByRole('tab', { name: fromName, exact: true })
  const to = page.getByRole('tab', { name: toName, exact: true })
  const pill = page.locator('.view-switcher .ui-tabs__indicator')
  await expect(from).toHaveAttribute('aria-selected', 'true')
  await expect.poll(() => pill.evaluate(element => element.getBoundingClientRect().width)).toBeGreaterThan(0)

  const startX = await pill.evaluate(element => element.getBoundingClientRect().x)
  const targetX = await to.evaluate(element => element.getBoundingClientRect().x)
  await to.click()
  await expect(to).toHaveAttribute('aria-selected', 'true')
  await page.waitForTimeout(80)
  const middleX = await pill.evaluate(element => element.getBoundingClientRect().x)

  expect(middleX).toBeGreaterThan(startX + 1)
  expect(middleX).toBeLessThan(targetX - 1)
  await expect.poll(() => pill.evaluate(element => element.getBoundingClientRect().x)).toBeCloseTo(targetX, 0)
}

test('keeps Community and Task marketplace headers and view switchers aligned', async ({ page }) => {
  const session = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  expect(session.ok()).toBeTruthy()

  await page.addInitScript(() => localStorage.setItem('hcai-theme', 'light'))
  await page.setViewportSize({ width: 1280, height: 800 })

  for (const viewport of [{ width: 1280, height: 800 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport)
    const measurements = []
    for (const [path, tab] of [['/community', 'All discussions'], ['/market/demands', 'Available work']] as const) {
      measurements.push(await measureLayout(page, path, tab))
      const bar = page.locator('.ui-filter-bar')
      await expect(bar).toHaveCSS('--control-height-toolbar', '40px')
      const controls = await bar.locator('.ui-filter-bar__search, .ui-select__trigger, .community-tools > button').evaluateAll(elements => elements.map(element => {
        const rect = element.getBoundingClientRect()
        return { x: rect.x, right: rect.right, height: rect.height }
      }).filter(rect => rect.height > 0))
      expect(controls.length).toBeGreaterThanOrEqual(2)
      for (const control of controls) {
        expect(control.height).toBe(40)
        expect(control.x).toBeGreaterThanOrEqual(0)
        expect(control.right).toBeLessThanOrEqual(viewport.width)
      }
      const tabs = await page.locator('.view-switcher button').evaluateAll(elements => elements.map(element => element.getBoundingClientRect().width))
      if (viewport.width < 768) expect(tabs[0]).toBe(tabs[1])
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(viewport.width)
    }
    expect(measurements[0]!.header.width).toBe(measurements[1]!.header.width)
    if (viewport.width >= 1200) {
      expect(measurements[0]!.header.height).toBeLessThanOrEqual(160)
      expect(measurements[1]!.header.height).toBeLessThan(300)
      expect(measurements[0]!.switcherBar.x).toBe(measurements[1]!.switcherBar.x)
    }
  }
})

test('animates both Community and Task marketplace view switchers', async ({ page }) => {
  const session = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  expect(session.ok()).toBeTruthy()
  await page.addInitScript(() => localStorage.setItem('hcai-theme', 'light'))

  await expectSlidingPill(page, '/community', 'All discussions', 'My discussions')
  await expectSlidingPill(page, '/market/demands', 'Available work', 'My activity')
})
