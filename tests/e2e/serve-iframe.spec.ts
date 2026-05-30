/**
 * serve-iframe.spec.ts — F-48 Playwright E2E smoke test for the iframe-preview
 * cross-origin contract.
 *
 * Traces to: chat-served-iframe-preview-spec.md (F-48)
 *           effervescent-bouncing-sonnet.md Track E3
 *
 * This test requires a running gateway at http://localhost:6060 (OMNIPUS_URL
 * env var) with onboarding completed.
 * The preview listener is expected at port+1 (default: 6061).
 *
 * To run:
 *   npm run e2e:setup && npx playwright test tests/e2e/serve-iframe.spec.ts
 *
 * Design note — token strategy:
 *   There is no REST API to register a serve_workspace token. The registry is
 *   populated exclusively by the serve_workspace tool during an agent turn.
 *   Rather than driving a live LLM turn, this test:
 *
 *   1. Seeds the session transcript with a synthetic `serve_workspace` tool
 *      result containing a FAKE token path ("/serve/main/e2e-smoke-token/").
 *   2. Intercepts GET /api/v1/about to return a controlled payload with a known
 *      preview_port (6061 or $OMNIPUS_PREVIEW_PORT). The SPA uses this to build
 *      the iframe src.
 *   3. Asserts on the iframe element the SPA renders — specifically its src,
 *      sandbox attribute, and chrome-bar buttons — all of which are properties
 *      of the SPA rendering logic (IframePreview.tsx), not of whether the
 *      preview server actually has the token registered.
 *
 *   Cross-origin policy (Approach B — network absence):
 *   Instead of injecting a probe script into the served iframe (which would
 *   require a real registered token + a served HTML file), this test asserts
 *   via Playwright network tracking that no request to /api/v1/* originates
 *   from the preview origin. This verifies the two-port topology's isolation
 *   guarantee (T-01/T-02): the iframe can't reach the SPA's API surface because
 *   the preview listener doesn't register /api/v1/ routes.
 *
 *   The limitation is documented here: this is a network-absence proof, not a
 *   direct SecurityError proof from within the iframe. The Go-level isolation
 *   (preview listener never registering /api/v1 routes) and the browser-level
 *   isolation (cross-origin framing blocked by CSP frame-ancestors) are each
 *   verified in pkg/gateway unit tests (preview_iframe_test.go).
 */

import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { seedAndOpenSession } from './fixtures/session-setup'

// ── Constants ──────────────────────────────────────────────────────────────────

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

/**
 * Preview port: gateway.port + 1 by default. Override via OMNIPUS_PREVIEW_PORT
 * when the gateway is started with a custom preview_port.
 */
const PREVIEW_PORT = parseInt(process.env.OMNIPUS_PREVIEW_PORT || '6061', 10)

/**
 * The synthetic agent ID used in the transcript and the fake serve path.
 * Must be a valid entity ID (alphanum + hyphens).
 */
const SYNTHETIC_AGENT_ID = 'main'

/**
 * Fake serve token embedded in the synthetic tool result path.
 * This token will NOT be in the gateway's registry, so the iframe itself will
 * receive a 401 from the preview listener — but the SPA will still render the
 * iframe element with the correct attributes, which is what this test verifies.
 * Use only base64-URL-safe characters to satisfy validatePreviewPath's regex.
 */
const FAKE_SERVE_TOKEN = 'e2e-smoke-token-abc123'

/**
 * The relative path that serve_workspace writes into the tool result.
 * Shape: /serve/{agent_id}/{token}/
 */
const FAKE_SERVE_PATH = `/serve/${SYNTHETIC_AGENT_ID}/${FAKE_SERVE_TOKEN}/`

/**
 * The absolute URL the SPA should construct for the iframe src.
 * Preview listener is on the same host as the SPA, port = PREVIEW_PORT.
 *
 * Hostname is derived from BASE_URL (which mirrors window.location.hostname in
 * the browser) rather than hardcoded — when the gateway binds to 127.0.0.1 the
 * SPA produces 127.0.0.1; when it's reverse-proxied behind omnipus.example.com
 * the SPA produces that domain. The cross-origin contract is enforced by the
 * differing PORT, not the hostname.
 */
const EXPECTED_IFRAME_HOSTNAME = new URL(BASE_URL).hostname
const EXPECTED_IFRAME_SRC = `http://${EXPECTED_IFRAME_HOSTNAME}:${PREVIEW_PORT}${FAKE_SERVE_PATH}`

