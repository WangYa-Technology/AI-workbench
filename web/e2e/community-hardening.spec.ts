import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'

test('saves, resumes, publishes, edits and deletes a private discussion draft', async ({ page }, info) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  const title = `Community lifecycle ${Date.now()}`
  await page.goto('/community')
  await page.locator('.page-hero-header').getByRole('button', { name: 'Publish post', exact: true }).click()
  let dialog = page.getByRole('dialog', { name: 'Publish post', exact: true })
  await dialog.getByRole('textbox', { name: 'Title', exact: true }).fill(title)
  await dialog.getByRole('textbox', { name: 'Body', exact: true }).fill('社区讨论 🙂 保存私密草稿后继续编辑。')
  await dialog.getByRole('button', { name: 'Save draft', exact: true }).click()
  await expect(page).toHaveURL(/view=drafts/)
  const row = page.locator('.community-post-row').filter({ hasText: title })
  await expect(row).toBeVisible()
  await row.getByRole('button', { name: 'Edit discussion', exact: true }).click()
  dialog = page.getByRole('dialog', { name: 'Edit discussion', exact: true })
  await expect(dialog.getByRole('textbox', { name: 'Body', exact: true })).toHaveValue('社区讨论 🙂 保存私密草稿后继续编辑。')
  await dialog.getByRole('button', { name: 'Publish post', exact: true }).click()
  await expect(page).toHaveURL(/view=mine/)
  await page.getByRole('heading', { name: title, exact: true }).getByRole('link').click()
  await expect(page).toHaveURL(/community\/posts\//)
  await page.getByRole('button', { name: 'Edit discussion', exact: true }).click()
  dialog = page.getByRole('dialog', { name: 'Edit discussion', exact: true })
  await dialog.getByRole('textbox', { name: 'Title', exact: true }).fill(`${title} revised`)
  await dialog.getByRole('button', { name: 'Save changes', exact: true }).click()
  await expect(page.getByRole('heading', { name: `${title} revised`, exact: true })).toBeVisible()
  await page.screenshot({ path: info.outputPath('discussion-detail.png'), fullPage: true })
  await page.getByRole('button', { name: 'Edit discussion', exact: true }).click()
  dialog = page.getByRole('dialog', { name: 'Edit discussion', exact: true })
  await dialog.getByRole('checkbox', { name: 'Delete this discussion and remove it from public view' }).check()
  await dialog.getByRole('button', { name: 'Delete discussion', exact: true }).click()
  await expect(page).toHaveURL(/community\?view=mine/)
  await expect(page.getByRole('heading', { name: `${title} revised`, exact: true })).toHaveCount(0)
})

test('retrieves saved standalone discussions and followed authors in shared personal views', async ({ page }, info) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  const title = `Personal community views ${Date.now()}`
  const response = await page.request.post('/api/v1/community/posts', {
    headers: { 'Idempotency-Key': `browser-community-${Date.now()}` },
    data: { title, body: 'A discussion with no associated work or media asset.' },
  })
  expect(response.status()).toBe(201)
  const post = await response.json() as { id: string }
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('publisher') })).ok()).toBeTruthy()
  await page.goto(`/community/posts/${post.id}`)
  await page.getByRole('button', { name: /^Save/ }).click()
  await page.getByRole('button', { name: 'Follow', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Following', exact: true })).toBeVisible()
  await page.goto('/community?view=saved')
  await expect(page.getByRole('tab', { name: 'Saved discussions', exact: true })).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible()
  await page.getByRole('tab', { name: 'Following feed', exact: true }).click()
  await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible()
  await page.setViewportSize({ width: 390, height: 844 })
  await page.screenshot({ path: info.outputPath('community-personal-mobile.png'), fullPage: true })
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(390)
})

test('does not restore another accounts unfinished comment after login changes', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  const response = await page.request.get('/api/v1/community/posts?limit=1')
  const result = await response.json() as { items: { id: string }[] }
  const path = `/community/posts/${result.items[0]!.id}`
  await page.goto(path)
  await page.getByLabel('Join the discussion', { exact: true }).fill('Private unfinished reply from the previous account')
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('publisher') })).ok()).toBeTruthy()
  await page.reload()
  await expect(page.getByLabel('Join the discussion', { exact: true })).toHaveValue('')
})
