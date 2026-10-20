/**
 * room-history-paging.spec.ts — #1332 T23, the browser-only clause.
 *
 * Oracle: /Users/danielpiatkowski/coordination/designs/ROOM-HISTORY-PAGING-1332-20261018.md
 * row T23: "Successful prepend preserves the visible message and offset;
 * live tail still works. At terminal false, hint disappears and further top
 * scrolling issues no request." Scroll anchoring across a prepend cannot be
 * observed in jsdom (no layout), so this one clause runs in a real browser.
 * The request-level flow (one request, oldest-seq cursor, default limit,
 * terminal hint removal, no mount walk) is pinned at unit/component level in
 * src/components/rooms/RoomScreen.history.test.tsx.
 *
 * Fixture facts (verified against the sources):
 * - A room needs TWO agent members (contracts/components/schemas/
 *   RoomCreateRequest.yaml::agent_ids minItems 2; pkg/rooms/admin.go::Service.Create
 *   refuses a one-agent room with 422 "a room needs at least two agents").
 * - Seed bodies carry the required `mentions` field (RoomMessageRequest.yaml;
 *   pkg/gateway/rest_rooms.go::postRoomMessage refuses a missing one with 400).
 * - The "Earlier messages aren't shown" hint is driven by the ATTACH
 *   snapshot's has_older, which pkg/rooms/attach.go::Service.snapshot sets
 *   only when the room holds more than maxSnapshotEntries
 *   (pkg/rooms/types.go, 10,000) entries — and the attach rides the
 *   WebSocket, which cannot be stubbed at the network layer, so there is no
 *   smaller honest route: the room really is seeded past the cap.
 *
 * Seeding happens ONCE per run in test.beforeAll, not per attempt: the seed
 * is the expensive phase (10,005 sequential posts), and Playwright's CI
 * retries re-run the whole test — per-test seeding made every retry pay the
 * seed again inside its own timeout and the attempt cascade red. The room is
 * created under a FIXED name and reused when its seed is complete (the
 * newest entry is the last seed or a later live message); a room whose seed
 * died mid-way is left behind and a fresh uniquely-named room is seeded
 * instead, so a partial seed can never be mistaken for a complete one.
 * Reused rooms may carry a few extra tail entries from earlier attempts, so
 * the position anchor sits well inside the attach window (seed 20, not the
 * window edge), and the terminal assertion (seed 1 arrives with the one
 * below-window page, default limit 100) tolerates that drift too.
 *
 * The reader's position is asserted, not assumed, and the older response is
 * HELD until the pre-load baseline is captured: the anchor message and the
 * log's scrollTop are recorded while the response is paused, then the page is
 * released, painted, and the same message must sit at the same viewport
 * offset with the scroll offset moved down by the prepended block (T23's
 * "preserves the visible message and offset"). The prepend is observed
 * through the real history request (cursor, one page, then no further
 * request after the hint clears), and the live tail is proven by a message
 * that arrives while the reader sits at the top.
 *
 * CI only — never run locally. Shard: stubs-1.
 */
import { expect, test, type Page } from '@playwright/test'

const ENTRY_COUNT = 10_005 // attach cap (10,000) + 5 entries below the window
const ROOM_NAME = 'Paging-history-replay'
// The anchor sits well inside the attach window: reused rooms carry a few
// extra tail entries from earlier attempts (one live message per attempt),
// which move the window's oldest entry forward by that many, and the position
// assertions compare the log's scrollTop against half its scrollHeight —
// both hold only for an anchor above the middle of the content.
const ANCHOR_TEXT = 'pager-message-20'
const OLDEST_TEXT = 'pager-message-1'

async function csrfHeaders(page: Page): Promise<Record<string, string>> {
  const cookies = await page.context().cookies()
  const csrf = cookies.find((c) => c.name === 'csrf' || c.name === '__Host-csrf')
  return csrf ? { 'X-CSRF-Token': csrf.value } : {}
}

let workspaceId = ''
let roomId = ''

