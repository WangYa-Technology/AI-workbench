import { expect, test } from '@playwright/test'

test('publishes a standalone Community post from the Hero drawer', async ({ page }) => {
  const session = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(session.ok()).toBeTruthy()

  await page.goto('/community')
  await expect(page.getByRole('button', { name: 'My posts', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Publish post', exact: true }).click()

  const drawer = page.getByRole('dialog', { name: 'Publish post' })
  await expect(drawer).toBeVisible()
  await expect.poll(async () => {
    const box = await drawer.boundingBox()
    const viewportWidth = await page.evaluate(() => document.documentElement.clientWidth)
    return box ? Math.abs(box.x + box.width - viewportWidth) : Number.POSITIVE_INFINITY
  }).toBeLessThanOrEqual(1)

  const title = `Repeatable workflow notes ${Date.now()}`
  await drawer.getByRole('textbox', { name: 'Title', exact: true }).fill(title)
  await drawer.getByRole('textbox', { name: 'Body', exact: true }).fill('Which generation parameters and review notes make an image workflow easiest for another creator to reproduce?')
  await drawer.getByRole('button', { name: 'Publish post', exact: true }).click()

  await expect(drawer).toBeHidden()
  await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible()

  await page.getByRole('button', { name: 'My posts', exact: true }).click()
  await expect(page).toHaveURL(/view=mine/)
  await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible()
  await page.getByRole('heading', { name: title, exact: true }).click()

  await expect(page.locator('.community-post-layout')).toHaveClass(/is-standalone/)
  await expect(page.locator('.community-post-context')).toHaveCount(0)
  await page.getByLabel('Add comment', { exact: true }).fill('Include the model, input settings, and selected output version.')
  await page.locator('.community-comment-composer').getByRole('button', { name: 'Add comment', exact: true }).click()
  await expect(page.getByText('Include the model, input settings, and selected output version.')).toBeVisible()

  const like = page.getByRole('button', { name: /Like/ }).first()
  await like.click()
  await expect(like).toHaveClass(/active/)
})

test('shows authentication actions instead of publishing controls to guests', async ({ page, context }) => {
  await context.clearCookies()
  await page.goto('/community')

  await expect(page.getByRole('link', { name: 'Sign in', exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Create account', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Publish post', exact: true })).toHaveCount(0)
})

test('returns from an empty My posts view to all Community posts', async ({ page }) => {
  const session = await page.request.post('/api/v1/auth/demo', { data: { actor: 'publisher' } })
  expect(session.ok()).toBeTruthy()

  await page.goto('/community')
  await page.getByRole('button', { name: 'My posts', exact: true }).click()
  await expect(page).toHaveURL(/view=mine/)
  await expect(page.getByRole('heading', { name: 'You have not published a post yet', exact: true })).toBeVisible()

  await page.getByRole('button', { name: 'All posts', exact: true }).click()
  await expect(page).not.toHaveURL(/view=mine/)
  await expect(page.locator('.community-post-row')).not.toHaveCount(0)
})
