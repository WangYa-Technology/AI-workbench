import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'

const jobID = '10000000-0000-4000-8000-000000009911'
const retryID = '20000000-0000-4000-8000-000000009912'
const ownerID = '30000000-0000-4000-8000-000000009913'
const path = '/api/v1/admin/data-rights/export-jobs'
const failedJob = (id = jobID) => ({
  id, userId: ownerID, requestId: '40000000-0000-4000-8000-000000009914', kind: 'export', status: 'failed', attempts: 5, maxAttempts: 5,
  errorCode: 'data_export_too_large', canRetry: true, unavailableReason: '',
  createdAt: '2026-09-19T10:00:00Z', updatedAt: '2026-09-19T10:01:00Z',
})

for (const width of [390, 1308]) {
  test(`export recovery preserves failure history and queues once at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 })
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
    let queued = false
    let calls = 0
    await page.route(/\/api\/v1\/admin\/data-rights\/export-jobs(?:\?|$)/, route => route.fulfill({ json: {
      items: [{ ...failedJob(), canRetry: !queued, unavailableReason: queued ? 'already_retried' : '', ...(queued ? { retryJobId: retryID } : {}) }],
    } }))
    await page.route(`**${path}/${jobID}/retry`, async route => {
      calls++
      expect(route.request().postDataJSON()).toEqual({ expectedAttempts: 5, reason: 'Storage access was repaired and checked.', confirmed: true })
      queued = true
      await route.fulfill({ status: 201, json: { ...failedJob(retryID), status: 'queued', attempts: 0, canRetry: false, unavailableReason: 'not_failed', retryOf: jobID } })
    })
    await page.goto('/admin?tab=dataRights')
    const queue = page.getByRole('region', { name: 'Export recovery', exact: true })
    await expect(queue.getByText('5 / 5 attempts', { exact: false })).toBeVisible()
    await queue.getByRole('button', { name: 'Retry export job', exact: true }).click()
    const drawer = page.getByRole('dialog', { name: 'Retry export job', exact: true })
    const submit = drawer.getByRole('button', { name: 'Retry export job', exact: true })
    await expect(drawer.getByText(`Job · ${jobID}`, { exact: true })).toBeVisible()
    await expect(drawer.getByText(`Export request · 40000000-0000-4000-8000-000000009914`, { exact: true })).toBeVisible()
    await expect(drawer.getByText('The complete export exceeds the server’s current capacity.', { exact: false })).toBeVisible()
    await expect(submit).toBeDisabled()
    await drawer.getByLabel('Decision reason', { exact: true }).fill('Storage access was repaired and checked.')
    await expect(submit).toBeDisabled()
    await drawer.getByRole('checkbox').check()
    await expect(submit).toBeEnabled()
    await expect.poll(() => drawer.evaluate(el => getComputedStyle(el).filter)).toBe('none')
    await page.screenshot({ path: testInfo.outputPath('export-recovery-drawer.png'), animations: 'disabled' })
    await submit.click()
    await expect(drawer).not.toBeVisible()
    await expect(queue.getByRole('status')).toHaveText('Recovery queued. Refresh to check the final result.')
    await expect(queue.getByText(`Replacement job · ${retryID}`)).toBeVisible()
    await expect(queue.getByRole('button', { name: 'Retry export job', exact: true })).toHaveCount(0)
    await expect(queue.getByText('5 / 5 attempts', { exact: false })).toBeVisible()
    expect(calls).toBe(1)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await queue.screenshot({ path: testInfo.outputPath('export-recovery-queue.png'), animations: 'disabled' })
  })
}


for (const code of ['export_job_failed', 'data_export_too_large', 'data_export_record_too_large', 'data_export_storage_unavailable', 'data_export_busy']) {
test(`failed export ${code} is visible to its owner and can be cancelled`, async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  let cancelled = false
  const id = '50000000-0000-4000-8000-000000009915'
  await page.route(/\/api\/v1\/account\/data-rights(?:\?|$)/, route => route.fulfill({ json: { items: [{
    id, requestType: 'data_export', status: cancelled ? 'cancelled' : 'failed', subjectRef: 'subject_aaaaaaaaaaaaaaaaaaaaaaaa',
    executeAfter: '2026-09-19T10:00:00Z', version: 1,
    ...(cancelled ? {} : { failureCode: code }),
    createdAt: '2026-09-19T10:00:00Z', updatedAt: '2026-09-19T10:01:00Z',
  }] } }))
  await page.route(`**/api/v1/account/data-rights/${id}`, async route => {
    expect(route.request().method()).toBe('DELETE')
    cancelled = true
    await route.fulfill({ json: { id, requestType: 'data_export', status: 'cancelled' } })
  })
  await page.goto('/settings?section=privacy')
  const history = page.locator('.data-rights-list')
  await expect(history.getByText('Export preparation could not be completed.', { exact: false })).toBeVisible()
  if (code === 'data_export_too_large') await expect(history.getByText('The complete export exceeds the server’s current capacity.', { exact: false })).toBeVisible()
  if (code === 'data_export_record_too_large') await expect(history.getByText('An export record exceeds the current processing limit.', { exact: false })).toBeVisible()
  if (code === 'data_export_storage_unavailable') await expect(history.getByText('Export storage is temporarily unavailable.', { exact: false })).toBeVisible()
  if (code === 'data_export_busy') await expect(history.getByText('Data exports are busy.', { exact: false })).toBeVisible()
  await expect(history.getByRole('link', { name: 'Download JSON' })).toHaveCount(0)
  await history.getByRole('button', { name: 'Cancel request' }).click()
  await expect(history.getByText('Cancelled', { exact: false })).toBeVisible()
})

}
