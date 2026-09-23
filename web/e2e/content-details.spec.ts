import { fixtureCredentials } from './helpers/identity'
import { expect, test } from '@playwright/test'

test('work details keep the primary action visible on mobile and preserve list filters', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/discover?kind=image')
  await page.getByRole('link', { name: 'View work', exact: true }).and(page.locator('a[href*="00000000-0000-4000-8000-000000000201"]')).click()
  const action = page.locator('.work-detail-heading').getByRole('link', { name: 'Create with reference', exact: true })
  await expect(action).toBeVisible()
  const box = await action.boundingBox()
  expect(box!.y + box!.height).toBeLessThan(600)
  const prompt = await page.locator('.prompt-text').innerText()
  await page.getByRole('button', { name: 'Copy prompt', exact: true }).click()
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(prompt)
  await page.locator('.back-link').click()
  await expect(page).toHaveURL(/\/discover\?kind=image$/)
})

test('task facts precede the brief and unavailable payments are explained once', async ({ page }) => {
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.route('**/api/v1/tasks/00000000-0000-4000-8000-000000000401', async route => {
    const response = await route.fetch()
    await route.fulfill({ json: { ...await response.json(), allowDirectAccept: true, deadline: new Date(Date.now() + 86400000).toISOString() } })
  })
  await page.goto('/market/demands/00000000-0000-4000-8000-000000000401')
  await expect(page.locator('.task-key-facts')).toContainText('Task budget')
  const facts = await page.locator('.task-key-facts').boundingBox()
  const brief = await page.locator('.task-brief-overview').boundingBox()
  expect(facts!.y).toBeLessThan(brief!.y)
  await expect(page.locator('.task-reward')).toHaveCount(0)
  await expect(page.locator('#task-payment-disabled')).toBeVisible()
  await expect(page.locator('[aria-describedby=task-payment-disabled]')).toBeDisabled()
  await expect(page.getByRole('link', { name: 'Start creating', exact: true })).toHaveCount(0)
})

test('post renders safe structured content and sends video work to video creation', async ({ page }) => {
  const id = '00000000-0000-4000-8000-000000000301'
  const response = await page.request.get(`/api/v1/community/posts/${id}`)
  const post = await response.json()
  await page.route(`**/api/v1/community/posts/${id}`, route => route.fulfill({ json: { ...post, mediaKind: 'video', body: '## Steps\n\n- Compose\n- Review\n\n<script>window.unsafePost = true</script>' } }))
  await page.goto(`/community/posts/${id}`)
  await expect(page.locator('.community-post-body h2')).toHaveText('Steps')
  await expect(page.locator('.community-post-body li')).toHaveCount(2)
  expect(await page.evaluate(() => 'unsafePost' in window)).toBe(false)
  await expect(page.locator('.community-post-context-actions a').last()).toHaveAttribute('href', /\/create\/video\?sourceWorkId=/)
})

test('post keeps the comment draft across login navigation and reload', async ({ page }) => {
  const path = '/community/posts/00000000-0000-4000-8000-000000000301'
  await page.goto(path)
  await page.locator('#community-comment').fill('A draft that should survive signing in.')
  await page.locator('.community-comment-composer button[type="submit"]').click()
  await expect(page).toHaveURL(/\/auth\?/)
  await page.goto(path)
  await expect(page.locator('#community-comment')).toHaveValue('A draft that should survive signing in.')
  await page.reload()
  await expect(page.locator('#community-comment')).toHaveValue('A draft that should survive signing in.')
})

test('post action menu supports keyboard opening and escape focus return', async ({ page }) => {
  await page.goto('/community/posts/00000000-0000-4000-8000-000000000301')
  const trigger = page.getByRole('button', { name: 'More actions', exact: true })
  await trigger.focus()
  await page.keyboard.press('ArrowDown')
  await expect(page.getByRole('menuitem')).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('menu')).toHaveCount(0)
  await expect(trigger).toBeFocused()
})

test('comment pagination errors stay beside the discussion and can be retried', async ({ page }) => {
  const id = '00000000-0000-4000-8000-000000000301'
  let attempts = 0
  await page.route(`**/api/v1/community/posts/${id}/comments?*`, async route => {
    if (!new URL(route.request().url()).searchParams.has('cursor')) {
      const response = await route.fetch()
      return route.fulfill({ json: { ...await response.json(), nextCursor: 'retry-page' } })
    }
    if (attempts++ === 0) return route.fulfill({ status: 503, json: { error: { code: 'internal_error', message: 'Try again' } } })
    return route.fulfill({ json: { items: [] } })
  })
  await page.goto(`/community/posts/${id}`)
  const comments = page.locator('#post-comments')
  await comments.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect(comments.getByRole('alert')).toBeVisible()
  await expect(page.locator('#community-comment')).toBeVisible()
  await comments.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect(comments.getByRole('alert')).toHaveCount(0)
  await expect(comments.getByRole('button', { name: 'Load more', exact: true })).toHaveCount(0)
})

test('unassigned task sources cannot silently submit an unrelated generation', async ({ page }) => {
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  await page.goto('/create/image?taskId=00000000-0000-4000-8000-000000000401')
  await page.locator('.creation-composer textarea').fill('A careful editorial composition with a clear subject.')
  await expect(page.locator('.creation-context-chip')).toBeVisible()
  const generate = page.getByRole('button', { name: 'Generate Image', exact: true })
  await expect(generate).toBeDisabled()
  await page.locator('.creation-context-chip button').click()
  await expect(page).toHaveURL(/\/create\/image(?:\?|$)/)
  await expect(generate).toBeEnabled()
})

test('an unavailable source asset cannot silently become an unrelated generation', async ({ page }) => {
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  await page.goto('/create/image?sourceAssetId=00000000-0000-4000-8000-999999999999')
  await page.locator('.creation-composer textarea').fill('A deliberate reference-based image request')
  await expect(page.locator('.creation-submit')).toBeDisabled()
  const source = page.locator('.creation-context-chip').filter({ hasText: 'This reference needs review before it can be used.' })
  await expect(source).toBeVisible()
  await source.getByRole('button', { name: 'Close', exact: true }).click()
  await expect(page).not.toHaveURL(/sourceAssetId=/)
  await expect(page.locator('.creation-submit')).toBeEnabled()
})
