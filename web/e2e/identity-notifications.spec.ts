import { expect, test } from '@playwright/test'

test('manages identity, session evidence, notification deep links, and preferences', async ({ page }) => {
  test.setTimeout(90_000)
  const runID = Date.now().toString(36)
  const email = `identity-${runID}@test.local`
  const password = `correct-horse-${runID}`
  const handle = `identity_${runID}`
  const originalName = `Identity Creator ${runID}`
  const updatedName = `Studio Identity ${runID}`

  await page.request.post('/api/v1/auth/logout')
  await page.goto('/settings')
  await expect(page.getByRole('heading', { name: 'Use one account across creation, assets, and transactions.' })).toBeVisible()
  await page.getByRole('button', { name: 'Create account', exact: true }).click()
  const registration = page.locator('.auth-panel .account-form')
  await registration.getByLabel('Display name', { exact: true }).fill(originalName)
  await registration.getByLabel('Handle', { exact: true }).fill(handle)
  await registration.getByLabel('Email', { exact: true }).fill(email)
  await registration.locator('input[type="password"]').fill(password)
  await registration.getByLabel('IANA timezone', { exact: true }).fill('Europe/London')
  await registration.getByRole('button', { name: 'Create account', exact: true }).click()

  await expect(page.getByText('Account created and session established.', { exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: originalName, exact: true })).toBeVisible()
  await page.getByLabel('Display name', { exact: true }).fill(updatedName)
  await page.getByLabel('IANA timezone', { exact: true }).fill('America/New_York')
  await page.getByRole('button', { name: 'Save profile', exact: true }).click()
  await expect(page.getByText('Profile and regional preferences saved.', { exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: updatedName, exact: true })).toBeVisible()

  await page.getByRole('link', { name: 'Security', exact: true }).click()
  await expect(page.getByText('Current session', { exact: true })).toBeVisible()
  await expect(page.getByText(/Last active/).first()).toBeVisible()
  await expect(page.getByText(/Network hint/).first()).toBeVisible()

  await page.getByRole('link', { name: 'Sign-in methods', exact: true }).click()
  await expect(page.getByText('Google', { exact: true })).toBeVisible()
  await expect(page.getByText('GitHub', { exact: true })).toBeVisible()
  await expect(page.getByText('Unavailable', { exact: true })).toHaveCount(2)
  await expect(page.locator('.connection-list article').getByText('External providers stay disabled until credentials and staging verification are complete.', { exact: true })).toHaveCount(2)

  await page.locator('.account-header').getByRole('button', { name: 'Sign out', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Use one account across creation, assets, and transactions.' })).toBeVisible()
  const login = page.locator('.auth-panel .account-form')
  await login.getByLabel('Email', { exact: true }).fill(email)
  await login.getByLabel('Password', { exact: true }).fill(password)
  await login.getByRole('button', { name: 'Sign in', exact: true }).click()
  await expect(page.getByRole('heading', { name: updatedName, exact: true })).toBeVisible()

  await page.goto('/create/image')
  await page.locator('.studio-composer textarea').fill(`A precise notification study with a single focal structure, ${runID}`)
  await page.getByRole('button', { name: 'Generate image', exact: true }).click()
  await expect(page.locator('.studio-task').first().locator('.studio-task-status')).toContainText('Saved to Assets')

  await page.getByRole('link', { name: 'Notifications', exact: true }).click()
  await expect(page).toHaveURL(/\/notifications$/)
  const generationNotice = page.locator('.notification-list article').filter({ hasText: 'Generation ready' }).first()
  await expect(generationNotice).toBeVisible()
  await generationNotice.getByRole('button', { name: 'Open linked workflow', exact: true }).click()
  await expect(page).toHaveURL(/\/workspace\/assets\/[0-9a-f-]+$/)
  await expect(page.getByRole('heading', { name: 'Generated with', exact: true })).toBeVisible()

  await page.goto('/notifications')
  const stillUnread = page.locator('.notification-list article').filter({ hasText: 'Generation ready' }).first()
  await expect(stillUnread.getByRole('button', { name: 'Mark as read', exact: true })).toBeVisible()
  await stillUnread.getByRole('button', { name: 'Mark as read', exact: true }).click()
  await expect(stillUnread.getByRole('button', { name: 'Mark as read', exact: true })).toBeHidden()

  await page.getByRole('link', { name: 'Preferences', exact: true }).click()
  const deliveredEvidence = page.locator('.delivery-evidence article').filter({ hasText: 'Generation completed' }).first()
  await expect(deliveredEvidence).toContainText('Delivered')
  await expect(deliveredEvidence).toContainText('Attempts: 1')
  const generationPreference = page.getByRole('checkbox', { name: /Generation completed/ })
  await expect(generationPreference).toBeChecked()
  const preferenceSaved = page.waitForResponse(response =>
    response.request().method() === 'PUT' && /\/api\/v1\/notification-preferences\/generation\.completed$/.test(response.url()) && response.status() === 200,
  )
  await generationPreference.uncheck()
  await preferenceSaved
  await expect(generationPreference).not.toBeChecked()

  await page.goto('/create/image')
  const secondPrompt = `A second notification suppression study with clean geometry, ${runID}`
  await page.locator('.studio-composer textarea').fill(secondPrompt)
  await page.getByRole('button', { name: 'Generate image', exact: true }).click()
  await expect(page.locator('.studio-task').filter({ hasText: secondPrompt }).first().locator('.studio-task-status')).toContainText('Saved to Assets')
  await page.goto('/notifications?readState=unread&kind=generation.completed')
  await expect(page.getByRole('heading', { name: 'Nothing needs your attention.', exact: true })).toBeVisible()
  await page.getByRole('link', { name: 'Preferences', exact: true }).click()
  const suppressedEvidence = page.locator('.delivery-evidence article').filter({ hasText: 'Generation completed' }).filter({ hasText: 'Suppressed' }).first()
  await expect(suppressedEvidence).toContainText('Suppressed')
  await expect(suppressedEvidence).toContainText('Preference disabled')
  await expect(suppressedEvidence).toContainText('Attempts: 1')
})

test('keeps the disabled creator payout boundary clear and usable on mobile', async ({ page }) => {
  test.setTimeout(60_000)
  await page.setViewportSize({ width: 390, height: 844 })
  const runID = Date.now().toString(36)
  const registration = await page.request.post('/api/v1/auth/register', { data: {
    email: `payout-ui-${runID}@test.local`, password: `correct-horse-${runID}`,
    handle: `payout_ui_${runID}`, displayName: `Payout UI ${runID}`, locale: 'en-US', timezone: 'UTC',
  } })
  expect(registration.ok()).toBeTruthy()

  await page.goto('/settings?section=payouts')
  await expect(page.getByRole('heading', { name: 'Creator payouts', exact: true })).toBeVisible()
  await expect(page.getByText('Not started', { exact: true })).toBeVisible()
  await expect(page.getByText('Unavailable', { exact: true })).toBeVisible()
  await expect(page.getByText('Stripe Connect is disabled in this environment. Local Test transactions remain available and no bank account or real payout is used.', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: /Stripe onboarding/, exact: false })).toHaveCount(0)
  await expect(page.evaluate(() => document.documentElement.scrollWidth)).resolves.toBe(390)
})
