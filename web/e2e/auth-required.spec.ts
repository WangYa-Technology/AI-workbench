import { expect, test } from '@playwright/test'

test.beforeEach(async ({ page }) => {
  await page.request.post('/api/v1/auth/logout')
})

test('gives anonymous visitors a clear next step for private workflows', async ({ page }) => {
  await page.goto('/workspace/generations')

  const workspaceGate = page.getByRole('region', { name: 'Sign in to open your workspace' })
  await expect(workspaceGate).toBeVisible()
  await expect(page.getByText('The local session could not be established.', { exact: true })).toHaveCount(0)
  await expect(workspaceGate.getByRole('link', { name: 'Sign in', exact: true })).toHaveAttribute('href', '/settings?auth=login&returnTo=/workspace/generations')
  await expect(workspaceGate.getByRole('link', { name: 'Create account', exact: true })).toHaveAttribute('href', '/settings?auth=register&returnTo=/workspace/generations')

  await page.goto('/publish?assetId=acceptance-asset')
  const publishGate = page.getByRole('region', { name: 'Sign in to publish your work' })
  await expect(publishGate).toBeVisible()
  await expect(page.getByText('The local session could not be established.', { exact: true })).toHaveCount(0)
  await publishGate.getByRole('link', { name: 'Create account', exact: true }).click()
  await expect(page).toHaveURL(/\/settings\?auth=register/)
  expect(new URL(page.url()).searchParams.get('returnTo')).toBe('/publish?assetId=acceptance-asset')
  await expect(page.locator('.auth-tabs').getByRole('button', { name: 'Create account', exact: true })).toHaveClass(/active/)
})

test('preserves the builder setup while a visitor goes to sign in', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/create/image')

  const prompt = 'Silver product study for an international launch'
  await page.locator('.studio-composer textarea').fill(prompt)

  const createGate = page.getByRole('region', { name: 'Sign in before generating' })
  await expect(createGate).toBeVisible()
  await expect(page.getByRole('button', { name: 'Generate image', exact: true })).toBeDisabled()
  await createGate.getByRole('link', { name: 'Sign in', exact: true }).click()
  await expect(page).toHaveURL('/settings?auth=login&returnTo=/create/image')

  await page.setViewportSize({ width: 1280, height: 800 })
  await page.getByRole('link', { name: 'AI creation', exact: true }).click()
  await expect(page).toHaveURL('/create/image')
  await expect(page.locator('.studio-composer textarea')).toHaveValue(prompt)

  await page.setViewportSize({ width: 390, height: 844 })
  const widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)
  const overlap = await page.evaluate(() => {
    const gate = document.querySelector('.auth-required-state')?.getBoundingClientRect()
    const actions = document.querySelector('.studio-composer')?.getBoundingClientRect()
    if (!gate || !actions) return -1
    const horizontal = Math.max(0, Math.min(gate.right, actions.right) - Math.max(gate.left, actions.left))
    const vertical = Math.max(0, Math.min(gate.bottom, actions.bottom) - Math.max(gate.top, actions.top))
    return horizontal * vertical
  })
  expect(overlap).toBe(0)
})

test('lets a new creator generate and publish a first work', async ({ page }) => {
  const runID = Date.now().toString(36)
  const prompt = `Silver studio product study for an international launch ${runID}`
  const workTitle = `First launch study ${runID}`

  await page.goto('/create/image')
  await page.locator('.studio-composer textarea').fill(prompt)
  await page.getByRole('region', { name: 'Sign in before generating' }).getByRole('link', { name: 'Create account', exact: true }).click()

  await page.getByLabel('Display name', { exact: true }).fill('First Time Creator')
  await page.getByLabel('Handle', { exact: true }).fill(`first_creator_${runID}`)
  await page.getByLabel('Email', { exact: true }).fill(`first-creator-${runID}@example.com`)
  await page.locator('.account-form').getByLabel('Password', { exact: false }).fill('first-creator-password-2026')
  await page.locator('.account-form').getByRole('button', { name: 'Create account', exact: true }).click()

  await expect(page).toHaveURL('/create/image')
  await expect(page.locator('.studio-composer textarea')).toHaveValue(prompt)
  await page.getByRole('button', { name: 'Generate image', exact: true }).click()

  await expect(page.locator('.studio-task').first().locator('.studio-task-status')).toContainText('Saved to Assets')
  await page.locator('.studio-task').first().locator('.studio-task-copy').click()
  await page.getByRole('dialog', { name: 'Generation details' }).getByRole('link', { name: 'Publish work', exact: true }).click()
  await expect(page).toHaveURL(/\/publish\?assetId=/)

  await page.getByLabel('Work title', { exact: true }).fill(workTitle)
  await page.getByLabel('Short description', { exact: true }).fill('A first completed work created from a personal account.')
  await page.getByLabel('Community note', { exact: true }).fill('Created, saved, and published without switching to a demo account.')
  await page.getByRole('button', { name: 'Publish work', exact: true }).click()

  await expect(page).toHaveURL(/\/works\/[0-9a-f-]+$/)
  await expect(page.getByRole('heading', { name: workTitle, exact: true })).toBeVisible()
  await page.getByLabel('Primary navigation').getByRole('link', { name: 'Community', exact: true }).click()
  await expect(page.getByRole('heading', { name: workTitle, exact: true })).toBeVisible()
})
