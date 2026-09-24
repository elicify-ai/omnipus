/**
 * quiet-disconnect-banner.spec.ts — #823 spec items 1-3: a real network drop
 * must stay visually silent for the first 15 seconds, then show only the
 * calm connection-status line — never the old alarming "Disconnected from
 * gateway — code NNNN" banner — and must clear cleanly on recovery.
 *
 * Modelled on tests/e2e/reconnect-mid-turn.spec.ts (structure, real-binary
 * drive style) and tests/e2e/ws-reconnect.spec.ts's
 * online_event_triggers_reconnect (T1.13) — this spec uses the same
 * `context.setOffline(true)` mechanism T1.13 already proved reliably drops
 * the live socket (polling `ws.readyState !== WebSocket.OPEN`), but T1.13
 * never runs past ~10s so it never reaches the 15s quiet threshold or
 * exercises the calm status line. This spec does.
 *
 * Spec source: issue #823 (elicify-ai/omnipus) — issue body, "D. Deliberately
 * invisible" ("A drop shorter than about 15s: nothing is shown") and
 * "C. Chat-wide (long outage only)" (the calm status line + retry, "back"
 * note on recovery); founder comment 2026-09-23, "Gap found in a real
 * browser: the old technical banner is still on the error path" ("It is
 * immediate" / "It is technical" / "It uses the error channel").
 *
 * Text assertions target visible page content directly (not `role="alert"`
 * element counts) because the e2e harness runs with `dev_mode_bypass: true`
 * (tests/e2e/global-setup.ts), which keeps its OWN unrelated
 * `data-testid="dev-mode-banner"` alert up for the whole suite — counting
 * `role="alert"` elements would false-fail on that banner regardless of this
 * bug. Text-content assertions avoid that noise entirely and are also a more
 * direct match for spec item 3's actual wording ("No visible text …").
 */

import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { chatInput, waitForConnected } from './fixtures/selectors'

// src/components/chat/ConnectionStatus.tsx's own exported QUIET_DROP_MS —
// the spec's oracle constant, never re-derived here.
const QUIET_DROP_MS = 15_000
// ConnectionStatus.tsx's CHAT_NOTICE_MS: the chat-wide calm line ("C. Chat-wide
// (long outage only)") appears only after this long offline. QUIET_DROP_MS
// governs the per-answer "still working" note, which needs an interrupted
// answer — this spec has none, so nothing at all may show before this mark.
const CHAT_NOTICE_MS = 120_000

const BANNED_TEXT = [/gateway/i, /disconnected from/i, /\b1006\b/]

async function assertNoBannedText(page: import('@playwright/test').Page): Promise<void> {
  const bodyText = await page.locator('body').innerText()
  for (const pattern of BANNED_TEXT) {
    expect(bodyText, `page text must not match ${pattern}`).not.toMatch(pattern)
  }
}

test.describe('quiet disconnect banner (#823)', () => {
  test('a real network drop stays silent for 15s, shows only the calm line after, and clears cleanly on recovery', async ({ page, context }) => {
    test.setTimeout(CHAT_NOTICE_MS + 90_000)

    await page.goto('/')
    await expect(chatInput(page)).toBeVisible({ timeout: 15_000 })
    await waitForConnected(page)
    await expect(page.getByTestId('connection-status-line')).toBeHidden()
    await assertNoBannedText(page)

    // Go offline — a real network-level drop (CDP), not a simulated close.
    await context.setOffline(true)

    // Confirm the drop actually registered (no open socket) before timing
    // the quiet window from a known start — same readiness gate as
    // ws-reconnect.spec.ts's T1.13.
    await expect.poll(async () => page.evaluate(() =>
      ((window as unknown as { __ws_instances?: WebSocket[] }).__ws_instances ?? [])
        .every((ws) => ws.readyState !== WebSocket.OPEN),
    ), { timeout: 10_000 }).toBe(true)

    // Nothing alarming for the first QUIET_DROP_MS: no calm status line
    // either yet, and no internal vocabulary anywhere on the page.
    await page.waitForTimeout(QUIET_DROP_MS - 2_000)
    await expect(page.getByTestId('connection-status-line')).toBeHidden()
    await assertNoBannedText(page)

    // Past QUIET_DROP_MS and up to CHAT_NOTICE_MS there is still nothing:
    // with no interrupted answer, the short-drop window has no visible state.
    await page.waitForTimeout(4_000)
    await expect(page.getByTestId('connection-status-line')).toBeHidden()
    await assertNoBannedText(page)

    // At CHAT_NOTICE_MS the calm chat-wide status line appears — never the old
    // alarming banner, and still never the banned wording.
    await expect(page.getByTestId('connection-status-line')).toBeVisible({ timeout: CHAT_NOTICE_MS })
    await assertNoBannedText(page)

    // Restore the network — recovery must clear cleanly (the ~2s "back"
    // note is allowed per spec item 2, but it must fade to nothing, and the
    // banned wording must never have appeared at any point).
    await context.setOffline(false)
    await expect.poll(async () => page.evaluate(() =>
      ((window as unknown as { __ws_instances?: WebSocket[] }).__ws_instances ?? [])
        .some((ws) => ws.readyState === WebSocket.OPEN),
    ), { timeout: 20_000 }).toBe(true)
    await expect(page.getByTestId('connection-status-line')).toBeHidden({ timeout: 10_000 })
    await assertNoBannedText(page)
  })
})
