import { expect, test, type Page } from '@playwright/test'

const measureLayout = async (page: Page, path: string, tabName: string) => {
  await page.goto(path)
  await expect(page.getByRole('tab', { name: tabName, exact: true })).toBeVisible()
  const header = await page.locator('.page-hero-header').boundingBox()
  const switcherBar = await page.locator('.view-switcher-bar').boundingBox()
  const activeTab = page.locator('.view-switcher button.active')
  const activePill = page.locator('.view-switcher .t-tabs-pill')
  expect(header).not.toBeNull()
  expect(switcherBar).not.toBeNull()
  await expect(activeTab).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
  await expect.poll(() => activePill.evaluate(element => element.getBoundingClientRect().width)).toBeGreaterThan(0)
  const activeTabBox = await activeTab.boundingBox()
  const activePillBox = await activePill.boundingBox()
  expect(activeTabBox).not.toBeNull()
  expect(activePillBox).not.toBeNull()
  expect(Math.abs(activeTabBox!.x - activePillBox!.x)).toBeLessThanOrEqual(1)
  expect(Math.abs(activeTabBox!.width - activePillBox!.width)).toBeLessThanOrEqual(1)
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
      account: box('.page-hero-account'),
    }
  })
  return { header: header!, switcherBar: switcherBar!, parts }
}

const expectSlidingPill = async (page: Page, path: string, fromName: string, toName: string) => {
  await page.goto(path)
  const from = page.getByRole('tab', { name: fromName, exact: true })
  const to = page.getByRole('tab', { name: toName, exact: true })
  const pill = page.locator('.view-switcher .t-tabs-pill')
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
  const session = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(session.ok()).toBeTruthy()

  await page.addInitScript(() => localStorage.setItem('hcai-theme', 'light'))
  await page.setViewportSize({ width: 1280, height: 800 })

  const community = await measureLayout(page, '/community', 'Latest discussions')
  const toolbarTop = await page.locator('.community-toolbar').evaluate(element => element.getBoundingClientRect().top)
  await page.getByRole('tab', { name: 'Most discussed', exact: true }).click()
  await expect(page.getByRole('tab', { name: 'Most discussed', exact: true })).toHaveAttribute('aria-selected', 'true')
  await expect.poll(() => page.locator('.community-toolbar').evaluate(element => element.getBoundingClientRect().top)).toBe(toolbarTop)

  const tasks = await measureLayout(page, '/market/demands', 'Available work')
  expect(community.header.height).toBe(320)
  expect(tasks.header.height).toBe(community.header.height)
  expect(tasks.header.y).toBe(community.header.y)
  expect(tasks.switcherBar.height).toBe(community.switcherBar.height)
  expect(tasks.switcherBar.y).toBe(community.switcherBar.y)
  for (const key of ['copy', 'stats', 'actions', 'art', 'account'] as const) {
    expect(tasks.parts[key]).not.toBeNull()
    expect(community.parts[key]).not.toBeNull()
    expect(tasks.parts[key]!.x).toBe(community.parts[key]!.x)
    expect(tasks.parts[key]!.y).toBe(community.parts[key]!.y)
    expect(tasks.parts[key]!.width).toBe(community.parts[key]!.width)
    expect(tasks.parts[key]!.height).toBe(community.parts[key]!.height)
  }

  await page.setViewportSize({ width: 390, height: 844 })
  const mobileParts: Array<Record<string, { x: number; y: number; width: number; height: number } | null>> = []
  for (const [path, tabName] of [['/community', 'Latest discussions'], ['/market/demands', 'Available work']] as const) {
    await page.goto(path)
    await expect(page.getByRole('tab', { name: tabName, exact: true })).toBeVisible()
    await expect(page.locator('.view-switcher')).toHaveCSS('width', '358px')
    const widths = await page.locator('.view-switcher button').evaluateAll(buttons => buttons.map(button => button.getBoundingClientRect().width))
    expect(widths[0]).toBe(widths[1])
    mobileParts.push(await page.locator('.page-hero-header').evaluate(element => {
      const box = (selector: string) => {
        const rect = element.querySelector(selector)?.getBoundingClientRect()
        return rect ? { x: rect.x, y: rect.y, width: rect.width, height: rect.height } : null
      }
      return { copy: box('.page-hero-copy'), stats: box('.page-hero-stats'), actions: box('.page-hero-actions'), art: box('.page-hero-art') }
    }))
    const viewport = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
    expect(viewport.scroll).toBe(viewport.client)
  }
  for (const key of ['copy', 'stats', 'actions', 'art'] as const) {
    expect(mobileParts[0][key]).not.toBeNull()
    expect(mobileParts[1][key]).not.toBeNull()
    expect(mobileParts[0][key]!.x).toBe(mobileParts[1][key]!.x)
    expect(mobileParts[0][key]!.y).toBe(mobileParts[1][key]!.y)
    expect(mobileParts[0][key]!.width).toBe(mobileParts[1][key]!.width)
    expect(mobileParts[0][key]!.height).toBe(mobileParts[1][key]!.height)
  }
})

test('animates both Community and Task marketplace view switchers', async ({ page }) => {
  const session = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(session.ok()).toBeTruthy()
  await page.addInitScript(() => localStorage.setItem('hcai-theme', 'light'))

  await expectSlidingPill(page, '/community', 'Latest discussions', 'Most discussed')
  await expectSlidingPill(page, '/market/demands', 'Available work', 'My activity')
})
