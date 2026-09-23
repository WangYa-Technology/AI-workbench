import { expect, test } from '@playwright/test'
import { chooseOption } from './helpers/select'
import { fixtureCredentials } from './helpers/identity'

test('a new member can publish through the real form in the selected timezone', async ({ page }) => {
  const suffix = Date.now().toString(36)
  const registration = await page.request.post('/api/v1/auth/register', { data: {
    email: `task-member-${suffix}@example.test`, password: `task-member-${suffix}-pass`,
    handle: `task_member_${suffix}`, displayName: 'New Task Member', locale: 'en-US', timezone: 'UTC',
  } })
  expect(registration.ok()).toBeTruthy()
  expect((await registration.json()).user.role).toBe('member')
  await page.goto('/market/demands')
  await page.locator('.ui-page-hero').getByRole('button', { name: 'Publish brief', exact: true }).click()
  await page.getByLabel('Task title', { exact: true }).fill(`Member publish ${suffix}`)
  await page.getByLabel('Short summary', { exact: true }).fill('A complete member production brief for review.')
  await page.getByLabel('Production brief', { exact: true }).fill('Create a source-documented image with reproducible lighting and no third-party logos.')
  await page.getByLabel('Reward (USD)', { exact: true }).fill('0.50')
  const day = new Date(Date.now() + 30 * 86400000).toISOString().slice(0, 10)
  await page.getByLabel('Deadline', { exact: true }).fill(`${day}T18:30`)
  await chooseOption(page.getByLabel('Commissioner timezone', { exact: true }), 'Asia/Shanghai')
  await page.getByLabel('Deliverables, one per line', { exact: true }).fill('Master image\nSource notes')
  await page.getByLabel('Acceptance rules, one per line', { exact: true }).fill('No third-party logos\nSource notes are complete')
  await page.getByLabel('Rights terms', { exact: true }).fill('Licensed campaign use for six months.')
  await page.getByLabel('AI disclosure requirements', { exact: true }).fill('List models and source files.')
  const response = page.waitForResponse(item => item.url().endsWith('/api/v1/tasks') && item.request().method() === 'POST')
  await page.getByRole('button', { name: 'Publish task', exact: true }).click()
  const created = await (await response).json()
  expect(created).toMatchObject({ budgetCents: 50, clientTimezone: 'Asia/Shanghai', allowDerivativeReuse: false })
  expect(Date.parse(created.deadline)).toBe(Date.parse(`${day}T10:30:00Z`))
  await expect(page).toHaveURL(new RegExp(`/market/demands/${created.id}$`))
})

test('original task window keeps polling delayed funding without a payment return query', async ({ page }) => {
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('publisher') })
  const taskId = '00000000-0000-4000-8000-000000000401'
  const response = await page.request.get(`/api/v1/tasks/${taskId}`)
  expect(response.ok()).toBeTruthy()
  const original = await response.json()
  const metaResponse = await page.request.get('/api/v1/meta')
  const meta = await metaResponse.json()
  await page.route('**/api/v1/meta', route => route.fulfill({ json: { ...meta, taskPaymentProvider: { ...meta.taskPaymentProvider, enabled: true } } }))
  let reads = 0
  await page.route(`**/api/v1/tasks/${taskId}`, route => {
    reads++
    return route.fulfill({ json: { ...original, viewerRole: 'client', status: 'open', funding: {
      status: reads > 6 ? 'paid' : 'checkout_open', amountCents: original.budgetCents, currency: 'USD',
      paymentMode: 'stripe', liveMode: false, updatedAt: new Date().toISOString(),
    } } })
  })
  await page.clock.install()
  await page.goto(`/market/demands/${taskId}`)
  await expect(page.getByRole('button', { name: 'Refresh funding status', exact: true })).toBeVisible()
  for (let i = 0; i < 7; i++) {
    const before = reads
    await page.clock.runFor(5100)
    if (before <= 6) await expect.poll(() => reads).toBeGreaterThan(before)
  }
  await expect(page.locator('.task-funding')).toContainText('Funding confirmed')
  expect(reads).toBeGreaterThan(6)
})
