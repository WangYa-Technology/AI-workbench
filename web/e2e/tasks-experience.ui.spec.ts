// Task lifecycle, privacy and recovery contracts against the production UI.
import { expect, test, type Page, type Route } from '@playwright/test'
const clientID = '00000000-0000-4000-8000-000000008101'
const creatorID = '00000000-0000-4000-8000-000000008102'
const firstID = '00000000-0000-4000-8000-000000008103'
const secondID = '00000000-0000-4000-8000-000000008104'
const person = (id: string) => ({ id, handle: `user_${id.slice(-4)}`, displayName: `User ${id.slice(-4)}` })
const user = (id: string) => ({ ...person(id), email: 'fixture@example.test', role: 'member', status: 'active', locale: 'en-US', timezone: 'UTC', permissions: [] })
const task = (id = firstID, overrides = {}) => ({
  id, title: `Review task ${id.slice(-4)}`, summary: 'Original brief with a clearly scoped image deliverable.',
  brief: 'A complete production brief requiring an original image and documented rights.',
  deliverableType: 'image', budgetCents: 10000, currency: 'USD', deadline: '2030-10-01T12:00:00Z',
  status: 'open', clientTimezone: 'UTC', allowDirectAccept: false, proposalCount: 0,
  client: person(clientID), createdAt: '2026-09-20T12:00:00Z', updatedAt: '2026-09-20T12:00:00Z',
  viewerRole: 'client', deliverables: ['Master image'], acceptanceRules: ['Clean original file'],
  rightsTerms: 'Campaign use for one year.', aiDisclosureRequirement: 'List sources and models.',
  allowDerivativeReuse: false, proposals: [], deliveries: [], events: [], ...overrides,
})
type MountedApp = HTMLElement & { __vue_app__: { config: { globalProperties: {
  $router: { push: (path: string) => Promise<unknown> },
  $pinia: { _s: Map<string, { ensure: (force: boolean) => Promise<unknown> }> },
} } } }
async function navigate(page: Page, path: string) {
  await page.evaluate(async path => { await (document.querySelector('#app') as MountedApp).__vue_app__.config.globalProperties.$router.push(path) }, path)
}
async function mockApp(page: Page, handler: (route: Route, url: URL) => Promise<void> | undefined, locale = 'en-US') {
  const unexpected: string[] = []
  await page.addInitScript(value => localStorage.setItem('hcai-locale', value), locale)
  await page.route('**/api/**', route => {
    const url = new URL(route.request().url())
    const handled = handler(route, url)
    if (handled) return handled
    const p = url.pathname
    if (p === '/api/v1/auth/session') return route.fulfill({ json: { user: { ...user(clientID), locale } } })
    if (p === '/api/v1/site-config') return route.fulfill({ json: { siteName: 'HCAI CHAT', serverUrl: 'http://localhost', siteIconUrl: '/brand/logo.png', footerText: { enUS: '', zhCN: '' }, policies: Object.fromEntries(['terms', 'privacy', 'cookies', 'acceptable', 'ai', 'licensing', 'refunds', 'copyright'].map(key => [key, { enUS: '', zhCN: '' }])) } })
    if (p === '/api/v1/notifications') return route.fulfill({ json: { items: [], unreadCount: 0 } })
    if (p === '/api/v1/meta') return route.fulfill({ json: { taskPaymentProvider: { enabled: true, provider: 'stripe', liveMode: false } } })
    if (p === '/api/v1/task-types') return route.fulfill({ json: { items: [{ code: 'image', nameZh: '图片', nameEn: 'Image', icon: 'image', scope: 'task' }] } })
    if (p === '/api/v1/account/payouts') return route.fulfill({ json: { status: 'verified', chargesEnabled: true, payoutsEnabled: true } })
    if (p === '/api/v1/assets') return route.fulfill({ json: { items: [], total: 0 } })
    if (p === `/api/v1/tasks/${firstID}` || p === `/api/v1/tasks/${secondID}`) return route.fulfill({ json: task(p.split('/').at(-1)) })
    unexpected.push(`${route.request().method()} ${p}`)
    return route.fulfill({ status: 500, json: { error: { code: 'unexpected_response', message: 'Unmocked fixture request' } } })
  })
  return unexpected
}

