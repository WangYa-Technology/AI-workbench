import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'

const workId = '00000000-0000-4000-8000-000000000201'

test('work bookmarks use the community store and expose discussion and login return', async ({ page }, testInfo) => {
  await page.goto(`/works/${workId}`)
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page).toHaveURL(/\/auth\?.*returnTo=/)
  expect(new URL(page.url()).searchParams.get('returnTo')).toBe(`/works/${workId}`)
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  await page.goto(`/works/${workId}`)
  const work = await (await page.request.get(`/api/v1/works/${workId}`)).json()
  await expect(page.getByRole('link', { name: 'Discuss or report' })).toHaveAttribute('href', `/community/posts/${work.postId}`)
  await page.request.put(`/api/v1/community/posts/${work.postId}/reactions/bookmark`, { data: { active: false } })
  await page.reload()
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Remove bookmark' })).toHaveAttribute('aria-pressed', 'true')
  const saved = await (await page.request.get('/api/v1/assets/saved-works')).json()
  expect(saved.items.some((item: { workId: string }) => item.workId === workId)).toBe(true)
  await page.reload()
  await page.getByRole('button', { name: 'Remove bookmark' }).click()
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toHaveAttribute('aria-pressed', 'false')
  await page.screenshot({ path: testInfo.outputPath('work-actions.png'), fullPage: true })
  await page.getByRole('link', { name: 'Discuss or report' }).click()
  await expect(page.locator('.community-comment-composer')).toBeVisible()
  await expect(page.getByRole('button', { name: 'More actions', exact: true })).toBeVisible()
})

test('unresolved source blocks submission, supports retry, and preserves user edits', async ({ page }) => {
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  const fixture = await (await page.request.get(`/api/v1/works/${workId}`)).json()
  let retry: (() => void) | undefined
  let attempts = 0
  await page.route(`**/api/v1/works/${workId}`, async route => {
    if (++attempts === 1) { await route.fulfill({ status: 404, json: { error: { code: 'work_not_found' } } }); return }
    await new Promise<void>(resolve => { retry = resolve })
    await route.fulfill({ json: fixture })
  })
  await page.goto(`/create/image?sourceWorkId=${workId}`)
  const prompt = page.locator('.creation-composer textarea')
  await prompt.fill('My own prompt that must be preserved')
  const generate = page.getByRole('button', { name: 'Generate Image', exact: true })
  await expect(page.getByText(/This source is unavailable/)).toBeVisible()
  await expect(generate).toBeDisabled()
  await page.getByRole('button', { name: 'Try again', exact: true }).click()
  await expect.poll(() => Boolean(retry)).toBe(true)
  await prompt.fill('Edited while the source request was pending')
  await expect(generate).toBeDisabled()
  retry!()
  await expect(page.locator('.creation-context-chip')).toContainText(fixture.title)
  await expect(prompt).toHaveValue('Edited while the source request was pending')
  await expect(generate).toBeEnabled()
  await page.goto('/create/image?sourceWorkId=00000000-0000-4000-8000-000000000999')
  await prompt.fill('Continue only after removing the unavailable source')
  await expect(generate).toBeDisabled()
  await page.getByRole('button', { name: 'Remove source and continue' }).click()
  await expect(page).not.toHaveURL(/sourceWorkId/)
  await expect(generate).toBeEnabled()
})

test('prompt states are honest and all media use the same reference action', async ({ page }) => {
  const fixture = await (await page.request.get(`/api/v1/works/${workId}`)).json()
  for (const [state, text] of [['public', 'No prompt has been provided'], ['private', 'has not shared'], ['partial', 'no public excerpt'], ['purchased', 'not supported']]) {
    await page.route(`**/api/v1/works/${workId}`, route => route.fulfill({ json: { ...fixture, prompt: undefined, promptVisibility: state } }))
    await page.goto(`/works/${workId}`)
    await expect(page.locator('.work-inspector')).toContainText(text)
    await expect(page.getByRole('button', { name: 'Copy prompt', exact: true })).toHaveCount(0)
    await page.unroute(`**/api/v1/works/${workId}`)
  }
  const kinds = ['image', 'video', 'audio', 'document']
  await page.route('**/api/v1/works?*', route => route.fulfill({ json: { items: kinds.map((mediaKind, i) => ({ ...fixture, id: `${i}`, mediaKind })), total: 4, categoryCounts: { image: 1, video: 1, audio: 1, document: 1 }, nextCursor: null } }))
  await page.goto('/discover?q=reference')
  const links = page.locator('.inspiration-card').getByRole('link', { name: 'Create with reference', exact: true })
  await expect(links).toHaveCount(4)
  for (const [i, mode] of ['image', 'video', 'music', 'chat'].entries()) await expect(links.nth(i)).toHaveAttribute('href', `/create/${mode}?sourceWorkId=${i}`)
})

