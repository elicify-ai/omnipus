/**
 * web-serve-canonical.spec.ts — T1.2 + T1.3
 *
 * T1.2: legacy_serve_workspace_replay_renders_in_new_iframe
 *   Seeds a legacy serve_workspace transcript, opens the session, asserts:
 *   - A preview LINK is rendered (via the unified IframePreview + WebServeBlock path)
 *   - The link's href contains the token from the legacy `/serve/` path.
 *   Note: The legacy serve_workspace result still uses the /serve/ path prefix in the
 *   transcript data; the SPA wires it through IframePreview and resolvePreviewHref —
 *   so the test asserts the preview link is rendered at all (not blank/crashed UI),
 *   not the specific path form.
 *
 * T1.3: legacy_run_in_workspace_warmup_replay
 *   Seeds a legacy run_in_workspace transcript (dev-mode, kind inferred from
 *   command + port fields). Asserts:
 *   - A warmup placeholder (or timeout error, or ready link) is rendered — the
 *     warmup state machine started.
 *   - No JS crash / blank UI.
 *
 * ADR-044 (preview-on-main-listener) update: IframePreview.tsx no longer mounts
 * an embedded `<iframe>` for this surface (D6) — `/preview/` (and the legacy
 * `/serve/`/`/dev/` replay prefixes) now share the SPA's own gateway origin,
 * so previews render as a plain clickable link (`data-testid="preview-link"`
 * inside `data-testid="preview-link-block"`). Both tests below were rewritten
 * from asserting an `<iframe>` element to asserting the link element and its
 * `href`. `/api/v1/about` mocks were updated to the current AboutResponse
 * contract (`preview_enabled` replaces the retired `preview_port` /
 * `preview_listener_enabled` fields, ADR-044 US-8) and the mocked result URLs
 * now point at the SAME origin as `BASE_URL` — there is no separate preview
 * listener/port to construct a URL against anymore.
 *
 * Both tests drive against the real embedded SPA (Go binary + Playwright).
 *
 * Note: the preview handler is not required to actually serve the content; we
 * are testing SPA rendering behavior, not end-to-end preview content delivery.
 */

import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { seedAndOpenSession } from './fixtures/session-setup'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'
const SYNTHETIC_AGENT_ID = 'main'

// ── Shared /api/v1/about mock ─────────────────────────────────────────────────

async function mockAbout(page: import('@playwright/test').Page, extra?: Record<string, unknown>) {
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
      // Gateway not reachable — stub is sufficient for SPA render path.
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        ...base,
        preview_enabled: true,
        ...extra,
      }),
    })
  })
}

// ── T1.2: legacy serve_workspace replay ───────────────────────────────────────

