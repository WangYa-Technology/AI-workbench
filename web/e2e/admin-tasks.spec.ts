import { expect, test } from '@playwright/test'
import { assignTaskFixture } from './fixtures/task'

test('resolves a disputed task through permission-scoped operations without settlement', async ({ page }) => {
  test.setTimeout(90_000)
  const runID = Date.now().toString(36)
  const title = `Operations dispute brief ${runID}`

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'publisher' } })
  const createResponse = await page.request.post('/api/v1/tasks', {
    headers: { 'Idempotency-Key': `admin-task-${runID}` },
    data: {
      title,
      summary: 'A bounded Local Test brief used to verify the complete task operations decision path.',
      brief: 'Create an editorial image with stable geometry, one clear subject, and complete source and model evidence.',
      deliverableType: 'image',
      deliverables: ['One 2400px master image'],
      acceptanceRules: ['No third-party marks', 'Source information is complete'],
      rightsTerms: 'Worldwide editorial use for six months.',
      aiDisclosureRequirement: 'List the model and every source asset.',
      budgetCents: 18_000,
      currency: 'USD',
      deadline: new Date(Date.now() + 14 * 24 * 60 * 60 * 1000).toISOString(),
      clientTimezone: 'Europe/London',
      allowDirectAccept: true,
    },
  })
  expect(createResponse.ok()).toBeTruthy()
  const created = (await createResponse.json()) as { id: string }

  const creatorSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  const creator = await creatorSession.json() as { user: { id: string } }
  await page.goto(`/market/demands/${created.id}`)
  await page.getByRole('button', { name: 'Submit proposal', exact: true }).click()
  await page.getByLabel('Approach', { exact: true }).fill('I will establish the composition first, then review source evidence and finish the requested image.')
  await page.getByLabel('Planned delivery', { exact: true }).fill('One master image with complete source and model evidence.')
  await page.getByLabel('Timeline (days)', { exact: true }).fill('3')
  await page.getByRole('button', { name: 'Submit proposal', exact: true }).click()
  await expect(page.getByText('Proposal submitted.', { exact: true })).toBeVisible()
  // This test covers delivery and operations. Funding/claiming is separately
  // exercised by the task service's payment-provider tests.
  assignTaskFixture(created.id, creator.user.id)
  await page.goto(`/market/demands/${created.id}`)
  await page.getByRole('link', { name: 'Start creating', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/create/image\\?taskId=${created.id}`))
  await expect(page.locator('.creation-composer textarea')).toHaveValue(new RegExp(title))
  const generation = page.waitForRequest(request => request.url().endsWith('/generations') && request.method() === 'POST')
  await page.getByRole('button', { name: 'Generate Image', exact: true }).click()
  expect((await generation).postDataJSON().sourceTaskId).toBe(created.id)
  await expect(page.locator('.creation-turn').first()).toHaveAttribute('data-status', 'succeeded')
  await page.goto(`/market/demands/${created.id}`)
  await page.getByLabel('Delivery note', { exact: true }).fill('Submitted with complete deterministic Local Test source and model evidence.')
  await page.getByRole('button', { name: 'Submit delivery', exact: true }).click()
  await expect(page.locator('.task-deliveries')).toContainText('Maya Chen')
  await page.request.post('/api/v1/auth/demo', { data: { actor: 'publisher' } })
  await page.reload()
  await page.getByLabel('Review note', { exact: true }).fill('Please increase the subject contrast and preserve the supplied source evidence.')
  await page.getByRole('button', { name: 'Request revision', exact: true }).click()
  await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  await page.reload()
  await page.getByLabel('Delivery note', { exact: true }).fill('Updated subject contrast and retained the complete source evidence for the second version.')
  await page.getByRole('button', { name: 'Submit delivery', exact: true }).click()
  await expect(page.locator('.task-deliveries')).toContainText('v2')
  await page.getByRole('button', { name: 'Open dispute', exact: true }).click()
  await page.getByLabel('Dispute reason', { exact: true }).fill('The recorded acceptance language conflicts with the requested interpretation and needs operations review.')
  await page.getByRole('button', { name: 'Open dispute', exact: true }).click()
  await expect(page.getByText('Dispute opened. Settlement remains paused.', { exact: true })).toBeVisible()

  const deniedQueue = await page.request.get('/api/v1/admin/tasks')
  expect(deniedQueue.status()).toBe(403)

  await page.setViewportSize({ width: 390, height: 844 })
  await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  await page.goto(`/admin?tab=tasks&taskQ=${runID}&taskStatus=disputed&taskDisputeStatus=open`)
  await expect(page.getByRole('heading', { name: 'Task operations queue', exact: true })).toBeVisible()
  await expect(page.getByRole('searchbox', { name: 'Search tasks', exact: true })).toHaveValue(runID)
  await expect(page.getByRole('combobox', { name: 'Task status', exact: true })).toContainText('Disputed')
  await expect(page.getByRole('combobox', { name: 'Dispute status', exact: true })).toContainText('Awaiting decision')
  await expect(page).toHaveURL(new RegExp(`taskQ=${runID}.*taskStatus=disputed.*taskDisputeStatus=open`))
  const widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)

  const task = page.locator('.task-operations-list article').filter({ hasText: title })
  await expect(task).toContainText('Disputed')
  await expect(task).toContainText('$180.00')
  await task.getByRole('button', { name: 'Resolve dispute', exact: true }).click()
  const commandPanel = page.locator('.admin-command-panel')
  await expect(commandPanel).toBeInViewport()
  await commandPanel.getByRole('combobox', { name: 'Dispute outcome', exact: true }).click()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('listbox')).toHaveCount(0)
  await expect(commandPanel).toBeVisible()
  await commandPanel.getByRole('combobox', { name: 'Dispute outcome', exact: true }).click()
  await page.getByRole('option', { name: 'Cancel without settlement', exact: true }).click()
  const resolution = page.waitForResponse(response => response.url().endsWith(`/admin/tasks/${created.id}/resolve`) && response.request().method() === 'POST')
  await commandPanel.getByRole('button', { name: 'Apply', exact: true }).click()
  expect(await (await resolution).json()).toMatchObject({ disputeStatus: 'resolved_client' })
  await expect(page.getByText('Operation completed.', { exact: true })).toBeVisible()
  await expect(task).toHaveCount(0)
  const resolvedResponse = await page.request.get(`/api/v1/tasks/${created.id}`)
  expect(resolvedResponse.ok()).toBeTruthy()
  const resolved = await resolvedResponse.json()
  expect(resolved.status).toBe('cancelled')
  expect(resolved.settlement).toBeFalsy()
})
