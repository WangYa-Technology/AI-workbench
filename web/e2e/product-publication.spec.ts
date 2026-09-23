import { randomUUID } from 'node:crypto'
import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'
import { chooseOption } from './helpers/select'

for (const width of [390, 1308]) {
  test(`seller draft, review, publication and pause at ${width}px`, async ({ page }) => {
    test.setTimeout(90_000)
    await page.setViewportSize({ width, height: 900 })
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
    const title = `Publication original ${width}`
    const upload = await page.request.post('/api/v1/assets/uploads', { headers: { 'Idempotency-Key': randomUUID() }, multipart: { title, file: { name: `original-${width}.txt`, mimeType: 'text/plain', buffer: Buffer.from(`Independent private delivery ${width}.`) } } })
    expect(upload.ok()).toBeTruthy()
    const asset = await upload.json()
    await expect.poll(async () => (await (await page.request.get(`/api/v1/assets/${asset.id}`)).json()).scanStatus).toBe('clean')
    await page.goto('/market')
    await page.getByRole('link', { name: 'My products', exact: true }).click()
    await page.getByRole('link', { name: 'New product', exact: true }).click()
    await page.getByLabel('Product title', { exact: true }).fill(`Sellable resource ${width}`)
    await page.getByLabel('Description', { exact: true }).fill('One independently owned text file, checked by its author.')
    await chooseOption(page.locator('.seller-product-form select').nth(2), asset.id)
    await page.getByLabel('AI disclosure and rights information', { exact: true }).fill('Original text written and reviewed by the seller.')
    await page.getByLabel('Delivered file label', { exact: true }).fill('original.txt')
    await page.getByLabel('Price (USD)', { exact: true }).fill('19.00')
    await page.screenshot({ path: `/tmp/hcai-product-form-${width}.png`, fullPage: true })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.getByRole('button', { name: 'Save draft', exact: true }).click()
    await expect(page).toHaveURL(/\/workspace\/products\/[0-9a-f-]{36}$/)
    const id = new URL(page.url()).pathname.split('/').at(-1)!
    await expect(page.getByRole('button', { name: 'Submit for review', exact: true })).toBeDisabled()
    // Changing an input must not submit the old saved content under a new display.
    await page.getByLabel('Product title', { exact: true }).fill(`Revised resource ${width}`)
    await expect(page.getByText('Save your changes before submitting for review.', { exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Submit for review', exact: true })).toHaveCount(0)
    await page.getByRole('button', { name: 'Save draft', exact: true }).click()
    await page.getByRole('checkbox', { name: 'I own the required rights and may sell this file under the selected license.', exact: true }).check()
    await page.getByRole('button', { name: 'Submit for review', exact: true }).click()
    await expect(page.locator('.ui-card-actions').first()).toContainText('In review')
    await expect(page.getByLabel('Product title', { exact: true })).toBeDisabled()

    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
    await page.goto('/admin?tab=content')
    await page.getByRole('link', { name: 'Product review', exact: true }).click()
    await page.getByRole('link').filter({ hasText: `Revised resource ${width}` }).click()
    await expect(page).toHaveURL(`/admin/products/${id}`)
    const file = await page.request.get((await page.getByRole('link', { name: 'Download original for review', exact: true }).getAttribute('href'))!)
    expect(file.ok()).toBeTruthy()
    expect(await file.text()).toBe(`Independent private delivery ${width}.`)
    await page.getByLabel('Decision reason (visible to seller)', { exact: true }).fill('Original file, description and license rights checked.')
    await page.getByRole('button', { name: 'Approve and publish', exact: true }).click()
    await expect(page.getByRole('alertdialog')).toBeVisible()
    await page.getByRole('button', { name: 'Confirm decision', exact: true }).click()
    await expect(page.locator('.ui-card-actions').first()).toContainText('Approved')
    await page.screenshot({ path: `/tmp/hcai-product-review-${width}.png`, fullPage: true })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    const publicOffer = await (await page.request.get(`/api/v1/products/${id}`)).json()
    expect(publicOffer.listingManaged).toBe(true)
    expect(publicOffer.mediaUrl).toBe('')
    // The normal asset endpoint must not grant the reviewer or a visitor access.
    expect((await page.request.get(`/api/v1/assets/${asset.id}/content`)).status()).toBe(403)

    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
    await page.goto(`/market/assets/${id}`)
    await page.getByRole('link', { name: 'Manage product', exact: true }).click()
    await page.getByRole('button', { name: 'Pause / withdraw', exact: true }).click()
    await page.getByRole('button', { name: 'Confirm decision', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Save draft', exact: true })).toBeVisible()
    expect((await page.request.get(`/api/v1/products/${id}`)).status()).toBe(404)
    // Review commits the notification and its delivery job together. The inbox
    // contains delivered notifications, so wait for the worker's actual result.
    await expect.poll(async () => {
      const response = await page.request.get('/api/v1/notifications?kind=marketplace.listing_reviewed')
      expect(response.ok()).toBeTruthy()
      const notification = await response.json()
      return notification.items.some((item: { targetPath: string }) => item.targetPath === `/workspace/products/${id}`)
    }).toBe(true)
  })
}
