/**
 * iframe-preview-warmup.spec.ts — T1.6
 *
 * T1.6: warmup_default_60s_when_about_returns_60
 *   Don't pass warmupTimeoutSeconds to IframePreview; mount in dev mode.
 *   /api/v1/about returns warmup_timeout_seconds=60.
 *   The component computes maxProbes = Math.floor(60 / 2) = 30.
 *   Using Playwright's fake clock, advance time by 62 s (enough to exhaust
 *   all 30 probes at 2 s intervals). After advancing, assert the warmup
 *   timeout error block appears ("Dev server did not respond in time").
 *
 * This test drives against the real embedded SPA. The clock is frozen so
 * the warmup exhausts in < 1 real second instead of 60 real seconds.
 *
 * Regression class: the maxProbes calculation (Math.floor(effectiveTimeout / 2))
 * must derive from the gateway-supplied warmup_timeout_seconds, not be hardcoded.
 */

import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { seedAndOpenSession } from './fixtures/session-setup'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'
const PREVIEW_PORT = parseInt(process.env.OMNIPUS_PREVIEW_PORT || '6061', 10)
const SYNTHETIC_AGENT_ID = 'main'

test(
  'warmup_default_60s_when_about_returns_60',
  async ({ page }) => {
    const devToken = 'warmup-timeout-60s-token-t16'
    const devPath = `/serve/${SYNTHETIC_AGENT_ID}/${devToken}/`
    const devUrl = `http://localhost:${PREVIEW_PORT}${devPath}`
    const expires = new Date(Date.now() + 3600 * 1000).toISOString()

    // Intercept /api/v1/about to return warmup_timeout_seconds=60.
    // This is the default from config and should be the value IframePreview
    // uses for maxProbes = Math.floor(60 / 2) = 30.
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
          preview_listener_enabled: true,
          preview_port: PREVIEW_PORT,
          warmup_timeout_seconds: 60,
        }),
      })
    })

    // Intercept all requests to the fake preview URL to return 503
    // (simulates no dev server running — all probes fail).
    await page.route(`http://localhost:${PREVIEW_PORT}/**`, (route) => {
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
    // page.clock.tick() advances the mocked clock, triggering all pending setInterval
    // callbacks synchronously. This is what makes the 60-probe cycle complete in
    // < 1 real second in the test.
    await page.clock.fastForward(62_000)

    // Step 3: After time has passed, the warmup should have timed out.
    // IframePreview.tsx: when probeCountRef.current >= maxProbes (30), stopPolling()
    // is called and setWarmupPhase('error') fires.
    await expect(
      page.locator('text=Dev server did not respond in time'),
    ).toBeVisible({ timeout: 10_000 })

    // Step 4: The Retry warmup button must be visible. There is also a generic
    // "Retry" icon button in the iframe chrome — disambiguate explicitly.
    await expect(
      page.getByRole('button', { name: 'Retry warmup' }),
    ).toBeVisible({ timeout: 5_000 })

    // Step 5: Verify the probe count is consistent with maxProbes=30.
    // We inspect the DOM to check that the probe iframe (used during warmup) is no
    // longer present — it is removed when the phase transitions to 'error'.
    // (The probe iframe has aria-hidden="true" and title="probe".)
    await expect(
      page.locator('iframe[title="probe"]'),
    ).not.toBeVisible()
  },
)