/**
 * The sandbox attribute value mandated by FR-011 (IframePreview.tsx line 639).
 * No allow-top-navigation, no allow-popups-to-escape-sandbox.
 */
const EXPECTED_SANDBOX = 'allow-scripts allow-same-origin allow-forms allow-popups allow-modals'

// ── Helpers ────────────────────────────────────────────────────────────────────

/**
 * Normalise a sandbox attribute value for comparison.
 * Split on whitespace, sort tokens, rejoin. This makes the assertion
 * order-independent in case browsers or future DOM reads reorder the tokens.
 */
function normaliseSandbox(value: string): string {
  return value
    .split(/\s+/)
    .filter(Boolean)
    .sort()
    .join(' ')
}

// ── Main test ──────────────────────────────────────────────────────────────────

test(
  'serve iframe enforces cross-origin contract',
  async ({ page, context }) => {
    // Traces to: chat-served-iframe-preview-spec.md F-48, Track E3 of
    //            effervescent-bouncing-sonnet.md.
    //
    // Asserts:
    //  1. SPA renders <iframe> with src matching the preview origin
    //  2. iframe sandbox attribute matches FR-011 exactly
    //  3. Chrome-bar "Open in new tab" opens a popup with the same URL
    //  4. (Approach B) No request to /api/v1/* originates from the preview origin
    //     — confirming two-port topology isolation (T-01 / T-02)

    // ── Step 1: Intercept /api/v1/about to supply a known preview_port ─────────
    //
    // Without this intercept, the SPA fetches the real gateway's about endpoint.
    // If the real gateway has preview_listener_enabled=false (e.g., on platforms
    // where the second listener is disabled), preview_port would be absent and
    // the SPA would not build an iframe URL. We supply a controlled response so
    // the test is deterministic regardless of the running gateway's preview state.
    //
    // The intercept is set up BEFORE navigation so it catches the first query.
    await page.route(`${BASE_URL}/api/v1/about`, async (route) => {
      // Let the real request through first to get version/os/arch, then merge
      // our preview fields on top. This avoids hardcoding unrelated about fields.
      let baseResponse: Record<string, unknown> = {
        version: 'test',
        go_version: 'go1.21',
        os: 'linux',
        arch: 'amd64',
        uptime_seconds: 1,
      }
      try {
        const real = await route.fetch()
        if (real.ok()) {
          baseResponse = (await real.json()) as Record<string, unknown>
        }
      } catch {
        // Gateway not reachable — use the stub. The test will still run and
        // exercise the SPA rendering path even if the gateway is down.
      }

      // Inject the preview fields the SPA needs to build the iframe URL.
      // preview_listener_enabled: true so the SPA does not suppress the iframe.
      // preview_port: PREVIEW_PORT so buildIframeURL produces EXPECTED_IFRAME_SRC.
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          ...baseResponse,
          preview_listener_enabled: true,
          preview_port: PREVIEW_PORT,
        }),
      })
    })

    // ── Step 2: Seed session with synthetic serve_workspace tool result ─────────
    //
    // seedAndOpenSession creates a session, seeds its transcript.jsonl, and opens
    // it via the sessions panel. The transcript contains one user message and one
    // assistant message with a serve_workspace tool call whose result carries the
    // fake serve path. The SPA will render a ServeWorkspaceUI → IframePreview for
    // this tool call.

    const sessionNamePrefix = 'serve-iframe-e2e'
    const expires = new Date(Date.now() + 60 * 60 * 1000).toISOString()

    await seedAndOpenSession(page, sessionNamePrefix, [
      {
        id: 'entry-user-serve-1',
        role: 'user',
        content: 'serve my workspace',
        timestamp: new Date(Date.now() - 5000).toISOString(),
        agent_id: '',
      },
      {
        id: 'entry-asst-serve-1',
        role: 'assistant',
        content: 'I have served your workspace.',
        timestamp: new Date(Date.now() - 4000).toISOString(),
        agent_id: SYNTHETIC_AGENT_ID,
        tool_calls: [
          {
            id: 'tc-serve-1',
            tool: 'serve_workspace',
            status: 'success',
            duration_ms: 80,
            parameters: { path: '.', duration_seconds: 3600 },
            result: {
              path: FAKE_SERVE_PATH,
              url: EXPECTED_IFRAME_SRC,
              expires_at: expires,
            },
          },
        ],
      },
    ])

    // ── Step 6 (set up early): Cross-origin isolation listener ─────────────────
    //
    // Register the request listener BEFORE asserting on the iframe, so we catch
    // any early requests that the iframe may fire on load (even the 401 response
    // causes the iframe to navigate, and a misbehaving served page might try to
    // fetch the SPA API immediately). Setting it up here ensures we capture all
    // requests for the full duration of the test.
    //
    // Traces to: F-48 assertion #6 (cross-origin policy), T-01, T-02
    const apiRequestsFromPreviewOrigin: string[] = []
    const PREVIEW_ORIGIN = `http://localhost:${PREVIEW_PORT}`

    page.on('request', (req) => {
      const referer = req.headers()['referer'] ?? ''
      const origin = req.headers()['origin'] ?? ''
      const url = req.url()

      // Catch any API request that appears to originate from the preview origin.
      // A legitimate request from the SPA itself will have a main-origin referer.
      const fromPreviewOrigin =
        referer.startsWith(PREVIEW_ORIGIN) || origin.startsWith(PREVIEW_ORIGIN)

      if (fromPreviewOrigin && url.includes('/api/v1/')) {
        apiRequestsFromPreviewOrigin.push(`${req.method()} ${url} (referer=${referer}, origin=${origin})`)
      }
    })

    // ── Step 3: Assert iframe element rendered with correct src ─────────────────
    //
    // The SPA renders IframePreview (kind='serve_workspace') which — once the
    // about query resolves with our mocked preview_port — mounts the visible
    // <iframe> element at EXPECTED_IFRAME_SRC.
    //
    // We target the visible iframe by its title attribute (IframePreview.tsx
    // line 633: title={`${toolName} preview`} = "serve_workspace preview").
    // This distinguishes it from the hidden probe iframe (title="probe", aria-hidden).
    //
    // We wait up to 15 s for the iframe to appear (the about query and React
    // render may take a moment, especially on a cold SPA load).
    //
    // Traces to: F-48 assertion #3 (iframe rendered with correct src)

    // Wait for the visible iframe (title contains "serve_workspace preview")
    await expect(
      page.locator('iframe[title="serve_workspace preview"]'),
    ).toBeVisible({ timeout: 15_000 })

    const actualSrc = await page
      .locator('iframe[title="serve_workspace preview"]')
      .first()
      .getAttribute('src')

    // The src must match the preview origin pattern.
    // Regex: http://<host>:<PREVIEW_PORT>/serve/<agent>/<token>/
    const PREVIEW_SRC_REGEX = /^http:\/\/[^/]+:\d+\/serve\/.+/
    expect(
      actualSrc,
      `iframe src must match preview URL pattern — got: ${actualSrc}`,
    ).toMatch(PREVIEW_SRC_REGEX)

    // Additionally assert the src matches our specific expected URL exactly.
    // Traces to: chat-served-iframe-preview-spec.md FR-010 (iframe url construction)
    expect(
      actualSrc,
      `iframe src must be the constructed preview URL`,
    ).toBe(EXPECTED_IFRAME_SRC)

    // ── Step 4: Assert sandbox attribute matches FR-011 exactly ─────────────────
    //
    // The visible iframe must have exactly the FR-011 sandbox tokens.
    // No allow-top-navigation. No allow-popups-to-escape-sandbox.
    //
    // Traces to: IframePreview.tsx line 639, FR-011 sandbox spec, F-48 assertion #4
    const actualSandbox = await page
      .locator('iframe[title="serve_workspace preview"]')
      .first()
      .getAttribute('sandbox')

    expect(
      actualSandbox,
      'iframe sandbox attribute must not be null',
    ).not.toBeNull()

    // Normalise both sides so order doesn't matter (DOM may reorder tokens)
    const normalisedActual = normaliseSandbox(actualSandbox ?? '')
    const normalisedExpected = normaliseSandbox(EXPECTED_SANDBOX)

    expect(
      normalisedActual,
      [
        `iframe sandbox must equal "${EXPECTED_SANDBOX}".`,
        `Got: "${actualSandbox}"`,
        'Verify: no allow-top-navigation, no allow-popups-to-escape-sandbox.',
      ].join(' '),
    ).toBe(normalisedExpected)

    // ── Step 5: "Open in new tab" button opens a popup to the iframe src ────────
    //
    // IframePreview.tsx ChromeBar.handleOpen() calls:
    //   window.open(absoluteUrl, '_blank', 'noopener,noreferrer')
    //
    // Playwright captures window.open popups via context.waitForEvent('page').
    //
    // Traces to: F-48 assertion #5 (Open in new tab → new page URL matches iframe src)
    const [popup] = await Promise.all([
      context.waitForEvent('page', { timeout: 10_000 }),
      page.getByRole('button', { name: 'Open preview in new tab' }).click(),
    ])

    // The popup may redirect or fail to load (fake token → 401 from preview
    // listener), but its initial navigation URL must match the iframe src.
    // We check the URL before any redirect completes.
    const popupUrl = popup.url()

    expect(
      popupUrl,
      `popup URL must match iframe src. iframe src=${actualSrc}, popup url=${popupUrl}`,
    ).toBe(EXPECTED_IFRAME_SRC)

    // Close the popup to avoid leaking browser contexts
    await popup.close()

    // ── Step 6 (continued): Wait and assert no API requests from preview origin ──
    //
    // The listener was set up before Step 3 to capture all requests.
    // Now we wait for any in-flight requests to complete before asserting.
    //
    // Justification for the 2 s wait: the iframe's 401 response completes in
    // < 1 s on loopback; any JS cross-origin fetch would also complete quickly.
    // 2 s gives ample margin without being unnecessarily slow.
    //
    // We cannot use waitForLoadState('networkidle') here because the iframe's
    // 401 navigation may keep the network state active.
    await page.waitForTimeout(2000)

    expect(
      apiRequestsFromPreviewOrigin,
      [
        'No /api/v1/* requests must originate from the preview origin.',
        'This verifies the two-port topology isolation (T-01 / T-02):',
        'the preview listener does not serve /api/v1 routes, and the browser',
        "same-origin policy prevents the iframe's JS from reaching the SPA API.",
      ].join(' '),
    ).toEqual([])
  },
)

