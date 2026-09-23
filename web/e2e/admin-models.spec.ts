import { fixtureCredentials } from './helpers/identity'
import { chooseOption } from './helpers/select'
import { expect, test } from '@playwright/test'

test('activates a versioned model route used by subsequent generation', async ({ page }) => {
  const runID = Date.now().toString(36)
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  expect((await page.request.get('/api/v1/admin/models/routes')).status()).toBe(403)

  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })
  await page.goto('/admin?tab=models')
  await expect(page.getByRole('heading', { name: 'Default model', exact: true })).toBeVisible()
  const form = page.locator('.model-routes-admin form')
  await chooseOption(form.getByRole('combobox', { name: 'Creation type', exact: true }), 'image')
  await form.getByRole('textbox', { name: 'Policy revision name', exact: true }).fill(`Browser image route ${runID}`)
  await form.getByRole('spinbutton', { name: 'Timeout per attempt (seconds)', exact: true }).fill('90')
  await form.getByRole('spinbutton', { name: 'Failed retry attempts', exact: true }).fill('2')
  await form.getByRole('button', { name: 'Activate revision', exact: true }).click()
  await expect(page.getByText('Default model policy activated.', { exact: true })).toBeVisible()

  const currentResponse = await page.request.get('/api/v1/admin/models/routes?mode=image&limit=1')
  expect(currentResponse.ok()).toBeTruthy()
  let currentPolicy = await currentResponse.json()
  for (let version = currentPolicy.routes.image.version; version < 21; version += 1) {
    const response = await page.request.post('/api/v1/admin/models/routes/image', {
      data: {
        providerProfileId: 'local-image-v1', name: `Local Test pagination route v${version + 1}`,
        timeoutSeconds: 90, maxAttempts: 2,
        expectedVersion: version,
      },
    })
    expect(response.ok()).toBeTruthy()
    currentPolicy = await response.json()
  }

  await page.goto('/admin?tab=models')
  const historyRows = page.locator('.model-routes-admin .ranking-history-list article')
  await expect(historyRows).toHaveCount(20)
  const activeVersion = await page.locator('.model-routes-admin > section:first-child > header > span').textContent()
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect.poll(() => historyRows.count()).toBeGreaterThan(20)
  const routeIDs = await historyRows.evaluateAll((rows) => rows.map((row) => row.getAttribute('data-model-route-id')))
  expect(new Set(routeIDs).size).toBe(routeIDs.length)
  await expect(page.locator('.model-routes-admin > section:first-child > header > span')).toHaveText(activeVersion || '')

  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  const generationResponse = await page.request.post('/api/v1/generations', {
    headers: { 'Idempotency-Key': `model-route-${runID}` }, data: { mode: 'image', prompt: `Subsequent routed generation ${runID}` },
  })
  expect(generationResponse.status()).toBe(202)
  expect(await generationResponse.json()).toMatchObject({ provider: 'local_test', modelName: 'hcai-local-image-v1' })

})
