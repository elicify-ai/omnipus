/**
 * reconnect-mid-turn.spec.ts — ADR-082 D2/D3/D4/D5, spec test T-18 (S-08, S-09, S-10).
 *
 * Traces to: docs/internal/architecture/ADR-082-ui-independent-turns-and-session-bound-streaming.md,
 * docs/internal/specs/ui-independent-turns-spec.md.
 *
 * Drives the REAL embedded binary against a LIVE model, so it asserts only
 * structure, never content: after a reload / a second tab / an offline blip
 * mid-turn, the streaming bubble is present, Stop is visible while the turn
 * runs, exactly one assistant message exists at the end, and its text is at
 * least as long as what was visible before the disconnect (catch-up + live
 * tail, no gap). Content equality across two tabs IS asserted — both views
 * of one session must converge on the same persisted text.
 */

import { expect, type Page } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { chatInput, waitForConnected, startNewChat, assistantMessages } from './fixtures/selectors'

// Plain prose, no tools: a single long streaming round with a stable shape.
const LONG_PROMPT =
  'Do NOT use any tools. Plain prose only. Write eight short paragraphs about the sea, about 600 words total.'

const stopButton = (page: Page) => page.locator('[data-testid="stop-btn"]')
// Any assistant row, running or finished (assistantMessages() excludes running rows).
const anyAssistantRow = (page: Page) => page.locator('[data-message-id]:not(.flex-row-reverse)')

async function startLongTurn(page: Page): Promise<string> {
  const input = chatInput(page)
  await expect(input).toBeVisible({ timeout: 15_000 })
  await waitForConnected(page)
  await startNewChat(page)
  await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })
  await input.fill(LONG_PROMPT)
  await input.press('Enter')
  await expect(stopButton(page)).toBeVisible({ timeout: 30_000 })
  // Wait until some text has actually streamed so "at least as long" is a real bar.
  const row = anyAssistantRow(page).first()
  await expect.poll(async () => (await row.innerText().catch(() => '')).trim().length, { timeout: 60_000 }).toBeGreaterThan(80)
  return (await row.innerText()).trim()
}

async function waitTurnDone(page: Page) {
  await expect(stopButton(page)).toBeHidden({ timeout: 240_000 })
}

/**
 * Asserts one session converged on exactly one finished answer that kept at
 * least as much text as was visible before the outage.
 *
 * `toHaveCount(1)` is the load-bearing assertion for catch-up: the classic
 * duplicate-bubble failure (a re-delivered frame opening a second message)
 * shows up here and nowhere else. The status check carries the other half of
 * #823 phase 2 — a message must never be left stuck in a running state after
 * catch-up.
 */
async function expectOneFinishedAnswer(page: Page, before: string, label: string) {
  await expect(assistantMessages(page), `session ${label} must end with exactly one answer`).toHaveCount(1, {
    timeout: 60_000,
  })
  const row = assistantMessages(page).first()
  await expect(row, `session ${label} must not be left in a running state`).not.toHaveAttribute(
    'data-status',
    'running',
    { timeout: 30_000 },
  )
  const after = (await row.innerText()).trim()
  expect(
    after.length,
    `session ${label} must not lose text across the outage (before ${before.length}, after ${after.length})`,
  ).toBeGreaterThanOrEqual(before.length)
}

