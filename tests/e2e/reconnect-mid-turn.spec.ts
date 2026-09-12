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

    // THE OUTAGE HAS TO ACTUALLY LAST, or the banner correctly never renders.
    //
    // ChatScreen gates this banner on useSettledFlag(..., 2000): a disconnect
    // must PERSIST 2s before anything is drawn, deliberately, so a blip the
    // user would never have noticed does not flash an alarm at them. Its own
    // comment says so — "only the rendering waits", reconnect still fires
    // instantly.
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
    // close, real reconnect scheduling with real failures, the real banner on
    // its real debounce, and real catch-up of a turn that kept running
    // server-side the whole time.
    await page.routeWebSocket(/\/api\/v1\/chat\/ws/, (ws) => ws.close())
    await context.setOffline(true)
    await page.evaluate(() => window.dispatchEvent(new Event('offline')))
    await expect(page.getByTestId('reconnect-banner')).toBeVisible({ timeout: 20_000 })

    // Hold the outage open past the debounce, then let the network back.
    await page.waitForTimeout(5_000)
    await page.unrouteAll({ behavior: 'ignoreErrors' })
    await context.setOffline(false)
    await page.evaluate(() => window.dispatchEvent(new Event('online')))
    await expect(page.getByTestId('reconnect-banner')).not.toBeVisible({ timeout: 30_000 })

    await waitTurnDone(page)
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
    const after = (await assistantMessages(page).first().innerText()).trim()
    expect(after.length).toBeGreaterThanOrEqual(before.length)
  })
})
