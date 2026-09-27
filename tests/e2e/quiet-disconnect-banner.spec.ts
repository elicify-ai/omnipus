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
 * Real-browser follow-up (orchestrator): a prior version of this spec
 * expected the calm CHAT-wide line at QUIET_DROP_MS (15s) itself. That
 * conflated two genuinely separate, deliberately staggered thresholds in
 * src/components/chat/ConnectionStatus.tsx (both directly tested there,
 * ConnectionStatus.test.tsx's "shows only the answer continuation state at
 * 15 seconds" and "... at two minutes"): QUIET_DROP_MS (15s) governs only
 * the PER-MESSAGE "this answer may not have finished" indicator, which
 * requires an actual interrupted answer to exist at all. This spec never
 * sends a message, so that indicator is never in play. The GLOBAL,
 * chat-wide reachability line this spec actually exercises
 * (`connection-status-line` rendered with no message context) is governed
 * by the SEPARATE, INTENTIONALLY LONGER CHAT_NOTICE_MS (2 minutes) — "C.
 * Chat-wide (LONG outage only)" names exactly this: a genuinely long
 * outage, not just "longer than a blip". Fixed to wait for the real
 * threshold instead of weakening what it proves.
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

// src/components/chat/ConnectionStatus.tsx's own exported constants — the
// spec's oracle values, never re-derived here. QUIET_DROP_MS gates the
// per-message answer indicator (not exercised by this spec, which never
// sends a message); CHAT_NOTICE_MS gates the chat-wide line this spec
// actually asserts on.
const QUIET_DROP_MS = 15_000
const CHAT_NOTICE_MS = 120_000

const BANNED_TEXT = [/gateway/i, /disconnected from/i, /\b1006\b/]

async function assertNoBannedText(page: import('@playwright/test').Page): Promise<void> {
  const bodyText = await page.locator('body').innerText()
  for (const pattern of BANNED_TEXT) {
    expect(bodyText, `page text must not match ${pattern}`).not.toMatch(pattern)
  }
}

test.describe('quiet disconnect banner (#823)', () => {
  test('a real network drop stays silent for 15s, silent well past it, shows only the calm line at the real chat-wide threshold, and clears cleanly on recovery', async ({ page, context }) => {
    test.setTimeout(CHAT_NOTICE_MS + 120_000)

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

    // Past QUIET_DROP_MS the page STAYS quiet — this spec never sends a
    // message, so the per-message answer indicator (the only thing
    // QUIET_DROP_MS actually gates) is never in play here at all. The
    // chat-wide line this spec exercises has its own, deliberately longer
    // threshold — asserting it's still hidden here is what actually proves
    // "silent for 15s" isn't accidentally true for the wrong reason.
    await page.waitForTimeout(5_000)
    await expect(page.getByTestId('connection-status-line')).toBeHidden()
    await assertNoBannedText(page)

    // Merge of release #845 and the #823 redo (stricter of both): still
    // hidden just BEFORE the real chat-wide threshold — so a line that
    // appeared early (anywhere between 15s and 2 minutes) fails here
    // instead of passing a single long "visible within 2 minutes" wait.
    // Elapsed since the drop registered: (QUIET_DROP_MS - 2s) + 5s so far.
    await page.waitForTimeout(CHAT_NOTICE_MS - QUIET_DROP_MS - 3_000 - 7_000)
    await expect(page.getByTestId('connection-status-line')).toBeHidden()
    await assertNoBannedText(page)

    // At the real chat-wide threshold (CHAT_NOTICE_MS) the calm status
    // line appears — never the old alarming banner, and still never the
    // banned wording.
    await expect(page.getByTestId('connection-status-line')).toBeVisible({ timeout: 25_000 })
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