test('confirmed anonymous session clears private task evidence and role actions', async ({ page }) => {
  let signedIn = true
  let taskReads = 0
  const unexpected = await mockApp(page, (r, u) => {
    if (u.pathname === '/api/v1/auth/session') return r.fulfill({ json: { user: signedIn ? user(clientID) : null } })
    if (u.pathname === `/api/v1/tasks/${firstID}`) {
      taskReads++
      return r.fulfill({ json: task(firstID, signedIn ? {
        events: [{ id: 'private-event', kind: 'created', note: 'CONFIDENTIAL CLIENT NEGOTIATION', createdAt: '2026-09-20T12:00:00Z' }],
        proposals: [{ id: secondID, creator: person(creatorID), status: 'submitted', approach: 'CONFIDENTIAL CREATOR PROPOSAL', deliverables: 'Private proposed deliverables', amountCents: 9000, timelineDays: 7, createdAt: '2026-09-20T12:00:00Z' }],
      } : { viewerRole: 'viewer' }) })
    }
  })
  await page.goto(`/market/demands/${firstID}`)
  await expect(page.locator('.task-history')).toContainText('CONFIDENTIAL CLIENT NEGOTIATION')
  signedIn = false
  await page.evaluate(async () => { await (document.querySelector('#app') as MountedApp).__vue_app__.config.globalProperties.$pinia._s.get('session')!.ensure(true) })
  await expect(page.locator('.task-action-rail .market-auth-prompt')).toBeVisible()
  await expect(page.locator('.task-history')).toHaveCount(0)
  await expect(page.locator('.task-proposals')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Cancel task', exact: true })).toHaveCount(0)
  expect(taskReads).toBe(2)
  expect(unexpected).toEqual([])
})

test('all statuses survives navigation and reload in available view', async ({ page }) => {
  const requested: string[] = []
  const unexpected = await mockApp(page, (r, u) => {
    if (u.pathname === '/api/v1/tasks') {
      requested.push(u.searchParams.get('status') || 'ALL')
      return r.fulfill({ json: { items: [task()], total: 1, typeCounts: { image: 1 } } })
    }
  })
  await page.goto('/market/demands?status=assigned')
  await expect(page.locator('.task-row')).toHaveCount(1)
  const trigger = page.getByRole('combobox', { name: 'All statuses', exact: true })
  await trigger.click()
  await page.getByRole('option', { name: 'All statuses', exact: true }).click()
  await expect.poll(() => requested.at(-1)).toBe('ALL')
  await expect(trigger).toHaveText('All statuses')
  expect(requested).toEqual(['assigned', 'ALL'])
  await page.reload()
  await expect(trigger).toHaveText('All statuses')
  await expect.poll(() => requested.at(-1)).toBe('ALL')
  expect(unexpected).toEqual([])
})

test('delivery assets paginate, preserve selection on error and retry the same cursor', async ({ page }) => {
  const queries: string[] = []
  let failMore = true
  const asset = (id: string) => ({ id, title: `Owned file ${id}`, scanStatus: 'clean', sourceType: 'upload', licenseCode: 'original', kind: 'image' })
  const unexpected = await mockApp(page, (r, u) => {
    if (u.pathname === `/api/v1/tasks/${firstID}`) return r.fulfill({ json: task(firstID, { status: 'assigned', viewerRole: 'assignee', assignee: person(creatorID) }) })
    if (u.pathname === '/api/v1/assets') {
      expect(u.searchParams.get('purpose')).toBe('task_delivery')
      queries.push(u.searchParams.get('cursor') || '')
      if (u.searchParams.has('cursor') && failMore) return r.fulfill({ status: 503, json: { error: { code: 'unexpected_response' } } })
      return r.fulfill({ json: u.searchParams.has('cursor')
        ? { items: [asset(firstID), asset(secondID)], total: 21 }
        : { items: Array.from({ length: 20 }, (_, i) => asset(i ? `first-${i}` : firstID)), total: 21, nextCursor: 'older' } })
    }
  })
  await page.goto(`/market/demands/${firstID}`)
  await expect(page.locator('.delivery-submit-form .ui-select-root option')).toHaveCount(20)
  await page.getByRole('button', { name: 'Load more delivery assets', exact: true }).click()
  await expect(page.locator('#task-participation [role="alert"]')).toBeVisible()
  await expect(page.locator('.delivery-submit-form select')).toHaveValue(firstID)
  failMore = false
  await page.locator('#task-participation [role="alert"]').getByRole('button', { name: 'Try again' }).click()
  await expect(page.locator('.delivery-submit-form .ui-select-root option')).toHaveCount(21)
  await expect(page.locator('#task-participation [role="alert"]')).toHaveCount(0)
  expect(queries).toEqual(['', 'older', 'older'])
  expect(unexpected).toEqual([])
})

for (const place of ['workspace', 'market']) {
  test(`${place} pagination failure keeps cards and retries the failed page`, async ({ page }) => {
    let failed = false
    let reads = 0
    const unexpected = await mockApp(page, (r, u) => {
      if (u.pathname === '/api/v1/tasks') {
        reads++
        if (u.searchParams.has('cursor') && !failed) {
          failed = true
          return r.fulfill({ status: 503, json: { error: { code: 'unexpected_response', message: 'Next page temporarily unavailable', retryable: true } } })
        }
        return r.fulfill({ json: u.searchParams.has('cursor') ? { items: [task(), task(secondID)], total: 2, typeCounts: { image: 2 } } : { items: [task()], total: 2, typeCounts: { image: 2 }, nextCursor: 'second-page' } })
      }
    })
    await page.goto(place === 'workspace' ? '/workspace/tasks' : '/market/demands')
    const cards = page.locator(place === 'workspace' ? '.workspace-task-card' : '.task-row')
    await expect(cards).toHaveCount(1)
    await page.getByRole('button', { name: 'Load more', exact: true }).click()
    await expect(page.getByRole('alert')).toContainText('The service returned an unexpected response.')
    await expect(cards).toHaveCount(1)
    await page.getByRole('button', { name: 'Try again', exact: true }).click()
    await expect(cards).toHaveCount(2)
    await expect(page.getByRole('alert')).toHaveCount(0)
    expect(reads).toBe(3)
    expect(failed).toBe(true)
    expect(unexpected).toEqual([])
  })
}

test('late mutation cannot lock the destination task or keep its old form', async ({ page }) => {
  let held: Route | undefined
  const unexpected = await mockApp(page, (r, u) => {
    if (u.pathname === `/api/v1/tasks/${firstID}/cancel`) { held = r; return Promise.resolve() }
  })
  await page.goto(`/market/demands/${firstID}`)
  await page.getByRole('button', { name: 'Cancel task', exact: true }).click()
  await page.locator('#task-participation textarea').fill('Cancel this task with a clear documented reason.')
  await page.locator('#task-participation button[type="submit"]').click()
  await expect.poll(() => Boolean(held)).toBe(true)
  await navigate(page, `/market/demands/${secondID}`)
  await expect(page.locator('.task-brief-header')).toContainText('Review task 8104')
  const finished = page.waitForResponse(r => r.url().endsWith(`/tasks/${firstID}/cancel`))
  await held!.fulfill({ json: task(firstID, { status: 'cancelled' }) })
  await (await finished).finished()
  await expect(page.locator('#task-participation textarea')).toHaveCount(0)
  const cancel = page.getByRole('button', { name: 'Cancel task', exact: true })
  await expect(cancel).toBeEnabled()
  await cancel.click()
  await expect(page.locator('#task-participation textarea')).toHaveValue('')
  await expect(page.locator('#task-participation button[type="submit"]')).toBeEnabled()
  expect(unexpected).toEqual([])
})

test('workspace cards distinguish task budgets from earnings without aggregate stats', async ({ page }) => {
  const unexpected = await mockApp(page, (r, u) => {
    if (u.pathname === '/api/v1/tasks') return r.fulfill({ json: u.searchParams.has('cursor')
      ? { items: [task(secondID)], total: 2, typeCounts: { image: 2 } }
      : { items: [task()], total: 2, typeCounts: { image: 2 }, nextCursor: 'second-page' } })
  })
  await page.goto('/workspace/tasks')
  await expect(page.locator('.page-hero-stats')).toHaveCount(0)
  await expect(page.locator('.workspace-task-count')).toHaveText('2 tasks')
  await expect(page.locator('.workspace-task-budget')).toContainText('$100.00')
  await expect(page.locator('.tasks-workspace')).toContainText('not earnings, amounts due, or settlements')
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect(page.locator('.workspace-task-budget')).toHaveCount(2)
  await expect(page.locator('.workspace-task-count')).toHaveText('2 tasks')
  expect(unexpected).toEqual([])
})

async function refreshSession(page: Page) {
  await page.evaluate(async () => { await (document.querySelector('#app') as MountedApp).__vue_app__.config.globalProperties.$pinia._s.get('session')!.ensure(true) })
}
async function settle(page: Page, route: Route, json: unknown, status = 200) {
  const done = page.waitForResponse(r => r.url() === route.request().url())
  await route.fulfill({ json, status })
  await (await done).finished()
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
}

test('a late private read cannot restore a preceding account after switching users', async ({ page }) => {
  let actor = clientID
  let held: Route | undefined
  const unexpected = await mockApp(page, (r, u) => {
    if (u.pathname === '/api/v1/auth/session') return r.fulfill({ json: { user: user(actor) } })
    if (u.pathname === `/api/v1/tasks/${firstID}`) {
      if (actor === clientID) { held = r; return Promise.resolve() }
      return r.fulfill({ json: task(firstID, { viewerRole: 'viewer' }) })
    }
  })
  await page.goto(`/market/demands/${firstID}`)
  await expect.poll(() => Boolean(held)).toBe(true)
  actor = creatorID
  await refreshSession(page)
  await expect(page.getByRole('button', { name: 'Submit proposal', exact: true })).toBeVisible()
  await settle(page, held!, task(firstID, { events: [{ id: 'private', kind: 'created', note: 'OLD PRIVATE EVIDENCE', createdAt: '2026-09-20T12:00:00Z' }] }))
  await expect(page.locator('.task-history')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Cancel task', exact: true })).toHaveCount(0)
  expect(unexpected).toEqual([])
})

for (const status of [401, 403, 404]) {
  test(`funding refresh ${status} clears the preceding private task context`, async ({ page }) => {
    let revoked = false
    const unexpected = await mockApp(page, (r, u) => {
      if (u.pathname === `/api/v1/tasks/${firstID}`) return revoked
        ? r.fulfill({ status, json: { error: { code: 'task_not_found' } } })
        : r.fulfill({ json: task(firstID, { funding: { status: 'paid', amountCents: 10000, currency: 'USD', paymentMode: 'stripe', updatedAt: '2026-09-20T12:00:00Z' }, events: [{ id: 'private', kind: 'created', note: 'PRIVATE EVIDENCE', createdAt: '2026-09-20T12:00:00Z' }] }) })
    })
    await page.goto(`/market/demands/${firstID}`)
    await expect(page.locator('.task-history')).toContainText('PRIVATE EVIDENCE')
    revoked = true
    await page.getByRole('button', { name: 'Refresh funding status', exact: true }).click()
    await expect(page.getByRole('alert')).toBeVisible()
    await expect(page.locator('.task-history')).toHaveCount(0)
    await expect(page.locator('.task-detail-layout')).toHaveCount(0)
    expect(unexpected).toEqual([])
  })
}

for (const eligible of [true, false]) {
  test(`a linked asset outside the first page is validated: eligible=${eligible}`, async ({ page }) => {
    const unexpected = await mockApp(page, (r, u) => {
      if (u.pathname === `/api/v1/tasks/${firstID}`) return r.fulfill({ json: task(firstID, { status: 'assigned', viewerRole: 'assignee', assignee: person(creatorID), deliveryKind: 'image', deliverableType: 'brand_image' }) })
      if (u.pathname === `/api/v1/assets/${secondID}`) return r.fulfill({ json: { id: secondID, title: 'Linked original', kind: 'image', scanStatus: 'clean', sourceType: 'upload', licenseCode: 'original', taskDeliveryEligible: eligible } })
    })
    await page.goto(`/market/demands/${firstID}?assetId=${secondID}`)
    const submit = page.locator('.delivery-submit-form button[type="submit"]')
    if (eligible) {
      await expect(page.locator('.delivery-submit-form select')).toHaveValue(secondID)
      await expect(submit).toBeEnabled()
    } else {
      await expect(page.locator('#task-participation [role="alert"]')).toContainText('not eligible')
      await expect(submit).toBeDisabled()
    }
    await expect(page.getByRole('link', { name: 'Create for this task', exact: true })).toHaveAttribute('href', `/create/image?taskId=${firstID}`)
    expect(unexpected).toEqual([])
  })
}

test('asset search preserves selected files, caps the bundle at twenty and submits the selection', async ({ page }) => {
  let posted: { assetIds: string[] } | undefined
  const asset = (i: number) => ({ id: `00000000-0000-4000-8000-${String(8200+i).padStart(12, '0')}`, title: `Original ${i}`, scanStatus: 'clean', sourceType: 'upload', licenseCode: 'original', kind: 'image' })
  const unexpected = await mockApp(page, (r, u) => {
    if (u.pathname === `/api/v1/tasks/${firstID}`) return r.fulfill({ json: task(firstID, { status: 'assigned', viewerRole: 'assignee', assignee: person(creatorID) }) })
    if (u.pathname === `/api/v1/tasks/${firstID}/deliveries`) {
      posted = r.request().postDataJSON()
      return r.fulfill({ json: task(firstID, { status: 'submitted', viewerRole: 'assignee' }) })
    }
    if (u.pathname === '/api/v1/assets') return r.fulfill({ json: { items: u.searchParams.get('q') ? [asset(30)] : Array.from({ length: 21 }, (_, i) => asset(i)), total: 21 } })
  })
  await page.goto(`/market/demands/${firstID}`)
  const boxes = page.locator('.delivery-submit-form fieldset input[type="checkbox"]')
  await expect(boxes).toHaveCount(20)
  for (let i=0;i<19;i++) await boxes.nth(i).check()
  await expect(boxes.nth(19)).toBeDisabled()
  await page.getByLabel('Search delivery assets', { exact: true }).fill('older')
  await page.locator('.delivery-asset-search button[type="submit"]').click()
  await expect(page.locator('.delivery-submit-form select')).toHaveValue(asset(0).id)
  await expect(page.locator('.delivery-submit-form input[type="checkbox"]:checked')).toHaveCount(19)
  await page.getByLabel('Delivery note', { exact: true }).fill('Complete original files for the agreed brief.')
  await page.getByLabel('Source and license evidence', { exact: true }).fill('All files are original and licensed for the campaign.')
  await page.getByLabel('AI models and source disclosure', { exact: true }).fill('Sources and models documented in the attached notes.')
  await page.getByRole('checkbox', { name: 'I confirm the delivery meets the task rights terms and all sources are disclosed.' }).check()
  await page.locator('.delivery-submit-form button[type="submit"]').click()
  await expect.poll(() => posted?.assetIds.length).toBe(20)
  expect(new Set(posted?.assetIds).size).toBe(20)
  expect(unexpected).toEqual([])
})

for (const locale of ['en-US', 'zh-CN']) {
  test(`task read failure shows unknown counts and retries without an empty state: ${locale}`, async ({ page }) => {
    let fail = true
    const unexpected = await mockApp(page, (r, u) => {
      if (u.pathname === '/api/v1/tasks') return fail
        ? r.fulfill({ status: 503, json: { error: { code: 'unexpected_response' } } })
        : r.fulfill({ json: { items: [], total: 0, typeCounts: {} } })
    }, locale)
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/workspace/tasks')
    await expect(page.locator('.workspace-page [role="alert"]')).toBeVisible()
    await expect(page.locator('.workspace-task-count')).toHaveText('—')
    await expect(page.locator('.task-market-empty')).toHaveCount(0)
    fail = false
    await page.getByRole('button', { name: locale === 'zh-CN' ? '重试' : 'Try again', exact: true }).click()
    await expect(page.locator('.task-market-empty')).toBeVisible()
    await expect(page.locator('.tasks-workspace')).toContainText(locale === 'zh-CN' ? '不代表收入' : 'not earnings')
    expect(unexpected).toEqual([])
  })
}

for (const width of [1551, 390]) {
  test(`header task search preserves filters and follows history at ${width}px`, async ({ page }) => {
    const requested: URL[] = []
    const unexpected = await mockApp(page, (r, u) => {
      if (u.pathname === '/api/v1/tasks') {
        requested.push(u)
        return r.fulfill({ json: { items: [], total: 0, typeCounts: {} } })
      }
    })
    await page.setViewportSize({ width, height: 901 })
    await page.goto('/market/demands?view=mine&status=&type=image&sort=budget_desc&q=original')
    const input = page.locator('.global-search-input')
    const scope = page.getByRole('combobox', { name: 'Search scope' })
    if (width < 1024) {
      await expect(input).not.toBeVisible()
      await page.getByRole('button', { name: 'Open search', exact: true }).click()
      await expect(input).toBeFocused()
    }
    await expect(scope).toHaveText('Tasks')
    await expect(input).toHaveValue('original')
    await expect(page.locator('.task-toolbar input[type="search"]')).toHaveCount(0)
    await input.fill('图')
    await input.press('Enter')
    await expect.poll(() => requested.at(-1)?.searchParams.get('q')).toBe('图')
    for (const [key, value] of Object.entries({ view: 'mine', status: '', type: 'image', sort: 'budget_desc', q: '图' })) {
      expect(new URL(page.url()).searchParams.get(key)).toBe(value)
    }
    await expect(page.locator('.catalog-keyword')).toContainText('图')
    await page.getByRole('button', { name: 'Clear search keyword', exact: true }).click()
    await expect(input).toHaveValue('')
    expect(new URL(page.url()).searchParams.get('status')).toBe('')
    expect(new URL(page.url()).searchParams.get('q')).toBeNull()
    await page.goBack()
    await expect(input).toHaveValue('图')
    await page.reload()
    if (width < 1024) await page.getByRole('button', { name: 'Open search', exact: true }).click()
    await expect(input).toHaveValue('图')
    await input.fill('')
    await input.press('Enter')
    await expect(page.locator('.catalog-keyword')).toHaveCount(0)
    expect(new URL(page.url()).searchParams.get('sort')).toBe('budget_desc')
    if (width < 1024) {
      await input.press('Escape')
      await expect(input).not.toBeVisible()
      await expect(page.getByRole('button', { name: 'Open search', exact: true })).toBeFocused()
    }
    expect(unexpected).toEqual([])
  })
}

test('header search explicitly switches to full site and validates Unicode length', async ({ page }) => {
  const unexpected = await mockApp(page, (r, u) => {
    if (u.pathname === '/api/v1/tasks') return r.fulfill({ json: { items: [], total: 0, typeCounts: {} } })
    if (u.pathname === '/api/v1/search') return r.fulfill({ json: { items: [], total: 0, page: 1, totalPages: 1 } })
  })
  await page.goto('/market/demands?status=open')
  const input = page.locator('.global-search-input')
  const scope = page.getByRole('combobox', { name: 'Search scope' })
  await scope.click()
  await page.getByRole('option', { name: 'All content', exact: true }).click()
  await input.fill('😀')
  await input.press('Enter')
  await expect(page.locator('#header-search-error')).toContainText('2–120')
  expect(new URL(page.url()).pathname).toBe('/market/demands')
  await input.fill('a'.repeat(121))
  await input.press('Enter')
  await expect(page.locator('#header-search-error')).toBeVisible()
  await input.fill('海报')
  await input.press('Enter')
  await expect(page).toHaveURL(/\/search\?q=/)
  expect(new URL(page.url()).searchParams.get('q')).toBe('海报')
  await expect(scope).toHaveCount(0)
  await page.goBack()
  await expect(scope).toHaveText('Tasks')
  await expect(input).toHaveValue('')
  await navigate(page, `/market/demands/${firstID}`)
  await expect(scope).toHaveCount(0)
  expect(unexpected).toEqual([])
})

test('guest task search uses the same scoped entry', async ({ page }) => {
  const unexpected = await mockApp(page, (r, u) => {
    if (u.pathname === '/api/v1/auth/session') return r.fulfill({ json: { user: null } })
    if (u.pathname === '/api/v1/tasks') return r.fulfill({ json: { items: [], total: 0, typeCounts: {} } })
  })
  await page.goto('/market/demands?status=open')
  await expect(page.getByRole('combobox', { name: 'Search scope' })).toHaveText('Tasks')
  await page.locator('.global-search-input').fill('poster')
  await page.locator('.global-search-input').press('Enter')
  await expect(page.locator('.catalog-keyword')).toContainText('poster')
  expect(new URL(page.url()).pathname).toBe('/market/demands')
  expect(unexpected).toEqual([])
})

test('switching marketplace views keeps the sticky toolbar stable during loading', async ({ page }) => {
  await page.setViewportSize({ width: 1281, height: 891 })
  let release: (() => void) | undefined
  const pending = new Promise<void>(resolve => { release = resolve })
  let hold = false
  await mockApp(page, (r, u) => {
    if (u.pathname === '/api/v1/tasks') return (async () => {
      if (hold) await pending
      await r.fulfill({ json: { items: Array.from({ length: 12 }, (_, i) => task(`00000000-0000-4000-8000-${String(8200 + i).padStart(12, '0')}`)), total: 12, typeCounts: { image: 12 } } })
    })()
  })
  await page.goto('/market/demands')
  await expect(page.locator('.task-row')).toHaveCount(12)
  await page.locator('#main-content').evaluate(el => { el.scrollTop = 1200 })
  await expect(page.locator('.catalog-sticky-bar')).toHaveClass(/is-stuck/)
  await expect.poll(() => page.locator('.task-view-tabs').evaluate(el => el.getBoundingClientRect().height)).toBe(40)
  const before = await page.locator('.catalog-sticky-bar').boundingBox()
  const scrollBefore = await page.locator('#main-content').evaluate(el => el.scrollTop)
  hold = true
  await page.getByRole('tab').last().click()
  await expect(page.locator('.task-results')).toHaveAttribute('aria-busy', 'true')
  const frames = await page.evaluate(async () => {
    const samples: Array<{ height: number; top: number; stuck: boolean }> = []
    const start = performance.now()
    while (performance.now() - start < 400) {
      await new Promise(requestAnimationFrame)
      const el = document.querySelector('.catalog-sticky-bar')!
      const box = el.getBoundingClientRect()
      samples.push({ height: box.height, top: box.top, stuck: el.classList.contains('is-stuck') })
    }
    return samples
  })
  release!()
  expect(frames.every(frame => frame.stuck && Math.abs(frame.height - before!.height) < 1 && Math.abs(frame.top - before!.y) < 1)).toBe(true)
  await expect(page.locator('.task-results')).toHaveAttribute('aria-busy', 'false')
  await expect.poll(() => page.locator('#main-content').evaluate(el => el.scrollTop)).toBe(scrollBefore)
  await expect(page.locator('.task-view-tabs')).toContainText('12')
})

for (const width of [390, 1289]) test(`workspace task catalog filters preserve private scope and pagination at ${width}px`, async ({ page }, info) => {
  const requested: URL[] = []
  let delayed: Route | undefined
  let hold = false
  const unexpected = await mockApp(page, (r, u) => {
    if (u.pathname === '/api/v1/tasks') {
      requested.push(u)
      expect(u.searchParams.get('mine')).toBe('true')
      if (hold) { delayed = r; return Promise.resolve() }
      const items = u.searchParams.get('q') === 'missing' ? [] : [task(u.searchParams.has('cursor') ? secondID : firstID)]
      return r.fulfill({ json: { items, total: items.length ? 2 : 0, nextCursor: items.length && !u.searchParams.has('cursor') ? 'older' : undefined } })
    }
  })
  await page.setViewportSize({ width, height: 891 })
  await page.goto('/workspace/tasks?type=image&status=open&sort=deadline')
  await expect(page.locator('.workspace-task-card')).toHaveCount(1)
  await expect(page.locator('.page-hero-stats, main [role=tablist], .workspace-switcher')).toHaveCount(0)
  const input = page.getByRole('searchbox', { name: 'Search tasks you commission, accept or propose on', exact: true })
  if (width < 768) await page.locator('#mobile-search-toggle').click()
  await input.fill('Review')
  await input.press('Enter')
  await expect.poll(() => requested.at(-1)?.searchParams.get('q')).toBe('Review')
  if (width < 768) await input.press('Escape')
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect(page.locator('.workspace-task-card')).toHaveCount(2)
  expect(Object.fromEntries(requested.at(-1)!.searchParams)).toMatchObject({ mine: 'true', q: 'Review', type: 'image', status: 'open', sort: 'deadline', cursor: 'older' })
  await page.getByRole('button', { name: 'Grid view', exact: true }).click()
  await expect(page.locator('.workspace-task-list')).toHaveClass(/is-grid/)
  expect(await page.locator('#main-content').evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true)
  await info.attach(`workspace-tasks-${width}.png`, { body: await page.screenshot(), contentType: 'image/png' })
  // A filter reload keeps the previous results height until the read resolves.
  const oldHeight = await page.locator('.tasks-workspace .catalog-results').evaluate(el => el.getBoundingClientRect().height)
  hold = true
  await page.getByRole('combobox', { name: 'All statuses', exact: true }).click()
  await page.getByRole('option', { name: 'Assigned', exact: true }).click()
  await expect.poll(() => Boolean(delayed)).toBe(true)
  expect(await page.locator('.tasks-workspace .catalog-results').evaluate(el => el.getBoundingClientRect().height)).toBeGreaterThanOrEqual(oldHeight - 1)
  hold = false
  await delayed!.fulfill({ json: { items: [task(firstID, { status: 'assigned' })], total: 1 } })
  await expect(page.locator('.workspace-task-card')).toHaveAttribute('data-status', 'assigned')
  await page.reload()
  await expect(page.locator('.workspace-task-toolbar').getByRole('combobox', { name: 'All statuses', exact: true })).toContainText('Assigned')
  if (width < 768) await page.locator('#mobile-search-toggle').click()
  await input.fill('missing'); await input.press('Enter')
  if (width < 768) await input.press('Escape')
  await expect(page.getByText('No matching tasks', { exact: true })).toBeVisible()
  await page.goBack()
  await expect(page.locator('.workspace-task-card')).toHaveCount(1)
  await page.locator('.workspace-task-toolbar').getByRole('button', { name: 'Clear filters', exact: true }).click()
  await expect(page).toHaveURL(/\/workspace\/tasks$/)
  expect(unexpected).toEqual([])
})
