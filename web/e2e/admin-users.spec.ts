import { expect, test } from '@playwright/test'

test('restores Admin user filters and updates the precise account', async ({ page }) => {
  const runID = Date.now().toString(36)
  const handle = `dir_${runID}`

  const registration = await page.request.post('/api/v1/auth/register', { data: {
    email: `directory-${runID}@example.test`,
    password: `directory-test-${runID}`,
    handle,
    displayName: 'Directory Test Account',
    locale: 'en-US',
    timezone: 'UTC',
  } })
  expect(registration.ok()).toBeTruthy()

  const adminSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  expect(adminSession.ok()).toBeTruthy()
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto(`/admin?tab=users&q=${runID}&role=member&status=active`)
  await expect(page.getByRole('heading', { name: 'Operations', exact: true })).toBeVisible()
  await expect(page.getByRole('searchbox', { name: 'Search users', exact: true })).toHaveValue(runID)
  await expect(page).toHaveURL(new RegExp(`tab=users.*q=${runID}.*role=member.*status=active`))

  const rows = page.locator('.admin-user-directory .admin-list article')
  await expect(rows).toHaveCount(1)
  const target = rows.filter({ hasText: `@${handle}` })
  await expect(target).toBeVisible()
  const widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)

  await target.getByRole('button', { name: 'Manage access', exact: true }).click()
  const commandPanel = page.locator('.admin-command-panel')
  await expect(commandPanel).toBeVisible()
  await commandPanel.getByRole('combobox', { name: 'Role', exact: true }).selectOption('creator')
  await commandPanel.getByRole('combobox', { name: 'Status', exact: true }).selectOption('suspended')
  await commandPanel.getByRole('textbox', { name: 'Required reason', exact: true }).fill(`E2E ${runID}: verified account access policy change.`)
  await commandPanel.getByRole('checkbox', { name: 'I reviewed the target and confirm this operation.', exact: true }).check()
  await commandPanel.getByRole('button', { name: 'Apply and record', exact: true }).click()
  await expect(page.getByText('Operation completed and audit evidence recorded.', { exact: true })).toBeVisible()
  await expect(target).toHaveCount(0)
})
