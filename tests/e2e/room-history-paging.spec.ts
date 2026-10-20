/**
 * #1332 T23: real-browser prepend anchoring, terminal history and live tail.
 * The real attach cap is 10,000; use existing REST to retain 10,005 messages.
 * Seed while paused (FR-170) to avoid creating model turns for seed broadcasts.
 * Replacement retry workers reuse a complete base OR suffixed recovery room.
 * All entry selectors use server IDs, never substring text. No oracle is
 * weakened: oldest/newest are visible, the newest is initially in viewport,
 * and the anchored message/offset, request counts and live tail are checked.
 */
import { expect, test, type Page } from '@playwright/test'
import type { components } from '../../src/lib/api/generated/openapi-types'
import { ensurePagingRoom, pagingEntry, waitForOlderRequest, PAGING_NEWEST_TEXT } from './fixtures/room-history-paging'

type Workspace = components['schemas']['Workspace']
let fixture: Awaited<ReturnType<typeof ensurePagingRoom>>
let workspaceId: string

async function csrfHeaders(page: Page): Promise<Record<string, string>> {
  const cookies = await page.context().cookies()
  const csrf = cookies.find((c) => c.name === 'csrf' || c.name === '__Host-csrf')
  return csrf ? { 'X-CSRF-Token': csrf.value } : {}
}

test.beforeAll(async ({ browser }, testInfo) => {
  // Setup is separate from the restored 90s browser budget. Record actual
  // cold/reuse time, seed count and chosen room in CI, rather than claiming
  // beforeAll itself runs only once (Playwright restarts workers on retry).
  test.setTimeout(420_000)
  const context = await browser.newContext({
    storageState: testInfo.project.use.storageState,
    baseURL: testInfo.project.use.baseURL,
  })
  try {
    const page = await context.newPage()
    const headers = await csrfHeaders(page)
    const response = await page.request.get('/api/v1/workspaces', { headers })
    expect(response.ok(), `list workspaces failed: ${response.status()}`).toBe(true)
    const workspace = ((await response.json()) as Workspace[]).find((w) => w.is_default)
    expect(workspace?.id, 'a default workspace must exist').toBeTruthy()
    workspaceId = workspace!.id
    fixture = await ensurePagingRoom(page.request, workspaceId, headers)
    await testInfo.attach('paging-seed-receipt', {
      body: JSON.stringify({ retry: testInfo.retry, ...fixture }), contentType: 'application/json',
    })
  } finally {
    await context.close()
  }
})

test('scrolling up through paged history keeps the reader anchored and ends at the first entry', async ({ page }) => {
  test.setTimeout(90_000)
  const { roomId } = fixture
  const headers = await csrfHeaders(page)
  const historyRequests: string[] = []
  page.on('request', (request) => {
    if (request.method() === 'GET' && new URL(request.url()).pathname === `/api/v1/rooms/${roomId}/messages`) {
      historyRequests.push(request.url())
    }
  })
  let releaseHistory!: () => void
  const released = new Promise<void>((resolve) => { releaseHistory = resolve })
  await page.route(`**/api/v1/rooms/${roomId}/messages?*`, async (route) => {
    const response = await route.fetch()
    expect(response.ok(), 'the real older page must succeed').toBe(true)
    await released
    await route.fulfill({ response })
  })
  try {
    await page.goto(`/#/workspaces/${workspaceId}/rooms/${roomId}`)
    const log = page.getByRole('log', { name: 'Group conversation' })
    await expect(log).toBeVisible({ timeout: 20_000 })
    const newest = pagingEntry(page, fixture.newest)
    await expect(newest).toHaveCount(1)
    await expect(newest.locator('p')).toHaveText(PAGING_NEWEST_TEXT)
    await expect(newest).toBeVisible({ timeout: 20_000 })
    await expect(newest.locator('p')).toBeVisible()
    await expect(newest).toBeInViewport()
    const hint = page.getByRole('button', { name: "Earlier messages aren't shown", exact: true })
    await expect(hint).toBeVisible()
    expect(historyRequests, 'opening must not automatically walk older pages').toEqual([])

    // The helper arms the event waiter BEFORE scrolling. A fast request
    // emitted before scrollIntoViewIfNeeded resolves cannot be lost.
    const request = await waitForOlderRequest(page, roomId, () => hint.scrollIntoViewIfNeeded())
    const cursor = Number(new URL(request.url()).searchParams.get('before_seq'))
    expect(cursor).toBeGreaterThan(fixture.oldest.seq)
    expect(cursor).toBeLessThanOrEqual(fixture.anchor.seq)
    await expect(page.getByRole('status').filter({ hasText: 'Loading earlier messages' })).toBeVisible()
    const anchor = pagingEntry(page, fixture.anchor)
    await expect(anchor).toHaveCount(1)
    await anchor.scrollIntoViewIfNeeded()
    await expect(anchor).toBeVisible()
    await expect(anchor).toBeInViewport()
    const before = await anchor.boundingBox()
    const scrollBefore = await log.evaluate((el) => el.scrollTop)
    expect(before).not.toBeNull()
    expect(historyRequests).toHaveLength(1)

    releaseHistory()
    const oldest = pagingEntry(page, fixture.oldest).locator('p')
    await expect(oldest).toHaveCount(1)
    // Restore the original visibility oracle: attachment alone accepts hidden
    // content. CSS visibility does not scroll the reader away from the anchor.
    await expect(oldest).toBeVisible({ timeout: 20_000 })
    await expect(hint).toHaveCount(0)
    const after = await anchor.boundingBox()
    expect(after).not.toBeNull()
    expect(Math.abs(after!.y - before!.y)).toBeLessThanOrEqual(2)
    const scrollAfter = await log.evaluate((el) => el.scrollTop)
    expect(scrollAfter).toBeGreaterThan(scrollBefore)
    expect(scrollAfter, 'prepend must not throw the reader to the tail').toBeLessThan(await log.evaluate((el) => el.scrollHeight / 2))
    await expect(anchor).toBeInViewport()
    expect(historyRequests).toHaveLength(1)

    await anchor.hover()
    for (let i = 0; i < 4; i++) await page.mouse.wheel(0, -1_500)
    await page.waitForTimeout(500) // bounded observation of forbidden extra GETs
    expect(historyRequests, 'terminal top scrolling sends no further GET').toHaveLength(1)
    await anchor.scrollIntoViewIfNeeded()
    await expect(anchor).toBeVisible()
    const tailBaseline = await log.evaluate((el) => el.scrollTop)
    const liveText = `pager-message-live-${Date.now()}`
    const live = await page.request.post(`/api/v1/rooms/${roomId}/messages`, {
      headers, data: { client_message_id: liveText, text: liveText, mentions: [] },
    })
    expect(live.ok(), `live message failed: ${live.status()}`).toBe(true)
    await expect(log.getByText(liveText, { exact: true })).toBeVisible({ timeout: 20_000 })
    await expect(anchor).toBeVisible()
    await expect(anchor).toBeInViewport()
    expect(Math.abs(await log.evaluate((el) => el.scrollTop) - tailBaseline)).toBeLessThanOrEqual(2)
  } finally {
    releaseHistory() // failed assertions must not strand an intercepted response
  }
})
