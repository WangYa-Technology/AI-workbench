import { fixtureCredentials } from './helpers/identity'
import { expect, test } from '@playwright/test'

test('exposes permission-scoped persistent request and job diagnostics', async ({ page }) => {
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  expect((await page.request.get('/api/v1/admin/observability')).status()).toBe(403)

  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })
  expect((await page.request.get('/api/v1/does-not-exist')).status()).toBe(404)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/admin?tab=diagnostics')
  await expect(page.getByRole('heading', { name: 'Request health', exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Durable job queue', exact: true })).toBeVisible()
  await expect(page.getByText('Job attempts', { exact: true })).toBeVisible()
  await expect(page.getByText('Lease renewals', { exact: true })).toBeVisible()
  await expect(page.getByText('Lease expirations', { exact: true })).toBeVisible()
  await expect(page.getByText('Terminal failures', { exact: true })).toBeVisible()
  await expect(page.getByText('Ready', { exact: true })).toBeVisible()
  await expect(page.getByText(/\d+ 2xx · [1-9]\d* 4xx · \d+ 5xx/)).toBeVisible()
  const widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)

  await expect(page.getByRole('region', { name: 'Diagnostics', exact: true })).toHaveAttribute('tabindex', '0')
})
