import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'

const jobID = '10000000-0000-4000-8000-000000009911'
const retryID = '20000000-0000-4000-8000-000000009912'
const ownerID = '30000000-0000-4000-8000-000000009913'
const path = '/api/v1/admin/data-rights/deletion-jobs'
const failedJob = (id = jobID) => ({
  id, userId: ownerID, requestId: '40000000-0000-4000-8000-000000009914', kind: 'cleanup', status: 'failed', attempts: 5, maxAttempts: 5,
  errorCode: 'handler_failed', canRetry: true, unavailableReason: '',
  createdAt: '2026-09-19T10:00:00Z', updatedAt: '2026-09-19T10:01:00Z',
})

for (const width of [390, 1308]) {
  test(`deletion recovery preserves failure history and queues once at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 })
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
    let queued = false
    let calls = 0
    await page.route(/\/api\/v1\/admin\/data-rights\/deletion-jobs(?:\?|$)/, route => route.fulfill({ json: {
      items: [{ ...failedJob(), canRetry: !queued, unavailableReason: queued ? 'already_retried' : '', ...(queued ? { retryJobId: retryID } : {}) }],
    } }))
    await page.route(`**${path}/${jobID}/retry`, async route => {
      calls++
      expect(route.request().postDataJSON()).toEqual({ expectedAttempts: 5, reason: 'Storage access was repaired and checked.', confirmed: true })
      queued = true
      await route.fulfill({ status: 201, json: { ...failedJob(retryID), status: 'queued', attempts: 0, canRetry: false, unavailableReason: 'not_failed', retryOf: jobID } })
    })
    await page.goto('/admin?tab=dataRights')
    const queue = page.getByRole('region', { name: 'Account deletion recovery', exact: true })
    await expect(queue.getByText('5 / 5 attempts', { exact: false })).toBeVisible()
    await queue.getByRole('button', { name: 'Retry deletion job', exact: true }).click()
    const drawer = page.getByRole('dialog', { name: 'Retry deletion job', exact: true })
    const submit = drawer.getByRole('button', { name: 'Retry deletion job', exact: true })
    await expect(drawer.getByText('Execution stage · Continue irreversible cleanup', { exact: true })).toBeVisible()
    await expect(drawer.getByText(`Job · ${jobID}`, { exact: true })).toBeVisible()
    await expect(drawer.getByText(`Deletion request · 40000000-0000-4000-8000-000000009914`, { exact: true })).toBeVisible()
    await expect(drawer.getByText('Deletion already in progress cannot be cancelled', { exact: false })).toBeVisible()
    await expect(submit).toBeDisabled()
    await drawer.getByLabel('Decision reason', { exact: true }).fill('Storage access was repaired and checked.')
    await expect(submit).toBeDisabled()
    await drawer.getByRole('checkbox').check()
    await expect(submit).toBeEnabled()
    await expect.poll(() => drawer.evaluate(el => getComputedStyle(el).filter)).toBe('none')
    await page.screenshot({ path: testInfo.outputPath('deletion-recovery-drawer.png'), animations: 'disabled' })
    await submit.click()
    await expect(drawer).not.toBeVisible()
    await expect(queue.getByRole('status')).toHaveText('Recovery queued. Refresh to check the final result.')
    await expect(queue.getByText(`Replacement job · ${retryID}`)).toBeVisible()
    await expect(queue.getByRole('button', { name: 'Retry deletion job', exact: true })).toHaveCount(0)
    await expect(queue.getByText('5 / 5 attempts', { exact: false })).toBeVisible()
    expect(calls).toBe(1)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await queue.screenshot({ path: testInfo.outputPath('deletion-recovery-queue.png'), animations: 'disabled' })
  })
}

test('deletion cancellation remains limited to the original grace period', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  await page.route(/\/api\/v1\/account\/data-rights(?:\?|$)/, route => route.fulfill({ json: { items: [
    { id: '50000000-0000-4000-8000-000000000001', status: 'scheduled', cancelUntil: new Date(Date.now() - 60_000).toISOString() },
    { id: '50000000-0000-4000-8000-000000000002', status: 'processing', cancelUntil: new Date(Date.now() + 60_000).toISOString() },
    { id: '50000000-0000-4000-8000-000000000003', status: 'blocked', cancelUntil: new Date(Date.now() + 60_000).toISOString() },
  ].map(item => ({ ...item, requestType: 'account_deletion', subjectRef: 'subject_aaaaaaaaaaaaaaaaaaaaaaaa',
    executeAfter: item.cancelUntil, version: 1, createdAt: '2026-09-19T10:00:00Z', updatedAt: '2026-09-19T10:01:00Z',
  })) } }))
  await page.goto('/settings?section=privacy')
  const rows = page.locator('.data-rights-list article')
  await expect(rows).toHaveCount(3)
  await expect(rows.nth(0).getByRole('button', { name: 'Cancel request' })).toHaveCount(0)
  await expect(rows.nth(1).getByRole('button', { name: 'Cancel request' })).toHaveCount(0)
  await expect(rows.nth(2).getByRole('button', { name: 'Cancel request' })).toBeVisible()
})
