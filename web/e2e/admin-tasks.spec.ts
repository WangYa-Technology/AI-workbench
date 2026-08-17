import { expect, test } from '@playwright/test'

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

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  await page.goto(`/market/demands/${created.id}`)
  await page.getByRole('button', { name: 'Accept task', exact: true }).click()
  await page.getByLabel('Delivery note', { exact: true }).fill('Submitted with complete deterministic Local Test source and model evidence.')
  await page.getByRole('button', { name: 'Submit delivery', exact: true }).click()
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
  await expect(page.getByRole('combobox', { name: 'Task status', exact: true })).toHaveValue('disputed')
  await expect(page.getByRole('combobox', { name: 'Dispute status', exact: true })).toHaveValue('open')
  await expect(page).toHaveURL(new RegExp(`taskQ=${runID}.*taskStatus=disputed.*taskDisputeStatus=open`))
  const widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)

  const task = page.locator('.task-operations-list article').filter({ hasText: title })
  await expect(task).toContainText('Disputed')
  await expect(task).toContainText('$180.00')
  await task.getByRole('button', { name: 'Resolve dispute', exact: true }).click()
  const commandPanel = page.locator('.admin-command-panel')
  await expect(commandPanel).toBeInViewport()
  await commandPanel.getByRole('combobox', { name: 'Dispute outcome', exact: true }).selectOption('cancel_without_settlement')
  const reason = `The evidence does not support a Local Test creator settlement for case ${runID}.`
  await commandPanel.getByRole('textbox', { name: 'Required reason', exact: true }).fill(reason)
  await commandPanel.getByRole('checkbox', { name: 'I reviewed the target and confirm this operation.', exact: true }).check()
  await commandPanel.getByRole('button', { name: 'Apply and record', exact: true }).click()

  await expect(page.getByText('Operation completed and audit evidence recorded.', { exact: true })).toBeVisible()
  await expect(task).toHaveCount(0)

  await page.getByRole('button', { name: 'Audit', exact: true }).click()
  await expect(page.getByText('admin.task_dispute_resolved', { exact: true }).first()).toBeVisible()
  await expect(page.locator('.audit-list article').filter({ hasText: reason }).first()).toBeVisible()
})
