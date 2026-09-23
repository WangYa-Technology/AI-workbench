import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'
import { chooseOption } from './helpers/select'

const order = '00000000-0000-4000-8000-000000009951'
const pending = '00000000-0000-4000-8000-000000009952'
const backup = '00000000-0000-4000-8000-000000009953'
const initial = { orderId: order, title: 'Accepted delivery with a missing file', sha256: 'a'.repeat(64), sizeBytes: 1042, state: 'ready', revision: 0, needed: true, health: 'missing', canRepair: true }

for (const width of [390, 1308]) {
  test(`delivery inspection, confirmed repair and lost-response continuation at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
    const real = await page.request.get(`/api/v1/admin/product-deliveries/${order}`)
    expect(real.status()).toBe(404)
    expect(real.headers()['cache-control']).toBe('private, no-store')
    let status = { ...initial, pendingId: undefined as string | undefined }
    let repairs = 0
    let resumes = 0
    await page.route(`**/api/v1/admin/product-deliveries/${order}`, route => route.fulfill({ json: status }))
    await page.route(`**/api/v1/admin/product-deliveries/${order}/repair`, async route => {
      const request = route.request()
      expect(request.method()).toBe('POST')
      expect(request.headers()['idempotency-key']).toMatch(/^[0-9a-f-]{36}$/)
      expect(request.postDataJSON()).toEqual({ expectedRevision: 0, sourceAssetId: backup, reason: 'Restoring the exact accepted file from a private verified backup.', confirmed: true })
      repairs++
      status = { ...status, revision: 1, pendingId: pending }
      // Server reserved the attempt but the reply was lost. Inspection must
      // discover the same pending ID and continuation must reuse that evidence.
      return route.abort()
    })
    await page.route(`**/api/v1/admin/product-deliveries/${order}/repairs/${pending}/resume`, route => {
      expect(route.request().postDataJSON()).toEqual({ confirmed: true })
      resumes++
      status = { ...status, health: 'healthy', canRepair: false, pendingId: undefined }
      return route.fulfill({ json: status })
    })
    await page.goto('/admin?tab=media')
    await page.getByRole('link', { name: 'Delivery file recovery', exact: true }).click()
    await page.getByLabel('Order ID', { exact: true }).fill(order)
    await page.getByRole('button', { name: 'Inspect delivery', exact: true }).click()
    await expect(page).toHaveURL(`/admin/deliveries/${order}`)
    await expect(page.locator('.delivery-repair-detail')).toContainText('File missing')
    await expect(page.locator('.delivery-repair-detail')).toContainText(initial.sha256)
    await chooseOption(page.locator('.delivery-repair-detail select'), 'backup')
    await page.getByLabel('Backup asset ID', { exact: true }).fill(backup)
    await page.getByLabel('Recovery reason', { exact: true }).fill('Restoring the exact accepted file from a private verified backup.')
    await expect(page.locator('.delivery-repair-detail .ui-select__trigger')).toHaveCount(1)
    await page.getByRole('button', { name: 'Restore delivery file', exact: true }).click()
    await expect(page.getByRole('alertdialog')).toBeVisible()
    await page.getByRole('button', { name: 'Cancel', exact: true }).click()
    expect(repairs).toBe(0)
    await page.getByRole('button', { name: 'Restore delivery file', exact: true }).click()
    await page.getByRole('button', { name: 'Confirm recovery', exact: true }).click()
    await expect(page.getByRole('alert')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Continue recorded repair', exact: true })).toBeVisible()
    expect(repairs).toBe(1)
    await page.getByRole('button', { name: 'Continue recorded repair', exact: true }).click()
    await page.getByRole('button', { name: 'Confirm recovery', exact: true }).click()
    await expect(page.getByRole('status')).toContainText('The delivery file has been verified and is ready.')
    await expect(page.getByRole('button', { name: 'Restore delivery file', exact: true })).toHaveCount(0)
    expect(resumes).toBe(1)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.evaluate(() => {
      if (document.activeElement instanceof HTMLElement) document.activeElement.blur()
      window.scrollTo({ top: 0, behavior: 'instant' })
      document.querySelector('#main-content')?.scrollTo({ top: 0, behavior: 'instant' })
    })
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0)
    await expect.poll(() => page.locator('.skip-link').evaluate(el => el.getBoundingClientRect().bottom)).toBeLessThan(0)
    await expect(page.locator('.delivery-repair-lookup')).toBeInViewport()
    await expect(page.locator('.content-context-bar')).not.toHaveClass(/is-visible/)
    await page.screenshot({ path: `/tmp/hcai-delivery-repair-${width}.png`, fullPage: true, animations: 'disabled' })
  })
}

test('delivery repair rejects non-admin access and ignores stale inspections', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  expect((await page.request.get(`/api/v1/admin/product-deliveries/${order}`)).status()).toBe(403)
  await page.goto(`/admin/deliveries/${order}`)
  await expect(page.getByText('Media operations permission is required', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Inspect delivery' })).toHaveCount(0)
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
  const other = '00000000-0000-4000-8000-000000009954'
  let release!: () => void
  const held = new Promise<void>(resolve => { release = resolve })
  let requested!: () => void
  const arrived = new Promise<void>(resolve => { requested = resolve })
  await page.route(`**/api/v1/admin/product-deliveries/${order}`, async route => {
    requested()
    await held
    await route.fulfill({ json: initial }).catch(() => {})
  })
  await page.route(`**/api/v1/admin/product-deliveries/${other}`, route => route.fulfill({ json: { ...initial, orderId: other, title: 'Current order only', health: 'healthy', canRepair: false } }))
  await page.goto(`/admin/deliveries/${order}`)
  await arrived
  // Navigate within the router while the old request is held in flight.
  await page.evaluate(path => {
    history.pushState({}, '', path)
    dispatchEvent(new PopStateEvent('popstate'))
  }, `/admin/deliveries/${other}`)
  await expect(page.getByRole('heading', { name: 'Current order only' })).toBeVisible()
  release()
  await expect(page.getByRole('heading', { name: initial.title })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Restore delivery file', exact: true })).toHaveCount(0)
})

for (const width of [390, 1308]) {
  test(`direct recovery upload validates size and continues the same reservation at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
    const bytes = Buffer.from([0, 255, 13, 10, 128, 64])
    let status = { ...initial, sizeBytes: bytes.length, pendingId: undefined as string | undefined, pendingSource: undefined as string | undefined }
    let reserves = 0
    let uploads = 0
    await page.route(`**/api/v1/admin/product-deliveries/${order}`, route => route.fulfill({ json: status }))
    await page.route(`**/api/v1/admin/product-deliveries/${order}/repair-upload`, route => {
      reserves++
      expect(route.request().postDataJSON()).toEqual({ expectedRevision: 0, confirmed: true, reason: 'Restore the exact original backup file.' })
      expect(route.request().headers()['idempotency-key']).toMatch(/^[0-9a-f-]{36}$/)
      status = { ...status, revision: 1, pendingId: pending, pendingSource: 'upload' }
      return route.fulfill({ json: status })
    })
    await page.route(`**/api/v1/admin/product-deliveries/${order}/repairs/${pending}/content?confirmed=true`, route => {
      uploads++
      expect(route.request().method()).toBe('PUT')
      expect(route.request().headers()['content-type']).toBe('application/octet-stream')
      expect(route.request().postDataBuffer()).toEqual(bytes)
      if (uploads === 1) return route.abort()
      status = { ...status, health: 'healthy', canRepair: false, pendingId: undefined, pendingSource: undefined }
      return route.fulfill({ json: status })
    })
    await page.goto(`/admin/deliveries/${order}`)
    await chooseOption(page.locator('.delivery-repair-detail select'), 'upload')
    const file = page.getByLabel('Backup file', { exact: true })
    await file.setInputFiles({ name: 'wrong.bin', mimeType: 'application/octet-stream', buffer: Buffer.from('x') })
    await page.getByLabel('Recovery reason', { exact: true }).fill('Restore the exact original backup file.')
    await expect(page.getByRole('alert')).toContainText('exactly 6 bytes')
    await expect(page.getByRole('button', { name: 'Restore delivery file', exact: true })).toBeDisabled()
    await file.setInputFiles({ name: 'private-original.jpg', mimeType: 'image/jpeg', buffer: bytes })
    await expect(page.locator('input.ui-file-input')).toHaveCount(1)
    await expect(page.getByRole('alert')).toHaveCount(0)
    await page.getByRole('button', { name: 'Restore delivery file', exact: true }).click()
    await page.getByRole('button', { name: 'Cancel', exact: true }).click()
    expect(reserves).toBe(0)
    expect(uploads).toBe(0)
    await page.getByRole('button', { name: 'Restore delivery file', exact: true }).click()
    await page.getByRole('button', { name: 'Confirm recovery', exact: true }).click()
    await expect(page.getByRole('alert')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Upload to recorded repair', exact: true })).toBeDisabled()
    await file.setInputFiles({ name: 'private-original.jpg', mimeType: 'image/jpeg', buffer: bytes })
    await expect(page.getByRole('button', { name: 'Upload to recorded repair', exact: true })).toBeEnabled()
    expect(await page.getByRole('alert').locator('div').last().evaluate(el => el.getBoundingClientRect().width)).toBeGreaterThan(200)
    await page.evaluate(() => {
      if (document.activeElement instanceof HTMLElement) document.activeElement.blur()
      window.scrollTo({ top: 0, behavior: 'instant' })
      document.querySelector('#main-content')?.scrollTo({ top: 0, behavior: 'instant' })
    })
    await expect(page.locator('.content-context-bar')).not.toHaveClass(/is-visible/)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.screenshot({ path: `/tmp/hcai-delivery-upload-${width}.png`, fullPage: true, animations: 'disabled' })
    await page.getByRole('button', { name: 'Upload to recorded repair', exact: true }).click()
    await page.getByRole('button', { name: 'Confirm recovery', exact: true }).click()
    await expect(page.getByRole('status')).toContainText('The delivery file has been verified and is ready.')
    expect(reserves).toBe(1)
    expect(uploads).toBe(2)
  })
}

