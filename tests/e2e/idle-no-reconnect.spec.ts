/**
 * idle-no-reconnect.spec.ts — WebSocket heartbeat fix regression test.
 *
 * Verifies that after the WebSocket heartbeat fix the SPA does NOT reconnect
 * during 90 s of chat idle. Before the fix, the client-side liveness check
 * would force-close every 60 s because no server frames arrived during idle
 * (server sent nothing in response to {"type":"ping"}).
 *
 * After the fix:
 *   - Server responds to every {"type":"ping"} with {"type":"pong"}.
 *   - The SPA's onmessage handler resets _receivedSinceLastPing on every pong.
 *   - missedPingCount never reaches 2, so the force-close path is never taken.
 *   - The WebSocket stays open for the full idle period.
 *
 * NOTE: This test must run against the embedded SPA (omnipus binary), NOT
 * `vite dev`. See CLAUDE.md "E2E Testing with the Embedded SPA". The binary
 * URL defaults to OMNIPUS_URL env var or http://localhost:6060.
 *
 * NOTE: This test waits 90 s of real time. It is marked with test.slow() so
 * Playwright allocates a longer timeout. There is no SKIP_SLOW_E2E opt-out
 * anymore: setting it now fails the test loudly (see the preflight below) — a
 * green run with this test silently skipped is the false green the suite's
 * skip policy forbids (tests/e2e/README.md §Skip policy). The test is still
 * runnable locally: npx playwright test idle-no-reconnect.
 */

import { test, expect } from '@playwright/test'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

test(
  'WebSocket stays open during 90s of chat idle',
  async ({ page }) => {
    // Mark as slow: Playwright default per-test timeout is 30 s; this test
    // needs at least 90 s + setup time. test.slow() multiplies the configured
    // timeout by 3.
    test.slow()

    // Preflight (2026-09-16): SKIP_SLOW_E2E=1 used to soft-skip this test,
    // reporting green with nothing executed — the exact false green the skip
    // policy forbids. It now fails fast instead; the 90 s idle window IS the
    // regression this test guards and cannot be traded away by an env var.
    if (process.env.SKIP_SLOW_E2E === '1') {
      throw new Error(
        '[E2E preflight] SKIP_SLOW_E2E=1 is set, but this suite no longer honors it as a silent skip.\n' +
        'A green run with this test skipped is exactly the false green the skip policy\n' +
        'forbids (tests/e2e/README.md §Skip policy; tests/e2e/fixtures/skip-tracking.ts).\n\n' +
        'To fix:\n' +
        '  unset SKIP_SLOW_E2E and let the test run — it needs 90 s of real idle time\n' +
        '  by design (that is the heartbeat regression it guards), or budget for it\n' +
        '  in the CI environment that set the flag.',
      )
    }

    // Track WebSocket openings. We record the URL for each newly opened WS so
    // we can count (re)connections during the idle window.
    const wsOpens: string[] = []
    page.on('websocket', (ws) => {
      wsOpens.push(ws.url())
    })

    await page.goto(BASE_URL)

    // Wait for the SPA to open its chat WebSocket, then for the page to settle.
    // `networkidle` alone is NOT a readiness signal for the socket: it fires
    // after 500 ms without HTTP traffic, which can land in the middle of boot,
    // before the SPA has opened the socket (measured: the socket opens ~0.9 s
    // after navigation, while networkidle fires anywhere from ~1.4 s to ~2.3 s
    // depending on how the router sequences its chunk loads — about 1 load in
    // 30 saw the socket open only AFTER networkidle). Waiting for the socket
    // first makes the count below a positive observation instead of a race.
    // If the gateway has not yet been set up (onboarding required) the socket
    // never opens and this fails here; run against a pre-onboarded instance.
    await expect
      .poll(() => wsOpens.length, {
        message: [
          'The SPA must open at least one WebSocket (the chat WS) after load.',
          'If this fails, the gateway may not be running or onboarding has not been completed.',
          `Base URL: ${BASE_URL}`,
        ].join(' '),
        timeout: 30_000,
      })
      .toBeGreaterThanOrEqual(1)
    await page.waitForLoadState('networkidle', { timeout: 30_000 })

    // Record the WebSocket count once the socket is up and the page is quiet:
    // any entry added during the idle window below is a reconnect.
    const initialWsCount = wsOpens.length

    // Idle for 90 s. Before the heartbeat fix, the client force-closed after
    // 60 s (2 × 30 s heartbeat interval with no server frames). With the fix,
    // each 30 s ping receives a pong, so missedPingCount never reaches 2.
    await page.waitForTimeout(90_000)

    // Assert no additional WebSocket was opened during the idle window.
    // Any reconnect would appear as a new entry in wsOpens.
    expect(
      wsOpens.length,
      [
        `Expected exactly ${initialWsCount} WebSocket connection(s) — no reconnects during 90 s idle.`,
        `Got ${wsOpens.length} total: ${wsOpens.join(', ')}`,
        'A higher count means the SPA force-closed and reconnected at least once,',
        'which indicates the server pong response is not being sent or not being received.',
        'Check: (1) server logs for "case ping" handling, (2) SPA onmessage pong interception.',
      ].join('\n'),
    ).toBe(initialWsCount)
  },
)
