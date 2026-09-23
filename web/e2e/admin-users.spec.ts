import { fixtureCredentials } from './helpers/identity'
import { expect, test, type Locator, type Page } from '@playwright/test'

async function chooseOption(page: Page, trigger: Locator, optionName: string) {
  await trigger.click()
  await page.getByRole('option', { name: optionName, exact: true }).click()
}

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

  const adminSession = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })
  expect(adminSession.ok()).toBeTruthy()
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto(`/admin?tab=users&q=${runID}&role=member&status=active`)
  await expect(page.getByRole('heading', { name: 'Operations', exact: true })).toBeVisible()
  await expect(page.getByRole('searchbox', { name: 'Search users', exact: true })).toHaveValue(runID)
  await expect(page).toHaveURL(new RegExp(`tab=users.*q=${runID}.*role=member.*status=active`))

  const rows = page.locator('.admin-user-table tbody tr')
  await expect(rows).toHaveCount(1)
  const target = rows.filter({ hasText: `@${handle}` })
  await expect(target).toBeVisible()
  const widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)

  await target.getByRole('button', { name: 'Manage access', exact: true }).click()
  const commandPanel = page.locator('.admin-command-panel')
  const drawer = page.locator('.ui-drawer')
  await expect(drawer).toBeVisible()
  await expect(drawer.locator('.admin-command-panel')).toBeVisible()
  await expect.poll(async () => {
    const bounds = await drawer.boundingBox()
    return Math.round((bounds?.x || 0) + (bounds?.width || 0))
  }).toBe(390)
  await expect(commandPanel).toBeVisible()
  await chooseOption(page, commandPanel.getByRole('combobox', { name: 'Role', exact: true }), 'Creator')
  await chooseOption(page, commandPanel.getByRole('combobox', { name: 'Status', exact: true }), 'Suspended')
  await commandPanel.getByRole('button', { name: 'Apply', exact: true }).click()
  await expect(page.getByText('Operation completed.', { exact: true })).toBeVisible()
  await expect(target).toHaveCount(0)
})
