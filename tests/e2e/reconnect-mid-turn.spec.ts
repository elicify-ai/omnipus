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

    // setOffline BLOCKS the network; the DOM `offline` event is what the app
    // actually listens to, and the two are not the same thing.
    //
    // ws.ts's _onOffline is the fast detection path: on `offline` it closes
    // the socket, which drives onDisconnected -> isConnected=false -> (after
    // ChatScreen's deliberate 2s useSettledFlag debounce) the banner. Absent
    // that event the ONLY other detector is the ping heartbeat, which needs
    // two consecutive 30s ticks with no server frame — up to 60s, far past
    // this assertion's budget AND past the 5s blip below, so the socket would
    // be healthy again before it ever noticed.
    //
    // Playwright's setOffline emulates network conditions via CDP; it does
    // not reliably fire `offline` or tear down an already-open WebSocket.
    // That inconsistency is exactly what this test hit: same commit, the
    // llm-agents shard PASSED on ci-omnipus and failed 3/3 on ci-omnipus-2,
    // and the failure snapshot showed a normally-connected UI with "Stop
    // generation" still visible 10s into being offline — the app was never
    // told. So raise the event explicitly rather than hoping the emulation
    // raises it for us.
    //
    // This does not soften what is under test. The contract is "the browser
    // reports offline -> the app re-attaches by itself, with no user action",
    // and every assertion below still has to hold on the real code path:
    // real close, real reconnect scheduling, real banner, real catch-up of a
    // turn that kept running server-side.
    await context.setOffline(true)
    await page.evaluate(() => window.dispatchEvent(new Event('offline')))
    await expect(page.getByTestId('reconnect-banner')).toBeVisible({ timeout: 15_000 })
    await page.waitForTimeout(5_000)
    await context.setOffline(false)
    await page.evaluate(() => window.dispatchEvent(new Event('online')))
    await expect(page.getByTestId('reconnect-banner')).not.toBeVisible({ timeout: 30_000 })

    await waitTurnDone(page)
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
    const after = (await assistantMessages(page).first().innerText()).trim()
    expect(after.length).toBeGreaterThanOrEqual(before.length)
  })
})