test('stale pagination cannot clear the new request loading state or change its count', async ({ page }) => {
  const fixture = await (await page.request.get(`/api/v1/works/${workId}`)).json()
  let releaseOld: (() => void) | undefined
  let releaseNew: (() => void) | undefined
  await page.route('**/api/v1/works?*', async route => {
    const url = new URL(route.request().url())
    const isNew = url.searchParams.get('q') === 'new'
    if (url.searchParams.has('cursor')) {
      await new Promise<void>(resolve => { if (isNew) releaseNew = resolve; else releaseOld = resolve })
      await route.fulfill({ json: { items: [{ ...fixture, id: isNew ? 'new-second' : 'old-second' }], total: isNew ? 2 : 9, categoryCounts: {}, nextCursor: null } })
    } else await route.fulfill({ json: { items: [{ ...fixture, id: isNew ? 'new-first' : 'old-first' }], total: isNew ? 2 : 9, categoryCounts: {}, nextCursor: 'next' } })
  })
  await page.goto('/discover?q=old')
  await expect(page.locator('.inspiration-results-meta')).toContainText('9 works')
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect.poll(() => Boolean(releaseOld)).toBe(true)
  await page.getByRole('searchbox', { name: 'Search works', exact: true }).fill('new')
  await page.locator('.inspiration-filters').getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.locator('.inspiration-results-meta')).toContainText('2 works')
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect.poll(() => Boolean(releaseNew)).toBe(true)
  const oldResponse = page.waitForResponse(response => response.url().includes('q=old') && response.url().includes('cursor='))
  releaseOld!(); await oldResponse
  await expect(page.getByRole('button', { name: 'Load more', exact: true })).toBeDisabled()
  releaseNew!()
  await expect(page.locator('.inspiration-card')).toHaveCount(2)
  await expect(page.locator('.inspiration-results-meta')).toContainText('2 works')
  await expect(page.locator('.catalog-pagination')).toHaveCount(0)
})

test('creator pagination is independent and restorable through URL', async ({ page }) => {
  const work = await (await page.request.get(`/api/v1/works/${workId}`)).json()
  const profile = await (await page.request.get(`/api/v1/creators/${work.author.handle}`)).json()
  await page.route('**/api/v1/creators/*', route => {
    const pageNumber = Number(new URL(route.request().url()).searchParams.get('worksPage') || 1)
    return route.fulfill({ json: { ...profile, worksTotal: 27, productsTotal: 0, products: [], worksPage: pageNumber, productsPage: 1, limit: 12, works: [{ ...work, title: `Portfolio page ${pageNumber}` }] } })
  })
  await page.goto(`/creators/${work.author.handle}`)
  const nav = page.getByRole('navigation', { name: 'Published works', exact: true })
  await expect(nav.getByRole('button', { name: 'Previous', exact: true })).toBeDisabled()
  await nav.getByRole('button', { name: 'Next', exact: true }).click()
  await expect(page).toHaveURL(/worksPage=2/)
  await expect(page.getByRole('heading', { name: 'Portfolio page 2' })).toBeVisible()
  await page.reload()
  await expect(nav).toContainText('Page 2')
  await nav.getByRole('button', { name: 'Next', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Portfolio page 3' })).toBeVisible()
  await expect(nav.getByRole('button', { name: 'Next', exact: true })).toBeDisabled()
})