test.beforeAll(async ({ browser }) => {
  // The seed dominates the run (10,005 sequential posts): give the hook its
  // own budget so one slow-but-succeeding seed is not killed at the default.
  test.setTimeout(420_000)

  const project = test.info().project.use as { storageState?: string; baseURL?: string }
  const context = await browser.newContext({
    storageState: project.storageState,
    baseURL: project.baseURL,
  })
  const page = await context.newPage()
  const headers = await csrfHeaders(page)

  const wsList = await page.request.get('/api/v1/workspaces', { headers })
  expect(wsList.ok()).toBeTruthy()
  const def = ((await wsList.json()) as Array<{ id?: string; is_default?: boolean }>).find((w) => w.is_default === true)
  const id = def?.id
  expect(id, 'no default workspace').toBeTruthy()
  workspaceId = id!

  // Create-or-reuse: a room from a previous attempt (or a crashed one) is
  // reused only when its seed is complete, so retries never re-seed and a
  // partial seed is never mistaken for a working one.
  const list = await page.request.get(`/api/v1/workspaces/${workspaceId}/rooms`, { headers })
  expect(list.ok(), `list rooms failed: ${list.status()}`).toBeTruthy()
  const existing = ((await list.json()) as Array<{ id: string; name?: string }>).find(
    (r) => r.name === ROOM_NAME,
  )
  if (existing) {
    const probe = await page.request.get(`/api/v1/rooms/${existing.id}/messages?limit=1`, { headers })
    expect(probe.ok(), `seed-completeness probe failed: ${probe.status()}`).toBeTruthy()
    const newest = ((await probe.json()) as { entries: Array<{ text?: string }> }).entries[0]?.text ?? ''
    if (newest === `pager-message-${ENTRY_COUNT}` || newest.startsWith('pager-message-live')) {
      roomId = existing.id
      await context.close()
      return
    }
    // A seed that died mid-way: never reuse it — seed a fresh, uniquely
    // named room instead (the partial room is left in the disposable CI home).
  }

  const create = await page.request.post(`/api/v1/workspaces/${workspaceId}/rooms`, {
    headers,
    data: {
      name: existing ? `${ROOM_NAME}-${Date.now()}` : ROOM_NAME,
      kind: 'adhoc',
      agent_ids: ['mia', 'jim'],
    },
  })
  expect(create.ok(), `create room failed: ${create.status()}`).toBeTruthy()
  roomId = ((await create.json()) as { id: string }).id

  // Seed past the attach cap, strictly in order so seq order matches seed
  // order: the window then shows the newest ENTRY_COUNT - 10,000 entries, and
  // everything below it arrives with the one older history page.
  for (let i = 1; i <= ENTRY_COUNT; i++) {
    const sent = await page.request.post(`/api/v1/rooms/${roomId}/messages`, {
      headers,
      data: { client_message_id: `seed-${i}`, text: `pager-message-${i}`, mentions: [] },
    })
    expect(sent.ok(), `seed ${i} failed: ${sent.status()}`).toBeTruthy()
  }
  await context.close()
})