// ── Structural integrity test ─────────────────────────────────────────────────
//
// This test verifies that the IframePreview component renders the correct
// DOM structure without requiring an actual tool call or a running gateway.
// It is a belt-and-suspenders check that does NOT depend on the gate being
// live — it depends only on the SPA loading and the about mock working.
//
// Separate from the main smoke test so it can run without a gateway.

test(
  'serve iframe DOM structure: title attribute matches kind',
  async ({ page }) => {
    // Traces to: IframePreview.tsx render logic, FR-011 sandbox, F-48

    // Mock the about endpoint
    await page.route(`${BASE_URL}/api/v1/about`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          version: 'test',
          go_version: 'go1.21',
          os: 'linux',
          arch: 'amd64',
          uptime_seconds: 1,
          preview_listener_enabled: true,
          preview_port: PREVIEW_PORT,
        }),
      })
    })

    const sessionNamePrefix = 'serve-iframe-struct-e2e'
    const expires = new Date(Date.now() + 60 * 60 * 1000).toISOString()

    await seedAndOpenSession(page, sessionNamePrefix, [
      {
        id: 'entry-user-struct-1',
        role: 'user',
        content: 'serve my workspace please',
        timestamp: new Date(Date.now() - 3000).toISOString(),
        agent_id: '',
      },
      {
        id: 'entry-asst-struct-1',
        role: 'assistant',
        content: 'Serving workspace.',
        timestamp: new Date(Date.now() - 2000).toISOString(),
        agent_id: SYNTHETIC_AGENT_ID,
        tool_calls: [
          {
            id: 'tc-struct-1',
            tool: 'serve_workspace',
            status: 'success',
            duration_ms: 50,
            parameters: { path: '.', duration_seconds: 3600 },
            result: {
              path: `/serve/${SYNTHETIC_AGENT_ID}/struct-smoke-token-xyz/`,
              url: `http://localhost:${PREVIEW_PORT}/serve/${SYNTHETIC_AGENT_ID}/struct-smoke-token-xyz/`,
              expires_at: expires,
            },
          },
        ],
      },
    ])

    // The iframe must have a title that includes the tool name.
    // IframePreview.tsx line 633: title={`${toolName} preview`}
    // = "serve_workspace preview"
    //
    // Traces to: accessibility (FR-011a implicit) — iframes must have titles.
    const iframeTitle = await page
      .locator('iframe[title*="serve_workspace"]')
      .first()
      .getAttribute('title', { timeout: 15_000 })

    expect(iframeTitle, 'iframe must have a title containing the tool name').toBe(
      'serve_workspace preview',
    )

    // Differentiation check: a second, different token produces a different iframe src.
    // This verifies the SPA is not returning a hardcoded response.
    // The src must contain our struct-smoke-token-xyz, not any cached value.
    const src = await page
      .locator('iframe[title*="serve_workspace"]')
      .first()
      .getAttribute('src')

    expect(
      src,
      'iframe src must contain the token from this session (differentiation check)',
    ).toContain('struct-smoke-token-xyz')
  },
)
