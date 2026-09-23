import { fixtureCredentials } from './helpers/identity'
import { expect, test } from '@playwright/test'

test('searches public domains, explains ranking, and follows a creator', async ({ page }) => {
  const session = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  expect(session.ok()).toBeTruthy()

  await page.goto('/search?q=signal')
  await expect(page.getByRole('heading', { name: 'Search the creative network', exact: true })).toBeVisible()
  await expect(page.getByText(/\d+ public results/, { exact: true })).toBeVisible()
  await expect(page.getByText('Title match', { exact: true }).first()).toBeVisible()
  await expect(page.getByRole('link', { name: /Signal Architecture campaign license/ })).toContainText('$38.00')

  await page.getByRole('button', { name: 'Work', exact: true }).click()
  await expect(page).toHaveURL(/types=work/)
  await expect(page.getByText(/\d+ public results/, { exact: true })).toBeVisible()
  await expect(page.getByText('Product · @fixture_studio', { exact: true })).toBeHidden()

  await page.locator('.search-page-header input').fill('fixture_studio')
  await page.locator('.search-page-header form').getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page).toHaveURL(/q=fixture_studio/)
  await page.getByRole('button', { name: 'All results', exact: true }).click()
  const creatorResult = page.getByRole('link', { name: /Fixture Studio Creator/ })
  await expect(creatorResult).toBeVisible()
  await creatorResult.click()

  await expect(page).toHaveURL('/creators/fixture_studio')
  await expect(page.getByRole('heading', { name: 'Fixture Studio', exact: true })).toBeVisible()
  await expect(page.locator('.creator-work-grid > a')).toHaveCount(1)
  await expect(page.locator('.creator-product-list > a')).toHaveCount(3)
  await expect(page.getByText('Test fixture media for local development.', { exact: false }).first()).toBeVisible()

  const follow = page.getByRole('button', { name: 'Follow', exact: true })
  const following = page.getByRole('button', { name: 'Following', exact: true })
  if (await follow.isVisible()) {
    await follow.click()
    await expect(following).toBeVisible()
    await following.click()
    await expect(follow).toBeVisible()
  } else {
    await following.click()
    await expect(follow).toBeVisible()
    await follow.click()
    await expect(following).toBeVisible()
  }
})

test('keeps search and creator pages within every required viewport', async ({ page }) => {
  const viewports = [
    { width: 320, height: 720 },
    { width: 390, height: 844 },
    { width: 768, height: 1024 },
    { width: 1280, height: 800 },
    { width: 1600, height: 1000 },
  ]
  for (const viewport of viewports) {
    await page.setViewportSize(viewport)
    await page.goto('/search?q=signal')
    await expect(page.locator('.search-result').first()).toBeVisible()
    let widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
    expect(widths.scroll, `search overflow at ${viewport.width}x${viewport.height}`).toBe(widths.client)

    await page.goto('/creators/fixture_studio')
    await expect(page.getByRole('heading', { name: 'Fixture Studio', exact: true })).toBeVisible()
    await expect(page.locator('.creator-header')).toBeVisible()
    widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
    expect(widths.scroll, `creator overflow at ${viewport.width}x${viewport.height}`).toBe(widths.client)
  }
})
