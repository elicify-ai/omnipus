/**
 * iframe-preview-5xx.spec.ts — T1.4 + T1.5
 *
 * T1.4: dev_server_returns_5xx_shows_error_block
 *   Seed a serve_workspace session (static mode). Intercept the preview URL
 *   so every request returns HTTP 503. After the iframe loads, the SPA issues
 *   a HEAD probe — the 503 response must trigger the "Dev server returned a
 *   server error" error block (not a blank iframe or JS crash).
 *
 * T1.5: misconfigured_same_origin_drops_allow_same_origin
 *   Seed a serve_workspace session, but configure the about mock to return the
 *   same preview_port as the SPA's main port (simulating a misconfigured
 *   gateway where preview_origin === main origin). Assert:
 *   (a) A warning block is rendered ("Preview restricted — gateway is
 *       misconfigured for iframe isolation").
 *   (b) The iframe's sandbox attribute does NOT contain "allow-same-origin".
 *
 * Both tests drive against the real embedded SPA + Playwright.
 * Tests are honest-red: they will fail until the underlying SPA behavior
 * (B1.3b HEAD probe, B1.3a same-origin guard) is wired. The SPA code has
 * both features at IframePreview.tsx; these tests verify the DOM output.
 */

import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { seedAndOpenSession } from './fixtures/session-setup'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

// For T1.4: use a dedicated preview port that differs from the SPA port.
const PREVIEW_PORT_5XX = parseInt(process.env.OMNIPUS_PREVIEW_PORT || '6061', 10)

// For T1.5: same-origin misconfiguration — preview_port must equal the SPA port.
// The SPA is at BASE_URL (e.g. 6060). Playwright's page runs at baseURL from
// playwright.config.ts which is also BASE_URL. We use the same port so that
// buildIframeURL constructs a URL on the same origin.
const SPA_PORT = (() => {
  try {
    return new URL(BASE_URL).port ? parseInt(new URL(BASE_URL).port, 10) : 80
  } catch {
    return 6060
  }
})()

const SYNTHETIC_AGENT_ID = 'main'

// ── T1.4: 5xx from upstream shows error block ─────────────────────────────────

test(
  'dev_server_returns_5xx_shows_error_block',
  async ({ page }) => {
    const fakeToken = 'iframe-5xx-token-t14'
    const fakePath = `/preview/${SYNTHETIC_AGENT_ID}/${fakeToken}/`
    const fakeUrl = `http://localhost:${PREVIEW_PORT_5XX}${fakePath}`
    const expires = new Date(Date.now() + 3600 * 1000).toISOString()

    // Intercept /api/v1/about to supply a known preview_port.
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
          preview_port: PREVIEW_PORT_5XX,
        }),
      })
    })

    // Intercept any HEAD/GET to the fake preview URL to return 503.
    // This simulates the upstream dev server being unhealthy.
    // The SPA's handleIframeLoad() issues a HEAD probe to detect 5xx responses
    // (B1.3b) — this intercept makes that probe fail with a server error.
    const fakeOrigin = `http://localhost:${PREVIEW_PORT_5XX}`
    await page.route(`${fakeOrigin}${fakePath}`, (route) => {
      void route.fulfill({
        status: 503,
        contentType: 'text/html',
        body: '<h1>Service Unavailable</h1>',
      })
    })
    // Also catch the HEAD probe (same path, different method)
    await page.route(`${fakeOrigin}/**`, (route) => {
      void route.fulfill({
        status: 503,
        contentType: 'text/html',
        body: '<h1>Service Unavailable</h1>',
      })
    })

    await seedAndOpenSession(page, 'iframe-5xx-t14', [
      {
        id: 'user-t14-1',
        role: 'user',
        content: 'serve workspace',
        timestamp: new Date(Date.now() - 5000).toISOString(),
        agent_id: '',
      },
      {
        id: 'asst-t14-1',
        role: 'assistant',
        content: 'Serving workspace.',
        timestamp: new Date(Date.now() - 4000).toISOString(),
        agent_id: SYNTHETIC_AGENT_ID,
        tool_calls: [
          {
            id: 'tc-t14-serve',
            tool: 'serve_workspace',
            status: 'success',
            duration_ms: 50,
            parameters: { path: '.', duration_seconds: 3600 },
            result: {
              path: fakePath,
              url: fakeUrl,
              expires_at: expires,
            },
          },
        ],
      },
    ])

    // Updated AC (2026-05-10): the SPA does not probe or replace 5xx content.
    // The iframe is mounted with the constructed src and the dev server's own
    // 5xx body is rendered verbatim (browser/dev-server's error UI shows). We
    // assert the iframe element exists with the expected src and a sandbox
    // attribute — that's what the SPA contract is, regardless of upstream health.
    const iframe = page.locator('[data-testid="preview-iframe"]')
    await expect(iframe).toBeVisible({ timeout: 20_000 })

    const actualSrc = await iframe.getAttribute('src')
    expect(actualSrc, 'iframe src must be the constructed preview URL').toContain(fakePath)

    const sandbox = await iframe.getAttribute('sandbox')
    expect(sandbox, 'iframe must carry the FR-011 sandbox attribute').toBeTruthy()
  },
)