test('a stale upload reservation never sends the selected file to another route', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
  const other = '00000000-0000-4000-8000-000000009954'
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  let requested!: () => void
  const arrived = new Promise<void>(resolve => { requested = resolve })
  let uploads = 0
  await page.route(`**/api/v1/admin/product-deliveries/${order}`, route => route.fulfill({ json: { ...initial, sizeBytes: 1 } }))
  await page.route(`**/api/v1/admin/product-deliveries/${order}/repair-upload`, async route => {
    requested()
    await gate
    await route.fulfill({ json: { ...initial, revision: 1, pendingId: pending, pendingSource: 'upload' } }).catch(() => {})
  })
  await page.route('**/api/v1/admin/product-deliveries/**/content?confirmed=true', route => { uploads++; return route.abort() })
  await page.route(`**/api/v1/admin/product-deliveries/${other}`, route => route.fulfill({ json: { ...initial, orderId: other, title: 'New inspection', canRepair: false, health: 'healthy' } }))
  await page.goto(`/admin/deliveries/${order}`)
  await chooseOption(page.locator('.delivery-repair-detail select'), 'upload')
  await page.getByLabel('Backup file', { exact: true }).setInputFiles({ name: 'private.bin', mimeType: 'application/octet-stream', buffer: Buffer.from('x') })
  await page.getByLabel('Recovery reason', { exact: true }).fill('Restore the exact original backup file.')
  await page.getByRole('button', { name: 'Restore delivery file', exact: true }).click()
  await page.getByRole('button', { name: 'Confirm recovery', exact: true }).click()
  await arrived
  await page.evaluate(path => { history.pushState({}, '', path); dispatchEvent(new PopStateEvent('popstate')) }, `/admin/deliveries/${other}`)
  await expect(page.getByRole('heading', { name: 'New inspection' })).toBeVisible()
  release()
  await expect(page.getByRole('alertdialog')).toHaveCount(0)
  await expect(page.getByLabel('Backup file', { exact: true })).toHaveCount(0)
  expect(uploads).toBe(0)
})