test(
  'legacy_serve_workspace_replay_renders_preview_link',
  async ({ page }) => {
    // preview_enabled replaces the retired preview_port/preview_listener_enabled
    // fields (ADR-044 US-8) — IframePreview no longer derives the preview
    // origin from /api/v1/about at all (resolvePreviewHref uses the tool
    // result's own url/path against window.location.origin), so this mock is
    // belt-and-suspenders schema realism, not load-bearing for the URL itself.
    await mockAbout(page)

    const fakeToken = 'serve-workspace-legacy-token-t12'
    const fakePath = `/serve/${SYNTHETIC_AGENT_ID}/${fakeToken}/`
    // ADR-044: same origin as the SPA — no separate preview listener/port.
    const fakeUrl = `${BASE_URL}${fakePath}`
    const expires = new Date(Date.now() + 3600 * 1000).toISOString()

    // Seed a legacy serve_workspace transcript (no `kind` field, no `command`/`port`).
    // The SPA's inferKind() will classify this as 'static' based on the absence of
    // command+port, routing to ServeWorkspaceUI → IframePreview(kind='serve_workspace').
    await seedAndOpenSession(page, 'web-serve-canonical-t12', [
      {
        id: 'user-t12-1',
        role: 'user',
        content: 'serve my workspace',
        timestamp: new Date(Date.now() - 5000).toISOString(),
        agent_id: '',
      },
      {
        id: 'asst-t12-1',
        role: 'assistant',
        content: 'I have served your workspace.',
        timestamp: new Date(Date.now() - 4000).toISOString(),
        agent_id: SYNTHETIC_AGENT_ID,
        tool_calls: [
          {
            id: 'tc-t12-serve',
            // Legacy tool name — the SPA registers ServeWorkspaceUI under serve_workspace.
            tool: 'serve_workspace',
            status: 'success',
            duration_ms: 80,
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

    // Assert: the preview link block is rendered — the SPA must not crash or
    // render a blank UI. data-testid="preview-link-block" / "preview-link"
    // are set by IframePreview.tsx's ready-state render (no warmup for
    // static/serve_workspace mode, so this renders immediately).
    const previewLink = page.locator('[data-testid="preview-link"]')
    await expect(
      previewLink,
      'IframePreview must render a preview link for the legacy serve_workspace transcript',
    ).toBeVisible({ timeout: 15_000 })

    // Assert: the link's href is the expected preview URL (not blank or an error href).
    const href = await previewLink.getAttribute('href')
    expect(
      href,
      `preview link href must be non-null and contain the preview token. Got: ${href}`,
    ).not.toBeNull()
    expect(
      href,
      'preview link href must contain the expected preview token in the URL path',
    ).toContain(fakeToken)

    // Differentiation check: the href must be the SAME origin as the SPA
    // itself — proving IframePreview builds it from the current page's own
    // origin (ADR-044), not a hardcoded or stale separate-port value.
    expect(new URL(href!).origin).toBe(new URL(BASE_URL).origin)

    // ADR-044 D6: no embedded <iframe> anywhere — link-only rendering.
    await expect(page.locator('iframe')).toHaveCount(0)

    // Assert: no malformed-result error block rendered (would appear if isWebServeResult rejected).
    // The MalformedResultBlock renders: "web_serve tool returned a malformed result"
    await expect(
      page.locator('text=tool returned a malformed result'),
    ).not.toBeVisible()
  },
)

// ── T1.3: legacy run_in_workspace warmup replay ───────────────────────────────

test(
  'legacy_run_in_workspace_warmup_replay',
  async ({ page }) => {
    await mockAbout(page, { warmup_timeout_seconds: 6 })

    const devToken = 'run-in-workspace-legacy-token-t13'
    const devPath = `/serve/${SYNTHETIC_AGENT_ID}/${devToken}/`
    // ADR-044: same origin as the SPA — no separate preview listener/port.
    const devUrl = `${BASE_URL}${devPath}`
    const expires = new Date(Date.now() + 3600 * 1000).toISOString()

    // Seed a legacy run_in_workspace result — has command + port fields.
    // inferKind() classifies this as 'dev', routing to IframePreview(kind='run_in_workspace').
    // The warmup state machine will start, probing the fake URL (which will fail
    // since no real dev server is there), but the warmup placeholder must render.
    await seedAndOpenSession(page, 'web-serve-canonical-t13', [
      {
        id: 'user-t13-1',
        role: 'user',
        content: 'start the dev server',
        timestamp: new Date(Date.now() - 5000).toISOString(),
        agent_id: '',
      },
      {
        id: 'asst-t13-1',
        role: 'assistant',
        content: 'Dev server started.',
        timestamp: new Date(Date.now() - 4000).toISOString(),
        agent_id: SYNTHETIC_AGENT_ID,
        tool_calls: [
          {
            id: 'tc-t13-run',
            // Legacy tool name
            tool: 'run_in_workspace',
            status: 'success',
            duration_ms: 200,
            parameters: { command: 'vite dev', port: 5173 },
            result: {
              // No `kind` field — legacy
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

    // Assert: the warmup state machine drives the placeholder — either:
    //   (a) "Starting dev server…" placeholder is visible, OR
    //   (b) Warmup timeout error block (if all probes failed before the test
    //       assertion runs — a real, unmocked same-origin fetch against
    //       devUrl will actually 404 quickly since no dev server exists).
    // Any of these is acceptable — the key regression is a blank/crashed UI.
    //
    // We give the component 15 s to render any of these states.
    const hasWarmupPlaceholder = page.locator('text=Starting dev server')
    const hasWarmupError = page.locator('text=Dev server did not respond in time')

    await expect(
      hasWarmupPlaceholder.or(hasWarmupError).first(),
    ).toBeVisible({ timeout: 15_000 })

    // Assert: no malformed-result error block
    await expect(
      page.locator('text=tool returned a malformed result'),
    ).not.toBeVisible()

    // ADR-044 D6: no embedded <iframe> anywhere — link-only / warmup-only rendering.
    await expect(page.locator('iframe')).toHaveCount(0)
  },
)
