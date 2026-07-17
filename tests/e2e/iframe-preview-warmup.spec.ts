/**
 * iframe-preview-warmup.spec.ts — T1.6
 *
 * T1.6: warmup_default_60s_when_about_returns_60
 *   Seed a legacy `run_in_workspace` (dev-mode) transcript so IframePreview
 *   starts its warmup state machine. Fake the clock, make every probe fail
 *   (503), advance past the full warmup window, and assert the timeout error
 *   block appears ("Dev server did not respond in time").
 *
 * ADR-044 (preview-on-main-listener) update: IframePreview.tsx no longer
 * mounts a hidden probe `<iframe>` to detect dev-server readiness — `/preview/`
 * (and legacy `/serve/`) now share the SPA's own gateway origin, so the warmup
 * poll is a plain same-origin `fetch(url, { method: 'HEAD' })` on an interval
 * (no CORS opt-in needed from the dev server, an improvement over the old
 * two-port model). This test is rewritten to fake that HEAD probe via
 * `page.route()` on the SAME origin the page itself is served from, instead
 * of a separate `PREVIEW_PORT` origin.
 *
 * Also updated: `warmup_timeout_seconds` is no longer read from `/api/v1/about`
 * by any consumer — `WebServeBlock` (src/components/chat/tools/WebServeUI.tsx)
 * never passes a `warmupTimeoutSeconds` prop to `IframePreview`, so the
 * component always falls back to its own hardcoded default (60 s →
 * `maxProbes = Math.floor(60 / 2) = 30`). Mocking `warmup_timeout_seconds` in
 * the about response therefore has no effect on the component under test; the
 * mock is kept only for a schema-realistic response (other parts of the SPA
 * still fetch `/api/v1/about`), with the retired `preview_port` /
 * `preview_listener_enabled` fields replaced by the current `preview_enabled`
 * field (AboutResponse contract, ADR-044 US-8).
 *
 * Regression class: the maxProbes calculation (Math.floor(effectiveTimeout / 2))
 * must derive from IframePreview's own default when no override is supplied.
 */

import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { seedAndOpenSession } from './fixtures/session-setup'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'
// 'mia' — the seeded default of the 4-base roster. The historical 'main' id
// no longer resolves to any agent; using it here would attribute these
// synthetic transcript entries to a non-existent agent.
const SYNTHETIC_AGENT_ID = 'mia'

test(
  'warmup_default_60s_when_about_returns_60',
  async ({ page }) => {
    const devToken = 'warmup-timeout-60s-token-t16'
    const devPath = `/serve/${SYNTHETIC_AGENT_ID}/${devToken}/`
    // ADR-044: /preview/ (and legacy /serve/) share the SPA's own origin —
    // no separate preview listener/port exists anymore. The mocked result
    // URL reflects that: same origin as BASE_URL, not a second port.
    const devUrl = `${BASE_URL}${devPath}`
    const expires = new Date(Date.now() + 3600 * 1000).toISOString()

    // Intercept /api/v1/about for a schema-realistic response. preview_enabled
    // replaces the retired preview_port/preview_listener_enabled fields
    // (ADR-044 US-8). warmup_timeout_seconds is kept for shape realism only —
    // see the file header note; it is not actually read by IframePreview.
    await page.route(`${BASE_URL}/api/v1/about`, async (route) => {
      let base: Record<string, unknown> = {
        version: 'test',
        go_version: 'go1.21',
        os: 'linux',
        arch: 'amd64',
        uptime_seconds: 1,
      }
      try {
        const real = await route.fetch()
        if (real.ok()) base = (await real.json()) as Record<string, unknown>
      } catch {
        // stub
      }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          ...base,
          preview_enabled: true,
          warmup_timeout_seconds: 60,
        }),
      })
    })

    // Intercept the same-origin preview URL to return 503 (simulates no dev
    // server running — every HEAD probe fails). Scoped to the exact preview
    // path only — BASE_URL is the same origin the page itself is served from,
    // so a broader pattern would also intercept the SPA's own asset/API
    // requests.
    await page.route(devUrl, (route) => {
      void route.fulfill({ status: 503, body: 'no server' })
    })

    // Install fake clock BEFORE the page navigation so React's setInterval
    // uses the mocked clock.
    await page.clock.install({ time: Date.now() })

    await seedAndOpenSession(page, 'warmup-60s-t16', [
      {
        id: 'user-t16-1',
        role: 'user',
        content: 'start dev server',
        timestamp: new Date(Date.now() - 5000).toISOString(),
        agent_id: '',
      },
      {
        id: 'asst-t16-1',
        role: 'assistant',
        content: 'Dev server is starting.',
        timestamp: new Date(Date.now() - 4000).toISOString(),
        agent_id: SYNTHETIC_AGENT_ID,
        tool_calls: [
          {
            id: 'tc-t16-run',
            tool: 'run_in_workspace',
            status: 'success',
            duration_ms: 200,
            parameters: { command: 'vite dev', port: 5173 },
            result: {
              path: devPath,
              url: devUrl,
              expires_at: expires,
              command: 'vite dev',
              port: 5173,
            },
          },
        ],
      },
    ])

    // Step 1: Verify the warmup placeholder is visible (warmup state machine started).
    // This also confirms the component rendered at all (not blank/crashed).
    // Scope to the preview-warmup testid — the page can render multiple
    // aria-live=polite regions (sr-only announcer, status text, span) which
    // would trip strict-mode if the bare text/role selector were used.
    await expect(
      page.getByTestId('preview-warmup').first(),
    ).toBeVisible({ timeout: 15_000 })

    // Step 2: Use Playwright clock to advance time past the full 60 s warmup window.
    // Each probe fires every 2 s; 30 probes * 2 s = 60 s total. We advance by 62 s
    // to ensure the last interval tick fires and transitions to 'error'.
    //
    // page.clock.runFor() advances the mocked clock tick-by-tick, firing EVERY
    // due setInterval callback in order (like real elapsed time, just fast) — this
    // is what makes the 30-probe cycle complete in < 1 real second in the test.
    // fastForward() is NOT equivalent here: per Playwright's Clock semantics it
    // jumps the clock forward and fires at most the single most-recent due callback
    // for a recurring timer (it's built for "skip past N calls you don't care about",
    // not "run through all of them") — so probeCountRef would only ever reach 1,
    // never maxProbes (30), and the component would never transition to 'error'.
    await page.clock.runFor(62_000)

    // Step 3: After time has passed, the warmup should have timed out.
    // IframePreview.tsx: when probeCountRef.current >= maxProbes (30), stopPolling()
    // is called and setWarmupPhase('error') fires, rendering ErrorBlock's message.
    await expect(
      page.locator('text=Dev server did not respond in time'),
    ).toBeVisible({ timeout: 10_000 })

    // Step 4: The Retry button must be visible (ErrorBlock's onRetry handler,
    // IframePreview.tsx — accessible name is "Retry warmup" via aria-label,
    // even though the visible label is just "Retry").
    await expect(
      page.getByRole('button', { name: 'Retry warmup' }),
    ).toBeVisible({ timeout: 5_000 })

    // Step 5: ADR-044 — IframePreview never mounts an <iframe> at all anymore
    // (link-only rendering, D6). Assert there is none anywhere on the page,
    // not just that a since-retired "probe" iframe is gone.
    await expect(page.locator('iframe')).toHaveCount(0)
  },
)
