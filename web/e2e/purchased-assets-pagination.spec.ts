import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'

test('purchased assets request filtered pages and ignore a continuation after leaving the view', async ({ page }) => {
  const login = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  expect(login.ok()).toBeTruthy()
  const original = await (await page.request.get('/api/v1/assets')).json()
  expect(original.items.length).toBeGreaterThan(0)
  const asset = original.items[0]
  const first = { ...asset, id: '00000000-0000-4000-8000-000000009901', title: 'First purchased asset', sourceType: 'purchase' }
  const second = { ...asset, id: '00000000-0000-4000-8000-000000009902', title: 'Second purchased asset', sourceType: 'purchase' }
  let delayContinuation = false
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  await page.route(/\/api\/v1\/assets(?:\?.*)?$/, async route => {
    const params = new URL(route.request().url()).searchParams
    if (params.get('source') !== 'purchase') {
      await route.fulfill({ json: { items: [asset], total: 1 } })
      return
    }
    if (params.get('cursor')) {
      expect(params.get('cursor')).toBe('purchases-next-page')
      if (delayContinuation) await gate
      await route.fulfill({ json: { items: [second], total: 2 } })
    } else {
      await route.fulfill({ json: { items: [first], total: 2, nextCursor: 'purchases-next-page' } })
    }
  })
  await page.goto('/workspace/purchases')
  await expect(page.getByRole('heading', { name: first.title, exact: true })).toBeVisible()
  const stats = page.locator('.page-hero-stats article')
  await expect(stats.nth(0).locator('strong')).toHaveText('2')
  await expect(stats.nth(1)).toContainText('Media types in loaded items')
  await expect(stats.nth(2)).toContainText('Safe loaded items')
  await expect(page.getByRole('heading', { name: asset.title, exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect(page.getByRole('heading', { name: second.title, exact: true })).toBeVisible()
  await expect(page.locator('.asset-row')).toHaveCount(2)
  await expect(stats.nth(0).locator('strong')).toHaveText('2')
  await expect(page.getByRole('button', { name: 'Load more', exact: true })).toHaveCount(0)

  await page.reload()
  await expect(page.getByRole('heading', { name: first.title, exact: true })).toBeVisible()
  delayContinuation = true
  const nextRequest = page.waitForRequest(request => request.url().includes('cursor=purchases-next-page'))
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await nextRequest
  await page.locator('a[href="/workspace/assets"]').first().click()
  await expect(page.getByRole('heading', { name: asset.title, exact: true })).toBeVisible()
  const nextResponse = page.waitForResponse(response => response.url().includes('cursor=purchases-next-page'))
  release()
  await nextResponse
  await expect(page.getByRole('heading', { name: second.title, exact: true })).toHaveCount(0)
  await expect(page.locator('.asset-row')).toHaveCount(1)
})
