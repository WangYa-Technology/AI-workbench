import { fixtureCredentials } from './helpers/identity'
import { expect, test } from '@playwright/test'

for (const scope of ['community', 'marketplace']) {
  test(`${scope}: administrator category lifecycle and responsive filtering`, async ({ page }) => {
    const code = `e2e_${scope}_${Date.now().toString(36)}`
    const title = `Category ${code}`
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
    await page.goto(`/admin?tab=${scope === 'community' ? 'communityCategories' : 'marketplaceCategories'}`)
    const manager = page.locator('.category-manager')
    await expect(manager).toBeVisible()
    const create = manager.locator('.category-edit-row').first()
    await create.getByLabel('Code', { exact: true }).fill(code)
    await create.getByLabel('Chinese name').fill('测试分类')
    await create.getByLabel('English name').fill(title)
    await create.getByLabel('Order', { exact: true }).fill('0')
    await create.getByRole('button', { name: 'Add', exact: true }).click()
    const row = manager.locator('.category-edit-row').filter({ has: page.locator('code', { hasText: code }) })
    await expect(row).toBeVisible()
    await row.getByLabel('English name').fill(`${title} renamed`)
    await row.getByRole('button', { name: 'Save', exact: true }).click()
    await expect(manager.getByRole('status')).toHaveText('Saved')
    const path = scope === 'community' ? '/community' : '/market'
    await page.setViewportSize({ width: 1551, height: 901 })
    await page.goto(path)
    await expect(page.locator('.category-sidebar')).toBeVisible()
    await expect(page.locator('.category-filter')).toBeHidden()
    await page.locator('.category-sidebar').getByRole('button', { name: new RegExp(`^${title} renamed(?: 0)?$`) }).click()
    await expect(page).toHaveURL(new RegExp(`category=${code}`))
    const response = await page.request.get(`/api/v1/${scope === 'community' ? 'community/posts' : 'products'}?category=${code}`)
    expect((await response.json()).items).toEqual([])
    await page.reload()
    await expect(page.locator('.category-sidebar .active')).toHaveText(new RegExp(`^${title} renamed(?:\\s*0)?$`))
    for (const width of [1308, 390]) {
      await page.setViewportSize({ width, height: 901 })
      await expect(page.locator('.category-sidebar')).toBeHidden()
      await expect(page.locator('.category-filter')).toBeVisible()
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy()
    }
    await page.goto(`/admin?tab=${scope === 'community' ? 'communityCategories' : 'marketplaceCategories'}`)
    await row.getByRole('button', { name: 'Delete', exact: true }).click()
    await manager.locator('.category-delete').filter({ hasText: `Delete category: ${code}` }).getByRole('button', { name: 'Delete', exact: true }).click()
    await expect(row).toHaveCount(0)
    const directory = await page.request.get(`/api/v1/task-types?scope=${scope}`)
    expect((await directory.json()).items.some((item: { code: string }) => item.code === code)).toBe(false)
  })
}

test('market content can be assigned and transferred before deleting its category', async ({ page }) => {
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })
  const product = (await (await page.request.get('/api/v1/products')).json()).items[0]
  const code = `assign_${Date.now().toString(36)}`
  expect((await page.request.post('/api/v1/admin/task-types?scope=marketplace', { data: { code, nameZh: '测试', nameEn: code, icon: 'mixed', sortOrder: 1 } })).ok()).toBeTruthy()
  await page.goto('/admin?tab=marketplaceCategories')
  const manager = page.locator('.category-manager')
  await manager.getByRole('combobox', { name: 'Select content', exact: true }).click()
  await page.getByRole('option', { name: new RegExp(product.title) }).click()
  await manager.getByRole('combobox', { name: 'Target category', exact: true }).click()
  await page.getByRole('option', { name: code, exact: true }).click()
  await manager.locator('.category-delete').getByRole('button', { name: 'Save', exact: true }).click()
  await expect(manager.getByRole('status')).toHaveText('Saved')
  let item = await (await page.request.get(`/api/v1/products/${product.id}`)).json()
  expect(item.category).toBe(code)
  expect(item.productType).toBe(product.productType)
  const row = manager.locator('.category-edit-row').filter({ has: page.locator('code', { hasText: code }) })
  await row.getByRole('button', { name: 'Delete', exact: true }).click()
  const deletion = manager.locator('.category-delete').filter({ hasText: `Delete category: ${code}` })
  await deletion.getByRole('button', { name: 'Delete', exact: true }).click()
  await expect(manager.getByRole('alert')).toContainText('replacement')
  await deletion.getByRole('combobox', { name: 'Move content to' }).click()
  const directory = (await (await page.request.get('/api/v1/task-types?scope=marketplace')).json()).items
  await page.getByRole('option', { name: directory.find((i: { code: string }) => i.code === product.category).nameEn, exact: true }).click()
  await deletion.getByRole('button', { name: 'Delete', exact: true }).click()
  await expect(row).toHaveCount(0)
  item = await (await page.request.get(`/api/v1/products/${product.id}`)).json()
  expect(item.category).toBe(product.category)
  expect(item.priceCents).toBe(product.priceCents)
})