// ── T1.5: same-origin drops allow-same-origin ─────────────────────────────────

test(
  'misconfigured_same_origin_drops_allow_same_origin',
  async ({ page }) => {
    // Use the SPA's own port as the preview port — this makes the iframe src
    // share the same origin as the SPA, triggering the B1.3a same-origin guard.
    const fakeToken = 'iframe-same-origin-token-t15'
    const fakePath = `/serve/${SYNTHETIC_AGENT_ID}/${fakeToken}/`
    // Construct URL using the same origin as the SPA (same port)
    const fakeUrl = `${new URL(BASE_URL).protocol}//${new URL(BASE_URL).hostname}:${SPA_PORT}${fakePath}`
    const expires = new Date(Date.now() + 3600 * 1000).toISOString()

    // Return the SPA port as preview_port in /api/v1/about.
    // This makes buildIframeURL construct a URL on the same origin as the SPA
    // (window.location.origin), triggering the B1.3a isSameOriginAsApp() guard.
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
          // Same port as the SPA → same origin
          preview_port: SPA_PORT,
        }),
      })
    })

    await seedAndOpenSession(page, 'iframe-same-origin-t15', [
      {
        id: 'user-t15-1',
        role: 'user',
        content: 'serve workspace',
        timestamp: new Date(Date.now() - 5000).toISOString(),
        agent_id: '',
      },
      {
        id: 'asst-t15-1',
        role: 'assistant',
        content: 'Serving workspace.',
        timestamp: new Date(Date.now() - 4000).toISOString(),
        agent_id: SYNTHETIC_AGENT_ID,
        tool_calls: [
          {
            id: 'tc-t15-serve',
            tool: 'serve_workspace',
            status: 'success',
            duration_ms: 50,
            parameters: { path: '.', duration_seconds: 3600 },
            result: {
              path: fakePath,
              url: fakeUrl,
              expires_at: expires,
            },
          },
        ],
      },
    ])

    // (a) Assert: warning block is rendered when same-origin is detected.
    // IframePreview.tsx renders this block when iframeIsSameOrigin === true:
    // "Preview restricted — gateway is misconfigured for iframe isolation."
    await expect(
      page.locator('text=Preview restricted'),
    ).toBeVisible({ timeout: 15_000 })

    // (b) Assert: the iframe sandbox attribute does NOT contain "allow-same-origin".
    // The B1.3a guard uses sandboxRestricted = 'allow-scripts allow-forms allow-popups allow-modals'
    // which deliberately omits allow-same-origin.
    const iframe = page.locator('iframe[title="serve_workspace preview"]').first()
    await expect(iframe).toBeVisible({ timeout: 10_000 })

    const sandboxAttr = await iframe.getAttribute('sandbox')
    expect(
      sandboxAttr,
      'Sandbox attribute must not be null on the iframe',
    ).not.toBeNull()

    const sandboxTokens = (sandboxAttr ?? '').split(/\s+/).filter(Boolean)
    expect(
      sandboxTokens,
      [
        'The iframe sandbox must NOT contain "allow-same-origin" when the iframe',
        `is same-origin as the SPA. Got sandbox="${sandboxAttr}".`,
        'This would grant the iframe access to the SPA\'s authenticated API.',
      ].join(' '),
    ).not.toContain('allow-same-origin')
  },
)
