import { expect, test } from '@playwright/test'
import { registerFromEmail } from './helpers/auth'

test.beforeEach(async ({ page }) => { await page.request.post('/api/v1/auth/logout') })

test('gives anonymous visitors a clear next step for private workflows', async ({ page }) => {
  await page.goto('/workspace/assets')
  const gate = page.getByRole('region', { name: 'Sign in to open your workspace' })
  await expect(gate).toBeVisible()
  await gate.getByRole('link', { name: 'Create account', exact: true }).click()
  await expect(page).toHaveURL(/\/auth\?/)
  expect(new URL(page.url()).searchParams.get('returnTo')).toBe('/workspace/assets')
  await page.goto('/publish?assetId=acceptance-asset')
  await page.getByRole('region', { name: 'Sign in to publish your work' }).getByRole('link', { name: 'Sign in', exact: true }).click()
  await expect(page).toHaveURL(/\/auth\?/)
  expect(new URL(page.url()).searchParams.get('returnTo')).toBe('/workspace/assets?publish=acceptance-asset')
})

test('preserves a visitor creation draft across sign-in navigation', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/create/image')
  const prompt = 'Silver product study for an international launch'
  await page.locator('.creation-composer textarea').fill(prompt)
  await expect(page.locator('.creation-submit')).toBeDisabled()
  await page.getByRole('region', { name: 'Sign in before generating' }).getByRole('link', { name: 'Sign in', exact: true }).click()
  await expect(page).toHaveURL(/\/auth\?/)
  expect(new URL(page.url()).searchParams.get('returnTo')).toBe('/create/image')
  await page.getByRole('button', { name: 'Close', exact: true }).click()
  await expect(page.locator('.creation-composer textarea')).toHaveValue(prompt)
  const widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)
})

test('verifies a new creator email, restores the draft and publishes a first work', async ({ page }) => {
  const runID = Date.now().toString(36)
  const prompt = `Silver studio product study ${runID}`
  const workTitle = `First launch study ${runID}`
  await page.goto('/create/image')
  await page.locator('.creation-composer textarea').fill(prompt)
  await page.getByRole('region', { name: 'Sign in before generating' }).getByRole('link', { name: 'Create account', exact: true }).click()
  await registerFromEmail(page, `first_${runID}`)
  await expect(page).toHaveURL(/\/create\/image$/)
  await expect(page.locator('.creation-composer textarea')).toHaveValue(prompt)
  await page.locator('.creation-submit').click()
  const result = page.locator('.creation-turn').filter({ hasText: prompt })
  await expect(result).toHaveAttribute('data-status', 'succeeded')
  await result.locator('.creation-result').click()
  await page.getByRole('dialog', { name: 'Generation details' }).getByRole('link', { name: 'Publish work', exact: true }).click()
  await expect(page).toHaveURL(/\/workspace\/assets\?.*publish=/)
  await page.getByLabel('Work title', { exact: true }).fill(workTitle)
  await page.getByLabel('Short description', { exact: true }).fill('A first completed work created from a personal account.')
  await page.getByLabel('Community note', { exact: true }).fill('Created, saved, and published with a verified personal account.')
  await page.getByRole('button', { name: 'Publish work', exact: true }).click()
  await expect(page).toHaveURL(/\/works\/[0-9a-f-]+$/)
  await expect(page.getByRole('heading', { name: workTitle, exact: true, level: 1 })).toBeVisible()
})