test('scrolling up through paged history keeps the reader anchored and ends at the first entry', async ({ page }) => {
  test.setTimeout(240_000) // the walk and its waits; the seed ran in beforeAll
  const headers = await csrfHeaders(page)

  // Observe the real paging requests instead of sleeping: every history page
  // the SPA fetches lands here with its cursor. Seed POSTs to the same path
  // carry no query string and are never recorded.
  const historyRequests: string[] = []
  page.on('request', (req) => {
    if (req.method() === 'GET' && req.url().includes(`/api/v1/rooms/${roomId}/messages`)) {
      historyRequests.push(req.url())
    }
  })

  // Hold the REAL older page: fetch it when the SPA asks, but do not hand it
  // to the browser until the pre-load position baseline has been captured.
  let releaseHistory: () => void = () => {}
  const historyReleased = new Promise<void>((resolve) => {
    releaseHistory = resolve
  })
  let olderRequestFired: (() => void) | null = null
  const olderRequested = new Promise<void>((resolve) => {
    olderRequestFired = resolve
  })
  let olderRequestUrl: string | null = null
  await page.route(`**/api/v1/rooms/${roomId}/messages?*`, async (route) => {
    const response = await route.fetch()
    olderRequestUrl = route.request().url()
    olderRequestFired?.()
    await historyReleased
    await route.fulfill({ response })
  })

  await page.goto(`/#/workspaces/${workspaceId}/rooms/${roomId}`)
  const log = page.getByRole('log', { name: 'Group conversation' })
  await expect(log).toBeVisible({ timeout: 20_000 })
  // The newest window carries the hint: older history exists above the cap.
  const hint = page.getByText(/Earlier messages aren't shown/)
  await expect(hint).toBeVisible()

  // Walk to the top: the hint entering view is what triggers the older page.
  // The response is held, so nothing has merged yet.
  await hint.scrollIntoViewIfNeeded()
  await page.waitForRequest(
    (req) => req.method() === 'GET' && req.url().includes(`/api/v1/rooms/${roomId}/messages`) && req.url().includes('before_seq='),
    { timeout: 30_000 },
  )
  await olderRequested
  const anchor = page.getByText(ANCHOR_TEXT)
  await expect(anchor).toBeVisible({ timeout: 20_000 })

  // Pre-load baseline: the visible message's viewport offset and the log's
  // scroll offset, captured while the older page is still in flight.
  const anchorBefore = await anchor.boundingBox()
  const scrollTopBefore = await log.evaluate((el) => el.scrollTop)
  expect(anchorBefore).not.toBeNull()

  // Release, paint, and re-assert the position (T23's clause): the SAME
  // message at the SAME viewport offset, the scroll offset moved DOWN by the
  // prepended block (a real prepend happened above), and the reader was not
  // thrown to the live tail.
  releaseHistory()
  await expect(page.getByText(OLDEST_TEXT)).toBeAttached({ timeout: 20_000 })
  const anchorAfter = await anchor.boundingBox()
  expect(anchorAfter).not.toBeNull()
  expect(Math.abs(anchorAfter!.y - anchorBefore!.y)).toBeLessThanOrEqual(2)
  const scrollTopAfter = await log.evaluate((el) => el.scrollTop)
  expect(scrollTopAfter).toBeGreaterThan(scrollTopBefore)
  expect(scrollTopAfter, 'the reader must stay near the top after the prepend, not be thrown to the tail').toBeLessThan(
    (await log.evaluate((el) => el.scrollHeight)) / 2,
  )

  // The one older page was fetched with a cursor, and exactly once.
  expect(olderRequestUrl, 'the older page request was seen').toBeTruthy()
  expect(olderRequestUrl).toContain('before_seq=')
  const requestsAtTerminal = historyRequests.length
  expect(requestsAtTerminal).toBe(1)

  // Terminal: the below-window entries are all there is — the hint is gone
  // and further top scrolling issues no further history request.
  await expect(hint).toHaveCount(0, { timeout: 20_000 })
  await anchor.hover() // wheel events scroll whatever is under the pointer
  for (let i = 0; i < 4; i++) {
    await page.mouse.wheel(0, -1_500)
  }
  await page.waitForTimeout(500) // any stray request would need a moment to fire
  expect(historyRequests.length, 'no request after the terminal page').toBe(requestsAtTerminal)

  // Live tail still works while the reader sits at the top: a message that
  // arrives now lands in the transcript, the reader's position does not move,
  // and the anchor stays on screen (the reader was not thrown to the tail).
  // The anchor is brought back into view first (the no-request probe above
  // scrolled the reader further up), then the baseline is captured fresh.
  await anchor.scrollIntoViewIfNeeded()
  await expect(anchor).toBeVisible()
  const scrollTopAtTail = await log.evaluate((el) => el.scrollTop)
  const liveText = `pager-message-live-${Date.now()}`
  const live = await page.request.post(`/api/v1/rooms/${roomId}/messages`, {
    headers,
    data: { client_message_id: liveText, text: liveText, mentions: [] },
  })
  expect(live.ok(), `live message failed: ${live.status()}`).toBeTruthy()
  await expect(page.getByText(liveText)).toBeAttached({ timeout: 20_000 })
  await expect(anchor).toBeVisible()
  expect(Math.abs((await log.evaluate((el) => el.scrollTop)) - scrollTopAtTail)).toBeLessThanOrEqual(2)
})
