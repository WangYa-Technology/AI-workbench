import { expect, test } from '@playwright/test'

test('shows the product home to guests and keeps the mobile layout contained', async ({ page }) => {
  await page.request.post('/api/v1/auth/logout')
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/')

  await expect(page.getByRole('heading', { level: 1, name: /Create, share, and earn with AI/i })).toBeVisible()
  await expect(page.getByRole('textbox', { name: 'Describe what you want to create' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Continue to the creation workspace' })).toBeVisible()
  await expect(page.locator('.site-sidebar')).toHaveCount(0)
  await expect(page.locator('.home-brand .brand-logo').first()).toHaveAttribute('src', '/brand/logo.png')
  await expect(page.locator('link[rel="icon"]')).toHaveAttribute('href', '/brand/logo.png')

  const layout = await page.evaluate(() => ({
    clientWidth: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
    heroBottom: Math.round(document.querySelector('.home-hero')?.getBoundingClientRect().bottom || 0),
  }))
  expect(layout.scrollWidth).toBe(layout.clientWidth)
  expect(layout.heroBottom).toBeLessThan(page.viewportSize()!.height)
})

test('carries a homepage idea into the selected creation mode', async ({ page }) => {
  await page.request.post('/api/v1/auth/logout')
  await page.goto('/')

  await page.locator('.mode-switcher').getByRole('button', { name: /Video/, exact: false }).click()
  await page.getByRole('textbox', { name: 'Describe what you want to create' }).fill('A quiet product film in soft daylight')
  await page.getByRole('button', { name: 'Continue to the creation workspace' }).click()

  await expect(page).toHaveURL(/\/create\/video\?starter=A(?:\+|%20)quiet/)
  await expect(page.getByPlaceholder('Describe the scene, camera movement, pacing, duration, and format…')).toHaveValue('A quiet product film in soft daylight')
})

test('keeps the desktop hero inside a centered marketing container', async ({ page }) => {
  await page.request.post('/api/v1/auth/logout')
  await page.setViewportSize({ width: 1305, height: 907 })
  await page.goto('/')

  const layout = await page.evaluate(() => {
    const hero = document.querySelector('.hero-main')?.getBoundingClientRect()
    const copy = document.querySelector('.hero-copy')?.getBoundingClientRect()
    const modes = document.querySelector('.mode-switcher')?.getBoundingClientRect()
    return {
      heroWidth: Math.round(hero?.width || 0),
      leftMargin: Math.round(hero?.left || 0),
      rightMargin: Math.round(innerWidth - (hero?.right || innerWidth)),
      modesRight: Math.round(modes?.right || 0),
      copyRight: Math.round(copy?.right || 0),
    }
  })

  expect(layout.heroWidth).toBeLessThanOrEqual(1040)
  expect(layout.leftMargin).toBeGreaterThanOrEqual(120)
  expect(Math.abs(layout.leftMargin - layout.rightMargin)).toBeLessThanOrEqual(1)
  expect(layout.modesRight).toBeLessThanOrEqual(layout.copyRight)
})

test('sends an authenticated user from the public home to Discover', async ({ page }) => {
  const response = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(response.ok()).toBeTruthy()

  await page.goto('/')
  await expect(page).toHaveURL(/\/discover$/)
  await expect(page.getByRole('heading', { level: 1, name: /Create, share, and license AI work/i })).toBeVisible()
})
