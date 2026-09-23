import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'

test('recovers an acknowledged server upload after response loss and page reload', async ({ page }) => {
  const login = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  expect(login.ok()).toBeTruthy()
  const title = `Upload retry evidence ${Date.now()}`
  const file = { name: 'retry-source.txt', mimeType: 'text/plain', buffer: Buffer.from(`Private retry content ${title}`) }
  const keys: string[] = []
  const assetIDs: string[] = []
  await page.route('**/api/v1/assets/uploads', async route => {
    const key = route.request().headers()['idempotency-key']
    expect(key).toBeTruthy()
    keys.push(key!)
    const response = await route.fetch()
    expect(response.status()).toBe(keys.length === 1 ? 201 : 200)
    assetIDs.push((await response.json()).id)
    if (keys.length === 1) await route.abort('failed')
    else await route.fulfill({ response })
  })
  const submit = async () => {
    await page.getByRole('button', { name: 'Upload Asset', exact: true }).click()
    const panel = page.locator('.asset-upload-panel')
    await panel.getByLabel('Asset title', { exact: true }).fill(title)
    await panel.locator('input[type="file"]').setInputFiles(file)
    await panel.getByRole('button', { name: 'Upload and scan', exact: true }).click()
  }
  await page.goto('/workspace/assets')
  await submit()
  await expect.poll(() => assetIDs.length).toBe(1)
  await expect(page.locator('.asset-upload-panel').getByRole('button', { name: 'Upload and scan', exact: true })).toBeEnabled()
  await page.reload()
  await submit()
  await expect.poll(() => assetIDs.length).toBe(2)
  expect(keys[0]).toBe(keys[1])
  expect(assetIDs[0]).toBe(assetIDs[1])
  await expect(page.locator('.asset-upload-panel')).toHaveCount(0)
  const response = await page.request.get('/api/v1/assets', { params: { q: title } })
  expect(response.ok()).toBeTruthy()
  const rows = (await response.json()).items.filter((item: { title: string }) => item.title === title)
  expect(rows).toHaveLength(1)
  await page.goto(`/workspace/assets/${assetIDs[0]}`)
  await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible()
})
