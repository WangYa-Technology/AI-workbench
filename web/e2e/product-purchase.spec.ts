import { expect, test } from '@playwright/test'

test('licenses a product, reuses the Asset, inspects provenance, and records a Local Test refund', async ({ page }) => {
  test.setTimeout(90_000)
  const productTitle = 'Architectural campaign workflow'
  const runID = Date.now().toString(36)

  const registration = await page.request.post('/api/v1/auth/register', { data: {
    email: `purchase-${runID}@example.test`,
    password: `purchase-test-${runID}`,
    handle: `purchase_${runID}`,
    displayName: 'Purchase Workflow Test',
    locale: 'en-US',
    timezone: 'UTC',
  } })
  expect(registration.ok()).toBeTruthy()
  await page.goto('/market')
  await expect(page.getByRole('heading', { name: 'Digital marketplace', exact: true })).toBeVisible()
  await expect(page.getByText('Local Test checkout · no real charge or payout', { exact: true })).toBeVisible()

  await page.getByRole('link', { name: new RegExp(productTitle) }).click()
  await expect(page.getByRole('heading', { name: productTitle, exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'HCAI Commercial Standard', exact: true })).toBeVisible()
  await expect(page.getByText('No standalone redistribution or resale', { exact: true })).toBeVisible()
  const purchaseButton = page.getByRole('button', { name: 'Complete Local Test purchase', exact: true })
  await expect(purchaseButton).toBeDisabled()
  await page.getByLabel(/I reviewed and accept HCAI Commercial Standard/).check()
  await purchaseButton.click()

  await expect(page.getByText('Purchase complete', { exact: true })).toBeVisible()
  await page.getByRole('link', { name: 'Open purchased Asset', exact: true }).click()
  await expect(page).toHaveURL(/\/workspace\/assets\/[0-9a-f-]+$/)
  await expect(page.getByRole('heading', { name: productTitle, exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Purchased from', exact: true })).toBeVisible()
  await expect(page.getByText('HCAI Commercial Standard', { exact: true })).toBeVisible()
  const download = page.waitForEvent('download')
  await page.getByRole('link', { name: 'Download licensed file', exact: true }).click()
  await download

  await page.getByRole('link', { name: 'Use in Create', exact: true }).click()
  await expect(page).toHaveURL(/\/create\/image\?sourceAssetId=/)
  await expect(page.locator('.source-reference').getByText(productTitle, { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Generate image', exact: true }).click()
  await expect(page.locator('.studio-task').first().locator('.studio-task-status')).toContainText('Saved to Assets')
  await page.locator('.studio-task').first().locator('.studio-task-copy').click()
  await page.getByRole('dialog', { name: 'Generation details' }).getByRole('link', { name: 'Inspect provenance', exact: true }).click()

  await expect(page).toHaveURL(/\/workspace\/assets\/[0-9a-f-]+$/)
  await expect(page.getByRole('heading', { name: 'Generated with', exact: true })).toBeVisible()
  await expect(page.locator('.source-lineage').getByText(productTitle, { exact: true })).toBeVisible()
  await expect(page.getByText('HCAI Commercial Standard', { exact: true })).toBeVisible()

  await page.goto('/workspace/orders')
  const order = page.locator('.order-row').filter({ hasText: productTitle }).first()
  await expect(order.getByText('Fulfilled', { exact: true })).toBeVisible()
  await expect(order.getByText('Entitlement and Asset granted', { exact: true })).toBeVisible()
  await order.getByLabel('Refund reason', { exact: true }).fill('The workflow does not fit the intended local campaign production process.')
  await order.getByRole('button', { name: 'Request Local Test refund', exact: true }).click()

  await expect(page.getByText('Local Test refund completed and entitlement revoked.', { exact: true })).toBeVisible()
  await expect(order.getByText('Local Test refunded', { exact: true })).toBeVisible()
  await page.goto('/workspace/purchases')
  await expect(page.getByRole('heading', { name: 'No licensed purchases', exact: true })).toBeVisible()
  await expect(page.getByText('Licensed products you acquire will appear here with their usage rights.', { exact: true })).toBeVisible()
})
