import { fixtureCredentials } from './helpers/identity'
import { expect, test } from '@playwright/test'

test('catalog banners stay in the results column and wrap actions across themes and sizes', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('publisher') })).ok()).toBeTruthy()
  for (const path of ['/market/demands', '/community', '/discover', '/market']) {
    await page.goto(path)
    const banner = page.locator('.ui-action-banner')
    await expect(banner).toBeVisible()
    for (const theme of ['light', 'dark']) {
      await page.evaluate(theme => document.documentElement.dataset.theme = theme, theme)
      for (const width of [1551, 1308, 768, 390, 320]) {
        await page.setViewportSize({ width, height: 901 })
        const layout = await banner.evaluate(element => {
          const bounds = element.getBoundingClientRect()
          const results = element.closest('.category-content, .task-results')!.getBoundingClientRect()
          return {
            left: Math.abs(bounds.left - results.left),
            right: Math.abs(bounds.right - results.right),
            actionsFit: [...element.querySelectorAll('.ui-button')].every(action => {
              const rect = action.getBoundingClientRect()
              return rect.left >= bounds.left && rect.right <= bounds.right && rect.bottom <= bounds.bottom
            }),
          }
        })
        expect(layout.left).toBeLessThanOrEqual(1)
        expect(layout.right).toBeLessThanOrEqual(1)
        expect(layout.actionsFit).toBeTruthy()
      }
    }
  }
})

test('banner publishing actions reuse the task dialog and community drawer', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('publisher') })).ok()).toBeTruthy()
  await page.goto('/market/demands')
  await page.locator('.ui-action-banner').getByRole('button', { name: 'Publish brief' }).click()
  await expect(page.locator('.task-create-modal')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.locator('.task-create-modal')).toHaveCount(0)
  await page.goto('/community')
  await page.locator('.ui-action-banner').getByRole('button', { name: 'Publish post' }).click()
  await expect(page.getByRole('dialog', { name: 'Publish post' })).toBeVisible()
})

test('guest banners preserve authentication return paths and exploration destinations', async ({ page }) => {
  await page.goto('/community?category=community_tutorial')
  await page.locator('.ui-action-banner').getByRole('link', { name: 'Sign in to join' }).click()
  await expect(page).toHaveURL(/\/auth\?/)
  expect(new URL(page.url()).searchParams.get('returnTo')).toBe('/community?category=community_tutorial')
  await page.goto('/market/demands')
  await expect(page.locator('.task-result-list')).toBeVisible()
  await expect(page.locator('.ui-action-banner')).toHaveCount(0)
  await page.goto('/discover')
  const links = page.locator('.ui-action-banner a')
  await expect(links).toHaveCount(3)
  expect(await links.evaluateAll(elements => elements.map(element => element.getAttribute('href')))).toEqual(['/market/demands', '/community', '/market'])
  await links.last().click()
  await expect(page).toHaveURL(/\/market$/)
  await expect(page.locator('.ui-action-banner a')).toHaveAttribute('href', '/create/image')
})
