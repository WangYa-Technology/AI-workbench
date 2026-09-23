import { randomUUID } from 'node:crypto'
import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'
import { chooseOption } from './helpers/select'

for (const width of [390, 1308]) {
  test(`multi-file draft, individual review and publication at ${width}px`, async ({ page }) => {
    test.setTimeout(90_000)
    await page.setViewportSize({ width, height: 900 })
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
    const files: { id: string; name: string; body: string }[] = []
    for (const name of ['instructions.txt', 'workflow.txt']) {
      const body = `Original ${name} for ${width}px`
      const upload = await page.request.post('/api/v1/assets/uploads', { headers: { 'Idempotency-Key': randomUUID() }, multipart: { title: `Bundle ${width} ${name}`, file: { name, mimeType: 'text/plain', buffer: Buffer.from(body) } } })
      expect(upload.ok()).toBeTruthy()
      const asset = await upload.json()
      files.push({ id: asset.id, name, body })
      await expect.poll(async () => (await (await page.request.get(`/api/v1/assets/${asset.id}`)).json()).scanStatus).toBe('clean')
    }
    await page.goto('/workspace/products/new')
    await page.getByLabel('Product title', { exact: true }).fill(`Multi-file resource ${width}`)
    await page.getByLabel('Description', { exact: true }).fill('Two real originals with stable download names.')
    await chooseOption(page.locator('.seller-product-form select').nth(2), files[0]!.id)
    await page.getByLabel('AI disclosure and rights information', { exact: true }).fill('Independent originals reviewed by the author.')
    await page.getByLabel('Delivered file label', { exact: true }).fill(files[0]!.name)
    await page.getByRole('button', { name: 'Add delivery file', exact: true }).click()
    await expect(page.getByText('All reviewed files are delivered as one ZIP package, with individual downloads available to buyers.', { exact: true })).toBeVisible()
    await chooseOption(page.locator('.seller-product-file').nth(1).locator('select'), files[1]!.id)
    await page.getByLabel('File 2 · Download name', { exact: true }).fill(files[1]!.name)
    await page.getByRole('button', { name: 'Move file 2 up', exact: true }).click()
    await expect(page.getByLabel('File 1 · Download name', { exact: true })).toHaveValue(files[1]!.name)
    await page.getByRole('button', { name: 'Save draft', exact: true }).click()
    await expect(page).toHaveURL(/\/workspace\/products\/[0-9a-f-]{36}$/)
    const id = new URL(page.url()).pathname.split('/').at(-1)!
    await page.reload()
    await expect(page.getByLabel('File 1 · Download name', { exact: true })).toHaveValue(files[1]!.name)
    await expect(page.getByRole('button', { name: 'Submit for review', exact: true })).toBeDisabled()
    expect((await page.request.get(`/api/v1/products/${id}`)).status()).toBe(404)
    await page.locator('.seller-product-file').first().scrollIntoViewIfNeeded()
    await page.screenshot({ path: `/tmp/hcai-product-files-${width}.png`, fullPage: true })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)

    await page.getByRole('checkbox').last().check()
    await page.getByRole('button', { name: 'Submit for review', exact: true }).click()
    await expect(page.locator('.ui-card-actions').first()).toContainText('In review')

    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
    await page.goto(`/admin/products/${id}`)
    for (const [index, file] of [...files].reverse().entries()) {
      const link = page.getByRole('link', { name: `Review file ${index + 1} · ${file.name}`, exact: true })
      const href = await link.getAttribute('href')
      expect(href).toContain(`fileIndex=${index}`)
      const response = await page.request.get(href!)
      expect(response.ok()).toBeTruthy()
      expect(await response.text()).toBe(file.body)
    }
    await page.getByLabel('Decision reason (visible to seller)', { exact: true }).fill('All source files, names and license rights checked.')
    await page.getByRole('button', { name: 'Approve and publish', exact: true }).click()
    await page.getByRole('button', { name: 'Confirm decision', exact: true }).click()
    await expect(page.locator('.ui-card-actions').first()).toContainText('Approved')
    const published = await (await page.request.get(`/api/v1/products/${id}`)).json()
    expect(published.includedFiles).toEqual([...files].reverse().map(file => file.name))
    expect(published.mediaUrl).toBe('')
    for (const file of files) expect((await page.request.get(`/api/v1/assets/${file.id}/content`)).status()).toBe(403)
    await page.getByRole('link', { name: `Review file 1 · ${files[1]!.name}`, exact: true }).scrollIntoViewIfNeeded()
    await page.screenshot({ path: `/tmp/hcai-product-files-review-${width}.png`, fullPage: true })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)

    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
    await page.goto(`/workspace/products/${id}`)
    await page.getByRole('button', { name: 'Pause / withdraw', exact: true }).click()
    await page.getByRole('button', { name: 'Confirm decision', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Save draft', exact: true })).toBeVisible()
    expect((await page.request.get(`/api/v1/products/${id}`)).status()).toBe(404)
    await page.getByRole('button', { name: 'Remove file 2', exact: true }).click()
    await expect(page.getByLabel('Delivered file label', { exact: true })).toHaveValue(files[1]!.name)
    await page.getByRole('button', { name: 'Save draft', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Submit for review', exact: true })).toBeVisible()
    const single = await (await page.request.get(`/api/v1/seller/products/${id}`)).json()
    expect(single.files || []).toEqual([])
    expect(single.assetId).toBe(files[1]!.id)
    expect(single.includedFiles).toEqual([files[1]!.name])
  })
}
