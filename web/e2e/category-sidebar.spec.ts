import { fixtureCredentials } from './helpers/identity'
import { expect, test } from '@playwright/test'

test('asset view tabs stay mounted through navigation, loading and retry', async ({ page }) => {
  await page.setViewportSize({ width: 1551, height: 901 })
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  await page.goto('/workspace/assets')
  const results = page.locator('.asset-results')
  const tabs = page.locator('.asset-view-toolbar .ui-tabs')
  await expect(results).toHaveAttribute('aria-busy', 'false')
  const original = await tabs.elementHandle()
  expect(original).not.toBeNull()
  await expect(tabs.getByRole('tab')).toHaveCount(2)
  await expect(page.locator('.asset-category-panel')).toHaveCount(0)

  for (const name of ['Saved Works', 'Owned Assets']) {
    let release!: () => void
    const pending = new Promise<void>(resolve => { release = resolve })
    await page.route('**/api/v1/assets', async route => { await pending; await route.continue() })
    try {
      await tabs.getByRole('tab', { name, exact: true }).click()
      await expect(results).toHaveAttribute('aria-busy', 'true')
      await expect(tabs).toBeVisible()
      expect(await original!.evaluate(element => element === document.querySelector('.asset-view-toolbar .ui-tabs'))).toBe(true)
      await expect(tabs.getByRole('tab', { name, exact: true })).toHaveAttribute('aria-selected', 'true')
    } finally { release() }
    await expect(results).toHaveAttribute('aria-busy', 'false')
    await page.unroute('**/api/v1/assets')
  }

  await page.route('**/api/v1/assets', route => route.fulfill({ status: 503, json: { error: { message: 'Temporarily unavailable' } } }))
  await tabs.getByRole('tab', { name: 'Saved Works', exact: true }).click()
  await expect(results.getByRole('alert')).toBeVisible()
  expect(await original!.evaluate(element => element === document.querySelector('.asset-view-toolbar .ui-tabs'))).toBe(true)
  await page.unroute('**/api/v1/assets')
  await results.getByRole('button', { name: 'Try again', exact: true }).click()
  await expect(results).toHaveAttribute('aria-busy', 'false')
  await expect(results.getByRole('alert')).toHaveCount(0)
  expect(await original!.evaluate(element => element === document.querySelector('.asset-view-toolbar .ui-tabs'))).toBe(true)
})

test('catalog category controls share their presentation', async ({ page }) => {
  await page.setViewportSize({ width: 1551, height: 901 })
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  let reference: string[] | undefined
  for (const path of ['/market/demands', '/community', '/discover', '/market']) {
    await page.goto(path)
    const sidebar = page.locator('.ui-category-sidebar')
    await expect(sidebar).toBeVisible()
    const active = sidebar.locator('nav .ui-button.active')
    await expect(active).toHaveCount(1)
    const style = await active.evaluate(element => {
      const css = getComputedStyle(element)
      return ['min-height', 'padding', 'gap', 'border-radius', 'background-color', 'color', 'font-size', 'font-weight'].map(property => css.getPropertyValue(property))
    })
    reference ??= style
    expect(style, path).toEqual(reference)
  }
})

