import { fixtureCredentials } from './helpers/identity'
import { expect, test } from '@playwright/test'

for (const width of [390, 1308]) {
  test(`card grid rules target content without collapsing catalog width at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    for (const route of ['/market', '/discover', '/community', '/market/demands']) {
      await page.goto(route)
      await expect(page.locator('.ui-card-content').first()).toBeVisible()
      await page.locator('.ui-layout-switcher button').last().click()
      await expect(page.locator('.ui-card-content').first()).toHaveCSS('justify-content', 'flex-start')
      await expect(page.locator('.ui-catalog').first()).not.toHaveCSS('justify-content', 'flex-start')
      const dimensions = await page.locator('.ui-card-content__meta').first().evaluate(el => {
        const content = el.closest('.ui-card-content')!
        return { bottom: el.getBoundingClientRect().bottom, contentBottom: content.getBoundingClientRect().bottom, padding: parseFloat(getComputedStyle(content).paddingBottom) }
      })
      expect(Math.abs(dimensions.bottom - dimensions.contentBottom + dimensions.padding)).toBeLessThan(1)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    }
  })
}

test('notification tabs support arrow keys and Home without adding tab stops', async ({ page }) => {
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  await page.goto('/notifications')
  const tabs = page.getByRole('tablist').getByRole('tab')
  await tabs.first().focus()
  await page.keyboard.press('ArrowRight')
  await expect(tabs.last()).toBeFocused()
  await expect(tabs.last()).toHaveAttribute('aria-selected', 'true')
  await expect(tabs.first()).toHaveAttribute('tabindex', '-1')
  await expect(page).toHaveURL(/view=preferences/)
  await page.keyboard.press('Home')
  await expect(tabs.first()).toBeFocused()
  await expect(tabs.first()).toHaveAttribute('aria-selected', 'true')
})

test('workspace assets use shared content and scan state badges', async ({ page }) => {
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  await page.goto('/workspace/assets')
  const card = page.locator('.asset-row').first()
  await expect(card.locator('.ui-card-content__title')).toBeVisible()
  await expect(card.locator('.ui-card-tag[data-variant="success"]')).toBeVisible()
  await expect(card.locator('.task-row-copy')).toHaveCount(0)
})