test.describe('reconnect mid-turn (ADR-082)', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
  })

  test('S-08 reload mid-turn: bubble continues, Stop visible, single done', async ({ page }) => {
    test.setTimeout(420_000)
    const before = await startLongTurn(page)

    await page.reload()
    await expect(chatInput(page)).toBeVisible({ timeout: 15_000 })
    await waitForConnected(page)

    // D4: the gateway emits session_state{active_turn} FIRST on attach, so if
    // the turn is still running Stop should reappear within ~5s of reconnect
    // — bounded here at 15s for CI jitter. The turn may also have already
    // finished during the reload, in which case a completed message exists
    // instead. Assert the ACTUAL branch explicitly (not a tautological
    // membership check that passes no matter which literal comes back).
    const stopOrDone = await Promise.race([
      stopButton(page).waitFor({ state: 'visible', timeout: 15_000 }).then(() => 'stop' as const),
      assistantMessages(page).first().waitFor({ state: 'visible', timeout: 15_000 }).then(() => 'done' as const),
    ])
    if (stopOrDone === 'stop') {
      await expect(stopButton(page)).toBeVisible()
      await expect(anyAssistantRow(page).first()).toBeVisible()
    } else {
      const doneRow = assistantMessages(page).first()
      await expect(doneRow).toBeVisible()
      expect((await doneRow.innerText()).trim().length).toBeGreaterThan(0)
    }

    await waitTurnDone(page)
    // ADR-082 D4: "Stop is back after reload" also means Stop goes away and
    // the composer re-enables once the turn genuinely ends — assert both,
    // bounded, rather than relying on waitTurnDone's 240s ceiling alone.
    await expect(stopButton(page)).toBeHidden({ timeout: 30_000 })
    await expect(chatInput(page)).toBeEnabled({ timeout: 10_000 })

    // Exactly one assistant message; catch-up + live tail ⇒ no gap.
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
    const after = (await assistantMessages(page).first().innerText()).trim()
    expect(after.length).toBeGreaterThanOrEqual(before.length)
    // The text seen before the reload is a prefix of the final text (modulo whitespace).
    const norm = (s: string) => s.replace(/\s+/g, ' ')
    expect(norm(after).startsWith(norm(before).slice(0, 60))).toBeTruthy()
  })

  test('S-09 second tab attaches mid-turn and converges on the same text', async ({ page, context }) => {
    test.setTimeout(420_000)
    await startLongTurn(page)

    const page2 = await context.newPage()
    await page2.goto('/')
    await expect(chatInput(page2)).toBeVisible({ timeout: 15_000 })
    await waitForConnected(page2)

    // Same D4 attach-order guarantee as S-08, asserted on the SECOND tab's
    // own attach: Stop reappears within the 15s bound if the turn is still
    // running, otherwise a completed, non-empty message is already there.
    const stopOrDone = await Promise.race([
      stopButton(page2).waitFor({ state: 'visible', timeout: 15_000 }).then(() => 'stop' as const),
      assistantMessages(page2).first().waitFor({ state: 'visible', timeout: 15_000 }).then(() => 'done' as const),
    ])
    if (stopOrDone === 'stop') {
      await expect(stopButton(page2)).toBeVisible()
      await expect(anyAssistantRow(page2).first()).toBeVisible()
    } else {
      const doneRow = assistantMessages(page2).first()
      await expect(doneRow).toBeVisible()
      expect((await doneRow.innerText()).trim().length).toBeGreaterThan(0)
    }

    await waitTurnDone(page)
    await waitTurnDone(page2)
    await expect(stopButton(page2)).toBeHidden({ timeout: 30_000 })
    await expect(chatInput(page2)).toBeEnabled({ timeout: 10_000 })
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
    await expect(assistantMessages(page2)).toHaveCount(1, { timeout: 30_000 })

    const t1 = (await assistantMessages(page).first().innerText()).replace(/\s+/g, ' ').trim()
    const t2 = (await assistantMessages(page2).first().innerText()).replace(/\s+/g, ' ').trim()
    expect(t2).toBe(t1)
    await page2.close()
  })

  test('S-10 offline blip mid-turn: automatic re-attach, no user action', async ({ page, context }) => {
    test.setTimeout(420_000)
    const before = await startLongTurn(page)

    // THE OUTAGE HAS TO ACTUALLY LAST: #823 keeps drops under 15 seconds quiet,
    // then adds a calm continuation line to the interrupted assistant answer.
    //
    // This test used to call context.setOffline(true) and expect the banner
    // within 10s. setOffline emulates network conditions over CDP; it does
    // not fire the DOM `offline` event that ws.ts._onOffline listens for, and
    // it does not reliably stop a loopback reconnect to the gateway running
    // on this same machine. So the socket came straight back — ws.ts's
    // _onOnline path resets backoff and redials after 250ms — and 250ms never
    // clears a 2000ms debounce. No banner, and the product was RIGHT not to
    // draw one.
    //
    // That is why it flip-flopped between the two CI workers on identical
    // commits: on a loaded box the redial slipped past 2s and the banner
    // appeared; on a fast one it did not. Three runs, three different
    // pass/fail splits across ci-omnipus and ci-omnipus-2.
    //
    // So make the outage real: routeWebSocket closes every reconnect attempt,
    // which keeps isConnected false well past the debounce. The route is
    // installed AFTER the turn is already streaming, so the original socket
    // is untouched — only the redials fail, exactly as a real outage behaves.
    //
    // Nothing is softened. Every assertion still runs the real path: a real
    // close, real reconnect scheduling with real failures, the real delayed
    // answer state, and real catch-up of a turn that kept running
    // server-side the whole time.
    await page.routeWebSocket(/\/api\/v1\/chat\/ws/, (ws) => ws.close())
    await context.setOffline(true)
    await page.evaluate(() => window.dispatchEvent(new Event('offline')))
    await expect(page.getByTestId('assistant-connection-status')).toContainText(
      'is still working on this — the rest appears when you\'re connected again.',
      { timeout: 20_000 },
    )

    // Let the network back after the delayed state is genuinely visible.
    await page.unrouteAll({ behavior: 'ignoreErrors' })
    await context.setOffline(false)
    await page.evaluate(() => window.dispatchEvent(new Event('online')))
    await expect(page.getByTestId('connection-status-line')).toHaveText('Up to date', { timeout: 30_000 })
    await expect(page.getByTestId('connection-status-line')).not.toBeVisible({ timeout: 5_000 })

    await waitTurnDone(page)
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
    const after = (await assistantMessages(page).first().innerText()).trim()
    expect(after.length).toBeGreaterThanOrEqual(before.length)
  })

  // #823 phase 2, the case the founder called out explicitly: an outage must be
  // recoverable for EVERY session the user has, not only the one on screen, and
  // each session must resume from ITS OWN position. Two turns therefore run
  // concurrently across the outage: the one visible when the network drops (B),
  // and one left behind in another conversation (A). A client that kept a single
  // global cursor instead of one per session would get one of them wrong.
  test('S-11 offline blip with activity in TWO sessions: each catches up on its own', async ({ page, context }) => {
    test.setTimeout(600_000)

    // Session A: started, then left running in the background.
    const beforeA = await startLongTurn(page)
    const urlA = page.url()

    // Session B: a second conversation with its own long turn, so the outage
    // window genuinely has activity in two different sessions at once.
    const beforeB = await startLongTurn(page)

    // A REAL outage — same mechanism as S-10 and for the same reason: the
    // redials must keep failing for longer than the quiet window, so both turns
    // keep running server-side while the browser is away.
    await page.routeWebSocket(/\/api\/v1\/chat\/ws/, (ws) => ws.close())
    await context.setOffline(true)
    await page.evaluate(() => window.dispatchEvent(new Event('offline')))
    await expect(page.getByTestId('assistant-connection-status')).toContainText('is still working on this', {
      timeout: 20_000,
    })

    await page.unrouteAll({ behavior: 'ignoreErrors' })
    await context.setOffline(false)
    await page.evaluate(() => window.dispatchEvent(new Event('online')))
    await expect(page.getByTestId('connection-status-line')).toHaveText('Up to date', { timeout: 30_000 })

    // B is the session on screen: it must catch up from its own cursor.
    await waitTurnDone(page)
    await expectOneFinishedAnswer(page, beforeB, 'B')

    // A was NOT open in the UI during the outage. Opening it now must still
    // catch up — either incrementally from A's own cursor or via a snapshot,
    // and either way with nothing dropped and nothing left running.
    await page.goto(urlA)
    await waitForConnected(page)
    await waitTurnDone(page)
    await expectOneFinishedAnswer(page, beforeA, 'A')
  })
})