test('shared category highlight slides without moving items and respects reduced motion', async ({ page }) => {
  await page.setViewportSize({ width: 1551, height: 901 })
  await page.emulateMedia({ reducedMotion: 'no-preference' })
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  for (const path of ['/market', '/community', '/discover', '/market/demands']) {
    await page.goto(path)
    const nav = page.locator('.ui-category-sidebar nav')
    await expect(nav).toHaveAttribute('data-positioned', 'true')
    const motion = await nav.evaluate(async element => {
      const buttons = [...element.querySelectorAll<HTMLElement>('.ui-button')]
      const target = buttons.at(-1)!
      const pill = element.querySelector<HTMLElement>('.ui-category-sidebar__indicator')!
      const before = buttons.map(button => button.getBoundingClientRect().y)
      const start = pill.getBoundingClientRect().y
      target.click()
      // Vue and layout must both commit before inspecting the CSS transition.
      await new Promise(requestAnimationFrame)
      await new Promise(requestAnimationFrame)
      const animation = pill.getAnimations().find(item => (item as CSSTransition).transitionProperty === 'transform')
      if (!animation) throw new Error('Category selection did not start a transform transition')
      animation.pause()
      const duration = Number(animation.effect!.getTiming().duration)
      animation.currentTime = duration / 2
      const middle = pill.getBoundingClientRect().y
      animation.finish()
      return { start, middle, end: pill.getBoundingClientRect().y, target: target.getBoundingClientRect().y, duration, before, after: buttons.map(button => button.getBoundingClientRect().y) }
    })
    expect(motion.duration, path).toBe(250)
    expect(motion.middle, path).toBeGreaterThan(motion.start)
    expect(motion.middle, path).toBeLessThan(motion.end)
    expect(motion.end, path).toBeCloseTo(motion.target, 0)
    expect(motion.after, path).toEqual(motion.before)
  }

  await page.emulateMedia({ reducedMotion: 'reduce' })
  const nav = page.locator('.ui-category-sidebar nav')
  await nav.locator('.ui-button').first().click()
  const pill = nav.locator('.ui-category-sidebar__indicator')
  await expect.poll(() => pill.evaluate(element => getComputedStyle(element).transitionDuration)).toBe('0s')
  await page.setViewportSize({ width: 1000, height: 800 })
  await expect(page.locator('.ui-category-sidebar')).toBeHidden()
  await page.setViewportSize({ width: 1551, height: 901 })
  await expect(nav).toHaveAttribute('data-positioned', 'true')
  const active = await nav.locator('.ui-button.active').boundingBox()
  const highlight = await pill.boundingBox()
  expect(highlight!.y).toBeCloseTo(active!.y, 0)
  expect(highlight!.height).toBeCloseTo(active!.height, 0)
})

test('asset list and grid use shared controls without losing the selected layout', async ({ page }) => {
  await page.setViewportSize({ width: 1551, height: 901 })
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  await page.goto('/workspace/assets')
  await expect(page.locator('.asset-results')).toHaveAttribute('aria-busy', 'false')
  const catalog = page.locator('.asset-result-list')
  const card = catalog.locator('.asset-row').first()
  await expect(card).toBeVisible()
  const listWidth = (await card.boundingBox())!.width
  await page.getByRole('button', { name: 'Grid view', exact: true }).click()
  await expect(catalog).toHaveClass(/is-grid/)
  expect((await card.boundingBox())!.width).toBeLessThan(listWidth)
  const media = await card.locator('.asset-row-media').boundingBox()
  const content = await card.locator('.task-card-content').boundingBox()
  expect(content!.y).toBeGreaterThanOrEqual(media!.y + media!.height)
  await page.getByRole('tab', { name: 'Saved Works', exact: true }).click()
  await expect(page.locator('.asset-results')).toHaveAttribute('aria-busy', 'false')
  await expect(page.getByRole('button', { name: 'Grid view', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await expect(catalog).toHaveClass(/is-grid/)
  await page.getByRole('button', { name: 'List view', exact: true }).click()
  await expect(catalog).not.toHaveClass(/is-grid/)
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.getByRole('button', { name: 'Grid view', exact: true })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
})

test('purchases use global navigation and the full content width', async ({ page }) => {
  await page.setViewportSize({ width: 1551, height: 901 })
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  await page.goto('/workspace/assets?view=saved')
  await expect(page.locator('.asset-results')).toHaveAttribute('aria-busy', 'false')

  const savedRequests: string[] = []
  page.on('request', request => {
    if (request.url().includes('/saved-works')) savedRequests.push(request.url())
  })
  await page.locator('#primary-navigation a[href="/workspace/purchases"]').click()
  await expect(page).toHaveURL(/\/workspace\/purchases$/)
  await expect(page.locator('.asset-results')).toHaveAttribute('aria-busy', 'false')
  await expect(page.locator('.asset-category-panel')).toHaveCount(0)
  expect(savedRequests).toEqual([])
  const container = await page.locator('.asset-browser-layout').boundingBox()
  const results = await page.locator('.asset-results').boundingBox()
  expect(results!.width).toBeCloseTo(container!.width, 0)

  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/workspace/assets?view=saved')
  await expect(page.locator('.asset-category-panel')).toBeHidden()
  await expect(page.getByRole('tab', { name: 'Saved Works', exact: true })).toHaveAttribute('aria-selected', 'true')
  await page.getByRole('tab', { name: 'Owned Assets', exact: true }).click()
  await expect(page.getByRole('tab', { name: 'Owned Assets', exact: true })).toHaveAttribute('aria-selected', 'true')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
})
