import { fixtureCredentials } from './helpers/identity'
import { expect, test } from '@playwright/test'

test('shows the product home to guests and keeps the mobile layout contained', async ({ page }) => {
  await page.request.post('/api/v1/auth/logout')
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/')

  await expect(page.getByRole('heading', { level: 1, name: /Creative projects.*Built through collaboration/i })).toBeVisible()
  await expect(page.locator('.home-publish')).toBeVisible()
  await expect(page.locator('.home-pathway-resources')).toHaveAttribute('href', '/market')
  await expect(page.locator('.site-sidebar')).toHaveCount(0)
  await expect(page.locator('.home-brand .brand-logo').first()).toHaveAttribute('src', '/brand/logo.png')
  await expect(page.locator('link[rel="icon"]')).toHaveAttribute('href', '/brand/logo.png')

  const layout = await page.evaluate(() => ({
    clientWidth: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
    heroBottom: Math.round(document.querySelector('.home-publish')?.getBoundingClientRect().bottom || 0),
  }))
  expect(layout.scrollWidth).toBe(layout.clientWidth)
  expect(layout.heroBottom).toBeLessThan(page.viewportSize()!.height)
})

test('carries publishing intent to the login page', async ({ page }) => {
  await page.request.post('/api/v1/auth/logout')
  await page.goto('/')
  await page.locator('.home-publish').click()
  await expect(page).toHaveURL(/\/auth\?/)
  expect(new URL(page.url()).searchParams.get('returnTo')).toBe('/market/demands?publish=1')
})

test('keeps the desktop hero inside a centered marketing container', async ({ page }) => {
  await page.request.post('/api/v1/auth/logout')
  await page.setViewportSize({ width: 1305, height: 907 })
  await page.goto('/')

  const layout = await page.evaluate(() => {
    const hero = document.querySelector('.home-hero')?.getBoundingClientRect()
    const copy = document.querySelector('.home-hero-copy')?.getBoundingClientRect()
    const modes = document.querySelector('.home-hero-links')?.getBoundingClientRect()
    return {
      heroWidth: Math.round(hero?.width || 0),
      leftMargin: Math.round(hero?.left || 0),
      rightMargin: Math.round(innerWidth - (hero?.right || innerWidth)),
      modesRight: Math.round(modes?.right || 0),
      copyRight: Math.round(copy?.right || 0),
    }
  })

  expect(layout.heroWidth).toBeLessThanOrEqual(1280)
  expect(layout.leftMargin).toBeGreaterThanOrEqual(40)
  expect(Math.abs(layout.leftMargin - layout.rightMargin)).toBeLessThanOrEqual(1)
  expect(layout.modesRight).toBeLessThanOrEqual(layout.copyRight)
})

test('sends an authenticated user from the public home to Discover', async ({ page }) => {
  const response = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  expect(response.ok()).toBeTruthy()

  await page.goto('/')
  await expect(page).toHaveURL(/\/discover$/)
  await expect(page.getByRole('heading', { level: 1, name: /Inspiration library/i })).toBeVisible()
})
