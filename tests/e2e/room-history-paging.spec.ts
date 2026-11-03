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
import { WsFrame } from '../../src/lib/api/generated/ws-schemas'
import { ensurePagingRoom, waitForOlderRequest, PAGING_ENTRY_COUNT, PAGING_NEWEST_TEXT, PAGING_OLDEST_TEXT } from './fixtures/room-history-paging'

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
  let openingSnapshot: Extract<WsFrame, { type: 'room_snapshot' }> | undefined
  const deliveredTexts = new Set<string>()
  page.on('websocket', (socket) => socket.on('framereceived', ({ payload }) => {
    const frame = WsFrame.parse(JSON.parse(payload.toString()))
    if (frame.type === 'room_snapshot' && frame.room_id === roomId && !openingSnapshot) openingSnapshot = frame
    if (frame.type === 'room_entry' && frame.room_id === roomId) deliveredTexts.add(frame.entry.text)
  }))
  // #1449 T-47: the shared virtual list keeps each server entry's identity,
  // but deliberately does not mount10,005 DOM rows at once.
  const log = page.getByRole('region', { name: 'Conversation history', exact: true })
  const pagingEntry = (entry: Pick<components['schemas']['RoomEntry'], 'id'>) => log.locator(`[data-message-id=${JSON.stringify(entry.id)}]`)
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
    await expect(log).toBeVisible({ timeout: 20_000 })
    // S-40/DS-9: real cap10,000, not a small mock snapshot. Five seed
    // messages are outside it; the first loaded one is seed6.
    await expect.poll(() => openingSnapshot?.entries.length).toBe(10_000)
    expect(openingSnapshot?.has_older).toBe(true)
    expect(openingSnapshot?.entries[0].client_message_id).toBe(`seed-${PAGING_ENTRY_COUNT - 10_000 + 1}`)
    const newest = pagingEntry(fixture.newest)
    await expect(newest).toHaveCount(1)
    await expect(newest.locator('p')).toHaveText(PAGING_NEWEST_TEXT)
    await expect(newest).toBeVisible({ timeout: 20_000 })
    await expect(newest.locator('p')).toBeVisible()
    await expect(newest).toBeInViewport()
    const hint = page.getByRole('button', { name: 'Show earlier messages', exact: true })
    await expect(hint).toBeVisible()
    expect(historyRequests, 'opening must not automatically walk older pages').toEqual([])

    // The helper arms the event waiter BEFORE scrolling. A fast request
    // emitted before scrollIntoViewIfNeeded resolves cannot be lost.
    const request = await waitForOlderRequest(page, roomId, async () => {
      await log.focus()
      await page.keyboard.press('Home') // Genuine reader intent, not a store edit.
      await log.evaluate((el) => { el.scrollTop = 0; el.dispatchEvent(new Event('scroll')) })
    })
    const cursor = Number(new URL(request.url()).searchParams.get('before_seq'))
    expect(cursor).toBeGreaterThan(fixture.oldest.seq)
    expect(cursor).toBeLessThanOrEqual(fixture.anchor.seq)
    expect(cursor, 'S-40: exact oldest loaded sequence, never seq-1 or the live high-water').toBe(openingSnapshot!.entries[0].seq)
    const limit = Number(new URL(request.url()).searchParams.get('limit'))
    expect(limit).toBeGreaterThanOrEqual(20)
    expect(limit).toBeLessThanOrEqual(100)
    await expect(page.getByRole('status').filter({ hasText: 'Loading earlier messages' })).toBeVisible()
    const anchor = pagingEntry(fixture.anchor)
    // The original seed20 anchor lies beyond the first virtual slice. A real
    // PageDown mounts it; waiting for an off-screen DOM row would never work.
    await log.focus()
    await page.keyboard.press('PageDown')
    await expect(anchor).toHaveCount(1)
    await anchor.scrollIntoViewIfNeeded()
    await expect(anchor).toBeVisible()
    await expect(anchor).toBeInViewport()
    const before = await anchor.boundingBox()
    const scrollBefore = await log.evaluate((el) => el.scrollTop)
    expect(before).not.toBeNull()
    expect(historyRequests).toHaveLength(1)

    releaseHistory()
    await expect(hint).toHaveCount(0)
    await expect(log).not.toHaveAttribute('aria-busy', 'true')
    const response = await request.response()
    expect(response?.status()).toBe(200)
    const earlier = (await response!.json()) as components['schemas']['RoomHistoryResponse']
    expect(earlier.has_older).toBe(false)
    expect(earlier.entries.map((entry) => entry.client_message_id)).toEqual(Array.from({ length: 5 }, (_, n) => `seed-${n + 1}`))
    expect(earlier.entries.every((entry) => entry.seq < cursor), 'the cursor is exclusive for every returned entry').toBe(true)
    const after = await anchor.boundingBox()
    expect(after).not.toBeNull()
    expect(Math.abs(after!.y - before!.y)).toBeLessThanOrEqual(2)
    const scrollAfter = await log.evaluate((el) => el.scrollTop)
    expect(scrollAfter).toBeGreaterThan(scrollBefore)
    expect(scrollAfter, 'prepend must not throw the reader to the tail').toBeLessThan(await log.evaluate((el) => el.scrollHeight / 2))
    await expect(anchor).toBeInViewport()
    expect(historyRequests).toHaveLength(1)

    // Virtual rows are mounted only near the reader. Move deliberately to
    // the first entry AFTER proving the unchanged prepend anchor, then retain
    // the original exact-count and visibility oracles (and add in-viewport).
    await log.focus()
    await page.keyboard.press('Home')
    await log.evaluate((el) => { el.scrollTop = 0; el.dispatchEvent(new Event('scroll')) })
    const oldest = pagingEntry(fixture.oldest).locator('p')
    await expect(oldest).toHaveCount(1)
    await expect(oldest).toHaveText(PAGING_OLDEST_TEXT)
    await expect(oldest).toBeVisible({ timeout: 20_000 })
    await expect(oldest).toBeInViewport()
    await oldest.hover()
    for (let i = 0; i < 4; i++) await page.mouse.wheel(0, -1_500)
    await page.waitForTimeout(500) // bounded observation of forbidden extra GETs
    expect(historyRequests, 'terminal top scrolling sends no further GET').toHaveLength(1)
    await log.focus()
    await page.keyboard.press('PageDown')
    await anchor.scrollIntoViewIfNeeded()
    await expect(anchor).toBeVisible()
    const tailBaseline = await log.evaluate((el) => el.scrollTop)
    const liveText = `pager-message-live-${Date.now()}`
    const live = await page.request.post(`/api/v1/rooms/${roomId}/messages`, {
      headers, data: { client_message_id: liveText, text: liveText, mentions: [] },
    })
    expect(live.ok(), `live message failed: ${live.status()}`).toBe(true)
    // Observe actual live delivery before judging the historical reader. A
    // virtualizer must not be forced to mount an off-screen live-tail row.
    await expect.poll(() => deliveredTexts.has(liveText)).toBe(true)
    await expect(anchor).toBeVisible()
    await expect(anchor).toBeInViewport()
    expect(Math.abs(await log.evaluate((el) => el.scrollTop) - tailBaseline)).toBeLessThanOrEqual(2)
    const latest = page.getByRole('button', { name: 'Return to latest', exact: true })
    await expect(latest).toBeVisible()
    await latest.click() // T-47/S-41: Return to latest FIRST, then inspect tail.
    await expect(log.getByText(liveText, { exact: true })).toBeVisible({ timeout: 20_000 })
    await expect(log.getByText(liveText, { exact: true })).toBeInViewport()
    await expect(latest).toHaveCount(0)
    expect(historyRequests, 'Return to latest uses the already loaded live tail').toHaveLength(1)
  } finally {
    releaseHistory() // failed assertions must not strand an intercepted response
  }
})
