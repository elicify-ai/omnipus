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
 * RED until #1332's SPA half lands: today the screen loads no older page, so
 * the hint never clears and walking to entry 1 is impossible.
 *
 * CI only — never run locally. Shard: stubs-1.
 */
import { expect, test, type Page } from '@playwright/test'

const ENTRY_COUNT = 105 // one default page (100) + 5 older

async function csrfHeaders(page: Page): Promise<Record<string, string>> {
  const cookies = await page.context().cookies()
  const csrf = cookies.find((c) => c.name === 'csrf' || c.name === '__Host-csrf')
  return csrf ? { 'X-CSRF-Token': csrf.value } : {}
}

test('scrolling up through paged history keeps the reader anchored and ends at the first entry', async ({ page }) => {
  const headers = await csrfHeaders(page)
  const wsList = await page.request.get('/api/v1/workspaces', { headers })
  expect(wsList.ok()).toBeTruthy()
  const def = ((await wsList.json()) as Array<{ id?: string; is_default?: boolean }>).find((w) => w.is_default === true)
  expect(def?.id, 'no default workspace').toBeTruthy()

  const create = await page.request.post(`/api/v1/workspaces/${def!.id}/rooms`, {
    headers,
    data: { name: `Paging-${Date.now()}`, kind: 'adhoc', agent_ids: ['mia'] },
  })
  expect(create.ok(), `create room failed: ${create.status()}`).toBeTruthy()
  const { id: roomId } = (await create.json()) as { id: string }

  // Seed 105 entries: attach shows the newest 100 (has_older true); walking
  // one older page must reach entry 1.
  for (let i = 1; i <= ENTRY_COUNT; i++) {
    const sent = await page.request.post(`/api/v1/rooms/${roomId}/messages`, {
      headers,
      data: { client_message_id: `seed-${i}`, text: `pager-message-${i}`, addressees: [] },
    })
    expect(sent.ok(), `seed ${i} failed: ${sent.status()}`).toBeTruthy()
  }

  await page.goto(`/#/workspaces/${def!.id}/rooms/${roomId}`)
  const log = page.getByRole('log', { name: 'Group conversation' })
  await expect(log).toBeVisible({ timeout: 20_000 })
  await expect(page.getByText('pager-message-105')).toBeVisible({ timeout: 20_000 })
  // The newest page carries the hint: older history exists.
  await expect(page.getByText(/Earlier messages aren't shown/)).toBeVisible()

  // Remember a mid-window anchor message the reader can see, then scroll to
  // the top to load the older page.
  const anchor = page.getByText('pager-message-60')
  await anchor.scrollIntoViewIfNeeded()
  await expect(anchor).toBeVisible()
  await page.getByText('pager-message-10').scrollIntoViewIfNeeded()
  await expect(page.getByText(/Earlier messages aren't shown/)).toBeVisible()

  // Walk the older pages to the terminal one, then assert the beginning.
  let guard = 0
  while (await page.getByText(/Earlier messages aren't shown/).count()) {
    guard += 1
    expect(guard, 'history walk must terminate').toBeLessThanOrEqual(5)
    await page.getByText(/Earlier messages aren't shown/).scrollIntoViewIfNeeded()
    await page.waitForTimeout(1_500) // one page load per step; 105 entries need 2
  }

  // The beginning of the room is reached: entry 1 is on screen, the hint is
  // gone, and the reader was not thrown to the bottom mid-walk (the anchor
  // message from the original window is still rendered in the transcript).
  await expect(page.getByText('pager-message-1')).toBeVisible()
  await expect(page.getByText(/Earlier messages aren't shown/)).toHaveCount(0)
  await expect(page.getByText('pager-message-60')).toBeAttached()
})
